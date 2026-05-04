package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/badlogic/pigo/pkg/ai"
)

type Message map[string]any
type Event map[string]any

type ToolExecutionMode string

const (
	ToolExecutionSequential  ToolExecutionMode = "sequential"
	ToolExecutionParallel    ToolExecutionMode = "parallel"
	ToolExecutionInterleaved ToolExecutionMode = "interleaved"
)

type ToolResult struct {
	Text      string
	Details   map[string]any
	IsError   bool
	Terminate bool
	Content   []ai.ContentBlock
}

type Tool struct {
	Name              string
	ExecutionMode     ToolExecutionMode
	PrepareArguments  func(args map[string]any) (map[string]any, error)
	Execute           func(ctx context.Context, call ai.ContentBlock) ToolResult
	ExecuteWithUpdate func(ctx context.Context, call ai.ContentBlock, onUpdate func(ToolResult)) ToolResult
}

type BeforeToolCallResult struct {
	Block  bool
	Reason string
	Args   map[string]any
}

type BeforeToolCallContext struct {
	AssistantMessage Message
	ToolCall         ai.ContentBlock
	Args             map[string]any
	Context          []ai.Message
}

type AfterToolCallContext struct {
	AssistantMessage Message
	ToolCall         ai.ContentBlock
	Args             map[string]any
	Context          []ai.Message
	Result           ToolResult
	IsError          bool
}

type AssistantTurn struct {
	Content    []ai.ContentBlock
	StopReason string
}

type ScriptedLoopInput struct {
	Prompts []string
	Tools   []Tool
	Turns   []AssistantTurn
}

type ProviderLoopInput struct {
	Prompts              []string
	PromptMessages       []ai.Message
	Tools                []Tool
	History              []ai.Message
	Provider             string
	Model                string
	ToolSpecs            []ai.Tool
	Options              ai.ChatOptions
	MaxRounds            int
	ToolExecution        ToolExecutionMode
	GetAPIKey            func(provider string) string
	TransformContext     func([]ai.Message) []ai.Message
	TransformContextFunc func(context.Context, []ai.Message) ([]ai.Message, error)
	GetSteeringMessages  func() []ai.Message
	GetFollowUpMessages  func() []ai.Message
	StreamFn             func(context.Context, ai.CompletionRequest) *ai.EventStream
	BeforeToolCall       func(ctx context.Context, input BeforeToolCallContext) (BeforeToolCallResult, error)
	AfterToolCall        func(ctx context.Context, input AfterToolCallContext) (ToolResult, error)
	EventSink            func(Event)
}

type LoopResult struct {
	Events   []Event
	Messages []Message
}

type executedToolCall struct {
	call      ai.ContentBlock
	message   Message
	result    ToolResult
	executed  bool
	started   bool
	blocked   bool
	startErr  string
	sourceIdx int
	immediate bool
}

func emit(result *LoopResult, sink func(Event), event Event) {
	result.Events = append(result.Events, event)
	if sink != nil {
		sink(event)
	}
}

func RunScriptedLoop(ctx context.Context, input ScriptedLoopInput) (LoopResult, error) {
	result := LoopResult{
		Events: []Event{
			{"type": "agent_start"},
			{"type": "turn_start"},
		},
		Messages: []Message{},
	}

	for _, prompt := range input.Prompts {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		user := UserMessage(prompt)
		emit(&result, nil, Event{"type": "message_start", "message": user})
		emit(&result, nil, Event{"type": "message_end", "message": user})
		result.Messages = append(result.Messages, user)
	}

	for turnIndex, turn := range input.Turns {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if turnIndex > 0 {
			result.Events = append(result.Events, Event{"type": "turn_start"})
		}

		assistant := AssistantMessage(turn.Content, turn.StopReason)
		emit(&result, nil, Event{"type": "message_start", "message": assistant})
		emit(&result, nil, Event{"type": "message_update", "message": assistant, "assistantEventType": firstAssistantUpdate(turn.Content)})
		emit(&result, nil, Event{"type": "message_end", "message": assistant})
		result.Messages = append(result.Messages, assistant)

		toolResults := []Message{}
		for _, block := range turn.Content {
			if block.Type != "toolCall" {
				continue
			}
			toolResult := executeTool(ctx, input.Tools, block)
			toolMessage := ToolResultMessage(block.ID, block.Name, toolResult)
			emit(&result, nil, Event{"type": "tool_execution_start", "toolCallId": block.ID, "toolName": block.Name, "args": block.Arguments})
			if !toolResult.IsError {
				emit(&result, nil, Event{
					"type":          "tool_execution_update",
					"toolCallId":    block.ID,
					"toolName":      block.Name,
					"args":          block.Arguments,
					"partialResult": toEventToolResult(toolResult),
				})
			}
			emit(&result, nil, Event{
				"type":       "tool_execution_end",
				"toolCallId": block.ID,
				"toolName":   block.Name,
				"result":     toEventToolResult(toolResult),
				"isError":    toolResult.IsError,
				"text":       toolResult.Text,
			})
			emit(&result, nil, Event{"type": "message_start", "message": toolMessage})
			emit(&result, nil, Event{"type": "message_end", "message": toolMessage})
			result.Messages = append(result.Messages, toolMessage)
			toolResults = append(toolResults, toolMessage)
		}

		emit(&result, nil, Event{
			"type":            "turn_end",
			"message":         assistant,
			"toolResults":     cloneMessages(toolResults),
			"messageRole":     "assistant",
			"toolResultCount": len(toolResults),
		})
	}

	emit(&result, nil, Event{"type": "agent_end", "messageCount": len(result.Messages)})
	return result, nil
}

