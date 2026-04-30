package acpadapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
	"github.com/badlogic/pigo/pkg/codingagent"
)

func TestInitialize(t *testing.T) {
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":7}}` + "\n")
	var output bytes.Buffer
	server := New(ServerOptions{DiscoverResources: false})
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	result := response["result"].(map[string]any)
	if result["protocolVersion"].(float64) != 7 {
		t.Fatalf("response = %#v", response)
	}
	caps := result["agentCapabilities"].(map[string]any)
	if !caps["loadSession"].(bool) {
		t.Fatalf("capabilities = %#v", caps)
	}
	promptCaps := caps["promptCapabilities"].(map[string]any)
	if !promptCaps["image"].(bool) || !promptCaps["embeddedContext"].(bool) {
		t.Fatalf("prompt capabilities = %#v", promptCaps)
	}
	sessionCaps := caps["sessionCapabilities"].(map[string]any)
	if _, ok := sessionCaps["list"]; !ok {
		t.Fatalf("session capabilities = %#v", sessionCaps)
	}
	if _, ok := sessionCaps["resume"]; !ok {
		t.Fatalf("session capabilities = %#v", sessionCaps)
	}
	if _, ok := sessionCaps["fork"]; !ok {
		t.Fatalf("session capabilities = %#v", sessionCaps)
	}
}

func TestAuthenticateAndLogoutAreNoOps(t *testing.T) {
	server := New(ServerOptions{})
	for _, method := range []string{"authenticate", "logout"} {
		result, rpcErr := server.handleRequest(context.Background(), jsonrpcRequest{Method: method})
		if rpcErr != nil {
			t.Fatalf("%s error = %#v", method, rpcErr)
		}
		if len(result.(map[string]any)) != 0 {
			t.Fatalf("%s result = %#v", method, result)
		}
	}
}

func TestPromptBlocksExtractsImages(t *testing.T) {
	text, attachments := promptBlocks([]map[string]any{
		{"type": "text", "text": "look"},
		{"type": "image", "data": "abc", "mimeType": "image/png"},
		{"type": "resource_link", "uri": "file:///tmp/a.txt"},
		{"type": "resource", "resource": map[string]any{"uri": "file:///tmp/b.txt", "text": "embedded"}},
	})
	if !strings.Contains(text, "look") ||
		!strings.Contains(text, "[Image: image/png]") ||
		!strings.Contains(text, "Resource: file:///tmp/a.txt") ||
		!strings.Contains(text, "Resource: file:///tmp/b.txt\nembedded") {
		t.Fatalf("text = %q", text)
	}
	if len(attachments) != 1 || attachments[0].Data != "abc" || attachments[0].MimeType != "image/png" {
		t.Fatalf("attachments = %#v", attachments)
	}
}

