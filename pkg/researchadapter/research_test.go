package researchadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
)

type testResearchHost struct {
	root string
}

func (h testResearchHost) Root() string { return h.root }

func (h testResearchHost) Model() (string, string) {
	return "research-test-provider", "research-test-model"
}

func (h testResearchHost) APIKey(context.Context, string) string { return "test-key" }

func (h testResearchHost) WorkspaceTools() ([]agentcore.Tool, []ai.Tool) {
	return []agentcore.Tool{{Name: "read", Execute: func(context.Context, ai.ContentBlock) agentcore.ToolResult {
		return agentcore.ToolResult{Text: "read ok"}
	}}, {Name: "grep", Execute: func(context.Context, ai.ContentBlock) agentcore.ToolResult {
		return agentcore.ToolResult{Text: "grep ok"}
	}}}, []ai.Tool{{Name: "read"}, {Name: "grep"}}
}

func TestResearchToolQuickModeUsesIsolatedSafeTools(t *testing.T) {
	seenTools := []string{}
	ai.RegisterProvider("research-test-provider", researchProviderFunc(func(_ context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		for _, tool := range req.Tools {
			seenTools = append(seenTools, tool.Name)
		}
		for _, forbidden := range []string{"bash", "write", "edit", "research"} {
			if containsName(seenTools, forbidden) {
				t.Fatalf("forbidden tool exposed: %s in %#v", forbidden, seenTools)
			}
		}
		if !containsName(seenTools, "read") || !containsName(seenTools, "grep") || !containsName(seenTools, "search") {
			t.Fatalf("tools = %#v", seenTools)
		}
		return textResult("report with sources"), ai.AssistantEvents([]ai.ContentBlock{{Type: "text", Text: "report with sources"}}, "stop"), nil
	}))
	events := []agentcore.Event{}
	tools, _ := Tools(Config{
		Tools:     []string{"research"},
		Host:      testResearchHost{root: t.TempDir()},
		EventSink: func(event agentcore.Event) { events = append(events, event) },
	})
	result := tools[0].Execute(context.Background(), ai.ContentBlock{ID: "research-1", Arguments: map[string]any{"query": "test topic"}})
	if result.IsError || !strings.Contains(result.Text, "report with sources") {
		t.Fatalf("result = %#v", result)
	}
	if result.Details["mode"] != "quick" {
		t.Fatalf("details = %#v", result.Details)
	}
	if len(events) == 0 {
		t.Fatal("expected research progress events")
	}
	if events[0]["toolCallId"] != "research-1" {
		t.Fatalf("events = %#v", events)
	}
}

func TestResearchToolRejectsDeepMode(t *testing.T) {
	tools, _ := Tools(Config{Tools: []string{"research"}, Host: testResearchHost{root: t.TempDir()}})
	result := tools[0].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{"query": "test", "depth": float64(1)}})
	if !result.IsError || !strings.Contains(result.Text, "depth 0") {
		t.Fatalf("result = %#v", result)
	}
}

type researchProviderFunc func(context.Context, ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error)

func (fn researchProviderFunc) Complete(ctx context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
	return fn(ctx, req)
}

func textResult(text string) ai.NormalizedResult {
	return ai.NormalizedResult{
		Role:       "assistant",
		StopReason: "stop",
		Text:       text,
		Content:    []any{map[string]any{"type": "text", "text": text}},
		Usage:      &ai.Usage{Input: 1, Output: 1, TotalTokens: 2},
	}
}

func containsName(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
