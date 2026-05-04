package researchadapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
	"golang.org/x/net/websocket"
)

func TestConfigFromEnvAndValidation(t *testing.T) {
	t.Setenv("PIGO_RESEARCH_TOOLS", "search,scrape")
	t.Setenv("PIGO_SEARXNG_URL", "http://search.test/")
	t.Setenv("PIGO_OBSCURA_URL", "http://obscura.test/")
	t.Setenv("PIGO_NVD_API_KEY", "nvd-key")
	config := ConfigFromEnv().Normalized()
	if len(config.Tools) != 2 || config.Tools[0] != "search" || config.SearXNGURL != "http://search.test" || config.ObscuraURL != "http://obscura.test" || config.NVDAPIKey != "nvd-key" {
		t.Fatalf("config = %#v", config)
	}
	if config.Metadata()["nvdApiKey"] != true {
		t.Fatalf("metadata = %#v", config.Metadata())
	}
	if err := (Config{Tools: []string{"unknown"}}).Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSearchToolUsesSearXNG(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" || r.URL.Query().Get("q") != "pigo" {
			t.Fatalf("request = %s", r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
			"title": "Pigo", "url": "https://example.test/pigo", "content": "snippet", "engine": "test",
		}}})
	}))
	defer server.Close()

	tools, specs := Tools(Config{Tools: []string{"search"}, SearXNGURL: server.URL})
	if len(tools) != 1 || len(specs) != 1 || tools[0].Name != "search" {
		t.Fatalf("tools = %#v specs = %#v", tools, specs)
	}
	result := tools[0].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{"query": "pigo"}})
	if result.IsError || !strings.Contains(result.Text, "https://example.test/pigo") {
		t.Fatalf("result = %#v", result)
	}
}

func TestSearchToolRequiresSearXNGURL(t *testing.T) {
	tools, _ := Tools(Config{Tools: []string{"search"}})
	result := tools[0].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{"query": "pigo"}})
	if !result.IsError || !strings.Contains(result.Text, "SEARXNG_URL") {
		t.Fatalf("result = %#v", result)
	}
}

func TestScrapeToolExtractsHTMLText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Doc</title></head><body><h1>Hello</h1><p>World</p><script>ignore</script></body></html>`))
	}))
	defer server.Close()

	tools, _ := Tools(Config{Tools: []string{"scrape"}})
	result := tools[0].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{"url": server.URL}})
	if result.IsError || !strings.Contains(result.Text, "Hello") || strings.Contains(result.Text, "ignore") {
		t.Fatalf("result = %#v", result)
	}
}

func TestScrapeToolRequiresObscuraURL(t *testing.T) {
	tools, _ := Tools(Config{Tools: []string{"scrape"}})
	result := tools[0].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{"url": "https://example.test", "engine": "obscura"}})
	if !result.IsError || !strings.Contains(result.Text, "OBSCURA_URL") {
		t.Fatalf("result = %#v", result)
	}
}

func TestScrapeToolRejectsUnknownEngine(t *testing.T) {
	tools, _ := Tools(Config{Tools: []string{"scrape"}})
	result := tools[0].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{"url": "https://example.test", "engine": "unknown"}})
	if !result.IsError || !strings.Contains(result.Text, "unsupported scrape engine") {
		t.Fatalf("result = %#v", result)
	}
}

func TestScrapeToolUsesObscuraCDP(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		for {
			var request map[string]any
			if err := websocket.JSON.Receive(conn, &request); err != nil {
				return
			}
			id := request["id"]
			method, _ := request["method"].(string)
			response := map[string]any{"id": id, "result": map[string]any{}}
			switch method {
			case "Target.createTarget":
				response["result"] = map[string]any{"targetId": "target-1"}
			case "Target.attachToTarget":
				response["result"] = map[string]any{"sessionId": "session-1"}
			case "Runtime.evaluate":
				params, _ := request["params"].(map[string]any)
				expression, _ := params["expression"].(string)
				value := "complete"
				if strings.Contains(expression, "document.title") {
					value = "Rendered Doc"
				}
				if strings.Contains(expression, "innerText") {
					value = "Rendered body text"
				}
				response["result"] = map[string]any{"result": map[string]any{"value": value}}
			}
			if err := websocket.JSON.Send(conn, response); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	obscuraURL := "ws" + strings.TrimPrefix(server.URL, "http")

	tools, _ := Tools(Config{Tools: []string{"scrape"}, ObscuraURL: obscuraURL})
	result := tools[0].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{"url": "https://example.test/page", "render": true}})
	if result.IsError || !strings.Contains(result.Text, "Rendered body text") {
		t.Fatalf("result = %#v", result)
	}
	if result.Details["engine"] != "obscura" {
		t.Fatalf("details = %#v", result.Details)
	}
}

func TestSecuritySearchUsesOSVAndCISA(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(req.URL.Path, "/vulns/CVE-2024-1234"):
			return jsonResponse(map[string]any{"id": "GHSA-test", "aliases": []string{"CVE-2024-1234"}, "summary": "OSV summary"}), nil
		case strings.Contains(req.URL.Host, "services.nvd.nist.gov"):
			if req.Header.Get("apiKey") != "nvd-key" {
				t.Fatalf("missing nvd api key header")
			}
			if req.URL.Query().Get("cveId") != "CVE-2024-1234" {
				t.Fatalf("nvd query = %s", req.URL.String())
			}
			return jsonResponse(map[string]any{"vulnerabilities": []map[string]any{{
				"cve": map[string]any{
					"id":               "CVE-2024-1234",
					"sourceIdentifier": "nvd@test",
					"vulnStatus":       "Analyzed",
					"descriptions":     []map[string]any{{"lang": "en", "value": "NVD description"}},
					"metrics": map[string]any{"cvssMetricV31": []map[string]any{{
						"cvssData": map[string]any{"baseScore": 9.8, "baseSeverity": "CRITICAL"},
					}}},
				},
			}}}), nil
		case strings.Contains(req.URL.Host, "cisa.gov"):
			return jsonResponse(map[string]any{"vulnerabilities": []map[string]any{{
				"cveID": "CVE-2024-1234", "vendorProject": "Example", "product": "App", "vulnerabilityName": "Example vuln",
			}}}), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL.String())
			return nil, nil
		}
	})}
	tools, _ := Tools(Config{Tools: []string{"security_search"}, NVDAPIKey: "nvd-key", HTTPClient: client})
	result := tools[0].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{"query": "CVE-2024-1234"}})
	if result.IsError || !strings.Contains(result.Text, "GHSA-test") || !strings.Contains(result.Text, "NVD") || !strings.Contains(result.Text, "CISA") {
		t.Fatalf("result = %#v", result)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(value any) *http.Response {
	var builder strings.Builder
	_ = json.NewEncoder(&builder).Encode(value)
	return &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       ioNopCloser{strings.NewReader(builder.String())},
	}
}

type ioNopCloser struct {
	*strings.Reader
}

func (c ioNopCloser) Close() error { return nil }
