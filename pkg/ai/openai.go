package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
)

const (
	openAIProviderName     = "openai"
	defaultOpenAIBaseURL   = "https://api.openai.com/v1"
	defaultAzureAPIVersion = "v1"
)

type openAIRequest struct {
	Model                string           `json:"model"`
	Messages             []openAIMessage  `json:"messages"`
	MaxTokens            int              `json:"max_tokens,omitempty"`
	Temperature          *float64         `json:"temperature,omitempty"`
	Tools                []openAIChatTool `json:"tools,omitempty"`
	ToolChoice           string           `json:"tool_choice,omitempty"`
	Stream               bool             `json:"stream,omitempty"`
	PromptCacheKey       string           `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string           `json:"prompt_cache_retention,omitempty"`
}

type openAIResponse struct {
	ID      string         `json:"id,omitempty"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

type openAIChoice struct {
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	ToolCalls  []openAICallSpec `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIChatTool struct {
	Type     string                   `json:"type"`
	Function openAIFunctionDescriptor `json:"function"`
}

type openAIFunctionDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type openAICallSpec struct {
	ID       string          `json:"id,omitempty"`
	Type     string          `json:"type,omitempty"`
	Function openAIArguments `json:"function"`
	Index    int             `json:"index,omitempty"`
}

type openAIArguments struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type openAIStreamChunk struct {
	ID      string               `json:"id,omitempty"`
	Choices []openAIStreamChoice `json:"choices"`
}

type openAIStreamChoice struct {
	Delta        openAIMessage `json:"delta"`
	FinishReason string        `json:"finish_reason"`
}

type openAIResponsesRequest struct {
	Model                string           `json:"model"`
	Input                []any            `json:"input"`
	Tools                []openAIChatTool `json:"tools,omitempty"`
	ToolChoice           string           `json:"tool_choice,omitempty"`
	MaxOutputTokens      int              `json:"max_output_tokens,omitempty"`
	Temperature          *float64         `json:"temperature,omitempty"`
	Stream               bool             `json:"stream,omitempty"`
	PromptCacheKey       string           `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string           `json:"prompt_cache_retention,omitempty"`
}

type openAIResponsesResponse struct {
	ID     string                `json:"id,omitempty"`
	Output []any                 `json:"output"`
	Usage  *openAIResponsesUsage `json:"usage"`
	Status string                `json:"status"`
}

type openAIResponsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	PromptTokens       int `json:"prompt_tokens"`
	CompletionTokens   int `json:"completion_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details,omitempty"`
}

func splitToolCallID(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if strings.Contains(raw, "|") {
		parts := strings.SplitN(raw, "|", 2)
		callID := normalizeOpenAIResponseID(parts[0])
		if callID == "" {
			return "", ""
		}
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			return callID, ""
		}
		return callID, normalizeOpenAIResponseItemID(parts[1])
	}
	return normalizeOpenAIResponseID(raw), ""
}

func normalizeOpenAIResponseID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var sanitized strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sanitized.WriteRune(r)
		} else {
			sanitized.WriteRune('_')
		}
	}
	value := strings.Trim(sanitized.String(), "_")
	if value == "" {
		return ""
	}
	if len(value) > 64 {
		return value[:64]
	}
	return value
}

func normalizeOpenAIResponseItemID(raw string) string {
	itemID := normalizeOpenAIResponseID(raw)
	if itemID == "" {
		return ""
	}
	if strings.HasPrefix(itemID, "fc_") {
		return itemID
	}
	itemID = "fc_" + itemID
	if len(itemID) > 64 {
		return itemID[:64]
	}
	return itemID
}

func inferCopilotInitiator(messages []Message) string {
	if len(messages) == 0 {
		return "user"
	}
	lastMessage := messages[len(messages)-1]
	if strings.TrimSpace(lastMessage.Role) == "user" {
		return "user"
	}
	return "agent"
}

func hasCopilotVisionInput(messages []Message) bool {
	for _, message := range messages {
		if message.Role != "user" && message.Role != "toolResult" {
			continue
		}
		for _, block := range messageContentBlocks(message.Content) {
			if block.Type == "image" && strings.TrimSpace(block.Data) != "" {
				return true
			}
		}
	}
	return false
}

func buildCopilotDynamicHeaders(messages []Message) map[string]string {
	headers := map[string]string{
		"X-Initiator":   inferCopilotInitiator(messages),
		"Openai-Intent": "conversation-edits",
	}
	if hasCopilotVisionInput(messages) {
		headers["Copilot-Vision-Request"] = "true"
	}
	return headers
}

func mergeOpenAIResponsesToolCallID(callID, itemID string) string {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return ""
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return callID
	}
	return callID + "|" + itemID
}

