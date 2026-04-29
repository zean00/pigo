package agentcore

import (
	"context"
	"fmt"
	"strings"

	"github.com/badlogic/pigo/pkg/ai"
)

type Message map[string]any
type Event map[string]any

type ToolExecutionMode string

const (
	ToolExecutionSequential ToolExecutionMode = "sequential"
	ToolExecutionParallel   ToolExecutionMode = "parallel"
)

type ToolResult struct {
	Text    string
	Details map[string]any
	IsError bool
	Terminate bool
}

type Tool struct {
	Name          string
	ExecutionMode ToolExecutionMode
	Execute       func(ctx context.Context, call ai.ContentBlock) ToolResult
}

type BeforeToolCallResult struct {
	Block  bool
	Reason string
}

type BeforeToolCallContext struct {
	AssistantMessage Message
	ToolCall         ai.ContentBlock
	Context          []ai.Message
}

type AfterToolCallContext struct {
	AssistantMessage Message
	ToolCall         ai.ContentBlock
	Context          []ai.Message
	Result           ToolResult
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
	Prompts   []string
	Tools     []Tool
	History   []ai.Message
	Provider  string
	Model     string
	ToolSpecs []ai.Tool
	Options   ai.ChatOptions
	MaxRounds int
	ToolExecution ToolExecutionMode
	GetSteeringMessages func() []ai.Message
	GetFollowUpMessages func() []ai.Message
	BeforeToolCall func(ctx context.Context, input BeforeToolCallContext) (BeforeToolCallResult, error)
	AfterToolCall func(ctx context.Context, input AfterToolCallContext) (ToolResult, error)
	EventSink func(Event)
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
			toolMessage := ToolResultMessage(block.ID, block.Name, toolResult.Text, toolResult.IsError)
			emit(&result, nil, Event{"type": "tool_execution_start", "toolCallId": block.ID, "toolName": block.Name, "args": block.Arguments})
			if !toolResult.IsError {
				emit(&result, nil, Event{
					"type":       "tool_execution_update",
					"toolCallId": block.ID,
					"toolName":   block.Name,
					"args":       block.Arguments,
					"text":       "started:" + block.ID,
				})
			}
			emit(&result, nil, Event{"type": "tool_execution_end", "toolCallId": block.ID, "toolName": block.Name, "text": toolResult.Text, "isError": toolResult.IsError})
			emit(&result, nil, Event{"type": "message_start", "message": toolMessage})
			emit(&result, nil, Event{"type": "message_end", "message": toolMessage})
			result.Messages = append(result.Messages, toolMessage)
			toolResults = append(toolResults, toolMessage)
		}

		emit(&result, nil, Event{
			"type":            "turn_end",
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
		request := ai.CompletionRequest{
			Provider: input.Provider,
			Model:    input.Model,
			Messages: conversation,
			Tools:    input.ToolSpecs,
			Options: ai.ChatOptions{
				Stream:      input.Options.Stream,
				ToolChoice:  input.Options.ToolChoice,
				MaxTokens:   input.Options.MaxTokens,
				Temperature: input.Options.Temperature,
				APIKey:      input.Options.APIKey,
				BaseURL:     input.Options.BaseURL,
				HTTPClient:  input.Options.HTTPClient,
				Timeout:     input.Options.Timeout,
				Headers:     input.Options.Headers,
			},
		}
		resultMessage, _, err := ai.Complete(ctx, request)
		if err != nil {
			return result, err
		}

		blocks := ai.ParseContentBlocks(resultMessage.Content)
		assistant := assistantMessageFromNormalized(blocks, resultMessage.StopReason)
		if resultMessage.Usage != nil {
			assistant["usage"] = map[string]any{
				"input":       resultMessage.Usage.Input,
				"output":      resultMessage.Usage.Output,
				"cacheRead":   resultMessage.Usage.CacheRead,
				"cacheWrite":  resultMessage.Usage.CacheWrite,
				"totalTokens": resultMessage.Usage.TotalTokens,
			}
		}

		result.Messages = append(result.Messages, assistant)
		conversation = append(conversation, ai.Message{
			Role:    "assistant",
			Content: assistant["content"],
		})
		emit(&result, input.EventSink, Event{"type": "message_start", "message": assistant})
		emit(&result, input.EventSink, Event{"type": "message_update", "message": assistant, "assistantEventType": firstAssistantUpdate(blocks)})
		emit(&result, input.EventSink, Event{"type": "message_end", "message": assistant})

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

		emit(&result, input.EventSink, Event{
			"type":            "turn_end",
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
		}
	}

	emit(&result, input.EventSink, Event{"type": "agent_end", "messageCount": len(result.Messages)})
	return result, nil
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
		return AssistantMessage(ai.ParseContentBlocks(message.Content), message.StopReason)
	case "toolResult":
		return ToolResultMessage(message.ToolCallID, message.ToolName, ai.MessageText(message), message.IsError)
	default:
		return UserMessage(ai.MessageText(message))
	}
}

