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
	mu                sync.Mutex
	promptCancel      context.CancelFunc
	bashCancel        context.CancelFunc

	entries       []SessionEntry
	entriesByID   map[string]SessionEntry
	leafID        string
	entrySequence int
}

type ModelInfo struct {
	Provider string `json:"provider"`
	ModelID  string `json:"id"`
}

type ForkMessage struct {
	EntryID string `json:"entryId"`
	Text    string `json:"text"`
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

func (s *Session) Prompt(ctx context.Context, prompt string) error {
	if s.IsStreaming {
		return fmt.Errorf("session is already streaming")
	}

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
		loop, err = agentcore.RunProviderLoop(opCtx, agentcore.ProviderLoopInput{
			Prompts:   []string{prompt},
			Tools:     BuiltinTools(s.Root),
			History:   sessionMessagesToAI(s.Messages),
			Provider:  s.Provider,
			Model:     s.ModelID,
			ToolSpecs: BuiltinToolSpecs(),
			Options:   ai.ChatOptions{},
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
	s.turnIndex = 0
	s.Events = nil
	s.Messages = nil
	s.AvailableModels = nil
	s.entries = nil
	s.entriesByID = map[string]SessionEntry{}
	s.leafID = ""
	s.entrySequence = 0
	s.SessionEntryTypes = []string{"model_change", "thinking_level_change"}
	s.Name = ""
	s.ThinkingLevel = "off"
	s.Provider = ""
	s.ModelID = ""
	s.SteeringMode = "one-at-a-time"
	s.FollowUpMode = "one-at-a-time"
	s.IsStreaming = false
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
	if outputPath == "" {
		outputPath = filepath.Join(s.Root, "session.html")
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
	if err := os.WriteFile(outputPath, []byte("<!doctype html><html><body><pre>"+html.EscapeString(string(marshaled))+"</pre></body></html>"), 0o644); err != nil {
		return "", err
	}
	return outputPath, nil
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
	if err := writeSessionEntries(targetPath, path); err != nil {
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
	if err := writeSessionEntries(targetPath, path); err != nil {
		return false, err
	}
	if err := s.SwitchSession(targetPath); err != nil {
		return false, err
	}
	return false, nil
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

func (s *Session) applyEntry(entry SessionEntry) {
	s.SessionEntryTypes = append(s.SessionEntryTypes, entry.Type)
	switch entry.Type {
	case "message":
		s.Messages = append(s.Messages, entry.Message)
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
	}
}

func (s *Session) loadSession(entries []SessionEntry) {
	s.turnIndex = 0
	s.Events = nil
	s.Messages = nil
	s.AvailableModels = nil
	s.entries = nil
	s.entriesByID = map[string]SessionEntry{}
	s.leafID = ""
	s.entrySequence = 0
	s.SessionEntryTypes = []string{"model_change", "thinking_level_change"}
	s.Name = ""
	s.ThinkingLevel = "off"
	s.Provider = ""
	s.ModelID = ""

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
		entries[i] = entry
		s.entries = append(s.entries, entry)
		s.entriesByID[entry.ID] = entry
		s.leafID = entry.ID
	}
	for _, entry := range s.resolveBranchEntries(s.leafID) {
		s.applyEntry(entry)
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
			content := messageTextFromSession(message)
			if content == "" {
				continue
			}
			out = append(out, ai.Message{Role: "toolResult", ToolCallID: callID, Content: content})
		}
	}
	return out
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