type openAIProvider struct {
	defaultBaseURL string
}

type openAIStreamingCall struct {
	ID        string
	Name      string
	Arguments bytes.Buffer
}

func init() {
	provider := OpenAIProvider()
	anthropicProvider := AnthropicProvider()
	googleProvider := GoogleProvider()
	googleCLIProvider := GoogleGeminiCLIProvider()
	googleVertexProvider := GoogleVertexProvider()
	bedrockProvider := BedrockProvider()
	mistralProvider := MistralProvider()
	codexProvider := OpenAICodexProvider()
	RegisterProvider(openAIProviderName, provider)
	for providerName, spec := range providerSpecs {
		if spec.Mode == providerModeOpenAI {
			RegisterProvider(providerName, provider)
			continue
		}
		if spec.Mode == providerModeBedrock {
			RegisterProvider(providerName, bedrockProvider)
			continue
		}
		if spec.Mode == providerModeAnthropic {
			RegisterProvider(providerName, anthropicProvider)
			continue
		}
		if spec.Mode == providerModeGoogle {
			RegisterProvider(providerName, googleProvider)
			continue
		}
		if spec.Mode == providerModeGoogleCLI {
			RegisterProvider(providerName, googleCLIProvider)
			continue
		}
		if spec.Mode == providerModeGoogleVertex {
			RegisterProvider(providerName, googleVertexProvider)
			continue
		}
		if spec.Mode == providerModeMistral {
			RegisterProvider(providerName, mistralProvider)
			continue
		}
		if spec.Mode == providerModeCodex {
			RegisterProvider(providerName, codexProvider)
			continue
		}
		RegisterProvider(providerName, unsupportedProviderFor(providerName))
	}
	for alias := range providerAliases {
		spec, ok := ProviderSpecForProvider(alias)
		if !ok {
			continue
		}
		if spec.Mode == providerModeOpenAI {
			RegisterProvider(alias, provider)
			continue
		}
		if spec.Mode == providerModeBedrock {
			RegisterProvider(alias, bedrockProvider)
			continue
		}
		if spec.Mode == providerModeAnthropic {
			RegisterProvider(alias, anthropicProvider)
			continue
		}
		if spec.Mode == providerModeGoogle {
			RegisterProvider(alias, googleProvider)
			continue
		}
		if spec.Mode == providerModeGoogleCLI {
			RegisterProvider(alias, googleCLIProvider)
			continue
		}
		if spec.Mode == providerModeGoogleVertex {
			RegisterProvider(alias, googleVertexProvider)
			continue
		}
		if spec.Mode == providerModeMistral {
			RegisterProvider(alias, mistralProvider)
			continue
		}
		if spec.Mode == providerModeCodex {
			RegisterProvider(alias, codexProvider)
			continue
		}
		RegisterProvider(alias, unsupportedProviderFor(spec.Name))
	}
}

func OpenAIProvider(opts ...OpenAIProviderOption) ChatProvider {
	provider := &openAIProvider{defaultBaseURL: defaultOpenAIBaseURL}
	for _, option := range opts {
		option(provider)
	}
	return provider
}

type OpenAIProviderOption func(provider *openAIProvider)

func WithOpenAIBaseURL(baseURL string) OpenAIProviderOption {
	return func(provider *openAIProvider) {
		baseURL = strings.TrimSpace(baseURL)
		if baseURL != "" {
			provider.defaultBaseURL = baseURL
		}
	}
}

