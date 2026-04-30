package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
	"github.com/badlogic/pigo/pkg/codingagent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Registry struct {
	session *codingagent.Session
	servers []registeredServer
}

type registeredServer struct {
	Name    string
	Session *mcp.ClientSession
}

func (r *Registry) Close() error {
	var first error
	for _, server := range r.servers {
		if err := server.Session.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func RegisterTools(ctx context.Context, session *codingagent.Session, cfg Config) (*Registry, error) {
	registry, tools, specs, err := buildTools(ctx, session, cfg)
	if err != nil {
		if registry != nil {
			_ = registry.Close()
		}
		return nil, err
	}
	for i := range tools {
		session.RegisterExtensionTool(tools[i], specs[i])
	}
	return registry, nil
}

func BuildTools(ctx context.Context, cfg Config) (*Registry, []agentcore.Tool, []ai.Tool, error) {
	return buildTools(ctx, nil, cfg)
}

func buildTools(ctx context.Context, codingSession *codingagent.Session, cfg Config) (*Registry, []agentcore.Tool, []ai.Tool, error) {
	registry := &Registry{session: codingSession}
	var tools []agentcore.Tool
	var specs []ai.Tool
	seen := map[string]bool{}
	for serverName, serverCfg := range cfg.Servers {
		clientSession, toolList, err := registry.connectServer(ctx, serverName, serverCfg)
		if err != nil {
			return registry, nil, nil, err
		}
		registry.servers = append(registry.servers, registeredServer{Name: serverName, Session: clientSession})
		for _, remoteTool := range toolList.Tools {
			if remoteTool == nil {
				continue
			}
			remoteName := remoteTool.Name
			localName := MakeToolName(serverName, remoteName)
			if seen[localName] {
				return registry, nil, nil, fmt.Errorf("duplicate MCP tool name %q", localName)
			}
			seen[localName] = true
			client := clientSession
			tools = append(tools, agentcore.Tool{
				Name: localName,
				Execute: func(ctx context.Context, call ai.ContentBlock) agentcore.ToolResult {
					result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: remoteName, Arguments: call.Arguments})
					if err != nil {
						return agentcore.ToolResult{
							Text:    fmt.Sprintf("MCP tool %s returned an error:\n%s", remoteName, err.Error()),
							IsError: true,
						}
					}
					return MapToolResult(result)
				},
			})
			specs = append(specs, ai.Tool{
				Name:        localName,
				Description: remoteTool.Description,
				Parameters:  schemaMap(remoteTool.InputSchema),
			})
		}
	}
	return registry, tools, specs, nil
}

func connectServer(ctx context.Context, serverName string, cfg ServerConfig) (*mcp.ClientSession, *mcp.ListToolsResult, error) {
	registry := &Registry{}
	return registry.connectServer(ctx, serverName, cfg)
}

func (r *Registry) connectServer(ctx context.Context, serverName string, cfg ServerConfig) (*mcp.ClientSession, *mcp.ListToolsResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "pigo-mcp-adapter", Version: "0.1.0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			if r.session != nil {
				go r.Refresh(context.Background())
			}
		},
	})
	transport, err := transportForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("MCP server %q: %w", serverName, err)
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("MCP server %q connect: %w", serverName, err)
	}
	tools, err := listAllTools(ctx, session)
	if err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("MCP server %q list tools: %w", serverName, err)
	}
	return session, tools, nil
}

func (r *Registry) Refresh(ctx context.Context) error {
	if r == nil || r.session == nil {
		return nil
	}
	var tools []agentcore.Tool
	var specs []ai.Tool
	seen := map[string]bool{}
	for _, server := range r.servers {
		toolList, err := listAllTools(ctx, server.Session)
		if err != nil {
			return fmt.Errorf("MCP server %q refresh tools: %w", server.Name, err)
		}
		for _, remoteTool := range toolList.Tools {
			if remoteTool == nil {
				continue
			}
			remoteName := remoteTool.Name
			localName := MakeToolName(server.Name, remoteName)
			if seen[localName] {
				return fmt.Errorf("duplicate MCP tool name %q", localName)
			}
			seen[localName] = true
			client := server.Session
			tools = append(tools, agentcore.Tool{
				Name: localName,
				Execute: func(ctx context.Context, call ai.ContentBlock) agentcore.ToolResult {
					result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: remoteName, Arguments: call.Arguments})
					if err != nil {
						return agentcore.ToolResult{Text: fmt.Sprintf("MCP tool %s returned an error:\n%s", remoteName, err.Error()), IsError: true}
					}
					return MapToolResult(result)
				},
			})
			specs = append(specs, ai.Tool{Name: localName, Description: remoteTool.Description, Parameters: schemaMap(remoteTool.InputSchema)})
		}
	}
	r.session.SetExtensionTools(tools, specs)
	return nil
}