func RunProviderLoop(ctx context.Context, input ProviderLoopInput) (LoopResult, error) {
	maxRounds := input.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 6
	}

	result := LoopResult{
		Events:   []Event{{"type": "agent_start"}, {"type": "turn_start"}},
		Messages: []Message{},
	}
	if input.EventSink != nil {
		input.EventSink(result.Events[0])
		input.EventSink(result.Events[1])
	}

	conversation := append([]ai.Message(nil), input.History...)
	for _, prompt := range input.Prompts {
		prompt = strings.TrimSpace(prompt)
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if prompt == "" {
			continue
		}
		user := UserMessage(prompt)
		conversation = append(conversation, ai.Message{
			Role:    "user",
			Content: prompt,
		})
		emit(&result, input.EventSink, Event{"type": "message_start", "message": user})
		emit(&result, input.EventSink, Event{"type": "message_end", "message": user})
		result.Messages = append(result.Messages, user)
	}
	for _, prompt := range input.PromptMessages {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if prompt.Role == "" {
			prompt.Role = "user"
		}
		if prompt.Role != "user" {
			return result, fmt.Errorf("prompt messages must use role user")
		}
		conversation = append(conversation, prompt)
		user := aiMessageToEventMessage(prompt)
		emit(&result, input.EventSink, Event{"type": "message_start", "message": user})
		emit(&result, input.EventSink, Event{"type": "message_end", "message": user})
		result.Messages = append(result.Messages, user)
	}
	if len(conversation) == 0 {
		return result, fmt.Errorf("provider loop requires at least one prompt or history message")
	}
	if last := conversation[len(conversation)-1].Role; last == "assistant" {
		return result, fmt.Errorf("cannot continue provider loop from assistant message")
	}

	for round := 0; round < maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		requestMessages := append([]ai.Message(nil), conversation...)
		if input.TransformContextFunc != nil {
			var err error
			requestMessages, err = input.TransformContextFunc(ctx, requestMessages)
			if err != nil {
				return result, err
			}
		}
		if input.TransformContext != nil {
			requestMessages = input.TransformContext(requestMessages)
		}
		apiKey := input.Options.APIKey
		if input.GetAPIKey != nil {
			if resolved := strings.TrimSpace(input.GetAPIKey(input.Provider)); resolved != "" {
				apiKey = resolved
			}
		}
		executionMode := toolExecutionMode(input)
		options := ai.ChatOptions{
			Temperature:       input.Options.Temperature,
			MaxTokens:         input.Options.MaxTokens,
			Stream:            input.Options.Stream,
			Transport:         input.Options.Transport,
			APIKey:            apiKey,
			BaseURL:           input.Options.BaseURL,
			HTTPClient:        input.Options.HTTPClient,
			Timeout:           input.Options.Timeout,
			Headers:           input.Options.Headers,
			ToolChoice:        input.Options.ToolChoice,
			SessionID:         input.Options.SessionID,
			CacheRetention:    input.Options.CacheRetention,
			ReasoningEffort:   input.Options.ReasoningEffort,
			ThinkingBudgets:   input.Options.ThinkingBudgets,
			ReasoningSummary:  input.Options.ReasoningSummary,
			ServiceTier:       input.Options.ServiceTier,
			TextVerbosity:     input.Options.TextVerbosity,
			Metadata:          input.Options.Metadata,
			OnPayload:         input.Options.OnPayload,
			OnResponse:        input.Options.OnResponse,
			MaxRetries:        input.Options.MaxRetries,
			MaxRetryDelay:     input.Options.MaxRetryDelay,
			ParallelToolCalls: input.Options.ParallelToolCalls,
		}
		if executionMode == ToolExecutionInterleaved && len(input.ToolSpecs) > 0 {
			disabled := false
			options.ParallelToolCalls = &disabled
		}
		request := ai.CompletionRequest{
			Provider: input.Provider,
			Model:    input.Model,
			Messages: requestMessages,
			Tools:    input.ToolSpecs,
			Options:  options,
		}
		resultMessage, assistant, blocks, err := runProviderAssistantTurn(ctx, input.EventSink, request, input.StreamFn, executionMode == ToolExecutionInterleaved, &result)
		if err != nil {
			return result, err
		}

		result.Messages = append(result.Messages, assistant)
		conversation = append(conversation, ai.Message{
			Role:       "assistant",
			Content:    assistant["content"],
			StopReason: resultMessage.StopReason,
		})

		executed, terminate, err := executeToolBatch(ctx, input, assistant, conversation, blocks, &result)
		if err != nil {
			return result, err
		}
		for _, item := range executed {
			if !item.executed {
				continue
			}
			result.Messages = append(result.Messages, item.message)
			conversation = append(conversation, ai.Message{
				Role:       "toolResult",
				Content:    item.message["text"],
				ToolCallID: item.call.ID,
				ToolName:   item.call.Name,
				IsError:    item.result.IsError,
			})
		}

		toolResultMessages := make([]Message, 0, len(executed))
		for _, item := range executed {
			if item.executed {
				toolResultMessages = append(toolResultMessages, item.message)
			}
		}
		emit(&result, input.EventSink, Event{
			"type":            "turn_end",
			"message":         assistant,
			"toolResults":     cloneMessages(toolResultMessages),
			"messageRole":     "assistant",
			"toolResultCount": len(executed),
		})
		if terminate || resultMessage.StopReason != "toolUse" {
			steering := drainMessages(input.GetFollowUpMessages)
			if len(steering) == 0 {
				break
			}
			if round+1 < maxRounds {
				emit(&result, input.EventSink, Event{"type": "turn_start"})
				for _, message := range steering {
					conversation = append(conversation, message)
					mapped := aiMessageToEventMessage(message)
					result.Messages = append(result.Messages, mapped)
					emit(&result, input.EventSink, Event{"type": "message_start", "message": mapped})
					emit(&result, input.EventSink, Event{"type": "message_end", "message": mapped})
				}
			}
			continue
		}

		steering := drainMessages(input.GetSteeringMessages)
		if len(steering) > 0 {
			emit(&result, input.EventSink, Event{"type": "turn_start"})
			for _, message := range steering {
				conversation = append(conversation, message)
				mapped := aiMessageToEventMessage(message)
				result.Messages = append(result.Messages, mapped)
				emit(&result, input.EventSink, Event{"type": "message_start", "message": mapped})
				emit(&result, input.EventSink, Event{"type": "message_end", "message": mapped})
			}
		} else if round+1 < maxRounds {
			emit(&result, input.EventSink, Event{"type": "turn_start"})
		}
	}

	emit(&result, input.EventSink, Event{"type": "agent_end", "messageCount": len(result.Messages)})
	return result, nil
}

