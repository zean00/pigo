package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestRPCPromptStateAndMessages(t *testing.T) {
	session := NewSession(t.TempDir(), []AssistantTurn{{
		StopReason: "stop",
		Content:    []ai.ContentBlock{{Type: "text", Text: "hello"}},
	}})
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"p1","type":"prompt","message":"hi"}` + "\n" +
			`{"id":"s1","type":"get_state"}` + "\n" +
			`{"id":"m1","type":"get_messages"}` + "\n",
	)

	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 3 {
		t.Fatalf("responses len = %d", len(responses))
	}
	if responses[0]["success"] != true || responses[0]["command"] != "prompt" {
		t.Fatalf("prompt response = %#v", responses[0])
	}
	state := responses[1]["data"].(map[string]any)
	if state["messageCount"] != float64(2) {
		t.Fatalf("messageCount = %#v", state["messageCount"])
	}
	messages := responses[2]["data"].(map[string]any)["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages len = %d", len(messages))
	}
}

func TestRPCPromptUsesInputSource(t *testing.T) {
	session := NewSession(t.TempDir(), []AssistantTurn{{Content: []ai.ContentBlock{{Type: "text", Text: "ok"}}, StopReason: "stop"}})
	session.RegisterInputHandler(func(ctx context.Context, event InputEvent) (InputResult, error) {
		if event.Source != "rpc" {
			t.Fatalf("source = %q", event.Source)
		}
		return InputResult{Action: "transform", Text: event.Text + " via rpc"}, nil
	})

	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(`{"id":"p1","type":"prompt","message":"hello"}` + "\n")
	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 1 || responses[0]["success"] != true {
		t.Fatalf("responses = %#v", responses)
	}
	if got, _ := session.Messages[0]["text"].(string); got != "hello via rpc" {
		t.Fatalf("prompt = %q", got)
	}
}

func TestRPCPromptWithAttachmentsAndRetry(t *testing.T) {
	session := NewSession(t.TempDir(), []AssistantTurn{
		{StopReason: "stop", Content: []ai.ContentBlock{{Type: "text", Text: "first"}}},
		{StopReason: "stop", Content: []ai.ContentBlock{{Type: "text", Text: "second"}}},
	})
	if err := os.WriteFile(filepath.Join(session.Root, "notes.txt"), []byte("attached"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"p1","type":"prompt","message":"hi","attachments":[{"type":"file","path":"notes.txt"}],"images":[{"type":"image","data":"aW1n"}]}` + "\n" +
			`{"id":"r1","type":"retry"}` + "\n",
	)

	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if responses[0]["success"] != true || responses[1]["success"] != true {
		t.Fatalf("responses = %#v", responses)
	}
	if len(session.Messages) != 4 || session.Messages[3]["text"] != "second" {
		t.Fatalf("messages = %#v", session.Messages)
	}
}

func TestRPCBash(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(`{"id":"b1","type":"bash","command":"printf hello"}` + "\n")

	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	data := responses[0]["data"].(map[string]any)
	if data["output"] != "hello" {
		t.Fatalf("output = %#v", data["output"])
	}
	if data["exitCode"] != float64(0) {
		t.Fatalf("exitCode = %#v", data["exitCode"])
	}
}

func TestRPCSessionStatsAndPersistence(t *testing.T) {
	root := t.TempDir()
	sessionFile := filepath.Join(root, "session.jsonl")
	session := NewSession(root, []AssistantTurn{{StopReason: "stop", Content: []ai.ContentBlock{{Type: "text", Text: "ok"}}}})
	session.Store = NewSessionStore(sessionFile)
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"p1","type":"prompt","message":"hi"}` + "\n" +
			`{"id":"stats1","type":"get_session_stats"}` + "\n",
	)

	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	stats := responses[1]["data"].(map[string]any)
	if stats["userMessages"] != float64(1) {
		t.Fatalf("userMessages = %#v", stats["userMessages"])
	}
	if stats["assistantMessages"] != float64(1) {
		t.Fatalf("assistantMessages = %#v", stats["assistantMessages"])
	}
	if stats["sessionFile"] != sessionFile {
		t.Fatalf("sessionFile = %#v", stats["sessionFile"])
	}
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("persisted lines = %d", len(lines))
	}
}

func TestRPCSetSessionNameAndThinkingLevel(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"n1","type":"set_session_name","name":"demo"}` + "\n" +
			`{"id":"t1","type":"set_thinking_level","level":"high"}` + "\n" +
			`{"id":"s1","type":"get_state"}` + "\n",
	)

	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	state := responses[2]["data"].(map[string]any)
	if state["sessionName"] != "demo" {
		t.Fatalf("sessionName = %#v", state["sessionName"])
	}
	if state["thinkingLevel"] != "high" {
		t.Fatalf("thinkingLevel = %#v", state["thinkingLevel"])
	}
}

