package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestSessionProviderLoopWritesFileThroughTool(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		callCount++
		var payload ai.CompletionRequest
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.Model != "test-model" {
			t.Fatalf("model = %q", payload.Model)
		}
		if callCount == 1 {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{
				"choices": [{
					"message": {
						"role": "assistant",
						"tool_calls": [{
							"id":"tool-write-1",
							"type":"function",
							"function": {
								"name": "write",
								"arguments": "{\"path\":\"notes.txt\",\"content\":\"hello\"}"
							}
						}]
					},
					"finish_reason": "tool_calls"
				}]
			}`)
			return
		}

		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{
				"choices": [{
					"message": {
						"role": "assistant",
						"content": "wrote file"
					},
					"finish_reason": "stop"
				}]
		}`)
	}))
	defer server.Close()
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	root := t.TempDir()
	session := NewSession(root, nil)
	if _, err := session.SetModel("openai", "test-model"); err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "make notes"); err != nil {
		t.Fatal(err)
	}

	if callCount != 2 {
		t.Fatalf("openai calls = %d", callCount)
	}

	content, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(content)) != "hello" {
		t.Fatalf("notes content = %q", string(content))
	}

	if len(session.Messages) != 4 {
		t.Fatalf("message count = %d", len(session.Messages))
	}
}

func TestSessionProviderLoopUsesStoredOAuthCredentials(t *testing.T) {
	const providerName = "custom-oauth-provider"
	refreshCalls := 0
	ai.RegisterProvider(providerName, providerFunc(func(_ context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		if got := req.Options.APIKey; got != "fresh-token" {
			t.Fatalf("api key = %q", got)
		}
		return ai.NormalizedResult{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "ok",
			Content:    []any{map[string]any{"type": "text", "text": "ok"}},
		}, []ai.NormalizedEvent{{Type: "start"}, {Type: "text_start", ContentIdx: 0}, {Type: "text_delta", ContentIdx: 0, Delta: "ok"}, {Type: "text_end", ContentIdx: 0, Content: "ok"}, {Type: "done", Reason: "stop"}}, nil
	}))
	ai.RegisterOAuthProvider(testOAuthProvider{
		id:   providerName,
		name: "Custom OAuth",
		refresh: func(ctx context.Context, credentials ai.OAuthCredentials) (ai.OAuthCredentials, error) {
			refreshCalls++
			credentials.Access = "fresh-token"
			credentials.Expires = time.Now().Add(time.Hour).UnixMilli()
			return credentials, nil
		},
		getAPIKey: func(credentials ai.OAuthCredentials) string {
			return credentials.Access
		},
	})
	defer ai.UnregisterOAuthProvider(providerName)

	root := t.TempDir()
	session := NewSession(root, nil)
	if _, err := session.SetModel(providerName, "test-model"); err != nil {
		t.Fatal(err)
	}
	if err := session.SetOAuthCredentials(providerName, ai.OAuthCredentials{
		Refresh:   "refresh-token",
		Access:    "expired-token",
		Expires:   time.Now().Add(-time.Hour).UnixMilli(),
		ProjectID: "project-1",
	}); err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	if refreshCalls != 1 {
		t.Fatalf("refreshCalls = %d", refreshCalls)
	}
	if got := session.OAuthCredentials[providerName].Access; got != "fresh-token" {
		t.Fatalf("stored access token = %q", got)
	}
}

func TestSessionProviderLoopForwardsThinkingLevel(t *testing.T) {
	const providerName = "thinking-provider"
	ai.RegisterProvider(providerName, providerFunc(func(_ context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		if got := req.Options.ReasoningEffort; got != "high" {
			t.Fatalf("reasoning effort = %q", got)
		}
		return ai.NormalizedResult{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "ok",
			Content:    []any{map[string]any{"type": "text", "text": "ok"}},
		}, []ai.NormalizedEvent{{Type: "start"}, {Type: "text_start", ContentIdx: 0}, {Type: "text_delta", ContentIdx: 0, Delta: "ok"}, {Type: "text_end", ContentIdx: 0, Content: "ok"}, {Type: "done", Reason: "stop"}}, nil
	}))

	root := t.TempDir()
	session := NewSession(root, nil)
	session.ThinkingLevel = "high"
	if _, err := session.SetModel(providerName, "test-model"); err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionProviderLoopInjectsSkillContext(t *testing.T) {
	const providerName = "skill-context-provider"
	var captured []ai.Message
	ai.RegisterProvider(providerName, providerFunc(func(_ context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		captured = append([]ai.Message(nil), req.Messages...)
		return ai.NormalizedResult{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "ok",
			Content:    []any{map[string]any{"type": "text", "text": "ok"}},
		}, []ai.NormalizedEvent{{Type: "start"}, {Type: "text_start", ContentIdx: 0}, {Type: "text_delta", ContentIdx: 0, Delta: "ok"}, {Type: "text_end", ContentIdx: 0, Content: "ok"}, {Type: "done", Reason: "stop"}}, nil
	}))

	root := t.TempDir()
	skillDir := filepath.Join(root, ".pi", "skills", "review-code")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("---\nname: review-code\ndescription: Review code carefully\n---\nUse this skill.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session := NewSession(root, nil)
	session.LoadSlashCommandResources(ResourceLoadOptions{IncludeDefaults: true})
	if _, err := session.SetModel(providerName, "test-model"); err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "inspect code"); err != nil {
		t.Fatal(err)
	}
	if len(captured) < 2 {
		t.Fatalf("captured messages = %#v", captured)
	}
	if captured[0].Role != "user" || !strings.Contains(ai.MessageText(captured[0]), "<available_skills>") {
		t.Fatalf("missing skill context: %#v", captured[0])
	}
	if !strings.Contains(ai.MessageText(captured[0]), skillPath) {
		t.Fatalf("missing skill path in context: %#v", captured[0])
	}
	if captured[len(captured)-1].Content != "inspect code" {
		t.Fatalf("last prompt = %#v", captured[len(captured)-1])
	}
}

type providerFunc func(context.Context, ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error)

func (fn providerFunc) Complete(ctx context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
	return fn(ctx, req)
}

type testOAuthProvider struct {
	id        string
	name      string
	refresh   func(context.Context, ai.OAuthCredentials) (ai.OAuthCredentials, error)
	getAPIKey func(ai.OAuthCredentials) string
}

func (p testOAuthProvider) ID() string               { return p.id }
func (p testOAuthProvider) Name() string             { return p.name }
func (p testOAuthProvider) UsesCallbackServer() bool { return false }
func (p testOAuthProvider) Login(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{Access: "login-token"}, nil
}
func (p testOAuthProvider) RefreshToken(ctx context.Context, credentials ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return p.refresh(ctx, credentials)
}
func (p testOAuthProvider) GetAPIKey(credentials ai.OAuthCredentials) string {
	return p.getAPIKey(credentials)
}