func runProviderAssistantTurn(ctx context.Context, sink func(Event), request ai.CompletionRequest, streamFn func(context.Context, ai.CompletionRequest) *ai.EventStream, interleaved bool, result *LoopResult) (ai.NormalizedResult, Message, []ai.ContentBlock, error) {
	streamFunction := ai.Stream
	if streamFn != nil {
		streamFunction = streamFn
	}

	if request.Options.Stream {
		partialBlocks := []ai.ContentBlock{}
		started := false
		stream := streamFunction(ctx, request)
		for event := range stream.Events() {
			if !started {
				started = true
				emit(result, sink, Event{
					"type":                  "message_start",
					"message":               AssistantMessage(partialBlocks, "stop"),
					"assistantEventType":    "start",
					"assistantMessageEvent": buildAssistantMessageEvent(ai.NormalizedEvent{Type: "start"}),
				})
			}
			if applyNormalizedEvent(&partialBlocks, event) {
				if interleaved && !interleavedEventVisible(partialBlocks, event) {
					continue
				}
				visibleBlocks := partialBlocks
				if interleaved {
					visibleBlocks = interleavedAssistantBlocks(partialBlocks)
				}
				emit(result, sink, Event{
					"type":                  "message_update",
					"message":               AssistantMessage(visibleBlocks, "stop"),
					"assistantEventType":    firstAssistantUpdateEventType(event),
					"assistantMessageEvent": buildAssistantMessageEvent(event),
				})
			}
		}
		resultMessage, err := stream.Result()
		if err != nil {
			return resultMessage, nil, nil, err
		}
		blocks := ai.ParseContentBlocks(resultMessage.Content)
		if interleaved {
			blocks = interleavedAssistantBlocks(blocks)
		}
		assistant := assistantMessageFromNormalized(resultMessage, blocks)
		applyUsage(assistant, resultMessage.Usage)
		if !started {
			startEvent := ai.NormalizedEvent{Type: "start", ContentIdx: 0}
			emit(result, sink, Event{
				"type":                  "message_start",
				"message":               assistant,
				"assistantEventType":    startEvent.Type,
				"assistantMessageEvent": buildAssistantMessageEvent(startEvent),
			})
		}
		emit(result, sink, Event{"type": "message_end", "message": assistant})
		return resultMessage, assistant, blocks, nil
	}

	resultMessage, _, err := ai.Complete(ctx, request)
	if err != nil {
		return resultMessage, nil, nil, err
	}
	blocks := ai.ParseContentBlocks(resultMessage.Content)
	if interleaved {
		blocks = interleavedAssistantBlocks(blocks)
	}
	assistant := assistantMessageFromNormalized(resultMessage, blocks)
	applyUsage(assistant, resultMessage.Usage)
	updateEventType := firstAssistantUpdate(blocks)
	updateEvent := map[string]any{
		"type":         updateEventType,
		"contentIndex": 0,
		"content":      assistantMessageTextForEvent(blocks),
	}
	emit(result, sink, Event{
		"type":                  "message_start",
		"message":               assistant,
		"assistantEventType":    "start",
		"assistantMessageEvent": map[string]any{"type": "start"},
	})
	emit(result, sink, Event{
		"type":                  "message_update",
		"message":               assistant,
		"assistantEventType":    updateEventType,
		"assistantMessageEvent": updateEvent,
	})
	emit(result, sink, Event{"type": "message_end", "message": assistant})
	return resultMessage, assistant, blocks, nil
}

