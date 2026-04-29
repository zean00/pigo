package conformance

import (
	"context"
	"testing"

	"github.com/badlogic/pigo/pkg/codingagent"
)

func TestNewCodingAgentSessionFromCase(t *testing.T) {
	testCase, err := ReadJSON[CodingAgentCase]("../../testdata/conformance/coding-agent-headless-write-read.json")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	session, err := NewCodingAgentSessionFromCase(root, testCase)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Turns) != 3 {
		t.Fatalf("turns len = %d", len(session.Turns))
	}
	if err := session.Prompt(context.Background(), testCase.Prompts[0]); err != nil {
		t.Fatal(err)
	}
	path, err := codingagent.ResolveWorkspacePath(root, "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("empty resolved path")
	}
}
