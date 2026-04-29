package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestNewSessionWithParentWritesHeader(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	parentPath := filepath.Join(t.TempDir(), "parent-session.jsonl")
	session.Store = NewSessionStore(filepath.Join(t.TempDir(), "seed", "session.jsonl"))
	session.NewSessionWithParent(parentPath)

	if session.Store == nil || session.Store.Path == "" {
		t.Fatal("store path not set")
	}
	entries, err := session.Store.ReadEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("session header entries = %d", len(entries))
	}
	if entries[0].Type != "session" {
		t.Fatalf("session header type = %q", entries[0].Type)
	}
	if entries[0].ParentSession != parentPath {
		t.Fatalf("parentSession = %q", entries[0].ParentSession)
	}
}

func TestNewSessionSeedsAvailableModels(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if len(session.AvailableModels) == 0 {
		t.Fatal("expected default available models")
	}
	modelsByProvider := map[string]struct{}{}
	for _, model := range session.AvailableModels {
		modelsByProvider[model.Provider+"/"+model.ModelID] = struct{}{}
	}
	var expected int
	for _, expectedModel := range ai.DefaultModels() {
		key := expectedModel.Provider + "/" + expectedModel.ModelID
		if _, ok := modelsByProvider[key]; !ok {
			t.Fatalf("missing default model %s", key)
		}
		expected++
	}
	if len(session.AvailableModels) < expected {
		t.Fatalf("expected at least %d models, got %d", expected, len(session.AvailableModels))
	}
}

