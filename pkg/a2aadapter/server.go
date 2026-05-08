package a2aadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/badlogic/pigo/pkg/a2a"
	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/codingagent"
)

type Server struct {
	Options ServerOptions

	mu    sync.Mutex
	tasks map[string]*taskRuntime
}

type ServerOptions struct {
	Root        string
	BaseURL     string
	Name        string
	Description string
	Version     string
	Provider    string
	ModelID     string
	BearerToken string
	NewSession  func() *codingagent.Session
}

type taskRuntime struct {
	task    a2a.Task
	session *codingagent.Session
	cancel  context.CancelFunc
}

func New(options ServerOptions) *Server {
	return &Server{Options: options, tasks: map[string]*taskRuntime{}}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/.well-known/agent-card.json" {
		writeJSON(w, http.StatusOK, s.AgentCard(r))
		return
	}
	if r.URL.Path != "/a2a" {
		http.NotFound(w, r)
		return
	}
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var request a2a.JSONRPCRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcError(nil, -32700, err.Error()))
		return
	}
	if request.Method == "message/stream" {
		s.handleStream(r.Context(), w, request)
		return
	}
	result, rpcErr := s.handle(r.Context(), request)
	response := a2a.JSONRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: result, Error: rpcErr}
	if rpcErr != nil {
		response.Result = nil
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) AgentCard(r *http.Request) a2a.AgentCard {
	name := strings.TrimSpace(s.Options.Name)
	if name == "" {
		name = "pigo"
	}
	version := strings.TrimSpace(s.Options.Version)
	if version == "" {
		version = "0.1.0"
	}
	baseURL := strings.TrimRight(strings.TrimSpace(s.Options.BaseURL), "/")
	if baseURL == "" && r != nil {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		baseURL = scheme + "://" + r.Host
	}
	card := a2a.AgentCard{
		ProtocolVersion:    a2a.ProtocolVersion,
		Name:               name,
		Description:        firstNonEmpty(s.Options.Description, "Headless pigo coding agent exposed over A2A."),
		URL:                baseURL + "/a2a",
		PreferredTransport: "JSONRPC",
		Version:            version,
		Capabilities:       a2a.AgentCapabilities{Streaming: true, StateTransitionHistory: true},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills: []a2a.AgentSkill{{
			ID:          "coding-agent",
			Name:        "Coding agent",
			Description: "Answer questions, inspect files, and perform coding-agent tasks in the configured workspace.",
			Tags:        []string{"coding", "agent", "headless"},
			InputModes:  []string{"text/plain"},
			OutputModes: []string{"text/plain"},
		}},
	}
	if s.Options.BearerToken != "" {
		card.SecuritySchemes = map[string]any{"bearer": map[string]any{"type": "http", "scheme": "bearer"}}
		card.Security = []map[string][]any{{"bearer": {}}}
	}
	return card
}

func (s *Server) handle(ctx context.Context, request a2a.JSONRPCRequest) (any, *a2a.JSONRPCError) {
	switch request.Method {
	case "message/send":
		var params a2a.MessageSendParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, &a2a.JSONRPCError{Code: -32602, Message: err.Error()}
		}
		task, err := s.runMessage(ctx, params)
		if err != nil {
			return nil, &a2a.JSONRPCError{Code: -32000, Message: err.Error()}
		}
		return task, nil
	case "tasks/get":
		var params a2a.TaskQueryParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, &a2a.JSONRPCError{Code: -32602, Message: err.Error()}
		}
		task, ok := s.getTask(params.ID)
		if !ok {
			return nil, &a2a.JSONRPCError{Code: -32001, Message: "task not found"}
		}
		return task, nil
	case "tasks/cancel":
		var params a2a.TaskIDParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, &a2a.JSONRPCError{Code: -32602, Message: err.Error()}
		}
		task, err := s.cancelTask(params.ID)
		if err != nil {
			return nil, &a2a.JSONRPCError{Code: -32001, Message: err.Error()}
		}
		return task, nil
	default:
		return nil, &a2a.JSONRPCError{Code: -32601, Message: "method not found"}
	}
}

func (s *Server) handleStream(ctx context.Context, w http.ResponseWriter, request a2a.JSONRPCRequest) {
	var params a2a.MessageSendParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		writeJSON(w, http.StatusOK, a2a.JSONRPCResponse{JSONRPC: "2.0", ID: request.ID, Error: &a2a.JSONRPCError{Code: -32602, Message: err.Error()}})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	task := s.prepareTask(params.Message)
	s.storeTask(task, nil, nil)
	s.writeSSE(w, request.ID, task)
	if flusher != nil {
		flusher.Flush()
	}
	s.updateTaskStatus(task.ID, a2a.TaskStateWorking, nil)
	s.writeSSE(w, request.ID, a2a.TaskStatusUpdateEvent{Kind: "status-update", TaskID: task.ID, ContextID: task.ContextID, Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Timestamp: now()}, Final: false})
	task, err := s.runPreparedTask(ctx, task, params.Message)
	if err != nil {
		task.Status = a2a.TaskStatus{State: a2a.TaskStateFailed, Message: textStatusMessage(task, err.Error()), Timestamp: now()}
		s.storeTask(task, nil, nil)
	}
	if len(task.Artifacts) > 0 {
		s.writeSSE(w, request.ID, a2a.TaskArtifactUpdateEvent{Kind: "artifact-update", TaskID: task.ID, ContextID: task.ContextID, Artifact: task.Artifacts[len(task.Artifacts)-1], LastChunk: true})
	}
	s.writeSSE(w, request.ID, a2a.TaskStatusUpdateEvent{Kind: "status-update", TaskID: task.ID, ContextID: task.ContextID, Status: task.Status, Final: true})
	if flusher != nil {
		flusher.Flush()
	}
}