func TestRPCModelAndModes(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"m1","type":"set_model","provider":"openai","modelId":"gpt-4o-mini"}` + "\n" +
			`{"id":"m2","type":"get_available_models"}` + "\n" +
			`{"id":"m3","type":"cycle_model"}` + "\n" +
			`{"id":"q1","type":"set_steering_mode","mode":"all"}` + "\n" +
			`{"id":"q2","type":"set_follow_up_mode","mode":"all"}` + "\n" +
			`{"id":"q3","type":"get_state"}` + "\n",
	)

	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 6 {
		t.Fatalf("responses len = %d", len(responses))
	}
	if responses[0]["success"] != true {
		t.Fatalf("set_model failed = %#v", responses[0])
	}
	models := responses[1]["data"].(map[string]any)["models"].([]any)
	if len(models) <= 1 {
		t.Fatalf("available models = %#v", models)
	}
	data := responses[2]["data"].(map[string]any)
	modelPayload, ok := data["model"].(map[string]any)
	if !ok {
		t.Fatalf("cycle_model should return model payload: %#v", data)
	}
	nextProvider, providerOK := modelPayload["provider"].(string)
	nextModel, modelOK := modelPayload["id"].(string)
	if !providerOK || !modelOK || nextProvider == "" || nextModel == "" {
		t.Fatalf("cycle_model should return model: %#v", data)
	}
	state := responses[5]["data"].(map[string]any)
	if state["steeringMode"] != "all" || state["followUpMode"] != "all" {
		t.Fatalf("mode update did not apply: %#v", state)
	}
}

func TestRPCOAuthCredentialsAndAuthStatus(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"a1","type":"set_oauth_credentials","provider":"anthropic","oauthCredentials":{"refresh":"r1","access":"a1","expires":123}}` + "\n" +
			`{"id":"a2","type":"get_provider_auth_status"}` + "\n",
	)

	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if responses[0]["success"] != true {
		t.Fatalf("set_oauth_credentials failed = %#v", responses[0])
	}
	if got := session.OAuthCredentials["anthropic"].Access; got != "a1" {
		t.Fatalf("stored oauth access = %q", got)
	}
	providers := responses[1]["data"].(map[string]any)["providers"].([]any)
	found := false
	for _, item := range providers {
		provider := item.(map[string]any)
		if provider["provider"] == "anthropic" {
			found = true
			if provider["hasStoredOAuth"] != true {
				t.Fatalf("anthropic auth status = %#v", provider)
			}
		}
	}
	if !found {
		t.Fatal("missing anthropic auth status")
	}
}

