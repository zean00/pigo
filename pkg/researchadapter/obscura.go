package researchadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

type obscuraClient struct {
	conn      *websocket.Conn
	nextID    int
	sessionID string
	timeout   time.Duration
}

func scrapeOneObscura(ctx context.Context, config Config, rawURL string, options scrapeOptions) scrapeResult {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return scrapeResult{URL: rawURL, Engine: "obscura", Error: "invalid URL"}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return scrapeResult{URL: rawURL, Engine: "obscura", Error: "unsupported URL scheme"}
	}
	timeout := time.Duration(normalizeTimeoutSeconds(options.TimeoutSeconds)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	wsURL, err := obscuraWebSocketURL(ctx, config)
	if err != nil {
		return scrapeResult{URL: rawURL, Engine: "obscura", Error: err.Error()}
	}
	client, err := newObscuraClient(wsURL, timeout)
	if err != nil {
		return scrapeResult{URL: rawURL, Engine: "obscura", Error: err.Error()}
	}
	defer client.close()
	result, err := client.scrape(ctx, parsed.String(), options)
	if err != nil {
		return scrapeResult{URL: parsed.String(), Engine: "obscura", Error: err.Error()}
	}
	result.Engine = "obscura"
	return result
}

func obscuraWebSocketURL(ctx context.Context, config Config) (string, error) {
	base := strings.TrimSpace(config.ObscuraURL)
	if base == "" {
		return "", fmt.Errorf("missing obscura url")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "ws", "wss":
		if parsed.Path == "" || parsed.Path == "/" {
			parsed.Path = "/devtools/browser"
		}
		return parsed.String(), nil
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported obscura url scheme")
	}
	versionURL := strings.TrimRight(base, "/") + "/json/version"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err == nil {
		if resp, err := config.client().Do(req); err == nil {
			defer resp.Body.Close()
			var payload struct {
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 && json.NewDecoder(resp.Body).Decode(&payload) == nil && strings.TrimSpace(payload.WebSocketDebuggerURL) != "" {
				return strings.TrimSpace(payload.WebSocketDebuggerURL), nil
			}
		}
	}
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	parsed.Path = "/devtools/browser"
	parsed.RawQuery = ""
	return parsed.String(), nil
}

func newObscuraClient(wsURL string, timeout time.Duration) (*obscuraClient, error) {
	origin := "http://localhost"
	conn, err := websocket.Dial(wsURL, "", origin)
	if err != nil {
		return nil, err
	}
	return &obscuraClient{conn: conn, timeout: timeout}, nil
}

func (c *obscuraClient) close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

func (c *obscuraClient) scrape(ctx context.Context, targetURL string, options scrapeOptions) (scrapeResult, error) {
	target, err := c.command(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		return scrapeResult{}, err
	}
	targetID, _ := target["targetId"].(string)
	if strings.TrimSpace(targetID) == "" {
		return scrapeResult{}, fmt.Errorf("obscura did not return targetId")
	}
	defer func() {
		_, _ = c.command(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": targetID})
	}()
	attached, err := c.command(ctx, "", "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true})
	if err != nil {
		return scrapeResult{}, err
	}
	c.sessionID, _ = attached["sessionId"].(string)
	if strings.TrimSpace(c.sessionID) == "" {
		return scrapeResult{}, fmt.Errorf("obscura did not return sessionId")
	}
	_, _ = c.command(ctx, c.sessionID, "Page.enable", nil)
	_, _ = c.command(ctx, c.sessionID, "Runtime.enable", nil)
	if _, err := c.command(ctx, c.sessionID, "Page.navigate", map[string]any{"url": targetURL}); err != nil {
		return scrapeResult{}, err
	}
	if err := c.waitReady(ctx, options.WaitUntil); err != nil {
		return scrapeResult{}, err
	}
	title, _ := c.evaluateString(ctx, `document.title || ""`)
	text, err := c.evaluateString(ctx, `document.body ? document.body.innerText : document.documentElement.innerText`)
	if err != nil {
		return scrapeResult{}, err
	}
	text, truncated := truncateText(text, maxScrapeOutputPerURL)
	return scrapeResult{
		URL:       targetURL,
		Title:     title,
		Text:      text,
		Bytes:     len(text),
		Truncated: truncated,
	}, nil
}

func (c *obscuraClient) waitReady(ctx context.Context, waitUntil string) error {
	want := "complete"
	if strings.EqualFold(strings.TrimSpace(waitUntil), "domcontentloaded") {
		want = "interactive"
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := c.evaluateString(ctx, `document.readyState`)
		if err == nil && (state == "complete" || want == "interactive" && state == "interactive") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *obscuraClient) evaluateString(ctx context.Context, expression string) (string, error) {
	result, err := c.command(ctx, c.sessionID, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	value, _ := result["result"].(map[string]any)
	return fmt.Sprint(value["value"]), nil
}

func (c *obscuraClient) command(ctx context.Context, sessionID, method string, params map[string]any) (map[string]any, error) {
	c.nextID++
	id := c.nextID
	request := map[string]any{"id": id, "method": method}
	if sessionID != "" {
		request["sessionId"] = sessionID
	}
	if params != nil {
		request["params"] = params
	}
	if err := c.conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, err
	}
	if err := websocket.JSON.Send(c.conn, request); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		var response map[string]any
		if err := websocket.JSON.Receive(c.conn, &response); err != nil {
			return nil, err
		}
		if intArg(response["id"], 0) != id {
			continue
		}
		if rawError, ok := response["error"]; ok {
			return nil, fmt.Errorf("obscura %s failed: %v", method, rawError)
		}
		result, _ := response["result"].(map[string]any)
		return result, nil
	}
}

func normalizeTimeoutSeconds(value int) int {
	if value <= 0 {
		return 30
	}
	if value > 120 {
		return 120
	}
	return value
}
