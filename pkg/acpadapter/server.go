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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
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
	ID                    string
	Session               *codingagent.Session
	MCP                   *mcpadapter.Registry
	Cwd                   string
	AdditionalDirectories []string
	Title                 string
	Updated               time.Time
	Documents             map[string]acpDocument
	FocusedDocument       string
	documentMu            sync.Mutex
	cancelMu              sync.Mutex
	cancel                context.CancelFunc
}

type acpDocument struct {
	URI        string
	LanguageID string
	Version    int
	Text       string
	Focused    bool
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
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
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
	case "authenticate", "logout":
		return map[string]any{}, nil
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
				"loadSession":        true,
				"mcpCapabilities":    map[string]any{"http": true, "sse": true},
				"promptCapabilities": map[string]any{"embeddedContext": true, "image": true},
				"sessionCapabilities": map[string]any{
					"close":           map[string]any{},
					"fork":            map[string]any{},
					"list":            map[string]any{},
					"resume":          map[string]any{},
					"setConfigOption": map[string]any{},
					"setMode":         map[string]any{},
					"setModel":        map[string]any{},
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
		session, _ := s.getSession(id)
		return s.newSessionResult(id, session), nil
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
	case "session/load":
		var params lifecycleSessionParams
		if err := unmarshalParams(request.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.loadSession(ctx, params)
	case "session/resume":
		var params lifecycleSessionParams
		if err := unmarshalParams(request.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.resumeSession(ctx, params)
	case "session/fork":
		var params lifecycleSessionParams
		if err := unmarshalParams(request.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.forkSession(ctx, params)
	case "session/set_model":
		var params setModelParams
		if err := unmarshalParams(request.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.setModel(params)
	case "session/set_mode":
		var params setModeParams
		if err := unmarshalParams(request.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.setMode(params)
	case "session/set_config_option":
		var params setConfigOptionParams
		if err := unmarshalParams(request.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.setConfigOption(params)
	case "document/didOpen", "document/didChange", "document/didFocus", "document/didSave", "document/didClose":
		if err := s.handleDocumentNotification(request.Method, request.Params); err != nil {
			return nil, invalidParams(err)
		}
		return map[string]any{}, nil
	default:
		return nil, &jsonrpcError{Code: -32601, Message: "method not found"}
	}
}

func (s *Server) handleNotification(request jsonrpcRequest) error {
	switch request.Method {
	case "session/cancel":
		return s.handleCancel(request.Params)
	case "document/didOpen", "document/didChange", "document/didFocus", "document/didSave", "document/didClose":
		return s.handleDocumentNotification(request.Method, request.Params)
	default:
		return nil
	}
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
	root = absolutePath(root)
	session, registry, err := s.buildSession(ctx, root, params.MCPServers, "")
	if err != nil {
		return "", err
	}
	id := uuid()
	sessionPath, err := newSessionPath(root, id)
	if err != nil {
		if registry != nil {
			_ = registry.Close()
		}
		return "", err
	}
	session.Store = codingagent.NewSessionStore(sessionPath)
	if err := writeSessionEntries(sessionPath, []codingagent.SessionEntry{{
		Type:      "session",
		ID:        id,
		CWD:       root,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}}); err != nil {
		if registry != nil {
			_ = registry.Close()
		}
		return "", err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	s.sessions[id] = &acpSession{
		ID:                    id,
		Session:               session,
		MCP:                   registry,
		Cwd:                   root,
		AdditionalDirectories: normalizedDirectories(params.AdditionalDirectories),
		Updated:               now,
		Documents:             map[string]acpDocument{},
	}
	s.mu.Unlock()
	return id, nil
}

func (s *Server) buildSession(ctx context.Context, root string, mcpServers []mcpadapter.ACPServer, sessionFile string) (*codingagent.Session, *mcpadapter.Registry, error) {
	session := codingagent.NewSession(root, nil)
	if sessionFile != "" {
		store := codingagent.NewSessionStore(sessionFile)
		session.Store = store
		if _, err := session.SwitchSessionContext(context.Background(), sessionFile); err != nil {
			return nil, nil, err
		}
	}
	if s.Options.AuthFile != "" {
		if err := session.LoadOAuthStore(s.Options.AuthFile); err != nil {
			return nil, nil, err
		}
	}
	session.LoadSlashCommandResources(codingagent.ResourceLoadOptions{
		AgentDir:        s.Options.AgentDir,
		PromptPaths:     s.Options.PromptPaths,
		SkillPaths:      s.Options.SkillPaths,
		IncludeDefaults: s.Options.DiscoverResources,
	})
	mcpConfig := mcpadapter.ConfigFromACPServers(mcpServers)
	if len(mcpConfig.Servers) == 0 {
		var err error
		mcpConfig, _, err = mcpadapter.LoadConfig(root)
		if err != nil {
			return nil, nil, err
		}
	}
	var registry *mcpadapter.Registry
	if len(mcpConfig.Servers) > 0 {
		var err error
		registry, err = mcpadapter.RegisterTools(ctx, session, mcpConfig)
		if err != nil {
			return nil, nil, err
		}
	}
	return session, registry, nil
}

func (s *Server) prompt(ctx context.Context, params promptParams) (any, *jsonrpcError) {
	session, ok := s.getSession(params.SessionID)
	if !ok {
		return nil, invalidParams(fmt.Errorf("unknown session %q", params.SessionID))
	}
	text, attachments := promptBlocks(params.Prompt)
	text = appendDocumentContext(text, session.documentContext())
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
	return map[string]any{"stopReason": stopReason, "userMessageId": params.MessageID, "currentModelId": acpCurrentModelID(session.Session)}, nil
}

func (s *Server) listSessions(params listSessionsParams) map[string]any {
	s.mu.Lock()
	active := make([]*acpSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		active = append(active, session)
	}
	s.mu.Unlock()

	sessions := make([]map[string]any, 0, len(active))
	seen := map[string]bool{}
	cwd := strings.TrimSpace(params.Cwd)
	if cwd != "" {
		cwd = absolutePath(cwd)
	}
	searchRoot := cwd
	if searchRoot == "" {
		searchRoot = absolutePath(".")
	}
	for _, session := range active {
		if cwd != "" && absolutePath(session.Cwd) != cwd {
			continue
		}
		seen[session.ID] = true
		item := map[string]any{
			"sessionId": session.ID,
			"cwd":       session.Cwd,
			"updatedAt": session.Updated.Format(time.RFC3339),
		}
		if len(session.AdditionalDirectories) > 0 {
			item["additionalDirectories"] = append([]string(nil), session.AdditionalDirectories...)
		}
		if session.Title != "" {
			item["title"] = session.Title
		}
		sessions = append(sessions, item)
	}
	for _, item := range persistedSessionInfos(searchRoot) {
		sessionID, _ := item["sessionId"].(string)
		if seen[sessionID] {
			continue
		}
		if cwd != "" && absolutePath(fmt.Sprint(item["cwd"])) != cwd {
			continue
		}
		sessions = append(sessions, item)
	}
	return map[string]any{"sessions": sessions}
}

func (s *Server) loadSession(ctx context.Context, params lifecycleSessionParams) (any, *jsonrpcError) {
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, invalidParams(fmt.Errorf("missing sessionId"))
	}
	root := absolutePath(defaultString(strings.TrimSpace(params.Cwd), "."))
	session, registry, err := s.buildSession(ctx, root, params.MCPServers, sessionID)
	if err != nil {
		return nil, internalError(err)
	}
	s.replaceSession(sessionID, &acpSession{
		ID:                    sessionID,
		Session:               session,
		MCP:                   registry,
		Cwd:                   root,
		AdditionalDirectories: normalizedDirectories(params.AdditionalDirectories),
		Updated:               time.Now().UTC(),
		Documents:             map[string]acpDocument{},
	})
	if err := s.replaySessionHistory(sessionID, session); err != nil {
		return nil, internalError(err)
	}
	return s.sessionStateResult(session), nil
}

func (s *Server) resumeSession(ctx context.Context, params lifecycleSessionParams) (any, *jsonrpcError) {
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, invalidParams(fmt.Errorf("missing sessionId"))
	}
	if _, ok := s.getSession(sessionID); ok {
		s.markSessionUpdated(sessionID)
		session, _ := s.getSession(sessionID)
		return s.sessionStateResult(session.Session), nil
	}
	return s.loadSession(ctx, params)
}

func (s *Server) forkSession(ctx context.Context, params lifecycleSessionParams) (any, *jsonrpcError) {
	sourceID := strings.TrimSpace(params.SessionID)
	if sourceID == "" {
		return nil, invalidParams(fmt.Errorf("missing sessionId"))
	}
	source, ok := s.getSession(sourceID)
	if !ok {
		return nil, invalidParams(fmt.Errorf("unknown session %q", sourceID))
	}
	targetPath, err := nextForkPath(source.Cwd)
	if err != nil {
		return nil, internalError(err)
	}
	if source.Session.Store == nil || source.Session.Store.Path == "" {
		return nil, invalidParams(fmt.Errorf("session %q is not backed by a session file", sourceID))
	}
	entries, err := source.Session.Store.ReadEntries()
	if err != nil {
		return nil, internalError(err)
	}
	if err := writeSessionEntries(targetPath, entries); err != nil {
		return nil, internalError(err)
	}
	root := absolutePath(defaultString(strings.TrimSpace(params.Cwd), source.Cwd))
	session, registry, err := s.buildSession(ctx, root, params.MCPServers, targetPath)
	if err != nil {
		return nil, internalError(err)
	}
	forkID := targetPath
	documents, focusedDocument := source.cloneDocuments()
	s.replaceSession(forkID, &acpSession{
		ID:                    forkID,
		Session:               session,
		MCP:                   registry,
		Cwd:                   root,
		AdditionalDirectories: normalizedDirectories(params.AdditionalDirectories),
		Updated:               time.Now().UTC(),
		Documents:             documents,
		FocusedDocument:       focusedDocument,
	})
	return s.newSessionResult(forkID, &acpSession{Session: session}), nil
}

func (s *Server) handleDocumentNotification(method string, paramsRaw json.RawMessage) error {
	var params documentParams
	if err := unmarshalParams(paramsRaw, &params); err != nil {
		return err
	}
	session, ok := s.getSession(params.SessionID)
	if !ok {
		return fmt.Errorf("unknown session %q", params.SessionID)
	}
	session.applyDocumentNotification(method, params)
	s.markSessionUpdated(params.SessionID)
	return nil
}

func (s *acpSession) applyDocumentNotification(method string, params documentParams) {
	s.documentMu.Lock()
	defer s.documentMu.Unlock()
	if s.Documents == nil {
		s.Documents = map[string]acpDocument{}
	}
	uri := strings.TrimSpace(params.URI)
	if uri == "" {
		return
	}
	switch method {
	case "document/didOpen":
		s.Documents[uri] = acpDocument{URI: uri, LanguageID: params.LanguageID, Version: params.Version, Text: params.Text}
	case "document/didChange":
		doc := s.Documents[uri]
		doc.URI = uri
		doc.Version = params.Version
		doc.Text = applyDocumentChanges(doc.Text, params.ContentChanges)
		s.Documents[uri] = doc
	case "document/didFocus":
		for key, doc := range s.Documents {
			doc.Focused = false
			s.Documents[key] = doc
		}
		doc := s.Documents[uri]
		doc.URI = uri
		doc.Version = params.Version
		doc.Focused = true
		s.Documents[uri] = doc
		s.FocusedDocument = uri
	case "document/didSave":
		doc := s.Documents[uri]
		doc.URI = uri
		s.Documents[uri] = doc
	case "document/didClose":
		delete(s.Documents, uri)
		if s.FocusedDocument == uri {
			s.FocusedDocument = ""
		}
	}
}

func (s *acpSession) cloneDocuments() (map[string]acpDocument, string) {
	s.documentMu.Lock()
	defer s.documentMu.Unlock()
	if len(s.Documents) == 0 {
		return map[string]acpDocument{}, s.FocusedDocument
	}
	out := make(map[string]acpDocument, len(s.Documents))
	for key, doc := range s.Documents {
		out[key] = doc
	}
	return out, s.FocusedDocument
}

func (s *acpSession) documentContext() string {
	s.documentMu.Lock()
	defer s.documentMu.Unlock()
	if len(s.Documents) == 0 {
		return ""
	}
	var builder strings.Builder
	if doc, ok := s.Documents[s.FocusedDocument]; ok {
		writeDocumentContext(&builder, doc)
	}
	for uri, doc := range s.Documents {
		if uri == s.FocusedDocument {
			continue
		}
		writeDocumentContext(&builder, doc)
	}
	return strings.TrimSpace(builder.String())
}

func writeDocumentContext(builder *strings.Builder, doc acpDocument) {
	if strings.TrimSpace(doc.Text) == "" {
		return
	}
	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}
	builder.WriteString("Open document: ")
	builder.WriteString(doc.URI)
	if doc.LanguageID != "" {
		builder.WriteString(" (")
		builder.WriteString(doc.LanguageID)
		builder.WriteString(")")
	}
	builder.WriteString("\n")
	builder.WriteString(doc.Text)
}

func appendDocumentContext(text, docs string) string {
	if strings.TrimSpace(docs) == "" {
		return text
	}
	if strings.TrimSpace(text) == "" {
		return "Open editor documents:\n\n" + docs
	}
	return text + "\n\nOpen editor documents:\n\n" + docs
}

func applyDocumentChanges(text string, changes []documentChange) string {
	for _, change := range changes {
		if change.Range == nil {
			text = change.Text
			continue
		}
		start, okStart := documentOffset(text, change.Range.Start)
		end, okEnd := documentOffset(text, change.Range.End)
		if !okStart || !okEnd || start > end {
			continue
		}
		runes := []rune(text)
		text = string(runes[:start]) + change.Text + string(runes[end:])
	}
	return text
}

func documentOffset(text string, position documentPosition) (int, bool) {
	if position.Line < 0 || position.Character < 0 {
		return 0, false
	}
	line := 0
	character := 0
	for index, r := range []rune(text) {
		if line == position.Line && character == position.Character {
			return index, true
		}
		if r == '\n' {
			line++
			character = 0
			continue
		}
		character++
	}
	if line == position.Line && character == position.Character {
		return len([]rune(text)), true
	}
	return 0, false
}

func (s *Server) setModel(params setModelParams) (any, *jsonrpcError) {
	session, rpcErr := s.requireSession(params.SessionID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	model, err := findACPModel(session.Session, params.ModelID)
	if err != nil {
		return nil, invalidParams(err)
	}
	if _, err := session.Session.SetModel(model.Provider, model.ModelID); err != nil {
		return nil, internalError(err)
	}
	s.markSessionUpdated(params.SessionID)
	return s.sessionStateResult(session.Session), nil
}

func (s *Server) setMode(params setModeParams) (any, *jsonrpcError) {
	session, rpcErr := s.requireSession(params.SessionID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	mode := strings.TrimSpace(params.ModeID)
	if err := session.Session.SetSteeringMode(mode); err != nil {
		return nil, invalidParams(err)
	}
	if err := session.Session.SetFollowUpMode(mode); err != nil {
		return nil, invalidParams(err)
	}
	s.markSessionUpdated(params.SessionID)
	return s.sessionStateResult(session.Session), nil
}

func (s *Server) setConfigOption(params setConfigOptionParams) (any, *jsonrpcError) {
	session, rpcErr := s.requireSession(params.SessionID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	value := strings.TrimSpace(params.configValue())
	var err error
	switch strings.TrimSpace(params.ConfigID) {
	case "thinking_level":
		err = session.Session.SetThinkingLevel(value)
	case "steering_mode":
		err = session.Session.SetSteeringMode(value)
	case "follow_up_mode":
		err = session.Session.SetFollowUpMode(value)
	case "model":
		var model codingagent.ModelInfo
		model, err = findACPModel(session.Session, value)
		if err == nil {
			_, err = session.Session.SetModel(model.Provider, model.ModelID)
		}
	default:
		return nil, invalidParams(fmt.Errorf("unknown config option %q", params.ConfigID))
	}
	if err != nil {
		return nil, invalidParams(err)
	}
	s.markSessionUpdated(params.SessionID)
	return s.sessionStateResult(session.Session), nil
}

func (s *Server) requireSession(sessionID string) (*acpSession, *jsonrpcError) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, invalidParams(fmt.Errorf("missing sessionId"))
	}
	session, ok := s.getSession(sessionID)
	if !ok {
		return nil, invalidParams(fmt.Errorf("unknown session %q", sessionID))
	}
	return session, nil
}

func (s *Server) newSessionResult(sessionID string, session *acpSession) map[string]any {
	result := map[string]any{"sessionId": sessionID}
	if session != nil && session.Session != nil {
		for key, value := range s.sessionStateResult(session.Session) {
			result[key] = value
		}
	}
	return result
}

func (s *Server) sessionStateResult(session *codingagent.Session) map[string]any {
	return map[string]any{
		"models":        acpModelState(session),
		"modes":         acpModeState(session),
		"configOptions": acpConfigOptions(session),
	}
}

func acpModelState(session *codingagent.Session) map[string]any {
	models := session.GetAvailableModels()
	available := make([]map[string]any, 0, len(models))
	for _, model := range models {
		id := acpModelID(model)
		available = append(available, map[string]any{
			"modelId": id,
			"name":    id,
		})
	}
	return map[string]any{
		"currentModelId":  acpCurrentModelID(session),
		"availableModels": available,
	}
}

func acpModeState(session *codingagent.Session) map[string]any {
	mode := session.SteeringMode
	if mode == "" {
		mode = "one-at-a-time"
	}
	return map[string]any{
		"currentModeId": mode,
		"availableModes": []map[string]any{
			{"id": "one-at-a-time", "name": "One at a time"},
			{"id": "all", "name": "All"},
		},
	}
}

func acpConfigOptions(session *codingagent.Session) []map[string]any {
	return []map[string]any{
		selectConfigOption("thinking_level", "Thinking level", session.ThinkingLevel, []string{"off", "low", "medium", "high", "xhigh"}),
		selectConfigOption("steering_mode", "Steering mode", session.SteeringMode, []string{"one-at-a-time", "all"}),
		selectConfigOption("follow_up_mode", "Follow-up mode", session.FollowUpMode, []string{"one-at-a-time", "all"}),
		modelConfigOption(session),
	}
}

func selectConfigOption(id, name, current string, values []string) map[string]any {
	options := make([]map[string]any, 0, len(values))
	for _, value := range values {
		options = append(options, map[string]any{"value": value, "name": value})
	}
	option := map[string]any{
		"id":           id,
		"name":         name,
		"type":         "select",
		"currentValue": current,
		"options":      options,
	}
	switch id {
	case "thinking_level":
		option["category"] = "thought_level"
	case "steering_mode", "follow_up_mode":
		option["category"] = "mode"
	}
	return option
}

func modelConfigOption(session *codingagent.Session) map[string]any {
	models := session.GetAvailableModels()
	options := make([]map[string]any, 0, len(models))
	for _, model := range models {
		id := acpModelID(model)
		options = append(options, map[string]any{"value": id, "name": id})
	}
	return map[string]any{
		"id":           "model",
		"name":         "Model",
		"category":     "model",
		"type":         "select",
		"currentValue": acpCurrentModelID(session),
		"options":      options,
	}
}

func findACPModel(session *codingagent.Session, id string) (codingagent.ModelInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return codingagent.ModelInfo{}, fmt.Errorf("missing modelId")
	}
	models := session.GetAvailableModels()
	for _, model := range models {
		if acpModelID(model) == id {
			return model, nil
		}
	}
	var matched *codingagent.ModelInfo
	for _, model := range models {
		if model.ModelID != id {
			continue
		}
		if matched != nil {
			return codingagent.ModelInfo{}, fmt.Errorf("ambiguous modelId %q", id)
		}
		copy := model
		matched = &copy
	}
	if matched != nil {
		return *matched, nil
	}
	if provider, modelID, ok := strings.Cut(id, "/"); ok && strings.TrimSpace(provider) != "" && strings.TrimSpace(modelID) != "" {
		return codingagent.ModelInfo{Provider: provider, ModelID: modelID}, nil
	}
	return codingagent.ModelInfo{}, fmt.Errorf("unknown modelId %q", id)
}

func acpCurrentModelID(session *codingagent.Session) string {
	return acpModelID(codingagent.ModelInfo{Provider: session.Provider, ModelID: session.ModelID})
}

func acpModelID(model codingagent.ModelInfo) string {
	if strings.TrimSpace(model.Provider) == "" {
		return strings.TrimSpace(model.ModelID)
	}
	if strings.TrimSpace(model.ModelID) == "" {
		return strings.TrimSpace(model.Provider)
	}
	return strings.TrimSpace(model.Provider) + "/" + strings.TrimSpace(model.ModelID)
}

func writeSessionEntries(path string, entries []codingagent.SessionEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			return err
		}
	}
	return nil
}

func persistedSessionInfos(cwd string) []map[string]any {
	if cwd == "" {
		return nil
	}
	paths := candidateSessionFiles(cwd)
	items := make([]map[string]any, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		path = absolutePath(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		info, ok := persistedSessionInfo(path, cwd)
		if ok {
			items = append(items, info)
		}
	}
	return items
}

func candidateSessionFiles(root string) []string {
	var paths []string
	for _, path := range []string{
		filepath.Join(root, "session.jsonl"),
		filepath.Join(root, ".pigo", "sessions"),
	} {
		stat, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !stat.IsDir() {
			paths = append(paths, path)
			continue
		}
		_ = filepath.WalkDir(path, func(current string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			if strings.EqualFold(filepath.Ext(current), ".jsonl") {
				paths = append(paths, current)
			}
			return nil
		})
	}
	return paths
}

func persistedSessionInfo(path, fallbackCWD string) (map[string]any, bool) {
	entries, err := codingagent.NewSessionStore(path).ReadEntries()
	if err != nil || len(entries) == 0 {
		return nil, false
	}
	cwd := fallbackCWD
	title := ""
	updatedAt := ""
	for _, entry := range entries {
		if entry.CWD != "" {
			cwd = absolutePath(entry.CWD)
		}
		if entry.Name != "" {
			title = entry.Name
		}
		if entry.Timestamp != "" {
			updatedAt = entry.Timestamp
		}
	}
	if updatedAt == "" {
		if stat, err := os.Stat(path); err == nil {
			updatedAt = stat.ModTime().UTC().Format(time.RFC3339)
		}
	}
	item := map[string]any{
		"sessionId": path,
		"cwd":       cwd,
	}
	if title != "" {
		item["title"] = title
	}
	if updatedAt != "" {
		item["updatedAt"] = updatedAt
	}
	return item, true
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

func (s *Server) replaceSession(id string, next *acpSession) {
	s.mu.Lock()
	previous := s.sessions[id]
	s.sessions[id] = next
	s.mu.Unlock()
	if previous != nil {
		previous.Session.Abort()
		if previous.MCP != nil {
			_ = previous.MCP.Close()
		}
	}
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

func (s *Server) replaySessionHistory(sessionID string, session *codingagent.Session) error {
	if s.encoder == nil {
		return nil
	}
	for _, message := range session.Messages {
		for _, update := range acpHistoryUpdates(message) {
			if err := s.notifySessionUpdate(sessionID, update); err != nil {
				return err
			}
		}
	}
	return nil
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

func nextForkPath(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	dir := filepath.Join(root, ".pigo", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "fork-"+time.Now().UTC().Format("20060102T150405.000000000")+".jsonl"), nil
}

func newSessionPath(root, id string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	dir := filepath.Join(root, ".pigo", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = uuid()
	}
	return filepath.Join(dir, "session-"+id+".jsonl"), nil
}

func normalizedDirectories(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		out = append(out, path)
	}
	return out
}

func absolutePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return absolute
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

type newSessionParams struct {
	Cwd                   string                 `json:"cwd"`
	MCPServers            []mcpadapter.ACPServer `json:"mcpServers"`
	AdditionalDirectories []string               `json:"additionalDirectories"`
}

type lifecycleSessionParams struct {
	SessionID             string                 `json:"sessionId"`
	Cwd                   string                 `json:"cwd"`
	MCPServers            []mcpadapter.ACPServer `json:"mcpServers"`
	AdditionalDirectories []string               `json:"additionalDirectories"`
}

type setModelParams struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

type setModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

type setConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     any    `json:"value"`
	ValueID   string `json:"valueId"`
}

func (p setConfigOptionParams) configValue() string {
	if strings.TrimSpace(p.ValueID) != "" {
		return p.ValueID
	}
	switch value := p.Value.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
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

type documentParams struct {
	SessionID      string           `json:"sessionId"`
	URI            string           `json:"uri"`
	LanguageID     string           `json:"languageId"`
	Version        int              `json:"version"`
	Text           string           `json:"text"`
	ContentChanges []documentChange `json:"contentChanges"`
}

type documentChange struct {
	Range *documentRange `json:"range"`
	Text  string         `json:"text"`
}

type documentRange struct {
	Start documentPosition `json:"start"`
	End   documentPosition `json:"end"`
}

type documentPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
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
			builder.WriteString(embeddedResourceText(block))
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

func embeddedResourceText(block map[string]any) string {
	resource, _ := block["resource"].(map[string]any)
	if resource == nil {
		resource, _ = block["content"].(map[string]any)
	}
	if resource == nil {
		return pretty(block)
	}
	uri := strings.TrimSpace(fmt.Sprint(resource["uri"]))
	text := fmt.Sprint(resource["text"])
	if strings.TrimSpace(text) != "" {
		if uri == "" {
			return text
		}
		return "Resource: " + uri + "\n" + text
	}
	blob := strings.TrimSpace(fmt.Sprint(resource["blob"]))
	mimeType := strings.TrimSpace(fmt.Sprint(resource["mimeType"]))
	if blob != "" {
		if uri != "" {
			return fmt.Sprintf("Resource: %s\n[Blob: %s]", uri, mimeType)
		}
		return fmt.Sprintf("[Blob: %s]", mimeType)
	}
	return pretty(block)
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
			"locations":     toolLocations(event["args"]),
		}}
	case "tool_execution_update":
		partial, _ := event["partialResult"].(map[string]any)
		return []map[string]any{{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    fmt.Sprint(event["toolCallId"]),
			"status":        "in_progress",
			"rawOutput":     event["partialResult"],
			"content":       toolContent(event["partialResult"]),
			"locations":     toolLocations(event["args"], partial),
		}}
	case "tool_execution_end":
		status := "completed"
		var result map[string]any
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
			"locations":     toolLocations(event["args"], result),
		}}
	}
	return nil
}