func assistantMessageTextForEvent(blocks []ai.ContentBlock) string {
	switch {
	case len(blocks) == 0:
		return ""
	case blocks[0].Type == "thinking":
		return blocks[0].Thinking
	case blocks[0].Type == "toolCall":
		return ""
	default:
		return blocks[0].Text
	}
}

func firstAssistantUpdateEventType(event ai.NormalizedEvent) string {
	switch event.Type {
	case "start", "done", "error", "":
		return "text_start"
	default:
		return event.Type
	}
}

func buildAssistantMessageEvent(event ai.NormalizedEvent) map[string]any {
	out := map[string]any{
		"type":         event.Type,
		"contentIndex": event.ContentIdx,
	}
	if event.Delta != "" {
		out["delta"] = event.Delta
	}
	if event.Content != "" {
		out["content"] = event.Content
	}
	if event.Reason != "" {
		out["reason"] = event.Reason
	}
	if event.ErrorMessage != "" {
		out["errorMessage"] = event.ErrorMessage
	}
	if event.ToolCall != nil {
		toolCall := map[string]any{
			"name":      event.ToolCall.Name,
			"arguments": event.ToolCall.Arguments,
		}
		if event.ToolCall.HasID {
			toolCall["hasId"] = true
		}
		out["toolCall"] = toolCall
	}
	return out
}