func (provider *openAIProvider) Complete(ctx context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	providerSpec, hasProviderSpec := ProviderSpecForProvider(req.Provider)
	useResponsesAPI := shouldUseOpenAIResponses(req.Provider, req.Model)
	apiKey := strings.TrimSpace(req.Options.APIKey)
	if apiKey == "" && hasProviderSpec {
		apiKey, _ = ProviderAPIKey(req.Provider)
	}
	if apiKey == "" {
		providerName := strings.TrimSpace(req.Provider)
		if providerName == "" {
			providerName = openAIProviderName
		}
		return NormalizedResult{}, nil, fmt.Errorf("missing API key for provider: %s", providerName)
	}
	if strings.TrimSpace(req.Model) == "" {
		return NormalizedResult{}, nil, errors.New("model is required")
	}

	baseURL := strings.TrimSpace(req.Options.BaseURL)
	if baseURL == "" {
		if hasProviderSpec && providerSpec.Name == "azure-openai-responses" {
			resolved, err := resolveAzureOpenAIBaseURL()
			if err != nil {
				return NormalizedResult{}, nil, err
			}
			baseURL = resolved
		}
		if hasProviderSpec && providerSpec.Name == openAIProviderName {
			baseURL = strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
		}
		if baseURL == "" && provider.defaultBaseURL != "" && strings.TrimSpace(provider.defaultBaseURL) != defaultOpenAIBaseURL {
			baseURL = strings.TrimSpace(provider.defaultBaseURL)
		}
		if baseURL == "" && hasProviderSpec {
			baseURL = strings.TrimSpace(providerSpec.BaseURL)
		}
		if baseURL == "" {
			baseURL = strings.TrimSpace(provider.defaultBaseURL)
		}
	}
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if useResponsesAPI {
		if !strings.HasSuffix(baseURL, "/responses") {
			baseURL = baseURL + "/responses"
		}
		if hasProviderSpec && providerSpec.Name == "azure-openai-responses" {
			baseURL = ensureAzureOpenAIAPIVersion(baseURL)
		}
	} else if !strings.HasSuffix(baseURL, "/chat/completions") {
		baseURL = baseURL + "/chat/completions"
	}

	var requestBody any
	var data []byte
	var err error
	if useResponsesAPI {
		payload := toOpenAIResponsesRequest(req)
		requestBody = payload
	} else {
		payload := openAIRequest{
			Model:    req.Model,
			Messages: toOpenAIMessages(req.Messages),
			Stream:   req.Options.Stream,
		}
		if sessionID := cacheSessionID(req.Options); sessionID != "" {
			payload.PromptCacheKey = sessionID
			payload.PromptCacheRetention = cacheRetentionValue(req.Options)
		}
		if req.Options.MaxTokens > 0 {
			payload.MaxTokens = req.Options.MaxTokens
		}
		if req.Options.Temperature != nil {
			payload.Temperature = req.Options.Temperature
		}
		if len(req.Tools) > 0 {
			payload.Tools = make([]openAIChatTool, 0, len(req.Tools))
			payload.ToolChoice = strings.TrimSpace(req.Options.ToolChoice)
			if payload.ToolChoice == "" {
				payload.ToolChoice = "auto"
			}
			for _, tool := range req.Tools {
				parameters := tool.Parameters
				if parameters == nil {
					parameters = map[string]any{
						"type":                 "object",
						"properties":           map[string]any{},
						"additionalProperties": true,
					}
				}
				payload.Tools = append(payload.Tools, openAIChatTool{
					Type: "function",
					Function: openAIFunctionDescriptor{
						Name:        tool.Name,
						Description: strings.TrimSpace(tool.Description),
						Parameters:  parameters,
					},
				})
			}
		}
		requestBody = payload
	}

	requestBody, err = applyPayloadHook(req, requestBody)
	if err != nil {
		return NormalizedResult{}, nil, err
	}

	data, err = json.Marshal(requestBody)
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("marshal openai payload: %w", err)
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
			return nil, fmt.Errorf("create openai request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if useResponsesAPI && req.Options.Stream {
			httpReq.Header.Set("Accept", "text/event-stream")
		}
		if sessionID := cacheSessionID(req.Options); sessionID != "" {
			httpReq.Header.Set("session_id", sessionID)
			httpReq.Header.Set("x-client-request-id", sessionID)
			httpReq.Header.Set("x-session-affinity", sessionID)
		}
		if hasProviderSpec {
			for key, value := range providerSpec.DefaultHeader {
				httpReq.Header.Set(key, value)
			}
		}
		if hasProviderSpec && providerSpec.Name == "github-copilot" {
			for key, value := range buildCopilotDynamicHeaders(req.Messages) {
				httpReq.Header.Set(key, value)
			}
		}
		for key, value := range req.Options.Headers {
			httpReq.Header.Set(key, value)
		}
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
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
				delay := retryDelayForAttempt(attempt, 0, defaultProviderBaseDelay)
				if sleepErr := sleepWithContext(ctx, delay); sleepErr != nil {
					return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: sleepErr.Error()}, nil, sleepErr
				}
				continue
			}
			return NormalizedResult{}, nil, fmt.Errorf("call openai API: %w", err)
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
				return NormalizedResult{}, nil, fmt.Errorf("openai API error: %s", resp.Status)
			}
			return NormalizedResult{}, nil, fmt.Errorf("openai API error: %s: %s", resp.Status, errorText)
		}
		if hookErr := notifyResponseHook(req, resp); hookErr != nil {
			_ = resp.Body.Close()
			return NormalizedResult{}, nil, hookErr
		}

		if req.Options.Stream && !useResponsesAPI {
			result, events, streamErr := openAIStreamToResult(resp.Body)
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

		if useResponsesAPI {
			if req.Options.Stream {
				result, events, streamErr := openAIResponsesSSEToResult(resp.Body)
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
			result, events, responseErr := openAIResponsesToResult(resp.Body)
			_ = resp.Body.Close()
			if responseErr != nil {
				return NormalizedResult{}, nil, responseErr
			}
			return result, events, nil
		}

		var completion openAIResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&completion)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return NormalizedResult{}, nil, fmt.Errorf("parse openai response: %w", decodeErr)
		}
		if len(completion.Choices) == 0 {
			return NormalizedResult{Role: "assistant", StopReason: "error", ErrorMessage: "openai response missing choices"}, nil,
				errors.New("openai response missing choices")
		}

		result := openAIChoiceToNormalized(completion.Choices[0])
		result.ResponseID = completion.ID
		if completion.Usage != nil {
			result.Usage = &Usage{
				Input:       completion.Usage.PromptTokens,
				Output:      completion.Usage.CompletionTokens,
				CacheRead:   0,
				CacheWrite:  0,
				TotalTokens: completion.Usage.TotalTokens,
			}
		}
		return result, AssistantEvents(result.contentBlocks(), result.StopReason), nil
	}

	return NormalizedResult{}, nil, errors.New("openai retry budget exhausted")
}

