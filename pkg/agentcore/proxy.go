package agentcore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/badlogic/pigo/pkg/ai"
)

type ProxyStreamOptions struct {
	AuthToken       string
	ProxyURL        string
	Temperature     *float64
	MaxTokens       int
	CacheRetention  ai.CacheRetention
	SessionID       string
	Headers         map[string]string
	Metadata        map[string]any
	Transport       string
	ThinkingBudgets ai.ThinkingBudgets
	MaxRetryDelay   time.Duration
	HTTPClient      *http.Client
}

type ProxyAssistantMessageEvent struct {
	Type             string    `json:"type"`
	ContentIndex     int       `json:"contentIndex,omitempty"`
	ContentIdx       int       `json:"contentIdx,omitempty"`
	Delta            string    `json:"delta,omitempty"`
	ContentSignature string    `json:"contentSignature,omitempty"`
	ID               string    `json:"id,omitempty"`
	ToolName         string    `json:"toolName,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	ErrorMessage     string    `json:"errorMessage,omitempty"`
	Usage            *ai.Usage `json:"usage,omitempty"`
}

func StreamProxy(ctx context.Context, model ai.Model, contextMessages []ai.Message, options ProxyStreamOptions) *ai.EventStream {
	stream := ai.CreateEventStream()
	go func() {
		result, err := streamProxy(ctx, stream, model, contextMessages, options)
		stream.Close(result, err)
	}()
	return stream
}

func streamProxy(ctx context.Context, stream *ai.EventStream, model ai.Model, contextMessages []ai.Message, options ProxyStreamOptions) (ai.NormalizedResult, error) {
	if strings.TrimSpace(options.ProxyURL) == "" {
		return proxyErrorResult(ctx, model, "proxy URL is required"), fmt.Errorf("proxy URL is required")
	}
	if strings.TrimSpace(options.AuthToken) == "" {
		return proxyErrorResult(ctx, model, "proxy auth token is required"), fmt.Errorf("proxy auth token is required")
	}

	partial := ai.NormalizedResult{
		Role:       "assistant",
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		StopReason: "stop",
		Content:    []any{},
		Usage:      &ai.Usage{},
		Timestamp:  time.Now().UnixMilli(),
	}
	body, err := json.Marshal(map[string]any{
		"model":   model,
		"context": contextMessages,
		"options": proxySerializableOptions(options),
	})
	if err != nil {
		return proxyErrorResult(ctx, model, err.Error()), err
	}

	client := options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(options.ProxyURL, "/")+"/api/stream", bytes.NewReader(body))
	if err != nil {
		return proxyErrorResult(ctx, model, err.Error()), err
	}
	req.Header.Set("Authorization", "Bearer "+options.AuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		result := proxyErrorResult(ctx, model, err.Error())
		stream.Push(ai.NormalizedEvent{Type: "error", Reason: result.StopReason, ErrorMessage: result.ErrorMessage, Error: &result})
		return result, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("proxy error: %s", resp.Status)
		result := proxyErrorResult(ctx, model, err.Error())
		stream.Push(ai.NormalizedEvent{Type: "error", Reason: result.StopReason, ErrorMessage: result.ErrorMessage, Error: &result})
		return result, err
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if payload == "" {
			continue
		}
		var proxyEvent ProxyAssistantMessageEvent
		if err := json.Unmarshal([]byte(payload), &proxyEvent); err != nil {
			return proxyErrorResult(ctx, model, err.Error()), err
		}
		event, emit := processProxyEvent(proxyEvent, &partial)
		if emit {
			stream.Push(event)
		}
	}
	if err := scanner.Err(); err != nil {
		result := proxyErrorResult(ctx, model, err.Error())
		stream.Push(ai.NormalizedEvent{Type: "error", Reason: result.StopReason, ErrorMessage: result.ErrorMessage, Error: &result})
		return result, err
	}
	return partial, nil
}

func proxySerializableOptions(options ProxyStreamOptions) map[string]any {
	return map[string]any{
		"temperature":     options.Temperature,
		"maxTokens":       options.MaxTokens,
		"cacheRetention":  options.CacheRetention,
		"sessionId":       options.SessionID,
		"headers":         options.Headers,
		"metadata":        options.Metadata,
		"transport":       options.Transport,
		"thinkingBudgets": options.ThinkingBudgets,
		"maxRetryDelayMs": options.MaxRetryDelay.Milliseconds(),
	}
}

func processProxyEvent(proxyEvent ProxyAssistantMessageEvent, partial *ai.NormalizedResult) (ai.NormalizedEvent, bool) {
	contentIndex := proxyEvent.ContentIndex
	if contentIndex == 0 && proxyEvent.ContentIdx != 0 {
		contentIndex = proxyEvent.ContentIdx
	}
	switch proxyEvent.Type {
	case "start":
		return ai.NormalizedEvent{Type: "start", Partial: partial}, true
	case "text_start":
		setProxyContent(partial, contentIndex, map[string]any{"type": "text", "text": ""})
		return ai.NormalizedEvent{Type: "text_start", ContentIdx: contentIndex, Partial: partial}, true
	case "text_delta":
		block := proxyContentMap(partial, contentIndex, "text")
		block["text"] = asString(block["text"]) + proxyEvent.Delta
		partial.Text = proxyText(partial.Content)
		return ai.NormalizedEvent{Type: "text_delta", ContentIdx: contentIndex, Delta: proxyEvent.Delta, Partial: partial}, true
	case "text_end":
		block := proxyContentMap(partial, contentIndex, "text")
		block["textSignature"] = proxyEvent.ContentSignature
		partial.Text = proxyText(partial.Content)
		return ai.NormalizedEvent{Type: "text_end", ContentIdx: contentIndex, Content: asString(block["text"]), Partial: partial}, true
	case "thinking_start":
		setProxyContent(partial, contentIndex, map[string]any{"type": "thinking", "thinking": ""})
		return ai.NormalizedEvent{Type: "thinking_start", ContentIdx: contentIndex, Partial: partial}, true
	case "thinking_delta":
		block := proxyContentMap(partial, contentIndex, "thinking")
		block["thinking"] = asString(block["thinking"]) + proxyEvent.Delta
		return ai.NormalizedEvent{Type: "thinking_delta", ContentIdx: contentIndex, Delta: proxyEvent.Delta, Partial: partial}, true
	case "thinking_end":
		block := proxyContentMap(partial, contentIndex, "thinking")
		block["thinkingSignature"] = proxyEvent.ContentSignature
		return ai.NormalizedEvent{Type: "thinking_end", ContentIdx: contentIndex, Content: asString(block["thinking"]), Partial: partial}, true
	case "toolcall_start":
		setProxyContent(partial, contentIndex, map[string]any{"type": "toolCall", "id": proxyEvent.ID, "name": proxyEvent.ToolName, "arguments": map[string]any{}, "partialJson": ""})
		return ai.NormalizedEvent{Type: "toolcall_start", ContentIdx: contentIndex, Partial: partial}, true
	case "toolcall_delta":
		block := proxyContentMap(partial, contentIndex, "toolCall")
		partialJSON := asString(block["partialJson"]) + proxyEvent.Delta
		block["partialJson"] = partialJSON
		var args map[string]any
		if json.Unmarshal([]byte(partialJSON), &args) == nil {
			block["arguments"] = args
		}
		return ai.NormalizedEvent{Type: "toolcall_delta", ContentIdx: contentIndex, Delta: proxyEvent.Delta, Partial: partial}, true
	case "toolcall_end":
		block := proxyContentMap(partial, contentIndex, "toolCall")
		delete(block, "partialJson")
		tool := &ai.NormalizedTool{ID: asString(block["id"]), Name: asString(block["name"]), Arguments: mapStringAny(block["arguments"]), HasID: asString(block["id"]) != ""}
		return ai.NormalizedEvent{Type: "toolcall_end", ContentIdx: contentIndex, ToolCall: tool, Partial: partial}, true
	case "done":
		partial.StopReason = proxyEvent.Reason
		partial.Usage = proxyEvent.Usage
		partial.Text = proxyText(partial.Content)
		return ai.NormalizedEvent{Type: "done", Reason: proxyEvent.Reason, Message: partial}, true
	case "error":
		partial.StopReason = proxyEvent.Reason
		partial.ErrorMessage = proxyEvent.ErrorMessage
		partial.Usage = proxyEvent.Usage
		return ai.NormalizedEvent{Type: "error", Reason: proxyEvent.Reason, ErrorMessage: proxyEvent.ErrorMessage, Error: partial}, true
	default:
		return ai.NormalizedEvent{}, false
	}
}

func setProxyContent(partial *ai.NormalizedResult, index int, block map[string]any) {
	for len(partial.Content) <= index {
		partial.Content = append(partial.Content, nil)
	}
	partial.Content[index] = block
}

func proxyContentMap(partial *ai.NormalizedResult, index int, blockType string) map[string]any {
	for len(partial.Content) <= index {
		partial.Content = append(partial.Content, nil)
	}
	block, _ := partial.Content[index].(map[string]any)
	if block == nil {
		block = map[string]any{"type": blockType}
		partial.Content[index] = block
	}
	return block
}

func proxyErrorResult(ctx context.Context, model ai.Model, message string) ai.NormalizedResult {
	reason := "error"
	if ctx.Err() != nil {
		reason = "aborted"
	}
	return ai.NormalizedResult{
		Role:         "assistant",
		API:          model.API,
		Provider:     model.Provider,
		Model:        model.ID,
		StopReason:   reason,
		ErrorMessage: message,
		Content:      []any{},
		Usage:        &ai.Usage{},
		Timestamp:    time.Now().UnixMilli(),
	}
}

func asString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func mapStringAny(value any) map[string]any {
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return map[string]any{}
}

func proxyText(content []any) string {
	var builder strings.Builder
	for _, item := range content {
		block, ok := item.(map[string]any)
		if !ok || block["type"] != "text" {
			continue
		}
		builder.WriteString(asString(block["text"]))
	}
	return builder.String()
}
