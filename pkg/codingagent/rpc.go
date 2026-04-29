package codingagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/badlogic/pigo/pkg/ai"
)

type RPCServer struct {
	Session *Session
}

type rpcCommand struct {
	ID                 string `json:"id,omitempty"`
	Type               string `json:"type"`
	Message            string `json:"message,omitempty"`
	Images             any    `json:"images,omitempty"`
	Command            string `json:"command,omitempty"`
	Name               string `json:"name,omitempty"`
	Level              string `json:"level,omitempty"`
	Provider           string `json:"provider,omitempty"`
	ModelID            string `json:"modelId,omitempty"`
	ParentSession      string `json:"parentSession,omitempty"`
	StreamingBehavior  string `json:"streamingBehavior,omitempty"`
	Enabled            *bool  `json:"enabled,omitempty"`
	Mode               string `json:"mode,omitempty"`
	CustomInstructions string `json:"customInstructions,omitempty"`
	Path               string `json:"path,omitempty"`
	// "name" is used by set_session_name.
	SessionPath      string               `json:"sessionPath,omitempty"`
	EntryID          string               `json:"entryId,omitempty"`
	OutputPath       string               `json:"outputPath,omitempty"`
	OAuthCredentials *ai.OAuthCredentials `json:"oauthCredentials,omitempty"`
	OAuthStorePath   string               `json:"oauthStorePath,omitempty"`
}

type rpcResponse struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Command string `json:"command"`
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

type rpcSlashCommand struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Source      string         `json:"source"`
	SourceInfo  map[string]any `json:"sourceInfo"`
}

func (s *RPCServer) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if s.Session == nil {
		return fmt.Errorf("missing session")
	}
	scanner := bufio.NewScanner(in)
	encoder := json.NewEncoder(out)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var command rpcCommand
		if err := json.Unmarshal(scanner.Bytes(), &command); err != nil {
			if err := encoder.Encode(rpcResponse{
				Type:    "response",
				Command: "unknown",
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				return err
			}
			continue
		}
		response := s.handle(ctx, command)
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *RPCServer) handle(ctx context.Context, command rpcCommand) rpcResponse {
	switch command.Type {
	case "prompt":
		if err := s.Session.Prompt(ctx, command.Message); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "steer":
		if err := s.Session.Steer(ctx, command.Message); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "follow_up":
		if err := s.Session.FollowUp(ctx, command.Message); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "abort":
		s.Session.Abort()
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "new_session":
		s.Session.NewSession()
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"cancelled": false},
		}

	case "get_state":
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    s.Session.State(),
		}

	case "set_model":
		model, err := s.Session.SetModel(command.Provider, command.ModelID)
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    model,
		}

	case "cycle_model":
		model, ok := s.Session.CycleModel()
		if !ok {
			return rpcResponse{
				ID:      command.ID,
				Type:    "response",
				Command: command.Type,
				Success: true,
				Data:    nil,
			}
		}
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"model": model, "thinkingLevel": s.Session.ThinkingLevel, "isScoped": false},
		}

	case "get_available_models":
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"models": s.Session.GetAvailableModels()},
		}

	case "set_oauth_credentials":
		if command.OAuthCredentials == nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: "missing oauthCredentials"}
		}
		if err := s.Session.SetOAuthCredentials(command.Provider, *command.OAuthCredentials); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "get_provider_auth_status":
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"providers": s.Session.GetProviderAuthStatus()},
		}

	case "get_oauth_providers":
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"providers": ai.GetOAuthProviderInfoList()},
		}

	case "load_oauth_store":
		if command.OAuthStorePath == "" {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: "missing oauthStorePath"}
		}
		if err := s.Session.LoadOAuthStore(command.OAuthStorePath); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "set_thinking_level":
		if err := s.Session.SetThinkingLevel(command.Level); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "cycle_thinking_level":
		level, ok := s.Session.CycleThinkingLevel()
		if !ok {
			return rpcResponse{
				ID:      command.ID,
				Type:    "response",
				Command: command.Type,
				Success: true,
				Data:    map[string]any{"level": nil},
			}
		}
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"level": level},
		}

	case "set_steering_mode":
		if err := s.Session.SetSteeringMode(command.Mode); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "set_follow_up_mode":
		if err := s.Session.SetFollowUpMode(command.Mode); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "compact":
		result := s.Session.Compact(command.CustomInstructions)
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: result}

	case "set_auto_compaction":
		if command.Enabled == nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: "missing enabled"}
		}
		s.Session.SetAutoCompactionEnabled(*command.Enabled)
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "set_auto_retry":
		if command.Enabled == nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: "missing enabled"}
		}
		s.Session.SetAutoRetryEnabled(*command.Enabled)
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "abort_retry":
		s.Session.AbortRetry()
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "bash":
		result, err := s.Session.Bash(ctx, command.Command)
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: result}

	case "abort_bash":
		s.Session.Abort()
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "get_session_stats":
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: s.Session.Stats()}

	case "export_html":
		path, err := s.Session.ExportToHTML(command.OutputPath)
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: map[string]any{"path": path}}

	case "switch_session":
		if err := s.Session.SwitchSession(command.SessionPath); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"cancelled": false},
		}

	case "fork":
		text, cancelled, err := s.Session.Fork(command.EntryID)
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data: map[string]any{
				"text":      text,
				"cancelled": cancelled,
			},
		}

	case "clone":
		cancelled, err := s.Session.Clone()
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"cancelled": cancelled},
		}

	case "get_fork_messages":
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data: map[string]any{
				"messages": s.Session.GetForkMessages(),
			},
		}

	case "get_last_assistant_text":
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data: map[string]any{
				"text": s.Session.GetLastAssistantText(),
			},
		}

	case "set_session_name":
		if err := s.Session.SetSessionName(command.Name); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "get_messages":
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: map[string]any{"messages": s.Session.Messages}}

	case "get_commands":
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data: map[string]any{
				"commands": []rpcSlashCommand{},
			},
		}

	default:
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: false,
			Error:   "unsupported command",
		}
	}
}