func shouldUseOpenAIResponses(provider, model string) bool {
	if strings.TrimSpace(model) == "" {
		return false
	}
	if strings.TrimSpace(provider) != "" {
		provider = strings.TrimSpace(strings.ToLower(provider))
		if provider != openAIProviderName && provider != "azure-openai-responses" {
			return false
		}
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(model, "gpt-5") || strings.HasPrefix(model, "oswe") {
		return true
	}
	return false
}

func toOpenAIResponsesRequest(req CompletionRequest) openAIResponsesRequest {
	payload := openAIResponsesRequest{
		Model:  req.Model,
		Input:  make([]any, 0, len(req.Messages)*2),
		Stream: req.Options.Stream,
	}
	if sessionID := cacheSessionID(req.Options); sessionID != "" {
		payload.PromptCacheKey = sessionID
		payload.PromptCacheRetention = cacheRetentionValue(req.Options)
	}
	if req.Options.MaxTokens > 0 {
		payload.MaxOutputTokens = req.Options.MaxTokens
	}
	if req.Options.Temperature != nil {
		payload.Temperature = req.Options.Temperature
	}

	for _, message := range req.Messages {
		switch message.Role {
		case "user":
			parts := messageContentBlocks(message.Content)
			content := make([]string, 0, len(parts))
			for _, block := range parts {
				if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
					content = append(content, block.Text)
				}
			}
			if len(content) == 0 {
				contentText := MessageText(message)
				if strings.TrimSpace(contentText) != "" {
					content = append(content, contentText)
				}
			}
			joined := strings.Join(content, "")
			if strings.TrimSpace(joined) == "" {
				continue
			}
			payload.Input = append(payload.Input, map[string]any{
				"role": "user",
				"content": []map[string]any{{
					"type": "input_text",
					"text": joined,
				}},
			})

		case "assistant":
			text := ""
			for _, block := range messageContentBlocks(message.Content) {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						if text == "" {
							text = block.Text
						} else {
							text += block.Text
						}
					}
				case "toolCall":
					callID, itemID := splitToolCallID(block.ID)
					if callID == "" {
						continue
					}
					arguments := "{}"
					if marshaled, err := json.Marshal(block.Arguments); err == nil {
						arguments = string(marshaled)
					}
					item := map[string]any{
						"type":      "function_call",
						"call_id":   callID,
						"name":      block.Name,
						"arguments": arguments,
					}
					if strings.TrimSpace(itemID) != "" {
						item["id"] = itemID
					}
					payload.Input = append(payload.Input, item)
				}
			}
			if strings.TrimSpace(text) != "" {
				payload.Input = append(payload.Input, map[string]any{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{{
						"type": "output_text",
						"text": text,
					}},
				})
			}

		case "toolResult":
			callID, _ := splitToolCallID(message.ToolCallID)
			if callID == "" {
				continue
			}
			payload.Input = append(payload.Input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  MessageText(message),
			})
		}
	}

	if len(req.Tools) > 0 {
		payload.Tools = make([]openAIChatTool, 0, len(req.Tools))
		payload.ToolChoice = strings.TrimSpace(req.Options.ToolChoice)
		if payload.ToolChoice == "" {
			payload.ToolChoice = "auto"
		}
		for _, tool := range req.Tools {
			parameters := tool.Parameters
			if parameters == nil {
				parameters = map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"additionalProperties": true,
				}
			}
			payload.Tools = append(payload.Tools, openAIChatTool{
				Type: "function",
				Function: openAIFunctionDescriptor{
					Name:        tool.Name,
					Description: strings.TrimSpace(tool.Description),
					Parameters:  parameters,
				},
			})
		}
	}
	return payload
}