func acpHistoryUpdates(message agentcore.Message) []map[string]any {
	if message == nil {
		return nil
	}
	role := strings.TrimSpace(fmt.Sprint(message["role"]))
	text := messageText(message)
	if text == "" {
		return nil
	}
	switch role {
	case "user":
		return []map[string]any{{
			"sessionUpdate": "user_message_chunk",
			"content":       map[string]any{"type": "text", "text": text},
		}}
	case "assistant":
		return []map[string]any{{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": text},
		}}
	case "toolResult":
		return []map[string]any{{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    defaultString(strings.TrimSpace(fmt.Sprint(message["toolCallId"])), uuid()),
			"status":        "completed",
			"rawOutput":     message,
			"content": []map[string]any{{
				"type":    "content",
				"content": map[string]any{"type": "text", "text": text},
			}},
		}}
	default:
		return []map[string]any{{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": text},
		}}
	}
}

func messageText(message agentcore.Message) string {
	if text, ok := message["text"].(string); ok {
		return text
	}
	if content, ok := message["content"].(string); ok {
		return content
	}
	if content, ok := message["content"].([]ai.ContentBlock); ok {
		parts := make([]string, 0, len(content))
		for _, block := range content {
			if block.Type == "text" && block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "")
	}
	if content, ok := message["content"].([]any); ok {
		parts := make([]string, 0, len(content))
		for _, block := range content {
			if text := contentBlockText(block); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}

func contentBlockText(block any) string {
	switch typed := block.(type) {
	case map[string]any:
		if typed["type"] == "text" {
			return fmt.Sprint(typed["text"])
		}
	case ai.ContentBlock:
		if typed.Type == "text" {
			return typed.Text
		}
	}
	return ""
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
	out := toolDiffContent(result)
	if content, ok := result["content"].([]ai.ContentBlock); ok {
		out = append(out, contentBlocksToACP(content)...)
		return out
	}
	if content, ok := result["content"].([]any); ok {
		for _, item := range content {
			block := contentBlockToACP(item)
			if block != nil {
				out = append(out, map[string]any{"type": "content", "content": block})
			}
		}
		return out
	}
	text := strings.TrimSpace(fmt.Sprint(result["text"]))
	if text == "" && len(out) == 0 {
		return nil
	}
	if text != "" {
		out = append(out, map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": text}})
	}
	return out
}

func toolDiffContent(result map[string]any) []map[string]any {
	details, _ := result["details"].(map[string]any)
	if len(details) == 0 {
		return nil
	}
	diff := strings.TrimSpace(fmt.Sprint(details["diff"]))
	if diff == "" || diff == "<nil>" {
		return nil
	}
	path := strings.TrimSpace(firstDetailPath(details, "modifiedFiles", "path", "file", "filePath", "targetPath"))
	if path == "" {
		path = "changes.diff"
	}
	return []map[string]any{{
		"type": "diff",
		"path": path,
		"diff": diff,
	}}
}

func contentBlocksToACP(blocks []ai.ContentBlock) []map[string]any {
	out := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		content := map[string]any{"type": block.Type}
		switch block.Type {
		case "text":
			content["text"] = block.Text
		case "image":
			content["data"] = block.Data
			content["mimeType"] = block.MimeType
		default:
			content["text"] = block.Text
		}
		out = append(out, map[string]any{"type": "content", "content": content})
	}
	return out
}

func contentBlockToACP(value any) map[string]any {
	switch block := value.(type) {
	case map[string]any:
		return block
	case ai.ContentBlock:
		blocks := contentBlocksToACP([]ai.ContentBlock{block})
		if len(blocks) == 0 {
			return nil
		}
		content, _ := blocks[0]["content"].(map[string]any)
		return content
	default:
		return map[string]any{"type": "text", "text": fmt.Sprint(value)}
	}
}

func toolLocations(values ...any) []map[string]any {
	seen := map[string]bool{}
	var locations []map[string]any
	for _, value := range values {
		for _, path := range toolLocationPaths(value) {
			if seen[path] {
				continue
			}
			seen[path] = true
			locations = append(locations, map[string]any{"path": path})
		}
	}
	return locations
}

func toolLocationPaths(value any) []string {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	paths := []string{}
	for _, key := range []string{"path", "file", "filePath", "targetPath"} {
		if value := strings.TrimSpace(fmt.Sprint(obj[key])); value != "" && value != "<nil>" {
			paths = append(paths, value)
		}
	}
	if len(paths) == 0 {
		details, _ := obj["details"].(map[string]any)
		for _, key := range []string{"readFiles", "modifiedFiles"} {
			paths = append(paths, stringList(details[key])...)
		}
	}
	return paths
}

func firstDetailPath(details map[string]any, keys ...string) string {
	for _, key := range keys {
		if items := stringList(details[key]); len(items) > 0 {
			return items[0]
		}
		if value := strings.TrimSpace(fmt.Sprint(details[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func stringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" && text != "<nil>" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}
