package agentcore

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/badlogic/pigo/pkg/ai"
)

type QueueMode string

const (
	QueueModeAll        QueueMode = "all"
	QueueModeOneAtATime QueueMode = "one-at-a-time"
)

type AgentState struct {
	SystemPrompt     string
	Provider         string
	Model            string
	ToolSpecs        []ai.Tool
	Tools            []Tool
	Messages         []Message
	IsStreaming      bool
	StreamingMessage Message
	PendingToolCalls map[string]struct{}
	ErrorMessage     string
}

type AgentOptions struct {
	SystemPrompt     string
	Provider         string
	Model            string
	ToolSpecs        []ai.Tool
	Tools            []Tool
	Messages         []Message
	Options          ai.ChatOptions
	MaxRounds        int
	ToolExecution    ToolExecutionMode
	SteeringMode     QueueMode
	FollowUpMode     QueueMode
	GetAPIKey        func(provider string) string
	TransformContext func([]ai.Message) []ai.Message
	OnPayload        func(payload any, req ai.CompletionRequest) (any, error)
	OnResponse       func(response ai.ProviderResponse, req ai.CompletionRequest) error
	SessionID        string
	Transport        string
	MaxRetryDelay    int64
	ThinkingBudgets  ai.ThinkingBudgets
	BeforeToolCall   func(ctx context.Context, input BeforeToolCallContext) (BeforeToolCallResult, error)
	AfterToolCall    func(ctx context.Context, input AfterToolCallContext) (ToolResult, error)
}

type Agent struct {
	mu           sync.Mutex
	state        AgentState
	options      AgentOptions
	listeners    map[int]func(Event, context.Context)
	nextListener int
	steering     pendingMessageQueue
	followUp     pendingMessageQueue
	active       *activeRun
}

type activeRun struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

type pendingMessageQueue struct {
	mode     QueueMode
	messages []Message
}

func newPendingMessageQueue(mode QueueMode) pendingMessageQueue {
	if mode == "" {
		mode = QueueModeOneAtATime
	}
	return pendingMessageQueue{mode: mode}
}

func (queue *pendingMessageQueue) enqueue(message Message) {
	queue.messages = append(queue.messages, cloneMessage(message))
}

func (queue *pendingMessageQueue) drain() []Message {
	if len(queue.messages) == 0 {
		return nil
	}
	if queue.mode == QueueModeAll {
		out := cloneMessages(queue.messages)
		queue.messages = nil
		return out
	}
	first := cloneMessage(queue.messages[0])
	queue.messages = queue.messages[1:]
	return []Message{first}
}

func (queue *pendingMessageQueue) clear() {
	queue.messages = nil
}

func (queue *pendingMessageQueue) hasItems() bool {
	return len(queue.messages) > 0
}

func NewAgent(options AgentOptions) *Agent {
	chatOptions := options.Options
	if chatOptions.OnPayload == nil && options.OnPayload != nil {
		chatOptions.OnPayload = options.OnPayload
	}
	if chatOptions.OnResponse == nil && options.OnResponse != nil {
		chatOptions.OnResponse = options.OnResponse
	}
	if chatOptions.SessionID == "" && options.SessionID != "" {
		chatOptions.SessionID = options.SessionID
	}
	if chatOptions.Transport == "" && options.Transport != "" {
		chatOptions.Transport = options.Transport
	}
	if chatOptions.MaxRetryDelay == 0 && options.MaxRetryDelay > 0 {
		chatOptions.MaxRetryDelay = time.Duration(options.MaxRetryDelay) * time.Millisecond
	}
	if chatOptions.ThinkingBudgets == (ai.ThinkingBudgets{}) {
		chatOptions.ThinkingBudgets = options.ThinkingBudgets
	}
	options.Options = chatOptions

	state := AgentState{
		SystemPrompt:     options.SystemPrompt,
		Provider:         options.Provider,
		Model:            options.Model,
		ToolSpecs:        append([]ai.Tool(nil), options.ToolSpecs...),
		Tools:            append([]Tool(nil), options.Tools...),
		Messages:         cloneMessages(options.Messages),
		PendingToolCalls: map[string]struct{}{},
	}
	return &Agent{
		state:     state,
		options:   options,
		listeners: map[int]func(Event, context.Context){},
		steering:  newPendingMessageQueue(options.SteeringMode),
		followUp:  newPendingMessageQueue(options.FollowUpMode),
	}
}