func openAIResponsesSSEToResult(body io.Reader) (NormalizedResult, []NormalizedEvent, error) {
	return openAIResponsesEventsToResult(func(handle func(map[string]any) error) error {
		return scanSSE(body, func(event sseEvent) error {
			payload := strings.TrimSpace(event.Data)
			if payload == "" || payload == "[DONE]" {
				return nil
			}
			var item map[string]any
			if err := json.Unmarshal([]byte(payload), &item); err != nil {
				return fmt.Errorf("parse openai responses stream event: %w", err)
			}
			return handle(item)
		})
	})
}

func openAIResponsesEventsToResult(iterate func(func(map[string]any) error) error) (NormalizedResult, []NormalizedEvent, error) {
	blocks := []ContentBlock{}
	rawToolArgs := map[int]string{}
	currentTextIndex := -1
	currentThinkingIndex := -1
	currentToolIndex := -1
	stopReason := "stop"
	result := NormalizedResult{Role: "assistant", StopReason: "stop"}
	events := []NormalizedEvent{{Type: "start"}}

	err := iterate(func(item map[string]any) error {
		switch asString(item["type"]) {
		case "response.output_item.added":
			outputItem, _ := item["item"].(map[string]any)
			switch asString(outputItem["type"]) {
			case "reasoning":
				blocks = append(blocks, ContentBlock{Type: "thinking"})
				currentThinkingIndex = len(blocks) - 1
				events = append(events, NormalizedEvent{Type: "thinking_start", ContentIdx: currentThinkingIndex})
			case "message":
				blocks = append(blocks, ContentBlock{Type: "text"})
				currentTextIndex = len(blocks) - 1
				events = append(events, NormalizedEvent{Type: "text_start", ContentIdx: currentTextIndex})
			case "function_call":
				blocks = append(blocks, ContentBlock{
					Type:      "toolCall",
					ID:        mergeOpenAIResponsesToolCallID(asString(outputItem["call_id"]), asString(outputItem["id"])),
					Name:      asString(outputItem["name"]),
					Arguments: map[string]any{},
				})
				currentToolIndex = len(blocks) - 1
				rawToolArgs[currentToolIndex] = asString(outputItem["arguments"])
				events = append(events, NormalizedEvent{Type: "toolcall_start", ContentIdx: currentToolIndex})
			}
		case "response.reasoning_summary_text.delta":
			if currentThinkingIndex >= 0 && currentThinkingIndex < len(blocks) {
				delta := asString(item["delta"])
				blocks[currentThinkingIndex].Thinking += delta
				if delta != "" {
					events = append(events, NormalizedEvent{Type: "thinking_delta", ContentIdx: currentThinkingIndex, Delta: delta})
				}
			}
		case "response.output_text.delta", "response.refusal.delta":
			if currentTextIndex >= 0 && currentTextIndex < len(blocks) {
				delta := asString(item["delta"])
				blocks[currentTextIndex].Text += delta
				if delta != "" {
					events = append(events, NormalizedEvent{Type: "text_delta", ContentIdx: currentTextIndex, Delta: delta})
				}
			}
		case "response.function_call_arguments.delta":
			if currentToolIndex >= 0 && currentToolIndex < len(blocks) {
				delta := asString(item["delta"])
				rawToolArgs[currentToolIndex] += delta
				var arguments map[string]any
				if json.Unmarshal([]byte(rawToolArgs[currentToolIndex]), &arguments) == nil {
					blocks[currentToolIndex].Arguments = arguments
				}
				if delta != "" {
					events = append(events, NormalizedEvent{Type: "toolcall_delta", ContentIdx: currentToolIndex, Delta: delta})
				}
			}
		case "response.function_call_arguments.done":
			if currentToolIndex >= 0 && currentToolIndex < len(blocks) {
				rawToolArgs[currentToolIndex] = asString(item["arguments"])
				var arguments map[string]any
				if json.Unmarshal([]byte(rawToolArgs[currentToolIndex]), &arguments) == nil {
					blocks[currentToolIndex].Arguments = arguments
				}
			}
		case "response.output_item.done":
			outputItem, _ := item["item"].(map[string]any)
			switch asString(outputItem["type"]) {
			case "reasoning":
				if currentThinkingIndex >= 0 && currentThinkingIndex < len(blocks) {
					events = append(events, NormalizedEvent{
						Type:       "thinking_end",
						ContentIdx: currentThinkingIndex,
						Content:    blocks[currentThinkingIndex].Thinking,
					})
				}
				currentThinkingIndex = -1
			case "message":
				if currentTextIndex >= 0 && currentTextIndex < len(blocks) {
					events = append(events, NormalizedEvent{
						Type:       "text_end",
						ContentIdx: currentTextIndex,
						Content:    blocks[currentTextIndex].Text,
					})
				}
				currentTextIndex = -1
			case "function_call":
				if currentToolIndex >= 0 && currentToolIndex < len(blocks) {
					events = append(events, NormalizedEvent{
						Type:       "toolcall_end",
						ContentIdx: currentToolIndex,
						ToolCall: &NormalizedTool{
							Name:      blocks[currentToolIndex].Name,
							Arguments: blocks[currentToolIndex].Arguments,
							HasID:     blocks[currentToolIndex].ID != "",
						},
					})
				}
				currentToolIndex = -1
			}
		case "response.completed":
			response, _ := item["response"].(map[string]any)
			if response != nil {
				stopReason = mapOpenAIResponsesStopReason(asString(response["status"]))
				if usageMap, ok := response["usage"].(map[string]any); ok {
					cachedTokens := 0
					if details, ok := usageMap["input_tokens_details"].(map[string]any); ok {
						cachedTokens = int(asFloat64(details["cached_tokens"]))
					}
					result.Usage = &Usage{
						Input:       int(asFloat64(usageMap["input_tokens"])) - cachedTokens,
						Output:      int(asFloat64(usageMap["output_tokens"])),
						CacheRead:   cachedTokens,
						TotalTokens: int(asFloat64(usageMap["total_tokens"])),
					}
				}
			}
		case "response.failed":
			response, _ := item["response"].(map[string]any)
			if errMap, ok := response["error"].(map[string]any); ok {
				return errors.New(asString(errMap["message"]))
			}
			return errors.New("openai responses stream failed")
		case "error":
			return errors.New(asString(item["message"]))
		}
		return nil
	})
	if err != nil {
		return NormalizedResult{}, nil, err
	}

	if stopReason == "stop" {
		for _, block := range blocks {
			if block.Type == "toolCall" {
				stopReason = "toolUse"
				break
			}
		}
	}
	result.StopReason = mapOpenAIStopReason(stopReason)
	result.Text = ContentText(blocks)
	result.Content = NormalizedContent(blocks)
	return result, appendTerminalEvent(events, result.StopReason), nil
}

