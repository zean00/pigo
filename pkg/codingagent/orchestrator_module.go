package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/badlogic/pigo/pkg/a2a"
	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
	"github.com/badlogic/pigo/pkg/orchestrator"
)

const orchestratorCustomType = "orchestrator.run"

func registerOrchestratorModule(registry *ModuleRegistry) error {
	registry.RegisterToolProvider(func(session *Session) ([]agentcore.Tool, []ai.Tool) {
		if !session.GetOrchestratorConfig().Enabled {
			return nil, nil
		}
		return orchestratorTools(session), orchestratorToolSpecs()
	})
	for _, option := range []ModuleConfigOption{
		{
			ID: "orchestrator_enabled", Name: "Orchestrator", Category: "orchestrator", Type: "select",
			Options: []string{"off", "on"},
			Get: func(session *Session) string {
				if session.GetOrchestratorConfig().Enabled {
					return "on"
				}
				return "off"
			},
			Set: func(session *Session, value string) error {
				config := session.GetOrchestratorConfig()
				switch strings.ToLower(strings.TrimSpace(value)) {
				case "on", "true", "1", "yes":
					config.Enabled = true
				case "", "off", "false", "0", "no":
					config.Enabled = false
				default:
					return fmt.Errorf("invalid orchestrator_enabled value: %s", value)
				}
				return session.SetOrchestratorConfig(config)
			},
		},
		{
			ID: "orchestrator_max_parallel", Name: "Orchestrator max parallel", Category: "orchestrator", Type: "number",
			Get: func(session *Session) string { return strconv.Itoa(session.GetOrchestratorConfig().MaxParallel) },
			Set: func(session *Session, value string) error {
				parsed, err := strconv.Atoi(strings.TrimSpace(value))
				if err != nil || parsed <= 0 {
					return fmt.Errorf("invalid orchestrator_max_parallel value: %s", value)
				}
				config := session.GetOrchestratorConfig()
				config.MaxParallel = parsed
				return session.SetOrchestratorConfig(config)
			},
		},
		{
			ID: "orchestrator_timeout_ms", Name: "Orchestrator timeout", Category: "orchestrator", Type: "number",
			Get: func(session *Session) string { return strconv.Itoa(session.GetOrchestratorConfig().TimeoutMillis) },
			Set: func(session *Session, value string) error {
				parsed, err := strconv.Atoi(strings.TrimSpace(value))
				if err != nil || parsed <= 0 {
					return fmt.Errorf("invalid orchestrator_timeout_ms value: %s", value)
				}
				config := session.GetOrchestratorConfig()
				config.TimeoutMillis = parsed
				return session.SetOrchestratorConfig(config)
			},
		},
		{
			ID: "orchestrator_agents", Name: "Orchestrator agents", Category: "orchestrator", Type: "text",
			Get: func(session *Session) string { return strings.Join(session.GetOrchestratorConfig().Agents, ",") },
			Set: func(session *Session, value string) error {
				config := session.GetOrchestratorConfig()
				config.Agents = commaSeparatedList(value)
				return session.SetOrchestratorConfig(config)
			},
		},
		{
			ID: "orchestrator_reducer", Name: "Orchestrator reducer", Category: "orchestrator", Type: "select",
			Options: []string{"markdown"},
			Get:     func(session *Session) string { return session.GetOrchestratorConfig().Reducer },
			Set: func(session *Session, value string) error {
				config := session.GetOrchestratorConfig()
				config.Reducer = value
				return session.SetOrchestratorConfig(config)
			},
		},
	} {
		if err := registry.RegisterConfigOption(option); err != nil {
			return err
		}
	}
	for command, handler := range map[string]ModuleRPCHandler{
		"start_orchestration":  startOrchestrationRPC,
		"get_orchestration":    getOrchestrationRPC,
		"list_orchestrations":  listOrchestrationsRPC,
		"cancel_orchestration": cancelOrchestrationRPC,
	} {
		if err := registry.RegisterRPCHandler(command, handler); err != nil {
			return err
		}
	}
	return nil
}

