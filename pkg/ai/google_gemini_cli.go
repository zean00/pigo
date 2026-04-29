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
)

const (
	defaultGoogleCLIBaseURL      = "https://cloudcode-pa.googleapis.com"
	defaultAntigravityBaseURL    = "https://daily-cloudcode-pa.sandbox.googleapis.com"
	defaultAntigravityVersion    = "1.21.9"
	antigravitySystemInstruction = "You are Antigravity, a powerful agentic AI coding assistant designed by the Google Deepmind team working on Advanced Agentic Coding. You are pair programming with a USER to solve their coding task. The task may require creating a new codebase, modifying or debugging an existing codebase, or simply answering a question. **Absolute paths only** **Proactiveness**"
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
	if req.Options.Stream {
		return NormalizedResult{}, nil, errors.New("streaming is not supported for google-gemini-cli providers yet")
	}

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
	data, err := json.Marshal(payload)
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(data))
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("create google-gemini-cli request: %w", err)
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

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: err.Error()}, nil, err
		}
		return NormalizedResult{}, nil, fmt.Errorf("call google-gemini-cli API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		if len(body) == 0 {
			return NormalizedResult{}, nil, fmt.Errorf("google-gemini-cli API error: %s", resp.Status)
		}
		return NormalizedResult{}, nil, fmt.Errorf("google-gemini-cli API error: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	result, err := googleCLISSEToResult(resp.Body)
	if err != nil {
		return NormalizedResult{}, nil, err
	}
	return result, AssistantEvents(result.contentBlocks(), result.StopReason), nil
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
		UserAgent: "pi-coding-agent",
		RequestID: "pi-request",
	}
	if isAntigravity {
		result.RequestType = "agent"
		result.UserAgent = "antigravity"
		result.RequestID = "agent-request"
	}
	return result
}

func googleCLISSEToResult(body io.Reader) (NormalizedResult, error) {
	scanner := bufio.NewScanner(body)
	var final googleResponse
	seen := false

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
			return NormalizedResult{}, fmt.Errorf("parse google-gemini-cli stream chunk: %w", err)
		}
		if chunk.Response == nil {
			continue
		}
		final = mergeGoogleStreamResponse(final, *chunk.Response)
		seen = true
	}
	if err := scanner.Err(); err != nil {
		return NormalizedResult{}, fmt.Errorf("scan google-gemini-cli stream: %w", err)
	}
	if !seen {
		return NormalizedResult{}, errors.New("google-gemini-cli returned an empty response")
	}
	return googleResponseToNormalized(final), nil
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
