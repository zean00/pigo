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

const defaultAnthropicBaseURL = "https://api.anthropic.com"

type anthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	ToolChoice  any                `json:"tool_choice,omitempty"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature *float64           `json:"temperature,omitempty"`
	System      string             `json:"system,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type anthropicResponse struct {
	Content    []map[string]any `json:"content"`
	Usage      anthropicUsage   `json:"usage"`
	StopReason string           `json:"stop_reason"`
}

type anthropicUsage struct {
	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CacheReadInputTokens int `json:"cache_read_input_tokens"`
	CacheCreationTokens  int `json:"cache_creation_input_tokens"`
}

type anthropicProvider struct{}

func AnthropicProvider() ChatProvider {
	return &anthropicProvider{}
}

func (provider *anthropicProvider) Complete(ctx context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	providerSpec, hasProviderSpec := ProviderSpecForProvider(req.Provider)

	apiKey := strings.TrimSpace(req.Options.APIKey)
	if apiKey == "" && hasProviderSpec {
		apiKey, _ = ProviderAPIKey(req.Provider)
	}
	if apiKey == "" {
		providerName := strings.TrimSpace(req.Provider)
		if providerName == "" {
			providerName = "anthropic"
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
		baseURL = defaultAnthropicBaseURL
	}
	baseURL = normalizeAnthropicURL(baseURL)

	payload := toAnthropicRequest(req)
	payload.Stream = req.Options.Stream
	data, err := json.Marshal(payload)
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("marshal anthropic payload: %w", err)
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
		return NormalizedResult{}, nil, fmt.Errorf("create anthropic request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	if useAnthropicOAuthToken(req.Provider, apiKey) {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	} else {
		httpReq.Header.Set("x-api-key", apiKey)
	}
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
		return NormalizedResult{}, nil, fmt.Errorf("call anthropic API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		if len(body) == 0 {
			return NormalizedResult{}, nil, fmt.Errorf("anthropic API error: %s", resp.Status)
		}
		return NormalizedResult{}, nil, fmt.Errorf("anthropic API error: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	if req.Options.Stream {
		return anthropicStreamToResult(resp.Body)
	}

	result, err := anthropicResponseToResult(resp.Body)
	if err != nil {
		return NormalizedResult{}, nil, err
	}
	return result, AssistantEvents(result.contentBlocks(), result.StopReason), nil
}

func anthropicStreamToResult(body io.Reader) (NormalizedResult, []NormalizedEvent, error) {
	blocksByIndex := map[int]map[string]any{}
	stopReason := ""
	usage := &Usage{}

	err := scanSSE(body, func(event sseEvent) error {
		payload := strings.TrimSpace(event.Data)
		if payload == "" || payload == "[DONE]" || event.Event == "ping" {
			return nil
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return fmt.Errorf("parse anthropic stream event: %w", err)
		}
		switch event.Event {
		case "message_start":
			if message, ok := item["message"].(map[string]any); ok {
				if usageMap, ok := message["usage"].(map[string]any); ok {
					usage.Input = int(asFloat64(usageMap["input_tokens"]))
					usage.Output = int(asFloat64(usageMap["output_tokens"]))
					usage.CacheRead = int(asFloat64(usageMap["cache_read_input_tokens"]))
					usage.CacheWrite = int(asFloat64(usageMap["cache_creation_input_tokens"]))
				}
			}
		case "content_block_start":
			index := int(asFloat64(item["index"]))
			block, _ := item["content_block"].(map[string]any)
			if block == nil {
				block = map[string]any{}
			}
			switch asString(block["type"]) {
			case "text":
				blocksByIndex[index] = map[string]any{"type": "text", "text": asString(block["text"])}
			case "thinking":
				blocksByIndex[index] = map[string]any{"type": "thinking", "thinking": asString(block["thinking"])}
			case "tool_use":
				inputMap, _ := block["input"].(map[string]any)
				if inputMap == nil {
					inputMap = map[string]any{}
				}
				blocksByIndex[index] = map[string]any{
					"type":  "tool_use",
					"id":    asString(block["id"]),
					"name":  asString(block["name"]),
					"input": inputMap,
					"json":  "",
				}
			}
		case "content_block_delta":
			index := int(asFloat64(item["index"]))
			block := blocksByIndex[index]
			if block == nil {
				return nil
			}
			delta, _ := item["delta"].(map[string]any)
			switch asString(delta["type"]) {
			case "text_delta":
				block["text"] = asString(block["text"]) + asString(delta["text"])
			case "thinking_delta":
				block["thinking"] = asString(block["thinking"]) + asString(delta["thinking"])
			case "input_json_delta":
				raw := asString(block["json"]) + asString(delta["partial_json"])
				block["json"] = raw
				var input map[string]any
				if strings.TrimSpace(raw) != "" && json.Unmarshal([]byte(raw), &input) == nil {
					block["input"] = input
				}
			}
		case "message_delta":
			delta, _ := item["delta"].(map[string]any)
			if value := asString(delta["stop_reason"]); value != "" {
				stopReason = value
			}
			if usageDelta, ok := item["usage"].(map[string]any); ok {
				if value := int(asFloat64(usageDelta["output_tokens"])); value != 0 {
					usage.Output = value
				}
			}
		}
		return nil
	})
	if err != nil {
		return NormalizedResult{}, nil, err
	}

	content := make([]map[string]any, 0, len(blocksByIndex))
	for index := 0; index < len(blocksByIndex); index++ {
		block := blocksByIndex[index]
		if block == nil {
			continue
		}
		delete(block, "json")
		content = append(content, block)
	}
	result := NormalizedResult{
		Role:       "assistant",
		StopReason: mapAnthropicStopReason(stopReason, len(content) > 0),
		Usage:      usage,
	}
	blocks, hasToolCalls := anthropicContentToBlocks(content)
	result.StopReason = mapAnthropicStopReason(stopReason, hasToolCalls)
	result.Text = ContentText(blocks)
	result.Content = NormalizedContent(blocks)
	if result.Usage != nil && result.Usage.TotalTokens == 0 {
		result.Usage.TotalTokens = result.Usage.Input + result.Usage.Output
	}
	return result, AssistantEvents(blocks, result.StopReason), nil
}

func normalizeAnthropicURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch {
	case strings.HasSuffix(baseURL, "/v1/messages"):
		return baseURL
	case strings.HasSuffix(baseURL, "/v1"):
		return baseURL + "/messages"
	default:
		return baseURL + "/v1/messages"
	}
}

