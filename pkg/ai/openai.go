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
	ParallelToolCalls    *bool            `json:"parallel_tool_calls,omitempty"`
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
	Role             string           `json:"role"`
	Content          any              `json:"content,omitempty"`
	ToolCalls        []openAICallSpec `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	Name             string           `json:"name,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	Reasoning        string           `json:"reasoning,omitempty"`
	ReasoningText    string           `json:"reasoning_text,omitempty"`
	ReasoningDetails []map[string]any `json:"reasoning_details,omitempty"`
}

type openAIUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
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
	Usage   *openAIUsage         `json:"usage,omitempty"`
}

type openAIStreamChoice struct {
	Delta        openAIMessage `json:"delta"`
	FinishReason string        `json:"finish_reason"`
	Usage        *openAIUsage  `json:"usage,omitempty"`
}

type openAIResponsesRequest struct {
	Model                string           `json:"model"`
	Input                []any            `json:"input"`
	Tools                []openAIChatTool `json:"tools,omitempty"`
	ToolChoice           string           `json:"tool_choice,omitempty"`
	ParallelToolCalls    *bool            `json:"parallel_tool_calls,omitempty"`
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
	BlockIdx  int
}

type openAICompat struct {
	SupportsStore                            bool
	SupportsDeveloperRole                    bool
	SupportsUsageInStreaming                 bool
	SupportsReasoningEffort                  bool
	ReasoningEffortMap                       map[string]string
	MaxTokensField                           string
	ThinkingFormat                           string
	ZAIToolStream                            bool
	SupportsStrictMode                       bool
	CacheControlFormat                       string
	SendSessionAffinityHeaders               bool
	SupportsLongCacheRetention               bool
	OpenRouterRouting                        map[string]any
	VercelGatewayRouting                     map[string]any
	RequiresToolResultName                   bool
	RequiresAssistantAfterToolResult         bool
	RequiresThinkingAsText                   bool
	RequiresReasoningContentOnAssistantReply bool
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
	resolvedModel, _ := resolveCompletionModel(req)
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
		payload := toOpenAIResponsesRequest(req, resolvedModel)
		requestBody = payload
	} else {
		requestBody = buildOpenAIChatCompletionsRequest(req, resolvedModel)
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
			if useResponsesAPI {
				httpReq.Header.Set("session_id", sessionID)
				httpReq.Header.Set("x-client-request-id", sessionID)
				httpReq.Header.Set("x-session-affinity", sessionID)
			} else if model, ok := resolveCompletionModel(req); ok {
				compat := getOpenAICompat(model)
				if compat.SendSessionAffinityHeaders {
					httpReq.Header.Set("session_id", sessionID)
					httpReq.Header.Set("x-client-request-id", sessionID)
					httpReq.Header.Set("x-session-affinity", sessionID)
				}
			}
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
			rawErrorText := strings.TrimSpace(string(body))
			if attempt < maxRetries && shouldRetryHTTPStatus(resp.StatusCode, rawErrorText) {
				serverDelay := retryAfterDelay(resp, rawErrorText)
				if err := validateRetryDelay(req.Options, serverDelay); err != nil {
					return NormalizedResult{}, nil, err
				}
				if sleepErr := sleepWithContext(ctx, retryDelayForAttempt(attempt, serverDelay, defaultProviderBaseDelay)); sleepErr != nil {
					return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: sleepErr.Error()}, nil, sleepErr
				}
				continue
			}
			errorText := formatOpenAIHTTPError(rawErrorText)
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
			result, events, streamErr := openAIStreamToResult(resp.Body, resolvedModel)
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
				result, events, streamErr := openAIResponsesSSEToResult(resp.Body, resolvedModel)
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
			result, events, responseErr := openAIResponsesToResult(resp.Body, resolvedModel)
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
			result.Usage = normalizeOpenAIChatUsage(completion.Usage, resolvedModel)
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

