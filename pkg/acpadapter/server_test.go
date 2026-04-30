package acpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestInitialize(t *testing.T) {
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n")
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
	if result["protocolVersion"].(float64) != 1 {
		t.Fatalf("response = %#v", response)
	}
	caps := result["agentCapabilities"].(map[string]any)
	if caps["loadSession"].(bool) {
		t.Fatalf("capabilities = %#v", caps)
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
