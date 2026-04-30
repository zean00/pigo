package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

const defaultOpenAICodexBaseURL = "https://chatgpt.com/backend-api"
const openAICodexResponsesBeta = "responses=experimental"
const openAICodexWebSocketBeta = "responses_websockets=2026-02-06"

type openAICodexProvider struct{}

type openAICodexRequest struct {
	Model          string           `json:"model"`
	Store          bool             `json:"store"`
	Stream         bool             `json:"stream,omitempty"`
	Instructions   string           `json:"instructions,omitempty"`
	Input          []any            `json:"input,omitempty"`
	Tools          []openAIChatTool `json:"tools,omitempty"`
	ToolChoice     string           `json:"tool_choice,omitempty"`
	Temperature    *float64         `json:"temperature,omitempty"`
	Reasoning      map[string]any   `json:"reasoning,omitempty"`
	ServiceTier    string           `json:"service_tier,omitempty"`
	Text           map[string]any   `json:"text,omitempty"`
	Include        []string         `json:"include,omitempty"`
	PromptCacheKey string           `json:"prompt_cache_key,omitempty"`
	ParallelTools  bool             `json:"parallel_tool_calls,omitempty"`
}

func OpenAICodexProvider() ChatProvider {
	return &openAICodexProvider{}
}

func (provider *openAICodexProvider) Complete(ctx context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	providerSpec, hasProviderSpec := ProviderSpecForProvider(req.Provider)

	apiKey := strings.TrimSpace(req.Options.APIKey)
	if apiKey == "" && hasProviderSpec {
		apiKey, _ = ProviderAPIKey(req.Provider)
	}
	if apiKey == "" {
		providerName := strings.TrimSpace(req.Provider)
		if providerName == "" {
			providerName = "openai-codex"
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
		baseURL = defaultOpenAICodexBaseURL
	}
	requestURL := resolveCodexURL(baseURL)
	resolvedModel, _ := resolveCompletionModel(req)
	responsesRequest := toOpenAIResponsesRequest(req, resolvedModel)

	payload := openAICodexRequest{
		Model:         req.Model,
		Store:         false,
		Stream:        req.Options.Stream,
		Input:         responsesRequest.Input,
		Text:          map[string]any{"verbosity": codexTextVerbosity(req.Options)},
		Include:       []string{"reasoning.encrypted_content"},
		ToolChoice:    "auto",
		Temperature:   req.Options.Temperature,
		Instructions:  extractSystemPrompt(req.Messages),
		ParallelTools: len(req.Tools) > 0,
	}
	if req.Options.Stream {
		payload.Store = false
	}
	if req.Options.MaxTokens > 0 {
		payload.Text["max_output_tokens"] = req.Options.MaxTokens
	}
	if sessionID := strings.TrimSpace(req.Options.SessionID); sessionID != "" {
		payload.PromptCacheKey = sessionID
	}
	if reasoning := codexReasoning(req.Options, req.Model); reasoning != nil {
		payload.Reasoning = reasoning
	}
	if tier := strings.TrimSpace(req.Options.ServiceTier); tier != "" {
		payload.ServiceTier = tier
	}
	if len(req.Tools) > 0 {
		payload.Tools = responsesRequest.Tools
	}

	payloadValue, err := applyPayloadHook(req, payload)
	if err != nil {
		return NormalizedResult{}, nil, err
	}
	data, err := json.Marshal(payload)
	if payloadValue != nil {
		data, err = json.Marshal(payloadValue)
	}
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("marshal openai-codex payload: %w", err)
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
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("create openai-codex request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if req.Options.Stream {
			httpReq.Header.Set("Accept", "text/event-stream")
		}
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		httpReq.Header.Set("OpenAI-Beta", openAICodexResponsesBeta)
		if accountID := extractCodexAccountID(apiKey); accountID != "" {
			httpReq.Header.Set("chatgpt-account-id", accountID)
		}
		if sessionID := strings.TrimSpace(req.Options.SessionID); sessionID != "" {
			httpReq.Header.Set("session_id", sessionID)
			httpReq.Header.Set("x-client-request-id", sessionID)
		}
		for key, value := range req.Options.Headers {
			httpReq.Header.Set(key, value)
		}
		return httpReq, nil
	}

	buildWebSocketConfig := func() (*websocket.Config, error) {
		webSocketURL, err := resolveCodexWebSocketURL(requestURL)
		if err != nil {
			return nil, err
		}
		config, err := websocket.NewConfig(webSocketURL, resolveCodexOrigin(webSocketURL))
		if err != nil {
			return nil, fmt.Errorf("create openai-codex websocket config: %w", err)
		}
		config.Header = make(http.Header)
		config.Header.Set("Authorization", "Bearer "+apiKey)
		config.Header.Set("OpenAI-Beta", openAICodexWebSocketBeta)
		if accountID := extractCodexAccountID(apiKey); accountID != "" {
			config.Header.Set("chatgpt-account-id", accountID)
		}
		requestID := strings.TrimSpace(req.Options.SessionID)
		if requestID == "" {
			requestID = createCodexRequestID()
		}
		config.Header.Set("session_id", requestID)
		config.Header.Set("x-client-request-id", requestID)
		for key, value := range req.Options.Headers {
			config.Header.Set(key, value)
		}
		return config, nil
	}

	transport := strings.TrimSpace(req.Options.Transport)
	if transport == "" {
		transport = "sse"
	}

	maxRetries := retryLimit(req.Options, defaultProviderMaxRetries)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if req.Options.Stream && transport != "sse" {
			config, err := buildWebSocketConfig()
			if err != nil {
				return NormalizedResult{}, nil, err
			}
			result, events, wsErr := completeOpenAICodexWebSocket(ctx, config, data, req)
			if wsErr == nil {
				return result, events, nil
			}
			if transport == "websocket" {
				return NormalizedResult{}, nil, wsErr
			}
		}
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
			return NormalizedResult{}, nil, fmt.Errorf("call openai-codex API: %w", err)
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
				return NormalizedResult{}, nil, fmt.Errorf("openai-codex API error: %s", resp.Status)
			}
			return NormalizedResult{}, nil, fmt.Errorf("openai-codex API error: %s: %s", resp.Status, errorText)
		}
		if hookErr := notifyResponseHook(req, resp); hookErr != nil {
			_ = resp.Body.Close()
			return NormalizedResult{}, nil, hookErr
		}

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

	return NormalizedResult{}, nil, errors.New("openai-codex retry budget exhausted")
}

func resolveCodexURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch {
	case strings.HasSuffix(baseURL, "/codex/responses"):
		return baseURL
	case strings.HasSuffix(baseURL, "/codex"):
		return baseURL + "/responses"
	default:
		return baseURL + "/codex/responses"
	}
}

func resolveCodexWebSocketURL(requestURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(requestURL))
	if err != nil {
		return "", fmt.Errorf("parse openai-codex websocket URL: %w", err)
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "wss", "ws":
	default:
		return "", fmt.Errorf("unsupported openai-codex websocket scheme: %s", parsed.Scheme)
	}
	return parsed.String(), nil
}

func resolveCodexOrigin(webSocketURL string) string {
	parsed, err := url.Parse(webSocketURL)
	if err != nil {
		return "https://chatgpt.com"
	}
	switch parsed.Scheme {
	case "wss":
		parsed.Scheme = "https"
	case "ws":
		parsed.Scheme = "http"
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func completeOpenAICodexWebSocket(ctx context.Context, config *websocket.Config, payloadData []byte, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	conn, err := websocket.DialConfig(config)
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("dial openai-codex websocket: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if req.Options.Timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(req.Options.Timeout))
	}

	responsePayload := map[string]any{
		"type": "response.create",
	}
	if err := json.Unmarshal(payloadData, &responsePayload); err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("decode openai-codex websocket payload: %w", err)
	}
	if err := websocket.JSON.Send(conn, responsePayload); err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("send openai-codex websocket payload: %w", err)
	}

	if req.Options.OnResponse != nil {
		if err := req.Options.OnResponse(ProviderResponse{
			Status:  101,
			Headers: map[string]string{},
		}, req); err != nil {
			return NormalizedResult{}, nil, err
		}
	}

	resolvedModel, _ := resolveCompletionModel(req)
	return openAIResponsesEventsToResult(resolvedModel, func(handle func(map[string]any) error) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			var item map[string]any
			if err := websocket.JSON.Receive(conn, &item); err != nil {
				return fmt.Errorf("receive openai-codex websocket event: %w", err)
			}
			if err := handle(item); err != nil {
				return err
			}
			switch asString(item["type"]) {
			case "response.completed", "response.done", "response.incomplete":
				return nil
			}
		}
	})
}

func createCodexRequestID() string {
	return fmt.Sprintf("codex_%d", time.Now().UnixNano())
}

func extractCodexAccountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payloadBytes, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}

	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return ""
	}
	authValue, _ := payload["https://api.openai.com/auth"].(map[string]any)
	accountID, _ := authValue["chatgpt_account_id"].(string)
	return strings.TrimSpace(accountID)
}

func codexTextVerbosity(options ChatOptions) string {
	value := strings.TrimSpace(options.TextVerbosity)
	switch value {
	case "medium", "high":
		return value
	default:
		return "low"
	}
}

func codexReasoning(options ChatOptions, model string) map[string]any {
	effort := strings.TrimSpace(options.ReasoningEffort)
	if effort == "" {
		return nil
	}
	reasoning := map[string]any{
		"effort": clampCodexReasoningEffort(model, effort),
	}
	summary := strings.TrimSpace(options.ReasoningSummary)
	if summary == "" {
		summary = "auto"
	}
	reasoning["summary"] = summary
	return reasoning
}

func clampCodexReasoningEffort(model, effort string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	switch effort {
	case "none", "minimal", "low", "medium", "high", "xhigh":
	default:
		return "medium"
	}
	if (strings.HasPrefix(model, "gpt-5.2") || strings.HasPrefix(model, "gpt-5.3") || strings.HasPrefix(model, "gpt-5.4") || strings.HasPrefix(model, "gpt-5.5")) && effort == "minimal" {
		return "low"
	}
	if model == "gpt-5.1" && effort == "xhigh" {
		return "high"
	}
	if model == "gpt-5.1-codex-mini" && (effort == "high" || effort == "xhigh") {
		return "high"
	}
	if model == "gpt-5.1-codex-mini" {
		return "medium"
	}
	return effort
}
