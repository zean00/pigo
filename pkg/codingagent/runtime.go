package codingagent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
)

var ErrSessionOperationCancelled = errors.New("session operation cancelled")

type Session struct {
	Root               string
	Name               string
	ThinkingLevel      string
	Provider           string
	ModelID            string
	Store              *SessionStore
	SteeringMode       string
	FollowUpMode       string
	AutoCompaction     bool
	AutoRetry          bool
	IsCompacting       bool
	LastPrompt         string
	lastAttachments    []PromptAttachment
	Compactor          CompactorFunc
	ShellCommandPrefix string
	ToolOutputLimit    int
	Turns              []AssistantTurn
	turnIndex          int
	Events             []agentcore.Event
	Messages           []agentcore.Message
	SessionEntryTypes  []string
	AvailableModels    []ModelInfo
	IsStreaming        bool
	OAuthCredentials   map[string]ai.OAuthCredentials
	mu                 sync.Mutex
	promptCancel       context.CancelFunc
	retryCancel        context.CancelFunc
	bashCancel         context.CancelFunc
	compactionCancel   context.CancelFunc

	entries       []SessionEntry
	entriesByID   map[string]SessionEntry
	leafID        string
	entrySequence int
	parentSession string
	labelsByID    map[string]string
	labelTimes    map[string]string
	customEntries []SessionEntry

	extensionCommands     []SlashCommandInfo
	extensionHandlers     map[string]ExtensionCommandHandler
	extensionTools        []agentcore.Tool
	extensionToolSpecs    []ai.Tool
	extensionFlags        map[string]ExtensionFlag
	extensionFlagValues   map[string]any
	extensionStatuses     map[string]string
	resourceProviders     []ExtensionResourceProvider
	beforeSwitchHooks     []SessionBeforeSwitchHandler
	beforeForkHooks       []SessionBeforeForkHandler
	beforeTreeHooks       []SessionBeforeTreeHandler
	beforeCompactHooks    []SessionBeforeCompactHandler
	beforeAgentStartHooks []BeforeAgentStartHandler
	inputHooks            []InputHandler
	contextHooks          []ContextHandler
	providerPayloadHooks  []ProviderPayloadHandler
	providerResponseHooks []ProviderResponseHandler
	toolCallHooks         []ToolCallHandler
	toolResultHooks       []ToolResultHandler
	promptTemplates       []SlashCommandInfo
	skills                []SlashCommandInfo
	resourceDiagnostics   []ResourceDiagnostic
}

type CompactorFunc func(ctx context.Context, messages []ai.Message, instructions string) (string, error)

type PromptAttachment struct {
	Type     string
	Path     string
	Data     string
	MimeType string
	Text     string
}

type ModelInfo struct {
	Provider string `json:"provider"`
	ModelID  string `json:"id"`
}

type SlashCommandInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Source      string         `json:"source"`
	SourceInfo  map[string]any `json:"sourceInfo"`
	Content     string         `json:"-"`
	FilePath    string         `json:"-"`
	BaseDir     string         `json:"-"`
	Disabled    bool           `json:"-"`
}

type ResourceCollision struct {
	ResourceType string `json:"resourceType"`
	Name         string `json:"name"`
	WinnerPath   string `json:"winnerPath"`
	LoserPath    string `json:"loserPath"`
}

type ResourceDiagnostic struct {
	Type      string             `json:"type"`
	Message   string             `json:"message"`
	Path      string             `json:"path,omitempty"`
	Collision *ResourceCollision `json:"collision,omitempty"`
}

type ProjectContextFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ExtensionCommandContext struct {
	Session *Session
	Name    string
	Args    string
}

type ExtensionCommandResult struct {
	Prompt  string
	Handled bool
}

type ExtensionCommandHandler func(ctx context.Context, command ExtensionCommandContext) (ExtensionCommandResult, error)

type ExtensionFlag struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Type          string `json:"type,omitempty"`
	Default       any    `json:"default,omitempty"`
	ExtensionPath string `json:"extensionPath,omitempty"`
}

type ExtensionResourceEvent struct {
	Type   string `json:"type"`
	CWD    string `json:"cwd"`
	Reason string `json:"reason"`
}

type ExtensionResourceResult struct {
	SkillPaths  []string `json:"skillPaths,omitempty"`
	PromptPaths []string `json:"promptPaths,omitempty"`
	ThemePaths  []string `json:"themePaths,omitempty"`
}

type ExtensionResourceProvider func(ctx context.Context, event ExtensionResourceEvent) (ExtensionResourceResult, error)

type SessionBeforeSwitchEvent struct {
	Type              string `json:"type"`
	Reason            string `json:"reason"`
	TargetSessionFile string `json:"targetSessionFile,omitempty"`
}

type SessionBeforeForkEvent struct {
	Type     string `json:"type"`
	EntryID  string `json:"entryId"`
	Position string `json:"position"`
}

type SessionBeforeTreeEvent struct {
	Type      string `json:"type"`
	TargetID  string `json:"targetId"`
	OldLeafID string `json:"oldLeafId,omitempty"`
}

type SessionBeforeCompactEvent struct {
	Type               string `json:"type"`
	TokensBefore       int    `json:"tokensBefore"`
	CustomInstructions string `json:"customInstructions,omitempty"`
}

type InputEvent struct {
	Type        string             `json:"type"`
	Text        string             `json:"text"`
	Attachments []PromptAttachment `json:"attachments,omitempty"`
	Source      string             `json:"source"`
}

type ContextEvent struct {
	Type     string       `json:"type"`
	Messages []ai.Message `json:"messages"`
}

type BeforeAgentStartEvent struct {
	Type         string             `json:"type"`
	Prompt       string             `json:"prompt"`
	Attachments  []PromptAttachment `json:"attachments,omitempty"`
	SystemPrompt string             `json:"systemPrompt"`
}

type SessionBeforeResult struct {
	Cancel bool `json:"cancel,omitempty"`
}

type InputResult struct {
	Action      string             `json:"action,omitempty"`
	Text        string             `json:"text,omitempty"`
	Attachments []PromptAttachment `json:"attachments,omitempty"`
}

type ContextResult struct {
	Messages []ai.Message `json:"messages,omitempty"`
}

type BeforeAgentStartResult struct {
	Message      agentcore.Message `json:"message,omitempty"`
	SystemPrompt *string           `json:"systemPrompt,omitempty"`
}

type ToolCallEvent struct {
	Type       string         `json:"type"`
	ToolName   string         `json:"toolName"`
	ToolCallID string         `json:"toolCallId"`
	Input      map[string]any `json:"input"`
}

type ToolCallResult struct {
	Block  bool           `json:"block,omitempty"`
	Reason string         `json:"reason,omitempty"`
	Input  map[string]any `json:"input,omitempty"`
}

type ToolResultEvent struct {
	Type       string            `json:"type"`
	ToolName   string            `json:"toolName"`
	ToolCallID string            `json:"toolCallId"`
	Input      map[string]any    `json:"input"`
	Content    []ai.ContentBlock `json:"content,omitempty"`
	Text       string            `json:"text,omitempty"`
	Details    map[string]any    `json:"details,omitempty"`
	IsError    bool              `json:"isError"`
}

type ToolResultPatch struct {
	Content []ai.ContentBlock `json:"content,omitempty"`
	Text    *string           `json:"text,omitempty"`
	Details map[string]any    `json:"details,omitempty"`
	IsError *bool             `json:"isError,omitempty"`
}

type ProviderPayloadHandler func(ctx context.Context, payload any, req ai.CompletionRequest) (any, error)
type ProviderResponseHandler func(ctx context.Context, response ai.ProviderResponse, req ai.CompletionRequest) error
type ToolCallHandler func(ctx context.Context, event ToolCallEvent) (ToolCallResult, error)
type ToolResultHandler func(ctx context.Context, event ToolResultEvent) (ToolResultPatch, error)
type BeforeAgentStartHandler func(ctx context.Context, event BeforeAgentStartEvent) (BeforeAgentStartResult, error)

type SessionBeforeSwitchHandler func(ctx context.Context, event SessionBeforeSwitchEvent) (SessionBeforeResult, error)
type SessionBeforeForkHandler func(ctx context.Context, event SessionBeforeForkEvent) (SessionBeforeResult, error)
type SessionBeforeTreeHandler func(ctx context.Context, event SessionBeforeTreeEvent) (SessionBeforeResult, error)
type SessionBeforeCompactHandler func(ctx context.Context, event SessionBeforeCompactEvent) (SessionBeforeResult, error)
type InputHandler func(ctx context.Context, event InputEvent) (InputResult, error)
type ContextHandler func(ctx context.Context, event ContextEvent) (ContextResult, error)

const (
	bashExecutionTextPrefix        = "Ran `%s`\n"
	branchSummaryPrefix            = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
	branchSummarySuffix            = "\n</summary>\n"
	compactionSummaryPrefix        = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	compactionSummarySuffix        = "\n</summary>\n"
	autoCompactionMessageThreshold = 18
	autoRetryDelay                 = 150 * time.Millisecond
)

type promptStreamState struct {
	messages         []agentcore.Message
	activeMessageIdx int
	activeMessage    agentcore.Message
}

type ForkMessage struct {
	EntryID string `json:"entryId"`
	Text    string `json:"text"`
}

type SessionTreeNode struct {
	ID             string            `json:"id"`
	Type           string            `json:"type"`
	ParentID       string            `json:"parentId"`
	Role           string            `json:"role,omitempty"`
	Timestamp      string            `json:"timestamp"`
	Text           string            `json:"text,omitempty"`
	Label          string            `json:"label,omitempty"`
	LabelTimestamp string            `json:"labelTimestamp,omitempty"`
	Children       []SessionTreeNode `json:"children"`
}

type CompactionResult struct {
	Summary        string `json:"summary"`
	FirstKeptEntry string `json:"firstKeptEntryId"`
	TokensBefore   int    `json:"tokensBefore"`
	Cancelled      bool   `json:"cancelled"`
}

type State struct {
	IsStreaming    bool   `json:"isStreaming"`
	MessageCount   int    `json:"messageCount"`
	SessionID      string `json:"sessionId"`
	SessionName    string `json:"sessionName,omitempty"`
	ThinkingLevel  string `json:"thinkingLevel"`
	Provider       string `json:"provider"`
	ModelID        string `json:"modelId"`
	SteeringMode   string `json:"steeringMode"`
	FollowUpMode   string `json:"followUpMode"`
	AutoCompaction bool   `json:"autoCompactionEnabled"`
	IsCompacting   bool   `json:"isCompacting"`
	// Not used in the simplified runtime yet.
	PendingMessageCnt int `json:"pendingMessageCount"`
	ContextUsage      any `json:"contextUsage,omitempty"`
}

type BashResult struct {
	Command   string `json:"command"`
	Output    string `json:"output"`
	ExitCode  int    `json:"exitCode"`
	Cancelled bool   `json:"cancelled"`
}

type Stats struct {
	SessionFile       string `json:"sessionFile,omitempty"`
	SessionID         string `json:"sessionId"`
	UserMessages      int    `json:"userMessages"`
	AssistantMessages int    `json:"assistantMessages"`
	ToolCalls         int    `json:"toolCalls"`
	ToolResults       int    `json:"toolResults"`
	TotalMessages     int    `json:"totalMessages"`
	Tokens            struct {
		Input      int `json:"input"`
		Output     int `json:"output"`
		CacheRead  int `json:"cacheRead"`
		CacheWrite int `json:"cacheWrite"`
		Total      int `json:"total"`
	} `json:"tokens"`
	Cost         float64 `json:"cost"`
	ContextUsage any     `json:"contextUsage,omitempty"`
}

type ContextUsage struct {
	EstimatedTokens int     `json:"estimatedTokens"`
	MessageCount    int     `json:"messageCount"`
	Limit           int     `json:"limit,omitempty"`
	Percent         float64 `json:"percent,omitempty"`
}

func NewSession(root string, turns []AssistantTurn) *Session {
	session := &Session{
		Root:         root,
		SteeringMode: "one-at-a-time",
		FollowUpMode: "one-at-a-time",
		Turns:        turns,
	}
	session.NewSession()
	return session
}

func (s *Session) ExtensionCommands() []SlashCommandInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	commands := make([]SlashCommandInfo, len(s.extensionCommands))
	copy(commands, s.extensionCommands)
	return commands
}

func (s *Session) SetExtensionCommands(commands []SlashCommandInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.extensionCommands = copySlashCommands(commands)
	s.extensionHandlers = nil
}

