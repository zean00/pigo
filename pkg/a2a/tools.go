package a2a

import (
	"context"
	"fmt"
	"strings"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
)

func Tools(config Config) ([]agentcore.Tool, []ai.Tool) {
	config = config.Normalized()
	if !config.Enabled {
		return nil, nil
	}
	tools := []agentcore.Tool{}
	specs := []ai.Tool{}
	for _, agent := range config.Agents {
		agent := agent
		if agent.Name == "" {
			continue
		}
		toolName := "a2a__" + agent.Name + "__send_message"
		tool := agentcore.Tool{
			Name: toolName,
			ExecuteWithUpdate: func(ctx context.Context, call ai.ContentBlock, onUpdate func(agentcore.ToolResult)) agentcore.ToolResult {
				return executeAgentTool(ctx, agent, config, call, onUpdate)
			},
		}
		spec := ai.Tool{
			Name:        toolName,
			Description: fmt.Sprintf("Send a task message to the remote A2A agent %q and return its final task result.", agent.Name),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message":   map[string]any{"type": "string", "description": "Message or task request to send to the remote agent."},
					"taskId":    map[string]any{"type": "string", "description": "Optional existing A2A task id to continue."},
					"contextId": map[string]any{"type": "string", "description": "Optional A2A context id."},
					"skill":     map[string]any{"type": "string", "description": "Optional remote skill id/name hint."},
					"blocking":  map[string]any{"type": "boolean", "description": "Whether to wait for the remote task to complete. Defaults to true."},
				},
				"required": []string{"message"},
			},
		}
		tools = append(tools, tool)
		specs = append(specs, spec)
	}
	return tools, specs
}

func executeAgentTool(ctx context.Context, agent RemoteAgent, config Config, call ai.ContentBlock, onUpdate func(agentcore.ToolResult)) agentcore.ToolResult {
	message := strings.TrimSpace(fmt.Sprint(call.Arguments["message"]))
	if message == "" {
		return agentcore.ToolResult{Text: "missing message", IsError: true}
	}
	blocking := true
	if value, ok := call.Arguments["blocking"].(bool); ok {
		blocking = value
	}
	msg := NewTextMessage(RoleUser, message)
	msg.TaskID = strings.TrimSpace(fmt.Sprint(call.Arguments["taskId"]))
	msg.ContextID = strings.TrimSpace(fmt.Sprint(call.Arguments["contextId"]))
	if skill := strings.TrimSpace(fmt.Sprint(call.Arguments["skill"])); skill != "" {
		msg.Metadata = map[string]any{"skill": skill}
	}
	params := MessageSendParams{
		Message:       msg,
		Configuration: &MessageSendConfiguration{Blocking: &blocking},
	}
	client := NewClient(agent, config.Timeout, config.MaxResponseBytes)
	card, err := client.FetchAgentCard(ctx)
	if err != nil {
		return agentcore.ToolResult{Text: err.Error(), IsError: true}
	}
	var task Task
	if card.Capabilities.Streaming && blocking {
		task, err = client.StreamMessage(ctx, params, func(event any) {
			text := eventSummary(event)
			if strings.TrimSpace(text) != "" && onUpdate != nil {
				onUpdate(agentcore.ToolResult{Text: text, Details: map[string]any{"agent": agent.Name, "event": event}})
			}
		})
	} else {
		task, err = client.SendMessage(ctx, params)
	}
	if err != nil {
		return agentcore.ToolResult{Text: err.Error(), IsError: true}
	}
	text := TaskText(task)
	if text == "" {
		text = fmt.Sprintf("A2A task %s finished with state %s", task.ID, task.Status.State)
	}
	return agentcore.ToolResult{
		Text: text,
		Details: map[string]any{
			"agent":     agent.Name,
			"taskId":    task.ID,
			"contextId": task.ContextID,
			"state":     task.Status.State,
			"task":      task,
		},
		IsError: task.Status.State == TaskStateFailed || task.Status.State == TaskStateRejected || task.Status.State == TaskStateCanceled,
	}
}

func TaskText(task Task) string {
	for i := len(task.Artifacts) - 1; i >= 0; i-- {
		text := TextFromParts(task.Artifacts[i].Parts)
		if strings.TrimSpace(text) != "" {
			return text
		}
	}
	if task.Status.Message != nil {
		return TextFromParts(task.Status.Message.Parts)
	}
	return ""
}

func eventSummary(event any) string {
	switch value := event.(type) {
	case map[string]any:
		if status, ok := value["status"].(map[string]any); ok {
			return fmt.Sprintf("Remote task status: %v", status["state"])
		}
		if artifact, ok := value["artifact"].(map[string]any); ok {
			if parts, ok := artifact["parts"].([]any); ok {
				texts := []string{}
				for _, part := range parts {
					partMap, _ := part.(map[string]any)
					if partMap["kind"] == "text" {
						texts = append(texts, fmt.Sprint(partMap["text"]))
					}
				}
				return strings.Join(texts, "\n")
			}
		}
	}
	return ""
}