func applyUsage(message Message, usage *ai.Usage) {
	if usage == nil {
		return
	}
	message["usage"] = map[string]any{
		"input":       usage.Input,
		"output":      usage.Output,
		"cacheRead":   usage.CacheRead,
		"cacheWrite":  usage.CacheWrite,
		"totalTokens": usage.TotalTokens,
		"cost": map[string]any{
			"input":      usage.Cost.Input,
			"output":     usage.Cost.Output,
			"cacheRead":  usage.Cost.CacheRead,
			"cacheWrite": usage.Cost.CacheWrite,
			"total":      usage.Cost.Total,
		},
	}
}

func applyNormalizedEvent(blocks *[]ai.ContentBlock, event ai.NormalizedEvent) bool {
	switch event.Type {
	case "start", "done", "error":
		return false
	case "text_start":
		ensureBlock(blocks, event.ContentIdx, ai.ContentBlock{Type: "text"})
		return true
	case "thinking_start":
		ensureBlock(blocks, event.ContentIdx, ai.ContentBlock{Type: "thinking"})
		return true
	case "toolcall_start":
		ensureBlock(blocks, event.ContentIdx, ai.ContentBlock{Type: "toolCall", Arguments: map[string]any{}})
		return true
	case "text_delta":
		ensureBlock(blocks, event.ContentIdx, ai.ContentBlock{Type: "text"})
		(*blocks)[event.ContentIdx].Text += event.Delta
		return true
	case "thinking_delta":
		ensureBlock(blocks, event.ContentIdx, ai.ContentBlock{Type: "thinking"})
		(*blocks)[event.ContentIdx].Thinking += event.Delta
		return true
	case "toolcall_delta":
		ensureBlock(blocks, event.ContentIdx, ai.ContentBlock{Type: "toolCall", Arguments: map[string]any{}})
		var arguments map[string]any
		if strings.TrimSpace(event.Delta) != "" && json.Unmarshal([]byte(event.Delta), &arguments) == nil {
			(*blocks)[event.ContentIdx].Arguments = arguments
		}
		return true
	case "text_end":
		ensureBlock(blocks, event.ContentIdx, ai.ContentBlock{Type: "text"})
		(*blocks)[event.ContentIdx].Text = event.Content
		return true
	case "thinking_end":
		ensureBlock(blocks, event.ContentIdx, ai.ContentBlock{Type: "thinking"})
		(*blocks)[event.ContentIdx].Thinking = event.Content
		return true
	case "toolcall_end":
		ensureBlock(blocks, event.ContentIdx, ai.ContentBlock{Type: "toolCall"})
		if event.ToolCall != nil {
			(*blocks)[event.ContentIdx].Name = event.ToolCall.Name
			(*blocks)[event.ContentIdx].Arguments = event.ToolCall.Arguments
		}
		return true
	default:
		return false
	}
}

func ensureBlock(blocks *[]ai.ContentBlock, index int, template ai.ContentBlock) {
	for len(*blocks) <= index {
		*blocks = append(*blocks, ai.ContentBlock{})
	}
	block := &(*blocks)[index]
	if block.Type == "" {
		*block = template
	}
	if block.Type == "toolCall" && block.Arguments == nil {
		block.Arguments = map[string]any{}
	}
}

func drainMessages(fn func() []ai.Message) []ai.Message {
	if fn == nil {
		return nil
	}
	return fn()
}

func aiMessageToEventMessage(message ai.Message) Message {
	switch message.Role {
	case "assistant":
		assistant := AssistantMessage(ai.ParseContentBlocks(message.Content), message.StopReason)
		applyAIMessageMetadata(assistant, message)
		return assistant
	case "toolResult":
		toolResult := ToolResultMessage(message.ToolCallID, message.ToolName, ToolResult{
			Text:    ai.MessageText(message),
			IsError: message.IsError,
		})
		applyAIMessageMetadata(toolResult, message)
		return toolResult
	default:
		user := UserMessage(ai.MessageText(message))
		if hasStructuredContent(message.Content) {
			user["content"] = normalizeEventContent(message.Content)
		}
		applyAIMessageMetadata(user, message)
		return user
	}
}

func hasStructuredContent(content any) bool {
	switch typed := content.(type) {
	case []ai.ContentBlock:
		return len(typed) > 0
	case []any:
		return len(ai.ParseContentBlocks(typed)) > 0
	default:
		return false
	}
}

func normalizeEventContent(content any) any {
	blocks := ai.ParseContentBlocks(content)
	if len(blocks) == 0 {
		return content
	}
	return ai.NormalizedContent(blocks)
}

