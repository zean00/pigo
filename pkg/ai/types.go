package ai

import (
	"context"
	"net/http"
	"time"
)

type ContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	Thinking  string         `json:"thinking,omitempty"`
	Redacted  bool           `json:"redacted,omitempty"`
	Data      string         `json:"data,omitempty"`
	MimeType  string         `json:"mimeType,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type Message struct {
	Role        string         `json:"role"`
	Content     any            `json:"content,omitempty"`
	ToolCallID  string         `json:"toolCallId,omitempty"`
	ToolName    string         `json:"toolName,omitempty"`
	IsError     bool           `json:"isError,omitempty"`
	StopReason  string         `json:"stopReason,omitempty"`
	ErrorText   string         `json:"errorMessage,omitempty"`
	ContentList []ContentBlock `json:"-"`
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ChatOptions struct {
	// Temperature controls sampling randomness.
	Temperature *float64
	// MaxTokens limits output token count.
	MaxTokens int
	// Stream requests server-side streaming.
	Stream bool
	// APIKey overrides environment credentials.
	APIKey string
	// BaseURL for the provider API endpoint.
	BaseURL string
	// HTTPClient allows tests and callers to inject transport settings.
	HTTPClient *http.Client
	// Timeout for requests when set.
	Timeout time.Duration
	// Headers provides request-time provider headers.
	Headers map[string]string
	// ToolChoice controls OpenAI-style tool call behavior.
	ToolChoice string
}

type CompletionRequest struct {
	Provider string
	Model    string
	Messages []Message
	Tools    []Tool
	Options  ChatOptions
}

type ChatProvider interface {
	Complete(ctx context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error)
}

type ProviderRegistry struct {
	factories map[string]func() ChatProvider
}

type Usage struct {
	Input       int `json:"input"`
	Output      int `json:"output"`
	CacheRead   int `json:"cacheRead"`
	CacheWrite  int `json:"cacheWrite"`
	TotalTokens int `json:"totalTokens"`
}

type NormalizedEvent struct {
	Type         string          `json:"type"`
	ContentIdx   int             `json:"contentIndex,omitempty"`
	Delta        string          `json:"delta,omitempty"`
	Content      string          `json:"content,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	ErrorMessage string          `json:"errorMessage,omitempty"`
	ToolCall     *NormalizedTool `json:"toolCall,omitempty"`
}

type NormalizedTool struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	HasID     bool           `json:"hasId"`
}

type NormalizedResult struct {
	Role         string `json:"role"`
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	Text         string `json:"text"`
	Content      []any  `json:"content"`
	Usage        *Usage `json:"usage,omitempty"`
}