func TestServeAcceptsLargeJSONRPCLines(t *testing.T) {
	largeText := strings.Repeat("x", 128*1024)
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":7,"_meta":{"large":` + quote(largeText) + `}}}` + "\n")
	var output bytes.Buffer
	server := New(ServerOptions{DiscoverResources: false})
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"protocolVersion":7`) {
		t.Fatalf("output = %q", output.String())
	}
}

func TestToolKind(t *testing.T) {
	cases := map[string]string{"read": "read", "edit": "edit", "grep": "search", "bash": "execute", "mcp__s__t": "fetch", "x": "other"}
	for name, want := range cases {
		if got := toolKind(name); got != want {
			t.Fatalf("toolKind(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestToolContentMapsGoContentBlocksAndLocations(t *testing.T) {
	content := toolContent(map[string]any{
		"content": []ai.ContentBlock{
			{Type: "text", Text: "hello"},
			{Type: "image", Data: "abc", MimeType: "image/png"},
		},
	})
	if len(content) != 2 {
		t.Fatalf("content = %#v", content)
	}
	first := content[0]["content"].(map[string]any)
	if first["type"] != "text" || first["text"] != "hello" {
		t.Fatalf("content = %#v", content)
	}
	second := content[1]["content"].(map[string]any)
	if second["type"] != "image" || second["data"] != "abc" || second["mimeType"] != "image/png" {
		t.Fatalf("content = %#v", content)
	}
	locations := toolLocations(map[string]any{"path": "main.go"})
	if len(locations) != 1 || locations[0]["path"] != "main.go" {
		t.Fatalf("locations = %#v", locations)
	}
}

func TestToolContentMapsDiffsAndDetailLocations(t *testing.T) {
	content := toolContent(map[string]any{
		"text": "edited",
		"details": map[string]any{
			"modifiedFiles": []string{"main.go"},
			"diff":          "--- main.go\n+++ main.go\n@@\n-old\n+new\n",
		},
	})
	if len(content) != 2 {
		t.Fatalf("content = %#v", content)
	}
	if content[0]["type"] != "diff" || content[0]["path"] != "main.go" {
		t.Fatalf("content = %#v", content)
	}
	locations := toolLocations(map[string]any{"details": map[string]any{"readFiles": []any{"readme.md"}, "modifiedFiles": []string{"main.go"}}})
	if len(locations) != 2 || locations[0]["path"] != "readme.md" || locations[1]["path"] != "main.go" {
		t.Fatalf("locations = %#v", locations)
	}
}

func TestBridgeEventsStartsAtPromptBoundary(t *testing.T) {
	session := codingagent.NewSession(t.TempDir(), nil)
	session.Events = append(session.Events, agentcore.Event{
		"type": "message_update",
		"assistantMessageEvent": map[string]any{
			"type":    "text_delta",
			"content": "old",
		},
	})
	start := len(session.RuntimeEvents())
	session.Events = append(session.Events, agentcore.Event{
		"type": "message_update",
		"assistantMessageEvent": map[string]any{
			"type":    "text_delta",
			"content": "new",
		},
	})

	var output bytes.Buffer
	server := New(ServerOptions{})
	server.encoder = json.NewEncoder(&output)
	done := make(chan struct{})
	close(done)
	if err := server.bridgeEvents(context.Background(), "s1", session, start, done); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("notifications = %q", output.String())
	}
	if strings.Contains(lines[0], "old") || !strings.Contains(lines[0], "new") {
		t.Fatalf("notification = %s", lines[0])
	}
}

func TestListSessionsAndCancelRequest(t *testing.T) {
	root := t.TempDir()
	session := codingagent.NewSession(root, nil)
	server := New(ServerOptions{DiscoverResources: false})
	server.sessions["s1"] = &acpSession{ID: "s1", Session: session, Cwd: root, Updated: time.Now().UTC()}
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":2,"method":"session/list","params":{"cwd":` + quote(root) + `}}` + "\n" +
			`{"jsonrpc":"2.0","id":3,"method":"session/cancel","params":{"sessionId":"missing"}}` + "\n",
	)
	var output bytes.Buffer
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("output = %q", output.String())
	}
	responses := responsesByID(t, lines)
	listResponse := responses[float64(2)]
	sessions := listResponse["result"].(map[string]any)["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v", sessions)
	}
	if sessions[0].(map[string]any)["cwd"] != root {
		t.Fatalf("sessions = %#v", sessions)
	}
	cancelResponse := responses[float64(3)]
	if _, hasError := cancelResponse["error"]; hasError {
		t.Fatalf("cancel response = %#v", cancelResponse)
	}
}

func responsesByID(t *testing.T, lines []string) map[any]map[string]any {
	t.Helper()
	responses := map[any]map[string]any{}
	for _, line := range lines {
		var response map[string]any
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		responses[response["id"]] = response
	}
	return responses
}

func containsSessionUpdate(lines []string, updateType, text string) bool {
	for _, line := range lines {
		var message map[string]any
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			continue
		}
		if message["method"] != "session/update" {
			continue
		}
		params, _ := message["params"].(map[string]any)
		update, _ := params["update"].(map[string]any)
		if update["sessionUpdate"] != updateType {
			continue
		}
		data, _ := json.Marshal(update)
		if strings.Contains(string(data), text) {
			return true
		}
	}
	return false
}

func TestACPUpdatesMapNonStreamingTextStartContent(t *testing.T) {
	updates := acpUpdates(agentcore.Event{
		"type": "message_update",
		"assistantMessageEvent": map[string]any{
			"type":    "text_start",
			"content": "final text",
		},
	})
	if len(updates) != 1 || updates[0]["sessionUpdate"] != "agent_message_chunk" {
		t.Fatalf("updates = %#v", updates)
	}
	ensureUpdateMessageID(updates[0], agentcore.Event{
		"type":    "message_update",
		"message": agentcore.AssistantMessage([]ai.ContentBlock{{Type: "text", Text: "final text"}}, "stop"),
	}, "", 3)
	if updates[0]["messageId"] == "" {
		t.Fatalf("missing message id: %#v", updates)
	}
	content := updates[0]["content"].(map[string]any)
	if content["text"] != "final text" {
		t.Fatalf("content = %#v", content)
	}
}

