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