func toAnthropicRequest(req CompletionRequest) anthropicRequest {
	payload := anthropicRequest{
		Model:     req.Model,
		Messages:  toAnthropicMessages(req.Messages),
		MaxTokens: req.Options.MaxTokens,
	}
	if payload.MaxTokens <= 0 {
		payload.MaxTokens = 1024
	}
	if req.Options.Temperature != nil {
		payload.Temperature = req.Options.Temperature
	}
	if len(req.Tools) > 0 {
		payload.Tools = make([]anthropicTool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			parameters := tool.Parameters
			if parameters == nil {
				parameters = map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"additionalProperties": true,
				}
			}
			payload.Tools = append(payload.Tools, anthropicTool{
				Name:        tool.Name,
				Description: strings.TrimSpace(tool.Description),
				InputSchema: parameters,
			})
		}
		toolChoice := strings.TrimSpace(req.Options.ToolChoice)
		if toolChoice == "" {
			toolChoice = "auto"
		}
		switch toolChoice {
		case "auto", "any", "none":
			payload.ToolChoice = map[string]any{"type": toolChoice}
		default:
			payload.ToolChoice = map[string]any{"type": "tool", "name": toolChoice}
		}
	}
	return payload
}

func toAnthropicMessages(messages []Message) []anthropicMessage {
	out := make([]anthropicMessage, 0, len(messages))
	for _, message := range messages {
		switch {
		case strings.EqualFold(message.Role, "user"):
			if content := anthropicUserContent(message); len(content) > 0 {
				out = append(out, anthropicMessage{Role: "user", Content: content})
			}
		case strings.EqualFold(message.Role, "assistant"):
			if content := anthropicAssistantContent(message); len(content) > 0 {
				out = append(out, anthropicMessage{Role: "assistant", Content: content})
			}
		case strings.EqualFold(message.Role, "toolResult"):
			out = append(out, anthropicMessage{Role: "user", Content: []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": strings.TrimSpace(message.ToolCallID),
					"is_error":    message.IsError,
					"content": []map[string]any{{
						"type": "text",
						"text": MessageText(message),
					}},
				},
			}})
		}
	}
	return out
}

