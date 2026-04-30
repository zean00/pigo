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

	"github.com/badlogic/pigo/pkg/agentcore"
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

func TestSessionContextHandlersTransformProviderMessages(t *testing.T) {
	const providerName = "context-hook-provider"
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

	session := NewSession(t.TempDir(), nil)
	if _, err := session.SetModel(providerName, "test-model"); err != nil {
		t.Fatal(err)
	}
	session.RegisterContextHandler(func(ctx context.Context, event ContextEvent) (ContextResult, error) {
		if event.Type != "context" || len(event.Messages) == 0 {
			t.Fatalf("event = %#v", event)
		}
		messages := append([]ai.Message{{Role: "system", Content: "context hook"}}, event.Messages...)
		return ContextResult{Messages: messages}, nil
	})

	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if len(captured) < 2 || captured[0].Role != "system" || captured[0].Content != "context hook" {
		t.Fatalf("captured = %#v", captured)
	}
}

func TestSessionProviderPayloadAndResponseHooks(t *testing.T) {
	const providerName = "provider-hook-provider"
	ai.RegisterProvider(providerName, providerFunc(func(_ context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		payload, err := req.Options.OnPayload(map[string]any{"value": "original"}, req)
		if err != nil {
			return ai.NormalizedResult{}, nil, err
		}
		if payload.(map[string]any)["value"] != "changed" {
			t.Fatalf("payload = %#v", payload)
		}
		if err := req.Options.OnResponse(ai.ProviderResponse{Status: 202, Headers: map[string]string{"x-test": "ok"}}, req); err != nil {
			return ai.NormalizedResult{}, nil, err
		}
		return ai.NormalizedResult{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "ok",
			Content:    []any{map[string]any{"type": "text", "text": "ok"}},
		}, []ai.NormalizedEvent{{Type: "start"}, {Type: "text_start", ContentIdx: 0}, {Type: "text_delta", ContentIdx: 0, Delta: "ok"}, {Type: "text_end", ContentIdx: 0, Content: "ok"}, {Type: "done", Reason: "stop"}}, nil
	}))

	session := NewSession(t.TempDir(), nil)
	if _, err := session.SetModel(providerName, "test-model"); err != nil {
		t.Fatal(err)
	}
	payloadSeen := false
	responseSeen := false
	session.RegisterProviderPayloadHandler(func(ctx context.Context, payload any, req ai.CompletionRequest) (any, error) {
		payloadSeen = true
		if req.Provider != providerName {
			t.Fatalf("payload provider = %q", req.Provider)
		}
		return map[string]any{"value": "changed"}, nil
	})
	session.RegisterProviderResponseHandler(func(ctx context.Context, response ai.ProviderResponse, req ai.CompletionRequest) error {
		responseSeen = true
		if response.Status != 202 || response.Headers["x-test"] != "ok" {
			t.Fatalf("response = %#v", response)
		}
		return nil
	})

	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if !payloadSeen || !responseSeen {
		t.Fatalf("payloadSeen=%v responseSeen=%v", payloadSeen, responseSeen)
	}
}