func TestBridgeEventStateUsesStableMessageIDForStreamingChunks(t *testing.T) {
	state := bridgeEventState{}
	start := agentcore.Event{
		"type":    "message_start",
		"message": agentcore.AssistantMessage(nil, "stop"),
	}
	if updates := state.updates(start, 7); len(updates) != 0 {
		t.Fatalf("start updates = %#v", updates)
	}
	first := state.updates(agentcore.Event{
		"type": "message_update",
		"message": agentcore.AssistantMessage([]ai.ContentBlock{{
			Type: "text",
			Text: "h",
		}}, "stop"),
		"assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "h"},
	}, 8)
	second := state.updates(agentcore.Event{
		"type": "message_update",
		"message": agentcore.AssistantMessage([]ai.ContentBlock{{
			Type: "text",
			Text: "he",
		}}, "stop"),
		"assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "e"},
	}, 9)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("updates = %#v %#v", first, second)
	}
	if first[0]["messageId"] == "" || first[0]["messageId"] != second[0]["messageId"] {
		t.Fatalf("message ids changed: %#v %#v", first, second)
	}
}

func TestACPUpdatesIgnoreStreamingTextEndContent(t *testing.T) {
	updates := acpUpdates(agentcore.Event{
		"type":                  "message_update",
		"assistantMessageEvent": map[string]any{"type": "text_end", "content": "ok"},
	})
	if len(updates) != 0 {
		t.Fatalf("text_end updates = %#v", updates)
	}
}

func TestServeProcessesCancelWhilePromptIsRunning(t *testing.T) {
	root := t.TempDir()
	session := codingagent.NewSession(root, []codingagent.AssistantTurn{
		{
			StopReason: "toolUse",
			Content: []ai.ContentBlock{{
				Type:      "toolCall",
				ID:        "bash-1",
				Name:      "bash",
				Arguments: map[string]any{"command": "sleep 5"},
			}},
		},
		{StopReason: "stop", Content: []ai.ContentBlock{{Type: "text", Text: "done"}}},
	})
	server := New(ServerOptions{})
	server.sessions["s1"] = &acpSession{ID: "s1", Session: session, Cwd: root, Updated: time.Now().UTC()}

	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	defer inWriter.Close()
	defer outReader.Close()

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(context.Background(), inReader, outWriter)
	}()
	defer func() {
		_ = inWriter.Close()
		_ = outWriter.Close()
		<-serveDone
	}()

	if _, err := inWriter.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"session/prompt","params":{"sessionId":"s1","prompt":[{"type":"text","text":"run"}]}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := inWriter.Write([]byte(`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"s1"}}` + "\n")); err != nil {
		t.Fatal(err)
	}

	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(outReader)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	timeout := time.After(2 * time.Second)
	for {
		select {
		case line := <-lines:
			var response map[string]any
			if err := json.Unmarshal([]byte(line), &response); err != nil {
				t.Fatal(err)
			}
			if response["id"] != float64(1) {
				continue
			}
			result := response["result"].(map[string]any)
			if result["stopReason"] != "cancelled" {
				t.Fatalf("response = %#v", response)
			}
			return
		case <-timeout:
			t.Fatal("timed out waiting for prompt cancellation response")
		}
	}
}

func TestLoadResumeAndForkSession(t *testing.T) {
	root := t.TempDir()
	sessionFile := filepath.Join(root, "session.jsonl")
	data := `{"type":"session","id":"s","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"session/load","params":{"sessionId":` + quote(sessionFile) + `,"cwd":` + quote(root) + `,"mcpServers":[]}}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"session/resume","params":{"sessionId":` + quote(sessionFile) + `,"cwd":` + quote(root) + `,"mcpServers":[]}}` + "\n" +
			`{"jsonrpc":"2.0","id":3,"method":"session/fork","params":{"sessionId":` + quote(sessionFile) + `,"cwd":` + quote(root) + `,"mcpServers":[]}}` + "\n",
	)
	var output bytes.Buffer
	server := New(ServerOptions{DiscoverResources: false})
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) < 4 {
		t.Fatalf("output = %q", output.String())
	}
	responses := responsesByID(t, lines)
	if _, hasError := responses[float64(1)]["error"]; hasError {
		t.Fatalf("load response = %#v", responses[float64(1)])
	}
	if _, hasError := responses[float64(2)]["error"]; hasError {
		t.Fatalf("resume response = %#v", responses[float64(2)])
	}
	forkResult := responses[float64(3)]["result"].(map[string]any)
	forkID, _ := forkResult["sessionId"].(string)
	if forkID == "" {
		t.Fatalf("fork response = %#v", responses[float64(3)])
	}
	if _, err := os.Stat(forkID); err != nil {
		t.Fatalf("fork file: %v", err)
	}
	if !containsSessionUpdate(lines, "user_message_chunk", "hello") {
		t.Fatalf("load did not replay history, output = %q", output.String())
	}
}

