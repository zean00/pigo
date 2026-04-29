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
	if req.Options.Stream {
		return NormalizedResult{}, nil, errors.New("streaming is not supported for anthropic providers yet")
	}

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

	result, err := anthropicResponseToResult(resp.Body)
	if err != nil {
		return NormalizedResult{}, nil, err
	}
	return result, AssistantEvents(result.contentBlocks(), result.StopReason), nil
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
