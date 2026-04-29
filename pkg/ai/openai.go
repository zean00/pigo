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
	"os"
	"sort"
	"strings"
)

const (
	openAIProviderName   = "openai"
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
)

type openAIRequest struct {
	Model       string           `json:"model"`
	Messages    []openAIMessage  `json:"messages"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	Tools       []openAIChatTool `json:"tools,omitempty"`
	ToolChoice  string           `json:"tool_choice,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
}

type openAIResponse struct {
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
	Choices []openAIStreamChoice `json:"choices"`
}

type openAIStreamChoice struct {
	Delta        openAIMessage `json:"delta"`
	FinishReason string        `json:"finish_reason"`
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
	RegisterProvider(openAIProviderName, OpenAIProvider())
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
	apiKey := strings.TrimSpace(req.Options.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if apiKey == "" {
		return NormalizedResult{}, nil, errors.New("missing OPENAI_API_KEY")
	}
	if strings.TrimSpace(req.Model) == "" {
		return NormalizedResult{}, nil, errors.New("model is required")
	}

	baseURL := strings.TrimSpace(req.Options.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
		if baseURL == "" {
			baseURL = strings.TrimSpace(provider.defaultBaseURL)
		}
	}
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/chat/completions") {
		baseURL = baseURL + "/chat/completions"
	}

	payload := openAIRequest{
		Model:    req.Model,
		Messages: toOpenAIMessages(req.Messages),
		Stream:   req.Options.Stream,
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

	data, err := json.Marshal(payload)
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(data))
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("create openai request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: err.Error()}, nil, err
		}
		return NormalizedResult{}, nil, fmt.Errorf("call openai API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		if len(body) == 0 {
			return NormalizedResult{}, nil, fmt.Errorf("openai API error: %s", resp.Status)
		}
		return NormalizedResult{}, nil, fmt.Errorf("openai API error: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	if req.Options.Stream {
		result, events, streamErr := openAIStreamToResult(resp.Body)
		if streamErr != nil {
			return NormalizedResult{}, nil, streamErr
		}
		return result, events, nil
	}

	var completion openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("parse openai response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return NormalizedResult{Role: "assistant", StopReason: "error", ErrorMessage: "openai response missing choices"}, nil,
			errors.New("openai response missing choices")
	}

	result := openAIChoiceToNormalized(completion.Choices[0])
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

func openAIStreamToResult(body io.Reader) (NormalizedResult, []NormalizedEvent, error) {
	scanner := bufio.NewScanner(body)
	builder := strings.Builder{}
	toolCalls := map[int]*openAIStreamingCall{}
	stopReason := "stop"

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
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				stopReason = choice.FinishReason
			}
			if text, ok := choice.Delta.Content.(string); ok && text != "" {
				builder.WriteString(text)
			}
			for _, toolCall := range choice.Delta.ToolCalls {
				entry, ok := toolCalls[toolCall.Index]
				if !ok {
					entry = &openAIStreamingCall{}
					toolCalls[toolCall.Index] = entry
				}
				if toolCall.ID != "" {
					entry.ID = toolCall.ID
				}
				if toolCall.Function.Name != "" {
					entry.Name = toolCall.Function.Name
				}
				entry.Arguments.WriteString(toolCall.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("scan openai stream: %w", err)
	}

	blocks := []ContentBlock{}
	if strings.TrimSpace(builder.String()) != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: builder.String()})
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
		Text:       ContentText(blocks),
		Content:    NormalizedContent(blocks),
	}
	return result, AssistantEvents(blocks, result.StopReason), nil
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