func assistantMessageFromNormalized(result ai.NormalizedResult, blocks []ai.ContentBlock) Message {
	message := AssistantMessage(blocks, result.StopReason)
	message["content"] = ai.NormalizedContent(blocks)
	if result.Provider != "" {
		message["provider"] = result.Provider
	}
	if result.API != "" {
		message["api"] = result.API
	}
	if result.Model != "" {
		message["model"] = result.Model
	}
	return message
}

func applyAIMessageMetadata(target Message, message ai.Message) {
	if message.Provider != "" {
		target["provider"] = message.Provider
	}
	if message.API != "" {
		target["api"] = message.API
	}
	if message.Model != "" {
		target["model"] = message.Model
	}
}

func executeTool(ctx context.Context, tools []Tool, call ai.ContentBlock) ToolResult {
	for _, tool := range tools {
		if tool.Name == call.Name {
			if tool.Execute == nil {
				return ToolResult{Text: "", Details: map[string]any{}, IsError: false}
			}
			return tool.Execute(ctx, call)
		}
	}
	return ToolResult{Text: fmt.Sprintf("Tool %s not found", call.Name), Details: map[string]any{}, IsError: true}
}

func toolExecutionMode(input ProviderLoopInput) ToolExecutionMode {
	if input.ToolExecution == ToolExecutionInterleaved {
		return ToolExecutionInterleaved
	}
	if input.ToolExecution == ToolExecutionSequential {
		return ToolExecutionSequential
	}
	for _, tool := range input.Tools {
		if tool.ExecutionMode == ToolExecutionInterleaved {
			return ToolExecutionInterleaved
		}
		if tool.ExecutionMode == ToolExecutionSequential {
			return ToolExecutionSequential
		}
	}
	return ToolExecutionParallel
}

func interleavedAssistantBlocks(blocks []ai.ContentBlock) []ai.ContentBlock {
	firstToolCall := -1
	for idx, block := range blocks {
		if block.Type == "toolCall" {
			firstToolCall = idx
			break
		}
	}
	if firstToolCall < 0 {
		return blocks
	}
	out := make([]ai.ContentBlock, 0, firstToolCall+1)
	for idx, block := range blocks {
		if block.Type == "toolCall" && idx != firstToolCall {
			continue
		}
		out = append(out, block)
	}
	return out
}

func interleavedEventVisible(blocks []ai.ContentBlock, event ai.NormalizedEvent) bool {
	switch event.Type {
	case "toolcall_start", "toolcall_delta", "toolcall_end":
	default:
		return true
	}
	if event.ContentIdx < 0 || event.ContentIdx >= len(blocks) {
		return true
	}
	if blocks[event.ContentIdx].Type != "toolCall" {
		return true
	}
	for idx := 0; idx < event.ContentIdx && idx < len(blocks); idx++ {
		if blocks[idx].Type == "toolCall" {
			return false
		}
	}
	return true
}

