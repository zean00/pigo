package codingagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

type Session struct {
	Root              string
	Name              string
	ThinkingLevel     string
	Provider          string
	ModelID           string
	Store             *SessionStore
	SteeringMode      string
	FollowUpMode      string
	AutoCompaction    bool
	AutoRetry         bool
	IsCompacting      bool
	Turns             []AssistantTurn
	turnIndex         int
	Events            []agentcore.Event
	Messages          []agentcore.Message
	SessionEntryTypes []string
	AvailableModels   []ModelInfo
	IsStreaming       bool
	OAuthCredentials  map[string]ai.OAuthCredentials
	mu                sync.Mutex
	promptCancel      context.CancelFunc
	bashCancel        context.CancelFunc

	entries       []SessionEntry
	entriesByID   map[string]SessionEntry
	leafID        string
	entrySequence int
	parentSession string
	labelsByID    map[string]string
	labelTimes    map[string]string
	customEntries []SessionEntry

	extensionCommands []SlashCommandInfo
	promptTemplates   []SlashCommandInfo
	skills            []SlashCommandInfo
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

const (
	bashExecutionTextPrefix = "Ran `%s`\n"
	branchSummaryPrefix     = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
	branchSummarySuffix     = "\n</summary>\n"
	compactionSummaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	compactionSummarySuffix = "\n</summary>\n"
)

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
	if s.IsStreaming {
		return fmt.Errorf("session is already streaming")
	}
	prompt = s.expandPromptTemplate(prompt)

	s.mu.Lock()
	opCtx, cancel := context.WithCancel(ctx)
	s.promptCancel = cancel
	s.mu.Unlock()

	s.IsStreaming = true
	defer func() {
		s.mu.Lock()
		s.promptCancel = nil
		s.mu.Unlock()
		s.IsStreaming = false
	}()

	var loop agentcore.LoopResult
	var err error
	if s.turnIndex < len(s.Turns) {
		loop, err = agentcore.RunScriptedLoop(opCtx, agentcore.ScriptedLoopInput{
			Prompts: []string{prompt},
			Tools:   BuiltinTools(s.Root),
			Turns:   s.consumePromptTurns(),
		})
	} else {
		if s.Provider == "" || s.ModelID == "" {
			return fmt.Errorf("no scripted turns and no model configured")
		}
		chatOptions := ai.ChatOptions{}
		if effort := strings.TrimSpace(s.ThinkingLevel); effort != "" && effort != "off" {
			chatOptions.ReasoningEffort = effort
			if model, ok := ai.GetModel(s.Provider, s.ModelID); ok && !ai.SupportsXhigh(model) {
				chatOptions.ReasoningEffort = ai.ClampReasoning(effort)
			}
		}
		loop, err = agentcore.RunProviderLoop(opCtx, agentcore.ProviderLoopInput{
			Prompts:   []string{prompt},
			Tools:     BuiltinTools(s.Root),
			History:   sessionMessagesToAI(s.Messages),
			Provider:  s.Provider,
			Model:     s.ModelID,
			ToolSpecs: BuiltinToolSpecs(),
			Options:   chatOptions,
			GetAPIKey: func(provider string) string {
				return s.resolveProviderAPIKey(opCtx, provider)
			},
			TransformContext: s.transformContextWithSkills,
		})
	}
	cancel()
	if err != nil {
		return err
	}
	if err := opCtx.Err(); err != nil && err != context.Canceled {
		return err
	}

	s.Events = append(s.Events, loop.Events...)
	for _, message := range loop.Messages {
		if err := s.appendEntry(SessionEntry{Type: "message", Message: message}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) Steer(ctx context.Context, message string) error {
	return s.Prompt(ctx, message)
}

func (s *Session) FollowUp(ctx context.Context, message string) error {
	return s.Prompt(ctx, message)
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
	}
}

func (s *Session) Abort() {
	s.mu.Lock()
	if s.promptCancel != nil {
		s.promptCancel()
	}
	if s.bashCancel != nil {
		s.bashCancel()
	}
	s.mu.Unlock()
}

func (s *Session) NewSession() {
	s.NewSessionWithParent("")
}

func (s *Session) NewSessionWithParent(parentSession string) {
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
	for _, model := range ai.DefaultModels() {
		s.AvailableModels = appendModelIfMissing(s.AvailableModels, ModelInfo{
			Provider: model.Provider,
			ModelID:  model.ModelID,
		})
	}
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
	s.IsCompacting = true
	instructions := strings.TrimSpace(customInstructions)
	summary := "Session compacted"
	if instructions != "" {
		summary = fmt.Sprintf("%s (%s)", summary, instructions)
	}
	defer func() {
		s.IsCompacting = false
	}()

	tokensBefore := s.Stats().Tokens.Total
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
	return CompactionResult{
		Summary:        summary,
		FirstKeptEntry: kept,
		TokensBefore:   tokensBefore,
		Cancelled:      false,
	}
}

func (s *Session) SetAutoCompactionEnabled(enabled bool) {
	s.AutoCompaction = enabled
}

func (s *Session) SetAutoRetryEnabled(enabled bool) {
	s.AutoRetry = enabled
}

func (s *Session) AbortRetry() {
	s.AutoRetry = false
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
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return fmt.Errorf("missing sessionPath")
	}
	store := NewSessionStore(sessionPath)
	entries, err := store.ReadEntries()
	if err != nil {
		return err
	}
	s.Store = store
	s.loadSession(entries)
	return nil
}

func (s *Session) Fork(entryID string) (string, bool, error) {
	entryID = strings.TrimSpace(entryID)
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
	if err := s.SwitchSession(targetPath); err != nil {
		return "", false, err
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
	if err := s.SwitchSession(targetPath); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Session) Branch(entryID string) error {
	entryID = strings.TrimSpace(entryID)
	if entryID == "" || strings.EqualFold(entryID, "root") {
		s.leafID = ""
		s.rebuildStateFromLeaf("")
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
	return stats
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
