package mcpadapter

import (
	"context"
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

func TestMapToolResultResourceContent(t *testing.T) {
	size := int64(12)
	result := MapToolResult(&mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.AudioContent{MIMEType: "audio/wav"},
			&mcp.ResourceLink{URI: "file:///tmp/a.txt", Title: "A", Size: &size},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///tmp/b.txt", Text: "embedded"}},
		},
	})
	if len(result.Content) != 3 {
		t.Fatalf("content = %#v", result.Content)
	}
	if result.Content[0].Text != "[Audio: audio/wav]" || result.Content[1].Text != "Resource: file:///tmp/a.txt (A)" || result.Content[2].Text != "embedded" {
		t.Fatalf("content = %#v", result.Content)
	}
}

func TestListAllToolsPaginates(t *testing.T) {
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "server"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "first"}, func(context.Context, *mcp.CallToolRequest, any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "second"}, func(context.Context, *mcp.CallToolRequest, any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.Run(ctx, serverTransport)
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "client"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := listAllTools(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 2 {
		t.Fatalf("tools = %#v", result.Tools)
	}
}