func (s *Session) RegisterExtensionFlag(flag ExtensionFlag) {
	flag.Name = strings.TrimSpace(flag.Name)
	if flag.Name == "" {
		return
	}
	if flag.Type == "" {
		switch flag.Default.(type) {
		case bool:
			flag.Type = "boolean"
		default:
			flag.Type = "string"
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.extensionFlags == nil {
		s.extensionFlags = map[string]ExtensionFlag{}
	}
	s.extensionFlags[flag.Name] = flag
	if flag.Default != nil {
		if s.extensionFlagValues == nil {
			s.extensionFlagValues = map[string]any{}
		}
		if _, exists := s.extensionFlagValues[flag.Name]; !exists {
			s.extensionFlagValues[flag.Name] = flag.Default
		}
	}
}

func (s *Session) SetExtensionFlagValue(name string, value any) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("missing flag name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	flag, registered := s.extensionFlags[name]
	if registered {
		switch flag.Type {
		case "boolean":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("extension flag %s expects a boolean value", name)
			}
		case "string", "":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("extension flag %s expects a string value", name)
			}
		}
	}
	if s.extensionFlagValues == nil {
		s.extensionFlagValues = map[string]any{}
	}
	s.extensionFlagValues[name] = value
	return nil
}

func (s *Session) ExtensionFlagValue(name string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.extensionFlagValues[strings.TrimSpace(name)]
	return value, ok
}

func (s *Session) ExtensionFlags() []ExtensionFlag {
	s.mu.Lock()
	defer s.mu.Unlock()
	flags := make([]ExtensionFlag, 0, len(s.extensionFlags))
	for _, flag := range s.extensionFlags {
		flags = append(flags, flag)
	}
	sort.Slice(flags, func(i, j int) bool {
		return flags[i].Name < flags[j].Name
	})
	return flags
}

func (s *Session) ExtensionFlagValues() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make(map[string]any, len(s.extensionFlagValues))
	for key, value := range s.extensionFlagValues {
		values[key] = value
	}
	return values
}

func (s *Session) SetExtensionStatus(key, status string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.extensionStatuses == nil {
		s.extensionStatuses = map[string]string{}
	}
	if strings.TrimSpace(status) == "" {
		delete(s.extensionStatuses, key)
		return
	}
	s.extensionStatuses[key] = status
}

func (s *Session) ExtensionStatuses() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	statuses := make(map[string]string, len(s.extensionStatuses))
	for key, value := range s.extensionStatuses {
		statuses[key] = value
	}
	return statuses
}

func (s *Session) RegisterExtensionResourceProvider(provider ExtensionResourceProvider) {
	if provider == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resourceProviders = append(s.resourceProviders, provider)
}

func (s *Session) RegisterSessionBeforeSwitchHandler(handler SessionBeforeSwitchHandler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeSwitchHooks = append(s.beforeSwitchHooks, handler)
}

func (s *Session) RegisterSessionBeforeForkHandler(handler SessionBeforeForkHandler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeForkHooks = append(s.beforeForkHooks, handler)
}

func (s *Session) RegisterSessionBeforeTreeHandler(handler SessionBeforeTreeHandler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeTreeHooks = append(s.beforeTreeHooks, handler)
}

func (s *Session) RegisterSessionBeforeCompactHandler(handler SessionBeforeCompactHandler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeCompactHooks = append(s.beforeCompactHooks, handler)
}

func (s *Session) RegisterBeforeAgentStartHandler(handler BeforeAgentStartHandler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeAgentStartHooks = append(s.beforeAgentStartHooks, handler)
}

func (s *Session) RegisterInputHandler(handler InputHandler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputHooks = append(s.inputHooks, handler)
}

func (s *Session) RegisterContextHandler(handler ContextHandler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contextHooks = append(s.contextHooks, handler)
}

func (s *Session) RegisterProviderPayloadHandler(handler ProviderPayloadHandler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providerPayloadHooks = append(s.providerPayloadHooks, handler)
}

func (s *Session) RegisterProviderResponseHandler(handler ProviderResponseHandler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providerResponseHooks = append(s.providerResponseHooks, handler)
}

func (s *Session) RegisterToolCallHandler(handler ToolCallHandler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCallHooks = append(s.toolCallHooks, handler)
}

func (s *Session) RegisterToolResultHandler(handler ToolResultHandler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolResultHooks = append(s.toolResultHooks, handler)
}

func (s *Session) RegisterExtensionCommand(command SlashCommandInfo, handler ExtensionCommandHandler) {
	command.Source = "extension"
	command.Name = strings.TrimSpace(command.Name)
	if command.SourceInfo == nil {
		command.SourceInfo = map[string]any{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.extensionHandlers == nil {
		s.extensionHandlers = map[string]ExtensionCommandHandler{}
	}
	replaced := false
	for i, existing := range s.extensionCommands {
		if strings.EqualFold(existing.Name, command.Name) {
			s.extensionCommands[i] = cloneSlashCommand(command)
			replaced = true
			break
		}
	}
	if !replaced {
		s.extensionCommands = append(s.extensionCommands, cloneSlashCommand(command))
	}
	if handler != nil {
		s.extensionHandlers[strings.ToLower(command.Name)] = handler
	}
}

func (s *Session) SetExtensionTools(tools []agentcore.Tool, specs []ai.Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.extensionTools = append([]agentcore.Tool(nil), tools...)
	s.extensionToolSpecs = append([]ai.Tool(nil), specs...)
}

func (s *Session) RegisterExtensionTool(tool agentcore.Tool, spec ai.Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.extensionTools {
		if strings.EqualFold(existing.Name, tool.Name) {
			s.extensionTools[i] = tool
			s.extensionToolSpecs[i] = spec
			return
		}
	}
	s.extensionTools = append(s.extensionTools, tool)
	s.extensionToolSpecs = append(s.extensionToolSpecs, spec)
}

func (s *Session) ExtensionTools() ([]agentcore.Tool, []ai.Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agentcore.Tool(nil), s.extensionTools...), append([]ai.Tool(nil), s.extensionToolSpecs...)
}

func (s *Session) ExecuteExtensionCommand(ctx context.Context, name, args string) (ExtensionCommandResult, bool, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	s.mu.Lock()
	handler := s.extensionHandlers[key]
	s.mu.Unlock()
	if handler == nil {
		return ExtensionCommandResult{}, false, nil
	}
	result, err := handler(ctx, ExtensionCommandContext{
		Session: s,
		Name:    strings.TrimSpace(name),
		Args:    strings.TrimSpace(args),
	})
	return result, true, err
}

func (s *Session) PromptTemplates() []SlashCommandInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	commands := make([]SlashCommandInfo, len(s.promptTemplates))
	copy(commands, s.promptTemplates)
	return commands
}

func (s *Session) SetPromptTemplates(commands []SlashCommandInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promptTemplates = copySlashCommands(commands)
}

func (s *Session) Skills() []SlashCommandInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	commands := make([]SlashCommandInfo, len(s.skills))
	copy(commands, s.skills)
	return commands
}

func (s *Session) SetSkills(commands []SlashCommandInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skills = copySlashCommands(commands)
}

func (s *Session) ResourceDiagnostics() []ResourceDiagnostic {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ResourceDiagnostic(nil), s.resourceDiagnostics...)
}

func (s *Session) SendCustomMessage(customType string, content any, display bool, details any) error {
	customType = strings.TrimSpace(customType)
	if customType == "" {
		return fmt.Errorf("missing customType")
	}
	message := agentcore.Message{
		"role":       "custom",
		"customType": customType,
		"display":    display,
		"content":    content,
		"details":    details,
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
	}
	return s.appendEntry(SessionEntry{
		Type:    "message",
		Message: message,
	})
}

func (s *Session) AppendCustomEntry(customType string, data any) (string, error) {
	customType = strings.TrimSpace(customType)
	if customType == "" {
		return "", fmt.Errorf("missing customType")
	}
	entry := SessionEntry{
		Type:       "custom",
		ID:         s.newEntryID(),
		CustomType: customType,
		Data:       data,
	}
	if err := s.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

func (s *Session) CustomEntries(customType string) []SessionEntry {
	customType = strings.TrimSpace(customType)
	out := make([]SessionEntry, 0, len(s.customEntries))
	for _, entry := range s.customEntries {
		if customType != "" && entry.CustomType != customType {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (s *Session) Entries() []SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SessionEntry(nil), s.entries...)
}

func (s *Session) RuntimeEvents() []agentcore.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]agentcore.Event, 0, len(s.Events))
	for _, event := range s.Events {
		cloned := agentcore.Event{}
		for key, value := range event {
			cloned[key] = value
		}
		out = append(out, cloned)
	}
	return out
}

func copySlashCommands(commands []SlashCommandInfo) []SlashCommandInfo {
	if len(commands) == 0 {
		return nil
	}
	cloned := make([]SlashCommandInfo, 0, len(commands))
	for _, command := range commands {
		cloned = append(cloned, cloneSlashCommand(command))
	}
	return cloned
}

func cloneSlashCommand(command SlashCommandInfo) SlashCommandInfo {
	sourceInfo := make(map[string]any, len(command.SourceInfo))
	for key, value := range command.SourceInfo {
		sourceInfo[key] = value
	}
	return SlashCommandInfo{
		Name:        strings.TrimSpace(command.Name),
		Description: strings.TrimSpace(command.Description),
		Source:      strings.TrimSpace(command.Source),
		SourceInfo:  sourceInfo,
		Content:     command.Content,
		FilePath:    command.FilePath,
		BaseDir:     command.BaseDir,
		Disabled:    command.Disabled,
	}
}

func (s *Session) GetSlashCommands() []SlashCommandInfo {
	var commands []SlashCommandInfo
	commands = append(commands, SlashCommandInfo{
		Name:        "branch",
		Description: "branch conversation history at an entry",
		Source:      "prompt",
		SourceInfo:  map[string]any{},
	})
	commands = append(commands, SlashCommandInfo{
		Name:        "tree",
		Description: "read conversation branch tree",
		Source:      "prompt",
		SourceInfo:  map[string]any{},
	})

	for _, command := range s.ExtensionCommands() {
		commands = append(commands, command)
	}
	for _, command := range s.PromptTemplates() {
		commands = append(commands, command)
	}
	for _, command := range s.Skills() {
		commands = append(commands, command)
	}
	return dedupeSlashCommands(commands)
}

func dedupeSlashCommands(commands []SlashCommandInfo) []SlashCommandInfo {
	seen := map[string]struct{}{}
	out := make([]SlashCommandInfo, 0, len(commands))
	for _, command := range commands {
		name := strings.TrimSpace(command.Name)
		if name == "" || command.Source == "" {
			continue
		}
		key := strings.ToLower(name) + "|" + strings.ToLower(command.Source)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		command.Name = name
		if command.SourceInfo == nil {
			command.SourceInfo = map[string]any{}
		}
		out = append(out, command)
	}
	return out
}

func (s *Session) expandPromptTemplate(prompt string) string {
	if !strings.HasPrefix(prompt, "/") {
		return prompt
	}
	name, args := splitCommandPrompt(prompt)
	if name == "" {
		return prompt
	}
	for _, template := range s.PromptTemplates() {
		if template.Name != name {
			continue
		}
		if template.Content == "" {
			return prompt
		}
		return substitutePromptArgs(template.Content, parseCommandArgs(args))
	}
	if strings.HasPrefix(name, "skill:") {
		skillName := strings.TrimPrefix(name, "skill:")
		for _, skill := range s.Skills() {
			if strings.TrimPrefix(skill.Name, "skill:") != skillName {
				continue
			}
			return formatSkillInvocationPrompt(skill, args)
		}
	}
	return prompt
}

func (s *Session) expandExtensionCommand(ctx context.Context, prompt string) (string, bool, error) {
	if !strings.HasPrefix(prompt, "/") {
		return prompt, false, nil
	}
	name, args := splitCommandPrompt(prompt)
	if name == "" {
		return prompt, false, nil
	}
	result, ok, err := s.ExecuteExtensionCommand(ctx, name, args)
	if err != nil {
		return "", true, err
	}
	if !ok {
		return prompt, false, nil
	}
	if result.Handled {
		return "", true, nil
	}
	if strings.TrimSpace(result.Prompt) == "" {
		return prompt, false, nil
	}
	return result.Prompt, false, nil
}

func splitCommandPrompt(prompt string) (string, string) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(prompt, "/"))
	if trimmed == "" {
		return "", ""
	}
	name, args, ok := strings.Cut(trimmed, " ")
	if !ok {
		return trimmed, ""
	}
	return strings.TrimSpace(name), strings.TrimSpace(args)
}

