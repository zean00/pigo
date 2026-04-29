package agentcore

import (
	"context"
	"fmt"
	"strings"

	"github.com/badlogic/pigo/pkg/ai"
)

type Message map[string]any
type Event map[string]any

type ToolResult struct {
	Text    string
	Details map[string]any
	IsError bool
}

type Tool struct {
	Name    string
	Execute func(ctx context.Context, call ai.ContentBlock) ToolResult
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
}

type LoopResult struct {
	Events   []Event
	Messages []Message
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
		result.Events = append(result.Events,
			Event{"type": "message_start", "message": user},
			Event{"type": "message_end", "message": user},
		)
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
		result.Events = append(result.Events,
			Event{"type": "message_start", "message": assistant},
			Event{"type": "message_update", "message": assistant, "assistantEventType": firstAssistantUpdate(turn.Content)},
			Event{"type": "message_end", "message": assistant},
		)
		result.Messages = append(result.Messages, assistant)

		toolResults := []Message{}
		for _, block := range turn.Content {
			if block.Type != "toolCall" {
				continue
			}
			toolResult := executeTool(ctx, input.Tools, block)
			toolMessage := ToolResultMessage(block.ID, block.Name, toolResult.Text, toolResult.IsError)
			result.Events = append(result.Events,
				Event{"type": "tool_execution_start", "toolCallId": block.ID, "toolName": block.Name, "args": block.Arguments},
			)
			if !toolResult.IsError {
				result.Events = append(result.Events, Event{
					"type":       "tool_execution_update",
					"toolCallId": block.ID,
					"toolName":   block.Name,
					"args":       block.Arguments,
					"text":       "started:" + block.ID,
				})
			}
			result.Events = append(result.Events,
				Event{"type": "tool_execution_end", "toolCallId": block.ID, "toolName": block.Name, "text": toolResult.Text, "isError": toolResult.IsError},
				Event{"type": "message_start", "message": toolMessage},
				Event{"type": "message_end", "message": toolMessage},
			)
			result.Messages = append(result.Messages, toolMessage)
			toolResults = append(toolResults, toolMessage)
		}

		result.Events = append(result.Events, Event{
			"type":            "turn_end",
			"messageRole":     "assistant",
			"toolResultCount": len(toolResults),
		})
	}

	result.Events = append(result.Events, Event{"type": "agent_end", "messageCount": len(result.Messages)})
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
		result.Events = append(result.Events, Event{"type": "message_start", "message": user})
		result.Events = append(result.Events, Event{"type": "message_end", "message": user})
		result.Messages = append(result.Messages, user)
	}

	if len(result.Messages) == 0 {
		user := UserMessage("")
		conversation = append(conversation, ai.Message{Role: "user", Content: ""})
		result.Messages = append(result.Messages, user)
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
		result.Events = append(result.Events, Event{"type": "message_start", "message": assistant})
		result.Events = append(result.Events, Event{"type": "message_update", "message": assistant, "assistantEventType": firstAssistantUpdate(blocks)})
		result.Events = append(result.Events, Event{"type": "message_end", "message": assistant})

		toolResultCount := 0
		for _, block := range blocks {
			if block.Type != "toolCall" {
				continue
			}
			toolResultCount++
			toolResult := executeTool(ctx, input.Tools, block)
			toolMessage := ToolResultMessage(block.ID, block.Name, toolResult.Text, toolResult.IsError)
			result.Events = append(result.Events,
				Event{"type": "tool_execution_start", "toolCallId": block.ID, "toolName": block.Name, "args": block.Arguments},
			)
			if !toolResult.IsError {
				result.Events = append(result.Events, Event{
					"type":       "tool_execution_update",
					"toolCallId": block.ID,
					"toolName":   block.Name,
					"args":       block.Arguments,
					"text":       "started:" + block.ID,
				})
			}
			result.Events = append(result.Events, Event{
				"type":       "tool_execution_end",
				"toolCallId": block.ID,
				"toolName":   block.Name,
				"text":       toolResult.Text,
				"isError":    toolResult.IsError,
			})
			result.Events = append(result.Events, Event{"type": "message_start", "message": toolMessage})
			result.Events = append(result.Events, Event{"type": "message_end", "message": toolMessage})

			result.Messages = append(result.Messages, toolMessage)
			conversation = append(conversation, ai.Message{
				Role:       "toolResult",
				Content:    toolMessage["text"],
				ToolCallID: block.ID,
				ToolName:   block.Name,
				IsError:    toolResult.IsError,
			})
		}
		result.Events = append(result.Events, Event{
			"type":            "turn_end",
			"messageRole":     "assistant",
			"toolResultCount": toolResultCount,
		})

		if resultMessage.StopReason != "toolUse" {
			break
		}
	}

	result.Events = append(result.Events, Event{"type": "agent_end", "messageCount": len(result.Messages)})
	return result, nil
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
