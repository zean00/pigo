package ai

import (
	"context"
	"net/http"
	"time"
)

type ProviderResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

type CacheRetention string

const (
	CacheRetentionNone  CacheRetention = "none"
	CacheRetentionShort CacheRetention = "short"
	CacheRetentionLong  CacheRetention = "long"
)

type ThinkingBudgets struct {
	Minimal int `json:"minimal,omitempty"`
	Low     int `json:"low,omitempty"`
	Medium  int `json:"medium,omitempty"`
	High    int `json:"high,omitempty"`
}

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
	// Transport selects a provider-specific streaming transport where supported.
	Transport string
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
	// SessionID enables provider session affinity and prompt-cache grouping where supported.
	SessionID string
	// CacheRetention controls provider prompt-cache behavior where supported.
	CacheRetention CacheRetention
	// ReasoningEffort selects provider-specific reasoning depth where supported.
	ReasoningEffort string
	// ThinkingBudgets overrides per-level token budgets where supported.
	ThinkingBudgets ThinkingBudgets
	// ReasoningSummary controls provider-specific reasoning summary behavior where supported.
	ReasoningSummary string
	// ServiceTier selects provider-specific service tiering where supported.
	ServiceTier string
	// TextVerbosity controls provider-specific text verbosity where supported.
	TextVerbosity string
	// Metadata carries provider-specific request metadata.
	Metadata map[string]any
	// OnPayload can inspect or replace the provider request payload before it is marshaled.
	OnPayload func(payload any, req CompletionRequest) (any, error)
	// OnResponse observes provider HTTP responses before the body is consumed.
	OnResponse func(response ProviderResponse, req CompletionRequest) error
	// MaxRetries overrides provider retry attempts for transient failures.
	// Zero uses the provider default. Negative disables retries.
	MaxRetries int
	// MaxRetryDelay caps server-requested retry delays. Zero uses the provider default.
	// Negative disables the cap.
	MaxRetryDelay time.Duration
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

type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

type Usage struct {
	Input       int  `json:"input"`
	Output      int  `json:"output"`
	CacheRead   int  `json:"cacheRead"`
	CacheWrite  int  `json:"cacheWrite"`
	TotalTokens int  `json:"totalTokens"`
	Cost        Cost `json:"cost"`
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
