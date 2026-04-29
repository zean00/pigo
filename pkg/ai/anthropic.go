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
	System      []map[string]any   `json:"system,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
	Metadata    map[string]any     `json:"metadata,omitempty"`
}

type anthropicTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema"`
	CacheControl map[string]any `json:"cache_control,omitempty"`
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
	payloadValue, err := applyPayloadHook(req, payload)
	if err != nil {
		return NormalizedResult{}, nil, err
	}
	data, err := json.Marshal(payload)
	if payloadValue != nil {
		data, err = json.Marshal(payloadValue)
	}
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

	buildRequest := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("create anthropic request: %w", err)
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
		return httpReq, nil
	}

	maxRetries := retryLimit(req.Options, defaultProviderMaxRetries)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		httpReq, err := buildRequest()
		if err != nil {
			return NormalizedResult{}, nil, err
		}
		resp, err := httpClient.Do(httpReq)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: err.Error()}, nil, err
			}
			if attempt < maxRetries && shouldRetryHTTPError(err) {
				if sleepErr := sleepWithContext(ctx, retryDelayForAttempt(attempt, 0, defaultProviderBaseDelay)); sleepErr != nil {
					return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: sleepErr.Error()}, nil, sleepErr
				}
				continue
			}
			return NormalizedResult{}, nil, fmt.Errorf("call anthropic API: %w", err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if hookErr := notifyResponseHook(req, resp); hookErr != nil {
				_ = resp.Body.Close()
				return NormalizedResult{}, nil, hookErr
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			errorText := strings.TrimSpace(string(body))
			if attempt < maxRetries && shouldRetryHTTPStatus(resp.StatusCode, errorText) {
				serverDelay := retryAfterDelay(resp, errorText)
				if err := validateRetryDelay(req.Options, serverDelay); err != nil {
					return NormalizedResult{}, nil, err
				}
				if sleepErr := sleepWithContext(ctx, retryDelayForAttempt(attempt, serverDelay, defaultProviderBaseDelay)); sleepErr != nil {
					return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: sleepErr.Error()}, nil, sleepErr
				}
				continue
			}
			if errorText == "" {
				return NormalizedResult{}, nil, fmt.Errorf("anthropic API error: %s", resp.Status)
			}
			return NormalizedResult{}, nil, fmt.Errorf("anthropic API error: %s: %s", resp.Status, errorText)
		}
		if hookErr := notifyResponseHook(req, resp); hookErr != nil {
			_ = resp.Body.Close()
			return NormalizedResult{}, nil, hookErr
		}

		if req.Options.Stream {
			result, events, streamErr := anthropicStreamToResult(resp.Body)
			_ = resp.Body.Close()
			if streamErr == nil {
				return result, events, nil
			}
			if attempt < maxRetries && isRetriableStreamError(streamErr) {
				if sleepErr := sleepWithContext(ctx, retryDelayForAttempt(attempt, 0, defaultProviderBaseDelay)); sleepErr != nil {
					return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: sleepErr.Error()}, nil, sleepErr
				}
				continue
			}
			return NormalizedResult{}, nil, streamErr
		}

		result, responseErr := anthropicResponseToResult(resp.Body)
		_ = resp.Body.Close()
		if responseErr != nil {
			return NormalizedResult{}, nil, responseErr
		}
		return result, AssistantEvents(result.contentBlocks(), result.StopReason), nil
	}

	return NormalizedResult{}, nil, errors.New("anthropic retry budget exhausted")
}