func parseCommandArgs(argsString string) []string {
	args := []string{}
	current := strings.Builder{}
	var quote rune
	for _, char := range argsString {
		if quote != 0 {
			if char == quote {
				quote = 0
				continue
			}
			current.WriteRune(char)
			continue
		}
		if char == '"' || char == '\'' {
			quote = char
			continue
		}
		if char == ' ' || char == '\t' {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(char)
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

func substitutePromptArgs(content string, args []string) string {
	result := content
	for i, arg := range args {
		result = strings.ReplaceAll(result, fmt.Sprintf("$%d", i+1), arg)
	}
	for i := 1; i <= 32; i++ {
		if i > len(args) {
			result = strings.ReplaceAll(result, fmt.Sprintf("$%d", i), "")
		}
	}
	allArgs := strings.Join(args, " ")
	result = substitutePromptArgSlices(result, args)
	result = strings.ReplaceAll(result, "$ARGUMENTS", allArgs)
	result = strings.ReplaceAll(result, "$@", allArgs)
	return result
}

func substitutePromptArgSlices(content string, args []string) string {
	result := content
	for {
		start := strings.Index(result, "${@:")
		if start < 0 {
			return result
		}
		end := strings.Index(result[start:], "}")
		if end < 0 {
			return result
		}
		end += start
		expression := result[start+4 : end]
		parts := strings.Split(expression, ":")
		from := parsePositiveInt(parts[0]) - 1
		if from < 0 {
			from = 0
		}
		to := len(args)
		if len(parts) > 1 {
			length := parsePositiveInt(parts[1])
			if length >= 0 {
				to = from + length
			}
		}
		if from > len(args) {
			from = len(args)
		}
		if to > len(args) {
			to = len(args)
		}
		replacement := strings.Join(args[from:to], " ")
		result = result[:start] + replacement + result[end+1:]
	}
}

func parsePositiveInt(raw string) int {
	value := 0
	for _, char := range strings.TrimSpace(raw) {
		if char < '0' || char > '9' {
			return 0
		}
		value = value*10 + int(char-'0')
	}
	return value
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func currentGitBranch(root string) string {
	output, err := exec.Command("git", "-C", root, "--no-optional-locks", "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func shortGitStatus(root string) string {
	output, err := exec.Command("git", "-C", root, "--no-optional-locks", "status", "--short").Output()
	if err != nil {
		return ""
	}
	text, _ := truncateToolOutput(strings.TrimSpace(string(output)), 4000)
	return text
}

func packageContext(root string) string {
	candidates := []string{"package.json", "go.mod", "pyproject.toml", "Cargo.toml", "Gemfile", "pom.xml"}
	lines := make([]string, 0, len(candidates))
	for _, name := range candidates {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		lines = append(lines, "- "+name)
	}
	return strings.Join(lines, "\n")
}

func addStringList(target map[string]struct{}, value any) {
	switch typed := value.(type) {
	case []string:
		for _, item := range typed {
			if strings.TrimSpace(item) != "" {
				target[item] = struct{}{}
			}
		}
	case []any:
		for _, item := range typed {
			text, _ := item.(string)
			if strings.TrimSpace(text) != "" {
				target[text] = struct{}{}
			}
		}
	}
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func formatSkillInvocationPrompt(skill SlashCommandInfo, args string) string {
	var builder strings.Builder
	builder.WriteString("Use the following skill for this request.\n\n")
	builder.WriteString(formatSkillForPrompt(skill))
	if strings.TrimSpace(args) != "" {
		builder.WriteString("\n\nRequest:\n")
		builder.WriteString(strings.TrimSpace(args))
	}
	return builder.String()
}

func (s *Session) transformContextWithSkills(messages []ai.Message) []ai.Message {
	skillPrompt := s.formatSkillsForPrompt()
	if strings.TrimSpace(skillPrompt) == "" {
		return messages
	}
	out := make([]ai.Message, 0, len(messages)+1)
	out = append(out, ai.Message{Role: "user", Content: skillPrompt})
	out = append(out, messages...)
	return out
}

func (s *Session) transformContextWithHooks(ctx context.Context, messages []ai.Message) ([]ai.Message, error) {
	current := s.transformContextWithSkills(messages)
	s.mu.Lock()
	hooks := append([]ContextHandler(nil), s.contextHooks...)
	s.mu.Unlock()
	for _, hook := range hooks {
		event := ContextEvent{Type: "context", Messages: append([]ai.Message(nil), current...)}
		result, err := hook(ctx, event)
		if err != nil {
			return nil, err
		}
		if result.Messages != nil {
			current = append([]ai.Message(nil), result.Messages...)
		}
	}
	return current, nil
}

func (s *Session) applyProviderPayloadHooks(ctx context.Context, payload any, req ai.CompletionRequest) (any, error) {
	current := payload
	s.mu.Lock()
	hooks := append([]ProviderPayloadHandler(nil), s.providerPayloadHooks...)
	s.mu.Unlock()
	for _, hook := range hooks {
		next, err := hook(ctx, current, req)
		if err != nil {
			return nil, err
		}
		if next != nil {
			current = next
		}
	}
	return current, nil
}

func (s *Session) applyProviderResponseHooks(ctx context.Context, response ai.ProviderResponse, req ai.CompletionRequest) error {
	s.mu.Lock()
	hooks := append([]ProviderResponseHandler(nil), s.providerResponseHooks...)
	s.mu.Unlock()
	for _, hook := range hooks {
		if err := hook(ctx, response, req); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) applyToolCallHooks(ctx context.Context, input agentcore.BeforeToolCallContext) (agentcore.BeforeToolCallResult, error) {
	currentInput := cloneMap(input.Args)
	s.mu.Lock()
	hooks := append([]ToolCallHandler(nil), s.toolCallHooks...)
	s.mu.Unlock()
	for _, hook := range hooks {
		result, err := hook(ctx, ToolCallEvent{
			Type:       "tool_call",
			ToolName:   input.ToolCall.Name,
			ToolCallID: input.ToolCall.ID,
			Input:      cloneMap(currentInput),
		})
		if err != nil {
			return agentcore.BeforeToolCallResult{}, err
		}
		if result.Input != nil {
			currentInput = cloneMap(result.Input)
		}
		if result.Block {
			return agentcore.BeforeToolCallResult{Block: true, Reason: result.Reason}, nil
		}
	}
	return agentcore.BeforeToolCallResult{Args: currentInput}, nil
}

func (s *Session) applyToolResultHooks(ctx context.Context, input agentcore.AfterToolCallContext) (agentcore.ToolResult, error) {
	current := input.Result
	s.mu.Lock()
	hooks := append([]ToolResultHandler(nil), s.toolResultHooks...)
	s.mu.Unlock()
	for _, hook := range hooks {
		patch, err := hook(ctx, ToolResultEvent{
			Type:       "tool_result",
			ToolName:   input.ToolCall.Name,
			ToolCallID: input.ToolCall.ID,
			Input:      cloneMap(input.Args),
			Content:    append([]ai.ContentBlock(nil), current.Content...),
			Text:       current.Text,
			Details:    cloneMap(current.Details),
			IsError:    current.IsError,
		})
		if err != nil {
			return agentcore.ToolResult{}, err
		}
		if patch.Content != nil {
			current.Content = append([]ai.ContentBlock(nil), patch.Content...)
		}
		if patch.Text != nil {
			current.Text = *patch.Text
		}
		if patch.Details != nil {
			current.Details = cloneMap(patch.Details)
		}
		if patch.IsError != nil {
			current.IsError = *patch.IsError
		}
	}
	return current, nil
}

func (s *Session) formatSkillsForPrompt() string {
	skills := s.Skills()
	if len(skills) == 0 {
		return ""
	}
	lines := []string{
		"The following skills provide specialized instructions for specific tasks.",
		"Use the read tool to load a skill's file when the task matches its description.",
		"When a skill file references a relative path, resolve it against the skill directory and use that absolute path in tool commands.",
		"",
		"<available_skills>",
	}
	visible := 0
	for _, skill := range skills {
		if skill.Disabled {
			continue
		}
		visible++
		lines = append(lines, formatSkillForPrompt(skill))
	}
	if visible == 0 {
		return ""
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

func formatSkillForPrompt(skill SlashCommandInfo) string {
	location := skill.FilePath
	if location == "" {
		if raw, ok := skill.SourceInfo["path"].(string); ok {
			location = raw
		}
	}
	return strings.Join([]string{
		"  <skill>",
		"    <name>" + escapeXML(strings.TrimPrefix(skill.Name, "skill:")) + "</name>",
		"    <description>" + escapeXML(skill.Description) + "</description>",
		"    <location>" + escapeXML(location) + "</location>",
		"  </skill>",
	}, "\n")
}

func escapeXML(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	value = strings.ReplaceAll(value, "'", "&apos;")
	return value
}

func (s *Session) Prompt(ctx context.Context, prompt string) error {
	return s.promptWithSource(ctx, prompt, nil, false, "extension")
}

func (s *Session) PromptWithAttachments(ctx context.Context, prompt string, attachments []PromptAttachment) error {
	return s.promptWithSource(ctx, prompt, attachments, false, "extension")
}

func (s *Session) PromptWithSource(ctx context.Context, prompt string, attachments []PromptAttachment, source string) error {
	return s.promptWithSource(ctx, prompt, attachments, false, source)
}

func (s *Session) SteerWithAttachments(ctx context.Context, message string, attachments []PromptAttachment) error {
	return s.promptWithSource(ctx, message, attachments, false, "extension")
}

func (s *Session) FollowUpWithAttachments(ctx context.Context, message string, attachments []PromptAttachment) error {
	return s.promptWithSource(ctx, message, attachments, false, "extension")
}

func waitForRetryDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func clonePromptAttachments(attachments []PromptAttachment) []PromptAttachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]PromptAttachment, len(attachments))
	copy(out, attachments)
	return out
}

func cloneMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func normalizeInputAction(action string) string {
	action = strings.TrimSpace(action)
	if action == "" {
		return "continue"
	}
	return action
}

func cloneAgentcoreMessage(message agentcore.Message) agentcore.Message {
	if message == nil {
		return nil
	}
	out := make(agentcore.Message, len(message))
	for key, value := range message {
		out[key] = value
	}
	return out
}

func (s *Session) applyPromptStreamEvent(state *promptStreamState, event agentcore.Event) {
	eventType := strings.TrimSpace(fmt.Sprintf("%v", event["type"]))
	switch eventType {
	case "message_start":
		message, ok := event["message"].(agentcore.Message)
		if !ok {
			break
		}
		s.Events = append(s.Events, event)
		state.activeMessage = cloneAgentcoreMessage(message)
		state.activeMessageIdx = len(state.messages)
		state.messages = append(state.messages, cloneAgentcoreMessage(message))
		return
	case "message_update":
		if state.activeMessageIdx < 0 || state.activeMessageIdx >= len(state.messages) {
			break
		}
		message, ok := event["message"].(agentcore.Message)
		if !ok {
			break
		}
		s.Events = append(s.Events, event)
		state.activeMessage = cloneAgentcoreMessage(message)
		state.messages[state.activeMessageIdx] = cloneAgentcoreMessage(message)
		return
	case "message_end":
		if state.activeMessageIdx >= 0 && state.activeMessageIdx < len(state.messages) {
			message, ok := event["message"].(agentcore.Message)
			if ok {
				state.messages[state.activeMessageIdx] = cloneAgentcoreMessage(message)
			} else {
				state.messages[state.activeMessageIdx] = state.activeMessage
			}
		}
		state.activeMessage = nil
		state.activeMessageIdx = -1
		s.Events = append(s.Events, event)
		return
	}

	if eventType == "agent_start" || eventType == "turn_start" || eventType == "turn_end" ||
		eventType == "tool_execution_start" || eventType == "tool_execution_update" || eventType == "tool_execution_end" {
		s.Events = append(s.Events, event)
		return
	}
	s.Events = append(s.Events, event)
}

func (s *Session) emitSessionEvent(eventType string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	event := agentcore.Event{"type": eventType}
	for key, value := range data {
		event[key] = value
	}
	s.Events = append(s.Events, event)
}

func (s *Session) EmitSessionStart(reason, previousSessionFile string) {
	data := map[string]any{"reason": strings.TrimSpace(reason)}
	if data["reason"] == "" {
		data["reason"] = "startup"
	}
	if previousSessionFile = strings.TrimSpace(previousSessionFile); previousSessionFile != "" {
		data["previousSessionFile"] = previousSessionFile
	}
	s.emitSessionEvent("session_start", data)
}

func (s *Session) EmitSessionShutdown(reason, targetSessionFile string) {
	data := map[string]any{"reason": strings.TrimSpace(reason)}
	if data["reason"] == "" {
		data["reason"] = "quit"
	}
	if targetSessionFile = strings.TrimSpace(targetSessionFile); targetSessionFile != "" {
		data["targetSessionFile"] = targetSessionFile
	}
	s.emitSessionEvent("session_shutdown", data)
}

func (s *Session) emitBeforeSwitch(ctx context.Context, reason, targetSessionFile string) (bool, error) {
	reason = strings.TrimSpace(reason)
	event := SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: reason, TargetSessionFile: strings.TrimSpace(targetSessionFile)}
	data := map[string]any{"reason": reason}
	if event.TargetSessionFile != "" {
		data["targetSessionFile"] = event.TargetSessionFile
	}
	s.emitSessionEvent("session_before_switch", data)

	s.mu.Lock()
	hooks := append([]SessionBeforeSwitchHandler(nil), s.beforeSwitchHooks...)
	s.mu.Unlock()
	for _, hook := range hooks {
		result, err := hook(ctx, event)
		if err != nil {
			return false, err
		}
		if result.Cancel {
			return true, nil
		}
	}
	return false, nil
}

func (s *Session) emitBeforeFork(ctx context.Context, entryID, position string) (bool, error) {
	position = strings.TrimSpace(position)
	if position == "" {
		position = "before"
	}
	event := SessionBeforeForkEvent{Type: "session_before_fork", EntryID: strings.TrimSpace(entryID), Position: position}
	s.emitSessionEvent("session_before_fork", map[string]any{"entryId": event.EntryID, "position": event.Position})

	s.mu.Lock()
	hooks := append([]SessionBeforeForkHandler(nil), s.beforeForkHooks...)
	s.mu.Unlock()
	for _, hook := range hooks {
		result, err := hook(ctx, event)
		if err != nil {
			return false, err
		}
		if result.Cancel {
			return true, nil
		}
	}
	return false, nil
}

func (s *Session) emitBeforeTree(ctx context.Context, targetID string) (bool, error) {
	event := SessionBeforeTreeEvent{Type: "session_before_tree", TargetID: strings.TrimSpace(targetID), OldLeafID: s.leafID}
	data := map[string]any{"targetId": event.TargetID}
	if event.OldLeafID != "" {
		data["oldLeafId"] = event.OldLeafID
	}
	s.emitSessionEvent("session_before_tree", data)

	s.mu.Lock()
	hooks := append([]SessionBeforeTreeHandler(nil), s.beforeTreeHooks...)
	s.mu.Unlock()
	for _, hook := range hooks {
		result, err := hook(ctx, event)
		if err != nil {
			return false, err
		}
		if result.Cancel {
			return true, nil
		}
	}
	return false, nil
}

func (s *Session) emitBeforeCompact(ctx context.Context, tokensBefore int, instructions string) (bool, error) {
	event := SessionBeforeCompactEvent{
		Type:               "session_before_compact",
		TokensBefore:       tokensBefore,
		CustomInstructions: strings.TrimSpace(instructions),
	}
	data := map[string]any{
		"tokensBefore":       tokensBefore,
		"customInstructions": event.CustomInstructions,
	}
	s.emitSessionEvent("session_before_compact", data)

	s.mu.Lock()
	hooks := append([]SessionBeforeCompactHandler(nil), s.beforeCompactHooks...)
	s.mu.Unlock()
	for _, hook := range hooks {
		result, err := hook(ctx, event)
		if err != nil {
			return false, err
		}
		if result.Cancel {
			return true, nil
		}
	}
	return false, nil
}

func (s *Session) applyInputHooks(ctx context.Context, text string, attachments []PromptAttachment, source string) (string, []PromptAttachment, bool, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "extension"
	}
	currentText := text
	currentAttachments := clonePromptAttachments(attachments)
	s.mu.Lock()
	hooks := append([]InputHandler(nil), s.inputHooks...)
	s.mu.Unlock()
	for _, hook := range hooks {
		event := InputEvent{
			Type:        "input",
			Text:        currentText,
			Attachments: clonePromptAttachments(currentAttachments),
			Source:      source,
		}
		result, err := hook(ctx, event)
		if err != nil {
			return "", nil, false, err
		}
		switch normalizeInputAction(result.Action) {
		case "continue":
			continue
		case "handled":
			return currentText, currentAttachments, true, nil
		case "transform":
			currentText = result.Text
			if result.Attachments != nil {
				currentAttachments = clonePromptAttachments(result.Attachments)
			}
		default:
			return "", nil, false, fmt.Errorf("unsupported input action: %s", result.Action)
		}
	}
	return currentText, currentAttachments, false, nil
}

func (s *Session) applyBeforeAgentStartHooks(ctx context.Context, prompt string, attachments []PromptAttachment, systemPrompt string) (string, error) {
	currentSystemPrompt := systemPrompt
	s.mu.Lock()
	hooks := append([]BeforeAgentStartHandler(nil), s.beforeAgentStartHooks...)
	s.mu.Unlock()
	for _, hook := range hooks {
		result, err := hook(ctx, BeforeAgentStartEvent{
			Type:         "before_agent_start",
			Prompt:       prompt,
			Attachments:  clonePromptAttachments(attachments),
			SystemPrompt: currentSystemPrompt,
		})
		if err != nil {
			return "", err
		}
		if result.Message != nil {
			message := cloneAgentcoreMessage(result.Message)
			if _, ok := message["role"].(string); !ok {
				message["role"] = "custom"
			}
			if _, ok := message["timestamp"]; !ok {
				message["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
			}
			if err := s.appendEntry(SessionEntry{Type: "message", Message: message}); err != nil {
				return "", err
			}
		}
		if result.SystemPrompt != nil {
			currentSystemPrompt = *result.SystemPrompt
		}
	}
	return currentSystemPrompt, nil
}

func (s *Session) prompt(ctx context.Context, prompt string, attachments []PromptAttachment, retrying bool) error {
	return s.promptWithSource(ctx, prompt, attachments, retrying, "extension")
}

func (s *Session) promptWithSource(ctx context.Context, prompt string, attachments []PromptAttachment, retrying bool, source string) error {
	streamState := promptStreamState{activeMessageIdx: -1}
	if s.IsStreaming {
		return fmt.Errorf("session is already streaming")
	}
	if !retrying {
		updatedPrompt, updatedAttachments, handled, inputErr := s.applyInputHooks(ctx, prompt, attachments, source)
		if inputErr != nil {
			return inputErr
		}
		if handled {
			return nil
		}
		prompt = updatedPrompt
		attachments = updatedAttachments
	}
	expanded, handled, expandErr := s.expandExtensionCommand(ctx, prompt)
	if expandErr != nil {
		return expandErr
	}
	if handled {
		return nil
	}
	prompt = expanded
	prompt = s.expandPromptTemplate(prompt)
	systemPrompt := s.HeadlessSystemPrompt()
	if !retrying {
		startPrompt, startErr := s.applyBeforeAgentStartHooks(ctx, prompt, attachments, systemPrompt)
		if startErr != nil {
			return startErr
		}
		systemPrompt = startPrompt
		s.LastPrompt = prompt
		s.lastAttachments = clonePromptAttachments(attachments)
	}

	s.mu.Lock()
	opCtx, cancel := context.WithCancel(ctx)
	s.promptCancel = cancel
	s.mu.Unlock()

	s.IsStreaming = true
	defer func() {
		s.mu.Lock()
		s.promptCancel = nil
		if s.retryCancel != nil {
			s.retryCancel = nil
		}
		s.mu.Unlock()
		s.IsStreaming = false
	}()

	var loop agentcore.LoopResult
	var err error
	usedScriptedTurns := s.turnIndex < len(s.Turns)
	if usedScriptedTurns {
		prompt, err = s.promptTextWithAttachments(prompt, attachments)
		if err != nil {
			cancel()
			return err
		}
		loop, err = agentcore.RunScriptedLoop(opCtx, agentcore.ScriptedLoopInput{
			Prompts: []string{prompt},
			Tools:   s.builtinTools(),
			Turns:   s.consumePromptTurns(),
		})
	} else {
		if s.Provider == "" || s.ModelID == "" {
			cancel()
			s.mu.Lock()
			s.promptCancel = nil
			s.mu.Unlock()
			return fmt.Errorf("no scripted turns and no model configured")
		}
		chatOptions := ai.ChatOptions{}
		if effort := strings.TrimSpace(s.ThinkingLevel); effort != "" && effort != "off" {
			chatOptions.ReasoningEffort = effort
			if model, ok := ai.GetModel(s.Provider, s.ModelID); ok && !ai.SupportsXhigh(model) {
				chatOptions.ReasoningEffort = ai.ClampReasoning(effort)
			}
		}
		chatOptions.OnPayload = func(payload any, req ai.CompletionRequest) (any, error) {
			return s.applyProviderPayloadHooks(opCtx, payload, req)
		}
		chatOptions.OnResponse = func(response ai.ProviderResponse, req ai.CompletionRequest) error {
			return s.applyProviderResponseHooks(opCtx, response, req)
		}
		prompts, promptErr := s.promptMessages(prompt, attachments)
		if promptErr != nil {
			cancel()
			return promptErr
		}
		loop, err = agentcore.RunProviderLoop(opCtx, agentcore.ProviderLoopInput{
			PromptMessages: prompts,
			Tools:          s.builtinTools(),
			History:        s.providerHistoryWithSystem(systemPrompt),
			Provider:       s.Provider,
			Model:          s.ModelID,
			ToolSpecs:      s.toolSpecs(),
			Options:        chatOptions,
			GetAPIKey: func(provider string) string {
				return s.resolveProviderAPIKey(opCtx, provider)
			},
			TransformContextFunc: s.transformContextWithHooks,
			BeforeToolCall:       s.applyToolCallHooks,
			AfterToolCall:        s.applyToolResultHooks,
			EventSink: func(event agentcore.Event) {
				s.applyPromptStreamEvent(&streamState, event)
			},
		})
	}
	if err != nil {
		shouldRetry := s.AutoRetry && !retrying && opCtx.Err() == nil
		cancel()
		if shouldRetry {
			s.mu.Lock()
			if s.retryCancel != nil {
				s.retryCancel()
			}
			retryCtx, retryCancel := context.WithCancel(ctx)
			s.retryCancel = retryCancel
			s.mu.Unlock()

			err = waitForRetryDelay(retryCtx, autoRetryDelay)
			s.mu.Lock()
			s.IsStreaming = false
			s.mu.Unlock()
			s.mu.Lock()
			if s.retryCancel != nil {
				s.retryCancel = nil
			}
			s.mu.Unlock()
			if err != nil {
				return err
			}
			return s.promptWithSource(ctx, prompt, s.lastAttachments, true, source)
		}
		return err
	}
	cancel()
	if err := opCtx.Err(); err != nil && err != context.Canceled {
		return err
	}

	if usedScriptedTurns {
		s.Events = append(s.Events, loop.Events...)
		for _, message := range loop.Messages {
			if err := s.appendEntry(SessionEntry{Type: "message", Message: message}); err != nil {
				return err
			}
		}
		return s.triggerAutoCompaction(context.Background())
	}

	for _, message := range loop.Messages {
		if message == nil || len(message) == 0 {
			continue
		}
		if _, ok := message["role"]; !ok {
			continue
		}
		if err := s.appendEntry(SessionEntry{Type: "message", Message: message}); err != nil {
			return err
		}
	}

	streamState.activeMessageIdx = -1
	streamState.activeMessage = nil

	return s.triggerAutoCompaction(context.Background())
}

func (s *Session) promptMessages(prompt string, attachments []PromptAttachment) ([]ai.Message, error) {
	if len(attachments) == 0 {
		return []ai.Message{{Role: "user", Content: prompt}}, nil
	}
	content := []any{map[string]any{"type": "text", "text": prompt}}
	for _, attachment := range attachments {
		block, err := s.attachmentContentBlock(attachment)
		if err != nil {
			return nil, err
		}
		content = append(content, block)
	}
	return []ai.Message{{Role: "user", Content: content}}, nil
}

func (s *Session) promptTextWithAttachments(prompt string, attachments []PromptAttachment) (string, error) {
	if len(attachments) == 0 {
		return prompt, nil
	}
	var builder strings.Builder
	builder.WriteString(prompt)
	builder.WriteString("\n")
	builder.WriteString("The following attachments were provided:\n")
	for _, attachment := range attachments {
		kind := strings.TrimSpace(strings.ToLower(strings.TrimSpace(attachment.Type)))
		switch kind {
		case "", "text", "file":
			text := attachment.Text
			if text == "" && attachment.Path != "" {
				path, err := ResolveWorkspacePath(s.Root, attachment.Path)
				if err != nil {
					return "", err
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return "", err
				}
				if isLikelyBinary(data) {
					return "", fmt.Errorf("%s appears to be a binary file and was not attached", attachment.Path)
				}
				text = string(data)
			}
			builder.WriteString("- ")
			if attachment.Path != "" {
				builder.WriteString("file ")
				builder.WriteString(attachment.Path)
			} else {
				builder.WriteString("text block")
			}
			builder.WriteString(":\n")
			if text != "" {
				text, truncated := truncateToolOutput(text, s.ToolOutputLimit)
				builder.WriteString(text)
				if truncated {
					builder.WriteString(" (truncated)")
				}
				builder.WriteString("\n")
			}
		case "image":
			label := "image attachment"
			if attachment.Path != "" {
				label += " " + attachment.Path
			}
			if attachment.MimeType != "" {
				label += fmt.Sprintf(" (%s)", attachment.MimeType)
			}
			if attachment.Data != "" {
				label += fmt.Sprintf(" [base64 bytes: %d]", len(attachment.Data))
			}
			builder.WriteString("- ")
			builder.WriteString(label)
			builder.WriteString("\n")
		default:
			return "", fmt.Errorf("unsupported attachment type: %s", attachment.Type)
		}
	}
	return builder.String(), nil
}

func (s *Session) builtinTools() []agentcore.Tool {
	tools := BuiltinToolsWithOptions(s.Root, BuiltinToolOptions{
		OutputLimit:        s.ToolOutputLimit,
		ShellCommandPrefix: s.ShellCommandPrefix,
	})
	extensionTools, _ := s.ExtensionTools()
	return append(tools, extensionTools...)
}

func (s *Session) toolSpecs() []ai.Tool {
	specs := BuiltinToolSpecs()
	_, extensionSpecs := s.ExtensionTools()
	return append(specs, extensionSpecs...)
}

func (s *Session) attachmentContentBlock(attachment PromptAttachment) (any, error) {
	kind := strings.TrimSpace(attachment.Type)
	if kind == "" {
		kind = "text"
	}
	switch kind {
	case "text", "file":
		text := attachment.Text
		if text == "" && attachment.Path != "" {
			path, err := ResolveWorkspacePath(s.Root, attachment.Path)
			if err != nil {
				return nil, err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			if isLikelyBinary(data) {
				return nil, fmt.Errorf("%s appears to be a binary file and cannot be attached as text", attachment.Path)
			}
			text = string(data)
		}
		if attachment.Path != "" {
			text = fmt.Sprintf("Attached file %s:\n%s", attachment.Path, text)
		}
		return map[string]any{"type": "text", "text": text}, nil
	case "image":
		data := attachment.Data
		if data == "" && attachment.Path != "" {
			path, err := ResolveWorkspacePath(s.Root, attachment.Path)
			if err != nil {
				return nil, err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			data = base64.StdEncoding.EncodeToString(raw)
		}
		if strings.TrimSpace(data) == "" {
			return nil, fmt.Errorf("image attachment requires data or path")
		}
		mimeType := attachment.MimeType
		if mimeType == "" {
			mimeType = "image/png"
		}
		return map[string]any{"type": "image", "data": data, "mimeType": mimeType}, nil
	default:
		return nil, fmt.Errorf("unsupported attachment type: %s", kind)
	}
}

func (s *Session) providerHistory() []ai.Message {
	return s.providerHistoryWithSystem(s.HeadlessSystemPrompt())
}

func (s *Session) providerHistoryWithSystem(systemPrompt string) []ai.Message {
	history := sessionMessagesToAI(s.Messages)
	if system := strings.TrimSpace(systemPrompt); system != "" {
		history = append([]ai.Message{{Role: "system", Content: system}}, history...)
	}
	return history
}

func (s *Session) HeadlessSystemPrompt() string {
	lines := []string{
		"You are a headless coding agent operating in a local workspace.",
		"Use the available tools to inspect and modify files. Keep responses concise and technical.",
		"Workspace: " + s.Root,
	}
	if branch := currentGitBranch(s.Root); branch != "" {
		lines = append(lines, "Git branch: "+branch)
	}
	if status := shortGitStatus(s.Root); status != "" {
		lines = append(lines, "Git status:\n"+status)
	}
	if packages := packageContext(s.Root); packages != "" {
		lines = append(lines, "Project context:\n"+packages)
	}
	if contextFiles := LoadProjectContextFiles(s.Root, DefaultAgentDir()); len(contextFiles) > 0 {
		var builder strings.Builder
		builder.WriteString("Project-specific instructions and guidelines:")
		for _, file := range contextFiles {
			builder.WriteString("\n\n## ")
			builder.WriteString(file.Path)
			builder.WriteString("\n\n")
			builder.WriteString(file.Content)
		}
		lines = append(lines, "Project context files:\n"+builder.String())
	}
	return strings.Join(lines, "\n")
}

func LoadProjectContextFiles(root, agentDir string) []ProjectContextFile {
	root = filepath.Clean(root)
	agentDir = filepath.Clean(agentDir)
	contextFiles := []ProjectContextFile{}
	seen := map[string]struct{}{}

	if file, ok := loadContextFileFromDir(agentDir); ok {
		contextFiles = append(contextFiles, file)
		seen[file.Path] = struct{}{}
	}

	var ancestors []ProjectContextFile
	current := root
	for {
		if file, ok := loadContextFileFromDir(current); ok {
			if _, exists := seen[file.Path]; !exists {
				ancestors = append([]ProjectContextFile{file}, ancestors...)
				seen[file.Path] = struct{}{}
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	contextFiles = append(contextFiles, ancestors...)
	return contextFiles
}

func loadContextFileFromDir(dir string) (ProjectContextFile, bool) {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(dir, name)
		content, err := os.ReadFile(path)
		if err == nil {
			return ProjectContextFile{Path: path, Content: string(content)}, true
		}
	}
	return ProjectContextFile{}, false
}

func (s *Session) Steer(ctx context.Context, message string) error {
	return s.SteerWithAttachments(ctx, message, nil)
}

func (s *Session) FollowUp(ctx context.Context, message string) error {
	return s.FollowUpWithAttachments(ctx, message, nil)
}

func (s *Session) RetryLast(ctx context.Context) error {
	if strings.TrimSpace(s.LastPrompt) == "" {
		return fmt.Errorf("no prompt to retry")
	}
	return s.prompt(ctx, s.LastPrompt, clonePromptAttachments(s.lastAttachments), true)
}

func (s *Session) State() State {
	return State{
		IsStreaming:       s.IsStreaming,
		MessageCount:      len(s.Messages),
		SessionID:         "pigo-session",
		SessionName:       s.Name,
		ThinkingLevel:     s.ThinkingLevel,
		Provider:          s.Provider,
		ModelID:           s.ModelID,
		SteeringMode:      s.SteeringMode,
		FollowUpMode:      s.FollowUpMode,
		AutoCompaction:    s.AutoCompaction,
		IsCompacting:      s.IsCompacting,
		PendingMessageCnt: 0,
		ContextUsage:      s.ContextUsage(),
	}
}

func (s *Session) Abort() {
	s.AbortRetry()
	s.mu.Lock()
	if s.promptCancel != nil {
		s.promptCancel()
	}
	if s.bashCancel != nil {
		s.bashCancel()
	}
	if s.compactionCancel != nil {
		s.compactionCancel()
	}
	s.mu.Unlock()
}

func (s *Session) NewSession() {
	s.NewSessionWithParent("")
}

func (s *Session) NewSessionWithParent(parentSession string) {
	cancelled, err := s.TryNewSessionWithParent(context.Background(), parentSession)
	if cancelled || err != nil {
		return
	}
}

func (s *Session) TryNewSessionWithParent(ctx context.Context, parentSession string) (bool, error) {
	cancelled, err := s.emitBeforeSwitch(ctx, "new", "")
	if cancelled || err != nil {
		return cancelled, err
	}
	previousSessionFile := ""
	if s.Store != nil {
		previousSessionFile = s.Store.Path
	}
	s.turnIndex = 0
	s.Events = nil
	s.Messages = nil
	s.AvailableModels = nil
	s.OAuthCredentials = map[string]ai.OAuthCredentials{}
	s.entries = nil
	s.entriesByID = map[string]SessionEntry{}
	s.leafID = ""
	s.entrySequence = 0
	s.labelsByID = map[string]string{}
	s.labelTimes = map[string]string{}
	s.customEntries = nil
	s.SessionEntryTypes = []string{"model_change", "thinking_level_change"}
	s.Name = ""
	s.ThinkingLevel = "off"
	s.Provider = ""
	s.ModelID = ""
	s.SteeringMode = "one-at-a-time"
	s.FollowUpMode = "one-at-a-time"
	s.ShellCommandPrefix = ""
	s.ToolOutputLimit = 0
	s.IsStreaming = false
	s.parentSession = strings.TrimSpace(parentSession)
	s.seedDefaultModels()

	if s.Store != nil {
		headerPath := s.nextSessionPathOrCurrent()
		if headerPath == "" {
			headerPath = s.Store.Path
		} else {
			s.Store = NewSessionStore(headerPath)
		}
		if headerPath != "" {
			_ = writeSessionEntries(headerPath, []SessionEntry{{
				Type:          "session",
				ID:            s.newEntryID(),
				ParentSession: s.parentSession,
				Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
			}})
		}
	}
	s.EmitSessionStart("new", previousSessionFile)
	return false, nil
}

func (s *Session) nextSessionPathOrCurrent() string {
	if s.Store == nil || s.Store.Path == "" {
		return ""
	}
	target, err := s.nextSessionPath("session")
	if err != nil {
		return ""
	}
	return target
}

func (s *Session) seedDefaultModels() {
	current := ModelInfo{Provider: s.Provider, ModelID: s.ModelID}
	s.AvailableModels = nil

	for _, model := range ai.GetAllModelsWithOAuth(s.OAuthCredentials) {
		s.AvailableModels = appendModelIfMissing(s.AvailableModels, ModelInfo{
			Provider: model.Provider,
			ModelID:  model.ID,
		})
	}

	if current.Provider != "" && current.ModelID != "" {
		s.AvailableModels = appendModelIfMissing(s.AvailableModels, current)
	}

	sort.SliceStable(s.AvailableModels, func(i, j int) bool {
		if s.AvailableModels[i].Provider == s.AvailableModels[j].Provider {
			return s.AvailableModels[i].ModelID < s.AvailableModels[j].ModelID
		}
		return s.AvailableModels[i].Provider < s.AvailableModels[j].Provider
	})
}

func (s *Session) SetSteeringMode(mode string) error {
	if mode != "all" && mode != "one-at-a-time" {
		return fmt.Errorf("invalid steering mode: %s", mode)
	}
	s.SteeringMode = mode
	return nil
}

func (s *Session) SetFollowUpMode(mode string) error {
	if mode != "all" && mode != "one-at-a-time" {
		return fmt.Errorf("invalid follow-up mode: %s", mode)
	}
	s.FollowUpMode = mode
	return nil
}

func (s *Session) SetThinkingLevel(level string) error {
	level = strings.TrimSpace(level)
	if level == "" {
		return fmt.Errorf("missing thinking level")
	}
	s.ThinkingLevel = level
	s.SessionEntryTypes = append(s.SessionEntryTypes, "thinking_level_change")
	return s.appendEntry(SessionEntry{
		Type:  "thinking_level_change",
		Level: level,
	})
}

func (s *Session) CycleThinkingLevel() (string, bool) {
	levels := []string{"off", "low", "medium", "high", "xhigh"}
	for i, level := range levels {
		if level == s.ThinkingLevel {
			next := levels[(i+1)%len(levels)]
			if err := s.SetThinkingLevel(next); err != nil {
				return levels[0], false
			}
			return next, true
		}
	}
	if err := s.SetThinkingLevel(levels[0]); err != nil {
		return levels[0], false
	}
	return levels[0], true
}

func (s *Session) SetModel(provider, modelID string) (ModelInfo, error) {
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	if provider == "" {
		return ModelInfo{}, fmt.Errorf("missing provider")
	}
	if modelID == "" {
		return ModelInfo{}, fmt.Errorf("missing modelId")
	}
	s.Provider = provider
	s.ModelID = modelID
	s.AvailableModels = appendModelIfMissing(s.AvailableModels, ModelInfo{Provider: provider, ModelID: modelID})
	s.SessionEntryTypes = append(s.SessionEntryTypes, "model_change")
	entry := SessionEntry{
		Type:     "model_change",
		Provider: provider,
		ModelID:  modelID,
		Level:    s.ThinkingLevel,
	}
	if err := s.appendEntry(entry); err != nil {
		return ModelInfo{}, err
	}
	return ModelInfo{Provider: provider, ModelID: modelID}, nil
}

func (s *Session) GetAvailableModels() []ModelInfo {
	return append([]ModelInfo(nil), s.AvailableModels...)
}

func (s *Session) SetOAuthCredentials(provider string, credentials ai.OAuthCredentials) error {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return fmt.Errorf("missing provider")
	}
	if strings.TrimSpace(credentials.Access) == "" && strings.TrimSpace(credentials.Refresh) == "" {
		return fmt.Errorf("missing oauth credentials")
	}
	if s.OAuthCredentials == nil {
		s.OAuthCredentials = map[string]ai.OAuthCredentials{}
	}
	s.OAuthCredentials[provider] = credentials
	s.seedDefaultModels()
	return s.appendEntry(SessionEntry{
		Type:             "oauth_login",
		OAuthProvider:    provider,
		OAuthCredentials: &credentials,
	})
}

func (s *Session) LoadOAuthStore(path string) error {
	store, err := ai.LoadOAuthStore(path)
	if err != nil {
		return err
	}
	if s.OAuthCredentials == nil {
		s.OAuthCredentials = map[string]ai.OAuthCredentials{}
	}
	for provider, credentials := range store.Providers {
		s.OAuthCredentials[provider] = credentials
	}
	s.seedDefaultModels()
	return nil
}

func (s *Session) GetProviderAuthStatus() []map[string]any {
	infos := ai.ProviderAuthInfos()
	result := make([]map[string]any, 0, len(infos))
	for _, info := range infos {
		_, hasStored := s.OAuthCredentials[info.Provider]
		result = append(result, map[string]any{
			"provider":          info.Provider,
			"methods":           info.Methods,
			"envKeys":           info.EnvKeys,
			"baseURL":           info.BaseURL,
			"configured":        info.Configured,
			"hasStoredOAuth":    hasStored,
			"usesOAuthRegistry": ai.GetOAuthProvider(info.Provider) != nil,
		})
	}
	return result
}

func (s *Session) CycleModel() (ModelInfo, bool) {
	current := ModelInfo{Provider: s.Provider, ModelID: s.ModelID}
	if len(s.AvailableModels) <= 1 {
		return ModelInfo{}, false
	}
	for i, model := range s.AvailableModels {
		if model == current {
			next := s.AvailableModels[(i+1)%len(s.AvailableModels)]
			modelInfo, err := s.SetModel(next.Provider, next.ModelID)
			if err != nil {
				return ModelInfo{}, false
			}
			return modelInfo, true
		}
	}
	next := s.AvailableModels[0]
	modelInfo, err := s.SetModel(next.Provider, next.ModelID)
	if err != nil {
		return ModelInfo{}, false
	}
	return modelInfo, true
}

func (s *Session) Compact(customInstructions string) CompactionResult {
	return s.compact(context.Background(), customInstructions, false)
}

func (s *Session) CompactWithModel(ctx context.Context, customInstructions string) CompactionResult {
	return s.compact(ctx, customInstructions, true)
}

func (s *Session) compact(ctx context.Context, customInstructions string, useModel bool) CompactionResult {
	ctx, cancel := context.WithCancel(ctx)
	s.IsCompacting = true
	s.mu.Lock()
	s.compactionCancel = cancel
	s.mu.Unlock()
	instructions := strings.TrimSpace(customInstructions)
	defer func() {
		cancel()
		s.mu.Lock()
		s.compactionCancel = nil
		s.mu.Unlock()
		s.IsCompacting = false
	}()

	tokensBefore := s.Stats().Tokens.Total
	if tokensBefore == 0 {
		tokensBefore = s.ContextUsage().EstimatedTokens
	}
	cancelled, err := s.emitBeforeCompact(ctx, tokensBefore, instructions)
	if cancelled || err != nil {
		return CompactionResult{TokensBefore: tokensBefore, Cancelled: true}
	}
	summary := s.buildCompactionSummary(instructions)
	if useModel && s.Compactor != nil {
		if generated, err := s.Compactor(ctx, sessionMessagesToAI(s.Messages), instructions); err == nil && strings.TrimSpace(generated) != "" {
			summary = strings.TrimSpace(generated)
		}
	} else if useModel {
		if generated, err := s.compactWithConfiguredModel(ctx, instructions); err == nil && strings.TrimSpace(generated) != "" {
			summary = strings.TrimSpace(generated)
		}
	}
	if ctx.Err() != nil {
		return CompactionResult{TokensBefore: tokensBefore, Cancelled: true}
	}
	kept := ""
	branch := s.resolveBranchEntries(s.leafID)
	if len(branch) > 0 {
		kept = branch[0].ID
	}
	_ = s.appendEntry(SessionEntry{
		Type:             "compaction",
		Summary:          summary,
		FirstKeptEntryID: kept,
		TokensBefore:     tokensBefore,
	})
	_ = s.appendEntry(SessionEntry{
		Type: "message",
		Message: map[string]any{
			"role":           "compactionSummary",
			"summary":        summary,
			"tokensBefore":   tokensBefore,
			"fromCompaction": true,
		},
	})
	result := CompactionResult{
		Summary:        summary,
		FirstKeptEntry: kept,
		TokensBefore:   tokensBefore,
		Cancelled:      false,
	}
	s.emitSessionEvent("session_compact", map[string]any{
		"summary":          summary,
		"firstKeptEntryId": kept,
		"tokensBefore":     tokensBefore,
		"fromExtension":    false,
	})
	return result
}

func (s *Session) compactWithConfiguredModel(ctx context.Context, instructions string) (string, error) {
	if strings.TrimSpace(s.Provider) == "" || strings.TrimSpace(s.ModelID) == "" {
		return "", fmt.Errorf("no model configured")
	}
	userText := "Summarize the conversation so a headless coding agent can continue from compacted history."
	if instructions != "" {
		userText += "\n\nAdditional instructions:\n" + instructions
	}
	result, _, err := ai.Complete(ctx, ai.CompletionRequest{
		Provider: s.Provider,
		Model:    s.ModelID,
		Messages: append([]ai.Message{
			{Role: "system", Content: s.HeadlessSystemPrompt()},
			{Role: "user", Content: userText},
		}, sessionMessagesToAI(s.Messages)...),
		Options: ai.ChatOptions{APIKey: s.resolveProviderAPIKey(ctx, s.Provider)},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Text), nil
}

func (s *Session) buildCompactionSummary(instructions string) string {
	stats := s.Stats()
	readFiles := map[string]struct{}{}
	modifiedFiles := map[string]struct{}{}
	for _, message := range s.Messages {
		role, _ := message["role"].(string)
		if role != "toolResult" {
			continue
		}
		details, _ := message["details"].(map[string]any)
		addStringList(readFiles, details["readFiles"])
		addStringList(modifiedFiles, details["modifiedFiles"])
	}
	lines := []string{
		"Session compacted.",
		fmt.Sprintf("Messages: %d user, %d assistant, %d tool result.", stats.UserMessages, stats.AssistantMessages, stats.ToolResults),
	}
	if instructions != "" {
		lines = append(lines, "Instructions: "+instructions)
	}
	if len(readFiles) > 0 {
		lines = append(lines, "Read files: "+strings.Join(sortedKeys(readFiles), ", "))
	}
	if len(modifiedFiles) > 0 {
		lines = append(lines, "Modified files: "+strings.Join(sortedKeys(modifiedFiles), ", "))
	}
	if last := s.GetLastAssistantText(); last != nil && strings.TrimSpace(*last) != "" {
		text, _ := truncateToolOutput(strings.TrimSpace(*last), 1000)
		lines = append(lines, "Last assistant response: "+text)
	}
	return strings.Join(lines, "\n")
}

func (s *Session) SetAutoCompactionEnabled(enabled bool) {
	s.AutoCompaction = enabled
}

func (s *Session) SetAutoRetryEnabled(enabled bool) {
	s.AutoRetry = enabled
}

func (s *Session) AbortRetry() {
	s.mu.Lock()
	if s.retryCancel != nil {
		s.retryCancel()
	}
	s.mu.Unlock()
}

func (s *Session) triggerAutoCompaction(ctx context.Context) error {
	if !s.AutoCompaction || s.IsCompacting || len(s.Messages) < autoCompactionMessageThreshold {
		return nil
	}
	_ = s.compact(ctx, "", false)
	return nil
}

func (s *Session) Bash(ctx context.Context, command string) (BashResult, error) {
	s.mu.Lock()
	cmdCtx, cancel := context.WithCancel(ctx)
	s.bashCancel = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.bashCancel = nil
		s.mu.Unlock()
	}()

	cmd := exec.CommandContext(cmdCtx, "bash", "-lc", command)
	cmd.Dir = s.Root
	output, err := cmd.CombinedOutput()
	cancel()
	exitCode := 0
	if err != nil {
		exitCode = 1
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		}
	}
	result := BashResult{
		Command:   command,
		Output:    string(output),
		ExitCode:  exitCode,
		Cancelled: cmdCtx.Err() != nil && exitCode != 0,
	}
	message := agentcore.Message{
		"role":     "bashExecution",
		"command":  command,
		"output":   result.Output,
		"exitCode": result.ExitCode,
	}
	if appendErr := s.appendEntry(SessionEntry{Type: "message", Message: message}); appendErr != nil && err == nil {
		err = appendErr
	}
	return result, err
}

func (s *Session) ExportToHTML(outputPath string) (string, error) {
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		outputPath = filepath.Join(s.Root, "session.html")
	} else if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(s.Root, outputPath)
	}
	payload := map[string]any{
		"sessionId": "pigo-session",
		"name":      s.Name,
		"messages":  s.Messages,
	}
	marshaled, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(outputPath, []byte("<!doctype html><html><body><pre>"+html.EscapeString(string(marshaled))+"</pre></body></html>"), 0o644); err != nil {
		return "", err
	}
	return outputPath, nil
}

func (s *Session) ExportToJSONL(outputPath string) (string, error) {
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		outputPath = filepath.Join(s.Root, "session.jsonl")
	} else if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(s.Root, outputPath)
	}

	header := SessionEntry{
		Type:          "session",
		Version:       3,
		ID:            s.newEntryID(),
		ParentSession: s.parentSession,
		CWD:           s.Root,
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	branch := s.resolveBranchEntries(s.leafID)
	linear := linearizeSessionEntries(branch)
	labels := s.labelEntriesForBranch(branch)
	exportEntries := append([]SessionEntry{header}, linear...)
	exportEntries = append(exportEntries, labels...)
	return outputPath, writeSessionEntries(outputPath, exportEntries)
}

func (s *Session) Share(outputPath string) (string, error) {
	path, err := s.ExportToJSONL(outputPath)
	if err != nil {
		return "", err
	}
	url := "file://" + filepath.ToSlash(path)
	return url, nil
}

func (s *Session) SwitchSession(sessionPath string) error {
	cancelled, err := s.SwitchSessionContext(context.Background(), sessionPath)
	if cancelled && err == nil {
		return ErrSessionOperationCancelled
	}
	return err
}

func (s *Session) SwitchSessionContext(ctx context.Context, sessionPath string) (bool, error) {
	return s.switchSession(ctx, sessionPath, "resume", true)
}

func (s *Session) switchSession(ctx context.Context, sessionPath, reason string, emitBefore bool) (bool, error) {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return false, fmt.Errorf("missing sessionPath")
	}
	if emitBefore {
		cancelled, err := s.emitBeforeSwitch(ctx, reason, sessionPath)
		if cancelled || err != nil {
			return cancelled, err
		}
	}
	previousSessionFile := ""
	if s.Store != nil {
		previousSessionFile = s.Store.Path
	}
	store := NewSessionStore(sessionPath)
	entries, err := store.ReadEntries()
	if err != nil {
		return false, err
	}
	s.EmitSessionShutdown(reason, sessionPath)
	shutdownEvents := s.RuntimeEvents()
	s.Store = store
	s.loadSession(entries)
	s.Events = append(shutdownEvents, s.Events...)
	s.EmitSessionStart(reason, previousSessionFile)
	return false, nil
}

func (s *Session) Fork(entryID string) (string, bool, error) {
	entryID = strings.TrimSpace(entryID)
	cancelled, err := s.emitBeforeFork(context.Background(), entryID, "before")
	if cancelled || err != nil {
		return "", cancelled, err
	}
	if entryID == "" {
		return "", false, fmt.Errorf("missing entryId")
	}
	selected := s.entriesByID[entryID]
	if selected.Type == "" {
		return "", false, fmt.Errorf("Invalid entry ID for forking")
	}
	role, _ := selected.Message["role"].(string)
	if selected.Type != "message" || role != "user" {
		return "", false, fmt.Errorf("Invalid entry ID for forking")
	}

	selectedText := extractUserMessageText(selected.Message)
	targetLeaf := selected.ParentID
	var path []SessionEntry
	if targetLeaf == "" {
		path = nil
	} else {
		if s.entriesByID[targetLeaf].ID == "" {
			return "", false, fmt.Errorf("Invalid entry ID for forking")
		}
		path = s.resolveBranchEntries(targetLeaf)
	}

	targetPath, err := s.nextSessionPath("fork")
	if err != nil {
		return "", false, err
	}
	header := SessionEntry{
		Type:      "session",
		ID:        s.newEntryID(),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if s.Store != nil {
		header.ParentSession = s.Store.Path
	}
	sessionEntries := append([]SessionEntry{header}, path...)
	if err := writeSessionEntries(targetPath, sessionEntries); err != nil {
		return "", false, err
	}
	if cancelled, err := s.switchSession(context.Background(), targetPath, "fork", false); cancelled || err != nil {
		return "", cancelled, err
	}
	return selectedText, false, nil
}

func (s *Session) Clone() (bool, error) {
	if s.leafID == "" {
		return false, fmt.Errorf("Cannot clone session: no current entry selected")
	}
	targetPath, err := s.nextSessionPath("clone")
	if err != nil {
		return false, err
	}
	path := s.resolveBranchEntries(s.leafID)
	header := SessionEntry{
		Type:      "session",
		ID:        s.newEntryID(),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if s.Store != nil {
		header.ParentSession = s.Store.Path
	}
	sessionEntries := append([]SessionEntry{header}, path...)
	if err := writeSessionEntries(targetPath, sessionEntries); err != nil {
		return false, err
	}
	if cancelled, err := s.switchSession(context.Background(), targetPath, "new", true); cancelled || err != nil {
		return cancelled, err
	}
	return false, nil
}

func (s *Session) Branch(entryID string) error {
	entryID = strings.TrimSpace(entryID)
	cancelled, err := s.emitBeforeTree(context.Background(), defaultString(entryID, "root"))
	if cancelled || err != nil {
		if cancelled && err == nil {
			return ErrSessionOperationCancelled
		}
		return err
	}
	beforeEvents := s.RuntimeEvents()
	oldLeafID := s.leafID
	if entryID == "" || strings.EqualFold(entryID, "root") {
		s.leafID = ""
		s.rebuildStateFromLeaf("")
		s.Events = append(beforeEvents, s.Events...)
		s.emitSessionEvent("session_tree", map[string]any{"newLeafId": "", "oldLeafId": oldLeafID})
		return nil
	}
	if s.entriesByID == nil {
		s.entriesByID = map[string]SessionEntry{}
	}
	entry, ok := s.entriesByID[entryID]
	if !ok || entry.ID == "" {
		return fmt.Errorf("Invalid entry ID for branching")
	}
	s.leafID = entry.ID
	s.rebuildStateFromLeaf(s.leafID)
	s.Events = append(beforeEvents, s.Events...)
	s.emitSessionEvent("session_tree", map[string]any{"newLeafId": s.leafID, "oldLeafId": oldLeafID})
	return nil
}

func (s *Session) BranchWithSummary(entryID string, summary string) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return s.Branch(entryID)
	}
	fromID := s.leafID
	if err := s.Branch(entryID); err != nil {
		return err
	}
	parentID := s.leafID
	if strings.TrimSpace(entryID) == "" || strings.EqualFold(entryID, "root") {
		parentID = ""
	}
	return s.appendEntry(SessionEntry{
		Type:     "branch_summary",
		ParentID: parentID,
		Summary:  summary,
		FromID:   defaultString(fromID, "root"),
	})
}

func (s *Session) SetLabel(entryID string, label string) error {
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return fmt.Errorf("missing entryId")
	}
	if s.entriesByID == nil || s.entriesByID[entryID].ID == "" {
		return fmt.Errorf("Entry %s not found", entryID)
	}
	return s.appendMetadataEntry(SessionEntry{
		Type:     "label",
		TargetID: entryID,
		Label:    strings.TrimSpace(label),
	})
}

func (s *Session) GetLabel(entryID string) string {
	if s.labelsByID == nil {
		return ""
	}
	return s.labelsByID[entryID]
}

func (s *Session) Tree() []SessionTreeNode {
	nodeMap := map[string]*SessionTreeNode{}
	roots := []*SessionTreeNode{}

	for _, entry := range s.entries {
		if entry.Type == "session" || entry.Type == "label" {
			continue
		}
		node := &SessionTreeNode{
			ID:        entry.ID,
			Type:      entry.Type,
			ParentID:  entry.ParentID,
			Timestamp: entry.Timestamp,
			Children:  []SessionTreeNode{},
		}
		if label := s.GetLabel(entry.ID); label != "" {
			node.Label = label
			node.LabelTimestamp = s.labelTimes[entry.ID]
		}
		if entry.Type == "message" {
			role, _ := entry.Message["role"].(string)
			node.Role = role
			node.Text = messageTextFromSessionEntry(entry)
		} else if entry.Type == "branch_summary" {
			node.Role = "branchSummary"
			node.Text = entry.Summary
		}
		nodeMap[entry.ID] = node
	}

	for _, entry := range s.entries {
		if entry.Type == "session" || entry.Type == "label" {
			continue
		}
		node := nodeMap[entry.ID]
		if node == nil {
			continue
		}
		if entry.ParentID == "" || entry.ParentID == entry.ID {
			roots = append(roots, node)
			continue
		}
		parent := nodeMap[entry.ParentID]
		if parent == nil {
			roots = append(roots, node)
			continue
		}
		parent.Children = append(parent.Children, *node)
		node = nil
	}

	sortSessionTreeNodes(roots)
	return derefSessionTreeNodes(roots)
}

func (s *Session) GetForkMessages() []ForkMessage {
	messages := make([]ForkMessage, 0)
	for _, entry := range s.entries {
		if entry.Type != "message" {
			continue
		}
		role, _ := entry.Message["role"].(string)
		if role != "user" {
			continue
		}
		text := extractUserMessageText(entry.Message)
		if text == "" {
			continue
		}
		messages = append(messages, ForkMessage{
			EntryID: entry.ID,
			Text:    text,
		})
	}
	return messages
}

func (s *Session) GetLastAssistantText() *string {
	for i := len(s.Messages) - 1; i >= 0; i-- {
		message := s.Messages[i]
		role, _ := message["role"].(string)
		if role != "assistant" {
			continue
		}
		text, ok := message["text"].(string)
		if !ok {
			continue
		}
		return &text
	}
	return nil
}

func (s *Session) SetSessionName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	s.Name = name
	s.SessionEntryTypes = append(s.SessionEntryTypes, "session_name")
	return s.appendEntry(SessionEntry{
		Type: "session_name",
		Name: name,
	})
}

func (s *Session) Stats() Stats {
	stats := Stats{
		SessionID:     "pigo-session",
		TotalMessages: len(s.Messages),
		Tokens: struct {
			Input      int `json:"input"`
			Output     int `json:"output"`
			CacheRead  int `json:"cacheRead"`
			CacheWrite int `json:"cacheWrite"`
			Total      int `json:"total"`
		}{
			Input:      0,
			Output:     0,
			CacheRead:  0,
			CacheWrite: 0,
			Total:      0,
		},
		Cost: 0.0,
	}
	if s.Store != nil {
		stats.SessionFile = s.Store.Path
	}
	for _, message := range s.Messages {
		role, _ := message["role"].(string)
		switch role {
		case "user":
			stats.UserMessages++
		case "assistant":
			stats.AssistantMessages++
			if content, ok := message["content"].([]any); ok {
				for _, block := range content {
					object, ok := block.(map[string]any)
					if ok && object["type"] == "toolCall" {
						stats.ToolCalls++
					}
				}
			}
			if usage, ok := message["usage"].(map[string]any); ok {
				stats.Tokens.Input += asInt(usage["input"])
				stats.Tokens.Output += asInt(usage["output"])
				stats.Tokens.CacheRead += asInt(usage["cacheRead"])
				stats.Tokens.CacheWrite += asInt(usage["cacheWrite"])
				stats.Tokens.Total += asInt(usage["totalTokens"])
			}
		case "toolResult":
			stats.ToolResults++
		}
	}
	stats.ContextUsage = s.ContextUsage()
	return stats
}

func (s *Session) ContextUsage() ContextUsage {
	total := 0
	for _, message := range s.Messages {
		total += estimateTokensFromValue(message)
	}
	usage := ContextUsage{
		EstimatedTokens: total,
		MessageCount:    len(s.Messages),
	}
	if s.Provider != "" && s.ModelID != "" {
		if model, ok := ai.GetModel(s.Provider, s.ModelID); ok && model.ContextWindow > 0 {
			usage.Limit = model.ContextWindow
			usage.Percent = float64(total) / float64(model.ContextWindow)
		}
	}
	return usage
}

func estimateTokensFromValue(value any) int {
	text := textFromValue(value)
	if text == "" {
		return 0
	}
	estimate := len([]rune(text)) / 4
	if estimate < 1 {
		estimate = 1
	}
	return estimate
}

func textFromValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case agentcore.Message:
		return textFromValue(map[string]any(typed))
	case map[string]any:
		var parts []string
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, textFromValue(typed[key]))
		}
		return strings.Join(parts, " ")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, textFromValue(item))
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func (s *Session) consumePromptTurns() []AssistantTurn {
	if s.turnIndex >= len(s.Turns) {
		return []AssistantTurn{{StopReason: "stop"}}
	}
	start := s.turnIndex
	for s.turnIndex < len(s.Turns) {
		turn := s.Turns[s.turnIndex]
		s.turnIndex++
		if turn.StopReason != "toolUse" {
			break
		}
	}
	return append([]AssistantTurn(nil), s.Turns[start:s.turnIndex]...)
}