func TestForkNewSession(t *testing.T) {
	root := t.TempDir()
	server := New(ServerOptions{DiscoverResources: false})
	result, rpcErr := server.handleRequest(context.Background(), jsonrpcRequest{
		Method: "session/new",
		Params: json.RawMessage(`{"cwd":` + quote(root) + `,"mcpServers":[]}`),
	})
	if rpcErr != nil {
		t.Fatalf("new session error = %#v", rpcErr)
	}
	sessionID := result.(map[string]any)["sessionId"].(string)
	session, ok := server.getSession(sessionID)
	if !ok || session.Session.Store == nil || session.Session.Store.Path == "" {
		t.Fatalf("new session store = %#v", session)
	}
	forkResult, rpcErr := server.handleRequest(context.Background(), jsonrpcRequest{
		Method: "session/fork",
		Params: json.RawMessage(`{"sessionId":` + quote(sessionID) + `,"cwd":` + quote(root) + `,"mcpServers":[]}`),
	})
	if rpcErr != nil {
		t.Fatalf("fork error = %#v", rpcErr)
	}
	forkID := forkResult.(map[string]any)["sessionId"].(string)
	if forkID == "" {
		t.Fatalf("fork result = %#v", forkResult)
	}
	if _, err := os.Stat(forkID); err != nil {
		t.Fatalf("fork file: %v", err)
	}
}