func assistantMessageFromNormalized(blocks []ai.ContentBlock, stopReason string) Message {
	message := AssistantMessage(blocks, stopReason)
	message["content"] = ai.NormalizedContent(blocks)
	return message
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
	if input.ToolExecution == ToolExecutionSequential {
		return ToolExecutionSequential
	}
	for _, tool := range input.Tools {
		if tool.ExecutionMode == ToolExecutionSequential {
			return ToolExecutionSequential
		}
	}
	return ToolExecutionParallel
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

	for idx := range calls {
		call := &calls[idx]
		call.started = true
		emit(result, input.EventSink, Event{"type": "tool_execution_start", "toolCallId": call.call.ID, "toolName": call.call.Name, "args": call.call.Arguments})
		if input.BeforeToolCall != nil {
			beforeResult, err := input.BeforeToolCall(ctx, BeforeToolCallContext{
				AssistantMessage: assistant,
				ToolCall:         call.call,
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
				call.result = ToolResult{Text: text, IsError: true}
				call.message = ToolResultMessage(call.call.ID, call.call.Name, call.result.Text, call.result.IsError)
			}
		}
	}

	mode := toolExecutionMode(input)
	if mode == ToolExecutionSequential {
		for idx := range calls {
			if calls[idx].blocked {
				calls[idx].executed = true
				emitFinalizedToolCall(result, input.EventSink, &calls[idx])
				continue
			}
			executeOneToolCall(ctx, input, assistant, conversation, &calls[idx])
			emitFinalizedToolCall(result, input.EventSink, &calls[idx])
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
				emitFinalizedToolCall(result, input.EventSink, &calls[idx])
				continue
			}
			pending++
			go func(i int) {
				executeOneToolCall(ctx, input, assistant, conversation, &calls[i])
				done <- completed{index: i}
			}(idx)
		}
		for pending > 0 {
			item := <-done
			pending--
			if item.err != nil {
				return nil, false, item.err
			}
			emitFinalizedToolCall(result, input.EventSink, &calls[item.index])
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

func executeOneToolCall(ctx context.Context, input ProviderLoopInput, assistant Message, conversation []ai.Message, call *executedToolCall) {
	if err := ctx.Err(); err != nil {
		call.result = ToolResult{Text: err.Error(), IsError: true}
		call.message = ToolResultMessage(call.call.ID, call.call.Name, call.result.Text, call.result.IsError)
		call.executed = true
		return
	}
	toolResult := executeTool(ctx, input.Tools, call.call)
	if input.AfterToolCall != nil {
		updated, err := input.AfterToolCall(ctx, AfterToolCallContext{
			AssistantMessage: assistant,
			ToolCall:         call.call,
			Context:          append([]ai.Message(nil), conversation...),
			Result:           toolResult,
		})
		if err != nil {
			toolResult = ToolResult{Text: err.Error(), IsError: true}
		} else {
			toolResult = updated
		}
	}
	call.result = toolResult
	call.message = ToolResultMessage(call.call.ID, call.call.Name, toolResult.Text, toolResult.IsError)
	call.executed = true
}

func emitFinalizedToolCall(result *LoopResult, sink func(Event), call *executedToolCall) {
	if !call.result.IsError {
		emit(result, sink, Event{
			"type":       "tool_execution_update",
			"toolCallId": call.call.ID,
			"toolName":   call.call.Name,
			"args":       call.call.Arguments,
			"text":       "started:" + call.call.ID,
		})
	}
	emit(result, sink, Event{"type": "tool_execution_end", "toolCallId": call.call.ID, "toolName": call.call.Name, "text": call.result.Text, "isError": call.result.IsError})
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

func ToolResultMessage(toolCallID, toolName, text string, isError bool) Message {
	return Message{
		"role":       "toolResult",
		"toolCallId": toolCallID,
		"toolName":   toolName,
		"text":       text,
		"isError":    isError,
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
