package mcpadapter

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMapToolResultContent(t *testing.T) {
	result := MapToolResult(&mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "hello"},
			&mcp.ImageContent{Data: []byte("abc"), MIMEType: "image/png"},
		},
		IsError: true,
	})
	if !result.IsError || result.Text != "hello" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Content) != 2 || result.Content[1].Type != "image" || result.Content[1].Data != "abc" {
		t.Fatalf("content = %#v", result.Content)
	}
}

func TestMapToolResultStructuredContent(t *testing.T) {
	result := MapToolResult(&mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}})
	if len(result.Content) != 1 || result.Content[0].Type != "text" || result.Content[0].Text == "" {
		t.Fatalf("content = %#v", result.Content)
	}
}
