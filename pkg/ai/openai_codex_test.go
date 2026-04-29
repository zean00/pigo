package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/websocket"
)

func TestOpenAICodexProviderUsesCodexEndpoint(t *testing.T) {
	token := buildCodexTestJWT("acct_test_123")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %q", req.Method)
		}
		if req.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if req.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}
		if req.Header.Get("OpenAI-Beta") != "responses=experimental" {
			t.Fatalf("openai-beta = %q", req.Header.Get("OpenAI-Beta"))
		}
		if req.Header.Get("chatgpt-account-id") != "acct_test_123" {
			t.Fatalf("chatgpt-account-id = %q", req.Header.Get("chatgpt-account-id"))
		}
		if req.Header.Get("session_id") != "session-123" {
			t.Fatalf("session_id = %q", req.Header.Get("session_id"))
		}
		if req.Header.Get("x-client-request-id") != "session-123" {
			t.Fatalf("x-client-request-id = %q", req.Header.Get("x-client-request-id"))
		}

		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "gpt-5.5" {
			t.Fatalf("model = %#v", payload["model"])
		}
		if payload["prompt_cache_key"] != "session-123" {
			t.Fatalf("prompt_cache_key = %#v", payload["prompt_cache_key"])
		}
		text, _ := payload["text"].(map[string]any)
		if text["verbosity"] != "high" {
			t.Fatalf("text = %#v", text)
		}
		reasoning, _ := payload["reasoning"].(map[string]any)
		if reasoning["effort"] != "low" || reasoning["summary"] != "detailed" {
			t.Fatalf("reasoning = %#v", reasoning)
		}
		if payload["service_tier"] != "priority" {
			t.Fatalf("service_tier = %#v", payload["service_tier"])
		}

		_, _ = fmt.Fprint(w, `{
			"output": [
				{"type": "message", "role": "assistant", "content": [{"type":"output_text", "text":"ok"}]}
			],
			"usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
			"status": "completed"
		}`)
	}))
	defer server.Close()

	provider := OpenAICodexProvider()
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai-codex",
		Model:    "gpt-5.5",
		Options: ChatOptions{
			APIKey:           token,
			BaseURL:          server.URL + "/backend-api",
			SessionID:        "session-123",
			TextVerbosity:    "high",
			ReasoningEffort:  "minimal",
			ReasoningSummary: "detailed",
			ServiceTier:      "priority",
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

func TestOpenAICodexProviderStreamingResponse(t *testing.T) {
	token := buildCodexTestJWT("acct_test_123")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
		if req.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("accept = %q", req.Header.Get("Accept"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	}))
	defer server.Close()

	provider := OpenAICodexProvider()
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai-codex",
		Model:    "gpt-5.5",
		Options: ChatOptions{
			APIKey:  token,
			BaseURL: server.URL + "/backend-api",
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

func TestOpenAICodexProviderWebSocketStreamingResponse(t *testing.T) {
	token := buildCodexTestJWT("acct_test_123")

	mux := http.NewServeMux()
	mux.Handle("/backend-api/codex/responses", websocket.Handler(func(conn *websocket.Conn) {
		defer conn.Close()
		if got := conn.Request().Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("authorization = %q", got)
			return
		}
		if got := conn.Request().Header.Get("OpenAI-Beta"); got != openAICodexWebSocketBeta {
			t.Errorf("openai-beta = %q", got)
			return
		}
		if got := conn.Request().Header.Get("session_id"); got != "session-123" {
			t.Errorf("session_id = %q", got)
			return
		}
		if got := conn.Request().Header.Get("x-client-request-id"); got != "session-123" {
			t.Errorf("x-client-request-id = %q", got)
			return
		}

		var payload map[string]any
		if err := websocket.JSON.Receive(conn, &payload); err != nil {
			t.Errorf("receive payload: %v", err)
			return
		}
		if payload["type"] != "response.create" {
			t.Errorf("type = %#v", payload["type"])
			return
		}
		if payload["prompt_cache_key"] != "session-123" {
			t.Errorf("prompt_cache_key = %#v", payload["prompt_cache_key"])
			return
		}

		for _, event := range []map[string]any{
			{"type": "response.output_item.added", "item": map[string]any{"type": "message"}},
			{"type": "response.output_text.delta", "delta": "hello"},
			{"type": "response.completed", "response": map[string]any{
				"status": "completed",
				"usage":  map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
			}},
		} {
			if err := websocket.JSON.Send(conn, event); err != nil {
				t.Errorf("send event: %v", err)
				return
			}
		}
	}))
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := OpenAICodexProvider()
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai-codex",
		Model:    "gpt-5.5",
		Options: ChatOptions{
			APIKey:    token,
			BaseURL:   server.URL + "/backend-api",
			Stream:    true,
			Transport: "websocket",
			SessionID: "session-123",
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

func TestOpenAICodexProviderAutoTransportFallsBackToSSE(t *testing.T) {
	token := buildCodexTestJWT("acct_test_123")
	postCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "websocket not supported", http.StatusBadRequest)
			return
		}
		postCalls++
		if req.Header.Get("OpenAI-Beta") != openAICodexResponsesBeta {
			t.Fatalf("openai-beta = %q", req.Header.Get("OpenAI-Beta"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"fallback\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	}))
	defer server.Close()

	provider := OpenAICodexProvider()
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai-codex",
		Model:    "gpt-5.5",
		Options: ChatOptions{
			APIKey:    token,
			BaseURL:   server.URL + "/backend-api",
			Stream:    true,
			Transport: "auto",
		},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "fallback" {
		t.Fatalf("text = %q", result.Text)
	}
	if postCalls != 1 {
		t.Fatalf("postCalls = %d", postCalls)
	}
}

func buildCodexTestJWT(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`))
	return header + "." + payload + ".sig"
}

func TestClampCodexReasoningEffort(t *testing.T) {
	if got := clampCodexReasoningEffort("gpt-5.5", "minimal"); got != "low" {
		t.Fatalf("got %q", got)
	}
	if got := clampCodexReasoningEffort("gpt-5.1", "xhigh"); got != "high" {
		t.Fatalf("got %q", got)
	}
	if got := clampCodexReasoningEffort("gpt-5.1-codex-mini", "low"); got != "medium" {
		t.Fatalf("got %q", got)
	}
}
