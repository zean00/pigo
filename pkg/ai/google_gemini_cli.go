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
	"strings"
	"time"
)

const (
	defaultGoogleCLIBaseURL      = "https://cloudcode-pa.googleapis.com"
	defaultAntigravityBaseURL    = "https://daily-cloudcode-pa.sandbox.googleapis.com"
	defaultAntigravityVersion    = "1.21.9"
	antigravitySystemInstruction = "You are Antigravity, a powerful agentic AI coding assistant designed by the Google Deepmind team working on Advanced Agentic Coding. You are pair programming with a USER to solve their coding task. The task may require creating a new codebase, modifying or debugging an existing codebase, or simply answering a question. **Absolute paths only** **Proactiveness**"
	googleCLIDefaultMaxRetries   = 3
)

var geminiCLIHeaders = map[string]string{
	"User-Agent":        "google-cloud-sdk vscode_cloudshelleditor/0.1",
	"X-Goog-Api-Client": "gl-node/22.17.0",
	"Client-Metadata":   `{"ideType":"IDE_UNSPECIFIED","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}`,
}

type googleGeminiCLIProvider struct{}

type googleCLICredentials struct {
	Token     string `json:"token"`
	ProjectID string `json:"projectId"`
}

type cloudCodeAssistRequest struct {
	Project     string                 `json:"project"`
	Model       string                 `json:"model"`
	Request     cloudCodeAssistPayload `json:"request"`
	RequestType string                 `json:"requestType,omitempty"`
	UserAgent   string                 `json:"userAgent,omitempty"`
	RequestID   string                 `json:"requestId,omitempty"`
}

type cloudCodeAssistPayload struct {
	Contents          []googleContent         `json:"contents"`
	SessionID         string                  `json:"sessionId,omitempty"`
	SystemInstruction *googleContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *googleGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []googleTool            `json:"tools,omitempty"`
	ToolConfig        *googleToolConfig       `json:"toolConfig,omitempty"`
}

type cloudCodeAssistChunk struct {
	Response *googleResponse `json:"response,omitempty"`
	TraceID  string          `json:"traceId,omitempty"`
}

func GoogleGeminiCLIProvider() ChatProvider {
	return &googleGeminiCLIProvider{}
}

