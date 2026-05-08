package codingagent

import (
	"context"
	"testing"

	"github.com/badlogic/pigo/pkg/a2a"
	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
)

func TestA2AModuleRegistersRemoteAgentTool(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.SetA2AConfig(a2a.Config{
		Enabled: true,
		Agents: []a2a.RemoteAgent{{
			Name:          "Remote One",
			URL:           "http://localhost:9999/a2a",
			AllowInsecure: true,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	_, specs := session.ensureModuleRegistry().Tools()
	found := false
	for _, spec := range specs {
		if spec.Name == "a2a__remote_one__send_message" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a2a remote tool, got %#v", specs)
	}
}

func TestPromptInjectionSourceRecognizesA2ATools(t *testing.T) {
	if got := promptInjectionToolSource("a2a__remote__send_message"); got != "a2a" {
		t.Fatalf("unexpected source %q", got)
	}
	config := PromptInjectionConfig{Mode: PromptInjectionGuardEnforce}.Normalized()
	if !config.SourceEnabled("a2a") {
		t.Fatalf("a2a should be included in default guarded sources")
	}
	if !config.SensitiveTool("a2a__remote__send_message", "a2a") {
		t.Fatalf("a2a tools should be sensitive by default in enforce mode")
	}
}

func TestA2AToolResultCanBeWrappedAsUntrusted(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.SetPromptInjectionConfig(PromptInjectionConfig{Mode: PromptInjectionGuardAnnotate, Sources: []string{"a2a"}}); err != nil {
		t.Fatal(err)
	}
	result, err := session.applyToolResultHooks(context.Background(), agentcore.AfterToolCallContext{
		ToolCall: ai.ContentBlock{Name: "a2a__remote__send_message"},
		Result:   agentcore.ToolResult{Text: "remote output"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Details["untrustedSource"] != "a2a" {
		t.Fatalf("expected a2a untrusted metadata, got %#v", result.Details)
	}
}
