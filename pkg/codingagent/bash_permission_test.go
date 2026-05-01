package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestEvaluateBashPermissionSupportsRuleTypes(t *testing.T) {
	policy := BashPermissionPolicy{
		Mode:  BashPermissionAllowList,
		Allow: []string{"exact:go test ./...", "glob:git *", `regex:^npm (test|run)`},
	}
	for _, command := range []string{"go test ./...", "git status --short", "npm test"} {
		if decision := EvaluateBashPermission(command, policy); !decision.Allowed {
			t.Fatalf("%q denied: %#v", command, decision)
		}
	}
	if decision := EvaluateBashPermission("go env", policy); decision.Allowed {
		t.Fatalf("unexpected allow: %#v", decision)
	}
}

func TestEvaluateBashPermissionDenyWins(t *testing.T) {
	policy := BashPermissionPolicy{
		Mode:  BashPermissionAllowList,
		Allow: []string{"glob:git *"},
		Deny:  []string{"exact:git clean -fdx"},
	}
	decision := EvaluateBashPermission("git clean -fdx", policy)
	if decision.Allowed || decision.MatchedRule != "exact:git clean -fdx" || decision.MatchedRuleType != "exact" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestBashPermissionPolicyRejectsInvalidRegex(t *testing.T) {
	err := BashPermissionPolicy{Mode: BashPermissionAllowList, Allow: []string{"regex:["}}.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestEvaluateBashPermissionInvalidRuleFailsClosed(t *testing.T) {
	decision := EvaluateBashPermission("printf ok", BashPermissionPolicy{Mode: BashPermissionAllowAll, Deny: []string{"regex:["}})
	if decision.Allowed || decision.Error == "" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestEvaluateBashPermissionInvalidModeFailsClosed(t *testing.T) {
	decision := EvaluateBashPermission("printf ok", BashPermissionPolicy{Mode: "allowlist"})
	if decision.Allowed || decision.Error == "" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestEvaluateBashPermissionInvalidGlobFailsClosed(t *testing.T) {
	decision := EvaluateBashPermission("printf ok", BashPermissionPolicy{Mode: BashPermissionAllowAll, Deny: []string{"glob:["}})
	if decision.Allowed || decision.Error == "" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestRunHeadlessSessionRejectsInvalidInputBashPermission(t *testing.T) {
	_, err := RunHeadlessSession(context.Background(), t.TempDir(), SessionInput{
		BashPermission: BashPermissionPolicy{Mode: BashPermissionAllowList, Allow: []string{"regex:["}},
	})
	if err == nil {
		t.Fatal("expected invalid policy error")
	}
}

func TestSessionBashDeniedDoesNotExecute(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "side-effect.txt")
	session := NewSession(root, nil)
	if err := session.SetBashPermissionPolicy(BashPermissionPolicy{Mode: BashPermissionAllowList, Allow: []string{"exact:printf ok"}}); err != nil {
		t.Fatal(err)
	}
	result, err := session.Bash(context.Background(), "touch side-effect.txt")
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 126 || result.Permission["permissionDenied"] != true {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("command executed, stat err = %v", err)
	}
}

func TestBuiltinBashPermissionDenied(t *testing.T) {
	tools := BuiltinToolsWithOptions(t.TempDir(), BuiltinToolOptions{
		BashPermission: BashPermissionPolicy{Mode: BashPermissionAllowAll, Deny: []string{"glob:rm *"}},
	})
	for _, tool := range tools {
		if tool.Name != "bash" {
			continue
		}
		result := tool.Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{"command": "rm file.txt"}})
		if !result.IsError || result.Details["permissionDenied"] != true {
			t.Fatalf("result = %#v", result)
		}
		return
	}
	t.Fatal("missing bash tool")
}

func TestRunHeadlessSessionPreservesEnvBashPermission(t *testing.T) {
	t.Setenv("PIGO_BASH_PERMISSION_MODE", "allow-list")
	t.Setenv("PIGO_BASH_ALLOW", "exact:printf ok")

	result, err := RunHeadlessSession(context.Background(), t.TempDir(), SessionInput{
		Prompts: []string{"run bash"},
		Turns: []AssistantTurn{{
			StopReason: "toolUse",
			Content: []ai.ContentBlock{{
				Type:      "toolCall",
				ID:        "bash-1",
				Name:      "bash",
				Arguments: map[string]any{"command": "printf denied"},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range result.Messages {
		if message["role"] != "toolResult" {
			continue
		}
		if message["isError"] != true || !strings.Contains(message["text"].(string), "not in allow list") {
			t.Fatalf("message = %#v", message)
		}
		return
	}
	t.Fatalf("missing tool result: %#v", result.Messages)
}