func (provider *googleGeminiCLIProvider) Complete(ctx context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	providerSpec, hasProviderSpec := ProviderSpecForProvider(req.Provider)

	apiKey := strings.TrimSpace(req.Options.APIKey)
	if apiKey == "" && hasProviderSpec {
		apiKey, _ = ProviderAPIKey(req.Provider)
	}
	if apiKey == "" {
		providerName := strings.TrimSpace(req.Provider)
		if providerName == "" {
			providerName = "google-gemini-cli"
		}
		return NormalizedResult{}, nil, fmt.Errorf("missing API key for provider: %s", providerName)
	}
	credentials, err := parseGoogleCLICredentials(apiKey)
	if err != nil {
		return NormalizedResult{}, nil, err
	}
	if strings.TrimSpace(req.Model) == "" {
		return NormalizedResult{}, nil, errors.New("model is required")
	}

	baseURL := strings.TrimSpace(req.Options.BaseURL)
	if baseURL == "" && hasProviderSpec {
		baseURL = strings.TrimSpace(providerSpec.BaseURL)
	}
	if baseURL == "" {
		if canonicalProviderName(req.Provider) == "google-antigravity" {
			baseURL = defaultAntigravityBaseURL
		} else {
			baseURL = defaultGoogleCLIBaseURL
		}
	}
	requestURL := strings.TrimRight(baseURL, "/") + "/v1internal:streamGenerateContent?alt=sse"
	payload := buildGoogleCLIRequest(req, credentials.ProjectID, canonicalProviderName(req.Provider) == "google-antigravity")
	payloadValue, err := applyPayloadHook(req, payload)
	if err != nil {
		return NormalizedResult{}, nil, err
	}
	data, err := json.Marshal(payload)
	if payloadValue != nil {
		data, err = json.Marshal(payloadValue)
	}
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("marshal google-gemini-cli payload: %w", err)
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
			return nil, fmt.Errorf("create google-gemini-cli request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		httpReq.Header.Set("Authorization", "Bearer "+credentials.Token)
		for key, value := range defaultGoogleCLIProviderHeaders(canonicalProviderName(req.Provider)) {
			httpReq.Header.Set(key, value)
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

	maxRetries := retryLimit(req.Options, googleCLIDefaultMaxRetries)
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
			if attempt < maxRetries && shouldRetryGoogleCLI(0, err.Error()) {
				if sleepErr := sleepWithContext(ctx, retryDelayForAttempt(attempt, 0, defaultProviderBaseDelay)); sleepErr != nil {
					return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: sleepErr.Error()}, nil, sleepErr
				}
				continue
			}
			return NormalizedResult{}, nil, fmt.Errorf("call google-gemini-cli API: %w", err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if hookErr := notifyResponseHook(req, resp); hookErr != nil {
				_ = resp.Body.Close()
				return NormalizedResult{}, nil, hookErr
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			errorText := strings.TrimSpace(string(body))
			if attempt < maxRetries && shouldRetryGoogleCLI(resp.StatusCode, errorText) {
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
				return NormalizedResult{}, nil, fmt.Errorf("google-gemini-cli API error: %s", resp.Status)
			}
			return NormalizedResult{}, nil, fmt.Errorf("google-gemini-cli API error: %s: %s", resp.Status, errorText)
		}
		if hookErr := notifyResponseHook(req, resp); hookErr != nil {
			_ = resp.Body.Close()
			return NormalizedResult{}, nil, hookErr
		}

		result, events, err := googleCLISSEToResult(resp.Body)
		_ = resp.Body.Close()
		if err == nil {
			return result, events, nil
		}
		if attempt < maxRetries && isRetriableStreamError(err) {
			if sleepErr := sleepWithContext(ctx, retryDelayForAttempt(attempt, 0, defaultProviderBaseDelay)); sleepErr != nil {
				return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: sleepErr.Error()}, nil, sleepErr
			}
			continue
		}
		return NormalizedResult{}, nil, err
	}
	return NormalizedResult{}, nil, errors.New("google-gemini-cli retry budget exhausted")
}

func parseGoogleCLICredentials(apiKey string) (googleCLICredentials, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return googleCLICredentials{}, errors.New("google-gemini-cli requires OAuth credentials")
	}

	var credentials googleCLICredentials
	if strings.HasPrefix(apiKey, "{") {
		if err := json.Unmarshal([]byte(apiKey), &credentials); err != nil {
			return googleCLICredentials{}, errors.New("invalid Google Cloud Code Assist credentials")
		}
		if strings.TrimSpace(credentials.Token) == "" {
			return googleCLICredentials{}, errors.New("missing token in Google Cloud Code Assist credentials")
		}
		if strings.TrimSpace(credentials.ProjectID) == "" {
			credentials.ProjectID = strings.TrimSpace(getenv("GOOGLE_CLOUD_PROJECT"))
			if credentials.ProjectID == "" {
				credentials.ProjectID = strings.TrimSpace(getenv("GCLOUD_PROJECT"))
			}
		}
		if strings.TrimSpace(credentials.ProjectID) == "" {
			return googleCLICredentials{}, errors.New("missing projectId in Google Cloud Code Assist credentials")
		}
		return credentials, nil
	}

	projectID := strings.TrimSpace(getenv("GOOGLE_CLOUD_PROJECT"))
	if projectID == "" {
		projectID = strings.TrimSpace(getenv("GCLOUD_PROJECT"))
	}
	if projectID == "" {
		return googleCLICredentials{}, errors.New("google-gemini-cli raw token auth requires GOOGLE_CLOUD_PROJECT or GCLOUD_PROJECT")
	}
	return googleCLICredentials{Token: apiKey, ProjectID: projectID}, nil
}