func (s *Server) runMessage(ctx context.Context, params a2a.MessageSendParams) (a2a.Task, error) {
	task := s.prepareTask(params.Message)
	return s.runPreparedTask(ctx, task, params.Message)
}

func (s *Server) runPreparedTask(ctx context.Context, task a2a.Task, message a2a.Message) (a2a.Task, error) {
	text := strings.TrimSpace(a2a.TextFromParts(message.Parts))
	if text == "" {
		return task, fmt.Errorf("message contains no text parts")
	}
	session := s.newSession()
	opCtx, cancel := context.WithCancel(ctx)
	s.storeTask(task, session, cancel)
	task.Status = a2a.TaskStatus{State: a2a.TaskStateWorking, Timestamp: now()}
	s.storeTask(task, session, cancel)
	err := session.PromptWithSource(opCtx, text, nil, "a2a")
	cancel()
	if err != nil {
		task.Status = a2a.TaskStatus{State: a2a.TaskStateFailed, Message: textStatusMessage(task, err.Error()), Timestamp: now()}
		s.storeTask(task, session, nil)
		return task, err
	}
	output := lastAssistantText(session.Messages)
	if output == "" {
		output = "completed"
	}
	response := a2a.NewTextMessage(a2a.RoleAgent, output)
	response.TaskID = task.ID
	response.ContextID = task.ContextID
	task.Status = a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &response, Timestamp: now()}
	task.Artifacts = []a2a.Artifact{{
		ArtifactID: "artifact-" + task.ID,
		Name:       "response",
		Parts:      []a2a.Part{{Kind: "text", Text: output}},
	}}
	task.History = append(task.History, response)
	s.storeTask(task, session, nil)
	return task, nil
}

func (s *Server) prepareTask(message a2a.Message) a2a.Task {
	taskID := strings.TrimSpace(message.TaskID)
	if taskID == "" {
		taskID = a2a.NewID("task")
	}
	contextID := strings.TrimSpace(message.ContextID)
	if contextID == "" {
		contextID = a2a.NewID("ctx")
	}
	if message.Kind == "" {
		message.Kind = "message"
	}
	if message.MessageID == "" {
		message.MessageID = a2a.NewID("msg")
	}
	message.TaskID = taskID
	message.ContextID = contextID
	return a2a.Task{
		Kind:      "task",
		ID:        taskID,
		ContextID: contextID,
		Status:    a2a.TaskStatus{State: a2a.TaskStateSubmitted, Timestamp: now()},
		History:   []a2a.Message{message},
	}
}

func (s *Server) newSession() *codingagent.Session {
	if s.Options.NewSession != nil {
		return s.Options.NewSession()
	}
	root := s.Options.Root
	if strings.TrimSpace(root) == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	session := codingagent.NewSession(root, nil)
	if s.Options.Provider != "" || s.Options.ModelID != "" {
		_, _ = session.SetModel(s.Options.Provider, s.Options.ModelID)
	}
	return session
}

func (s *Server) storeTask(task a2a.Task, session *codingagent.Session, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime := s.tasks[task.ID]
	if runtime == nil {
		runtime = &taskRuntime{}
		s.tasks[task.ID] = runtime
	}
	runtime.task = task
	if session != nil {
		runtime.session = session
	}
	runtime.cancel = cancel
}

func (s *Server) getTask(id string) (a2a.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime := s.tasks[strings.TrimSpace(id)]
	if runtime == nil {
		return a2a.Task{}, false
	}
	return runtime.task, true
}

func (s *Server) updateTaskStatus(id, state string, message *a2a.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime := s.tasks[id]
	if runtime == nil {
		return
	}
	runtime.task.Status = a2a.TaskStatus{State: state, Message: message, Timestamp: now()}
}

func (s *Server) cancelTask(id string) (a2a.Task, error) {
	s.mu.Lock()
	runtime := s.tasks[strings.TrimSpace(id)]
	if runtime == nil {
		s.mu.Unlock()
		return a2a.Task{}, errors.New("task not found")
	}
	if runtime.cancel != nil {
		runtime.cancel()
	}
	if runtime.session != nil {
		runtime.session.Abort()
	}
	runtime.task.Status = a2a.TaskStatus{State: a2a.TaskStateCanceled, Timestamp: now()}
	task := runtime.task
	s.mu.Unlock()
	return task, nil
}

func (s *Server) writeSSE(w http.ResponseWriter, id any, result any) {
	data, _ := json.Marshal(a2a.JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: result})
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func (s *Server) authorized(r *http.Request) bool {
	token := strings.TrimSpace(s.Options.BearerToken)
	if token == "" {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+token
}

func lastAssistantText(messages []agentcore.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		role, _ := messages[i]["role"].(string)
		if role != "assistant" {
			continue
		}
		if text, _ := messages[i]["text"].(string); strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func textStatusMessage(task a2a.Task, text string) *a2a.Message {
	message := a2a.NewTextMessage(a2a.RoleAgent, text)
	message.TaskID = task.ID
	message.ContextID = task.ContextID
	return &message
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func rpcError(id any, code int, message string) a2a.JSONRPCResponse {
	return a2a.JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &a2a.JSONRPCError{Code: code, Message: message}}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
