package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIProviderReturnsToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %q", req.Method)
		}
		if req.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}
		if req.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "gpt-mini" {
			t.Fatalf("model = %v", payload["model"])
		}

		_, _ = fmt.Fprint(w, `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "I can do that.",
					"tool_calls": [{
						"id": "tc-1",
						"type": "function",
						"function": {
							"name": "math",
							"arguments": "{\"a\":15,\"b\":27}"
						}
					}]
				},
				"finish_reason": "tool_calls"
			}],
			"usage": {
				"prompt_tokens": 5,
				"completion_tokens": 20,
				"total_tokens": 25
			}
		}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, events, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Tools: []Tool{{
			Name:        "math",
			Description: "add numbers",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"a": map[string]any{"type": "number"},
					"b": map[string]any{"type": "number"},
				},
			},
		}},
		Options: ChatOptions{
			APIKey:    "test-key",
			Stream:    false,
			MaxTokens: 128,
		},
		Messages: []Message{
			{Role: "user", Content: "calculate"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "toolUse" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if len(events) == 0 || events[0].Type != "start" || events[len(events)-1].Type != "done" {
		t.Fatalf("events = %#v", events)
	}
	if len(result.Content) != 2 {
		t.Fatalf("content len = %d", len(result.Content))
	}
	if first, ok := result.Content[0].(map[string]any); !ok || first["type"] != "text" {
		t.Fatalf("content[0] = %#v", result.Content[0])
	}
}

func TestOpenAIProviderStreamingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var chunks bytes.Buffer
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\" world\"},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"tc-1\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"value\\\":\\\"ok\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n")
		chunks.WriteString("data: [DONE]\n")
		if _, err := w.Write(chunks.Bytes()); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, events, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Tools:    []Tool{{Name: "echo"}},
		Options: ChatOptions{
			APIKey: "test-key",
			Stream: true,
		},
		Messages: []Message{{Role: "user", Content: "please"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "toolUse" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if len(events) < 4 {
		t.Fatalf("events = %#v", events)
	}
	if result.Text != "hello world" {
		t.Fatalf("text = %q", result.Text)
	}
	if events[1].Type != "text_start" || events[2].Type != "text_delta" || events[2].Delta != "hello" {
		t.Fatalf("unexpected leading events = %#v", events[:3])
	}
	foundToolDelta := false
	for _, event := range events {
		if event.Type == "toolcall_delta" && strings.Contains(event.Delta, `"value":"ok"`) {
			foundToolDelta = true
			break
		}
	}
	if !foundToolDelta {
		t.Fatalf("missing toolcall_delta in %#v", events)
	}
}

func TestOpenAIProviderStreamingPreservesInterleavedParallelToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var chunks bytes.Buffer
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"tc-1\",\"function\":{\"name\":\"first\",\"arguments\":\"{\\\"a\\\":\"}}]},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"tc-2\",\"function\":{\"name\":\"second\",\"arguments\":\"{\\\"b\\\":\"}}]},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1}\"}}]},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\"2}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n")
		chunks.WriteString("data: [DONE]\n")
		_, _ = w.Write(chunks.Bytes())
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Tools:    []Tool{{Name: "first"}, {Name: "second"}},
		Options:  ChatOptions{APIKey: "test-key", Stream: true},
		Messages: []Message{{Role: "user", Content: "please"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks := ParseContentBlocks(result.Content)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[0].ID != "tc-1" || blocks[0].Name != "first" || blocks[0].Arguments["a"] != float64(1) {
		t.Fatalf("first tool call = %#v", blocks[0])
	}
	if blocks[1].ID != "tc-2" || blocks[1].Name != "second" || blocks[1].Arguments["b"] != float64(2) {
		t.Fatalf("second tool call = %#v", blocks[1])
	}
}

func TestOpenAIProviderStreamingParsesReasoningAndToolSignatures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var chunks bytes.Buffer
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think 1\"},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"answer\",\"tool_calls\":[{\"index\":0,\"id\":\"tc-1\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"value\\\":\"}}],\"reasoning_details\":[{\"type\":\"reasoning.encrypted\",\"id\":\"tc-1\",\"data\":\"encrypted\"}]},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"ok\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n")
		chunks.WriteString("data: [DONE]\n")
		_, _ = w.Write(chunks.Bytes())
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Tools:    []Tool{{Name: "echo"}},
		Options:  ChatOptions{APIKey: "test-key", Stream: true},
		Messages: []Message{{Role: "user", Content: "please"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks := ParseContentBlocks(result.Content)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[0].Type != "thinking" || blocks[0].Thinking != "think 1" {
		t.Fatalf("thinking block = %#v", blocks[0])
	}
	if blocks[1].Type != "text" || blocks[1].Text != "answer" {
		t.Fatalf("text block = %#v", blocks[1])
	}
	if blocks[2].Type != "toolCall" || blocks[2].ThoughtSignature == "" {
		t.Fatalf("tool block = %#v", blocks[2])
	}
}

func TestOpenAIProviderStreamingParsesChunkUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var chunks bytes.Buffer
		chunks.WriteString("data: {\"id\":\"resp-usage\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4,\"total_tokens\":14,\"prompt_tokens_details\":{\"cached_tokens\":5,\"cache_write_tokens\":2}}}\n")
		chunks.WriteString("data: [DONE]\n")
		_, _ = w.Write(chunks.Bytes())
	}))
	defer server.Close()

	RegisterModel(Model{
		ID:       "priced-chat-usage-model",
		Name:     "Priced Chat Usage",
		API:      "openai-completions",
		Provider: "openai",
		BaseURL:  server.URL + "/v1",
		Input:    []string{"text"},
		Cost: ModelCost{
			Input:      1000,
			Output:     2000,
			CacheRead:  500,
			CacheWrite: 250,
		},
	})
	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "priced-chat-usage-model",
		Options:  ChatOptions{APIKey: "test-key", Stream: true},
		Messages: []Message{{Role: "user", Content: "please"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage == nil {
		t.Fatal("missing usage")
	}
	if result.Usage.Input != 5 || result.Usage.Output != 4 || result.Usage.CacheRead != 3 || result.Usage.CacheWrite != 2 || result.Usage.TotalTokens != 14 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	if result.Usage.Cost.Total <= 0 {
		t.Fatalf("cost = %#v", result.Usage.Cost)
	}
}

func TestOpenAIProviderStreamingParsesChoiceUsageFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var chunks bytes.Buffer
		chunks.WriteString("data: {\"id\":\"resp-usage-choice\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\",\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":3,\"total_tokens\":11,\"prompt_tokens_details\":{\"cached_tokens\":2}}}]}\n")
		chunks.WriteString("data: [DONE]\n")
		_, _ = w.Write(chunks.Bytes())
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Options:  ChatOptions{APIKey: "test-key", Stream: true},
		Messages: []Message{{Role: "user", Content: "please"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage == nil {
		t.Fatal("missing usage")
	}
	if result.Usage.Input != 6 || result.Usage.Output != 3 || result.Usage.CacheRead != 2 || result.Usage.CacheWrite != 0 || result.Usage.TotalTokens != 11 {
		t.Fatalf("usage = %#v", result.Usage)
	}
}

func TestOpenAIProviderStreamingPreservesInterleavedBlockOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var chunks bytes.Buffer
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think 1\"},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"answer 1\"},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think 2\"},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"answer 2\"},\"finish_reason\":\"stop\"}]}\n")
		chunks.WriteString("data: [DONE]\n")
		_, _ = w.Write(chunks.Bytes())
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Options:  ChatOptions{APIKey: "test-key", Stream: true},
		Messages: []Message{{Role: "user", Content: "please"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks := ParseContentBlocks(result.Content)
	if len(blocks) != 4 {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[0].Type != "thinking" || blocks[0].Thinking != "think 1" {
		t.Fatalf("block[0] = %#v", blocks[0])
	}
	if blocks[1].Type != "text" || blocks[1].Text != "answer 1" {
		t.Fatalf("block[1] = %#v", blocks[1])
	}
	if blocks[2].Type != "thinking" || blocks[2].Thinking != "think 2" {
		t.Fatalf("block[2] = %#v", blocks[2])
	}
	if blocks[3].Type != "text" || blocks[3].Text != "answer 2" {
		t.Fatalf("block[3] = %#v", blocks[3])
	}
}

func TestOpenAIProviderStreamingHandlesLargeChunkPayload(t *testing.T) {
	large := strings.Repeat("x", 80*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var chunks bytes.Buffer
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"")
		chunks.WriteString(large)
		chunks.WriteString("\"},\"finish_reason\":\"stop\"}]}\n")
		chunks.WriteString("data: [DONE]\n")
		_, _ = w.Write(chunks.Bytes())
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Options:  ChatOptions{APIKey: "test-key", Stream: true},
		Messages: []Message{{Role: "user", Content: "please"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != large {
		t.Fatalf("text length = %d", len(result.Text))
	}
}

func TestOpenAIProviderAddsCacheAffinityForChatCompletions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("session_id") != "session-abc" {
			t.Fatalf("session_id = %q", req.Header.Get("session_id"))
		}
		if req.Header.Get("x-client-request-id") != "session-abc" {
			t.Fatalf("x-client-request-id = %q", req.Header.Get("x-client-request-id"))
		}
		if req.Header.Get("x-session-affinity") != "session-abc" {
			t.Fatalf("x-session-affinity = %q", req.Header.Get("x-session-affinity"))
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["prompt_cache_key"] != "session-abc" {
			t.Fatalf("prompt_cache_key = %#v", payload["prompt_cache_key"])
		}
		if payload["prompt_cache_retention"] != "24h" {
			t.Fatalf("prompt_cache_retention = %#v", payload["prompt_cache_retention"])
		}
		_, _ = fmt.Fprint(w, `{
			"choices": [{
				"message": {"role": "assistant", "content": "cached"},
				"finish_reason": "stop"
			}]
		}`)
	}))
	defer server.Close()

	RegisterModel(Model{
		ID:       "chat-affinity-model",
		Name:     "Chat Affinity",
		API:      "openai-completions",
		Provider: "openai",
		BaseURL:  server.URL + "/v1",
		Input:    []string{"text"},
		Compat:   map[string]any{"sendSessionAffinityHeaders": true},
	})

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "chat-affinity-model",
		Options: ChatOptions{
			APIKey:         "test-key",
			SessionID:      "session-abc",
			CacheRetention: CacheRetentionLong,
		},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "cached" {
		t.Fatalf("text = %q", result.Text)
	}
}

func TestOpenAIProviderUsesResponsesEndpointForGpt5(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if req.Header.Get("session_id") != "session-rsp" {
			t.Fatalf("session_id = %q", req.Header.Get("session_id"))
		}
		if req.Header.Get("x-client-request-id") != "session-rsp" {
			t.Fatalf("x-client-request-id = %q", req.Header.Get("x-client-request-id"))
		}
		if req.Header.Get("x-session-affinity") != "session-rsp" {
			t.Fatalf("x-session-affinity = %q", req.Header.Get("x-session-affinity"))
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		input, _ := payload["input"].([]any)
		if len(input) == 0 {
			t.Fatal("missing input")
		}
		first, ok := input[0].(map[string]any)
		if !ok {
			t.Fatalf("first input item = %#v", input[0])
		}
		if got := first["role"]; got != "user" {
			t.Fatalf("input role = %v", got)
		}
		if payload["prompt_cache_key"] != "session-rsp" {
			t.Fatalf("prompt_cache_key = %#v", payload["prompt_cache_key"])
		}
		if payload["prompt_cache_retention"] != "24h" {
			t.Fatalf("prompt_cache_retention = %#v", payload["prompt_cache_retention"])
		}

		_, _ = fmt.Fprint(w, `{
			"output": [
				{
					"type": "message",
					"role": "assistant",
					"content": [{"type": "output_text", "text": "hi"}]
				}
			],
			"usage": {
				"input_tokens": 3,
				"output_tokens": 4,
				"total_tokens": 7
			},
			"status": "completed"
		}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-5.4",
		Options: ChatOptions{
			APIKey:         "test-key",
			SessionID:      "session-rsp",
			CacheRetention: CacheRetentionLong,
		},
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "stop" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if result.Text != "hi" {
		t.Fatalf("text = %q", result.Text)
	}
}