func (agent *Agent) Subscribe(listener func(Event, context.Context)) func() {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	id := agent.nextListener
	agent.nextListener++
	agent.listeners[id] = listener
	return func() {
		agent.mu.Lock()
		defer agent.mu.Unlock()
		delete(agent.listeners, id)
	}
}

func (agent *Agent) State() AgentState {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return cloneState(agent.state)
}

func (agent *Agent) Steer(message Message) {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	agent.steering.enqueue(message)
}

func (agent *Agent) FollowUp(message Message) {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	agent.followUp.enqueue(message)
}

func (agent *Agent) ClearSteeringQueue() {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	agent.steering.clear()
}

func (agent *Agent) ClearFollowUpQueue() {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	agent.followUp.clear()
}

func (agent *Agent) ClearAllQueues() {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	agent.steering.clear()
	agent.followUp.clear()
}

func (agent *Agent) HasQueuedMessages() bool {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return agent.steering.hasItems() || agent.followUp.hasItems()
}

func (agent *Agent) Abort() {
	agent.mu.Lock()
	active := agent.active
	agent.mu.Unlock()
	if active != nil {
		active.cancel()
	}
}

func (agent *Agent) WaitForIdle() {
	agent.mu.Lock()
	active := agent.active
	agent.mu.Unlock()
	if active != nil {
		<-active.done
	}
}

func (agent *Agent) Reset() {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	agent.state.Messages = nil
	agent.state.IsStreaming = false
	agent.state.StreamingMessage = nil
	agent.state.PendingToolCalls = map[string]struct{}{}
	agent.state.ErrorMessage = ""
	agent.steering.clear()
	agent.followUp.clear()
}

func (agent *Agent) Prompt(ctx context.Context, prompt string) error {
	return agent.PromptMessages(ctx, []Message{UserMessage(prompt)})
}

func (agent *Agent) PromptMessages(ctx context.Context, messages []Message) error {
	if len(messages) == 0 {
		return nil
	}
	prompts, err := promptTexts(messages)
	if err != nil {
		return err
	}
	return agent.run(ctx, prompts, false)
}

func (agent *Agent) Continue(ctx context.Context) error {
	agent.mu.Lock()
	if agent.active != nil {
		agent.mu.Unlock()
		return fmt.Errorf("agent is already processing")
	}
	last, hasLast := lastMessage(agent.state.Messages)
	queuedSteering := agent.steering.hasItems()
	queuedFollowUp := agent.followUp.hasItems()
	agent.mu.Unlock()

	if !hasLast {
		return fmt.Errorf("no messages to continue from")
	}
	role, _ := last["role"].(string)
	if role == "assistant" {
		if queuedSteering {
			return agent.runQueuedMessages(ctx, true)
		}
		if queuedFollowUp {
			return agent.runQueuedMessages(ctx, false)
		}
		return fmt.Errorf("cannot continue from message role: assistant")
	}
	return agent.run(ctx, nil, true)
}

func (agent *Agent) runQueuedMessages(ctx context.Context, steering bool) error {
	var messages []Message
	agent.mu.Lock()
	if steering {
		messages = agent.steering.drain()
	} else {
		messages = agent.followUp.drain()
	}
	agent.mu.Unlock()
	return agent.PromptMessages(ctx, messages)
}

