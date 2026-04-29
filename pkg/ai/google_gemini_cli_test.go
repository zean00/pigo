package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGoogleGeminiCLIProviderParsesSSEToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %q", req.Method)
		}
		if req.URL.Path != "/v1internal:streamGenerateContent" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if req.URL.Query().Get("alt") != "sse" {
			t.Fatalf("alt = %q", req.URL.Query().Get("alt"))
		}
		if req.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}
		if req.Header.Get("User-Agent") != "google-cloud-sdk vscode_cloudshelleditor/0.1" {
			t.Fatalf("user-agent = %q", req.Header.Get("User-Agent"))
		}

		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["project"] != "test-project" {
			t.Fatalf("project = %#v", payload["project"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Working on it.\"},{\"functionCall\":{\"id\":\"call-1\",\"name\":\"math\",\"args\":{\"a\":15,\"b\":27}}}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":6,\"thoughtsTokenCount\":2,\"cachedContentTokenCount\":3,\"totalTokenCount\":18}}}\n\n")
	}))
	defer server.Close()

	provider := GoogleGeminiCLIProvider()
	result, events, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "google-gemini-cli",
		Model:    "gemini-test",
		Options: ChatOptions{
			APIKey:  `{"token":"test-token","projectId":"test-project"}`,
			BaseURL: server.URL,
		},
		Tools: []Tool{{
			Name:        "math",
			Description: "add numbers",
			Parameters: map[string]any{
				"type": "object",
			},
		}},
		Messages: []Message{{Role: "user", Content: "calculate"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "toolUse" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if result.Usage == nil || result.Usage.Input != 7 || result.Usage.Output != 8 || result.Usage.CacheRead != 3 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	if len(events) == 0 || events[0].Type != "start" || events[len(events)-1].Type != "done" {
		t.Fatalf("events = %#v", events)
	}
}

func TestGoogleGeminiCLIProviderAccumulatesSSEChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Working \"}]}}]}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"on it.\"},{\"functionCall\":{\"id\":\"call-1\",\"name\":\"math\",\"args\":{\"a\":15,\"b\":27}}}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":6,\"totalTokenCount\":16}}}\n\n")
	}))
	defer server.Close()

	provider := GoogleGeminiCLIProvider()
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "google-gemini-cli",
		Model:    "gemini-test",
		Options: ChatOptions{
			APIKey:  `{"token":"test-token","projectId":"test-project"}`,
			BaseURL: server.URL,
		},
		Tools:    []Tool{{Name: "math"}},
		Messages: []Message{{Role: "user", Content: "calculate"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Working on it." {
		t.Fatalf("text = %q", result.Text)
	}
	if result.StopReason != "toolUse" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	blocks := result.contentBlocks()
	if len(blocks) != 3 {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[2].Type != "toolCall" || blocks[2].Name != "math" {
		t.Fatalf("tool call block = %#v", blocks[2])
	}
}

func TestGoogleAntigravityProviderUsesSandboxHeadersAndSystemInstruction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}
		if got := req.Header.Get("User-Agent"); !strings.HasPrefix(got, "antigravity/") {
			t.Fatalf("user-agent = %q", got)
		}

		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["requestType"] != "agent" {
			t.Fatalf("requestType = %#v", payload["requestType"])
		}
		requestMap, _ := payload["request"].(map[string]any)
		systemInstruction, _ := requestMap["systemInstruction"].(map[string]any)
		parts, _ := systemInstruction["parts"].([]any)
		if len(parts) < 2 {
			t.Fatalf("systemInstruction.parts = %#v", parts)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}}\n\n")
	}))
	defer server.Close()

	t.Setenv("PI_AI_ANTIGRAVITY_VERSION", "9.9.9")
	provider := GoogleGeminiCLIProvider()
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "google-antigravity",
		Model:    "claude-opus-4-6-thinking",
		Options: ChatOptions{
			APIKey:  `{"token":"test-token","projectId":"test-project"}`,
			BaseURL: server.URL,
		},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "ok" {
		t.Fatalf("text = %q", result.Text)
	}
}

func TestGoogleGeminiCLIProviderSupportsStreamFlag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}}\n\n")
	}))
	defer server.Close()

	provider := GoogleGeminiCLIProvider()
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "google-gemini-cli",
		Model:    "gemini-test",
		Options: ChatOptions{
			APIKey:  `{"token":"test-token","projectId":"test-project"}`,
			BaseURL: server.URL,
			Stream:  true,
		},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello" {
		t.Fatalf("text = %q", result.Text)
	}
}
