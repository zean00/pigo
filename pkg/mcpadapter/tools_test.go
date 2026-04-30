package mcpadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
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
	result := MapToolResult(&mcp.CallToolResult{Meta: mcp.Meta{"trace": "abc"}, StructuredContent: map[string]any{"ok": true}})
	if len(result.Content) != 1 || result.Content[0].Type != "text" || result.Content[0].Text == "" {
		t.Fatalf("content = %#v", result.Content)
	}
	if result.Details["structuredContent"] == nil || result.Details["mcpMeta"].(map[string]any)["trace"] != "abc" {
		t.Fatalf("details = %#v", result.Details)
	}
}

func TestMCPToolSpecPreservesMetadata(t *testing.T) {
	readOnly := true
	spec := mcpToolSpec("mcp__server__tool", "server", &mcp.Tool{
		Name:         "tool",
		Title:        "Remote Tool",
		Description:  "Does remote work",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}},
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &readOnly},
	})
	if spec.Name != "mcp__server__tool" || spec.Parameters["type"] != "object" {
		t.Fatalf("spec = %#v", spec)
	}
	for _, want := range []string{"Remote Tool", "Does remote work", "MCP tool annotations", "readOnlyHint", "MCP output schema"} {
		if !strings.Contains(spec.Description, want) {
			t.Fatalf("description missing %q: %s", want, spec.Description)
		}
	}
}

func TestMCPAgentToolMapsProtocolErrors(t *testing.T) {
	tool := mcpAgentTool("local", "remote", nil, nil)
	result := tool.Execute(context.Background(), ai.ContentBlock{})
	if !result.IsError || !strings.Contains(result.Text, "MCP tool remote returned an error") {
		t.Fatalf("result = %#v", result)
	}
}

func TestRegistryRoutesProgressNotifications(t *testing.T) {
	registry := &Registry{}
	var updates []agentcore.ToolResult
	cleanup := registry.registerProgress("token", func(result agentcore.ToolResult) {
		updates = append(updates, result)
	})
	registry.handleProgress(context.Background(), &mcp.ProgressNotificationClientRequest{
		Params: &mcp.ProgressNotificationParams{
			ProgressToken: "token",
			Progress:      1,
			Total:         2,
			Message:       "half done",
		},
	})
	if len(updates) != 1 || updates[0].Text != "half done" {
		t.Fatalf("updates = %#v", updates)
	}
	progress := updates[0].Details["mcpProgress"].(map[string]any)
	if progress["progressToken"] != "token" || progress["progress"] != float64(1) || progress["total"] != float64(2) {
		t.Fatalf("progress = %#v", progress)
	}
	cleanup()
	registry.handleProgress(context.Background(), &mcp.ProgressNotificationClientRequest{
		Params: &mcp.ProgressNotificationParams{ProgressToken: "token", Progress: 2},
	})
	if len(updates) != 1 {
		t.Fatalf("progress callback was not removed: %#v", updates)
	}
}

func TestMCPAgentToolEmitsProgressUpdates(t *testing.T) {
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "server"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "progress"}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
		if token := req.Params.GetProgressToken(); token != nil {
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
				ProgressToken: token,
				Progress:      1,
				Total:         2,
				Message:       "half done",
			})
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.Run(ctx, serverTransport)
	}()
	registry := &Registry{}
	client := mcp.NewClient(&mcp.Implementation{Name: "client"}, &mcp.ClientOptions{ProgressNotificationHandler: registry.handleProgress})
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tool := mcpAgentTool("local", "progress", session, registry)
	updates := make(chan agentcore.ToolResult, 1)
	result := tool.ExecuteWithUpdate(ctx, ai.ContentBlock{}, func(result agentcore.ToolResult) {
		updates <- result
	})
	if result.Text != "done" || result.IsError {
		t.Fatalf("result = %#v", result)
	}
	select {
	case update := <-updates:
		if update.Text != "half done" {
			t.Fatalf("update = %#v", update)
		}
	default:
		t.Fatal("missing progress update")
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
