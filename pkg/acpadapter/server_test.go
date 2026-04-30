package acpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/agentcore"
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
	if caps["loadSession"].(bool) {
		t.Fatalf("capabilities = %#v", caps)
	}
	sessionCaps := caps["sessionCapabilities"].(map[string]any)
	if _, ok := sessionCaps["list"]; !ok {
		t.Fatalf("session capabilities = %#v", sessionCaps)
	}
}

func TestPromptBlocksExtractsImages(t *testing.T) {
	text, attachments := promptBlocks([]map[string]any{
		{"type": "text", "text": "look"},
		{"type": "image", "data": "abc", "mimeType": "image/png"},
		{"type": "resource_link", "uri": "file:///tmp/a.txt"},
	})
	if !strings.Contains(text, "look") || !strings.Contains(text, "[Image: image/png]") || !strings.Contains(text, "Resource: file:///tmp/a.txt") {
		t.Fatalf("text = %q", text)
	}
	if len(attachments) != 1 || attachments[0].Data != "abc" || attachments[0].MimeType != "image/png" {
		t.Fatalf("attachments = %#v", attachments)
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
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":` + quote(root) + `,"mcpServers":[]}}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"session/list","params":{"cwd":` + quote(root) + `}}` + "\n" +
			`{"jsonrpc":"2.0","id":3,"method":"session/cancel","params":{"sessionId":"missing"}}` + "\n",
	)
	var output bytes.Buffer
	server := New(ServerOptions{DiscoverResources: false})
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("output = %q", output.String())
	}
	var listResponse map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &listResponse); err != nil {
		t.Fatal(err)
	}
	sessions := listResponse["result"].(map[string]any)["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v", sessions)
	}
	if sessions[0].(map[string]any)["cwd"] != root {
		t.Fatalf("sessions = %#v", sessions)
	}
	var cancelResponse map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &cancelResponse); err != nil {
		t.Fatal(err)
	}
	if _, hasError := cancelResponse["error"]; hasError {
		t.Fatalf("cancel response = %#v", cancelResponse)
	}
}

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