func executeToolBatch(ctx context.Context, input ProviderLoopInput, assistant Message, conversation []ai.Message, blocks []ai.ContentBlock, result *LoopResult) ([]executedToolCall, bool, error) {
	calls := make([]executedToolCall, 0)
	for _, block := range blocks {
		if block.Type != "toolCall" {
			continue
		}
		calls = append(calls, executedToolCall{call: block, sourceIdx: len(calls)})
	}
	if len(calls) == 0 {
		return nil, false, nil
	}

	mode := toolExecutionMode(input)
	if mode == ToolExecutionInterleaved && len(calls) > 1 {
		calls = calls[:1]
	}

	for idx := range calls {
		call := &calls[idx]
		call.started = true
		emit(result, input.EventSink, Event{"type": "tool_execution_start", "toolCallId": call.call.ID, "toolName": call.call.Name, "args": call.call.Arguments})
		tool := findTool(input.Tools, call.call.Name)
		if tool == nil {
			call.blocked = true
			call.immediate = true
			call.result = ToolResult{Text: fmt.Sprintf("Tool %s not found", call.call.Name), Details: map[string]any{}, IsError: true}
			call.message = ToolResultMessage(call.call.ID, call.call.Name, call.result)
			continue
		}
		if tool.PrepareArguments != nil {
			prepared, err := tool.PrepareArguments(cloneArguments(call.call.Arguments))
			if err != nil {
				call.blocked = true
				call.immediate = true
				call.result = ToolResult{Text: err.Error(), IsError: true}
				call.message = ToolResultMessage(call.call.ID, call.call.Name, call.result)
				continue
			}
			call.call.Arguments = prepared
		}
		if toolSpec := findToolSpec(input.ToolSpecs, call.call.Name); toolSpec != nil {
			arguments, err := ai.ValidateToolArguments(*toolSpec, call.call)
			if err != nil {
				call.blocked = true
				call.immediate = true
				call.result = ToolResult{Text: err.Error(), IsError: true}
				call.message = ToolResultMessage(call.call.ID, call.call.Name, call.result)
				continue
			}
			call.call.Arguments = arguments
		}
		if input.BeforeToolCall != nil {
			beforeResult, err := input.BeforeToolCall(ctx, BeforeToolCallContext{
				AssistantMessage: assistant,
				ToolCall:         call.call,
				Args:             cloneArguments(call.call.Arguments),
				Context:          append([]ai.Message(nil), conversation...),
			})
			if err != nil {
				return nil, false, err
			}
			if beforeResult.Block {
				text := beforeResult.Reason
				if strings.TrimSpace(text) == "" {
					text = "tool execution blocked"
				}
				call.blocked = true
				call.immediate = true
				call.result = ToolResult{Text: text, IsError: true}
				call.message = ToolResultMessage(call.call.ID, call.call.Name, call.result)
			} else if beforeResult.Args != nil {
				call.call.Arguments = cloneArguments(beforeResult.Args)
			}
		}
	}

	if mode == ToolExecutionInterleaved || mode == ToolExecutionSequential {
		for idx := range calls {
			if calls[idx].blocked {
				calls[idx].executed = true
				emitToolExecutionEnd(result, input.EventSink, &calls[idx])
				emitToolResultMessage(result, input.EventSink, &calls[idx])
				continue
			}
			executeOneToolCall(ctx, input, assistant, conversation, input.EventSink, &calls[idx])
			emitToolExecutionEnd(result, input.EventSink, &calls[idx])
			emitToolResultMessage(result, input.EventSink, &calls[idx])
		}
	} else {
		type completed struct {
			index int
			err   error
		}
		done := make(chan completed, len(calls))
		pending := 0
		for idx := range calls {
			if calls[idx].blocked {
				calls[idx].executed = true
				emitToolExecutionEnd(result, input.EventSink, &calls[idx])
				continue
			}
			pending++
			go func(i int) {
				executeOneToolCall(ctx, input, assistant, conversation, input.EventSink, &calls[i])
				done <- completed{index: i}
			}(idx)
		}
		for pending > 0 {
			item := <-done
			pending--
			if item.err != nil {
				return nil, false, item.err
			}
			emitToolExecutionEnd(result, input.EventSink, &calls[item.index])
		}
		for idx := range calls {
			if calls[idx].executed {
				emitToolResultMessage(result, input.EventSink, &calls[idx])
			}
		}
	}

	terminate := true
	for idx := range calls {
		if !calls[idx].result.Terminate {
			terminate = false
		}
	}
	return calls, terminate, nil
}

func executeOneToolCall(ctx context.Context, input ProviderLoopInput, assistant Message, conversation []ai.Message, sink func(Event), call *executedToolCall) {
	if err := ctx.Err(); err != nil {
		call.result = ToolResult{Text: err.Error(), IsError: true}
		call.message = ToolResultMessage(call.call.ID, call.call.Name, call.result)
		call.executed = true
		return
	}
	toolResult := executeToolWithUpdates(ctx, input.Tools, call.call, sink, func(partial ToolResult) {
		if sink != nil {
			sink(Event{
				"type":          "tool_execution_update",
				"toolCallId":    call.call.ID,
				"toolName":      call.call.Name,
				"args":          call.call.Arguments,
				"partialResult": toEventToolResult(partial),
			})
		}
	})
	call.result = toolResult
	if input.AfterToolCall != nil {
		updated, err := input.AfterToolCall(ctx, AfterToolCallContext{
			AssistantMessage: assistant,
			ToolCall:         call.call,
			Args:             cloneArguments(call.call.Arguments),
			Context:          append([]ai.Message(nil), conversation...),
			Result:           toolResult,
			IsError:          toolResult.IsError,
		})
		if err != nil {
			toolResult = ToolResult{Text: err.Error(), IsError: true}
		} else {
			toolResult = updated
		}
	}
	call.result = toolResult
	if !call.result.IsError {
		if sink != nil {
			sink(Event{
				"type":          "tool_execution_update",
				"toolCallId":    call.call.ID,
				"toolName":      call.call.Name,
				"args":          call.call.Arguments,
				"partialResult": toEventToolResult(call.result),
			})
		}
	}
	call.message = ToolResultMessage(call.call.ID, call.call.Name, toolResult)
	call.executed = true
}

