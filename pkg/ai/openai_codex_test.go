package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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
			APIKey:  token,
			BaseURL: server.URL + "/backend-api",
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

func buildCodexTestJWT(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`))
	return header + "." + payload + ".sig"
}