func TestOpenAIProviderParsesNonStreamingReasoningAndToolSignatures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprint(w, `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "done",
					"reasoning_content": "internal",
					"tool_calls": [{
						"id": "tc-1",
						"type": "function",
						"function": {"name":"echo","arguments":"{\"value\":\"ok\"}"}
					}],
					"reasoning_details": [{
						"type": "reasoning.encrypted",
						"id": "tc-1",
						"data": "encrypted"
					}]
				},
				"finish_reason": "tool_calls"
			}]
		}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Tools:    []Tool{{Name: "echo"}},
		Options:  ChatOptions{APIKey: "test-key"},
		Messages: []Message{{Role: "user", Content: "please"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks := ParseContentBlocks(result.Content)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[0].Type != "thinking" || blocks[0].Thinking != "internal" {
		t.Fatalf("thinking block = %#v", blocks[0])
	}
	if blocks[2].Type != "toolCall" || blocks[2].ThoughtSignature == "" {
		t.Fatalf("tool block = %#v", blocks[2])
	}
}

func TestOpenAIProviderIncludesSystemMessageInChatCompletions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		messages, _ := payload["messages"].([]any)
		if len(messages) < 2 {
			t.Fatalf("messages = %#v", payload["messages"])
		}
		first, _ := messages[0].(map[string]any)
		if first["role"] != "system" || first["content"] != "be terse" {
			t.Fatalf("first message = %#v", first)
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Options:  ChatOptions{APIKey: "test-key"},
		Messages: []Message{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderIncludesUserImagesInChatCompletions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		messages, _ := payload["messages"].([]any)
		first, _ := messages[0].(map[string]any)
		content, _ := first["content"].([]any)
		if len(content) != 2 {
			t.Fatalf("content = %#v", first["content"])
		}
		imagePart, _ := content[1].(map[string]any)
		if imagePart["type"] != "image_url" {
			t.Fatalf("image part = %#v", imagePart)
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Options:  ChatOptions{APIKey: "test-key"},
		Messages: []Message{{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": "look"},
				map[string]any{"type": "image", "data": "abc", "mimeType": "image/png"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderConvertsToolResultImagesForChatCompletions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		messages, _ := payload["messages"].([]any)
		if len(messages) != 3 {
			t.Fatalf("messages = %#v", payload["messages"])
		}
		toolMsg, _ := messages[1].(map[string]any)
		if toolMsg["role"] != "tool" || toolMsg["content"] != "(see attached image)" {
			t.Fatalf("tool message = %#v", toolMsg)
		}
		userMsg, _ := messages[2].(map[string]any)
		content, _ := userMsg["content"].([]any)
		if len(content) != 2 {
			t.Fatalf("user content = %#v", userMsg["content"])
		}
		imagePart, _ := content[1].(map[string]any)
		if imagePart["type"] != "image_url" {
			t.Fatalf("image part = %#v", imagePart)
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Options:  ChatOptions{APIKey: "test-key"},
		Messages: []Message{
			{
				Role: "assistant",
				Content: []any{
					map[string]any{"type": "toolCall", "id": "call-1", "name": "screenshot", "arguments": map[string]any{}},
				},
			},
			{
				Role:       "toolResult",
				ToolCallID: "call-1",
				ToolName:   "screenshot",
				Content: []any{
					map[string]any{"type": "image", "data": "abc", "mimeType": "image/png"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIResponsesOmitsForeignFunctionCallItemIDForDifferentModelReplay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		input, _ := payload["input"].([]any)
		if len(input) != 2 {
			t.Fatalf("input = %#v", payload["input"])
		}
		call, _ := input[0].(map[string]any)
		if call["type"] != "function_call" || call["call_id"] != "call-1" {
			t.Fatalf("call item = %#v", call)
		}
		if _, ok := call["id"]; ok {
			t.Fatalf("unexpected id = %#v", call["id"])
		}
		_, _ = fmt.Fprint(w, `{"output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1},"status":"completed"}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-5.4",
		Options:  ChatOptions{APIKey: "test-key"},
		Messages: []Message{{
			Role:       "assistant",
			Provider:   "openai",
			API:        "openai-responses",
			Model:      "gpt-5.3",
			StopReason: "toolUse",
			Content: []any{
				map[string]any{"type": "toolCall", "id": "call-1|fc_existing", "name": "search", "arguments": map[string]any{"q": "pi"}},
			},
		}, {
			Role:       "toolResult",
			Provider:   "openai",
			API:        "openai-responses",
			Model:      "gpt-5.3",
			ToolCallID: "call-1|fc_existing",
			ToolName:   "search",
			Content:    []any{map[string]any{"type": "text", "text": "done"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIResponsesKeepsImageToolResultOutputString(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		input, _ := payload["input"].([]any)
		if len(input) != 2 {
			t.Fatalf("input = %#v", payload["input"])
		}
		output, _ := input[1].(map[string]any)
		if output["type"] != "function_call_output" {
			t.Fatalf("output item = %#v", output)
		}
		if got, ok := output["output"].(string); !ok || got != "(see attached image)" {
			t.Fatalf("function output = %#v", output["output"])
		}
		_, _ = fmt.Fprint(w, `{"output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1},"status":"completed"}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-5.4",
		Options:  ChatOptions{APIKey: "test-key"},
		Messages: []Message{{
			Role: "assistant",
			Content: []any{
				map[string]any{"type": "toolCall", "id": "call-1", "name": "screenshot", "arguments": map[string]any{}},
			},
		}, {
			Role:       "toolResult",
			ToolCallID: "call-1",
			ToolName:   "screenshot",
			Content: []any{
				map[string]any{"type": "image", "data": "abc", "mimeType": "image/png"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderStreamsResponsesModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["stream"] != true {
			t.Fatalf("stream = %#v", payload["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"msg-1\",\"content\":[{\"type\":\"output_text\",\"text\":\"hi\"}]}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call-1\",\"id\":\"fc_1\",\"name\":\"echo\",\"arguments\":\"{\\\"value\\\":\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"\\\"ok\\\"}\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call-1\",\"id\":\"fc_1\",\"name\":\"echo\",\"arguments\":\"{\\\"value\\\":\\\"ok\\\"}\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	}))
	defer server.Close()

	RegisterModel(Model{
		ID:       "gpt-5-priced",
		Name:     "GPT 5 Priced",
		API:      "openai-completions",
		Provider: "openai",
		BaseURL:  server.URL + "/v1",
		Input:    []string{"text"},
		Cost: ModelCost{
			Input:     1000,
			Output:    2000,
			CacheRead: 500,
		},
	})
	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, events, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-5-priced",
		Options: ChatOptions{
			APIKey: "test-key",
			Stream: true,
		},
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hi" {
		t.Fatalf("text = %q", result.Text)
	}
	if result.Usage == nil || result.Usage.Cost.Total <= 0 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	foundToolEnd := false
	for _, event := range events {
		if event.Type == "toolcall_end" {
			foundToolEnd = true
			if event.ToolCall == nil || event.ToolCall.ID != "call-1|fc_1" {
				t.Fatalf("toolcall_end = %#v", event)
			}
		}
	}
	if !foundToolEnd {
		t.Fatalf("events = %#v", events)
	}
}

func TestOpenAIProviderUsesMaxOutputTokensForResponsesModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["max_output_tokens"] != float64(123) {
			t.Fatalf("payload = %#v", payload)
		}
		_, _ = fmt.Fprint(w, `{"output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1},"status":"completed"}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-5.4",
		Options:  ChatOptions{APIKey: "test-key", MaxTokens: 123},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderUsesDeepSeekReasoningPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		thinking, _ := payload["thinking"].(map[string]any)
		if thinking["type"] != "enabled" {
			t.Fatalf("thinking = %#v", payload["thinking"])
		}
		if payload["reasoning_effort"] != "max" {
			t.Fatalf("payload = %#v", payload)
		}
		if _, ok := payload["store"]; ok {
			t.Fatalf("payload unexpectedly set store: %#v", payload)
		}
		messages, _ := payload["messages"].([]any)
		first, _ := messages[0].(map[string]any)
		if first["role"] != "system" {
			t.Fatalf("first message = %#v", first)
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "deepseek",
		Model:    "deepseek-v4-pro",
		Options:  ChatOptions{APIKey: "test-key", ReasoningEffort: "xhigh"},
		Messages: []Message{
			{Role: "system", Content: "follow rules"},
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderUsesOpenRouterReasoningPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		reasoning, _ := payload["reasoning"].(map[string]any)
		if reasoning["effort"] != "high" {
			t.Fatalf("reasoning = %#v", payload["reasoning"])
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	RegisterModel(Model{
		ID:        "or-model",
		Name:      "OR Model",
		API:       "openai-completions",
		Provider:  "openrouter",
		BaseURL:   server.URL + "/v1",
		Reasoning: true,
		Input:     []string{"text"},
	})
	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openrouter",
		Model:    "or-model",
		Options:  ChatOptions{APIKey: "test-key", ReasoningEffort: "high"},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderUsesOpenRouterRoutingCompat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		provider, _ := payload["provider"].(map[string]any)
		if provider["only"] == nil {
			t.Fatalf("provider = %#v", payload["provider"])
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	RegisterModel(Model{
		ID:        "or-route",
		Name:      "OR Route",
		API:       "openai-completions",
		Provider:  "openrouter",
		BaseURL:   server.URL + "/v1",
		Reasoning: true,
		Input:     []string{"text"},
		Compat: map[string]any{
			"openRouterRouting": map[string]any{"only": []any{"openai"}},
		},
	})
	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openrouter",
		Model:    "or-route",
		Options:  ChatOptions{APIKey: "test-key"},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderUsesVercelGatewayRoutingCompat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		providerOptions, _ := payload["providerOptions"].(map[string]any)
		gateway, _ := providerOptions["gateway"].(map[string]any)
		if gateway["order"] == nil {
			t.Fatalf("providerOptions = %#v", payload["providerOptions"])
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	RegisterModel(Model{
		ID:       "vercel-route",
		Name:     "Vercel Route",
		API:      "openai-completions",
		Provider: "vercel-ai-gateway",
		BaseURL:  server.URL + "/v1",
		Input:    []string{"text"},
		Compat: map[string]any{
			"vercelGatewayRouting": map[string]any{"order": []any{"anthropic", "openai"}},
		},
	})
	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "vercel-ai-gateway",
		Model:    "vercel-route",
		Options:  ChatOptions{APIKey: "test-key"},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderSkipsStreamUsageWhenCompatDisablesIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if _, ok := payload["stream_options"]; ok {
			t.Fatalf("payload = %#v", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n")
	}))
	defer server.Close()

	RegisterModel(Model{
		ID:       "no-stream-usage",
		Name:     "No Stream Usage",
		API:      "openai-completions",
		Provider: "openai",
		BaseURL:  server.URL + "/v1",
		Input:    []string{"text"},
		Compat:   map[string]any{"supportsUsageInStreaming": false},
	})
	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "no-stream-usage",
		Options:  ChatOptions{APIKey: "test-key", Stream: true},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderUsesSessionAffinityHeadersOnlyWhenCompatEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("session_id") != "session-chat" {
			t.Fatalf("session_id = %q", req.Header.Get("session_id"))
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	RegisterModel(Model{
		ID:       "session-affinity-model",
		Name:     "Session Affinity",
		API:      "openai-completions",
		Provider: "openai",
		BaseURL:  server.URL + "/v1",
		Input:    []string{"text"},
		Compat:   map[string]any{"sendSessionAffinityHeaders": true},
	})
	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "session-affinity-model",
		Options:  ChatOptions{APIKey: "test-key", SessionID: "session-chat"},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderUsesDeveloperRoleForReasoningModelsWhenCompatEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		messages, _ := payload["messages"].([]any)
		if len(messages) == 0 {
			t.Fatalf("messages = %#v", payload["messages"])
		}
		first, _ := messages[0].(map[string]any)
		if first["role"] != "developer" {
			t.Fatalf("first message = %#v", first)
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	RegisterModel(Model{
		ID:        "developer-role-model",
		Name:      "Developer Role Model",
		API:       "openai-completions",
		Provider:  "openai",
		BaseURL:   server.URL + "/v1",
		Reasoning: true,
		Input:     []string{"text"},
	})
	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "developer-role-model",
		Options:  ChatOptions{APIKey: "test-key"},
		Messages: []Message{
			{Role: "system", Content: "follow these rules"},
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderHonorsReasoningEffortMapCompat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["reasoning_effort"] != "default" {
			t.Fatalf("payload = %#v", payload)
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	RegisterModel(Model{
		ID:        "groq-qwen-compat",
		Name:      "Groq Qwen Compat",
		API:       "openai-completions",
		Provider:  "groq",
		BaseURL:   server.URL + "/v1",
		Reasoning: true,
		Input:     []string{"text"},
		Compat: map[string]any{
			"reasoningEffortMap": map[string]any{
				"minimal": "default",
				"low":     "default",
				"medium":  "default",
				"high":    "default",
				"xhigh":   "default",
			},
		},
	})
	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "groq",
		Model:    "groq-qwen-compat",
		Options:  ChatOptions{APIKey: "test-key", ReasoningEffort: "xhigh"},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderUsesRegisteredProviderCompatDefaults(t *testing.T) {
	ClearModelCompatDefaults()
	defer ClearModelCompatDefaults()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if _, ok := payload["store"]; ok {
			t.Fatalf("store should be disabled by provider compat: %#v", payload)
		}
		if payload["max_tokens"] != float64(32) {
			t.Fatalf("payload = %#v", payload)
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	RegisterModelCompatDefaults("new-compatible", map[string]any{
		"supportsStore":  false,
		"maxTokensField": "max_tokens",
	})
	RegisterModel(Model{
		ID:       "provider-default-compat",
		Name:     "Provider Default Compat",
		API:      "openai-completions",
		Provider: "new-compatible",
		BaseURL:  server.URL + "/v1",
		Input:    []string{"text"},
	})
	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "new-compatible",
		Model:    "provider-default-compat",
		Options:  ChatOptions{APIKey: "test-key", MaxTokens: 32},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderSendsEmptyToolsArrayForToolHistoryWithoutCurrentTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		tools, ok := payload["tools"].([]any)
		if !ok {
			t.Fatalf("payload = %#v", payload)
		}
		if len(tools) != 0 {
			t.Fatalf("tools = %#v", tools)
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-4.1-mini",
		Options:  ChatOptions{APIKey: "test-key"},
		Messages: []Message{
			{
				Role: "assistant",
				Content: []ContentBlock{
					{Type: "toolCall", ID: "call-1", Name: "search", Arguments: map[string]any{"q": "hi"}},
				},
			},
			{Role: "toolResult", ToolCallID: "call-1", ToolName: "search", Content: "done"},
			{Role: "user", Content: "continue"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderAppliesAnthropicCacheControlCompat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		messages, _ := payload["messages"].([]any)
		if len(messages) == 0 {
			t.Fatalf("messages = %#v", payload["messages"])
		}
		first, _ := messages[0].(map[string]any)
		content, _ := first["content"].([]any)
		part, _ := content[0].(map[string]any)
		if part["cache_control"] == nil {
			t.Fatalf("system content = %#v", first["content"])
		}
		tools, _ := payload["tools"].([]any)
		lastTool, _ := tools[len(tools)-1].(map[string]any)
		if lastTool["cache_control"] == nil {
			t.Fatalf("tool = %#v", lastTool)
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	RegisterModel(Model{
		ID:       "anthropic-cache-compatible",
		Name:     "Anthropic Cache Compatible",
		API:      "openai-completions",
		Provider: "openrouter",
		BaseURL:  server.URL + "/v1",
		Input:    []string{"text"},
		Compat: map[string]any{
			"cacheControlFormat": "anthropic",
		},
	})
	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openrouter",
		Model:    "anthropic-cache-compatible",
		Options: ChatOptions{
			APIKey:         "test-key",
			SessionID:      "session-cache",
			CacheRetention: CacheRetentionLong,
		},
		Tools: []Tool{{Name: "echo"}},
		Messages: []Message{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderRetriesRateLimits(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, `{"error":"rate limit"}`, http.StatusTooManyRequests)
			return
		}
		_, _ = fmt.Fprint(w, `{
			"choices": [{
				"message": {"role": "assistant", "content": "ok"},
				"finish_reason": "stop"
			}]
		}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Options: ChatOptions{
			APIKey:     "test-key",
			MaxRetries: 1,
		},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d", attempts)
	}
	if result.Text != "ok" {
		t.Fatalf("text = %q", result.Text)
	}
}

func TestOpenAIProviderIncludesStructuredRawErrorMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{
			"error": {
				"message": "bad prompt",
				"metadata": {
					"raw": "provider detail"
				}
			}
		}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Options:  ChatOptions{APIKey: "test-key"},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bad prompt") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "provider detail") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenAIProviderPayloadAndResponseHooks(t *testing.T) {
	hookCalled := false
	responseCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "gpt-hooked" {
			t.Fatalf("model = %#v", payload["model"])
		}
		w.Header().Set("X-Test-Hook", "ok")
		_, _ = fmt.Fprint(w, `{
			"choices": [{
				"message": {"role": "assistant", "content": "hooked"},
				"finish_reason": "stop"
			}]
		}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Options: ChatOptions{
			APIKey: "test-key",
			OnPayload: func(payload any, req CompletionRequest) (any, error) {
				hookCalled = true
				payloadMap, ok := payload.(map[string]any)
				if ok {
					payloadMap["model"] = "gpt-hooked"
					return payloadMap, nil
				}
				raw, err := json.Marshal(payload)
				if err != nil {
					return nil, err
				}
				var next map[string]any
				if err := json.Unmarshal(raw, &next); err != nil {
					return nil, err
				}
				next["model"] = "gpt-hooked"
				return next, nil
			},
			OnResponse: func(response ProviderResponse, req CompletionRequest) error {
				responseCalled = true
				if response.Status != 200 {
					t.Fatalf("status = %d", response.Status)
				}
				if response.Headers["X-Test-Hook"] != "ok" {
					t.Fatalf("headers = %#v", response.Headers)
				}
				return nil
			},
		},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hookCalled || !responseCalled {
		t.Fatalf("hookCalled=%v responseCalled=%v", hookCalled, responseCalled)
	}
	if result.Text != "hooked" {
		t.Fatalf("text = %q", result.Text)
	}
}

func TestOpenAIProviderUsesProviderProfileAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if req.Header.Get("Authorization") != "Bearer secret-deepseek-key" {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}
		_, _ = fmt.Fprint(w, `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "ok"
				},
				"finish_reason": "stop"
			}]
		}`)
	}))
	defer server.Close()

	t.Setenv("DEEPSEEK_API_KEY", "secret-deepseek-key")
	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "deep-seek",
		Model:    "deepseek-v4-pro",
		Options:  ChatOptions{},
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderUsesCopilotHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if req.Header.Get("Authorization") != "Bearer secret-copilot-key" {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}
		if req.Header.Get("User-Agent") != "GitHubCopilotChat/0.35.0" {
			t.Fatalf("user-agent = %q", req.Header.Get("User-Agent"))
		}
		if req.Header.Get("Editor-Version") != "vscode/1.107.0" {
			t.Fatalf("editor-version = %q", req.Header.Get("Editor-Version"))
		}
		if req.Header.Get("Editor-Plugin-Version") != "copilot-chat/0.35.0" {
			t.Fatalf("editor-plugin-version = %q", req.Header.Get("Editor-Plugin-Version"))
		}
		if req.Header.Get("Copilot-Integration-Id") != "vscode-chat" {
			t.Fatalf("copilot-integration-id = %q", req.Header.Get("Copilot-Integration-Id"))
		}
		if req.Header.Get("X-Initiator") != "user" {
			t.Fatalf("x-initiator = %q", req.Header.Get("X-Initiator"))
		}
		if req.Header.Get("Openai-Intent") != "conversation-edits" {
			t.Fatalf("openai-intent = %q", req.Header.Get("Openai-Intent"))
		}
		if got := req.Header.Get("Copilot-Vision-Request"); got != "" {
			t.Fatalf("copilot-vision-request = %q", got)
		}
		_, _ = fmt.Fprint(w, `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "ok"
				},
				"finish_reason": "stop"
			}]
		}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "github-copilot",
		Model:    "gpt-5.4",
		Options:  ChatOptions{APIKey: "secret-copilot-key"},
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderCopilotHeadersUseAgentInitiatorAndVisionHeaderWhenImagesPresent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Initiator") != "agent" {
			t.Fatalf("x-initiator = %q", req.Header.Get("X-Initiator"))
		}
		if req.Header.Get("Openai-Intent") != "conversation-edits" {
			t.Fatalf("openai-intent = %q", req.Header.Get("Openai-Intent"))
		}
		if req.Header.Get("Copilot-Vision-Request") != "true" {
			t.Fatalf("copilot-vision-request = %q", req.Header.Get("Copilot-Vision-Request"))
		}
		_, _ = fmt.Fprint(w, `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "ok"
				},
				"finish_reason": "stop"
			}]
		}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL))
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "github-copilot",
		Model:    "gpt-5.4",
		Options:  ChatOptions{APIKey: "secret-copilot-key"},
		Messages: []Message{
			{Role: "user", Content: "hi"},
			{Role: "user", Content: []any{
				map[string]any{
					"type":     "image",
					"mimeType": "image/png",
					"data":     "aGVsbG8=",
				},
			}},
			{Role: "assistant", Content: "ready"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderSupportsAzureResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/openai/v1/responses" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if got := req.URL.Query().Get("api-version"); got != "test-version" {
			t.Fatalf("api-version = %q", got)
		}
		if req.Header.Get("Authorization") != "Bearer secret-azure-key" {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}
		_, _ = fmt.Fprint(w, `{
			"output": [
				{"type": "message", "role": "assistant", "content": [{"type":"output_text", "text": "ok"}]}
			],
			"usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
			"status": "completed"
		}`)
	}))
	defer server.Close()

	t.Setenv("AZURE_OPENAI_API_KEY", "secret-azure-key")
	t.Setenv("AZURE_OPENAI_BASE_URL", server.URL+"/openai/v1")
	t.Setenv("AZURE_OPENAI_API_VERSION", "test-version")
	provider := OpenAIProvider()
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "azure-openai-responses",
		Model:    "gpt-5.4",
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIProviderAzureResponsesRequiresBaseURL(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "secret-azure-key")
	t.Setenv("AZURE_OPENAI_BASE_URL", "")
	t.Setenv("AZURE_OPENAI_RESOURCE_NAME", "")
	provider := OpenAIProvider()
	_, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "azure-openai-responses",
		Model:    "gpt-5.4",
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Azure OpenAI base URL is required") {
		t.Fatalf("error = %q", err)
	}
}

func TestOpenAIProviderAzureResponsesUsesResourceNameFallback(t *testing.T) {
	t.Setenv("AZURE_OPENAI_BASE_URL", "")
	t.Setenv("AZURE_OPENAI_RESOURCE_NAME", "my-azure-resource")
	t.Setenv("AZURE_OPENAI_API_VERSION", "")
	t.Setenv("AZURE_OPENAI_API_KEY", "secret-azure-key")

	got, err := resolveAzureOpenAIBaseURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://my-azure-resource.openai.azure.com/openai/v1" {
		t.Fatalf("resolved base URL = %q", got)
	}
}