func (agent *Agent) run(ctx context.Context, prompts []string, continuation bool) error {
	runCtx, cancel := context.WithCancel(ctx)

	agent.mu.Lock()
	if agent.active != nil {
		agent.mu.Unlock()
		cancel()
		return fmt.Errorf("agent is already processing")
	}
	active := &activeRun{ctx: runCtx, cancel: cancel, done: make(chan struct{})}
	agent.active = active
	agent.state.IsStreaming = true
	agent.state.StreamingMessage = nil
	agent.state.ErrorMessage = ""
	agent.mu.Unlock()

	defer func() {
		agent.mu.Lock()
		agent.state.IsStreaming = false
		agent.state.StreamingMessage = nil
		agent.state.PendingToolCalls = map[string]struct{}{}
		agent.active = nil
		agent.mu.Unlock()
		close(active.done)
		cancel()
	}()

	input := ProviderLoopInput{
		Prompts:             prompts,
		Tools:               append([]Tool(nil), agent.options.Tools...),
		History:             agent.historyMessages(),
		Provider:            agent.options.Provider,
		Model:               agent.options.Model,
		ToolSpecs:           append([]ai.Tool(nil), agent.options.ToolSpecs...),
		Options:             agent.options.Options,
		MaxRounds:           agent.options.MaxRounds,
		ToolExecution:       agent.options.ToolExecution,
		GetAPIKey:           agent.options.GetAPIKey,
		TransformContext:    agent.options.TransformContext,
		BeforeToolCall:      agent.options.BeforeToolCall,
		AfterToolCall:       agent.options.AfterToolCall,
		GetSteeringMessages: agent.drainSteeringAsAI,
		GetFollowUpMessages: agent.drainFollowUpsAsAI,
		EventSink:           func(event Event) { agent.processEvent(event, runCtx) },
	}

	if continuation && len(input.Prompts) == 0 && len(input.History) == 0 {
		return fmt.Errorf("no messages to continue from")
	}

	_, err := RunProviderLoop(runCtx, input)
	if err != nil {
		agent.handleFailure(err, runCtx)
		return err
	}
	return nil
}

func (agent *Agent) historyMessages() []ai.Message {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	messages := sessionMessagesToAI(agent.state.Messages)
	if prompt := strings.TrimSpace(agent.state.SystemPrompt); prompt != "" {
		messages = append([]ai.Message{{Role: "system", Content: prompt}}, messages...)
	}
	return messages
}

func (agent *Agent) drainSteeringAsAI() []ai.Message {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return sessionMessagesToAI(agent.steering.drain())
}

func (agent *Agent) drainFollowUpsAsAI() []ai.Message {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return sessionMessagesToAI(agent.followUp.drain())
}

func (agent *Agent) processEvent(event Event, ctx context.Context) {
	agent.mu.Lock()
	switch eventType(event) {
	case "message_start", "message_update":
		if message, ok := event["message"].(Message); ok {
			agent.state.StreamingMessage = cloneMessage(message)
		}
	case "message_end":
		agent.state.StreamingMessage = nil
		if message, ok := event["message"].(Message); ok {
			agent.state.Messages = append(agent.state.Messages, cloneMessage(message))
		}
	case "tool_execution_start":
		id, _ := event["toolCallId"].(string)
		pending := cloneSet(agent.state.PendingToolCalls)
		pending[id] = struct{}{}
		agent.state.PendingToolCalls = pending
	case "tool_execution_end":
		id, _ := event["toolCallId"].(string)
		pending := cloneSet(agent.state.PendingToolCalls)
		delete(pending, id)
		agent.state.PendingToolCalls = pending
	case "turn_end":
		if message, ok := event["message"].(Message); ok {
			if value, ok := message["errorMessage"].(string); ok && value != "" {
				agent.state.ErrorMessage = value
			}
		}
	case "agent_end":
		agent.state.StreamingMessage = nil
	}
	listeners := make([]func(Event, context.Context), 0, len(agent.listeners))
	for _, listener := range agent.listeners {
		listeners = append(listeners, listener)
	}
	agent.mu.Unlock()

	for _, listener := range listeners {
		listener(cloneEvent(event), ctx)
	}
}

