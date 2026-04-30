package ai

import (
	"crypto/sha1"
	"encoding/hex"
	"strconv"
	"strings"
)

const (
	nonVisionUserImagePlaceholder = "(image omitted: model does not support images)"
	nonVisionToolImagePlaceholder = "(tool image omitted: model does not support images)"
)

func PrepareMessagesForModel(messages []Message, model Model) []Message {
	normalizer := toolCallIDNormalizer(model)
	return TransformMessages(messages, model, normalizer)
}

func TransformMessages(messages []Message, model Model, normalizeToolCallID func(string, Model, Message) string) []Message {
	toolCallIDMap := map[string]string{}
	imageAwareMessages := downgradeUnsupportedImages(messages, model)

	transformed := make([]Message, 0, len(imageAwareMessages))
	for _, message := range imageAwareMessages {
		switch message.Role {
		case "user":
			transformed = append(transformed, cloneAIMessage(message))
		case "toolResult":
			next := cloneAIMessage(message)
			if normalizedID, ok := toolCallIDMap[message.ToolCallID]; ok && normalizedID != "" && normalizedID != message.ToolCallID {
				next.ToolCallID = normalizedID
			}
			transformed = append(transformed, next)
		case "assistant":
			next := cloneAIMessage(message)
			blocks := messageContentBlocks(message.Content)
			isSameModel := message.Provider == model.Provider && message.API == model.API && message.Model == model.ID
			content := make([]ContentBlock, 0, len(blocks))

			for _, block := range blocks {
				switch block.Type {
				case "thinking":
					if block.Redacted {
						if isSameModel {
							content = append(content, block)
						}
						continue
					}
					if isSameModel && block.ThinkingSignature != "" {
						content = append(content, block)
						continue
					}
					if strings.TrimSpace(block.Thinking) == "" {
						continue
					}
					if isSameModel {
						content = append(content, block)
						continue
					}
					content = append(content, ContentBlock{Type: "text", Text: block.Thinking})
				case "text":
					if isSameModel {
						content = append(content, block)
					} else {
						content = append(content, ContentBlock{Type: "text", Text: block.Text})
					}
				case "toolCall":
					toolCall := block
					if !isSameModel {
						toolCall.ThoughtSignature = ""
					}
					if !isSameModel && normalizeToolCallID != nil {
						normalizedID := normalizeToolCallID(block.ID, model, message)
						if normalizedID != "" && normalizedID != block.ID {
							toolCallIDMap[block.ID] = normalizedID
							toolCall.ID = normalizedID
						}
					}
					content = append(content, toolCall)
				default:
					content = append(content, block)
				}
			}

			next.Content = NormalizedContent(content)
			transformed = append(transformed, next)
		default:
			transformed = append(transformed, cloneAIMessage(message))
		}
	}

	result := make([]Message, 0, len(transformed))
	pendingToolCalls := []ContentBlock{}
	existingToolResultIDs := map[string]struct{}{}
	insertSyntheticToolResults := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		for _, toolCall := range pendingToolCalls {
			if _, ok := existingToolResultIDs[toolCall.ID]; ok {
				continue
			}
			result = append(result, Message{
				Role:       "toolResult",
				ToolCallID: toolCall.ID,
				ToolName:   toolCall.Name,
				Content:    NormalizedContent([]ContentBlock{{Type: "text", Text: "No result provided"}}),
				IsError:    true,
			})
		}
		pendingToolCalls = nil
		existingToolResultIDs = map[string]struct{}{}
	}

	for _, message := range transformed {
		switch message.Role {
		case "assistant":
			insertSyntheticToolResults()
			if message.StopReason == "error" || message.StopReason == "aborted" {
				continue
			}
			blocks := messageContentBlocks(message.Content)
			pendingToolCalls = nil
			existingToolResultIDs = map[string]struct{}{}
			for _, block := range blocks {
				if block.Type == "toolCall" {
					pendingToolCalls = append(pendingToolCalls, block)
				}
			}
			result = append(result, cloneAIMessage(message))
		case "toolResult":
			existingToolResultIDs[message.ToolCallID] = struct{}{}
			result = append(result, cloneAIMessage(message))
		case "user":
			insertSyntheticToolResults()
			result = append(result, cloneAIMessage(message))
		default:
			result = append(result, cloneAIMessage(message))
		}
	}

	insertSyntheticToolResults()
	return result
}

func downgradeUnsupportedImages(messages []Message, model Model) []Message {
	if modelSupportsInput(model, "image") {
		return cloneAIMessages(messages)
	}

	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		next := cloneAIMessage(message)
		switch message.Role {
		case "user":
			if blocks, ok := replaceImagesWithPlaceholder(message.Content, nonVisionUserImagePlaceholder); ok {
				next.Content = NormalizedContent(blocks)
			}
		case "toolResult":
			if blocks, ok := replaceImagesWithPlaceholder(message.Content, nonVisionToolImagePlaceholder); ok {
				next.Content = NormalizedContent(blocks)
			}
		}
		out = append(out, next)
	}
	return out
}

