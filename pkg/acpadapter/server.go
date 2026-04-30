package acpadapter

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/codingagent"
	"github.com/badlogic/pigo/pkg/mcpadapter"
)

type Server struct {
	Options ServerOptions

	mu       sync.Mutex
	sessions map[string]*acpSession
	encoder  *json.Encoder
}

type ServerOptions struct {
	AuthFile          string
	AgentDir          string
	DiscoverResources bool
	PromptPaths       []string
	SkillPaths        []string
}

type acpSession struct {
	ID       string
	Session  *codingagent.Session
	MCP      *mcpadapter.Registry
	Cwd      string
	Title    string
	Updated  time.Time
	cancelMu sync.Mutex
	cancel   context.CancelFunc
}

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *jsonrpcError `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func New(options ServerOptions) *Server {
	return &Server{Options: options, sessions: map[string]*acpSession{}}
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
		s.closeAllSessions()
	}()
	s.encoder = json.NewEncoder(out)
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var request jsonrpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if err := s.write(jsonrpcResponse{JSONRPC: "2.0", Error: &jsonrpcError{Code: -32700, Message: err.Error()}}); err != nil {
				return err
			}
			continue
		}
		if request.ID == nil {
			if err := s.handleNotification(request); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			continue
		}
		if request.Method == "session/prompt" {
			wg.Add(1)
			go func(request jsonrpcRequest) {
				defer wg.Done()
				s.handleRequestAndWrite(ctx, request)
			}(request)
			continue
		}
		if err := s.handleRequestAndWrite(ctx, request); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handleRequestAndWrite(ctx context.Context, request jsonrpcRequest) error {
	result, rpcErr := s.handleRequest(ctx, request)
	response := jsonrpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result, Error: rpcErr}
	if rpcErr != nil {
		response.Result = nil
	}
	return s.write(response)
}

func (s *Server) handleRequest(ctx context.Context, request jsonrpcRequest) (any, *jsonrpcError) {
	switch request.Method {
	case "initialize":
		var params initializeParams
		_ = unmarshalParams(request.Params, &params)
		protocolVersion := params.ProtocolVersion
		if protocolVersion == 0 {
			protocolVersion = 1
		}
		return map[string]any{
			"protocolVersion": protocolVersion,
			"agentInfo":       map[string]any{"name": "pigo-acp", "version": "0.1.0"},
			"agentCapabilities": map[string]any{
				"loadSession":        false,
				"mcpCapabilities":    map[string]any{"http": true, "sse": true},
				"promptCapabilities": map[string]any{"image": true},
				"sessionCapabilities": map[string]any{
					"close": map[string]any{},
					"list":  map[string]any{},
				},
			},
		}, nil
	case "session/new":
		var params newSessionParams
		if err := unmarshalParams(request.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		id, err := s.newSession(ctx, params)
		if err != nil {
			return nil, internalError(err)
		}
		return map[string]any{"sessionId": id}, nil
	case "session/prompt":
		var params promptParams
		if err := unmarshalParams(request.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.prompt(ctx, params)
	case "session/close":
		var params sessionParams
		if err := unmarshalParams(request.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return map[string]any{}, s.closeSession(params.SessionID)
	case "session/list":
		var params listSessionsParams
		if err := unmarshalParams(request.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.listSessions(params), nil
	case "session/cancel":
		if err := s.handleCancel(request.Params); err != nil {
			return nil, invalidParams(err)
		}
		return map[string]any{}, nil
	default:
		return nil, &jsonrpcError{Code: -32601, Message: "method not found"}
	}
}

func (s *Server) handleNotification(request jsonrpcRequest) error {
	if request.Method != "session/cancel" {
		return nil
	}
	return s.handleCancel(request.Params)
}

func (s *Server) handleCancel(paramsRaw json.RawMessage) error {
	var params sessionParams
	if err := unmarshalParams(paramsRaw, &params); err != nil {
		return err
	}
	session, ok := s.getSession(params.SessionID)
	if !ok {
		return nil
	}
	session.cancelMu.Lock()
	if session.cancel != nil {
		session.cancel()
	}
	session.Session.Abort()
	session.cancelMu.Unlock()
	return nil
}

func (s *Server) newSession(ctx context.Context, params newSessionParams) (string, error) {
	root := strings.TrimSpace(params.Cwd)
	if root == "" {
		root = "."
	}
	session := codingagent.NewSession(root, nil)
	if s.Options.AuthFile != "" {
		if err := session.LoadOAuthStore(s.Options.AuthFile); err != nil {
			return "", err
		}
	}
	session.LoadSlashCommandResources(codingagent.ResourceLoadOptions{
		AgentDir:        s.Options.AgentDir,
		PromptPaths:     s.Options.PromptPaths,
		SkillPaths:      s.Options.SkillPaths,
		IncludeDefaults: s.Options.DiscoverResources,
	})
	mcpConfig := mcpadapter.ConfigFromACPServers(params.MCPServers)
	if len(mcpConfig.Servers) == 0 {
		var err error
		mcpConfig, _, err = mcpadapter.LoadConfig(root)
		if err != nil {
			return "", err
		}
	}
	var registry *mcpadapter.Registry
	if len(mcpConfig.Servers) > 0 {
		var err error
		registry, err = mcpadapter.RegisterTools(ctx, session, mcpConfig)
		if err != nil {
			return "", err
		}
	}
	id := uuid()
	now := time.Now().UTC()
	s.mu.Lock()
	s.sessions[id] = &acpSession{ID: id, Session: session, MCP: registry, Cwd: root, Updated: now}
	s.mu.Unlock()
	return id, nil
}

func (s *Server) prompt(ctx context.Context, params promptParams) (any, *jsonrpcError) {
	session, ok := s.getSession(params.SessionID)
	if !ok {
		return nil, invalidParams(fmt.Errorf("unknown session %q", params.SessionID))
	}
	text, attachments := promptBlocks(params.Prompt)
	promptCtx, cancel := context.WithCancel(ctx)
	session.cancelMu.Lock()
	session.cancel = cancel
	session.cancelMu.Unlock()
	defer func() {
		session.cancelMu.Lock()
		session.cancel = nil
		session.cancelMu.Unlock()
		cancel()
	}()

	done := make(chan struct{})
	bridgeErr := make(chan error, 1)
	eventStart := len(session.Session.RuntimeEvents())
	go func() {
		bridgeErr <- s.bridgeEvents(promptCtx, params.SessionID, session.Session, eventStart, done)
	}()
	err := session.Session.PromptWithSource(promptCtx, text, attachments, "acp")
	close(done)
	if err := <-bridgeErr; err != nil {
		return nil, internalError(err)
	}
	stopReason := "end_turn"
	if errors.Is(err, context.Canceled) || promptCtx.Err() == context.Canceled {
		stopReason = "cancelled"
	} else if err != nil {
		stopReason = "refusal"
	}
	s.markSessionUpdated(params.SessionID)
	return map[string]any{"stopReason": stopReason, "userMessageId": params.MessageID}, nil
}

func (s *Server) listSessions(params listSessionsParams) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions := make([]map[string]any, 0, len(s.sessions))
	for _, session := range s.sessions {
		if params.Cwd != "" && session.Cwd != params.Cwd {
			continue
		}
		item := map[string]any{
			"sessionId": session.ID,
			"cwd":       session.Cwd,
			"updatedAt": session.Updated.Format(time.RFC3339),
		}
		if session.Title != "" {
			item["title"] = session.Title
		}
		sessions = append(sessions, item)
	}
	return map[string]any{"sessions": sessions}
}

func (s *Server) closeSession(sessionID string) *jsonrpcError {
	s.mu.Lock()
	session, ok := s.sessions[sessionID]
	if ok {
		delete(s.sessions, sessionID)
	}
	s.mu.Unlock()
	if !ok {
		return invalidParams(fmt.Errorf("unknown session %q", sessionID))
	}
	session.Session.Abort()
	if session.MCP != nil {
		_ = session.MCP.Close()
	}
	return nil
}

func (s *Server) closeAllSessions() {
	s.mu.Lock()
	sessions := make([]*acpSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.sessions = map[string]*acpSession{}
	s.mu.Unlock()
	for _, session := range sessions {
		session.Session.Abort()
		if session.MCP != nil {
			_ = session.MCP.Close()
		}
	}
}

func (s *Server) markSessionUpdated(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.sessions[id]; session != nil {
		session.Updated = time.Now().UTC()
	}
}

func (s *Server) getSession(id string) (*acpSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	return session, ok
}

func (s *Server) bridgeEvents(ctx context.Context, sessionID string, session *codingagent.Session, seen int, done <-chan struct{}) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		events := session.RuntimeEvents()
		for seen < len(events) {
			for _, update := range acpUpdates(events[seen]) {
				if err := s.notifySessionUpdate(sessionID, update); err != nil {
					return err
				}
			}
			seen++
		}
		select {
		case <-done:
			events := session.RuntimeEvents()
			for seen < len(events) {
				for _, update := range acpUpdates(events[seen]) {
					if err := s.notifySessionUpdate(sessionID, update); err != nil {
						return err
					}
				}
				seen++
			}
			return nil
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Server) notifySessionUpdate(sessionID string, update map[string]any) error {
	return s.write(map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/update",
		"params":  map[string]any{"sessionId": sessionID, "update": update},
	})
}

func (s *Server) write(value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.encoder.Encode(value)
}

func invalidParams(err error) *jsonrpcError {
	return &jsonrpcError{Code: -32602, Message: "invalid params", Data: err.Error()}
}

func internalError(err error) *jsonrpcError {
	return &jsonrpcError{Code: -32603, Message: "internal error", Data: err.Error()}
}

func unmarshalParams(data json.RawMessage, target any) error {
	if len(data) == 0 {
		data = []byte("{}")
	}
	return json.Unmarshal(data, target)
}

func uuid() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(bytes[:])
	return hexed[:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:]
}

type newSessionParams struct {
	Cwd        string                 `json:"cwd"`
	MCPServers []mcpadapter.ACPServer `json:"mcpServers"`
}

type initializeParams struct {
	ProtocolVersion int `json:"protocolVersion"`
}

type sessionParams struct {
	SessionID string `json:"sessionId"`
}

type listSessionsParams struct {
	Cursor string `json:"cursor,omitempty"`
	Cwd    string `json:"cwd,omitempty"`
}

type promptParams struct {
	SessionID string           `json:"sessionId"`
	MessageID string           `json:"messageId,omitempty"`
	Prompt    []map[string]any `json:"prompt"`
}

func promptBlocks(blocks []map[string]any) (string, []codingagent.PromptAttachment) {
	var builder strings.Builder
	var attachments []codingagent.PromptAttachment
	for _, block := range blocks {
		switch strings.TrimSpace(fmt.Sprint(block["type"])) {
		case "text":
			builder.WriteString(fmt.Sprint(block["text"]))
		case "resource_link":
			builder.WriteString("Resource: ")
			builder.WriteString(fmt.Sprint(block["uri"]))
		case "resource":
			builder.WriteString(pretty(block))
		case "image":
			data := strings.TrimSpace(fmt.Sprint(block["data"]))
			mimeType := strings.TrimSpace(fmt.Sprint(block["mimeType"]))
			builder.WriteString("[Image: " + mimeType + "]")
			if data != "" {
				attachments = append(attachments, codingagent.PromptAttachment{Type: "image", Data: data, MimeType: mimeType})
			}
		case "audio":
			builder.WriteString("[Audio: " + fmt.Sprint(block["mimeType"]) + "]")
		default:
			builder.WriteString(pretty(block))
		}
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String()), attachments
}

func pretty(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func acpUpdates(event agentcore.Event) []map[string]any {
	switch strings.TrimSpace(fmt.Sprint(event["type"])) {
	case "message_update":
		assistantEvent, _ := event["assistantMessageEvent"].(map[string]any)
		eventType := fmt.Sprint(assistantEvent["type"])
		content := fmt.Sprint(assistantEvent["content"])
		if content == "" {
			content = fmt.Sprint(assistantEvent["delta"])
		}
		if content == "" {
			return nil
		}
		if eventType == "thinking_delta" {
			return []map[string]any{{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": content}}}
		}
		if eventType == "text_delta" || eventType == "text" {
			return []map[string]any{{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": content}}}
		}
	case "tool_execution_start":
		name := fmt.Sprint(event["toolName"])
		return []map[string]any{{
			"sessionUpdate": "tool_call",
			"toolCallId":    fmt.Sprint(event["toolCallId"]),
			"title":         name,
			"kind":          toolKind(name),
			"status":        "pending",
			"rawInput":      event["args"],
		}}
	case "tool_execution_update":
		return []map[string]any{{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    fmt.Sprint(event["toolCallId"]),
			"status":        "in_progress",
			"rawOutput":     event["partialResult"],
			"content":       toolContent(event["partialResult"]),
		}}
	case "tool_execution_end":
		status := "completed"
		if result, ok := event["result"].(map[string]any); ok {
			if isError, _ := result["isError"].(bool); isError {
				status = "failed"
			}
		}
		return []map[string]any{{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    fmt.Sprint(event["toolCallId"]),
			"status":        status,
			"rawOutput":     event["result"],
			"content":       toolContent(event["result"]),
		}}
	}
	return nil
}

func toolKind(name string) string {
	switch {
	case name == "read" || name == "ls":
		return "read"
	case name == "write" || name == "edit":
		return "edit"
	case name == "grep" || name == "find":
		return "search"
	case name == "bash":
		return "execute"
	case strings.HasPrefix(name, "mcp__"):
		return "fetch"
	default:
		return "other"
	}
}

func toolContent(value any) []map[string]any {
	result, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if content, ok := result["content"].([]any); ok {
		out := make([]map[string]any, 0, len(content))
		for _, item := range content {
			if block, ok := item.(map[string]any); ok {
				out = append(out, map[string]any{"type": "content", "content": block})
			}
		}
		return out
	}
	text := strings.TrimSpace(fmt.Sprint(result["text"]))
	if text == "" {
		return nil
	}
	return []map[string]any{{"type": "content", "content": map[string]any{"type": "text", "text": text}}}
}
