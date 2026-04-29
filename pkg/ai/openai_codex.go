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
	"strings"
)

const defaultOpenAICodexBaseURL = "https://chatgpt.com/backend-api"

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
	Text           map[string]any   `json:"text,omitempty"`
	Include        []string         `json:"include,omitempty"`
	PromptCacheKey string           `json:"prompt_cache_key,omitempty"`
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

	payload := openAICodexRequest{
		Model:        req.Model,
		Store:        false,
		Stream:       req.Options.Stream,
		Input:        toOpenAIResponsesRequest(req).Input,
		Text:         map[string]any{"verbosity": "low"},
		Include:      []string{"reasoning.encrypted_content"},
		ToolChoice:   "auto",
		Temperature:  req.Options.Temperature,
		Instructions: "",
	}
	if req.Options.Stream {
		payload.Store = false
	}
	if req.Options.MaxTokens > 0 {
		payload.Text["max_output_tokens"] = req.Options.MaxTokens
	}
	if len(req.Tools) > 0 {
		payload.Tools = toOpenAIResponsesRequest(req).Tools
	}

	data, err := json.Marshal(payload)
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(data))
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("create openai-codex request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.Options.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("OpenAI-Beta", "responses=experimental")
	if accountID := extractCodexAccountID(apiKey); accountID != "" {
		httpReq.Header.Set("chatgpt-account-id", accountID)
	}
	for key, value := range req.Options.Headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: err.Error()}, nil, err
		}
		return NormalizedResult{}, nil, fmt.Errorf("call openai-codex API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		if len(body) == 0 {
			return NormalizedResult{}, nil, fmt.Errorf("openai-codex API error: %s", resp.Status)
		}
		return NormalizedResult{}, nil, fmt.Errorf("openai-codex API error: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	if req.Options.Stream {
		return openAIResponsesSSEToResult(resp.Body)
	}
	return openAIResponsesToResult(resp.Body)
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
