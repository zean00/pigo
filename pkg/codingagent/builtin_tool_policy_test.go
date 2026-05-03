package codingagent

import (
	"context"
	"testing"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
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
