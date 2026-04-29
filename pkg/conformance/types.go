package conformance

import (
	"encoding/json"

	"github.com/badlogic/pigo/pkg/ai"
)

type ModelRef struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type AICase struct {
	Name    string   `json:"name"`
	Model   ModelRef `json:"model"`
	Context struct {
		SystemPrompt string       `json:"systemPrompt,omitempty"`
		Messages     []ai.Message `json:"messages"`
		Tools        []ai.Tool    `json:"tools,omitempty"`
	} `json:"context"`
}

type AIOutput struct {
	Case           string               `json:"case"`
	Implementation Implementation       `json:"implementation"`
	Model          ModelRef             `json:"model"`
	Events         []ai.NormalizedEvent `json:"events"`
	Result         ai.NormalizedResult  `json:"result"`
}

type AgentCase struct {
	Name    string   `json:"name"`
	Model   ModelRef `json:"model"`
	Context struct {
		SystemPrompt string             `json:"systemPrompt,omitempty"`
		Messages     []ai.Message       `json:"messages"`
		Tools        []AgentFixtureTool `json:"tools,omitempty"`
	} `json:"context"`
	Prompts        []ai.Message           `json:"prompts"`
	AssistantTurns []AssistantFixtureTurn `json:"assistantTurns"`
	Options        struct {
		ToolExecution string `json:"toolExecution,omitempty"`
	} `json:"options,omitempty"`
	Expect AgentExpectations `json:"expect,omitempty"`
}

type AgentExpectations struct {
	EventTypesInOrder []string `json:"eventTypesInOrder,omitempty"`
	FinalMessageRoles []string `json:"finalMessageRoles,omitempty"`
	FinalTextContains []string `json:"finalTextContains,omitempty"`
	ToolResults       []struct {
		ToolCallID   string `json:"toolCallId,omitempty"`
		ToolName     string `json:"toolName"`
		TextContains string `json:"textContains,omitempty"`
		IsError      *bool  `json:"isError,omitempty"`
	} `json:"toolResults,omitempty"`
	ToolExecutions []struct {
		ToolCallID string `json:"toolCallId,omitempty"`
		ToolName   string `json:"toolName"`
		IsError    *bool  `json:"isError,omitempty"`
	} `json:"toolExecutions,omitempty"`
}

type VerificationResult struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors"`
}

type AgentFixtureTool struct {
	ai.Tool
	Result struct {
		Text      string         `json:"text"`
		Details   map[string]any `json:"details,omitempty"`
		Terminate bool           `json:"terminate,omitempty"`
	} `json:"result"`
}

type AssistantFixtureTurn struct {
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage,omitempty"`
}

type CodingAgentCase struct {
	Name      string   `json:"name"`
	Model     ModelRef `json:"model"`
	Workspace struct {
		Files map[string]string `json:"files"`
	} `json:"workspace"`
	Prompts        []string               `json:"prompts"`
	AssistantTurns []AssistantFixtureTurn `json:"assistantTurns"`
	Expect         struct {
		Files map[string]string `json:"files"`
	} `json:"expect"`
}

type AgentOutput struct {
	Case           string         `json:"case"`
	Implementation Implementation `json:"implementation"`
	Model          ModelRef       `json:"model"`
	Events         []any          `json:"events"`
	Messages       []any          `json:"messages"`
}

type CodingAgentOutput struct {
	Case              string            `json:"case"`
	Implementation    Implementation    `json:"implementation"`
	Model             ModelRef          `json:"model"`
	Events            []any             `json:"events"`
	Messages          []any             `json:"messages"`
	SessionEntryTypes []string          `json:"sessionEntryTypes"`
	Files             map[string]string `json:"files"`
}
