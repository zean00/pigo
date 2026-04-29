package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultMistralBaseURL = "https://api.mistral.ai"

type mistralProvider struct{}

type mistralRequest struct {
	Model       string           `json:"model"`
	Messages    []mistralMessage `json:"messages"`
	Tools       []mistralTool    `json:"tools,omitempty"`
	ToolChoice  any              `json:"tool_choice,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
}

type mistralMessage struct {
	Role       string            `json:"role"`
	Content    any               `json:"content,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Name       string            `json:"name,omitempty"`
	ToolCalls  []mistralToolCall `json:"tool_calls,omitempty"`
}

type mistralTool struct {
	Type     string                `json:"type"`
	Function mistralToolDefinition `json:"function"`
}

type mistralToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

type mistralToolCall struct {
	ID       string                `json:"id,omitempty"`
	Type     string                `json:"type,omitempty"`
	Function mistralToolInvocation `json:"function"`
}

type mistralToolInvocation struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type mistralResponse struct {
	Choices []mistralChoice `json:"choices"`
	Usage   *mistralUsage   `json:"usage,omitempty"`
}

type mistralChoice struct {
	Message      mistralResponseMessage `json:"message"`
	FinishReason string                 `json:"finish_reason"`
}

type mistralResponseMessage struct {
	Role      string            `json:"role"`
	Content   any               `json:"content,omitempty"`
	ToolCalls []mistralToolCall `json:"tool_calls,omitempty"`
}

type mistralUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func MistralProvider() ChatProvider {
	return &mistralProvider{}
}

func (provider *mistralProvider) Complete(ctx context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	providerSpec, hasProviderSpec := ProviderSpecForProvider(req.Provider)
	if req.Options.Stream {
		return NormalizedResult{}, nil, errors.New("streaming is not supported for mistral providers yet")
	}

	apiKey := strings.TrimSpace(req.Options.APIKey)
	if apiKey == "" && hasProviderSpec {
		apiKey, _ = ProviderAPIKey(req.Provider)
	}
	if apiKey == "" {
		providerName := strings.TrimSpace(req.Provider)
		if providerName == "" {
			providerName = "mistral"
		}
		return NormalizedResult{}, nil, fmt.Errorf("missing API key for provider: %s", providerName)
	}
	if strings.TrimSpace(req.Model) == "" {
		return NormalizedResult{}, nil, errors.New("model is required")
	}

	baseURL := strings.TrimSpace(req.Options.BaseURL)
	if baseURL == "" && hasProviderSpec {
		baseURL = strings.TrimSpace(providerSpec.BaseURL)
	}
	if baseURL == "" {
		baseURL = defaultMistralBaseURL
	}
	baseURL = normalizeMistralURL(baseURL)

	payload := mistralRequest{
		Model:    req.Model,
		Messages: toMistralMessages(req.Messages),
		Stream:   false,
	}
	if req.Options.MaxTokens > 0 {
		payload.MaxTokens = req.Options.MaxTokens
	}
	if req.Options.Temperature != nil {
		payload.Temperature = req.Options.Temperature
	}
	if len(req.Tools) > 0 {
		payload.Tools = make([]mistralTool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			parameters := tool.Parameters
			if parameters == nil {
				parameters = map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"additionalProperties": true,
				}
			}
			payload.Tools = append(payload.Tools, mistralTool{
				Type: "function",
				Function: mistralToolDefinition{
					Name:        tool.Name,
					Description: strings.TrimSpace(tool.Description),
					Parameters:  parameters,
					Strict:      false,
				},
			})
		}
		toolChoice := strings.TrimSpace(req.Options.ToolChoice)
		if toolChoice == "" {
			toolChoice = "auto"
		}
		switch toolChoice {
		case "auto", "none", "any", "required":
			payload.ToolChoice = toolChoice
		default:
			payload.ToolChoice = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": toolChoice,
				},
			}
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("marshal mistral payload: %w", err)
	}

	httpClient := req.Options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if req.Options.Timeout > 0 {
		cloned := *httpClient
		cloned.Timeout = req.Options.Timeout
		httpClient = &cloned
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(data))
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("create mistral request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	if hasProviderSpec {
		for key, value := range providerSpec.DefaultHeader {
			httpReq.Header.Set(key, value)
		}
	}
	for key, value := range req.Options.Headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: err.Error()}, nil, err
		}
		return NormalizedResult{}, nil, fmt.Errorf("call mistral API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		if len(body) == 0 {
			return NormalizedResult{}, nil, fmt.Errorf("mistral API error: %s", resp.Status)
		}
		return NormalizedResult{}, nil, fmt.Errorf("mistral API error: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var completion mistralResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("parse mistral response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return NormalizedResult{}, nil, errors.New("mistral response missing choices")
	}

	result := mistralChoiceToNormalized(completion.Choices[0])
	if completion.Usage != nil {
		result.Usage = &Usage{
			Input:       completion.Usage.PromptTokens,
			Output:      completion.Usage.CompletionTokens,
			TotalTokens: completion.Usage.TotalTokens,
		}
	}
	return result, AssistantEvents(result.contentBlocks(), result.StopReason), nil
}

func normalizeMistralURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch {
	case strings.HasSuffix(baseURL, "/v1/chat/completions"):
		return baseURL
	case strings.HasSuffix(baseURL, "/v1"):
		return baseURL + "/chat/completions"
	default:
		return baseURL + "/v1/chat/completions"
	}
}

