package researchadapter

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
)

func researchTool(config Config) agentcore.Tool {
	return agentcore.Tool{
		Name:          "research",
		ExecutionMode: agentcore.ToolExecutionSequential,
		Execute: func(ctx context.Context, call ai.ContentBlock) agentcore.ToolResult {
			query, _ := call.Arguments["query"].(string)
			query = strings.TrimSpace(query)
			if query == "" {
				return agentcore.ToolResult{Text: "research requires query", IsError: true}
			}
			depth := intArg(call.Arguments["depth"], 0)
			if depth != 0 {
				return agentcore.ToolResult{Text: "research currently supports depth 0 quick mode only", IsError: true}
			}
			if config.Host == nil {
				return agentcore.ToolResult{Text: "research requires a session host", IsError: true}
			}
			toolCallID := strings.TrimSpace(call.ID)
			provider, model := config.Host.Model()
			if override, _ := call.Arguments["model"].(string); strings.TrimSpace(override) != "" {
				model = strings.TrimSpace(override)
			}
			if provider == "" || model == "" {
				return agentcore.ToolResult{Text: "research requires the parent session to have a configured model", IsError: true}
			}
			start := now(config)
			emitResearchProgress(config, "started", map[string]any{"toolCallId": toolCallID, "query": query, "depth": depth, "mode": "quick"})
			result, details, err := runQuickResearch(ctx, config, provider, model, query, toolCallID)
			if err != nil {
				emitResearchProgress(config, "failed", map[string]any{"toolCallId": toolCallID, "query": query, "error": err.Error()})
				return agentcore.ToolResult{Text: err.Error(), Details: details, IsError: true}
			}
			details["durationMs"] = now(config).Sub(start).Milliseconds()
			emitResearchProgress(config, "completed", details)
			return agentcore.ToolResult{Text: result, Details: details}
		},
	}
}

func runQuickResearch(ctx context.Context, config Config, provider, model, query, toolCallID string) (string, map[string]any, error) {
	workspaceTools, workspaceSpecs := config.Host.WorkspaceTools()
	researchConfig := config
	researchConfig.Tools = []string{"search", "scrape", "security_search"}
	researchConfig.Host = nil
	researchConfig.EventSink = nil
	researchTools, researchSpecs := Tools(researchConfig)
	tools := append(workspaceTools, researchTools...)
	specs := append(workspaceSpecs, researchSpecs...)
	budget := DefaultToolBudget()
	details := map[string]any{
		"mode":     "quick",
		"query":    query,
		"provider": provider,
		"model":    model,
	}
	if toolCallID != "" {
		details["toolCallId"] = toolCallID
	}
	loop, err := agentcore.RunProviderLoop(ctx, agentcore.ProviderLoopInput{
		PromptMessages: []ai.Message{{Role: "user", Content: quickResearchPrompt(query)}},
		Tools:          tools,
		ToolSpecs:      specs,
		Provider:       provider,
		Model:          model,
		MaxRounds:      10,
		ToolExecution:  agentcore.ToolExecutionSequential,
		GetAPIKey: func(provider string) string {
			return config.Host.APIKey(ctx, provider)
		},
		BeforeToolCall: budget.BeforeToolCall,
		EventSink: func(event agentcore.Event) {
			emitResearchProgress(config, "event", map[string]any{"toolCallId": toolCallID, "query": query, "event": event})
		},
	})
	details["budgetUsage"] = budget.Usage()
	if err != nil {
		return "", details, err
	}
	text := strings.TrimSpace(lastAssistantText(loop.Messages))
	if text == "" {
		return "", details, fmt.Errorf("research completed without an assistant report")
	}
	return text, details, nil
}

func quickResearchPrompt(query string) string {
	return strings.Join([]string{
		"You are an isolated quick research agent.",
		"Use only the available read, grep, search, scrape, and security_search tools.",
		"Do not modify files or run shell commands.",
		"Gather enough evidence to answer the query, scrape selectively, then produce a concise Markdown report.",
		"Include links or source identifiers for important claims.",
		"",
		"Research query:",
		query,
	}, "\n")
}

func lastAssistantText(messages []agentcore.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i]["role"] != "assistant" {
			continue
		}
		if text, ok := messages[i]["text"].(string); ok {
			return text
		}
	}
	return ""
}

func emitResearchProgress(config Config, phase string, data map[string]any) {
	if config.EventSink == nil {
		return
	}
	event := agentcore.Event{"type": "research_progress", "phase": phase}
	for key, value := range data {
		event[key] = value
	}
	config.EventSink(event)
}

func now(config Config) time.Time {
	if config.Now != nil {
		return config.Now()
	}
	return time.Now()
}