func executeToolWithUpdates(ctx context.Context, tools []Tool, call ai.ContentBlock, sink func(Event), onUpdate func(ToolResult)) ToolResult {
	for _, tool := range tools {
		if tool.Name != call.Name {
			continue
		}
		if tool.Execute == nil && tool.ExecuteWithUpdate == nil {
			return ToolResult{Text: "", Details: map[string]any{}, IsError: false}
		}
		if tool.ExecuteWithUpdate == nil {
			result := tool.Execute(ctx, call)
			return result
		}
		return tool.ExecuteWithUpdate(ctx, call, onUpdate)
	}
	return ToolResult{Text: fmt.Sprintf("Tool %s not found", call.Name), Details: map[string]any{}, IsError: true}
}

func toEventToolResult(result ToolResult) map[string]any {
	text := result.Text
	if text == "" {
		text = toolResultText(result)
	}
	return map[string]any{
		"text":      text,
		"content":   cloneToolResultContent(result.Content),
		"details":   cloneToolResultDetails(result.Details),
		"isError":   result.IsError,
		"terminate": result.Terminate,
	}
}

func cloneToolResultDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(details))
	for key, value := range details {
		cloned[key] = value
	}
	return cloned
}

func toolResultText(result ToolResult) string {
	if text := result.Text; text != "" {
		return text
	}
	return ai.ContentText(result.Content)
}

func toolResultContent(content []ai.ContentBlock) []any {
	if len(content) == 0 {
		return nil
	}
	return cloneToolResultContent(content)
}

func cloneToolResultContent(content []ai.ContentBlock) []any {
	if len(content) == 0 {
		return nil
	}
	blocks := ai.NormalizedContent(content)
	out := make([]any, len(blocks))
	for i, block := range blocks {
		out[i] = block
	}
	return out
}

func findToolSpec(tools []ai.Tool, name string) *ai.Tool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

func findTool(tools []Tool, name string) *Tool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

func cloneArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(arguments))
	for key, value := range arguments {
		out[key] = value
	}
	return out
}

func emitToolExecutionEnd(result *LoopResult, sink func(Event), call *executedToolCall) {
	emit(result, sink, Event{
		"type":       "tool_execution_end",
		"toolCallId": call.call.ID,
		"toolName":   call.call.Name,
		"result":     toEventToolResult(call.result),
		"isError":    call.result.IsError,
		"text":       call.result.Text,
	})
}

func emitToolResultMessage(result *LoopResult, sink func(Event), call *executedToolCall) {
	emit(result, sink, Event{"type": "message_start", "message": call.message})
	emit(result, sink, Event{"type": "message_end", "message": call.message})
}

func UserMessage(text string) Message {
	return Message{"role": "user", "text": text}
}

func AssistantMessage(blocks []ai.ContentBlock, stopReason string) Message {
	if stopReason == "" {
		stopReason = "stop"
	}
	return Message{
		"role":       "assistant",
		"stopReason": stopReason,
		"text":       ai.ContentText(blocks),
		"content":    ai.NormalizedContent(blocks),
		"usage": map[string]any{
			"input":       1,
			"output":      1,
			"cacheRead":   0,
			"cacheWrite":  0,
			"totalTokens": 2,
		},
	}
}

func ToolResultMessage(toolCallID, toolName string, result ToolResult) Message {
	return Message{
		"role":       "toolResult",
		"toolCallId": toolCallID,
		"toolName":   toolName,
		"text":       toolResultText(result),
		"content":    toolResultContent(result.Content),
		"details":    cloneToolResultDetails(result.Details),
		"isError":    result.IsError,
	}
}

func firstAssistantUpdate(blocks []ai.ContentBlock) string {
	if len(blocks) == 0 {
		return "done"
	}
	switch blocks[0].Type {
	case "toolCall":
		return "toolcall_start"
	case "thinking":
		return "thinking_start"
	default:
		return "text_start"
	}
}
