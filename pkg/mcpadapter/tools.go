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
	sessions []*mcp.ClientSession
}

func (r *Registry) Close() error {
	var first error
	for _, session := range r.sessions {
		if err := session.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func RegisterTools(ctx context.Context, session *codingagent.Session, cfg Config) (*Registry, error) {
	registry, tools, specs, err := BuildTools(ctx, cfg)
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
	registry := &Registry{}
	var tools []agentcore.Tool
	var specs []ai.Tool
	seen := map[string]bool{}
	for serverName, serverCfg := range cfg.Servers {
		clientSession, toolList, err := connectServer(ctx, serverName, serverCfg)
		if err != nil {
			return registry, nil, nil, err
		}
		registry.sessions = append(registry.sessions, clientSession)
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
	client := mcp.NewClient(&mcp.Implementation{Name: "pigo-mcp-adapter", Version: "0.1.0"}, nil)
	transport, err := transportForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("MCP server %q: %w", serverName, err)
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("MCP server %q connect: %w", serverName, err)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("MCP server %q list tools: %w", serverName, err)
	}
	return session, tools, nil
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
		default:
			out = append(out, ai.ContentBlock{Type: "text", Text: prettyJSON(item)})
		}
	}
	return out
}

func prettyJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}