func anthropicUserContent(message Message) []any {
	blocks := messageContentBlocks(message.Content)
	if len(blocks) == 0 {
		text := strings.TrimSpace(MessageText(message))
		if text == "" {
			return nil
		}
		return []any{map[string]any{"type": "text", "text": text}}
	}

	content := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				content = append(content, map[string]any{"type": "text", "text": block.Text})
			}
		case "image":
			if strings.TrimSpace(block.Data) != "" && strings.TrimSpace(block.MimeType) != "" {
				content = append(content, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": block.MimeType,
						"data":       block.Data,
					},
				})
			}
		}
	}
	return content
}

func anthropicAssistantContent(message Message) []any {
	blocks := messageContentBlocks(message.Content)
	if len(blocks) == 0 {
		text := strings.TrimSpace(MessageText(message))
		if text == "" {
			return nil
		}
		return []any{map[string]any{"type": "text", "text": text}}
	}

	content := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				content = append(content, map[string]any{"type": "text", "text": block.Text})
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				content = append(content, map[string]any{"type": "thinking", "thinking": block.Thinking})
			}
		case "toolCall":
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    block.ID,
				"name":  block.Name,
				"input": block.Arguments,
			})
		}
	}
	return content
}

func anthropicResponseToResult(body io.Reader) (NormalizedResult, error) {
	var response anthropicResponse
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return NormalizedResult{}, fmt.Errorf("parse anthropic response: %w", err)
	}

	blocks, hasToolCalls := anthropicContentToBlocks(response.Content)
	result := NormalizedResult{
		Role:       "assistant",
		StopReason: mapAnthropicStopReason(response.StopReason, hasToolCalls),
		Text:       ContentText(blocks),
		Content:    NormalizedContent(blocks),
		Usage: &Usage{
			Input:       response.Usage.InputTokens,
			Output:      response.Usage.OutputTokens,
			CacheRead:   response.Usage.CacheReadInputTokens,
			CacheWrite:  response.Usage.CacheCreationTokens,
			TotalTokens: response.Usage.InputTokens + response.Usage.OutputTokens,
		},
	}
	if result.Usage.TotalTokens == 0 {
		result.Usage.TotalTokens = response.Usage.InputTokens + response.Usage.OutputTokens +
			response.Usage.CacheReadInputTokens + response.Usage.CacheCreationTokens
	}
	return result, nil
}

func anthropicContentToBlocks(content []map[string]any) ([]ContentBlock, bool) {
	blocks := make([]ContentBlock, 0, len(content))
	hasToolCalls := false
	for _, item := range content {
		switch asString(item["type"]) {
		case "text":
			text := asString(item["text"])
			if strings.TrimSpace(text) != "" {
				blocks = append(blocks, ContentBlock{Type: "text", Text: text})
			}
		case "thinking":
			thinking := asString(item["thinking"])
			if strings.TrimSpace(thinking) != "" {
				blocks = append(blocks, ContentBlock{Type: "thinking", Thinking: thinking})
			}
		case "tool_use":
			hasToolCalls = true
			arguments, _ := item["input"].(map[string]any)
			if arguments == nil {
				arguments = map[string]any{}
			}
			blocks = append(blocks, ContentBlock{
				Type:      "toolCall",
				ID:        asString(item["id"]),
				Name:      asString(item["name"]),
				Arguments: arguments,
			})
		}
	}
	return blocks, hasToolCalls
}

func mapAnthropicStopReason(reason string, hasToolCalls bool) string {
	switch reason {
	case "tool_use":
		return "toolUse"
	case "max_tokens":
		return "length"
	case "end_turn", "stop_sequence":
		return "stop"
	case "error", "aborted", "failed":
		return "error"
	default:
		if hasToolCalls {
			return "toolUse"
		}
		return "stop"
	}
}

func useAnthropicOAuthToken(provider, apiKey string) bool {
	if canonicalProviderName(provider) != "anthropic" {
		return false
	}
	return strings.TrimSpace(getenv("ANTHROPIC_OAUTH_TOKEN")) == strings.TrimSpace(apiKey) && strings.TrimSpace(apiKey) != ""
}