func (s *Session) appendEntry(entry SessionEntry) error {
	entry = s.prepareEntry(entry)
	s.entries = append(s.entries, entry)
	s.entriesByID[entry.ID] = entry
	s.leafID = entry.ID

	s.applyEntry(entry)
	if err := s.Store.Append(entry); err != nil {
		return err
	}
	return nil
}

func (s *Session) appendMetadataEntry(entry SessionEntry) error {
	leafID := s.leafID
	entry = s.prepareEntry(entry)
	entry.ParentID = ""
	s.entries = append(s.entries, entry)
	s.entriesByID[entry.ID] = entry
	s.leafID = leafID

	s.applyEntry(entry)
	if err := s.Store.Append(entry); err != nil {
		return err
	}
	return nil
}

func (s *Session) applyEntry(entry SessionEntry) {
	s.SessionEntryTypes = append(s.SessionEntryTypes, entry.Type)
	switch entry.Type {
	case "message":
		s.Messages = append(s.Messages, entry.Message)
	case "branch_summary":
		if entry.Summary != "" {
			s.Messages = append(s.Messages, agentcore.Message{
				"role":    "branchSummary",
				"summary": entry.Summary,
				"fromId":  entry.FromID,
			})
		}
	case "model_change":
		s.Provider = entry.Provider
		s.ModelID = entry.ModelID
		if entry.Provider != "" && entry.ModelID != "" {
			s.AvailableModels = appendModelIfMissing(s.AvailableModels, ModelInfo{
				Provider: entry.Provider,
				ModelID:  entry.ModelID,
			})
		}
	case "thinking_level_change":
		s.ThinkingLevel = entry.Level
	case "session_name":
		s.Name = entry.Name
	case "label":
		if entry.TargetID != "" {
			if s.labelsByID == nil {
				s.labelsByID = map[string]string{}
			}
			if s.labelTimes == nil {
				s.labelTimes = map[string]string{}
			}
			if strings.TrimSpace(entry.Label) == "" {
				delete(s.labelsByID, entry.TargetID)
				delete(s.labelTimes, entry.TargetID)
			} else {
				s.labelsByID[entry.TargetID] = entry.Label
				s.labelTimes[entry.TargetID] = entry.Timestamp
			}
		}
	case "custom":
		s.customEntries = append(s.customEntries, entry)
	case "oauth_login":
		if entry.OAuthProvider != "" && entry.OAuthCredentials != nil {
			if s.OAuthCredentials == nil {
				s.OAuthCredentials = map[string]ai.OAuthCredentials{}
			}
			s.OAuthCredentials[entry.OAuthProvider] = *entry.OAuthCredentials
		}
	}
}