func anthropicStreamToResult(body io.Reader) (NormalizedResult, []NormalizedEvent, error) {
	blocksByIndex := map[int]map[string]any{}
	stopReason := ""
	usage := &Usage{}
	events := []NormalizedEvent{{Type: "start"}}

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
				events = append(events, NormalizedEvent{Type: "text_start", ContentIdx: index})
				if text := asString(block["text"]); text != "" {
					events = append(events, NormalizedEvent{Type: "text_delta", ContentIdx: index, Delta: text})
				}
			case "thinking":
				blocksByIndex[index] = map[string]any{"type": "thinking", "thinking": asString(block["thinking"])}
				events = append(events, NormalizedEvent{Type: "thinking_start", ContentIdx: index})
				if thinking := asString(block["thinking"]); thinking != "" {
					events = append(events, NormalizedEvent{Type: "thinking_delta", ContentIdx: index, Delta: thinking})
				}
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
				events = append(events, NormalizedEvent{Type: "toolcall_start", ContentIdx: index})
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
				value := asString(delta["text"])
				block["text"] = asString(block["text"]) + value
				if value != "" {
					events = append(events, NormalizedEvent{Type: "text_delta", ContentIdx: index, Delta: value})
				}
			case "thinking_delta":
				value := asString(delta["thinking"])
				block["thinking"] = asString(block["thinking"]) + value
				if value != "" {
					events = append(events, NormalizedEvent{Type: "thinking_delta", ContentIdx: index, Delta: value})
				}
			case "input_json_delta":
				value := asString(delta["partial_json"])
				raw := asString(block["json"]) + value
				block["json"] = raw
				var input map[string]any
				if strings.TrimSpace(raw) != "" && json.Unmarshal([]byte(raw), &input) == nil {
					block["input"] = input
				}
				if value != "" {
					events = append(events, NormalizedEvent{Type: "toolcall_delta", ContentIdx: index, Delta: value})
				}
			}
		case "content_block_stop":
			index := int(asFloat64(item["index"]))
			block := blocksByIndex[index]
			switch asString(block["type"]) {
			case "text":
				events = append(events, NormalizedEvent{Type: "text_end", ContentIdx: index, Content: asString(block["text"])})
			case "thinking":
				events = append(events, NormalizedEvent{Type: "thinking_end", ContentIdx: index, Content: asString(block["thinking"])})
			case "tool_use":
				arguments, _ := block["input"].(map[string]any)
				events = append(events, NormalizedEvent{
					Type:       "toolcall_end",
					ContentIdx: index,
					ToolCall: &NormalizedTool{
						Name:      asString(block["name"]),
						Arguments: arguments,
						HasID:     asString(block["id"]) != "",
					},
				})
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
	return result, appendTerminalEvent(events, result.StopReason), nil
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
	cacheControl := anthropicCacheControl(req.Options)
	payload := anthropicRequest{
		Model:     req.Model,
		Messages:  toAnthropicMessages(req.Messages, cacheControl),
		MaxTokens: req.Options.MaxTokens,
	}
	if payload.MaxTokens <= 0 {
		payload.MaxTokens = 1024
	}
	if req.Options.Temperature != nil {
		payload.Temperature = req.Options.Temperature
	}
	if userID := strings.TrimSpace(asString(req.Options.Metadata["user_id"])); userID != "" {
		payload.Metadata = map[string]any{"user_id": userID}
	}
	if prompt := strings.TrimSpace(extractSystemPrompt(req.Messages)); prompt != "" {
		systemBlock := map[string]any{
			"type": "text",
			"text": prompt,
		}
		if cacheControl != nil {
			systemBlock["cache_control"] = cacheControl
		}
		payload.System = []map[string]any{systemBlock}
	}
	if len(req.Tools) > 0 {
		payload.Tools = make([]anthropicTool, 0, len(req.Tools))
		for index, tool := range req.Tools {
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
				CacheControl: func() map[string]any {
					if cacheControl != nil && index == len(req.Tools)-1 {
						return cacheControl
					}
					return nil
				}(),
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

func toAnthropicMessages(messages []Message, cacheControl map[string]any) []anthropicMessage {
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
	if cacheControl != nil && len(out) > 0 {
		last := &out[len(out)-1]
		if strings.EqualFold(last.Role, "user") && len(last.Content) > 0 {
			if block, ok := last.Content[len(last.Content)-1].(map[string]any); ok {
				block["cache_control"] = cacheControl
			}
		}
	}
	return out
}

func anthropicCacheControl(options ChatOptions) map[string]any {
	retention := resolveCacheRetention(options)
	if retention == CacheRetentionNone {
		return nil
	}
	cacheControl := map[string]any{"type": "ephemeral"}
	if retention == CacheRetentionLong {
		cacheControl["ttl"] = "1h"
	}
	return cacheControl
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