func openAIResponsesToResult(body io.Reader) (NormalizedResult, []NormalizedEvent, error) {
	var response openAIResponsesResponse
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("parse openai responses payload: %w", err)
	}

	blocks, hasToolCalls := openAIResponsesContentToBlocks(response.Output)
	stopReason := mapOpenAIResponsesStopReason(response.Status)
	if stopReason == "" {
		stopReason = "stop"
	}
	if stopReason == "stop" && hasToolCalls {
		stopReason = "toolUse"
	}

	result := NormalizedResult{
		Role:       "assistant",
		StopReason: mapOpenAIStopReason(stopReason),
		ResponseID: response.ID,
		Text:       ContentText(blocks),
		Content:    NormalizedContent(blocks),
	}
	if response.Usage != nil {
		inputTokens := response.Usage.InputTokens
		if inputTokens == 0 {
			inputTokens = response.Usage.PromptTokens
		}
		outputTokens := response.Usage.OutputTokens
		if outputTokens == 0 {
			outputTokens = response.Usage.CompletionTokens
		}
		cacheRead := 0
		if response.Usage.InputTokensDetails.CachedTokens != 0 {
			cacheRead = response.Usage.InputTokensDetails.CachedTokens
		}
		if cacheRead > inputTokens {
			cacheRead = inputTokens
		}
		input := inputTokens - cacheRead
		if input < 0 {
			input = 0
		}
		result.Usage = &Usage{
			Input:       input,
			Output:      outputTokens,
			CacheRead:   cacheRead,
			TotalTokens: response.Usage.TotalTokens,
		}
	}

	return result, AssistantEvents(blocks, result.StopReason), nil
}

func mapOpenAIResponsesStopReason(status string) string {
	switch status {
	case "completed":
		return "stop"
	case "incomplete":
		return "length"
	case "failed", "cancelled":
		return "error"
	case "aborted":
		return "aborted"
	case "queued", "in_progress":
		return "stop"
	case "":
		return ""
	default:
		return "error"
	}
}

