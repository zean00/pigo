package codingagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

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
	Attachments        any    `json:"attachments,omitempty"`
	Command            string `json:"command,omitempty"`
	Name               string `json:"name,omitempty"`
	Label              string `json:"label,omitempty"`
	CustomType         string `json:"customType,omitempty"`
	Data               any    `json:"data,omitempty"`
	Level              string `json:"level,omitempty"`
	Provider           string `json:"provider,omitempty"`
	ModelID            string `json:"modelId,omitempty"`
	ParentSession      string `json:"parentSession,omitempty"`
	StreamingBehavior  string `json:"streamingBehavior,omitempty"`
	Enabled            *bool  `json:"enabled,omitempty"`
	Mode               string `json:"mode,omitempty"`
	CustomInstructions string `json:"customInstructions,omitempty"`
	Summary            string `json:"summary,omitempty"`
	Path               string `json:"path,omitempty"`
	// "name" is used by set_session_name.
	SessionPath      string               `json:"sessionPath,omitempty"`
	EntryID          string               `json:"entryId,omitempty"`
	OutputPath       string               `json:"outputPath,omitempty"`
	AgentDir         string               `json:"agentDir,omitempty"`
	PromptPaths      []string             `json:"promptPaths,omitempty"`
	SkillPaths       []string             `json:"skillPaths,omitempty"`
	IncludeDefaults  *bool                `json:"includeDefaults,omitempty"`
	Commands         []SlashCommandInfo   `json:"commands,omitempty"`
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
		attachments, err := rpcPromptAttachments(command.Attachments, command.Images)
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		if len(attachments) > 0 {
			err = s.Session.PromptWithAttachments(ctx, command.Message, attachments)
		} else {
			err = s.Session.Prompt(ctx, command.Message)
		}
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "steer":
		attachments, err := rpcPromptAttachments(command.Attachments, command.Images)
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		if len(attachments) > 0 {
			err = s.Session.SteerWithAttachments(ctx, command.Message, attachments)
		} else {
			err = s.Session.Steer(ctx, command.Message)
		}
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "follow_up":
		attachments, err := rpcPromptAttachments(command.Attachments, command.Images)
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		if len(attachments) > 0 {
			err = s.Session.FollowUpWithAttachments(ctx, command.Message, attachments)
		} else {
			err = s.Session.FollowUp(ctx, command.Message)
		}
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "abort":
		s.Session.Abort()
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "new_session":
		s.Session.NewSessionWithParent(command.ParentSession)
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"cancelled": false},
		}

	case "branch":
		var err error
		if strings.TrimSpace(command.Summary) != "" {
			err = s.Session.BranchWithSummary(command.EntryID, command.Summary)
		} else {
			err = s.Session.Branch(command.EntryID)
		}
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "tree":
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"nodes": s.Session.Tree()},
		}

	case "set_label":
		if err := s.Session.SetLabel(command.EntryID, command.Label); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "get_label":
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"label": s.Session.GetLabel(command.EntryID)},
		}

	case "append_custom_entry":
		entryID, err := s.Session.AppendCustomEntry(command.CustomType, command.Data)
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: map[string]any{"entryId": entryID}}

	case "get_custom_entries":
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"entries": s.Session.CustomEntries(command.CustomType)},
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

	case "retry":
		if err := s.Session.RetryLast(ctx); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
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

	case "export":
		outputPath := command.OutputPath
		if outputPath == "" {
			outputPath = command.Path
		}
		if outputPath != "" && strings.HasSuffix(strings.ToLower(outputPath), ".jsonl") {
			path, err := s.Session.ExportToJSONL(outputPath)
			if err != nil {
				return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
			}
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: map[string]any{"path": path, "format": "jsonl"}}
		}
		path, err := s.Session.ExportToHTML(outputPath)
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: map[string]any{"path": path, "format": "html"}}

	case "share":
		outputPath := command.OutputPath
		if outputPath == "" {
			outputPath = command.Path
		}
		url, err := s.Session.Share(outputPath)
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: map[string]any{"url": url}}

	case "import":
		sessionPath := command.SessionPath
		if sessionPath == "" {
			sessionPath = command.Path
		}
		if sessionPath == "" {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: "missing sessionPath"}
		}
		if err := s.Session.SwitchSession(sessionPath); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: map[string]any{"cancelled": false}}

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
		commands := make([]rpcSlashCommand, 0, 2+len(s.Session.extensionCommands)+len(s.Session.promptTemplates)+len(s.Session.skills))
		for _, command := range s.Session.GetSlashCommands() {
			commands = append(commands, rpcSlashCommand{
				Name:        command.Name,
				Description: command.Description,
				Source:      command.Source,
				SourceInfo:  command.SourceInfo,
			})
		}
		return rpcResponse{
			ID:      command.ID,
			Type:    "response",
			Command: command.Type,
			Success: true,
			Data:    map[string]any{"commands": commands},
		}

	case "register_commands":
		s.Session.SetExtensionCommands(command.Commands)
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

	case "reload_resources":
		includeDefaults := true
		if command.IncludeDefaults != nil {
			includeDefaults = *command.IncludeDefaults
		}
		s.Session.LoadSlashCommandResources(ResourceLoadOptions{
			AgentDir:        command.AgentDir,
			PromptPaths:     command.PromptPaths,
			SkillPaths:      command.SkillPaths,
			IncludeDefaults: includeDefaults,
		})
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true}

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

func rpcPromptAttachments(attachmentsRaw, imagesRaw any) ([]PromptAttachment, error) {
	attachments, err := rpcAttachments(attachmentsRaw)
	if err != nil {
		return nil, err
	}
	images, err := rpcAttachments(imagesRaw)
	if err != nil {
		return nil, err
	}
	return append(attachments, images...), nil
}

func rpcAttachments(raw any) ([]PromptAttachment, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var attachments []PromptAttachment
	if err := json.Unmarshal(data, &attachments); err == nil {
		return attachments, nil
	}
	var single PromptAttachment
	if err := json.Unmarshal(data, &single); err != nil {
		return nil, err
	}
	return []PromptAttachment{single}, nil
}