func defaultGoogleCLIProviderHeaders(provider string) map[string]string {
	if provider == "google-antigravity" {
		version := strings.TrimSpace(getenv("PI_AI_ANTIGRAVITY_VERSION"))
		if version == "" {
			version = defaultAntigravityVersion
		}
		return map[string]string{
			"User-Agent": "antigravity/" + version + " darwin/arm64",
		}
	}

	headers := make(map[string]string, len(geminiCLIHeaders))
	for key, value := range geminiCLIHeaders {
		headers[key] = value
	}
	return headers
}

func buildGoogleCLIRequest(req CompletionRequest, projectID string, isAntigravity bool) cloudCodeAssistRequest {
	request := cloudCodeAssistPayload{
		Contents: toGoogleContents(req.Messages),
	}
	if prompt := strings.TrimSpace(extractSystemPrompt(req.Messages)); prompt != "" {
		request.SystemInstruction = &googleContent{
			Parts: []googlePart{{Text: prompt}},
		}
	}
	if len(req.Tools) > 0 {
		declarations := make([]googleFunctionDeclaration, 0, len(req.Tools))
		for _, tool := range req.Tools {
			parameters := tool.Parameters
			if parameters == nil {
				parameters = map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"additionalProperties": true,
				}
			}
			declarations = append(declarations, googleFunctionDeclaration{
				Name:        tool.Name,
				Description: strings.TrimSpace(tool.Description),
				Parameters:  parameters,
			})
		}
		request.Tools = []googleTool{{FunctionDeclarations: declarations}}
		if toolChoice := strings.TrimSpace(req.Options.ToolChoice); toolChoice != "" {
			request.ToolConfig = &googleToolConfig{
				FunctionCallingConfig: googleFunctionCallingConfig{
					Mode: mapGoogleToolChoice(toolChoice),
				},
			}
		}
	}
	if req.Options.Temperature != nil || req.Options.MaxTokens > 0 {
		config := &googleGenerationConfig{Temperature: req.Options.Temperature}
		if req.Options.MaxTokens > 0 {
			config.MaxOutputTokens = req.Options.MaxTokens
		}
		request.GenerationConfig = config
	}
	if isAntigravity {
		existingParts := []googlePart{}
		if request.SystemInstruction != nil {
			existingParts = request.SystemInstruction.Parts
		}
		request.SystemInstruction = &googleContent{
			Role: "user",
			Parts: append([]googlePart{
				{Text: antigravitySystemInstruction},
				{Text: "Please ignore following [ignore]" + antigravitySystemInstruction + "[/ignore]"},
			}, existingParts...),
		}
	}

	result := cloudCodeAssistRequest{
		Project:   projectID,
		Model:     req.Model,
		Request:   request,
		RequestID: "pi-request",
		UserAgent: "pi-coding-agent",
	}
	if sessionID := strings.TrimSpace(req.Options.SessionID); sessionID != "" {
		result.Request.SessionID = sessionID
		result.RequestID = sessionID
	}
	if isAntigravity {
		result.RequestType = "agent"
		result.UserAgent = "antigravity"
		if strings.TrimSpace(req.Options.SessionID) == "" {
			result.RequestID = "agent-request"
		}
	}
	return result
}

