package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const defaultGoogleBaseURL = "https://generativelanguage.googleapis.com/v1beta"

type googleProvider struct{}

type googleRequest struct {
	Contents          []googleContent         `json:"contents"`
	SystemInstruction *googleContent          `json:"systemInstruction,omitempty"`
	Tools             []googleTool            `json:"tools,omitempty"`
	ToolConfig        *googleToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *googleGenerationConfig `json:"generationConfig,omitempty"`
}

type googleContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []googlePart `json:"parts"`
}

type googlePart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	InlineData       *googleInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *googleFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *googleFunctionResponse `json:"functionResponse,omitempty"`
}

type googleInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type googleFunctionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name,omitempty"`
	Args map[string]any `json:"args,omitempty"`
}

type googleFunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type googleTool struct {
	FunctionDeclarations []googleFunctionDeclaration `json:"functionDeclarations"`
}

type googleFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type googleToolConfig struct {
	FunctionCallingConfig googleFunctionCallingConfig `json:"functionCallingConfig"`
}

type googleFunctionCallingConfig struct {
	Mode string `json:"mode"`
}

type googleGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
}

type googleResponse struct {
	Candidates    []googleCandidate    `json:"candidates"`
	UsageMetadata *googleUsageMetadata `json:"usageMetadata,omitempty"`
	ResponseID    string               `json:"responseId,omitempty"`
}

type googleCandidate struct {
	Content      *googleContent `json:"content,omitempty"`
	FinishReason string         `json:"finishReason,omitempty"`
}

type googleUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
}

func GoogleProvider() ChatProvider {
	return &googleProvider{}
}

func (provider *googleProvider) Complete(ctx context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	providerSpec, hasProviderSpec := ProviderSpecForProvider(req.Provider)

	apiKey := strings.TrimSpace(req.Options.APIKey)
	if apiKey == "" && hasProviderSpec {
		apiKey, _ = ProviderAPIKey(req.Provider)
	}
	if apiKey == "" {
		providerName := strings.TrimSpace(req.Provider)
		if providerName == "" {
			providerName = "google"
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
		baseURL = defaultGoogleBaseURL
	}
	requestURL, err := buildGoogleURL(baseURL, req.Model, apiKey, req.Options.Stream)
	if err != nil {
		return NormalizedResult{}, nil, err
	}

	payload := toGoogleRequest(req)
	data, err := json.Marshal(payload)
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("marshal google payload: %w", err)
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
		return NormalizedResult{}, nil, fmt.Errorf("create google request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
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
		return NormalizedResult{}, nil, fmt.Errorf("call google API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		if len(body) == 0 {
			return NormalizedResult{}, nil, fmt.Errorf("google API error: %s", resp.Status)
		}
		return NormalizedResult{}, nil, fmt.Errorf("google API error: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	if req.Options.Stream {
		result, err := googleSSEToResult(resp.Body)
		if err != nil {
			return NormalizedResult{}, nil, err
		}
		return result, AssistantEvents(result.contentBlocks(), result.StopReason), nil
	}

	var completion googleResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("parse google response: %w", err)
	}
	result := googleResponseToNormalized(completion)
	return result, AssistantEvents(result.contentBlocks(), result.StopReason), nil
}

func buildGoogleURL(baseURL, model, apiKey string, stream bool) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultGoogleBaseURL
	}
	suffix := ":generateContent"
	if stream {
		suffix = ":streamGenerateContent"
	}
	requestURL := baseURL + "/models/" + url.PathEscape(model) + suffix
	parsed, err := url.Parse(requestURL)
	if err != nil {
		return "", fmt.Errorf("invalid google base URL: %w", err)
	}
	query := parsed.Query()
	query.Set("key", apiKey)
	if stream {
		query.Set("alt", "sse")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func googleSSEToResult(body io.Reader) (NormalizedResult, error) {
	var final googleResponse
	seen := false
	err := scanSSE(body, func(event sseEvent) error {
		payload := strings.TrimSpace(event.Data)
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		var chunk googleResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("parse google stream chunk: %w", err)
		}
		final = mergeGoogleStreamResponse(final, chunk)
		seen = true
		return nil
	})
	if err != nil {
		return NormalizedResult{}, err
	}
	if !seen {
		return NormalizedResult{}, errors.New("google returned an empty response")
	}
	return googleResponseToNormalized(final), nil
}