func TestRPCOAuthProviderCatalogAndStoreLoad(t *testing.T) {
	root := t.TempDir()
	authFile := filepath.Join(root, "auth.json")
	if err := ai.UpsertOAuthStoreCredentials(authFile, "anthropic", ai.OAuthCredentials{
		Access:  "stored-access",
		Refresh: "stored-refresh",
		Expires: 123,
	}); err != nil {
		t.Fatal(err)
	}

	session := NewSession(root, nil)
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"o1","type":"get_oauth_providers"}` + "\n" +
			`{"id":"o2","type":"load_oauth_store","oauthStorePath":"` + authFile + `"}` + "\n" +
			`{"id":"o3","type":"get_provider_auth_status"}` + "\n",
	)

	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	providers := responses[0]["data"].(map[string]any)["providers"].([]any)
	if len(providers) == 0 {
		t.Fatal("expected oauth providers")
	}
	if got := session.OAuthCredentials["anthropic"].Access; got != "stored-access" {
		t.Fatalf("stored oauth access = %q", got)
	}
	statuses := responses[2]["data"].(map[string]any)["providers"].([]any)
	found := false
	for _, item := range statuses {
		status := item.(map[string]any)
		if status["provider"] == "anthropic" {
			found = true
			if status["hasStoredOAuth"] != true {
				t.Fatalf("anthropic auth status = %#v", status)
			}
		}
	}
	if !found {
		t.Fatal("missing anthropic provider status")
	}
}

func TestRPCThinkingAndCommands(t *testing.T) {
	session := NewSession(t.TempDir(), []AssistantTurn{{
		StopReason: "stop",
		Content:    []ai.ContentBlock{{Type: "text", Text: "done"}},
	}})
	var out bytes.Buffer
	server := RPCServer{Session: session}
	tmp := t.TempDir()
	htmlPath := filepath.Join(tmp, "session-report.html")
	input := strings.NewReader(
		`{"id":"t1","type":"prompt","message":"hello"}` + "\n" +
			`{"id":"t2","type":"cycle_thinking_level"}` + "\n" +
			`{"id":"t3","type":"compact","customInstructions":"run compact"}` + "\n" +
			`{"id":"t4","type":"set_auto_compaction","enabled":true}` + "\n" +
			`{"id":"t5","type":"set_auto_retry","enabled":true}` + "\n" +
			`{"id":"t6","type":"abort_retry"}` + "\n" +
			`{"id":"t7","type":"get_last_assistant_text"}` + "\n" +
			`{"id":"t8","type":"get_commands"}` + "\n" +
			`{"id":"t9","type":"export_html","outputPath":"` + htmlPath + `"}` + "\n",
	)

	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 9 {
		t.Fatalf("responses len = %d", len(responses))
	}
	if responses[2]["success"] != true {
		t.Fatalf("compact failed = %#v", responses[2])
	}
	compact := responses[2]["data"].(map[string]any)
	if compact["cancelled"] != false {
		t.Fatalf("compact cancelled = %#v", compact["cancelled"])
	}
	commands := responses[7]["data"].(map[string]any)
	if _, ok := commands["commands"].([]any); !ok {
		t.Fatalf("commands payload = %#v", commands)
	}
	textPayload := responses[6]["data"].(map[string]any)
	if textPayload["text"] == nil {
		t.Fatalf("last assistant text is nil: %#v", textPayload)
	}
	if _, err := os.Stat(htmlPath); err != nil {
		t.Fatalf("export_html output missing: %v", err)
	}
}

func TestRPCGetCommandsIncludesRegisteredSources(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	session.SetExtensionCommands([]SlashCommandInfo{{
		Name:        "ext-cmd",
		Description: "extension command",
		Source:      "extension",
		SourceInfo:  map[string]any{"path": "extension"},
	}})
	session.SetPromptTemplates([]SlashCommandInfo{{
		Name:        "tpl-cmd",
		Description: "template command",
		Source:      "prompt",
		SourceInfo:  map[string]any{"path": "template"},
	}})
	session.SetSkills([]SlashCommandInfo{{
		Name:        "skill:demo",
		Description: "skill command",
		Source:      "skill",
		SourceInfo:  map[string]any{"path": "skill"},
	}})

	var out bytes.Buffer
	server := RPCServer{Session: session}
	if err := server.Serve(context.Background(), strings.NewReader(`{"id":"g1","type":"get_commands"}`+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 1 || responses[0]["success"] != true {
		t.Fatalf("get_commands response = %#v", responses)
	}
	commands := responses[0]["data"].(map[string]any)["commands"].([]any)
	if len(commands) < 5 {
		t.Fatalf("commands = %#v", commands)
	}
	found := map[string]bool{}
	for _, command := range commands {
		item := command.(map[string]any)
		name, _ := item["name"].(string)
		source, _ := item["source"].(string)
		found[name+"|"+source] = true
	}
	for _, wanted := range []string{
		"ext-cmd|extension",
		"tpl-cmd|prompt",
		"skill:demo|skill",
		"branch|prompt",
		"tree|prompt",
	} {
		if !found[wanted] {
			t.Fatalf("missing command %q in %#v", wanted, commands)
		}
	}
}

func TestRPCExposesSessionEntriesAndEvents(t *testing.T) {
	session := NewSession(t.TempDir(), []AssistantTurn{{Content: []ai.ContentBlock{{Type: "text", Text: "ok"}}, StopReason: "stop"}})
	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"e1","type":"get_session_entries"}` + "\n" +
			`{"id":"e2","type":"get_events"}` + "\n",
	)
	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 2 {
		t.Fatalf("responses = %#v", responses)
	}
	entries := responses[0]["data"].(map[string]any)["entries"].([]any)
	events := responses[1]["data"].(map[string]any)["events"].([]any)
	if len(entries) == 0 || len(events) == 0 {
		t.Fatalf("entries=%#v events=%#v", entries, events)
	}
}

