package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestRunHeadlessSessionWriteRead(t *testing.T) {
	root := t.TempDir()
	result, err := RunHeadlessSession(context.Background(), root, SessionInput{
		Prompts: []string{"write and read"},
		Turns: []AssistantTurn{
			{
				StopReason: "toolUse",
				Content: []ai.ContentBlock{{
					Type:      "toolCall",
					ID:        "write-1",
					Name:      "write",
					Arguments: map[string]any{"path": "notes.txt", "content": "hello\n"},
				}},
			},
			{
				StopReason: "toolUse",
				Content: []ai.ContentBlock{{
					Type:      "toolCall",
					ID:        "read-1",
					Name:      "read",
					Arguments: map[string]any{"path": "notes.txt"},
				}},
			},
			{StopReason: "stop", Content: []ai.ContentBlock{{Type: "text", Text: "done"}}},
		},
		ExpectedFiles: []string{"notes.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files["notes.txt"] != "hello\n" {
		t.Fatalf("notes.txt = %q", result.Files["notes.txt"])
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestResolveWorkspacePathRejectsEscape(t *testing.T) {
	if _, err := ResolveWorkspacePath(t.TempDir(), "../outside.txt"); err == nil {
		t.Fatal("expected escape error")
	}
}

func TestWorkspaceEdit(t *testing.T) {
	root := t.TempDir()
	if err := WriteWorkspaceFile(root, "notes.txt", "hello old world\n"); err != nil {
		t.Fatal(err)
	}
	if err := EditWorkspaceFile(root, "notes.txt", []WorkspaceEdit{{
		OldText: "old",
		NewText: "new",
	}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello new world\n" {
		t.Fatalf("notes.txt = %q", string(data))
	}
}

func TestWorkspaceEditMapsFuzzyMatchOffsetsToOriginalContent(t *testing.T) {
	root := t.TempDir()
	original := "prefix\u00a0\nsay \u201chello\u201d now\nsuffix\n"
	if err := WriteWorkspaceFile(root, "notes.txt", original); err != nil {
		t.Fatal(err)
	}
	if err := EditWorkspaceFile(root, "notes.txt", []WorkspaceEdit{{
		OldText: "say \"hello\" now\n",
		NewText: "done\n",
	}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "prefix\u00a0\ndone\nsuffix\n"; got != want {
		t.Fatalf("notes.txt = %q, want %q", got, want)
	}
}

func TestWorkspaceEditRejectsMissingAndAmbiguousText(t *testing.T) {
	root := t.TempDir()
	if err := WriteWorkspaceFile(root, "notes.txt", "one two one\n"); err != nil {
		t.Fatal(err)
	}
	if err := EditWorkspaceFile(root, "notes.txt", []WorkspaceEdit{{
		OldText: "missing",
		NewText: "new",
	}}); err == nil || !strings.Contains(err.Error(), "Could not find") {
		t.Fatalf("missing edit error = %v", err)
	}
	if err := EditWorkspaceFile(root, "notes.txt", []WorkspaceEdit{{
		OldText: "one",
		NewText: "new",
	}}); err == nil || !strings.Contains(err.Error(), "must be unique") {
		t.Fatalf("ambiguous edit error = %v", err)
	}
	if err := EditWorkspaceFile(root, "notes.txt", []WorkspaceEdit{{
		OldText: "one two one\n",
		NewText: "one two one\n",
	}}); err == nil || !strings.Contains(err.Error(), "No changes made to") {
		t.Fatalf("no-op edit error = %v", err)
	}
}

func TestWorkspaceEditSupportsLegacyArgumentsFromToolCall(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, []AssistantTurn{{
		StopReason: "toolUse",
		Content: []ai.ContentBlock{{
			Type:      "toolCall",
			ID:        "edit-legacy",
			Name:      "edit",
			Arguments: map[string]any{"path": "notes.txt", "oldText": "old", "newText": "new"},
		}},
	}})
	if err := WriteWorkspaceFile(root, "notes.txt", "hello old world\n"); err != nil {
		t.Fatal(err)
	}
	if err := session.Prompt(context.Background(), "legacy edit"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(session.Root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello new world\n" {
		t.Fatalf("notes.txt = %q", string(data))
	}
}

func TestParseWorkspaceEditsStringifiesLegacyArgs(t *testing.T) {
	parsed, err := parseWorkspaceEdits(map[string]any{
		"path":     "notes.txt",
		"edits":    `[{"oldText":"one","newText":"two"}]`,
		"oldText":  "legacy-old",
		"newText":  "legacy-new",
		"ignored":  true,
		"pathLike": 5,
	})
	if err != nil {
		t.Fatalf("parseWorkspaceEdits = %v", err)
	}
	if got, want := len(parsed), 2; got != want {
		t.Fatalf("len(parsed)=%d, want %d", got, want)
	}
	if parsed[0].OldText != "one" || parsed[0].NewText != "two" {
		t.Fatalf("first edit = %#v", parsed[0])
	}
	if parsed[1].OldText != "legacy-old" || parsed[1].NewText != "legacy-new" {
		t.Fatalf("second edit = %#v", parsed[1])
	}
}

func TestRunHeadlessSessionEditTool(t *testing.T) {
	root := t.TempDir()
	result, err := RunHeadlessSession(context.Background(), root, SessionInput{
		WorkspaceFiles: map[string]string{"notes.txt": "hello old world\n"},
		Prompts:        []string{"edit"},
		Turns: []AssistantTurn{{
			StopReason: "toolUse",
			Content: []ai.ContentBlock{{
				Type: "toolCall",
				ID:   "edit-1",
				Name: "edit",
				Arguments: map[string]any{
					"path": "notes.txt",
					"edits": []any{map[string]any{
						"oldText": "old",
						"newText": "new",
					}},
				},
			}},
		}},
		ExpectedFiles: []string{"notes.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files["notes.txt"] != "hello new world\n" {
		t.Fatalf("notes.txt = %q", result.Files["notes.txt"])
	}
}

func TestRunHeadlessSessionBashTool(t *testing.T) {
	root := t.TempDir()
	result, err := RunHeadlessSession(context.Background(), root, SessionInput{
		WorkspaceFiles: map[string]string{"notes.txt": "hello\n"},
		Prompts:        []string{"run bash"},
		Turns: []AssistantTurn{{
			StopReason: "toolUse",
			Content: []ai.ContentBlock{{
				Type:      "toolCall",
				ID:        "bash-1",
				Name:      "bash",
				Arguments: map[string]any{"command": "cat notes.txt"},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	last := result.Messages[len(result.Messages)-1]
	if last["role"] != "toolResult" || last["text"] != "hello\n" {
		t.Fatalf("unexpected bash result: %#v", last)
	}
}

func TestRunBashCommandNonZeroExit(t *testing.T) {
	output, exitCode, err := RunBashCommand(context.Background(), t.TempDir(), "printf nope && exit 7", 0)
	if err != nil {
		t.Fatal(err)
	}
	if output != "nope" || exitCode != 7 {
		t.Fatalf("output=%q exitCode=%d", output, exitCode)
	}
}

func TestWorkspaceLsGrepFind(t *testing.T) {
	root := t.TempDir()
	if err := WriteWorkspaceFile(root, "src/main.go", "package main\nfunc main() {}\n"); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspaceFile(root, "README.md", "hello docs\n"); err != nil {
		t.Fatal(err)
	}

	found, err := FindWorkspace(root, ".", "*.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(found, "src/main.go") {
		t.Fatalf("find output = %q", found)
	}

	fullPathGlob, err := FindWorkspace(root, ".", "src/*.go")
	if err != nil {
		t.Fatal(err)
	}
	if fullPathGlob != "src/main.go" {
		t.Fatalf("full path glob output = %q", fullPathGlob)
	}

	grep, err := GrepWorkspace(root, ".", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if grep != "README.md" {
		t.Fatalf("grep output = %q", grep)
	}

	session, err := RunHeadlessSession(context.Background(), root, SessionInput{
		Prompts: []string{"list"},
		Turns: []AssistantTurn{{
			StopReason: "toolUse",
			Content: []ai.ContentBlock{{
				Type:      "toolCall",
				ID:        "ls-1",
				Name:      "ls",
				Arguments: map[string]any{"path": "."},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	last := session.Messages[len(session.Messages)-1]
	if last["role"] != "toolResult" || !strings.Contains(last["text"].(string), "README.md") {
		t.Fatalf("unexpected ls result: %#v", last)
	}
}
