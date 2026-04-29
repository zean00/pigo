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

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
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
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-5.4",
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