func TestListSessionsIncludesPersistedSessionFiles(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, ".pigo", "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(sessionDir, "saved.jsonl")
	data := `{"type":"session","id":"s","cwd":` + quote(root) + `,"timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"type":"session_name","name":"Saved work","timestamp":"2026-01-01T00:00:01Z"}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	server := New(ServerOptions{DiscoverResources: false})
	result := server.listSessions(listSessionsParams{Cwd: root})
	sessions := result["sessions"].([]map[string]any)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v", sessions)
	}
	if sessions[0]["sessionId"] != sessionFile || sessions[0]["title"] != "Saved work" || sessions[0]["cwd"] != root {
		t.Fatalf("sessions = %#v", sessions)
	}
}

func TestSessionStateAndSetters(t *testing.T) {
	root := t.TempDir()
	server := New(ServerOptions{DiscoverResources: false})
	result, rpcErr := server.handleRequest(context.Background(), jsonrpcRequest{
		Method: "session/new",
		Params: json.RawMessage(`{"cwd":` + quote(root) + `,"mcpServers":[]}`),
	})
	if rpcErr != nil {
		t.Fatalf("new session error = %#v", rpcErr)
	}
	newResult := result.(map[string]any)
	sessionID, _ := newResult["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("new response = %#v", newResult)
	}
	if _, ok := newResult["models"].(map[string]any); !ok {
		t.Fatalf("new response missing models = %#v", newResult)
	}
	if _, ok := newResult["modes"].(map[string]any); !ok {
		t.Fatalf("new response missing modes = %#v", newResult)
	}
	if options, ok := newResult["configOptions"].([]map[string]any); !ok || len(options) == 0 {
		t.Fatalf("new response missing config options = %#v", newResult)
	}

	setModel, rpcErr := server.handleRequest(context.Background(), jsonrpcRequest{
		Method: "session/set_model",
		Params: json.RawMessage(`{"sessionId":` + quote(sessionID) + `,"modelId":"openai/gpt-4o-mini"}`),
	})
	if rpcErr != nil {
		t.Fatalf("set model error = %#v", rpcErr)
	}
	models := setModel.(map[string]any)["models"].(map[string]any)
	if models["currentModelId"] != "openai/gpt-4o-mini" {
		t.Fatalf("models = %#v", models)
	}

	setMode, rpcErr := server.handleRequest(context.Background(), jsonrpcRequest{
		Method: "session/set_mode",
		Params: json.RawMessage(`{"sessionId":` + quote(sessionID) + `,"modeId":"all"}`),
	})
	if rpcErr != nil {
		t.Fatalf("set mode error = %#v", rpcErr)
	}
	modes := setMode.(map[string]any)["modes"].(map[string]any)
	if modes["currentModeId"] != "all" {
		t.Fatalf("modes = %#v", modes)
	}
	session, ok := server.getSession(sessionID)
	if !ok || session.Session.SteeringMode != "all" || session.Session.FollowUpMode != "all" {
		t.Fatalf("session modes = %#v", session)
	}

	setConfig, rpcErr := server.handleRequest(context.Background(), jsonrpcRequest{
		Method: "session/set_config_option",
		Params: json.RawMessage(`{"sessionId":` + quote(sessionID) + `,"configId":"thinking_level","value":"high"}`),
	})
	if rpcErr != nil {
		t.Fatalf("set config option error = %#v", rpcErr)
	}
	options := setConfig.(map[string]any)["configOptions"].([]map[string]any)
	found := false
	for _, option := range options {
		if option["id"] == "thinking_level" && option["currentValue"] == "high" {
			found = true
		}
	}
	if !found {
		t.Fatalf("config options = %#v", options)
	}
}

func TestNewSessionAppliesInitialModelSelection(t *testing.T) {
	root := t.TempDir()
	server := New(ServerOptions{DiscoverResources: false})
	result, rpcErr := server.handleRequest(context.Background(), jsonrpcRequest{
		Method: "session/new",
		Params: json.RawMessage(`{"cwd":` + quote(root) + `,"mcpServers":[],"mode":"openrouter/moonshotai/kimi-k2.6"}`),
	})
	if rpcErr != nil {
		t.Fatalf("new session error = %#v", rpcErr)
	}
	models := result.(map[string]any)["models"].(map[string]any)
	if models["currentModelId"] != "openrouter/moonshotai/kimi-k2.6" {
		t.Fatalf("models = %#v", models)
	}
}

func TestDocumentNotificationsFeedPromptContext(t *testing.T) {
	root := t.TempDir()
	session := codingagent.NewSession(root, []codingagent.AssistantTurn{
		{StopReason: "stop", Content: []ai.ContentBlock{{Type: "text", Text: "ok"}}},
	})
	server := New(ServerOptions{})
	server.sessions["s1"] = &acpSession{ID: "s1", Session: session, Cwd: root, Updated: time.Now().UTC(), Documents: map[string]acpDocument{}}

	open := json.RawMessage(`{"sessionId":"s1","uri":"file:///repo/main.go","languageId":"go","version":1,"text":"package main\n"}`)
	if err := server.handleDocumentNotification("document/didOpen", open); err != nil {
		t.Fatal(err)
	}
	change := json.RawMessage(`{"sessionId":"s1","uri":"file:///repo/main.go","version":2,"contentChanges":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}},"text":"func main() {}\n"}]}`)
	if err := server.handleDocumentNotification("document/didChange", change); err != nil {
		t.Fatal(err)
	}
	focus := json.RawMessage(`{"sessionId":"s1","uri":"file:///repo/main.go","version":2,"position":{"line":1,"character":0},"visibleRange":{"start":{"line":0,"character":0},"end":{"line":2,"character":0}}}`)
	if err := server.handleDocumentNotification("document/didFocus", focus); err != nil {
		t.Fatal(err)
	}

	result, rpcErr := server.prompt(context.Background(), promptParams{
		SessionID: "s1",
		Prompt:    []map[string]any{{"type": "text", "text": "summarize"}},
	})
	if rpcErr != nil {
		t.Fatalf("prompt error = %#v", rpcErr)
	}
	if result.(map[string]any)["stopReason"] != "end_turn" {
		t.Fatalf("prompt result = %#v", result)
	}
	if !strings.Contains(session.LastPrompt, "Open editor documents") ||
		!strings.Contains(session.LastPrompt, "file:///repo/main.go") ||
		!strings.Contains(session.LastPrompt, "func main() {}") {
		t.Fatalf("last prompt = %q", session.LastPrompt)
	}

	closeParams := json.RawMessage(`{"sessionId":"s1","uri":"file:///repo/main.go"}`)
	if err := server.handleDocumentNotification("document/didClose", closeParams); err != nil {
		t.Fatal(err)
	}
	if context := server.sessions["s1"].documentContext(); context != "" {
		t.Fatalf("document context after close = %q", context)
	}
}

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
