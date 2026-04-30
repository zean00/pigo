package codingagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/badlogic/pigo/pkg/agentcore"
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

func TestBuiltinToolsTruncateLargeReadOutputAndRecordDetails(t *testing.T) {
	root := t.TempDir()
	large := strings.Repeat("x", maxToolOutputBytes+2000)
	if err := WriteWorkspaceFile(root, "large.txt", large); err != nil {
		t.Fatal(err)
	}
	tools := BuiltinTools(root)
	var readTool agentcore.Tool
	for _, tool := range tools {
		if tool.Name == "read" {
			readTool = tool
			break
		}
	}
	result := readTool.Execute(context.Background(), ai.ContentBlock{
		Name:      "read",
		Arguments: map[string]any{"path": "large.txt"},
	})
	if !strings.Contains(result.Text, "[... truncated ") {
		t.Fatalf("expected truncated output, got %d bytes", len(result.Text))
	}
	if result.Details["truncated"] != true {
		t.Fatalf("details = %#v", result.Details)
	}
}

func TestBuiltinReadRejectsBinaryFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "image.bin"), []byte{1, 2, 0, 4}, 0o644); err != nil {
		t.Fatal(err)
	}
	var readTool agentcore.Tool
	for _, tool := range BuiltinTools(root) {
		if tool.Name == "read" {
			readTool = tool
			break
		}
	}
	result := readTool.Execute(context.Background(), ai.ContentBlock{
		Name:      "read",
		Arguments: map[string]any{"path": "image.bin"},
	})
	if !result.IsError || !strings.Contains(result.Text, "binary file") {
		t.Fatalf("result = %#v", result)
	}
}