func TestRPCRegisterCommandsAndReloadResources(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "prompts", "fix.md"), []byte("---\ndescription: Fix issue\n---\nFix it\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session := NewSession(root, nil)
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"r1","type":"register_commands","commands":[{"name":"ext","description":"ext desc","source":"extension","sourceInfo":{"path":"ext"}}]}` + "\n" +
			`{"id":"r2","type":"reload_resources","includeDefaults":false,"promptPaths":["prompts"]}` + "\n" +
			`{"id":"g1","type":"get_commands"}` + "\n",
	)
	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 3 {
		t.Fatalf("responses len = %d", len(responses))
	}
	if responses[0]["success"] != true || responses[1]["success"] != true || responses[2]["success"] != true {
		t.Fatalf("responses = %#v", responses)
	}
	commands := responses[2]["data"].(map[string]any)["commands"].([]any)
	found := map[string]bool{}
	for _, rawCommand := range commands {
		command := rawCommand.(map[string]any)
		found[command["name"].(string)+"|"+command["source"].(string)] = true
	}
	if !found["ext|extension"] {
		t.Fatalf("missing registered extension command: %#v", commands)
	}
	if !found["fix|prompt"] {
		t.Fatalf("missing reloaded prompt command: %#v", commands)
	}
}

func TestRPCExtensionFlagsAndStatuses(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"f1","type":"register_flags","flags":[{"name":"verbose","description":"Verbose mode","type":"boolean","default":false}]}` + "\n" +
			`{"id":"f2","type":"set_flag","name":"verbose","value":true}` + "\n" +
			`{"id":"s1","type":"set_status","name":"ext","status":"ready"}` + "\n" +
			`{"id":"g1","type":"get_flags"}` + "\n" +
			`{"id":"g2","type":"get_statuses"}` + "\n",
	)
	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 5 {
		t.Fatalf("responses len = %d", len(responses))
	}
	for _, response := range responses {
		if response["success"] != true {
			t.Fatalf("response = %#v", response)
		}
	}
	flagData := responses[3]["data"].(map[string]any)
	flags := flagData["flags"].([]any)
	values := flagData["values"].(map[string]any)
	if len(flags) != 1 || flags[0].(map[string]any)["name"] != "verbose" || values["verbose"] != true {
		t.Fatalf("flag data = %#v", flagData)
	}
	statuses := responses[4]["data"].(map[string]any)["statuses"].(map[string]any)
	if statuses["ext"] != "ready" {
		t.Fatalf("statuses = %#v", statuses)
	}
}