func googleCLISSEToResult(body io.Reader) (NormalizedResult, []NormalizedEvent, error) {
	scanner := bufio.NewScanner(body)
	var final googleResponse
	seen := false
	events := []NormalizedEvent{{Type: "start"}}
	contentIndex := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var chunk cloudCodeAssistChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return NormalizedResult{}, nil, fmt.Errorf("parse google-gemini-cli stream chunk: %w", err)
		}
		if chunk.Response == nil {
			continue
		}
		if len(chunk.Response.Candidates) > 0 && chunk.Response.Candidates[0].Content != nil {
			for _, part := range chunk.Response.Candidates[0].Content.Parts {
				switch {
				case part.FunctionCall != nil:
					arguments := part.FunctionCall.Args
					if arguments == nil {
						arguments = map[string]any{}
					}
					events = append(events,
						NormalizedEvent{Type: "toolcall_start", ContentIdx: contentIndex},
						NormalizedEvent{Type: "toolcall_delta", ContentIdx: contentIndex, Delta: mustJSON(arguments)},
						NormalizedEvent{
							Type:       "toolcall_end",
							ContentIdx: contentIndex,
							ToolCall: &NormalizedTool{
								ID:               part.FunctionCall.ID,
								Name:             part.FunctionCall.Name,
								Arguments:        arguments,
								HasID:            strings.TrimSpace(part.FunctionCall.ID) != "",
								ThoughtSignature: part.ThoughtSignature,
							},
						},
					)
					contentIndex++
				case strings.TrimSpace(part.Text) != "":
					eventType := "text"
					if part.Thought {
						eventType = "thinking"
					}
					events = append(events,
						NormalizedEvent{Type: eventType + "_start", ContentIdx: contentIndex},
						NormalizedEvent{Type: eventType + "_delta", ContentIdx: contentIndex, Delta: part.Text},
						NormalizedEvent{Type: eventType + "_end", ContentIdx: contentIndex, Content: part.Text},
					)
					contentIndex++
				}
			}
		}
		final = mergeGoogleStreamResponse(final, *chunk.Response)
		seen = true
	}
	if err := scanner.Err(); err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("scan google-gemini-cli stream: %w", err)
	}
	if !seen {
		return NormalizedResult{}, nil, errors.New("google-gemini-cli returned an empty response")
	}
	result := googleResponseToNormalized(final)
	return result, appendTerminalEvent(events, result.StopReason), nil
}

func mergeGoogleStreamResponse(base, next googleResponse) googleResponse {
	if next.ResponseID != "" {
		base.ResponseID = next.ResponseID
	}
	if next.UsageMetadata != nil {
		base.UsageMetadata = next.UsageMetadata
	}
	for i, candidate := range next.Candidates {
		if i >= len(base.Candidates) {
			base.Candidates = append(base.Candidates, candidate)
			continue
		}
		baseCandidate := &base.Candidates[i]
		if candidate.FinishReason != "" {
			baseCandidate.FinishReason = candidate.FinishReason
		}
		if candidate.Content == nil {
			continue
		}
		if baseCandidate.Content == nil {
			content := *candidate.Content
			content.Parts = append([]googlePart(nil), candidate.Content.Parts...)
			baseCandidate.Content = &content
			continue
		}
		if candidate.Content.Role != "" {
			baseCandidate.Content.Role = candidate.Content.Role
		}
		baseCandidate.Content.Parts = append(baseCandidate.Content.Parts, candidate.Content.Parts...)
	}
	return base
}

func shouldRetryGoogleCLI(status int, errorText string) bool {
	if status == http.StatusTooManyRequests ||
		status == http.StatusInternalServerError ||
		status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout {
		return true
	}
	text := strings.ToLower(errorText)
	return strings.Contains(text, "resource exhausted") ||
		strings.Contains(text, "rate limit") ||
		strings.Contains(text, "overloaded") ||
		strings.Contains(text, "service unavailable") ||
		strings.Contains(text, "other side closed")
}

func retryDelayFromText(errorText string) time.Duration {
	lower := strings.ToLower(errorText)
	if idx := strings.Index(lower, "please retry in "); idx >= 0 {
		part := lower[idx+len("please retry in "):]
		fields := strings.Fields(part)
		if len(fields) > 0 {
			value := fields[0]
			if strings.HasSuffix(value, "ms") {
				if duration, err := time.ParseDuration(value); err == nil {
					return duration + time.Second
				}
			}
			if strings.HasSuffix(value, "s") {
				if duration, err := time.ParseDuration(value); err == nil {
					return duration + time.Second
				}
			}
		}
	}
	return 0
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