func openAIResponsesContentToBlocks(raw []any) ([]ContentBlock, bool) {
	blocks := make([]ContentBlock, 0)
	hasToolCalls := false

	for _, item := range raw {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typeValue := asString(itemMap["type"])
		switch typeValue {
		case "message":
			contents, ok := itemMap["content"].([]any)
			if !ok {
				continue
			}
			for _, content := range contents {
				asMap, ok := content.(map[string]any)
				if !ok {
					continue
				}
				contentType := asString(asMap["type"])
				switch contentType {
				case "output_text", "text", "refusal":
					text := asString(asMap["text"])
					if strings.TrimSpace(text) != "" {
						blocks = append(blocks, ContentBlock{Type: "text", Text: text})
					}
				case "tool_call", "function_call":
					hasToolCalls = true
					arguments := asResponseArguments(asMap["arguments"])
					if len(arguments) == 0 {
						arguments = asResponseArguments(asMap["input"])
					}
					blocks = append(blocks, ContentBlock{
						Type:      "toolCall",
						ID:        mergeOpenAIResponsesToolCallID(asString(asMap["call_id"]), asString(asMap["id"])),
						Name:      asString(asMap["name"]),
						Arguments: arguments,
					})
				}
			}
		case "tool_call", "function_call":
			hasToolCalls = true
			arguments := asResponseArguments(itemMap["arguments"])
			if len(arguments) == 0 {
				arguments = asResponseArguments(itemMap["input"])
			}
			blocks = append(blocks, ContentBlock{
				Type:      "toolCall",
				ID:        mergeOpenAIResponsesToolCallID(asString(itemMap["call_id"]), asString(itemMap["id"])),
				Name:      asString(itemMap["name"]),
				Arguments: arguments,
			})
		}
	}
	return blocks, hasToolCalls
}

func asResponseArguments(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case string:
		if strings.TrimSpace(typed) == "" {
			return map[string]any{}
		}
		parsed := map[string]any{}
		if err := json.Unmarshal([]byte(typed), &parsed); err == nil {
			return parsed
		}
		return map[string]any{"arguments": typed}
	default:
		return map[string]any{}
	}
}

func openAIChoiceToNormalized(choice openAIChoice) NormalizedResult {
	blocks := []ContentBlock{}

	if choice.Message.Content != nil {
		if text, ok := choice.Message.Content.(string); ok && strings.TrimSpace(text) != "" {
			blocks = append(blocks, ContentBlock{Type: "text", Text: text})
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
		StopReason: mapOpenAIStopReason(choice.FinishReason),
		Text:       ContentText(blocks),
		Content:    NormalizedContent(blocks),
	}
}

func resolveAzureOpenAIBaseURL() (string, error) {
	baseURL := strings.TrimSpace(os.Getenv("AZURE_OPENAI_BASE_URL"))
	if baseURL != "" {
		return baseURL, nil
	}
	resourceName := strings.TrimSpace(os.Getenv("AZURE_OPENAI_RESOURCE_NAME"))
	if resourceName != "" {
		return "https://" + resourceName + ".openai.azure.com/openai/v1", nil
	}
	return "", errors.New(
		"Azure OpenAI base URL is required. Set AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME.",
	)
}

func ensureAzureOpenAIAPIVersion(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	apiVersion := strings.TrimSpace(os.Getenv("AZURE_OPENAI_API_VERSION"))
	if apiVersion == "" {
		apiVersion = defaultAzureAPIVersion
	}
	query := parsed.Query()
	if query.Get("api-version") == "" {
		query.Set("api-version", apiVersion)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func openAIStreamToResult(body io.Reader) (NormalizedResult, []NormalizedEvent, error) {
	scanner := bufio.NewScanner(body)
	builder := strings.Builder{}
	toolCalls := map[int]*openAIStreamingCall{}
	stopReason := "stop"
	responseID := ""
	events := []NormalizedEvent{{Type: "start"}}
	textStarted := false
	toolBlockIndexes := map[int]int{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return NormalizedResult{}, nil, fmt.Errorf("parse openai stream chunk: %w", err)
		}
		if chunk.ID != "" && responseID == "" {
			responseID = chunk.ID
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				stopReason = choice.FinishReason
			}
			if text, ok := choice.Delta.Content.(string); ok && text != "" {
				if !textStarted {
					textStarted = true
					events = append(events, NormalizedEvent{Type: "text_start", ContentIdx: 0})
				}
				builder.WriteString(text)
				events = append(events, NormalizedEvent{Type: "text_delta", ContentIdx: 0, Delta: text})
			}
			for _, toolCall := range choice.Delta.ToolCalls {
				entry, ok := toolCalls[toolCall.Index]
				if !ok {
					entry = &openAIStreamingCall{}
					toolCalls[toolCall.Index] = entry
				}
				contentIndex, ok := toolBlockIndexes[toolCall.Index]
				if !ok {
					contentIndex = len(toolBlockIndexes)
					if textStarted {
						contentIndex++
					}
					toolBlockIndexes[toolCall.Index] = contentIndex
					events = append(events, NormalizedEvent{Type: "toolcall_start", ContentIdx: contentIndex})
				}
				if toolCall.ID != "" {
					entry.ID = toolCall.ID
				}
				if toolCall.Function.Name != "" {
					entry.Name = toolCall.Function.Name
				}
				entry.Arguments.WriteString(toolCall.Function.Arguments)
				if toolCall.Function.Arguments != "" {
					events = append(events, NormalizedEvent{
						Type:       "toolcall_delta",
						ContentIdx: contentIndex,
						Delta:      toolCall.Function.Arguments,
					})
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("scan openai stream: %w", err)
	}

	blocks := []ContentBlock{}
	if strings.TrimSpace(builder.String()) != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: builder.String()})
		events = append(events, NormalizedEvent{Type: "text_end", ContentIdx: 0, Content: builder.String()})
	}

	indices := make([]int, 0, len(toolCalls))
	for index := range toolCalls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		call := toolCalls[index]
		arguments := map[string]any{}
		argumentPayload := strings.TrimSpace(call.Arguments.String())
		if argumentPayload != "" {
			if err := json.Unmarshal([]byte(argumentPayload), &arguments); err != nil {
				return NormalizedResult{}, nil, fmt.Errorf("parse openai tool arguments: %w", err)
			}
		}
		blocks = append(blocks, ContentBlock{
			Type:      "toolCall",
			ID:        call.ID,
			Name:      call.Name,
			Arguments: arguments,
		})
		events = append(events, NormalizedEvent{
			Type:       "toolcall_end",
			ContentIdx: len(blocks) - 1,
			ToolCall: &NormalizedTool{
				ID:        call.ID,
				Name:      call.Name,
				Arguments: arguments,
				HasID:     call.ID != "",
			},
		})
	}

	if len(toolCalls) > 0 {
		stopReason = mapOpenAIStopReason(stopReason)
		if stopReason == "stop" {
			stopReason = "toolUse"
		}
	}

	result := NormalizedResult{
		Role:       "assistant",
		StopReason: mapOpenAIStopReason(stopReason),
		ResponseID: responseID,
		Text:       ContentText(blocks),
		Content:    NormalizedContent(blocks),
	}
	return result, appendTerminalEvent(events, result.StopReason), nil
}