func listAllTools(ctx context.Context, session *mcp.ClientSession) (*mcp.ListToolsResult, error) {
	var all []*mcp.Tool
	cursor := ""
	seenCursors := map[string]bool{}
	for {
		params := &mcp.ListToolsParams{Cursor: cursor}
		if cursor == "" {
			params = nil
		}
		result, err := session.ListTools(ctx, params)
		if err != nil {
			return nil, err
		}
		if result != nil {
			all = append(all, result.Tools...)
			cursor = strings.TrimSpace(result.NextCursor)
		} else {
			cursor = ""
		}
		if cursor == "" {
			break
		}
		if seenCursors[cursor] {
			return nil, fmt.Errorf("MCP server repeated list tools cursor %q", cursor)
		}
		seenCursors[cursor] = true
	}
	return &mcp.ListToolsResult{Tools: all}, nil
}

func transportForConfig(cfg ServerConfig) (mcp.Transport, error) {
	switch cfg.Type {
	case "", "stdio":
		cmd := exec.Command(cfg.Command, cfg.Args...)
		cmd.Env = os.Environ()
		for key, value := range cfg.Env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		if cfg.Cwd != "" {
			cmd.Dir = cfg.Cwd
		}
		return &mcp.CommandTransport{Command: cmd}, nil
	case "http":
		return &mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: httpClientWithHeaders(cfg.Headers)}, nil
	case "sse":
		return &mcp.SSEClientTransport{Endpoint: cfg.URL, HTTPClient: httpClientWithHeaders(cfg.Headers)}, nil
	default:
		return nil, fmt.Errorf("unsupported type %q", cfg.Type)
	}
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for key, value := range t.headers {
		clone.Header.Set(key, value)
	}
	return t.base.RoundTrip(clone)
}

func httpClientWithHeaders(headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return nil
	}
	base := http.DefaultTransport
	return &http.Client{Transport: headerTransport{base: base, headers: headers}}
}

func schemaMap(schema any) map[string]any {
	if schema == nil {
		return map[string]any{"type": "object", "additionalProperties": true}
	}
	if typed, ok := schema.(map[string]any); ok {
		return typed
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{"type": "object", "additionalProperties": true}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil || out == nil {
		return map[string]any{"type": "object", "additionalProperties": true}
	}
	return out
}

func MapToolResult(result *mcp.CallToolResult) agentcore.ToolResult {
	if result == nil {
		return agentcore.ToolResult{Text: "", Content: []ai.ContentBlock{{Type: "text", Text: ""}}}
	}
	content := MapContent(result.Content)
	if len(content) == 0 && result.StructuredContent != nil {
		text := prettyJSON(result.StructuredContent)
		content = []ai.ContentBlock{{Type: "text", Text: text}}
	}
	if len(content) == 0 {
		content = []ai.ContentBlock{{Type: "text", Text: ""}}
	}
	textParts := make([]string, 0, len(content))
	for _, block := range content {
		if block.Type == "text" {
			textParts = append(textParts, block.Text)
		}
	}
	return agentcore.ToolResult{
		Text:    strings.Join(textParts, "\n"),
		Content: content,
		IsError: result.IsError,
		Details: map[string]any{
			"structuredContent": result.StructuredContent,
		},
	}
}

func MapContent(items []mcp.Content) []ai.ContentBlock {
	out := make([]ai.ContentBlock, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case *mcp.TextContent:
			out = append(out, ai.ContentBlock{Type: "text", Text: typed.Text})
		case *mcp.ImageContent:
			out = append(out, ai.ContentBlock{Type: "image", Data: string(typed.Data), MimeType: typed.MIMEType})
		case *mcp.AudioContent:
			out = append(out, ai.ContentBlock{Type: "text", Text: fmt.Sprintf("[Audio: %s]", typed.MIMEType)})
		case *mcp.ResourceLink:
			out = append(out, ai.ContentBlock{Type: "text", Text: resourceLinkText(typed)})
		case *mcp.EmbeddedResource:
			out = append(out, ai.ContentBlock{Type: "text", Text: embeddedResourceText(typed)})
		default:
			out = append(out, ai.ContentBlock{Type: "text", Text: prettyJSON(item)})
		}
	}
	return out
}

func resourceLinkText(link *mcp.ResourceLink) string {
	if link == nil {
		return ""
	}
	if link.Title != "" {
		return fmt.Sprintf("Resource: %s (%s)", link.URI, link.Title)
	}
	return "Resource: " + link.URI
}

func embeddedResourceText(resource *mcp.EmbeddedResource) string {
	if resource == nil || resource.Resource == nil {
		return ""
	}
	if resource.Resource.Text != "" {
		return resource.Resource.Text
	}
	if len(resource.Resource.Blob) > 0 {
		return fmt.Sprintf("[Resource: %s]", resource.Resource.MIMEType)
	}
	return "Resource: " + resource.Resource.URI
}

func prettyJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}