func (agent *Agent) handleFailure(err error, ctx context.Context) {
	failure := Message{
		"role":         "assistant",
		"text":         "",
		"content":      []any{map[string]any{"type": "text", "text": ""}},
		"stopReason":   failureStopReason(ctx),
		"errorMessage": err.Error(),
	}

	agent.mu.Lock()
	agent.state.Messages = append(agent.state.Messages, cloneMessage(failure))
	agent.state.ErrorMessage = err.Error()
	listeners := make([]func(Event, context.Context), 0, len(agent.listeners))
	for _, listener := range agent.listeners {
		listeners = append(listeners, listener)
	}
	agent.mu.Unlock()

	for _, listener := range listeners {
		listener(Event{"type": "message_start", "message": cloneMessage(failure)}, ctx)
		listener(Event{"type": "message_end", "message": cloneMessage(failure)}, ctx)
		listener(Event{"type": "agent_end", "messageCount": 1}, ctx)
	}
}

func promptTexts(messages []Message) ([]string, error) {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		role, _ := message["role"].(string)
		if role != "" && role != "user" {
			return nil, fmt.Errorf("prompt messages must use role user")
		}
		text := messageText(message)
		if text == "" {
			return nil, fmt.Errorf("prompt messages must contain text")
		}
		out = append(out, text)
	}
	return out, nil
}

func eventType(event Event) string {
	value, _ := event["type"].(string)
	return value
}

func lastMessage(messages []Message) (Message, bool) {
	if len(messages) == 0 {
		return nil, false
	}
	return messages[len(messages)-1], true
}

func cloneState(state AgentState) AgentState {
	return AgentState{
		SystemPrompt:     state.SystemPrompt,
		Provider:         state.Provider,
		Model:            state.Model,
		ToolSpecs:        append([]ai.Tool(nil), state.ToolSpecs...),
		Tools:            append([]Tool(nil), state.Tools...),
		Messages:         cloneMessages(state.Messages),
		IsStreaming:      state.IsStreaming,
		StreamingMessage: cloneMessage(state.StreamingMessage),
		PendingToolCalls: cloneSet(state.PendingToolCalls),
		ErrorMessage:     state.ErrorMessage,
	}
}

func cloneSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for key := range in {
		out[key] = struct{}{}
	}
	return out
}

func cloneMessages(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		out = append(out, cloneMessage(message))
	}
	return out
}

func cloneMessage(message Message) Message {
	if message == nil {
		return nil
	}
	out := Message{}
	for key, value := range message {
		out[key] = value
	}
	return out
}

func cloneEvent(event Event) Event {
	if event == nil {
		return nil
	}
	out := Event{}
	for key, value := range event {
		out[key] = value
	}
	return out
}

func sessionMessagesToAI(messages []Message) []ai.Message {
	out := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		role, _ := message["role"].(string)
		switch role {
		case "user":
			if text := messageText(message); text != "" {
				out = append(out, ai.Message{Role: "user", Content: text})
			}
		case "assistant":
			content := message["content"]
			if content == nil {
				content = messageText(message)
			}
			if content != nil {
				stopReason, _ := message["stopReason"].(string)
				out = append(out, ai.Message{Role: "assistant", Content: content, StopReason: stopReason})
			}
		case "toolResult":
			callID, _ := message["toolCallId"].(string)
			toolName, _ := message["toolName"].(string)
			text := messageText(message)
			if text != "" {
				isError, _ := message["isError"].(bool)
				out = append(out, ai.Message{Role: "toolResult", ToolCallID: callID, ToolName: toolName, Content: text, IsError: isError})
			}
		}
	}
	return out
}

func messageText(message Message) string {
	if text, ok := message["text"].(string); ok && text != "" {
		return text
	}
	content, ok := message["content"]
	if !ok {
		return ""
	}
	return ai.MessageText(ai.Message{Role: "user", Content: content})
}

func failureStopReason(ctx context.Context) string {
	if ctx.Err() != nil {
		return "aborted"
	}
	return "error"
}