func TestWorkspaceSearchSkipsIgnoredDirectoriesAndBinaryFiles(t *testing.T) {
	root := t.TempDir()
	if err := WriteWorkspaceFile(root, "src/main.go", "package main\nconst needle = true\n"); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspaceFile(root, "node_modules/pkg/index.js", "needle\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "blob.bin"), []byte{'n', 'e', 'e', 'd', 'l', 'e', 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	grep, err := GrepWorkspace(root, ".", "needle")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(grep, "node_modules") || strings.Contains(grep, "blob.bin") || !strings.Contains(grep, "src/main.go") {
		t.Fatalf("grep output = %q", grep)
	}
	find, err := FindWorkspace(root, ".", "*")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(find, "node_modules") {
		t.Fatalf("find output = %q", find)
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
	expectedModels := ai.GetAllModels()
	session := NewSession(t.TempDir(), nil)
	if len(session.AvailableModels) == 0 {
		t.Fatal("expected default available models")
	}
	modelsByProvider := map[string]struct{}{}
	for _, model := range session.AvailableModels {
		modelsByProvider[model.Provider+"/"+model.ModelID] = struct{}{}
	}
	for _, expectedModel := range ai.DefaultModels() {
		key := expectedModel.Provider + "/" + expectedModel.ModelID
		if _, ok := modelsByProvider[key]; !ok {
			t.Fatalf("missing default model %s", key)
		}
	}
	if len(session.AvailableModels) < len(expectedModels) {
		t.Fatalf("expected at least %d models, got %d", len(expectedModels), len(session.AvailableModels))
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

func TestSessionCompactSummarizesFileOperations(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.appendEntry(SessionEntry{Type: "message", Message: map[string]any{
		"role": "user", "text": "change file",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{Type: "message", Message: map[string]any{
		"role": "toolResult", "toolName": "read", "text": "content", "details": map[string]any{"readFiles": []string{"a.txt"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{Type: "message", Message: map[string]any{
		"role": "toolResult", "toolName": "write", "text": "wrote", "details": map[string]any{"modifiedFiles": []string{"b.txt"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{Type: "message", Message: map[string]any{
		"role": "assistant", "text": "done",
	}}); err != nil {
		t.Fatal(err)
	}

	result := session.Compact("keep file list")
	if !strings.Contains(result.Summary, "Read files: a.txt") || !strings.Contains(result.Summary, "Modified files: b.txt") {
		t.Fatalf("summary = %q", result.Summary)
	}
	if !strings.Contains(result.Summary, "Instructions: keep file list") {
		t.Fatalf("summary missing instructions = %q", result.Summary)
	}
	last := session.Messages[len(session.Messages)-1]
	if last["role"] != "compactionSummary" || !strings.Contains(last["summary"].(string), "b.txt") {
		t.Fatalf("compaction message = %#v", last)
	}
}

func TestSessionCompactWithModelUsesCompactorSummary(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.appendEntry(SessionEntry{Type: "message", Message: map[string]any{
		"role": "user", "text": "summarize me",
	}}); err != nil {
		t.Fatal(err)
	}
	session.Compactor = func(ctx context.Context, messages []ai.Message, instructions string) (string, error) {
		if len(messages) != 1 || messages[0].Content != "summarize me" {
			t.Fatalf("messages = %#v", messages)
		}
		if instructions != "custom" {
			t.Fatalf("instructions = %q", instructions)
		}
		return "model summary", nil
	}
	result := session.CompactWithModel(context.Background(), "custom")
	if result.Summary != "model summary" {
		t.Fatalf("summary = %q", result.Summary)
	}
	last := session.Messages[len(session.Messages)-1]
	if last["summary"] != "model summary" {
		t.Fatalf("compaction message = %#v", last)
	}
}

func TestSessionCompactionEventsAndContextUsage(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.appendEntry(SessionEntry{Type: "message", Message: map[string]any{
		"role": "user",
		"text": strings.Repeat("context ", 20),
	}}); err != nil {
		t.Fatal(err)
	}

	usage := session.ContextUsage()
	if usage.EstimatedTokens == 0 || usage.MessageCount != 1 {
		t.Fatalf("usage = %#v", usage)
	}
	if stateUsage, ok := session.State().ContextUsage.(ContextUsage); !ok || stateUsage.EstimatedTokens != usage.EstimatedTokens {
		t.Fatalf("state context usage = %#v", session.State().ContextUsage)
	}
	result := session.Compact("keep context")
	if result.TokensBefore == 0 {
		t.Fatalf("tokens before = %#v", result)
	}
	seenBefore := false
	seenAfter := false
	for _, event := range session.Events {
		switch event["type"] {
		case "session_before_compact":
			seenBefore = true
		case "session_compact":
			seenAfter = event["summary"] == result.Summary && event["tokensBefore"] == result.TokensBefore
		}
	}
	if !seenBefore || !seenAfter {
		t.Fatalf("compaction events = %#v", session.Events)
	}
}

func TestSessionAbortCancelsCompaction(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.appendEntry(SessionEntry{Type: "message", Message: map[string]any{
		"role": "user",
		"text": "hello",
	}}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	session.Compactor = func(ctx context.Context, _ []ai.Message, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}

	results := make(chan CompactionResult, 1)
	go func() {
		results <- session.CompactWithModel(context.Background(), "")
	}()
	<-started
	session.Abort()

	select {
	case result := <-results:
		if !result.Cancelled {
			t.Fatalf("result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("compaction did not cancel")
	}
}

func TestBuiltinToolsUseConfiguredOutputLimitAndShellPrefix(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("x", 1000)), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := BuiltinToolsWithOptions(root, BuiltinToolOptions{OutputLimit: 220, ShellCommandPrefix: "FOO=bar"})
	byName := map[string]agentcore.Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}

	bash := byName["bash"].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{"command": "printf value"}})
	bashDetails := bash.Details
	if bashDetails["command"] != "FOO=bar printf value" {
		t.Fatalf("bash details = %#v", bashDetails)
	}

	read := byName["read"].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{"path": "large.txt"}})
	readText := read.Text
	if !strings.Contains(readText, "truncated") || len(readText) > 360 {
		t.Fatalf("read text = %q", readText)
	}
}

func TestSessionRetryLastReusesPrompt(t *testing.T) {
	session := NewSession(t.TempDir(), []AssistantTurn{
		{StopReason: "stop", Content: []ai.ContentBlock{{Type: "text", Text: "first"}}},
		{StopReason: "stop", Content: []ai.ContentBlock{{Type: "text", Text: "second"}}},
	})
	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if err := session.RetryLast(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(session.Messages) != 4 {
		t.Fatalf("messages = %#v", session.Messages)
	}
	if session.Messages[2]["text"] != "hello" || session.Messages[3]["text"] != "second" {
		t.Fatalf("retry messages = %#v", session.Messages)
	}
}

func TestSessionScriptedPromptAttachesContentText(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("attached content"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession(root, []AssistantTurn{
		{StopReason: "stop", Content: []ai.ContentBlock{{Type: "text", Text: "ready"}}},
	})
	if err := session.PromptWithAttachments(context.Background(), "analyze", []PromptAttachment{
		{Type: "file", Path: "notes.txt"},
		{Type: "image", Data: "aW1n", MimeType: "image/png"},
	}); err != nil {
		t.Fatal(err)
	}
	userMessage := session.Messages[0]
	if userMessage["role"] != "user" {
		t.Fatalf("user message = %#v", userMessage)
	}
	text, _ := userMessage["text"].(string)
	if !strings.Contains(text, "notes.txt") || !strings.Contains(text, "image attachment") {
		t.Fatalf("user message text = %q", text)
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

func TestSessionExtensionCommandHandlerCanRewritePrompt(t *testing.T) {
	session := NewSession(t.TempDir(), []AssistantTurn{{Content: []ai.ContentBlock{{Type: "text", Text: "done"}}, StopReason: "stop"}})
	session.RegisterExtensionCommand(SlashCommandInfo{
		Name:        "review",
		Description: "review command",
		SourceInfo:  map[string]any{"path": "extension.go"},
	}, func(_ context.Context, command ExtensionCommandContext) (ExtensionCommandResult, error) {
		if command.Session != session {
			t.Fatalf("handler session mismatch")
		}
		return ExtensionCommandResult{Prompt: "rewritten: " + command.Args}, nil
	})

	if err := session.Prompt(context.Background(), "/review main.go"); err != nil {
		t.Fatal(err)
	}
	if len(session.Messages) == 0 || session.Messages[0]["text"] != "rewritten: main.go" {
		t.Fatalf("messages = %#v", session.Messages)
	}
	commands := session.GetSlashCommands()
	found := false
	for _, command := range commands {
		if command.Name == "review" && command.Source == "extension" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing extension command: %#v", commands)
	}
}

func TestSessionExtensionCommandHandlerCanHandleWithoutPrompt(t *testing.T) {
	session := NewSession(t.TempDir(), []AssistantTurn{{Content: []ai.ContentBlock{{Type: "text", Text: "unused"}}, StopReason: "stop"}})
	handled := false
	session.RegisterExtensionCommand(SlashCommandInfo{Name: "mark"}, func(_ context.Context, command ExtensionCommandContext) (ExtensionCommandResult, error) {
		handled = command.Args == "now"
		return ExtensionCommandResult{Handled: true}, nil
	})

	if err := session.Prompt(context.Background(), "/mark now"); err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatalf("handler was not called")
	}
	if len(session.Messages) != 0 {
		t.Fatalf("handled command should not prompt model: %#v", session.Messages)
	}
}

func TestSessionExtensionToolExecutesInProviderLoop(t *testing.T) {
	session := NewSession(t.TempDir(), []AssistantTurn{
		{
			Content: []ai.ContentBlock{{
				Type:      "toolCall",
				ID:        "ext-1",
				Name:      "ext_echo",
				Arguments: map[string]any{"value": "ok"},
			}},
			StopReason: "toolUse",
		},
		{Content: []ai.ContentBlock{{Type: "text", Text: "done"}}, StopReason: "stop"},
	})
	session.RegisterExtensionTool(agentcore.Tool{
		Name: "ext_echo",
		Execute: func(_ context.Context, call ai.ContentBlock) agentcore.ToolResult {
			return agentcore.ToolResult{Text: "ext:" + call.Arguments["value"].(string)}
		},
	}, ai.Tool{
		Name:        "ext_echo",
		Description: "echo from extension",
		Parameters:  map[string]any{"type": "object"},
	})

	if err := session.Prompt(context.Background(), "use extension tool"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range session.Messages {
		if message["role"] == "toolResult" && message["toolName"] == "ext_echo" && message["text"] == "ext:ok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("messages = %#v", session.Messages)
	}
	specs := session.toolSpecs()
	if specs[len(specs)-1].Name != "ext_echo" {
		t.Fatalf("specs = %#v", specs)
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

func TestSessionLoadsExtensionDiscoveredResources(t *testing.T) {
	root := t.TempDir()
	promptDir := filepath.Join(root, "ext-prompts")
	skillDir := filepath.Join(root, "ext-skills", "ext-skill")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "ext.md"), []byte("---\ndescription: Extension prompt\n---\nRun extension prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: ext-skill\ndescription: Extension skill\n---\nUse it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session := NewSession(root, nil)
	session.RegisterExtensionResourceProvider(func(ctx context.Context, event ExtensionResourceEvent) (ExtensionResourceResult, error) {
		if event.Type != "resources_discover" || event.CWD != root || event.Reason != "reload" {
			t.Fatalf("event = %#v", event)
		}
		return ExtensionResourceResult{
			PromptPaths: []string{"ext-prompts"},
			SkillPaths:  []string{filepath.Join("ext-skills", "ext-skill")},
			ThemePaths:  []string{"theme.json"},
		}, nil
	})
	session.LoadSlashCommandResources(ResourceLoadOptions{IncludeDefaults: false, Reason: "reload"})

	found := map[string]bool{}
	for _, command := range session.GetSlashCommands() {
		found[command.Name+"|"+command.Source] = true
	}
	if !found["ext|prompt"] || !found["skill:ext-skill|skill"] {
		t.Fatalf("commands = %#v", session.GetSlashCommands())
	}
	diagnostics := session.ResourceDiagnostics()
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "theme paths") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestSessionLifecycleEventsForSwitchAndFork(t *testing.T) {
	root := t.TempDir()
	storePath := filepath.Join(root, "session.jsonl")
	session := NewSession(root, nil)
	session.Store = NewSessionStore(storePath)
	session.NewSession()
	if err := session.appendEntry(SessionEntry{Type: "message", Message: map[string]any{"role": "user", "text": "hello"}}); err != nil {
		t.Fatal(err)
	}
	userID := session.leafID

	if _, _, err := session.Fork(userID); err != nil {
		t.Fatal(err)
	}
	seenBeforeFork := false
	seenShutdown := false
	seenStart := false
	for _, event := range session.Events {
		switch event["type"] {
		case "session_before_fork":
			seenBeforeFork = event["entryId"] == userID
		case "session_shutdown":
			seenShutdown = event["reason"] == "fork"
		case "session_start":
			seenStart = event["reason"] == "fork"
		}
	}
	if !seenBeforeFork || !seenShutdown || !seenStart {
		t.Fatalf("events = %#v", session.Events)
	}
}

func TestSessionLifecycleHooksCanCancelSwitchForkAndBranch(t *testing.T) {
	root := t.TempDir()
	storePath := filepath.Join(root, "session.jsonl")
	session := NewSession(root, nil)
	session.Store = NewSessionStore(storePath)
	session.NewSession()
	currentStorePath := session.Store.Path
	if err := session.appendEntry(SessionEntry{Type: "message", Message: map[string]any{"role": "user", "text": "hello"}}); err != nil {
		t.Fatal(err)
	}
	userID := session.leafID
	otherPath := filepath.Join(root, "other.jsonl")
	if err := writeSessionEntries(otherPath, []SessionEntry{{Type: "session", ID: "s1"}}); err != nil {
		t.Fatal(err)
	}

	session.RegisterSessionBeforeSwitchHandler(func(ctx context.Context, event SessionBeforeSwitchEvent) (SessionBeforeResult, error) {
		if event.Reason != "resume" || event.TargetSessionFile != otherPath {
			t.Fatalf("switch event = %#v", event)
		}
		return SessionBeforeResult{Cancel: true}, nil
	})
	cancelled, err := session.SwitchSessionContext(context.Background(), otherPath)
	if err != nil || !cancelled {
		t.Fatalf("switch cancelled=%v err=%v", cancelled, err)
	}
	if session.Store.Path != currentStorePath {
		t.Fatalf("store changed to %q", session.Store.Path)
	}

	session.RegisterSessionBeforeForkHandler(func(ctx context.Context, event SessionBeforeForkEvent) (SessionBeforeResult, error) {
		if event.EntryID != userID || event.Position != "before" {
			t.Fatalf("fork event = %#v", event)
		}
		return SessionBeforeResult{Cancel: true}, nil
	})
	if _, cancelled, err := session.Fork(userID); err != nil || !cancelled {
		t.Fatalf("fork cancelled=%v err=%v", cancelled, err)
	}

	session.RegisterSessionBeforeTreeHandler(func(ctx context.Context, event SessionBeforeTreeEvent) (SessionBeforeResult, error) {
		if event.TargetID != "root" || event.OldLeafID != userID {
			t.Fatalf("tree event = %#v", event)
		}
		return SessionBeforeResult{Cancel: true}, nil
	})
	if err := session.Branch("root"); !errors.Is(err, ErrSessionOperationCancelled) {
		t.Fatalf("branch err = %v", err)
	}
	if session.leafID != userID {
		t.Fatalf("leaf changed to %q", session.leafID)
	}
}

func TestSessionResourceDiagnosticsAndPrecedence(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	userPromptDir := filepath.Join(agentDir, "prompts")
	projectPromptDir := filepath.Join(root, ".pi", "prompts")
	userSkillDir := filepath.Join(agentDir, "skills", "dup-skill")
	projectSkillDir := filepath.Join(root, ".pi", "skills", "dup-skill")
	invalidSkillDir := filepath.Join(root, ".pi", "skills", "InvalidName")
	for _, dir := range []string{userPromptDir, projectPromptDir, userSkillDir, projectSkillDir, invalidSkillDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(userPromptDir, "dup.md"), []byte("---\ndescription: User prompt\n---\nuser\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectPromptDir, "dup.md"), []byte("---\ndescription: Project prompt\n---\nproject\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userSkillDir, "SKILL.md"), []byte("---\nname: dup-skill\ndescription: User skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectSkillDir, "SKILL.md"), []byte("---\nname: dup-skill\ndescription: Project skill\ndisable-model-invocation: true\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalidSkillDir, "SKILL.md"), []byte("---\nname: Bad_Name\ndescription: Invalid name\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session := NewSession(root, nil)
	session.LoadSlashCommandResources(ResourceLoadOptions{
		AgentDir:        agentDir,
		PromptPaths:     []string{"missing-prompts"},
		SkillPaths:      []string{"missing-skills"},
		IncludeDefaults: true,
	})

	prompts := session.PromptTemplates()
	if len(prompts) != 1 || prompts[0].Description != "User prompt" {
		t.Fatalf("prompt precedence = %#v", prompts)
	}
	skills := session.Skills()
	if len(skills) != 2 {
		t.Fatalf("skills = %#v", skills)
	}
	if skills[0].Description != "User skill" {
		t.Fatalf("skill precedence = %#v", skills)
	}

	diagnostics := session.ResourceDiagnostics()
	seen := map[string]bool{}
	for _, diagnostic := range diagnostics {
		if diagnostic.Type == "collision" && diagnostic.Collision != nil {
			seen[diagnostic.Collision.ResourceType+"|"+diagnostic.Collision.Name] = true
		}
		if strings.Contains(diagnostic.Message, "does not exist") {
			seen["missing"] = true
		}
		if strings.Contains(diagnostic.Message, "invalid characters") {
			seen["invalid-skill-name"] = true
		}
		if strings.Contains(diagnostic.Message, "does not match parent directory") {
			seen["skill-dir-mismatch"] = true
		}
	}
	for _, key := range []string{"prompt|dup", "skill|skill:dup-skill", "missing", "invalid-skill-name", "skill-dir-mismatch"} {
		if !seen[key] {
			t.Fatalf("missing diagnostic %s in %#v", key, diagnostics)
		}
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

func TestWorkspaceMutationDetailsIncludeDiff(t *testing.T) {
	root := t.TempDir()
	writeDetails, err := WriteWorkspaceFileWithDetails(root, "notes.txt", "hello\n")
	if err != nil {
		t.Fatal(err)
	}
	if writeDetails["afterBytes"] != 6 || !strings.Contains(writeDetails["diff"].(string), "+hello") {
		t.Fatalf("write details = %#v", writeDetails)
	}

	editDetails, err := EditWorkspaceFileWithDetails(root, "notes.txt", []WorkspaceEdit{{OldText: "hello", NewText: "goodbye"}})
	if err != nil {
		t.Fatal(err)
	}
	if editDetails["editCount"] != 1 || !strings.Contains(editDetails["diff"].(string), "-hello") || !strings.Contains(editDetails["diff"].(string), "+goodbye") {
		t.Fatalf("edit details = %#v", editDetails)
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