func orchestratorTools(session *Session) []agentcore.Tool {
	return []agentcore.Tool{
		{
			Name: "delegate_task",
			ExecuteWithUpdate: func(ctx context.Context, call ai.ContentBlock, _ func(agentcore.ToolResult)) agentcore.ToolResult {
				req := orchestratorRequestFromArgs(call.Arguments)
				if req.Goal == "" {
					req.Goal = strings.TrimSpace(fmt.Sprint(call.Arguments["message"]))
				}
				run, err := session.orchestrator().Delegate(ctx, req)
				return orchestratorToolResult(run, err)
			},
		},
		{
			Name: "orchestrate_task",
			ExecuteWithUpdate: func(ctx context.Context, call ai.ContentBlock, _ func(agentcore.ToolResult)) agentcore.ToolResult {
				run, err := session.orchestrator().Start(ctx, orchestratorRequestFromArgs(call.Arguments))
				return orchestratorToolResult(run, err)
			},
		},
		{
			Name: "orchestration_status",
			Execute: func(_ context.Context, call ai.ContentBlock) agentcore.ToolResult {
				runID := strings.TrimSpace(fmt.Sprint(call.Arguments["runId"]))
				run, ok := session.orchestrator().Get(runID)
				if !ok {
					return agentcore.ToolResult{Text: "orchestration run not found", IsError: true}
				}
				return agentcore.ToolResult{Text: run.Result, Details: map[string]any{"run": run}}
			},
		},
		{
			Name: "cancel_orchestration",
			Execute: func(_ context.Context, call ai.ContentBlock) agentcore.ToolResult {
				runID := strings.TrimSpace(fmt.Sprint(call.Arguments["runId"]))
				run, err := session.orchestrator().Cancel(runID)
				return orchestratorToolResult(run, err)
			},
		},
	}
}

func orchestratorToolSpecs() []ai.Tool {
	return []ai.Tool{
		{
			Name:        "delegate_task",
			Description: "Delegate one task to a configured remote A2A agent and return the result.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"message": map[string]any{"type": "string"},
				"agent":   map[string]any{"type": "string"},
				"skill":   map[string]any{"type": "string"},
			}, "required": []string{"message"}},
		},
		{
			Name:        "orchestrate_task",
			Description: "Run a bounded A2A task graph and return a synthesized result.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"goal":          map[string]any{"type": "string"},
				"steps":         map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
				"maxParallel":   map[string]any{"type": "number"},
				"timeoutMillis": map[string]any{"type": "number"},
			}, "required": []string{"goal"}},
		},
		{
			Name:        "orchestration_status",
			Description: "Return the current state of an orchestration run.",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"runId": map[string]any{"type": "string"}}, "required": []string{"runId"}},
		},
		{
			Name:        "cancel_orchestration",
			Description: "Cancel an active orchestration run.",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"runId": map[string]any{"type": "string"}}, "required": []string{"runId"}},
		},
	}
}

func (s *Session) orchestrator() *orchestrator.Manager {
	s.mu.Lock()
	manager := s.orchestratorManager
	s.mu.Unlock()
	if manager != nil {
		return manager
	}
	manager = orchestrator.NewManager(s.GetOrchestratorConfig(), s.GetA2AConfig(), s.orchestratorAgents(), func(run orchestrator.Run) error {
		_, err := s.AppendCustomEntry(orchestratorCustomType, run)
		return err
	})
	for _, run := range s.orchestratorRunsFromEntries() {
		manager.Restore(run)
	}
	s.mu.Lock()
	if s.orchestratorManager == nil {
		s.orchestratorManager = manager
	} else {
		manager = s.orchestratorManager
	}
	s.mu.Unlock()
	return manager
}