func toMistralMessages(messages []Message) []mistralMessage {
	out := make([]mistralMessage, 0, len(messages))
	for _, message := range messages {
		switch {
		case strings.EqualFold(message.Role, "user"):
			content := mistralUserContent(message)
			if content == nil {
				continue
			}
			out = append(out, mistralMessage{Role: "user", Content: content})
		case strings.EqualFold(message.Role, "assistant"):
			assistant := mistralAssistantMessage(message)
			if assistant.Content == nil && len(assistant.ToolCalls) == 0 {
				continue
			}
			out = append(out, assistant)
		case strings.EqualFold(message.Role, "toolResult"):
			out = append(out, mistralMessage{
				Role:       "tool",
				ToolCallID: strings.TrimSpace(message.ToolCallID),
				Name:       strings.TrimSpace(message.ToolName),
				Content:    mistralToolResultContent(message),
			})
		}
	}
	return out
}

func mistralUserContent(message Message) any {
	blocks := messageContentBlocks(message.Content)
	if len(blocks) == 0 {
		text := strings.TrimSpace(MessageText(message))
		if text == "" {
			return nil
		}
		return text
	}

	content := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				content = append(content, map[string]any{"type": "text", "text": block.Text})
			}
		case "image":
			if strings.TrimSpace(block.Data) != "" && strings.TrimSpace(block.MimeType) != "" {
				content = append(content, map[string]any{
					"type":      "image_url",
					"image_url": "data:" + block.MimeType + ";base64," + block.Data,
				})
			}
		}
	}
	if len(content) == 1 && content[0]["type"] == "text" {
		return content[0]["text"]
	}
	if len(content) == 0 {
		return nil
	}
	return content
}

func mistralAssistantMessage(message Message) mistralMessage {
	blocks := messageContentBlocks(message.Content)
	content := make([]map[string]any, 0, len(blocks))
	toolCalls := make([]mistralToolCall, 0)
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				content = append(content, map[string]any{"type": "text", "text": block.Text})
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				content = append(content, map[string]any{
					"type": "thinking",
					"thinking": []map[string]any{{
						"type": "text",
						"text": block.Thinking,
					}},
				})
			}
		case "toolCall":
			arguments := "{}"
			if marshaled, err := json.Marshal(block.Arguments); err == nil {
				arguments = string(marshaled)
			}
			toolCalls = append(toolCalls, mistralToolCall{
				ID:   normalizeMistralToolCallID(block.ID),
				Type: "function",
				Function: mistralToolInvocation{
					Name:      block.Name,
					Arguments: arguments,
				},
			})
		}
	}

	assistant := mistralMessage{Role: "assistant", ToolCalls: toolCalls}
	if len(content) == 1 && content[0]["type"] == "text" {
		assistant.Content = content[0]["text"]
	} else if len(content) > 0 {
		assistant.Content = content
	}
	return assistant
}

func mistralToolResultContent(message Message) any {
	blocks := messageContentBlocks(message.Content)
	content := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				content = append(content, map[string]any{"type": "text", "text": block.Text})
			}
		case "image":
			if strings.TrimSpace(block.Data) != "" && strings.TrimSpace(block.MimeType) != "" {
				content = append(content, map[string]any{
					"type":      "image_url",
					"image_url": "data:" + block.MimeType + ";base64," + block.Data,
				})
			}
		}
	}
	if len(content) == 0 {
		return "(no tool output)"
	}
	if len(content) == 1 && content[0]["type"] == "text" {
		return content[0]["text"]
	}
	return content
}

func mistralChoiceToNormalized(choice mistralChoice) NormalizedResult {
	blocks := make([]ContentBlock, 0)
	appendText := func(text string) {
		if strings.TrimSpace(text) != "" {
			blocks = append(blocks, ContentBlock{Type: "text", Text: text})
		}
	}

	switch content := choice.Message.Content.(type) {
	case string:
		appendText(content)
	case []any:
		for _, item := range content {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch asString(itemMap["type"]) {
			case "text":
				appendText(asString(itemMap["text"]))
			case "thinking":
				thinkingParts, _ := itemMap["thinking"].([]any)
				var builder strings.Builder
				for _, part := range thinkingParts {
					partMap, ok := part.(map[string]any)
					if !ok {
						continue
					}
					builder.WriteString(asString(partMap["text"]))
				}
				if strings.TrimSpace(builder.String()) != "" {
					blocks = append(blocks, ContentBlock{Type: "thinking", Thinking: builder.String()})
				}
			}
		}
	}

	for _, toolCall := range choice.Message.ToolCalls {
		arguments := map[string]any{}
		if strings.TrimSpace(toolCall.Function.Arguments) != "" {
			_ = json.Unmarshal([]byte(toolCall.Function.Arguments), &arguments)
		}
		blocks = append(blocks, ContentBlock{
			Type:      "toolCall",
			ID:        toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: arguments,
		})
	}

	return NormalizedResult{
		Role:       "assistant",
		StopReason: mapMistralStopReason(choice.FinishReason),
		Text:       ContentText(blocks),
		Content:    NormalizedContent(blocks),
	}
}

func normalizeMistralToolCallID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var sanitized strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sanitized.WriteRune(r)
		}
	}
	value := sanitized.String()
	if len(value) > 9 {
		value = value[:9]
	}
	return value
}

func mapMistralStopReason(reason string) string {
	switch reason {
	case "tool_calls":
		return "toolUse"
	case "length", "model_length":
		return "length"
	case "error":
		return "error"
	case "", "stop":
		return "stop"
	default:
		return "stop"
	}
}
