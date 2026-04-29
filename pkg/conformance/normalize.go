package conformance

import "github.com/badlogic/pigo/pkg/ai"

var ParseTurnContent = ai.ParseContent
var MessageText = ai.MessageText
var ContentText = ai.ContentText

func NormalizedAssistant(blocks []ai.ContentBlock, stopReason string) map[string]any {
	if stopReason == "" {
		stopReason = "stop"
	}
	content := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "toolCall":
			content = append(content, map[string]any{
				"type":      "toolCall",
				"name":      block.Name,
				"arguments": block.Arguments,
				"hasId":     block.ID != "",
			})
		case "thinking":
			content = append(content, map[string]any{
				"type":     "thinking",
				"thinking": block.Thinking,
				"redacted": block.Redacted,
			})
		default:
			content = append(content, map[string]any{
				"type": "text",
				"text": block.Text,
			})
		}
	}
	return map[string]any{
		"role":       "assistant",
		"stopReason": stopReason,
		"text":       ContentText(blocks),
		"content":    content,
		"usage": map[string]any{
			"input":       1,
			"output":      1,
			"cacheRead":   0,
			"cacheWrite":  0,
			"totalTokens": 2,
		},
	}
}

func NormalizedUser(text string) map[string]any {
	return map[string]any{"role": "user", "text": text}
}

func NormalizedToolResult(toolCallID, toolName, text string, isError bool) map[string]any {
	return map[string]any{
		"role":       "toolResult",
		"toolCallId": toolCallID,
		"toolName":   toolName,
		"text":       text,
		"isError":    isError,
	}
}

var AssistantEvents = ai.AssistantEvents