func (s *Session) loadSession(entries []SessionEntry) {
	s.turnIndex = 0
	s.Events = nil
	s.Messages = nil
	s.AvailableModels = nil
	s.OAuthCredentials = map[string]ai.OAuthCredentials{}
	s.entries = nil
	s.entriesByID = map[string]SessionEntry{}
	s.leafID = ""
	s.entrySequence = 0
	s.labelsByID = map[string]string{}
	s.labelTimes = map[string]string{}
	s.customEntries = nil
	s.SessionEntryTypes = []string{"model_change", "thinking_level_change"}
	s.Name = ""
	s.ThinkingLevel = "off"
	s.Provider = ""
	s.ModelID = ""
	s.parentSession = ""

	if len(entries) == 0 {
		s.entries = make([]SessionEntry, 0)
		return
	}

	for i, entry := range entries {
		if strings.TrimSpace(entry.ID) == "" || s.entriesByID[entry.ID].ID != "" {
			entry.ID = s.newEntryID()
		}
		if entry.Timestamp == "" {
			entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
		}
		if entry.Type == "session" && s.parentSession == "" {
			s.parentSession = entry.ParentSession
		}
		entries[i] = entry
		s.entries = append(s.entries, entry)
		s.entriesByID[entry.ID] = entry
	}

	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i].Type == "session" || s.entries[i].Type == "label" {
			continue
		}
		s.leafID = s.entries[i].ID
		break
	}
	s.rebuildStateFromLeaf(s.leafID)
}