func replaceImagesWithPlaceholder(content any, placeholder string) ([]ContentBlock, bool) {
	blocks := ParseContentBlocks(content)
	if len(blocks) == 0 {
		return nil, false
	}
	result := make([]ContentBlock, 0, len(blocks))
	previousWasPlaceholder := false
	changed := false

	for _, block := range blocks {
		if block.Type == "image" {
			changed = true
			if !previousWasPlaceholder {
				result = append(result, ContentBlock{Type: "text", Text: placeholder})
			}
			previousWasPlaceholder = true
			continue
		}

		result = append(result, block)
		previousWasPlaceholder = block.Type == "text" && block.Text == placeholder
	}

	return result, changed
}

func modelSupportsInput(model Model, inputType string) bool {
	for _, supported := range model.Input {
		if strings.EqualFold(strings.TrimSpace(supported), inputType) {
			return true
		}
	}
	return false
}

func toolCallIDNormalizer(model Model) func(string, Model, Message) string {
	switch normalizeAPIName(model.API) {
	case "anthropic-messages", "bedrock-converse-stream", "google-generative-ai", "google-gemini-cli", "google-vertex":
		return normalizeStrictToolCallID
	case "mistral-conversations":
		return createMistralToolCallIDNormalizer()
	case "openai-responses", "azure-openai-responses", "openai-codex-responses":
		return normalizeOpenAIResponsesToolCallID
	case "openai-completions":
		return normalizeOpenAICompletionToolCallID
	default:
		return nil
	}
}

func normalizeStrictToolCallID(raw string, _ Model, _ Message) string {
	return sanitizeToolCallID(raw, 64)
}

func createMistralToolCallIDNormalizer() func(string, Model, Message) string {
	idMap := map[string]string{}
	reverseMap := map[string]string{}

	return func(id string, _ Model, _ Message) string {
		if existing, ok := idMap[id]; ok {
			return existing
		}
		for attempt := 0; ; attempt++ {
			candidate := deriveMistralToolCallID(id, attempt)
			if owner, ok := reverseMap[candidate]; ok && owner != id {
				continue
			}
			idMap[id] = candidate
			reverseMap[candidate] = id
			return candidate
		}
	}
}

func deriveMistralToolCallID(id string, attempt int) string {
	normalized := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, id)
	if attempt == 0 && len(normalized) == 9 {
		return normalized
	}
	seed := normalized
	if seed == "" {
		seed = id
	}
	if attempt > 0 {
		seed = seed + ":" + strconv.Itoa(attempt)
	}
	return truncateString(shortStableHash(seed), 9)
}

func normalizeOpenAICompletionToolCallID(raw string, model Model, _ Message) string {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "|") {
		parts := strings.SplitN(raw, "|", 2)
		return sanitizeToolCallID(parts[0], 40)
	}
	if model.Provider == "openai" {
		return truncateString(raw, 40)
	}
	return raw
}

func normalizeOpenAIResponsesToolCallID(raw string, model Model, source Message) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "|") {
		return sanitizeToolCallID(raw, 64)
	}
	parts := strings.SplitN(raw, "|", 2)
	callID := sanitizeToolCallID(parts[0], 64)
	itemID := parts[1]
	if source.Provider != model.Provider || source.API != model.API {
		itemID = "fc_" + shortStableHash(parts[1])
	} else {
		itemID = sanitizeToolCallID(itemID, 64)
		if !strings.HasPrefix(itemID, "fc_") {
			itemID = sanitizeToolCallID("fc_"+itemID, 64)
		}
	}
	if itemID == "" {
		itemID = "fc_" + shortStableHash(parts[1])
	}
	return callID + "|" + truncateString(itemID, 64)
}

func sanitizeToolCallID(raw string, limit int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	value := strings.Trim(builder.String(), "_")
	if value == "" {
		return ""
	}
	return truncateString(value, limit)
}

func truncateString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}

func shortStableHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func cloneAIMessages(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		out = append(out, cloneAIMessage(message))
	}
	return out
}

func cloneAIMessage(message Message) Message {
	return Message{
		Role:        message.Role,
		Content:     message.Content,
		Provider:    message.Provider,
		API:         message.API,
		Model:       message.Model,
		ToolCallID:  message.ToolCallID,
		ToolName:    message.ToolName,
		IsError:     message.IsError,
		StopReason:  message.StopReason,
		ErrorText:   message.ErrorText,
		ContentList: append([]ContentBlock(nil), message.ContentList...),
	}
}