func TestRPCPromptExpandsReloadedPromptTemplate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "prompts", "fix.md"), []byte("---\ndescription: Fix target\n---\nFix $1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession(root, []AssistantTurn{{StopReason: "stop", Content: []ai.ContentBlock{{Type: "text", Text: "ok"}}}})
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"r1","type":"reload_resources","includeDefaults":false,"promptPaths":["prompts"]}` + "\n" +
			`{"id":"p1","type":"prompt","message":"/fix parser"}` + "\n" +
			`{"id":"m1","type":"get_messages"}` + "\n",
	)
	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 3 {
		t.Fatalf("responses len = %d", len(responses))
	}
	messages := responses[2]["data"].(map[string]any)["messages"].([]any)
	first := messages[0].(map[string]any)
	if first["text"] != "Fix parser\n" {
		t.Fatalf("expanded rpc prompt = %#v", first)
	}
}

func TestRPCExportAndShareCommands(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	session.Messages = append(session.Messages, map[string]any{
		"role": "user", "text": "hello",
	})
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"e1","type":"export","outputPath":"session.html"}` + "\n" +
			`{"id":"e2","type":"export","outputPath":"session.jsonl"}` + "\n" +
			`{"id":"s1","type":"share","outputPath":"shared.jsonl"}` + "\n",
	)
	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 3 {
		t.Fatalf("responses len = %d", len(responses))
	}
	if responses[0]["success"] != true || responses[1]["success"] != true || responses[2]["success"] != true {
		t.Fatalf("responses = %#v", responses)
	}
	htmlPath := responses[0]["data"].(map[string]any)["path"].(string)
	if _, err := os.Stat(htmlPath); err != nil {
		t.Fatalf("html export missing: %v", err)
	}
	jsonlPath := responses[1]["data"].(map[string]any)["path"].(string)
	if _, err := os.Stat(jsonlPath); err != nil {
		t.Fatalf("jsonl export missing: %v", err)
	}
	shareURL := responses[2]["data"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(shareURL, "file://") {
		t.Fatalf("share url = %q", shareURL)
	}
	sharePath := strings.TrimPrefix(shareURL, "file://")
	if _, err := os.Stat(sharePath); err != nil {
		t.Fatalf("shared file missing: %v", err)
	}
}

func TestRPCSessionTreeCommands(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.appendEntry(SessionEntry{ID: "node-1", Type: "message", Message: map[string]any{
		"role": "user",
		"text": "first",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{ID: "node-2", Type: "message", Message: map[string]any{
		"role":       "assistant",
		"text":       "answer",
		"stopReason": "stop",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{ID: "node-3", Type: "message", Message: map[string]any{
		"role": "user",
		"text": "second",
	}}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"s1","type":"fork","entryId":"node-3"}` + "\n" +
			`{"id":"s2","type":"clone"}` + "\n" +
			`{"id":"s3","type":"get_fork_messages"}` + "\n" +
			`{"id":"s4","type":"new_session"}` + "\n",
	)

	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if responses[0]["data"].(map[string]any)["cancelled"] != false {
		t.Fatalf("fork cancelled flag = %#v", responses[0])
	}
	if responses[1]["data"].(map[string]any)["cancelled"] != false {
		t.Fatalf("clone cancelled flag = %#v", responses[1])
	}
	messages := responses[2]["data"].(map[string]any)["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("fork messages = %#v", messages)
	}
	if message, ok := messages[0].(map[string]any); !ok || message["entryId"] != "node-1" {
		t.Fatalf("unexpected fork message = %#v", messages[0])
	}
	if responses[3]["success"] != true {
		t.Fatalf("new_session failed = %#v", responses[3])
	}
}

func TestRPCBranchAndTreeCommands(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.appendEntry(SessionEntry{ID: "u-1", Type: "message", Message: map[string]any{
		"role": "user", "text": "first",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{ID: "a-1", Type: "message", Message: map[string]any{
		"role": "assistant", "text": "reply",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{ID: "u-2", Type: "message", Message: map[string]any{
		"role": "user", "text": "second",
	}}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"b1","type":"branch","entryId":"u-1"}` + "\n" +
			`{"id":"m1","type":"get_messages"}` + "\n" +
			`{"id":"t1","type":"tree"}` + "\n",
	)
	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 3 {
		t.Fatalf("responses len = %d", len(responses))
	}
	if responses[0]["success"] != true {
		t.Fatalf("branch failed = %#v", responses[0])
	}
	messages := responses[1]["data"].(map[string]any)["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages after branch = %d", len(messages))
	}
	tree := responses[2]["data"].(map[string]any)["nodes"].([]any)
	if len(tree) != 1 {
		t.Fatalf("tree nodes = %d", len(tree))
	}
}

func TestRPCBranchCommandAcceptsSummary(t *testing.T) {
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

	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"b1","type":"branch","entryId":"u1","summary":"came from answer"}` + "\n" +
			`{"id":"m1","type":"get_messages"}` + "\n",
	)
	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 2 || responses[0]["success"] != true {
		t.Fatalf("responses = %#v", responses)
	}
	messages := responses[1]["data"].(map[string]any)["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %#v", messages)
	}
	summary := messages[1].(map[string]any)
	if summary["role"] != "branchSummary" || summary["summary"] != "came from answer" {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestRPCSetAndGetLabel(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.appendEntry(SessionEntry{ID: "u1", Type: "message", Message: map[string]any{
		"role": "user", "text": "first",
	}}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"l1","type":"set_label","entryId":"u1","label":"start"}` + "\n" +
			`{"id":"l2","type":"get_label","entryId":"u1"}` + "\n" +
			`{"id":"t1","type":"tree"}` + "\n",
	)
	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 3 || responses[0]["success"] != true {
		t.Fatalf("responses = %#v", responses)
	}
	if responses[1]["data"].(map[string]any)["label"] != "start" {
		t.Fatalf("label response = %#v", responses[1])
	}
	nodes := responses[2]["data"].(map[string]any)["nodes"].([]any)
	node := nodes[0].(map[string]any)
	if node["label"] != "start" {
		t.Fatalf("tree node = %#v", node)
	}
}