func TestSessionBranchAndTree(t *testing.T) {
	session := NewSession(t.TempDir(), nil)

	appendNode := func(id, parentID, role, text string, when time.Time) {
		if err := session.appendEntry(SessionEntry{
			ID:        id,
			Type:      "message",
			ParentID:  parentID,
			Timestamp: when.Format(time.RFC3339Nano),
			Message: map[string]any{
				"role":    role,
				"text":    text,
				"content": []any{map[string]any{"type": "text", "text": text}},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	appendNode("root-user", "", "user", "first", time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC))
	appendNode("root-assistant", "root-user", "assistant", "answer", time.Date(2024, 1, 1, 10, 1, 0, 0, time.UTC))
	appendNode("branch-user", "root-assistant", "user", "second", time.Date(2024, 1, 1, 10, 2, 0, 0, time.UTC))
	appendNode("parallel-user", "root-user", "user", "parallel", time.Date(2024, 1, 1, 10, 1, 30, 0, time.UTC))
	appendNode("parallel-asst", "parallel-user", "assistant", "parallel answer", time.Date(2024, 1, 1, 10, 2, 30, 0, time.UTC))

	tree := session.Tree()
	if len(tree) != 1 {
		t.Fatalf("tree roots = %d", len(tree))
	}
	if tree[0].ID != "root-user" {
		t.Fatalf("root id = %q", tree[0].ID)
	}
	if len(tree[0].Children) != 2 {
		t.Fatalf("children = %d", len(tree[0].Children))
	}
	if tree[0].Children[0].ID != "root-assistant" {
		t.Fatalf("first child = %q", tree[0].Children[0].ID)
	}
	if tree[0].Children[1].ID != "parallel-user" {
		t.Fatalf("second child = %q", tree[0].Children[1].ID)
	}

	if err := session.Branch("root-assistant"); err != nil {
		t.Fatal(err)
	}
	if len(session.Messages) != 2 {
		t.Fatalf("branched message count = %d", len(session.Messages))
	}
	if err := session.Branch("root"); err != nil {
		t.Fatal(err)
	}
	if len(session.Messages) != 0 {
		t.Fatalf("root branch message count = %d", len(session.Messages))
	}
}

func TestExportToJSONLLinearizesCurrentBranch(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, nil)
	appendNode := func(id, parentID, role, text string) {
		if err := session.appendEntry(SessionEntry{
			ID:       id,
			Type:     "message",
			ParentID: parentID,
			Message: map[string]any{
				"role": role,
				"text": text,
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	appendNode("u1", "", "user", "first")
	appendNode("a1", "u1", "assistant", "answer")
	appendNode("u2", "a1", "user", "second")
	appendNode("u1b", "u1", "user", "parallel")
	appendNode("a1b", "u1b", "assistant", "parallel answer")
	if err := session.Branch("a1"); err != nil {
		t.Fatal(err)
	}

	path, err := session.ExportToJSONL(filepath.Join(root, "branch.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	imported := NewSession(root, nil)
	if err := imported.SwitchSession(path); err != nil {
		t.Fatal(err)
	}
	if len(imported.Messages) != 2 {
		t.Fatalf("imported messages = %d", len(imported.Messages))
	}
	if imported.Messages[0]["text"] != "first" || imported.Messages[1]["text"] != "answer" {
		t.Fatalf("imported branch = %#v", imported.Messages)
	}
	entries, err := NewSessionStore(path).ReadEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("exported entries = %d", len(entries))
	}
	if entries[0].Type != "session" || entries[0].Version != 3 || entries[0].CWD != root {
		t.Fatalf("header = %#v", entries[0])
	}
	if entries[1].ParentID != "" || entries[2].ParentID != entries[1].ID {
		t.Fatalf("entries were not linearized: %#v", entries)
	}
}

func TestSessionBranchWithSummaryAddsContext(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.appendEntry(SessionEntry{ID: "u1", Type: "message", Message: map[string]any{
		"role": "user", "text": "first",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{ID: "a1", Type: "message", Message: map[string]any{
		"role": "assistant", "text": "answer",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{ID: "u2", Type: "message", Message: map[string]any{
		"role": "user", "text": "second",
	}}); err != nil {
		t.Fatal(err)
	}

	if err := session.BranchWithSummary("u1", "parallel branch summary"); err != nil {
		t.Fatal(err)
	}
	if len(session.Messages) != 2 {
		t.Fatalf("messages = %#v", session.Messages)
	}
	last := session.Messages[len(session.Messages)-1]
	if last["role"] != "branchSummary" || last["summary"] != "parallel branch summary" || last["fromId"] != "u2" {
		t.Fatalf("branch summary message = %#v", last)
	}
	tree := session.Tree()
	if len(tree) != 1 || len(tree[0].Children) == 0 {
		t.Fatalf("tree = %#v", tree)
	}
	found := false
	for _, child := range tree[0].Children {
		if child.Type == "branch_summary" && child.Text == "parallel branch summary" {
			found = true
		}
	}
	if !found {
		t.Fatalf("branch summary missing from tree: %#v", tree)
	}
}

func TestSessionLabelsAreResolvedInTreeAndExport(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, nil)
	if err := session.appendEntry(SessionEntry{ID: "u1", Type: "message", Message: map[string]any{
		"role": "user", "text": "first",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.SetLabel("u1", "start"); err != nil {
		t.Fatal(err)
	}
	if got := session.GetLabel("u1"); got != "start" {
		t.Fatalf("label = %q", got)
	}
	tree := session.Tree()
	if len(tree) != 1 || tree[0].Label != "start" || tree[0].LabelTimestamp == "" {
		t.Fatalf("tree label = %#v", tree)
	}

	path, err := session.ExportToJSONL(filepath.Join(root, "labeled.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	imported := NewSession(root, nil)
	if err := imported.SwitchSession(path); err != nil {
		t.Fatal(err)
	}
	if len(imported.Messages) != 1 || imported.Messages[0]["text"] != "first" {
		t.Fatalf("imported messages = %#v", imported.Messages)
	}
	if got := imported.GetLabel("u1"); got != "start" {
		t.Fatalf("imported label = %q", got)
	}
	if err := imported.SetLabel("u1", ""); err != nil {
		t.Fatal(err)
	}
	if got := imported.GetLabel("u1"); got != "" {
		t.Fatalf("cleared label = %q", got)
	}
}

func TestSessionLabelDoesNotAdvanceConversationLeaf(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.appendEntry(SessionEntry{ID: "u1", Type: "message", Message: map[string]any{
		"role": "user", "text": "first",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.SetLabel("u1", "start"); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{ID: "a1", Type: "message", Message: map[string]any{
		"role": "assistant", "text": "answer",
	}}); err != nil {
		t.Fatal(err)
	}

	tree := session.Tree()
	if len(tree) != 1 {
		t.Fatalf("tree = %#v", tree)
	}
	if tree[0].ID != "u1" || tree[0].Label != "start" {
		t.Fatalf("root = %#v", tree[0])
	}
	if len(tree[0].Children) != 1 || tree[0].Children[0].ID != "a1" {
		t.Fatalf("children = %#v", tree[0].Children)
	}
}

func TestSessionCustomEntriesPersistAcrossSwitch(t *testing.T) {
	root := t.TempDir()
	sessionPath := filepath.Join(root, "session.jsonl")
	session := NewSession(root, nil)
	session.Store = NewSessionStore(sessionPath)
	entryID, err := session.AppendCustomEntry("demo.extension", map[string]any{"value": "one"})
	if err != nil {
		t.Fatal(err)
	}
	if entryID == "" {
		t.Fatal("missing custom entry id")
	}

	reloaded := NewSession(root, nil)
	if err := reloaded.SwitchSession(sessionPath); err != nil {
		t.Fatal(err)
	}
	entries := reloaded.CustomEntries("demo.extension")
	if len(entries) != 1 {
		t.Fatalf("custom entries = %#v", entries)
	}
	data := entries[0].Data.(map[string]any)
	if data["value"] != "one" {
		t.Fatalf("custom entry data = %#v", data)
	}
}

func TestSessionSlashCommandRegistration(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	session.SetExtensionCommands([]SlashCommandInfo{{
		Name:        "ext-cmd",
		Description: "from extension",
		Source:      "extension",
		SourceInfo:  map[string]any{"path": "ext.json"},
	}})
	session.SetPromptTemplates([]SlashCommandInfo{{
		Name:        "tpl",
		Description: "prompt template",
		Source:      "prompt",
		SourceInfo:  map[string]any{"path": "template.md"},
	}})
	session.SetSkills([]SlashCommandInfo{{
		Name:        "skill:demo",
		Description: "demo skill",
		Source:      "skill",
		SourceInfo:  map[string]any{"path": "skill"},
	}})

	commands := session.GetSlashCommands()
	if len(commands) < 5 {
		t.Fatalf("commands = %#v", commands)
	}
	for _, command := range []struct {
		name   string
		source string
	}{
		{name: "ext-cmd", source: "extension"},
		{name: "tpl", source: "prompt"},
		{name: "skill:demo", source: "skill"},
		{name: "branch", source: "prompt"},
		{name: "tree", source: "prompt"},
	} {
		found := false
		for _, got := range commands {
			if got.Name == command.name && got.Source == command.source {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing command %q from %q: %#v", command.name, command.source, commands)
		}
	}
}

func TestSessionLoadsPromptAndSkillCommandsFromResources(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(filepath.Join(root, ".pi", "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "skills", "demo-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".pi", "prompts", "review.md"), []byte("---\ndescription: Review code\n---\nReview $ARGUMENTS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "skills", "demo-skill", "SKILL.md"), []byte("---\nname: demo-skill\ndescription: Demo skill\n---\nUse this skill.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session := NewSession(root, nil)
	session.LoadSlashCommandResources(ResourceLoadOptions{
		AgentDir:        agentDir,
		IncludeDefaults: true,
	})

	commands := session.GetSlashCommands()
	found := map[string]bool{}
	for _, command := range commands {
		found[command.Name+"|"+command.Source] = true
	}
	if !found["review|prompt"] {
		t.Fatalf("missing prompt command: %#v", commands)
	}
	if !found["skill:demo-skill|skill"] {
		t.Fatalf("missing skill command: %#v", commands)
	}
}

func TestSessionExpandsPromptTemplateBeforePrompt(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".pi", "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".pi", "prompts", "fix.md"), []byte("---\ndescription: Fix target\n---\nFix $1 with $ARGUMENTS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession(root, []AssistantTurn{{StopReason: "stop", Content: []ai.ContentBlock{{Type: "text", Text: "ok"}}}})
	session.LoadSlashCommandResources(ResourceLoadOptions{IncludeDefaults: true})

	if err := session.Prompt(context.Background(), `/fix "parser bug"`); err != nil {
		t.Fatal(err)
	}
	if len(session.Messages) == 0 {
		t.Fatal("missing messages")
	}
	if got := session.Messages[0]["text"]; got != "Fix parser bug with parser bug\n" {
		t.Fatalf("expanded prompt = %#v", got)
	}
}

func TestSessionExpandsSkillInvocationPrompt(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".pi", "skills", "review-code"), 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(root, ".pi", "skills", "review-code", "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("---\nname: review-code\ndescription: Review code carefully\n---\nUse this skill.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession(root, []AssistantTurn{{StopReason: "stop", Content: []ai.ContentBlock{{Type: "text", Text: "ok"}}}})
	session.LoadSlashCommandResources(ResourceLoadOptions{IncludeDefaults: true})

	if err := session.Prompt(context.Background(), "/skill:review-code inspect main.go"); err != nil {
		t.Fatal(err)
	}
	got, _ := session.Messages[0]["text"].(string)
	if !strings.Contains(got, "<name>review-code</name>") {
		t.Fatalf("skill invocation prompt missing skill name: %q", got)
	}
	if !strings.Contains(got, "inspect main.go") {
		t.Fatalf("skill invocation prompt missing request: %q", got)
	}
	if !strings.Contains(got, skillPath) {
		t.Fatalf("skill invocation prompt missing path: %q", got)
	}
}

func TestSessionSendCustomMessageAndConversion(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.SendCustomMessage("demo", "custom text", true, nil); err != nil {
		t.Fatal(err)
	}
	session.Messages = append(session.Messages, map[string]any{
		"role":     "bashExecution",
		"command":  "printf hi",
		"output":   "hi\n",
		"exitCode": 0,
	})
	session.Messages = append(session.Messages, map[string]any{
		"role":    "compactionSummary",
		"summary": "old context compacted",
	})

	converted := sessionMessagesToAI(session.Messages)
	if len(converted) != 3 {
		t.Fatalf("converted len = %d", len(converted))
	}
	if converted[0].Role != "user" || converted[1].Role != "user" || converted[2].Role != "user" {
		t.Fatalf("converted roles = %#v", converted)
	}
	if !strings.Contains(messageText(converted[1].Content), "Ran `printf hi`") {
		t.Fatalf("bash conversion = %#v", converted[1].Content)
	}
}

func messageText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	blocks, ok := content.([]ai.ContentBlock)
	if !ok {
		return ""
	}
	for _, block := range blocks {
		if block.Type == "text" {
			return block.Text
		}
	}
	return ""
}

func TestSessionLoadSessionCapturesParentSessionHeader(t *testing.T) {
	tmp := t.TempDir()
	sessionPath := filepath.Join(tmp, "session.jsonl")
	parentPath := filepath.Join(tmp, "parent-session.jsonl")
	entries := []SessionEntry{
		{
			Type:          "session",
			ID:            "header-1",
			ParentSession: parentPath,
			Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		},
		{
			ID:        "u1",
			Type:      "message",
			ParentID:  "",
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Message:   map[string]any{"role": "user", "text": "hello"},
		},
		{
			ID:        "a1",
			Type:      "message",
			ParentID:  "u1",
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Message:   map[string]any{"role": "assistant", "text": "ok"},
		},
	}
	if err := writeSessionEntries(sessionPath, entries); err != nil {
		t.Fatal(err)
	}

	session := NewSession(tmp, nil)
	session.Store = NewSessionStore(sessionPath)
	if err := session.SwitchSession(sessionPath); err != nil {
		t.Fatal(err)
	}
	if session.parentSession != parentPath {
		t.Fatalf("parent session = %q", session.parentSession)
	}
	if len(session.Messages) != 2 {
		t.Fatalf("message count = %d", len(session.Messages))
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