func toGoogleRequest(req CompletionRequest) googleRequest {
	payload := googleRequest{
		Contents: toGoogleContents(req.Messages),
	}
	if prompt := strings.TrimSpace(extractSystemPrompt(req.Messages)); prompt != "" {
		payload.SystemInstruction = &googleContent{
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
		payload.Tools = []googleTool{{FunctionDeclarations: declarations}}
		if toolChoice := strings.TrimSpace(req.Options.ToolChoice); toolChoice != "" {
			payload.ToolConfig = &googleToolConfig{
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
		payload.GenerationConfig = config
	}
	return payload
}

func toGoogleContents(messages []Message) []googleContent {
	out := make([]googleContent, 0, len(messages))
	for _, message := range messages {
		switch {
		case strings.EqualFold(message.Role, "user"):
			if parts := googleUserParts(message); len(parts) > 0 {
				out = append(out, googleContent{Role: "user", Parts: parts})
			}
		case strings.EqualFold(message.Role, "assistant"):
			if parts := googleAssistantParts(message); len(parts) > 0 {
				out = append(out, googleContent{Role: "model", Parts: parts})
			}
		case strings.EqualFold(message.Role, "toolResult"):
			if parts := googleToolResultParts(message); len(parts) > 0 {
				out = append(out, googleContent{Role: "user", Parts: parts})
			}
		}
	}
	return out
}

func googleUserParts(message Message) []googlePart {
	blocks := messageContentBlocks(message.Content)
	if len(blocks) == 0 {
		text := strings.TrimSpace(MessageText(message))
		if text == "" {
			return nil
		}
		return []googlePart{{Text: text}}
	}
	parts := make([]googlePart, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, googlePart{Text: block.Text})
			}
		case "image":
			if strings.TrimSpace(block.Data) != "" && strings.TrimSpace(block.MimeType) != "" {
				parts = append(parts, googlePart{
					InlineData: &googleInlineData{
						MimeType: block.MimeType,
						Data:     block.Data,
					},
				})
			}
		}
	}
	return parts
}

func googleAssistantParts(message Message) []googlePart {
	blocks := messageContentBlocks(message.Content)
	if len(blocks) == 0 {
		text := strings.TrimSpace(MessageText(message))
		if text == "" {
			return nil
		}
		return []googlePart{{Text: text}}
	}
	parts := make([]googlePart, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, googlePart{Text: block.Text})
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				parts = append(parts, googlePart{Text: block.Thinking, Thought: true})
			}
		case "toolCall":
			parts = append(parts, googlePart{
				FunctionCall: &googleFunctionCall{
					ID:   block.ID,
					Name: block.Name,
					Args: block.Arguments,
				},
			})
		}
	}
	return parts
}

func googleToolResultParts(message Message) []googlePart {
	responseValue := MessageText(message)
	if strings.TrimSpace(responseValue) == "" {
		responseValue = "(no tool output)"
	}
	return []googlePart{{
		FunctionResponse: &googleFunctionResponse{
			ID:   strings.TrimSpace(message.ToolCallID),
			Name: strings.TrimSpace(message.ToolName),
			Response: map[string]any{
				"value":   responseValue,
				"isError": message.IsError,
			},
		},
	}}
}

func extractSystemPrompt(messages []Message) string {
	for _, message := range messages {
		if strings.EqualFold(message.Role, "system") {
			return MessageText(message)
		}
	}
	return ""
}

func mapGoogleToolChoice(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none":
		return "NONE"
	case "any":
		return "ANY"
	default:
		return "AUTO"
	}
}

func googleResponseToNormalized(response googleResponse) NormalizedResult {
	blocks := make([]ContentBlock, 0)
	stopReason := "stop"
	if len(response.Candidates) > 0 {
		candidate := response.Candidates[0]
		stopReason = mapGoogleStopReason(candidate.FinishReason)
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				switch {
				case part.FunctionCall != nil:
					arguments := part.FunctionCall.Args
					if arguments == nil {
						arguments = map[string]any{}
					}
					blocks = append(blocks, ContentBlock{
						Type:      "toolCall",
						ID:        part.FunctionCall.ID,
						Name:      part.FunctionCall.Name,
						Arguments: arguments,
					})
					stopReason = "toolUse"
				case strings.TrimSpace(part.Text) != "":
					if part.Thought {
						blocks = append(blocks, ContentBlock{Type: "thinking", Thinking: part.Text})
					} else {
						blocks = append(blocks, ContentBlock{Type: "text", Text: part.Text})
					}
				}
			}
		}
	}

	result := NormalizedResult{
		Role:       "assistant",
		StopReason: stopReason,
		Text:       ContentText(blocks),
		Content:    NormalizedContent(blocks),
	}
	if response.UsageMetadata != nil {
		input := response.UsageMetadata.PromptTokenCount - response.UsageMetadata.CachedContentTokenCount
		if input < 0 {
			input = 0
		}
		result.Usage = &Usage{
			Input:       input,
			Output:      response.UsageMetadata.CandidatesTokenCount + response.UsageMetadata.ThoughtsTokenCount,
			CacheRead:   response.UsageMetadata.CachedContentTokenCount,
			TotalTokens: response.UsageMetadata.TotalTokenCount,
		}
	}
	return result
}

func mapGoogleStopReason(reason string) string {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case "MAX_TOKENS":
		return "length"
	case "STOP", "FINISH_REASON_UNSPECIFIED":
		return "stop"
	case "SAFETY", "RECITATION", "OTHER", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "MALFORMED_FUNCTION_CALL":
		return "error"
	default:
		return "stop"
	}
}
