package conformance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunAgentConformanceFixtures(t *testing.T) {
	for _, name := range []string{
		"agent-basic-tool.json",
		"agent-multi-tool.json",
		"agent-missing-tool.json",
		"agent-terminate-tools.json",
		"agent-context-continue.json",
	} {
		t.Run(name, func(t *testing.T) {
			testCase, err := ReadJSON[AgentCase](filepath.Join("../../testdata/conformance", name))
			if err != nil {
				t.Fatal(err)
			}
			output, err := RunAgent(testCase)
			if err != nil {
				t.Fatal(err)
			}
			if output.Case != testCase.Name {
				t.Fatalf("case = %q, want %q", output.Case, testCase.Name)
			}
			if len(output.Messages) == 0 {
				t.Fatal("expected messages")
			}
			if len(output.Events) == 0 {
				t.Fatal("expected events")
			}
			verification := VerifyAgentConformanceOutput(testCase, output)
			if !verification.OK {
				t.Fatalf("verification errors = %#v", verification.Errors)
			}
		})
	}
}

func TestSessionFilePathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	sibling := filepath.Join(filepath.Dir(root), filepath.Base(root)+"2")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatalf("mkdir sibling = %v", err)
	}

	if _, err := sessionFilePath(root, "../"+filepath.Base(sibling)+"/outside.txt"); err == nil {
		t.Fatalf("expected traversal path to be rejected")
	}

	if _, err := sessionFilePath(root, filepath.Join("/", "etc", "passwd")); err == nil {
		t.Fatalf("expected absolute path to be rejected")
	}
}

func TestRunCodingAgentReportsMissingExpectedFileAsEmpty(t *testing.T) {
	output, err := RunCodingAgent(CodingAgentCase{
		Name:  "coding-agent-missing-expected-file",
		Model: ModelRef{Provider: "faux", ID: "faux-1"},
		Workspace: struct {
			Files map[string]string `json:"files"`
		}{
			Files: map[string]string{},
		},
		Prompts:        nil,
		AssistantTurns: nil,
		Expect: struct {
			Files map[string]string `json:"files"`
		}{
			Files: map[string]string{
				"missing.txt": "expected",
			},
		},
	})
	if err != nil {
		t.Fatalf("run coding agent = %v", err)
	}
	if got, ok := output.Files["missing.txt"]; !ok || got != "" {
		t.Fatalf("expected missing file to read as empty string, got %q (%v)", got, ok)
	}
}
