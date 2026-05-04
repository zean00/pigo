package codingagent

import (
	"context"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
	"github.com/badlogic/pigo/pkg/researchadapter"
)

func TestBuiltinToolsCanBeDisabled(t *testing.T) {
	tools := BuiltinToolsWithOptions(t.TempDir(), BuiltinToolOptions{
		BuiltinToolPolicy: BuiltinToolPolicy{Disabled: []string{"bash", "write"}},
	})
	names := toolNames(tools)
	if containsString(names, "bash") || containsString(names, "write") {
		t.Fatalf("tools = %#v", names)
	}
	if !containsString(names, "read") {
		t.Fatalf("tools = %#v", names)
	}
}

func TestBuiltinToolEnabledListLimitsTools(t *testing.T) {
	specs := BuiltinToolSpecsWithPolicy(BuiltinToolPolicy{Enabled: []string{"read", "grep"}})
	names := specNames(specs)
	if len(names) != 2 || !containsString(names, "read") || !containsString(names, "grep") {
		t.Fatalf("specs = %#v", names)
	}
}

func TestBuiltinToolPolicyRejectsUnknownTool(t *testing.T) {
	if err := (BuiltinToolPolicy{Disabled: []string{"unknown"}}).Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSessionToolSpecsRespectBuiltinToolPolicy(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.SetBuiltinToolPolicy(BuiltinToolPolicy{Enabled: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	names := specNames(session.toolSpecs())
	if len(names) != 1 || names[0] != "read" {
		t.Fatalf("specs = %#v", names)
	}
}

func TestRunHeadlessSessionRejectsInvalidBuiltinToolPolicy(t *testing.T) {
	_, err := RunHeadlessSession(context.Background(), t.TempDir(), SessionInput{
		BuiltinToolPolicy: BuiltinToolPolicy{Disabled: []string{"unknown"}},
	})
	if err == nil {
		t.Fatal("expected invalid policy error")
	}
}

func TestBuiltinToolPolicyFromEnv(t *testing.T) {
	t.Setenv("PIGO_BUILTIN_TOOLS", "read,grep")
	t.Setenv("PIGO_DISABLED_BUILTIN_TOOLS", "grep")
	policy := BuiltinToolPolicyFromEnv()
	if !policy.ToolEnabled("read") || policy.ToolEnabled("grep") || policy.ToolEnabled("bash") {
		t.Fatalf("policy = %#v", policy)
	}
}

func TestSessionResearchToolsAreOptIn(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if containsString(specNames(session.toolSpecs()), "search") {
		t.Fatalf("research tool exposed by default: %#v", specNames(session.toolSpecs()))
	}
	if err := session.SetResearchConfig(researchadapter.Config{Tools: []string{"search", "scrape"}}); err != nil {
		t.Fatal(err)
	}
	names := specNames(session.toolSpecs())
	if !containsString(names, "search") || !containsString(names, "scrape") || containsString(names, "security_search") {
		t.Fatalf("specs = %#v", names)
	}
}

func TestSessionResearchToolRunsWithSafeSubAgentTools(t *testing.T) {
	seenTools := []string{}
	ai.RegisterProvider("codingagent-research-test-provider", sessionResearchProviderFunc(func(_ context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
		for _, tool := range req.Tools {
			seenTools = append(seenTools, tool.Name)
		}
		for _, forbidden := range []string{"bash", "write", "edit", "ls", "find", "research"} {
			if containsString(seenTools, forbidden) {
				t.Fatalf("forbidden research sub-agent tool exposed: %s in %#v", forbidden, seenTools)
			}
		}
		for _, required := range []string{"read", "grep", "search", "scrape", "security_search"} {
			if !containsString(seenTools, required) {
				t.Fatalf("missing research sub-agent tool %q in %#v", required, seenTools)
			}
		}
		return sessionResearchTextResult("session research report"), ai.AssistantEvents([]ai.ContentBlock{{Type: "text", Text: "session research report"}}, "stop"), nil
	}))
	session := NewSession(t.TempDir(), nil)
	session.Provider = "codingagent-research-test-provider"
	session.ModelID = "codingagent-research-test-model"
	if err := session.SetResearchConfig(researchadapter.Config{Tools: []string{"research"}}); err != nil {
		t.Fatal(err)
	}
	var researchTool agentcore.Tool
	for _, tool := range session.builtinTools() {
		if tool.Name == "research" {
			researchTool = tool
			break
		}
	}
	if researchTool.Name == "" {
		t.Fatal("research tool not exposed")
	}
	result := researchTool.Execute(context.Background(), ai.ContentBlock{ID: "research-call-1", Arguments: map[string]any{"query": "session research"}})
	if result.IsError || !strings.Contains(result.Text, "session research report") {
		t.Fatalf("result = %#v", result)
	}
	hasProgress := false
	for _, event := range session.Events {
		if event["type"] == "research_progress" && event["toolCallId"] == "research-call-1" {
			hasProgress = true
			break
		}
	}
	if !hasProgress {
		t.Fatalf("events = %#v", session.Events)
	}
}

func TestRunHeadlessSessionRejectsInvalidResearchTool(t *testing.T) {
	_, err := RunHeadlessSession(context.Background(), t.TempDir(), SessionInput{
		ResearchConfig: researchadapter.Config{Tools: []string{"unknown"}},
	})
	if err == nil {
		t.Fatal("expected invalid research tool error")
	}
}

type sessionResearchProviderFunc func(context.Context, ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error)

func (fn sessionResearchProviderFunc) Complete(ctx context.Context, req ai.CompletionRequest) (ai.NormalizedResult, []ai.NormalizedEvent, error) {
	return fn(ctx, req)
}

func sessionResearchTextResult(text string) ai.NormalizedResult {
	return ai.NormalizedResult{
		Role:       "assistant",
		StopReason: "stop",
		Text:       text,
		Content:    []any{map[string]any{"type": "text", "text": text}},
		Usage:      &ai.Usage{Input: 1, Output: 1, TotalTokens: 2},
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func toolNames(tools []agentcore.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func specNames(specs []ai.Tool) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}