func (s *Session) rebuildStateFromLeaf(leafID string) {
	s.Events = nil
	s.Messages = nil
	s.AvailableModels = nil
	s.OAuthCredentials = map[string]ai.OAuthCredentials{}
	s.SessionEntryTypes = []string{"model_change", "thinking_level_change"}
	s.labelsByID = map[string]string{}
	s.labelTimes = map[string]string{}
	s.customEntries = nil
	s.Name = ""
	s.ThinkingLevel = "off"
	s.Provider = ""
	s.ModelID = ""

	branch := s.resolveBranchEntries(leafID)
	for _, entry := range branch {
		s.applyEntry(entry)
	}
	branchIDs := map[string]struct{}{}
	for _, entry := range branch {
		branchIDs[entry.ID] = struct{}{}
	}
	for _, entry := range s.entries {
		if entry.Type != "label" {
			continue
		}
		if _, ok := branchIDs[entry.TargetID]; ok {
			s.applyEntry(entry)
		}
	}
	s.seedDefaultModels()
}

func (s *Session) prepareEntry(entry SessionEntry) SessionEntry {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	entry.ID = strings.TrimSpace(entry.ID)
	if entry.ID == "" {
		entry.ID = s.newEntryID()
	}
	if s.leafID != "" && entry.ParentID == "" {
		entry.ParentID = s.leafID
	}
	return entry
}

