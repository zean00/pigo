package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ParseContent(raw json.RawMessage) ([]ContentBlock, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []ContentBlock{{Type: "text", Text: text}}, nil
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("parse content: %w", err)
	}
	return blocks, nil
}

func ParseContentBlocks(raw any) []ContentBlock {
	switch typed := raw.(type) {
	case []ContentBlock:
		return append([]ContentBlock(nil), typed...)
	case []any:
		blocks := make([]ContentBlock, 0, len(typed))
		for _, item := range typed {
			block := parseContentBlock(item)
			if block != nil {
				blocks = append(blocks, *block)
			}
		}
		return blocks
	case json.RawMessage:
		blocks, _ := ParseContent(typed)
		return blocks
	case string:
		if typed == "" {
			return nil
		}
		var blocks []ContentBlock
		if err := json.Unmarshal([]byte(typed), &blocks); err == nil {
			return blocks
		}
		return []ContentBlock{{Type: "text", Text: typed}}
	case []byte:
		if len(typed) == 0 {
			return nil
		}
		var blocks []ContentBlock
		if err := json.Unmarshal(typed, &blocks); err == nil {
			return blocks
		}
		return []ContentBlock{{Type: "text", Text: string(typed)}}
	default:
		return nil
	}
}

func parseContentBlock(raw any) *ContentBlock {
	rawMap, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	blockType := asString(rawMap["type"])
	if blockType == "" {
		return nil
	}
	block := &ContentBlock{Type: blockType}
	block.Text = asString(rawMap["text"])
	block.TextSignature = asString(rawMap["textSignature"])
	block.Thinking = asString(rawMap["thinking"])
	block.ThinkingSignature = asString(rawMap["thinkingSignature"])
	block.Redacted = asBool(rawMap["redacted"])
	block.Data = asString(rawMap["data"])
	block.MimeType = asString(rawMap["mimeType"])
	block.ID = asString(rawMap["id"])
	if block.ID == "" && asBool(rawMap["hasId"]) {
		block.ID = ""
	}
	block.Name = asString(rawMap["name"])
	block.Arguments = parseArgumentMap(rawMap["arguments"])
	block.ThoughtSignature = asString(rawMap["thoughtSignature"])
	return block
}

func parseArgumentMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case string:
		var parsed map[string]any
		if typed == "" {
			return map[string]any{}
		}
		if err := json.Unmarshal([]byte(typed), &parsed); err != nil {
			return map[string]any{"arguments": typed}
		}
		return parsed
	default:
		return nil
	}
}

func asBool(value any) bool {
	result, ok := value.(bool)
	if !ok {
		return false
	}
	return result
}

func asFloat64(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return 0
		}
		return number
	default:
		return 0
	}
}

func firstNonEmptyString(values ...any) string {
	for _, value := range values {
		if text := asString(value); strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func MessageText(message Message) string {
	if text, ok := message.Content.(string); ok {
		return text
	}
	blocks, ok := message.Content.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		object, ok := block.(map[string]any)
		if !ok || object["type"] != "text" {
			continue
		}
		if text, ok := object["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func ContentText(blocks []ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func NormalizedContent(blocks []ContentBlock) []any {
	content := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "toolCall":
			item := map[string]any{
				"type":      "toolCall",
				"id":        block.ID,
				"name":      block.Name,
				"arguments": block.Arguments,
			}
			if block.ThoughtSignature != "" {
				item["thoughtSignature"] = block.ThoughtSignature
			}
			content = append(content, item)
		case "thinking":
			item := map[string]any{
				"type":     "thinking",
				"thinking": block.Thinking,
				"redacted": block.Redacted,
			}
			if block.ThinkingSignature != "" {
				item["thinkingSignature"] = block.ThinkingSignature
			}
			content = append(content, item)
		case "image":
			content = append(content, map[string]any{
				"type":     "image",
				"data":     block.Data,
				"mimeType": block.MimeType,
			})
		default:
			item := map[string]any{
				"type": "text",
				"text": block.Text,
			}
			if block.TextSignature != "" {
				item["textSignature"] = block.TextSignature
			}
			content = append(content, item)
		}
	}
	return content
}

func AssistantEvents(blocks []ContentBlock, stopReason string) []NormalizedEvent {
	events := []NormalizedEvent{{Type: "start"}}
	for index, block := range blocks {
		switch block.Type {
		case "toolCall":
			events = append(events,
				NormalizedEvent{Type: "toolcall_start", ContentIdx: index},
				NormalizedEvent{Type: "toolcall_delta", ContentIdx: index, Delta: mustJSON(block.Arguments)},
				NormalizedEvent{
					Type:       "toolcall_end",
					ContentIdx: index,
					ToolCall: &NormalizedTool{
						ID:               block.ID,
						Name:             block.Name,
						Arguments:        block.Arguments,
						HasID:            block.ID != "",
						ThoughtSignature: block.ThoughtSignature,
					},
				},
			)
		case "thinking":
			events = append(events,
				NormalizedEvent{Type: "thinking_start", ContentIdx: index},
				NormalizedEvent{Type: "thinking_delta", ContentIdx: index, Delta: block.Thinking},
				NormalizedEvent{Type: "thinking_end", ContentIdx: index, Content: block.Thinking},
			)
		default:
			events = append(events,
				NormalizedEvent{Type: "text_start", ContentIdx: index},
				NormalizedEvent{Type: "text_delta", ContentIdx: index, Delta: block.Text},
				NormalizedEvent{Type: "text_end", ContentIdx: index, Content: block.Text},
			)
		}
	}
	if stopReason == "" {
		stopReason = "stop"
	}
	return appendTerminalEvent(events, stopReason)
}

func appendTerminalEvent(events []NormalizedEvent, stopReason string) []NormalizedEvent {
	if stopReason == "error" || stopReason == "aborted" {
		return append(events, NormalizedEvent{Type: "error", Reason: stopReason})
	}
	return append(events, NormalizedEvent{Type: "done", Reason: stopReason})
}

func AttachEventPayloads(events []NormalizedEvent, result NormalizedResult) []NormalizedEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]NormalizedEvent, len(events))
	for index, event := range events {
		out[index] = attachEventPayload(event, result)
	}
	return out
}

func attachEventPayload(event NormalizedEvent, result NormalizedResult) NormalizedEvent {
	switch event.Type {
	case "done":
		if event.Message == nil {
			message := result
			event.Message = &message
		}
	case "error":
		if event.Error == nil {
			message := result
			if message.ErrorMessage == "" {
				message.ErrorMessage = event.ErrorMessage
			}
			event.Error = &message
		}
	default:
		if event.Partial == nil {
			partial := result
			event.Partial = &partial
		}
	}
	return event
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}