func TestRPCAppendAndGetCustomEntries(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"c1","type":"append_custom_entry","customType":"demo","data":{"value":"one"}}` + "\n" +
			`{"id":"c2","type":"get_custom_entries","customType":"demo"}` + "\n",
	)
	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 2 || responses[0]["success"] != true || responses[1]["success"] != true {
		t.Fatalf("responses = %#v", responses)
	}
	if responses[0]["data"].(map[string]any)["entryId"] == "" {
		t.Fatalf("missing custom entry id: %#v", responses[0])
	}
	entries := responses[1]["data"].(map[string]any)["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %#v", entries)
	}
	data := entries[0].(map[string]any)["data"].(map[string]any)
	if data["value"] != "one" {
		t.Fatalf("data = %#v", data)
	}
}

func TestRPCNewSessionAcceptsParentSession(t *testing.T) {
	root := t.TempDir()
	sessionPath := filepath.Join(root, "session.jsonl")
	parentPath := filepath.Join(root, "parent-session.jsonl")
	session := NewSession(root, nil)
	session.Store = NewSessionStore(sessionPath)

	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(`{"id":"n1","type":"new_session","parentSession":"` + parentPath + `"}` + "\n")

	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 1 || responses[0]["success"] != true {
		t.Fatalf("new_session response = %#v", responses)
	}
	entries, err := session.Store.ReadEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("new session entries = %d", len(entries))
	}
	if entries[0].Type != "session" {
		t.Fatalf("new session entry type = %q", entries[0].Type)
	}
	if entries[0].ParentSession != parentPath {
		t.Fatalf("new session parent = %q", entries[0].ParentSession)
	}
}

func TestRPCForkFirstUserMessageStartsFromEmptyBranch(t *testing.T) {
	session := NewSession(t.TempDir(), nil)
	if err := session.appendEntry(SessionEntry{ID: "u1", Type: "message", Message: map[string]any{
		"role": "user",
		"text": "first message",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{ID: "a1", Type: "message", Message: map[string]any{
		"role":       "assistant",
		"text":       "first answer",
		"stopReason": "stop",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := session.appendEntry(SessionEntry{ID: "u2", Type: "message", Message: map[string]any{
		"role": "user",
		"text": "second message",
	}}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	server := RPCServer{Session: session}
	input := strings.NewReader(
		`{"id":"f1","type":"fork","entryId":"u1"}` + "\n" +
			`{"id":"m1","type":"get_messages"}` + "\n",
	)
	if err := server.Serve(context.Background(), input, &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeRPCResponses(t, out.String())
	if len(responses) != 2 {
		t.Fatalf("responses len = %d", len(responses))
	}
	if responses[0]["success"] != true {
		t.Fatalf("fork failed = %#v", responses[0])
	}
	data := responses[1]["data"].(map[string]any)
	rawMessages, ok := data["messages"]
	if !ok {
		t.Fatal("missing messages in get_messages response")
	}
	if rawMessages != nil {
		messages, ok := rawMessages.([]any)
		if !ok {
			t.Fatalf("messages has type %T", rawMessages)
		}
		if len(messages) != 0 {
			t.Fatalf("messages after fork should be empty; got %d", len(messages))
		}
	}
}

func decodeRPCResponses(t *testing.T, output string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	responses := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var response map[string]any
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	return responses
}