func (s *Session) resolveBranchEntries(leafID string) []SessionEntry {
	if len(s.entries) == 0 {
		return nil
	}
	if leafID == "" {
		return nil
	}
	if s.entriesByID == nil {
		return nil
	}
	if _, ok := s.entriesByID[leafID]; !ok {
		return nil
	}

	visited := map[string]bool{}
	path := make([]SessionEntry, 0, len(s.entries))
	for currentID := leafID; currentID != ""; {
		if visited[currentID] {
			break
		}
		visited[currentID] = true
		entry, ok := s.entriesByID[currentID]
		if !ok {
			break
		}
		path = append(path, entry)
		if entry.ParentID == "" {
			break
		}
		currentID = entry.ParentID
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func (s *Session) nextSessionPath(prefix string) (string, error) {
	baseDir := os.TempDir()
	if s.Store != nil && s.Store.Path != "" {
		baseDir = filepath.Dir(s.Store.Path)
	} else if s.Root != "" {
		baseDir = s.Root
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(baseDir, fmt.Sprintf("%s-%s.jsonl", prefix, s.newEntryID())), nil
}

func (s *Session) newEntryID() string {
	if s.entriesByID == nil {
		s.entriesByID = map[string]SessionEntry{}
	}
	for {
		var raw [8]byte
		if _, err := rand.Read(raw[:]); err == nil {
			id := hex.EncodeToString(raw[:])
			if _, exists := s.entriesByID[id]; !exists {
				return id
			}
			continue
		}
		s.entrySequence++
		return fmt.Sprintf("%d-%d", time.Now().UnixNano(), s.entrySequence)
	}
}

func writeSessionEntries(path string, entries []SessionEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	for _, entry := range entries {
		if entry.Timestamp == "" {
			entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
		}
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func linearizeSessionEntries(entries []SessionEntry) []SessionEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]SessionEntry, 0, len(entries))
	previousID := ""
	for _, entry := range entries {
		if entry.Type == "session" {
			continue
		}
		entry.ParentID = previousID
		out = append(out, entry)
		previousID = entry.ID
	}
	return out
}

func (s *Session) labelEntriesForBranch(branch []SessionEntry) []SessionEntry {
	if len(branch) == 0 || len(s.labelsByID) == 0 {
		return nil
	}
	ids := map[string]struct{}{}
	for _, entry := range branch {
		if entry.ID != "" {
			ids[entry.ID] = struct{}{}
		}
	}
	labels := make([]SessionEntry, 0, len(s.labelsByID))
	for targetID, label := range s.labelsByID {
		if _, ok := ids[targetID]; !ok {
			continue
		}
		labels = append(labels, SessionEntry{
			Type:      "label",
			ID:        s.newEntryID(),
			TargetID:  targetID,
			Label:     label,
			Timestamp: defaultString(s.labelTimes[targetID], time.Now().UTC().Format(time.RFC3339Nano)),
		})
	}
	sort.Slice(labels, func(i, j int) bool {
		return labels[i].TargetID < labels[j].TargetID
	})
	return labels
}

func appendModelIfMissing(models []ModelInfo, model ModelInfo) []ModelInfo {
	found := false
	for _, current := range models {
		if current == model {
			found = true
			break
		}
	}
	if found {
		return models
	}
	return append(models, model)
}

func (s *Session) resolveProviderAPIKey(ctx context.Context, provider string) string {
	s.mu.Lock()
	credentials := cloneOAuthCredentialsMap(s.OAuthCredentials)
	s.mu.Unlock()

	refreshed, apiKey, ok, err := ai.GetOAuthAPIKey(ctx, provider, credentials)
	if err != nil || !ok || strings.TrimSpace(apiKey) == "" {
		return ""
	}

	s.mu.Lock()
	if s.OAuthCredentials == nil {
		s.OAuthCredentials = map[string]ai.OAuthCredentials{}
	}
	current, exists := s.OAuthCredentials[provider]
	changed := !exists || !oauthCredentialsEqual(current, refreshed)
	s.OAuthCredentials[provider] = refreshed
	s.mu.Unlock()

	if changed {
		_ = s.appendEntry(SessionEntry{
			Type:             "oauth_login",
			OAuthProvider:    provider,
			OAuthCredentials: &refreshed,
		})
	}
	return apiKey
}

func cloneOAuthCredentialsMap(in map[string]ai.OAuthCredentials) map[string]ai.OAuthCredentials {
	out := make(map[string]ai.OAuthCredentials, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func oauthCredentialsEqual(a, b ai.OAuthCredentials) bool {
	if a.Refresh != b.Refresh || a.Access != b.Access || a.Expires != b.Expires || a.ProjectID != b.ProjectID {
		return false
	}
	if len(a.Metadata) != len(b.Metadata) {
		return false
	}
	for key, value := range a.Metadata {
		if b.Metadata[key] != value {
			return false
		}
	}
	return true
}

func extractUserMessageText(message agentcore.Message) string {
	if message == nil {
		return ""
	}
	if text, ok := message["text"].(string); ok && text != "" {
		return text
	}
	content, ok := message["content"].([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(content))
	for _, block := range content {
		asMap, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if asMap["type"] != "text" {
			continue
		}
		text, ok := asMap["text"].(string)
		if !ok || text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "")
}

func sessionMessagesToAI(messages []agentcore.Message) []ai.Message {
	out := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		role, _ := message["role"].(string)
		switch role {
		case "user":
			text := messageTextFromSession(message)
			if text == "" {
				continue
			}
			out = append(out, ai.Message{Role: "user", Content: text})
		case "assistant":
			content := message["content"]
			if content == nil {
				content = messageTextFromSession(message)
			}
			if content == nil {
				continue
			}
			out = append(out, ai.Message{Role: "assistant", Content: content})
		case "toolResult":
			callID, _ := message["toolCallId"].(string)
			content, hasContent := message["content"]
			if !hasContent || content == nil || content == "" {
				content = messageTextFromSession(message)
			}
			if content == nil || content == "" {
				continue
			}
			out = append(out, ai.Message{Role: "toolResult", ToolCallID: callID, Content: content})
		case "custom", "bashExecution", "branchSummary", "compactionSummary":
			content, ok := sessionMessageToContent(role, message)
			if !ok || content == nil || content == "" {
				continue
			}
			out = append(out, ai.Message{Role: "user", Content: content})
		}
	}
	return out
}

func sessionMessageToContent(role string, message agentcore.Message) (any, bool) {
	switch role {
	case "custom":
		content, ok := message["content"]
		if !ok || content == nil {
			return nil, false
		}
		if text, ok := content.(string); ok {
			if text == "" {
				return nil, false
			}
			return []ai.ContentBlock{{Type: "text", Text: text}}, true
		}
		if blocks, ok := content.([]ai.ContentBlock); ok {
			return blocks, true
		}
		return content, true
	case "bashExecution":
		command, _ := message["command"].(string)
		output, _ := message["output"].(string)
		text := fmt.Sprintf(bashExecutionTextPrefix, command)
		if output != "" {
			text += fmt.Sprintf("```\n%s\n```", output)
		} else {
			text += "(no output)"
		}
		if exitCode, ok := message["exitCode"].(int); ok && exitCode != 0 {
			text += fmt.Sprintf("\n\nCommand exited with code %d", exitCode)
		}
		return []ai.ContentBlock{{Type: "text", Text: text}}, true
	case "branchSummary":
		summary, _ := message["summary"].(string)
		fromID, _ := message["fromId"].(string)
		if summary == "" {
			return nil, false
		}
		text := branchSummaryPrefix + summary
		if fromID != "" {
			text += "\nfrom: " + fromID
		}
		text += branchSummarySuffix
		return []ai.ContentBlock{{Type: "text", Text: text}}, true
	case "compactionSummary":
		summary, _ := message["summary"].(string)
		if summary == "" {
			return nil, false
		}
		text := compactionSummaryPrefix + summary + compactionSummarySuffix
		return []ai.ContentBlock{{Type: "text", Text: text}}, true
	default:
		return nil, false
	}
}

func messageTextFromSessionEntry(entry SessionEntry) string {
	if entry.ID == "" {
		return ""
	}
	return messageTextFromSession(entry.Message)
}

func messageTextFromSession(message agentcore.Message) string {
	if message == nil {
		return ""
	}
	if text, ok := message["text"].(string); ok && text != "" {
		return text
	}
	content, ok := message["content"].([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(content))
	for _, block := range content {
		asMap, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if asMap["type"] != "text" {
			continue
		}
		text, ok := asMap["text"].(string)
		if !ok || text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "")
}

func sortSessionTreeNodes(nodes []*SessionTreeNode) {
	if len(nodes) == 0 {
		return
	}
	sort.Slice(nodes, func(i, j int) bool {
		iTime := parseTreeNodeTime(nodes[i].Timestamp)
		jTime := parseTreeNodeTime(nodes[j].Timestamp)
		return iTime.Before(jTime)
	})
	for _, node := range nodes {
		children := make([]*SessionTreeNode, 0, len(node.Children))
		for i := range node.Children {
			children = append(children, &node.Children[i])
		}
		sortSessionTreeNodes(children)
	}
}

func derefSessionTreeNodes(nodes []*SessionTreeNode) []SessionTreeNode {
	out := make([]SessionTreeNode, len(nodes))
	for i, node := range nodes {
		if node == nil {
			continue
		}
		out[i] = *node
		out[i].Children = derefSessionTreeNodes(childrenToPointers(out[i].Children))
	}
	return out
}

func childrenToPointers(nodes []SessionTreeNode) []*SessionTreeNode {
	if len(nodes) == 0 {
		return nil
	}
	children := make([]*SessionTreeNode, len(nodes))
	for i := range nodes {
		children[i] = &nodes[i]
	}
	return children
}

func parseTreeNodeTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func asInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}