func TestSessionToolCallAndResultHooks(t *testing.T) {
	const providerName = "tool-hook-provider"
	callCount := 0
	var secondRequest []ai.Message
	ai.RegisterProvider(providerName, providerFunc(func(_ context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		callCount++
		if callCount == 1 {
			return ai.NormalizedResult{
				Role:       "assistant",
				StopReason: "toolUse",
				Content: []any{map[string]any{
					"type":      "toolCall",
					"id":        "call-1",
					"name":      "echo",
					"arguments": map[string]any{"value": "original"},
				}},
			}, nil, nil
		}
		secondRequest = append([]ai.Message(nil), req.Messages...)
		return ai.NormalizedResult{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "done",
			Content:    []any{map[string]any{"type": "text", "text": "done"}},
		}, nil, nil
	}))

	session := NewSession(t.TempDir(), nil)
	if _, err := session.SetModel(providerName, "test-model"); err != nil {
		t.Fatal(err)
	}
	session.RegisterExtensionTool(agentcore.Tool{
		Name: "echo",
		Execute: func(_ context.Context, call ai.ContentBlock) agentcore.ToolResult {
			return agentcore.ToolResult{Text: "tool:" + call.Arguments["value"].(string)}
		},
	}, ai.Tool{Name: "echo", Parameters: map[string]any{"type": "object"}})
	session.RegisterToolCallHandler(func(ctx context.Context, event ToolCallEvent) (ToolCallResult, error) {
		if event.ToolName != "echo" || event.ToolCallID != "call-1" || event.Input["value"] != "original" {
			t.Fatalf("tool call event = %#v", event)
		}
		return ToolCallResult{Input: map[string]any{"value": "changed"}}, nil
	})
	text := "hooked"
	session.RegisterToolResultHandler(func(ctx context.Context, event ToolResultEvent) (ToolResultPatch, error) {
		if event.Text != "tool:changed" || event.Input["value"] != "changed" {
			t.Fatalf("tool result event = %#v", event)
		}
		return ToolResultPatch{Text: &text}, nil
	})

	if err := session.Prompt(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	if len(secondRequest) == 0 {
		t.Fatal("missing second provider request")
	}
	found := false
	for _, message := range secondRequest {
		if message.Role == "toolResult" && message.Content == "hooked" {
			found = true
		}
	}
	if !found {
		t.Fatalf("second request = %#v", secondRequest)
	}
}

func TestSessionBeforeAgentStartInjectsMessageAndSystemPrompt(t *testing.T) {
	const providerName = "before-agent-start-provider"
	var captured []ai.Message
	ai.RegisterProvider(providerName, providerFunc(func(_ context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		captured = append([]ai.Message(nil), req.Messages...)
		return ai.NormalizedResult{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "ok",
			Content:    []any{map[string]any{"type": "text", "text": "ok"}},
		}, nil, nil
	}))

	session := NewSession(t.TempDir(), nil)
	if _, err := session.SetModel(providerName, "test-model"); err != nil {
		t.Fatal(err)
	}
	session.RegisterBeforeAgentStartHandler(func(ctx context.Context, event BeforeAgentStartEvent) (BeforeAgentStartResult, error) {
		if event.Type != "before_agent_start" || event.Prompt != "hello" || !strings.Contains(event.SystemPrompt, "headless coding agent") {
			t.Fatalf("event = %#v", event)
		}
		systemPrompt := event.SystemPrompt + "\nextra instructions"
		return BeforeAgentStartResult{
			Message:      agentcore.NewCustomMessage("before-start", "injected", true, map[string]any{"source": "test"}),
			SystemPrompt: &systemPrompt,
		}, nil
	})

	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if len(captured) < 3 || !strings.Contains(fmt.Sprint(captured[0].Content), "extra instructions") {
		t.Fatalf("captured = %#v", captured)
	}
	foundInjected := false
	for _, message := range captured {
		if message.Role != "user" {
			continue
		}
		if message.Content == "injected" {
			foundInjected = true
		}
		if blocks, ok := message.Content.([]ai.ContentBlock); ok && len(blocks) == 1 && blocks[0].Text == "injected" {
			foundInjected = true
		}
	}
	if !foundInjected {
		t.Fatalf("captured = %#v", captured)
	}
	if len(session.Messages) == 0 || session.Messages[0]["role"] != "custom" || session.Messages[0]["customType"] != "before-start" {
		t.Fatalf("messages = %#v", session.Messages)
	}
}

func TestSessionUsesOAuthModelMutations(t *testing.T) {
	const providerName = "mutating-oauth-provider"
	ai.RegisterModel(ai.Model{
		Provider: providerName,
		ID:       "before-oauth",
		API:      "x-test",
	})
	defer ai.ClearProviderModels(providerName)

	ai.RegisterOAuthProvider(testOAuthProvider{
		id:   providerName,
		name: "Mutating OAuth",
		refresh: func(ctx context.Context, credentials ai.OAuthCredentials) (ai.OAuthCredentials, error) {
			return credentials, nil
		},
		getAPIKey: func(credentials ai.OAuthCredentials) string {
			return credentials.Access
		},
		modifyModels: func(models []ai.Model, _ ai.OAuthCredentials) []ai.Model {
			out := make([]ai.Model, 0, len(models))
			for _, model := range models {
				if model.Provider == providerName && model.ID == "before-oauth" {
					model.ID = "after-oauth"
				}
				out = append(out, model)
			}
			return out
		},
	})
	defer ai.UnregisterOAuthProvider(providerName)

	session := NewSession(t.TempDir(), nil)
	if !hasSessionModel(session.AvailableModels, providerName, "before-oauth") {
		t.Fatalf("expected base model before oauth mutation: %#v", session.AvailableModels)
	}

	if err := session.SetOAuthCredentials(providerName, ai.OAuthCredentials{Access: "token"}); err != nil {
		t.Fatal(err)
	}

	if hasSessionModel(session.AvailableModels, providerName, "before-oauth") {
		t.Fatalf("expected oauth model mutation to replace before-oauth model")
	}
	if !hasSessionModel(session.AvailableModels, providerName, "after-oauth") {
		t.Fatalf("expected mutated model after oauth credentials: %#v", session.AvailableModels)
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

func TestSessionProviderLoopInjectsHeadlessSystemContext(t *testing.T) {
	const providerName = "headless-system-context-provider"
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
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession(root, nil)
	if _, err := session.SetModel(providerName, "test-model"); err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if len(captured) == 0 || captured[0].Role != "system" {
		t.Fatalf("captured messages = %#v", captured)
	}
	system, _ := captured[0].Content.(string)
	if !strings.Contains(system, "headless coding agent") || !strings.Contains(system, "Workspace: "+root) || !strings.Contains(system, "go.mod") {
		t.Fatalf("system context = %q", system)
	}
}

func TestSessionProviderLoopInjectsProjectContextFiles(t *testing.T) {
	const providerName = "project-context-provider"
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
	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("parent instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "CLAUDE.md"), []byte("child instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession(child, nil)
	if _, err := session.SetModel(providerName, "test-model"); err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if len(captured) == 0 || captured[0].Role != "system" {
		t.Fatalf("captured messages = %#v", captured)
	}
	system, _ := captured[0].Content.(string)
	if !strings.Contains(system, "Project context files:") || !strings.Contains(system, "parent instructions") || !strings.Contains(system, "child instructions") {
		t.Fatalf("system context = %q", system)
	}
	if strings.Index(system, filepath.Join(parent, "AGENTS.md")) > strings.Index(system, filepath.Join(child, "CLAUDE.md")) {
		t.Fatalf("context file order = %q", system)
	}
}

func TestSessionPromptWithAttachmentsForwardsTextAndImageBlocks(t *testing.T) {
	const providerName = "attachment-provider"
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
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("attached text"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession(root, nil)
	if _, err := session.SetModel(providerName, "test-model"); err != nil {
		t.Fatal(err)
	}

	if err := session.PromptWithAttachments(context.Background(), "inspect", []PromptAttachment{
		{Type: "file", Path: "notes.txt"},
		{Type: "image", Data: "aW1n", MimeType: "image/png"},
	}); err != nil {
		t.Fatal(err)
	}
	last := captured[len(captured)-1]
	content, ok := last.Content.([]any)
	if !ok || len(content) != 3 {
		t.Fatalf("content = %#v", last.Content)
	}
	if !strings.Contains(content[1].(map[string]any)["text"].(string), "attached text") {
		t.Fatalf("file block = %#v", content[1])
	}
	if content[2].(map[string]any)["type"] != "image" {
		t.Fatalf("image block = %#v", content[2])
	}
}

func TestSessionAutoRetryRetriesProviderFailureOnce(t *testing.T) {
	const providerName = "auto-retry-provider"
	calls := 0
	ai.RegisterProvider(providerName, providerFunc(func(_ context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		calls++
		if calls == 1 {
			return ai.NormalizedResult{}, nil, fmt.Errorf("transient")
		}
		return ai.NormalizedResult{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "ok",
			Content:    []any{map[string]any{"type": "text", "text": "ok"}},
		}, []ai.NormalizedEvent{{Type: "start"}, {Type: "text_start", ContentIdx: 0}, {Type: "text_delta", ContentIdx: 0, Delta: "ok"}, {Type: "text_end", ContentIdx: 0, Content: "ok"}, {Type: "done", Reason: "stop"}}, nil
	}))

	session := NewSession(t.TempDir(), nil)
	session.AutoRetry = true
	if _, err := session.SetModel(providerName, "test-model"); err != nil {
		t.Fatal(err)
	}
	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestSessionCompactWithModelUsesConfiguredProvider(t *testing.T) {
	const providerName = "compaction-provider"
	var captured ai.CompletionRequest
	ai.RegisterProvider(providerName, providerFunc(func(_ context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		captured = req
		return ai.NormalizedResult{
			Role:       "assistant",
			StopReason: "stop",
			Text:       "provider summary",
			Content:    []any{map[string]any{"type": "text", "text": "provider summary"}},
		}, []ai.NormalizedEvent{{Type: "done", Reason: "stop"}}, nil
	}))

	session := NewSession(t.TempDir(), nil)
	if _, err := session.SetModel(providerName, "compact-model"); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{Type: "message", Message: map[string]any{
		"role": "user", "text": "existing turn",
	}}); err != nil {
		t.Fatal(err)
	}

	result := session.CompactWithModel(context.Background(), "keep decisions")
	if result.Summary != "provider summary" {
		t.Fatalf("summary = %q", result.Summary)
	}
	if captured.Provider != providerName || captured.Model != "compact-model" {
		t.Fatalf("request = %#v", captured)
	}
	if len(captured.Messages) < 3 || captured.Messages[0].Role != "system" || captured.Messages[1].Role != "user" {
		t.Fatalf("messages = %#v", captured.Messages)
	}
	if !strings.Contains(ai.MessageText(captured.Messages[1]), "keep decisions") {
		t.Fatalf("compaction prompt = %#v", captured.Messages[1])
	}
}

type providerFunc func(context.Context, ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error)

func (fn providerFunc) Complete(ctx context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
	return fn(ctx, req)
}

type testOAuthProvider struct {
	id           string
	name         string
	refresh      func(context.Context, ai.OAuthCredentials) (ai.OAuthCredentials, error)
	getAPIKey    func(ai.OAuthCredentials) string
	modifyModels func([]ai.Model, ai.OAuthCredentials) []ai.Model
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

func (p testOAuthProvider) ModifyModels(models []ai.Model, credentials ai.OAuthCredentials) []ai.Model {
	if p.modifyModels == nil {
		return models
	}
	return p.modifyModels(models, credentials)
}

func hasSessionModel(models []ModelInfo, provider, modelID string) bool {
	for _, model := range models {
		if model.Provider == provider && model.ModelID == modelID {
			return true
		}
	}
	return false
}