func toOpenAIResponsesRequest(req CompletionRequest, model Model) openAIResponsesRequest {
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

	supportsImages := modelSupportsInput(model, "image")
	for _, message := range req.Messages {
		switch message.Role {
		case "system":
			text := strings.TrimSpace(MessageText(message))
			if text == "" {
				continue
			}
			payload.Input = append(payload.Input, map[string]any{
				"role": "system",
				"content": []map[string]any{{
					"type": "input_text",
					"text": text,
				}},
			})
		case "user":
			parts := make([]map[string]any, 0)
			for _, block := range messageContentBlocks(message.Content) {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						parts = append(parts, map[string]any{
							"type": "input_text",
							"text": block.Text,
						})
					}
				case "image":
					if supportsImages && strings.TrimSpace(block.Data) != "" && strings.TrimSpace(block.MimeType) != "" {
						parts = append(parts, map[string]any{
							"type":      "input_image",
							"detail":    "auto",
							"image_url": "data:" + block.MimeType + ";base64," + block.Data,
						})
					}
				}
			}
			if len(parts) == 0 {
				contentText := strings.TrimSpace(MessageText(message))
				if contentText != "" {
					parts = append(parts, map[string]any{
						"type": "input_text",
						"text": contentText,
					})
				}
			}
			if len(parts) == 0 {
				continue
			}
			payload.Input = append(payload.Input, map[string]any{
				"role":    "user",
				"content": parts,
			})

		case "assistant":
			isDifferentModel := message.Model != "" && message.Model != model.ID && message.Provider == model.Provider && message.API == model.API
			for _, block := range messageContentBlocks(message.Content) {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						payload.Input = append(payload.Input, map[string]any{
							"type":   "message",
							"role":   "assistant",
							"status": "completed",
							"content": []map[string]any{{
								"type":        "output_text",
								"text":        block.Text,
								"annotations": []any{},
							}},
						})
					}
				case "toolCall":
					callID, itemID := splitToolCallID(block.ID)
					if callID == "" {
						continue
					}
					if isDifferentModel && strings.HasPrefix(itemID, "fc_") {
						itemID = ""
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

		case "toolResult":
			callID, _ := splitToolCallID(message.ToolCallID)
			if callID == "" {
				continue
			}
			output := MessageText(message)
			if strings.TrimSpace(output) == "" && supportsImages {
				for _, block := range messageContentBlocks(message.Content) {
					if block.Type == "image" {
						output = "(see attached image)"
						break
					}
				}
			}
			item := map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  output,
			}
			payload.Input = append(payload.Input, item)
		}
	}

	if len(req.Tools) > 0 {
		payload.Tools = make([]openAIChatTool, 0, len(req.Tools))
		payload.ToolChoice = strings.TrimSpace(req.Options.ToolChoice)
		if payload.ToolChoice == "" {
			payload.ToolChoice = "auto"
		}
		payload.ParallelToolCalls = req.Options.ParallelToolCalls
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

func openAIResponsesSSEToResult(body io.Reader, model Model) (NormalizedResult, []NormalizedEvent, error) {
	return openAIResponsesEventsToResult(model, func(handle func(map[string]any) error) error {
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

func openAIResponsesEventsToResult(model Model, iterate func(func(map[string]any) error) error) (NormalizedResult, []NormalizedEvent, error) {
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
							ID:        blocks[currentToolIndex].ID,
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
					calculated := CalculateCost(model, *result.Usage)
					*result.Usage = calculated
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

func openAIResponsesToResult(body io.Reader, model Model) (NormalizedResult, []NormalizedEvent, error) {
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
		calculated := CalculateCost(model, *result.Usage)
		*result.Usage = calculated
	}

	return result, AssistantEvents(blocks, result.StopReason), nil
}

func normalizeOpenAIChatUsage(usage *openAIUsage, model Model) *Usage {
	if usage == nil {
		return nil
	}
	reportedCachedTokens := usage.PromptTokensDetails.CachedTokens
	cacheWriteTokens := usage.PromptTokensDetails.CacheWriteTokens
	cacheReadTokens := reportedCachedTokens
	if cacheWriteTokens > 0 {
		cacheReadTokens = reportedCachedTokens - cacheWriteTokens
		if cacheReadTokens < 0 {
			cacheReadTokens = 0
		}
	}
	input := usage.PromptTokens - cacheReadTokens - cacheWriteTokens
	if input < 0 {
		input = 0
	}
	result := &Usage{
		Input:       input,
		Output:      usage.CompletionTokens,
		CacheRead:   cacheReadTokens,
		CacheWrite:  cacheWriteTokens,
		TotalTokens: input + usage.CompletionTokens + cacheReadTokens + cacheWriteTokens,
	}
	calculated := CalculateCost(model, *result)
	*result = calculated
	return result
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
	if reasoning := firstOpenAIReasoning(choice.Message); strings.TrimSpace(reasoning) != "" {
		blocks = append([]ContentBlock{{Type: "thinking", Thinking: reasoning}}, blocks...)
	}
	for _, toolCall := range choice.Message.ToolCalls {
		arguments := map[string]any{}
		if strings.TrimSpace(toolCall.Function.Arguments) != "" {
			_ = json.Unmarshal([]byte(toolCall.Function.Arguments), &arguments)
		}
		block := ContentBlock{
			Type:      "toolCall",
			ID:        toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: arguments,
		}
		attachOpenAIReasoningSignature(&block, choice.Message.ReasoningDetails)
		blocks = append(blocks, block)
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

func openAIStreamToResult(body io.Reader, model Model) (NormalizedResult, []NormalizedEvent, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	toolCalls := map[int]*openAIStreamingCall{}
	stopReason := "stop"
	responseID := ""
	events := []NormalizedEvent{{Type: "start"}}
	var usage *Usage
	toolReasoningDetails := map[string]string{}
	blocks := []ContentBlock{}
	currentKind := ""
	currentIndex := -1

	finishCurrentBlock := func() error {
		if currentIndex < 0 || currentIndex >= len(blocks) {
			currentKind = ""
			currentIndex = -1
			return nil
		}
		block := &blocks[currentIndex]
		switch block.Type {
		case "text":
			events = append(events, NormalizedEvent{Type: "text_end", ContentIdx: currentIndex, Content: block.Text})
		case "thinking":
			events = append(events, NormalizedEvent{Type: "thinking_end", ContentIdx: currentIndex, Content: block.Thinking})
		case "toolCall":
			tool := &NormalizedTool{
				ID:               block.ID,
				Name:             block.Name,
				Arguments:        block.Arguments,
				HasID:            block.ID != "",
				ThoughtSignature: block.ThoughtSignature,
			}
			events = append(events, NormalizedEvent{Type: "toolcall_end", ContentIdx: currentIndex, ToolCall: tool})
		}
		currentKind = ""
		currentIndex = -1
		return nil
	}

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
		if chunk.Usage != nil {
			usage = normalizeOpenAIChatUsage(chunk.Usage, model)
		}
		for _, choice := range chunk.Choices {
			if usage == nil && choice.Usage != nil {
				usage = normalizeOpenAIChatUsage(choice.Usage, model)
			}
			if choice.FinishReason != "" {
				stopReason = choice.FinishReason
			}
			if reasoning := firstOpenAIReasoning(choice.Delta); reasoning != "" {
				if currentKind != "thinking" {
					if err := finishCurrentBlock(); err != nil {
						return NormalizedResult{}, nil, err
					}
					blocks = append(blocks, ContentBlock{Type: "thinking", Thinking: "", ThinkingSignature: firstOpenAIReasoningField(choice.Delta)})
					currentIndex = len(blocks) - 1
					currentKind = "thinking"
					events = append(events, NormalizedEvent{Type: "thinking_start", ContentIdx: currentIndex})
				}
				blocks[currentIndex].Thinking += reasoning
				events = append(events, NormalizedEvent{Type: "thinking_delta", ContentIdx: currentIndex, Delta: reasoning})
			}
			if text, ok := choice.Delta.Content.(string); ok && text != "" {
				if currentKind != "text" {
					if err := finishCurrentBlock(); err != nil {
						return NormalizedResult{}, nil, err
					}
					blocks = append(blocks, ContentBlock{Type: "text", Text: ""})
					currentIndex = len(blocks) - 1
					currentKind = "text"
					events = append(events, NormalizedEvent{Type: "text_start", ContentIdx: currentIndex})
				}
				blocks[currentIndex].Text += text
				events = append(events, NormalizedEvent{Type: "text_delta", ContentIdx: currentIndex, Delta: text})
			}
			for _, toolCall := range choice.Delta.ToolCalls {
				entry, ok := toolCalls[toolCall.Index]
				if !ok {
					if err := finishCurrentBlock(); err != nil {
						return NormalizedResult{}, nil, err
					}
					blocks = append(blocks, ContentBlock{Type: "toolCall", Arguments: map[string]any{}})
					currentIndex = len(blocks) - 1
					currentKind = "toolCall"
					events = append(events, NormalizedEvent{Type: "toolcall_start", ContentIdx: currentIndex})
					entry = &openAIStreamingCall{BlockIdx: currentIndex}
					toolCalls[toolCall.Index] = entry
				} else {
					if currentKind != "" && currentKind != "toolCall" {
						if err := finishCurrentBlock(); err != nil {
							return NormalizedResult{}, nil, err
						}
					}
					currentIndex = entry.BlockIdx
					currentKind = "toolCall"
				}
				if toolCall.ID != "" {
					entry.ID = toolCall.ID
					blocks[entry.BlockIdx].ID = toolCall.ID
				}
				if toolCall.Function.Name != "" {
					entry.Name = toolCall.Function.Name
					blocks[entry.BlockIdx].Name = toolCall.Function.Name
				}
				entry.Arguments.WriteString(toolCall.Function.Arguments)
				if toolCall.Function.Arguments != "" {
					events = append(events, NormalizedEvent{Type: "toolcall_delta", ContentIdx: entry.BlockIdx, Delta: toolCall.Function.Arguments})
				}
			}
			for _, detail := range choice.Delta.ReasoningDetails {
				if asString(detail["type"]) == "reasoning.encrypted" {
					callID := asString(detail["id"])
					data := asString(detail["data"])
					if callID != "" && data != "" {
						toolReasoningDetails[callID] = mustJSON(detail)
						for index := range blocks {
							if blocks[index].Type == "toolCall" && blocks[index].ID == callID {
								blocks[index].ThoughtSignature = toolReasoningDetails[callID]
							}
						}
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("scan openai stream: %w", err)
	}

	for _, call := range toolCalls {
		arguments := map[string]any{}
		argumentPayload := strings.TrimSpace(call.Arguments.String())
		if argumentPayload != "" {
			if err := json.Unmarshal([]byte(argumentPayload), &arguments); err != nil {
				return NormalizedResult{}, nil, fmt.Errorf("parse openai tool arguments: %w", err)
			}
		}
		if call.BlockIdx >= 0 && call.BlockIdx < len(blocks) {
			blocks[call.BlockIdx].Arguments = arguments
			if signature, ok := toolReasoningDetails[blocks[call.BlockIdx].ID]; ok {
				blocks[call.BlockIdx].ThoughtSignature = signature
			}
		}
	}
	if err := finishCurrentBlock(); err != nil {
		return NormalizedResult{}, nil, err
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
	result.Usage = usage
	return result, appendTerminalEvent(events, result.StopReason), nil
}

func firstOpenAIReasoning(message openAIMessage) string {
	for _, value := range []string{message.ReasoningContent, message.Reasoning, message.ReasoningText} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstOpenAIReasoningField(message openAIMessage) string {
	for _, candidate := range []struct {
		name  string
		value string
	}{
		{name: "reasoning_content", value: message.ReasoningContent},
		{name: "reasoning", value: message.Reasoning},
		{name: "reasoning_text", value: message.ReasoningText},
	} {
		if strings.TrimSpace(candidate.value) != "" {
			return candidate.name
		}
	}
	return ""
}

func attachOpenAIReasoningSignature(block *ContentBlock, reasoningDetails []map[string]any) {
	if block == nil || block.Type != "toolCall" || block.ID == "" {
		return
	}
	for _, detail := range reasoningDetails {
		if asString(detail["type"]) != "reasoning.encrypted" {
			continue
		}
		if asString(detail["id"]) != block.ID || strings.TrimSpace(asString(detail["data"])) == "" {
			continue
		}
		block.ThoughtSignature = mustJSON(detail)
		return
	}
}

func toOpenAIMessages(messages []Message, model Model) []openAIMessage {
	compat := getOpenAICompat(model)
	out := make([]openAIMessage, 0, len(messages))
	lastRole := ""
	for i := 0; i < len(messages); i++ {
		message := messages[i]
		if compat.RequiresAssistantAfterToolResult && lastRole == "toolResult" && message.Role == "user" {
			out = append(out, openAIMessage{Role: "assistant", Content: "I have processed the tool results."})
		}
		switch message.Role {
		case "system":
			text := strings.TrimSpace(MessageText(message))
			if text != "" {
				role := "system"
				if model.Reasoning && compat.SupportsDeveloperRole {
					role = "developer"
				}
				out = append(out, openAIMessage{Role: role, Content: text})
				lastRole = "system"
			}
		case "user":
			blocks := messageContentBlocks(message.Content)
			if len(blocks) == 0 {
				text := strings.TrimSpace(MessageText(message))
				if text == "" {
					continue
				}
				out = append(out, openAIMessage{Role: "user", Content: text})
				lastRole = "user"
				continue
			}
			content := make([]map[string]any, 0, len(blocks))
			for _, block := range blocks {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						content = append(content, map[string]any{"type": "text", "text": block.Text})
					}
				case "image":
					if modelSupportsInput(model, "image") {
						content = append(content, map[string]any{
							"type": "image_url",
							"image_url": map[string]any{
								"url": "data:" + block.MimeType + ";base64," + block.Data,
							},
						})
					}
				}
			}
			if len(content) == 0 {
				continue
			}
			if len(content) == 1 && content[0]["type"] == "text" {
				out = append(out, openAIMessage{Role: "user", Content: content[0]["text"]})
			} else {
				out = append(out, openAIMessage{Role: "user", Content: content})
			}
			lastRole = "user"
		case "assistant":
			blocks := messageContentBlocks(message.Content)
			aiMsg := openAIMessage{Role: "assistant", Content: nil}
			if compat.RequiresAssistantAfterToolResult {
				aiMsg.Content = ""
			}
			textParts := make([]string, 0, len(blocks))
			thinkingParts := make([]string, 0, len(blocks))
			for _, block := range blocks {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						textParts = append(textParts, block.Text)
					}
				case "thinking":
					if strings.TrimSpace(block.Thinking) != "" {
						thinkingParts = append(thinkingParts, block.Thinking)
					}
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
			if len(thinkingParts) > 0 {
				if compat.RequiresThinkingAsText {
					combined := append([]string{}, thinkingParts...)
					combined = append(combined, textParts...)
					aiMsg.Content = strings.Join(combined, "\n\n")
				} else if len(textParts) == 0 {
					aiMsg.Content = strings.Join(thinkingParts, "\n\n")
				}
			}
			if len(textParts) > 0 {
				aiMsg.Content = strings.Join(textParts, "")
			}
			if len(aiMsg.ToolCalls) > 0 && aiMsg.Content == nil {
				aiMsg.Content = ""
			}
			if compat.RequiresReasoningContentOnAssistantReply && aiMsg.Content == nil {
				aiMsg.Content = ""
			}
			if aiMsg.Content == nil && len(aiMsg.ToolCalls) == 0 {
				continue
			}
			out = append(out, aiMsg)
			lastRole = "assistant"
		case "toolResult":
			text := MessageText(message)
			hasImages := false
			for _, block := range messageContentBlocks(message.Content) {
				if block.Type == "image" {
					hasImages = true
					break
				}
			}
			if strings.TrimSpace(text) == "" && hasImages {
				text = "(see attached image)"
			}
			toolMsg := openAIMessage{
				Role:       "tool",
				ToolCallID: message.ToolCallID,
				Content:    text,
			}
			if compat.RequiresToolResultName && message.ToolName != "" {
				toolMsg.Name = message.ToolName
			}
			out = append(out, toolMsg)
			lastRole = "toolResult"
			if hasImages && modelSupportsInput(model, "image") {
				if compat.RequiresAssistantAfterToolResult {
					out = append(out, openAIMessage{Role: "assistant", Content: "I have processed the tool results."})
				}
				imageContent := []map[string]any{{"type": "text", "text": "Attached image(s) from tool result:"}}
				for _, block := range messageContentBlocks(message.Content) {
					if block.Type == "image" {
						imageContent = append(imageContent, map[string]any{
							"type": "image_url",
							"image_url": map[string]any{
								"url": "data:" + block.MimeType + ";base64," + block.Data,
							},
						})
					}
				}
				out = append(out, openAIMessage{Role: "user", Content: imageContent})
				lastRole = "user"
			}
		}
	}
	return out
}

func getOpenAICompat(model Model) openAICompat {
	baseURL := strings.ToLower(strings.TrimSpace(model.BaseURL))
	provider := canonicalProviderName(model.Provider)
	isZAI := provider == "zai" || strings.Contains(baseURL, "api.z.ai")
	isNonStandard := provider == "cerebras" ||
		strings.Contains(baseURL, "cerebras.ai") ||
		provider == "xai" ||
		strings.Contains(baseURL, "api.x.ai") ||
		strings.Contains(baseURL, "chutes.ai") ||
		provider == "deepseek" ||
		strings.Contains(baseURL, "deepseek.com") ||
		isZAI ||
		provider == "opencode" ||
		strings.Contains(baseURL, "opencode.ai")
	compat := openAICompat{
		SupportsStore:                            !isNonStandard,
		SupportsDeveloperRole:                    !isNonStandard,
		SupportsUsageInStreaming:                 true,
		SupportsReasoningEffort:                  provider != "xai" && !isZAI && !strings.Contains(baseURL, "api.x.ai"),
		ReasoningEffortMap:                       map[string]string{},
		MaxTokensField:                           "max_completion_tokens",
		ThinkingFormat:                           "openai",
		ZAIToolStream:                            false,
		SupportsStrictMode:                       true,
		CacheControlFormat:                       "",
		SendSessionAffinityHeaders:               false,
		SupportsLongCacheRetention:               true,
		OpenRouterRouting:                        map[string]any{},
		VercelGatewayRouting:                     map[string]any{},
		RequiresToolResultName:                   false,
		RequiresAssistantAfterToolResult:         false,
		RequiresThinkingAsText:                   false,
		RequiresReasoningContentOnAssistantReply: provider == "deepseek" || strings.Contains(baseURL, "deepseek.com"),
	}
	if isZAI {
		compat.ThinkingFormat = "zai"
	}
	if provider == "deepseek" || strings.Contains(baseURL, "deepseek.com") {
		compat.ThinkingFormat = "deepseek"
		compat.SupportsReasoningEffort = true
		compat.ReasoningEffortMap = map[string]string{
			"minimal": "high",
			"low":     "high",
			"medium":  "high",
			"high":    "high",
			"xhigh":   "max",
		}
	}
	if provider == "openrouter" || strings.Contains(baseURL, "openrouter.ai") {
		compat.ThinkingFormat = "openrouter"
		if strings.HasPrefix(strings.TrimSpace(model.ID), "anthropic/") {
			compat.CacheControlFormat = "anthropic"
		}
	}
	if provider == "groq" || strings.Contains(baseURL, "groq.com") {
		if strings.TrimSpace(model.ID) == "qwen/qwen3-32b" {
			compat.ReasoningEffortMap = map[string]string{
				"minimal": "default",
				"low":     "default",
				"medium":  "default",
				"high":    "default",
				"xhigh":   "default",
			}
		}
	}
	if strings.Contains(baseURL, "chutes.ai") {
		compat.MaxTokensField = "max_tokens"
	}
	providerCompat := modelCompatForProvider(provider)
	if len(providerCompat) > 0 {
		merged := cloneCompatMap(providerCompat)
		for key, value := range model.Compat {
			merged[key] = value
		}
		model.Compat = merged
	}
	if model.Compat == nil {
		return compat
	}
	if value, ok := compatBool(model.Compat["supportsStore"]); ok {
		compat.SupportsStore = value
	}
	if value, ok := compatBool(model.Compat["supportsDeveloperRole"]); ok {
		compat.SupportsDeveloperRole = value
	}
	if value, ok := compatBool(model.Compat["supportsUsageInStreaming"]); ok {
		compat.SupportsUsageInStreaming = value
	}
	if value, ok := compatBool(model.Compat["supportsReasoningEffort"]); ok {
		compat.SupportsReasoningEffort = value
	}
	if value, ok := compatBool(model.Compat["zaiToolStream"]); ok {
		compat.ZAIToolStream = value
	}
	if value, ok := compatBool(model.Compat["supportsStrictMode"]); ok {
		compat.SupportsStrictMode = value
	}
	if value, ok := compatBool(model.Compat["sendSessionAffinityHeaders"]); ok {
		compat.SendSessionAffinityHeaders = value
	}
	if value, ok := compatBool(model.Compat["supportsLongCacheRetention"]); ok {
		compat.SupportsLongCacheRetention = value
	}
	if value, ok := model.Compat["maxTokensField"].(string); ok && strings.TrimSpace(value) != "" {
		compat.MaxTokensField = value
	}
	if value, ok := model.Compat["thinkingFormat"].(string); ok && strings.TrimSpace(value) != "" {
		compat.ThinkingFormat = value
	}
	if value, ok := model.Compat["cacheControlFormat"].(string); ok {
		compat.CacheControlFormat = strings.TrimSpace(value)
	}
	if value, ok := model.Compat["openRouterRouting"].(map[string]any); ok {
		compat.OpenRouterRouting = value
	}
	if value, ok := model.Compat["vercelGatewayRouting"].(map[string]any); ok {
		compat.VercelGatewayRouting = value
	}
	if value, ok := compatBool(model.Compat["requiresToolResultName"]); ok {
		compat.RequiresToolResultName = value
	}
	if value, ok := compatBool(model.Compat["requiresAssistantAfterToolResult"]); ok {
		compat.RequiresAssistantAfterToolResult = value
	}
	if value, ok := compatBool(model.Compat["requiresThinkingAsText"]); ok {
		compat.RequiresThinkingAsText = value
	}
	if value, ok := compatBool(model.Compat["requiresReasoningContentOnAssistantMessages"]); ok {
		compat.RequiresReasoningContentOnAssistantReply = value
	}
	if value, ok := compatStringMap(model.Compat["reasoningEffortMap"]); ok {
		compat.ReasoningEffortMap = value
	}
	return compat
}

func buildOpenAIChatCompletionsRequest(req CompletionRequest, model Model) map[string]any {
	compat := getOpenAICompat(model)
	payload := map[string]any{
		"model":    req.Model,
		"messages": toOpenAIMessages(req.Messages, model),
		"stream":   req.Options.Stream,
	}
	if compat.SupportsStore {
		payload["store"] = false
	}
	if sessionID := cacheSessionID(req.Options); sessionID != "" {
		retention := cacheRetentionValue(req.Options)
		if strings.Contains(strings.ToLower(model.BaseURL), "api.openai.com") || (retention == "24h" && compat.SupportsLongCacheRetention) {
			payload["prompt_cache_key"] = sessionID
		}
		if retention == "24h" && compat.SupportsLongCacheRetention {
			payload["prompt_cache_retention"] = retention
		}
	}
	if req.Options.MaxTokens > 0 {
		payload[compat.MaxTokensField] = req.Options.MaxTokens
	}
	if req.Options.Temperature != nil {
		payload["temperature"] = req.Options.Temperature
	}
	if req.Options.Stream && compat.SupportsUsageInStreaming {
		payload["stream_options"] = map[string]any{"include_usage": true}
	}
	if len(req.Tools) > 0 {
		payload["tools"] = buildOpenAIChatTools(req.Tools, compat)
		toolChoice := strings.TrimSpace(req.Options.ToolChoice)
		if toolChoice == "" {
			toolChoice = "auto"
		}
		payload["tool_choice"] = toolChoice
		if req.Options.ParallelToolCalls != nil {
			payload["parallel_tool_calls"] = *req.Options.ParallelToolCalls
		}
		if compat.ZAIToolStream {
			payload["tool_stream"] = true
		}
	} else if hasOpenAIToolHistory(req.Messages) {
		payload["tools"] = []any{}
	}
	applyOpenAICacheControl(payload, compat, req.Options.CacheRetention)
	applyOpenAIReasoningOptions(payload, req.Options, model, compat)
	if len(compat.OpenRouterRouting) > 0 && (canonicalProviderName(model.Provider) == "openrouter" || strings.Contains(strings.ToLower(model.BaseURL), "openrouter.ai")) {
		payload["provider"] = compat.OpenRouterRouting
	}
	if len(compat.VercelGatewayRouting) > 0 && (canonicalProviderName(model.Provider) == "vercel-ai-gateway" || strings.Contains(strings.ToLower(model.BaseURL), "ai-gateway.vercel.sh")) {
		gateway := map[string]any{}
		if value, ok := compat.VercelGatewayRouting["only"]; ok {
			gateway["only"] = value
		}
		if value, ok := compat.VercelGatewayRouting["order"]; ok {
			gateway["order"] = value
		}
		if len(gateway) > 0 {
			payload["providerOptions"] = map[string]any{"gateway": gateway}
		}
	}
	return payload
}

func buildOpenAIChatTools(tools []Tool, compat openAICompat) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		parameters := tool.Parameters
		if parameters == nil {
			parameters = map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": true,
			}
		}
		function := map[string]any{
			"name":        tool.Name,
			"description": strings.TrimSpace(tool.Description),
			"parameters":  parameters,
		}
		if compat.SupportsStrictMode {
			function["strict"] = false
		}
		out = append(out, map[string]any{
			"type":     "function",
			"function": function,
		})
	}
	return out
}

func applyOpenAIReasoningOptions(payload map[string]any, options ChatOptions, model Model, compat openAICompat) {
	effort := strings.TrimSpace(options.ReasoningEffort)
	if effort == "" || effort == "off" || !model.Reasoning {
		switch compat.ThinkingFormat {
		case "deepseek":
			payload["thinking"] = map[string]any{"type": "disabled"}
		case "openrouter":
			payload["reasoning"] = map[string]any{"effort": "none"}
		}
		return
	}
	mappedEffort := mapOpenAIReasoningEffort(effort, compat, model)
	switch compat.ThinkingFormat {
	case "zai", "qwen":
		payload["enable_thinking"] = true
	case "qwen-chat-template":
		payload["chat_template_kwargs"] = map[string]any{
			"enable_thinking":   true,
			"preserve_thinking": true,
		}
	case "deepseek":
		payload["thinking"] = map[string]any{"type": "enabled"}
		if compat.SupportsReasoningEffort {
			payload["reasoning_effort"] = mappedEffort
		}
	case "openrouter":
		payload["reasoning"] = map[string]any{"effort": mappedEffort}
	default:
		if compat.SupportsReasoningEffort {
			payload["reasoning_effort"] = mappedEffort
		}
	}
}

func hasOpenAIToolHistory(messages []Message) bool {
	for _, message := range messages {
		if message.Role == "toolResult" {
			return true
		}
		if message.Role != "assistant" {
			continue
		}
		for _, block := range messageContentBlocks(message.Content) {
			if block.Type == "toolCall" {
				return true
			}
		}
	}
	return false
}

func mapOpenAIReasoningEffort(effort string, compat openAICompat, model Model) string {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return ""
	}
	if mapped, ok := compat.ReasoningEffortMap[effort]; ok && strings.TrimSpace(mapped) != "" {
		return mapped
	}
	if !compat.SupportsReasoningEffort && effort == "xhigh" {
		return "high"
	}
	return effort
}

func compatStringMap(value any) (map[string]string, bool) {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(raw))
	for key, item := range raw {
		str, ok := item.(string)
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		str = strings.TrimSpace(str)
		if key == "" || str == "" {
			continue
		}
		out[key] = str
	}
	return out, true
}

func formatOpenAIHTTPError(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw
	}
	if str := strings.TrimSpace(asString(payload["error"])); str != "" {
		return str
	}
	errorMap, _ := payload["error"].(map[string]any)
	if len(errorMap) == 0 {
		return raw
	}
	message := strings.TrimSpace(asString(errorMap["message"]))
	rawMetadata := ""
	if metadata, ok := errorMap["metadata"].(map[string]any); ok {
		rawMetadata = strings.TrimSpace(asString(metadata["raw"]))
	}
	switch {
	case message != "" && rawMetadata != "":
		return message + "\n" + rawMetadata
	case message != "":
		return message
	case rawMetadata != "":
		return rawMetadata
	default:
		return raw
	}
}

func applyOpenAICacheControl(payload map[string]any, compat openAICompat, retention CacheRetention) {
	if compat.CacheControlFormat != "anthropic" || retention == CacheRetentionNone {
		return
	}

	cacheControl := map[string]any{"type": "ephemeral"}
	if retention == CacheRetentionLong && compat.SupportsLongCacheRetention {
		cacheControl["ttl"] = "1h"
	}

	if tools, ok := payload["tools"].([]map[string]any); ok && len(tools) > 0 {
		tools[len(tools)-1]["cache_control"] = cacheControl
		payload["tools"] = tools
	}

	messages, ok := payload["messages"].([]openAIMessage)
	if !ok || len(messages) == 0 {
		return
	}

	for i := range messages {
		if messages[i].Role == "system" {
			messages[i].Content = wrapOpenAIContentWithCacheControl(messages[i].Content, cacheControl)
			break
		}
	}

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" && messages[i].Role != "assistant" {
			continue
		}
		updated, changed := appendOpenAIContentCacheControl(messages[i].Content, cacheControl)
		if changed {
			messages[i].Content = updated
			break
		}
	}

	payload["messages"] = messages
}

func wrapOpenAIContentWithCacheControl(content any, cacheControl map[string]any) any {
	if text, ok := content.(string); ok {
		if text == "" {
			return content
		}
		return []map[string]any{{
			"type":          "text",
			"text":          text,
			"cache_control": cacheControl,
		}}
	}
	updated, changed := appendOpenAIContentCacheControl(content, cacheControl)
	if changed {
		return updated
	}
	return content
}

func appendOpenAIContentCacheControl(content any, cacheControl map[string]any) (any, bool) {
	switch typed := content.(type) {
	case []map[string]any:
		for i := len(typed) - 1; i >= 0; i-- {
			if asString(typed[i]["type"]) != "text" {
				continue
			}
			copyItem := map[string]any{}
			for key, value := range typed[i] {
				copyItem[key] = value
			}
			copyItem["cache_control"] = cacheControl
			typed[i] = copyItem
			return typed, true
		}
	case []any:
		for i := len(typed) - 1; i >= 0; i-- {
			item, ok := typed[i].(map[string]any)
			if !ok || asString(item["type"]) != "text" {
				continue
			}
			copyItem := map[string]any{}
			for key, value := range item {
				copyItem[key] = value
			}
			copyItem["cache_control"] = cacheControl
			typed[i] = copyItem
			return typed, true
		}
	}
	return content, false
}

func compatBool(value any) (bool, bool) {
	boolean, ok := value.(bool)
	return boolean, ok
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
