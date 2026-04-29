package conformance

import (
	"os"
	"path/filepath"
	"testing"
)

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