func (s *Session) orchestratorAgents() []orchestrator.Agent {
	agents := []orchestrator.Agent{}
	for _, remote := range s.GetA2AConfig().Agents {
		agents = append(agents, orchestrator.Agent{Name: remote.Name})
	}
	for _, profile := range s.AgentProfiles() {
		agents = append(agents, orchestrator.Agent{Name: profile.Name, Description: profile.Description, Skills: profile.Tools})
	}
	return agents
}

func (s *Session) orchestratorRunsFromEntries() []orchestrator.Run {
	out := []orchestrator.Run{}
	for _, entry := range s.CustomEntries(orchestratorCustomType) {
		data, err := json.Marshal(entry.Data)
		if err != nil {
			continue
		}
		var run orchestrator.Run
		if err := json.Unmarshal(data, &run); err == nil && run.RunID != "" {
			out = append(out, run)
		}
	}
	return out
}

func (s *Session) Orchestrations() []orchestrator.Run {
	return s.orchestrator().List()
}

func orchestratorRequestFromArgs(args map[string]any) orchestrator.RunRequest {
	var req orchestrator.RunRequest
	data, _ := json.Marshal(args)
	_ = json.Unmarshal(data, &req)
	if req.Goal == "" {
		req.Goal = optionalStringArg(args, "goal")
	}
	if req.Agent == "" {
		req.Agent = a2a.ToolSafeName(optionalStringArg(args, "agent"))
	}
	if req.Skill == "" {
		req.Skill = optionalStringArg(args, "skill")
	}
	return req
}

func optionalStringArg(args map[string]any, key string) string {
	value, ok := args[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func orchestratorToolResult(run orchestrator.Run, err error) agentcore.ToolResult {
	if err != nil {
		return agentcore.ToolResult{Text: err.Error(), IsError: true}
	}
	text := run.Result
	if text == "" {
		text = fmt.Sprintf("orchestration %s finished with state %s", run.RunID, run.State)
	}
	return agentcore.ToolResult{
		Text:    text,
		Details: map[string]any{"run": run, "runId": run.RunID, "state": run.State},
		IsError: run.State == orchestrator.StateFailed || run.State == orchestrator.StateCanceled,
	}
}

func startOrchestrationRPC(ctx context.Context, session *Session, command rpcCommand) rpcResponse {
	req := orchestrationRequestFromCommand(command)
	run, err := session.orchestrator().Start(ctx, req)
	return orchestrationRPCResponse(command, run, err)
}

func getOrchestrationRPC(_ context.Context, session *Session, command rpcCommand) rpcResponse {
	run, ok := session.orchestrator().Get(firstNonEmpty(command.RunID, command.Name))
	if !ok {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: "orchestration run not found"}
	}
	return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: run}
}

func listOrchestrationsRPC(_ context.Context, session *Session, command rpcCommand) rpcResponse {
	return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: map[string]any{"runs": session.orchestrator().List()}}
}

func cancelOrchestrationRPC(_ context.Context, session *Session, command rpcCommand) rpcResponse {
	run, err := session.orchestrator().Cancel(firstNonEmpty(command.RunID, command.Name))
	return orchestrationRPCResponse(command, run, err)
}

func orchestrationRequestFromCommand(command rpcCommand) orchestrator.RunRequest {
	req := orchestrator.RunRequest{
		Goal:          firstNonEmpty(command.Goal, command.Message),
		Agent:         a2a.ToolSafeName(command.Agent),
		Skill:         command.Skill,
		Agents:        append([]string(nil), command.Agents...),
		Steps:         append([]orchestrator.TaskSpec(nil), command.Steps...),
		MaxParallel:   command.MaxParallel,
		TimeoutMillis: command.TimeoutMillis,
	}
	if command.Data != nil {
		data, _ := json.Marshal(command.Data)
		_ = json.Unmarshal(data, &req)
	}
	return req
}

func orchestrationRPCResponse(command rpcCommand, run orchestrator.Run, err error) rpcResponse {
	if err != nil {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
	}
	return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: run}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