func toOpenAIMessages(messages []Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "user":
			out = append(out, openAIMessage{Role: "user", Content: MessageText(message)})
		case "assistant":
			blocks := messageContentBlocks(message.Content)
			aiMsg := openAIMessage{Role: "assistant"}
			textParts := make([]string, 0, len(blocks))
			for _, block := range blocks {
				switch block.Type {
				case "text":
					textParts = append(textParts, block.Text)
				case "toolCall":
					args, _ := json.Marshal(block.Arguments)
					aiMsg.ToolCalls = append(aiMsg.ToolCalls, openAICallSpec{
						ID:   block.ID,
						Type: "function",
						Function: openAIArguments{
							Name:      block.Name,
							Arguments: string(args),
						},
					})
				}
			}
			if len(textParts) > 0 {
				aiMsg.Content = strings.Join(textParts, "")
			}
			if len(aiMsg.ToolCalls) > 0 && aiMsg.Content == nil {
				aiMsg.Content = ""
			}
			out = append(out, aiMsg)
		case "toolResult":
			out = append(out, openAIMessage{
				Role:       "tool",
				ToolCallID: message.ToolCallID,
				Content:    MessageText(message),
			})
		}
	}
	return out
}

func messageContentBlocks(raw any) []ContentBlock {
	blocks := ParseContentBlocks(raw)
	if len(blocks) > 0 {
		return blocks
	}
	text := MessageText(Message{Role: "user", Content: raw})
	if text == "" {
		return nil
	}
	return []ContentBlock{{Type: "text", Text: text}}
}

func mapOpenAIStopReason(reason string) string {
	switch reason {
	case "tool_calls", "tool call", "function_call", "function":
		return "toolUse"
	case "length", "max_tokens":
		return "length"
	case "stop", "stoped", "completed":
		return "stop"
	case "error":
		return "error"
	case "aborted":
		return "aborted"
	default:
		if reason == "" {
			return "stop"
		}
		return reason
	}
}

func (result NormalizedResult) contentBlocks() []ContentBlock {
	return ParseContentBlocks(result.Content)
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	if bytes, ok := value.([]byte); ok {
		return string(bytes)
	}
	return ""
}
