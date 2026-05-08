package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/badlogic/pigo/pkg/a2a"
	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
	"github.com/badlogic/pigo/pkg/researchadapter"
)

type SessionModule interface {
	ID() string
	Register(*ModuleRegistry) error
}

type ModuleRegistry struct {
	session *Session

	moduleIDs     map[string]struct{}
	toolProviders []ModuleToolProvider
	configOptions []ModuleConfigOption
	configByID    map[string]ModuleConfigOption
	rpcHandlers   map[string]ModuleRPCHandler
	entryHandlers map[string]ModuleSessionEntryHandler
	entryOrder    []string
}

type ModuleToolProvider func(*Session) ([]agentcore.Tool, []ai.Tool)

type ModuleRPCHandler func(context.Context, *Session, rpcCommand) rpcResponse

type ModuleConfigOption struct {
	ID          string
	Name        string
	Category    string
	Type        string
	Options     []string
	OptionsFunc func(*Session) []string
	Get         func(*Session) string
	Set         func(*Session, string) error
}

type ModuleSessionEntryHandler struct {
	VisibleInTree    bool
	AffectsLeaf      bool
	Apply            func(*Session, SessionEntry)
	ApplyAfterBranch func(*Session, SessionEntry, map[string]struct{})
	ExportMetadata   func(*Session, []SessionEntry) []SessionEntry
}

func newModuleRegistry(session *Session) *ModuleRegistry {
	return &ModuleRegistry{
		session:       session,
		moduleIDs:     map[string]struct{}{},
		configByID:    map[string]ModuleConfigOption{},
		rpcHandlers:   map[string]ModuleRPCHandler{},
		entryHandlers: map[string]ModuleSessionEntryHandler{},
	}
}

func (s *Session) ensureModuleRegistry() *ModuleRegistry {
	if s.modules != nil {
		return s.modules
	}
	registry := newModuleRegistry(s)
	s.modules = registry
	for _, module := range defaultSessionModules() {
		_ = registry.RegisterModule(module)
	}
	return registry
}

func (r *ModuleRegistry) RegisterModule(module SessionModule) error {
	if module == nil {
		return nil
	}
	id := strings.TrimSpace(module.ID())
	if id == "" {
		return fmt.Errorf("module id cannot be empty")
	}
	if _, exists := r.moduleIDs[id]; exists {
		return fmt.Errorf("module %q already registered", id)
	}
	snapshot := r.snapshot()
	r.moduleIDs[id] = struct{}{}
	if err := module.Register(r); err != nil {
		r.restore(snapshot)
		return fmt.Errorf("register module %s: %w", id, err)
	}
	return nil
}

type moduleRegistrySnapshot struct {
	moduleIDs     map[string]struct{}
	toolProviders []ModuleToolProvider
	configOptions []ModuleConfigOption
	configByID    map[string]ModuleConfigOption
	rpcHandlers   map[string]ModuleRPCHandler
	entryHandlers map[string]ModuleSessionEntryHandler
	entryOrder    []string
}

func (r *ModuleRegistry) snapshot() moduleRegistrySnapshot {
	return moduleRegistrySnapshot{
		moduleIDs:     cloneStringSet(r.moduleIDs),
		toolProviders: append([]ModuleToolProvider(nil), r.toolProviders...),
		configOptions: append([]ModuleConfigOption(nil), r.configOptions...),
		configByID:    cloneConfigOptions(r.configByID),
		rpcHandlers:   cloneRPCHandlers(r.rpcHandlers),
		entryHandlers: cloneSessionEntryHandlers(r.entryHandlers),
		entryOrder:    append([]string(nil), r.entryOrder...),
	}
}

func (r *ModuleRegistry) restore(snapshot moduleRegistrySnapshot) {
	r.moduleIDs = snapshot.moduleIDs
	r.toolProviders = snapshot.toolProviders
	r.configOptions = snapshot.configOptions
	r.configByID = snapshot.configByID
	r.rpcHandlers = snapshot.rpcHandlers
	r.entryHandlers = snapshot.entryHandlers
	r.entryOrder = snapshot.entryOrder
}

func cloneStringSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneConfigOptions(in map[string]ModuleConfigOption) map[string]ModuleConfigOption {
	out := make(map[string]ModuleConfigOption, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneRPCHandlers(in map[string]ModuleRPCHandler) map[string]ModuleRPCHandler {
	out := make(map[string]ModuleRPCHandler, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneSessionEntryHandlers(in map[string]ModuleSessionEntryHandler) map[string]ModuleSessionEntryHandler {
	out := make(map[string]ModuleSessionEntryHandler, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (r *ModuleRegistry) RegisterToolProvider(provider ModuleToolProvider) {
	if provider == nil {
		return
	}
	r.toolProviders = append(r.toolProviders, provider)
}

func (r *ModuleRegistry) Tools() ([]agentcore.Tool, []ai.Tool) {
	tools := make([]agentcore.Tool, 0)
	specs := make([]ai.Tool, 0)
	for _, provider := range r.toolProviders {
		providerTools, providerSpecs := provider(r.session)
		tools = append(tools, providerTools...)
		specs = append(specs, providerSpecs...)
	}
	return tools, specs
}

func (r *ModuleRegistry) RegisterConfigOption(option ModuleConfigOption) error {
	option.ID = strings.TrimSpace(option.ID)
	if option.ID == "" {
		return fmt.Errorf("config option id cannot be empty")
	}
	if _, exists := r.configByID[option.ID]; exists {
		return fmt.Errorf("config option %q already registered", option.ID)
	}
	if option.Type == "" {
		option.Type = "text"
	}
	r.configByID[option.ID] = option
	r.configOptions = append(r.configOptions, option)
	return nil
}

func (r *ModuleRegistry) ConfigOptions() []ModuleConfigOption {
	options := make([]ModuleConfigOption, len(r.configOptions))
	copy(options, r.configOptions)
	return options
}

func (r *ModuleRegistry) ConfigOption(id string) (ModuleConfigOption, bool) {
	option, ok := r.configByID[strings.TrimSpace(id)]
	return option, ok
}

func (o ModuleConfigOption) CurrentValue(session *Session) string {
	if o.Get == nil {
		return ""
	}
	return o.Get(session)
}

func (o ModuleConfigOption) SelectOptions(session *Session) []string {
	if o.OptionsFunc != nil {
		return o.OptionsFunc(session)
	}
	return append([]string(nil), o.Options...)
}

func (o ModuleConfigOption) SetValue(session *Session, value string) error {
	if o.Set == nil {
		return fmt.Errorf("config option %s is read-only", o.ID)
	}
	return o.Set(session, value)
}

func (r *ModuleRegistry) RegisterRPCHandler(commandType string, handler ModuleRPCHandler) error {
	commandType = strings.TrimSpace(commandType)
	if commandType == "" {
		return fmt.Errorf("rpc command type cannot be empty")
	}
	if handler == nil {
		return fmt.Errorf("rpc handler %q cannot be nil", commandType)
	}
	if _, exists := r.rpcHandlers[commandType]; exists {
		return fmt.Errorf("rpc command %q already registered", commandType)
	}
	r.rpcHandlers[commandType] = handler
	return nil
}

func (r *ModuleRegistry) RPCHandler(commandType string) (ModuleRPCHandler, bool) {
	handler, ok := r.rpcHandlers[strings.TrimSpace(commandType)]
	return handler, ok
}

func (r *ModuleRegistry) RegisterSessionEntryHandler(entryType string, handler ModuleSessionEntryHandler) error {
	entryType = strings.TrimSpace(entryType)
	if entryType == "" {
		return fmt.Errorf("session entry type cannot be empty")
	}
	if _, exists := r.entryHandlers[entryType]; exists {
		return fmt.Errorf("session entry type %q already registered", entryType)
	}
	r.entryHandlers[entryType] = handler
	r.entryOrder = append(r.entryOrder, entryType)
	return nil
}

func (r *ModuleRegistry) SessionEntryHandler(entryType string) (ModuleSessionEntryHandler, bool) {
	handler, ok := r.entryHandlers[strings.TrimSpace(entryType)]
	return handler, ok
}

func (s *Session) ConfigOptions() []ModuleConfigOption {
	return s.ensureModuleRegistry().ConfigOptions()
}

func (s *Session) ConfigOption(id string) (ModuleConfigOption, bool) {
	return s.ensureModuleRegistry().ConfigOption(id)
}

func (s *Session) RegisterModule(module SessionModule) error {
	return s.ensureModuleRegistry().RegisterModule(module)
}

type sessionModule struct {
	id       string
	register func(*ModuleRegistry) error
}

func (m sessionModule) ID() string { return m.id }

func (m sessionModule) Register(registry *ModuleRegistry) error {
	return m.register(registry)
}

func defaultSessionModules() []SessionModule {
	return []SessionModule{
		sessionModule{id: "core", register: registerCoreModule},
		sessionModule{id: "prompt_injection_guard", register: registerPromptInjectionGuardModule},
		sessionModule{id: "command_compression", register: registerCommandCompressionModule},
		sessionModule{id: "bash_permission", register: registerBashPermissionModule},
		sessionModule{id: "builtin_tools", register: registerBuiltinToolsModule},
		sessionModule{id: "a2a", register: registerA2AModule},
		sessionModule{id: "orchestrator", register: registerOrchestratorModule},
		sessionModule{id: "research", register: registerResearchModule},
		sessionModule{id: "extension_tools", register: registerExtensionToolsModule},
		sessionModule{id: "tool_search", register: registerToolSearchModule},
		sessionModule{id: "usage", register: registerUsageModule},
	}
}

func registerPromptInjectionGuardModule(registry *ModuleRegistry) error {
	for _, option := range []ModuleConfigOption{
		{
			ID: "prompt_injection_guard", Name: "Prompt injection guard", Category: "security", Type: "select",
			Options: PromptInjectionGuardModes(),
			Get:     func(session *Session) string { return session.GetPromptInjectionConfig().Mode },
			Set: func(session *Session, value string) error {
				config := session.GetPromptInjectionConfig()
				config.Mode = value
				return session.SetPromptInjectionConfig(config)
			},
		},
		{
			ID: "prompt_injection_sources", Name: "Prompt injection sources", Category: "security", Type: "text",
			Get: func(session *Session) string {
				return strings.Join(session.GetPromptInjectionConfig().Sources, ",")
			},
			Set: func(session *Session, value string) error {
				config := session.GetPromptInjectionConfig()
				config.Sources = commaSeparatedList(value)
				return session.SetPromptInjectionConfig(config)
			},
		},
		{
			ID: "prompt_injection_sensitive_tools", Name: "Prompt injection sensitive tools", Category: "security", Type: "text",
			Get: func(session *Session) string {
				return strings.Join(session.GetPromptInjectionConfig().SensitiveTools, ",")
			},
			Set: func(session *Session, value string) error {
				config := session.GetPromptInjectionConfig()
				config.SensitiveTools = commaSeparatedList(value)
				return session.SetPromptInjectionConfig(config)
			},
		},
	} {
		if err := registry.RegisterConfigOption(option); err != nil {
			return err
		}
	}
	if err := registry.RegisterRPCHandler("set_prompt_injection_guard", setPromptInjectionGuardRPC); err != nil {
		return err
	}
	return registry.RegisterRPCHandler("get_prompt_injection_guard", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetPromptInjectionConfig().Metadata()}
	})
}

func registerCoreModule(registry *ModuleRegistry) error {
	for _, entryType := range []string{"session", "label"} {
		handler := ModuleSessionEntryHandler{VisibleInTree: false, AffectsLeaf: false}
		if entryType == "label" {
			handler.Apply = applyLabelEntry
			handler.ApplyAfterBranch = func(session *Session, entry SessionEntry, branchIDs map[string]struct{}) {
				if _, ok := branchIDs[entry.TargetID]; ok {
					applyLabelEntry(session, entry)
				}
			}
			handler.ExportMetadata = func(session *Session, branch []SessionEntry) []SessionEntry {
				return session.labelEntriesForBranch(branch)
			}
		}
		if err := registry.RegisterSessionEntryHandler(entryType, handler); err != nil {
			return err
		}
	}
	options := []ModuleConfigOption{
		{
			ID: "thinking_level", Name: "Thinking level", Category: "thought_level", Type: "select",
			Options: []string{"off", "low", "medium", "high", "xhigh"},
			Get:     func(session *Session) string { return session.ThinkingLevel },
			Set:     func(session *Session, value string) error { return session.SetThinkingLevel(value) },
		},
		{
			ID: "steering_mode", Name: "Steering mode", Category: "mode", Type: "select",
			Options: []string{"one-at-a-time", "all"},
			Get:     func(session *Session) string { return session.SteeringMode },
			Set:     func(session *Session, value string) error { return session.SetSteeringMode(value) },
		},
		{
			ID: "follow_up_mode", Name: "Follow-up mode", Category: "mode", Type: "select",
			Options: []string{"one-at-a-time", "all"},
			Get:     func(session *Session) string { return session.FollowUpMode },
			Set:     func(session *Session, value string) error { return session.SetFollowUpMode(value) },
		},
		{
			ID: "tool_execution", Name: "Tool execution", Category: "mode", Type: "select",
			Options: []string{"parallel", "sequential", "interleaved"},
			Get:     func(session *Session) string { return string(session.ToolExecution) },
			Set:     func(session *Session, value string) error { return session.SetToolExecutionMode(value) },
		},
		{
			ID: "agent_profile", Name: "Agent profile", Category: "agent", Type: "select",
			OptionsFunc: func(session *Session) []string {
				values := []string{"default"}
				for _, profile := range session.AgentProfiles() {
					values = append(values, profile.Name)
				}
				return values
			},
			Get: func(session *Session) string {
				active := session.ActiveAgentProfile()
				if active == "" {
					return "default"
				}
				return active
			},
			Set: func(session *Session, value string) error { return session.SetActiveAgentProfile(value) },
		},
		{
			ID: "session_purpose", Name: "Session purpose", Category: "domain", Type: "select",
			Options: SessionPurposeValues(),
			Get:     func(session *Session) string { return session.GetDomainConfig().Purpose },
			Set: func(session *Session, value string) error {
				config := session.GetDomainConfig()
				config.Purpose = value
				return session.SetDomainConfig(config)
			},
		},
		{
			ID: "context_files", Name: "Context files", Category: "domain", Type: "text",
			Get: func(session *Session) string { return strings.Join(session.GetDomainConfig().ContextFiles, ",") },
			Set: func(session *Session, value string) error {
				config := session.GetDomainConfig()
				config.ContextFiles = commaSeparatedList(value)
				return session.SetDomainConfig(config)
			},
		},
		{
			ID: "include_git_context", Name: "Include git context", Category: "domain", Type: "select",
			Options: []string{"true", "false"},
			Get: func(session *Session) string {
				return fmt.Sprint(boolValue(session.GetDomainConfig().IncludeGitContext))
			},
			Set: func(session *Session, value string) error {
				parsed, err := parseConfigBool(value)
				if err != nil {
					return err
				}
				config := session.GetDomainConfig()
				config.IncludeGitContext = &parsed
				return session.SetDomainConfig(config)
			},
		},
		{
			ID: "include_package_context", Name: "Include package context", Category: "domain", Type: "select",
			Options: []string{"true", "false"},
			Get: func(session *Session) string {
				return fmt.Sprint(boolValue(session.GetDomainConfig().IncludePackageContext))
			},
			Set: func(session *Session, value string) error {
				parsed, err := parseConfigBool(value)
				if err != nil {
					return err
				}
				config := session.GetDomainConfig()
				config.IncludePackageContext = &parsed
				return session.SetDomainConfig(config)
			},
		},
		{
			ID: "extra_instructions", Name: "Extra instructions", Category: "domain", Type: "text",
			Get: func(session *Session) string { return session.GetDomainConfig().ExtraInstructions },
			Set: func(session *Session, value string) error {
				config := session.GetDomainConfig()
				config.ExtraInstructions = value
				return session.SetDomainConfig(config)
			},
		},
	}
	for _, option := range options {
		if err := registry.RegisterConfigOption(option); err != nil {
			return err
		}
	}
	if err := registry.RegisterRPCHandler("set_agent_profile", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		if err := session.SetActiveAgentProfile(command.Name); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: agentResourcesMetadata(session)}
	}); err != nil {
		return err
	}
	if err := registry.RegisterRPCHandler("get_agent_profiles", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: agentResourcesMetadata(session)}
	}); err != nil {
		return err
	}
	if err := registry.RegisterRPCHandler("get_agent_teams", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: map[string]any{"teams": session.AgentTeams()}}
	}); err != nil {
		return err
	}
	if err := registry.RegisterRPCHandler("get_agent_chains", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: map[string]any{"chains": session.AgentChains()}}
	}); err != nil {
		return err
	}
	if err := registry.RegisterRPCHandler("set_domain_config", setDomainConfigRPC); err != nil {
		return err
	}
	if err := registry.RegisterRPCHandler("get_domain_config", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetDomainConfig().Metadata()}
	}); err != nil {
		return err
	}
	return nil
}

func agentResourcesMetadata(session *Session) map[string]any {
	return map[string]any{
		"active":   session.ActiveAgentProfile(),
		"profiles": session.AgentProfiles(),
		"teams":    session.AgentTeams(),
		"chains":   session.AgentChains(),
	}
}

func registerBuiltinToolsModule(registry *ModuleRegistry) error {
	registry.RegisterToolProvider(func(session *Session) ([]agentcore.Tool, []ai.Tool) {
		tools := BuiltinToolsWithOptions(session.Root, BuiltinToolOptions{
			OutputLimit:        session.ToolOutputLimit,
			ShellCommandPrefix: session.ShellCommandPrefix,
			CommandCompression: session.CommandCompression,
			BashPermission:     session.BashPermission,
			BuiltinToolPolicy:  session.BuiltinToolPolicy,
		})
		specs := BuiltinToolSpecsWithPolicy(session.BuiltinToolPolicy)
		return tools, specs
	})
	for _, option := range []ModuleConfigOption{
		{
			ID: "builtin_tools_enabled", Name: "Built-in tools enabled", Category: "tools", Type: "text",
			Get: func(session *Session) string { return strings.Join(session.GetBuiltinToolPolicy().Enabled, ",") },
			Set: func(session *Session, value string) error {
				policy := session.GetBuiltinToolPolicy()
				policy.Enabled = commaSeparatedList(value)
				return session.SetBuiltinToolPolicy(policy)
			},
		},
		{
			ID: "builtin_tools_disabled", Name: "Built-in tools disabled", Category: "tools", Type: "text",
			Get: func(session *Session) string { return strings.Join(session.GetBuiltinToolPolicy().Disabled, ",") },
			Set: func(session *Session, value string) error {
				policy := session.GetBuiltinToolPolicy()
				policy.Disabled = commaSeparatedList(value)
				return session.SetBuiltinToolPolicy(policy)
			},
		},
	} {
		if err := registry.RegisterConfigOption(option); err != nil {
			return err
		}
	}
	if err := registry.RegisterRPCHandler("set_builtin_tool_policy", setBuiltinToolPolicyRPC); err != nil {
		return err
	}
	return registry.RegisterRPCHandler("get_builtin_tool_policy", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetBuiltinToolPolicy().Metadata()}
	})
}

func registerExtensionToolsModule(registry *ModuleRegistry) error {
	registry.RegisterToolProvider(func(session *Session) ([]agentcore.Tool, []ai.Tool) {
		return session.ExtensionTools()
	})
	return nil
}

func registerToolSearchModule(registry *ModuleRegistry) error {
	registry.RegisterToolProvider(func(session *Session) ([]agentcore.Tool, []ai.Tool) {
		if !session.IsToolSearchEnabled() {
			return nil, nil
		}
		tool := agentcore.Tool{
			Name: "tool_search",
			Execute: func(_ context.Context, call ai.ContentBlock) agentcore.ToolResult {
				query := strings.ToLower(strings.TrimSpace(fmt.Sprint(call.Arguments["query"])))
				metadata := session.visibleToolMetadata()
				if query != "" {
					filtered := make([]map[string]any, 0, len(metadata))
					for _, item := range metadata {
						text := strings.ToLower(fmt.Sprint(item["name"]) + " " + fmt.Sprint(item["description"]) + " " + fmt.Sprint(item["source"]))
						if strings.Contains(text, query) {
							filtered = append(filtered, item)
						}
					}
					metadata = filtered
				}
				return agentcore.ToolResult{
					Text:    fmt.Sprintf("%d tools matched", len(metadata)),
					Details: map[string]any{"tools": metadata},
				}
			},
		}
		spec := ai.Tool{
			Name:        "tool_search",
			Description: "Search the currently visible model tools and return read-only metadata about their names, descriptions, and sources.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Optional substring to match against tool metadata."},
				},
			},
		}
		return []agentcore.Tool{tool}, []ai.Tool{spec}
	})
	if err := registry.RegisterConfigOption(ModuleConfigOption{
		ID:       "tool_search",
		Name:     "Tool search",
		Category: "tools",
		Type:     "select",
		Options:  []string{"off", "on"},
		Get: func(session *Session) string {
			if session.IsToolSearchEnabled() {
				return "on"
			}
			return "off"
		},
		Set: func(session *Session, value string) error {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "on", "true", "1", "yes":
				session.SetToolSearchEnabled(true)
			case "", "off", "false", "0", "no":
				session.SetToolSearchEnabled(false)
			default:
				return fmt.Errorf("invalid tool_search value: %s", value)
			}
			return nil
		},
	}); err != nil {
		return err
	}
	return nil
}

func registerA2AModule(registry *ModuleRegistry) error {
	registry.RegisterToolProvider(func(session *Session) ([]agentcore.Tool, []ai.Tool) {
		return a2a.Tools(session.GetA2AConfig())
	})
	for _, option := range []ModuleConfigOption{
		{
			ID: "a2a_tools", Name: "A2A tools", Category: "a2a", Type: "select",
			Options: []string{"off", "on"},
			Get: func(session *Session) string {
				if session.GetA2AConfig().Enabled {
					return "on"
				}
				return "off"
			},
			Set: func(session *Session, value string) error {
				config := session.GetA2AConfig()
				switch strings.ToLower(strings.TrimSpace(value)) {
				case "on", "true", "1", "yes":
					config.Enabled = true
				case "", "off", "false", "0", "no":
					config.Enabled = false
				default:
					return fmt.Errorf("invalid a2a_tools value: %s", value)
				}
				return session.SetA2AConfig(config)
			},
		},
		{
			ID: "a2a_agents", Name: "A2A agents", Category: "a2a", Type: "text",
			Get: func(session *Session) string {
				names := []string{}
				for _, agent := range session.GetA2AConfig().Agents {
					names = append(names, agent.Name)
				}
				return strings.Join(names, ",")
			},
			Set: func(session *Session, value string) error {
				config := session.GetA2AConfig()
				config.Agents = nil
				for _, item := range commaSeparatedList(value) {
					parts := strings.SplitN(item, "=", 2)
					if len(parts) != 2 {
						return fmt.Errorf("a2a agent must be name=url")
					}
					config.Agents = append(config.Agents, a2a.RemoteAgent{Name: parts[0], URL: parts[1]})
				}
				return session.SetA2AConfig(config)
			},
		},
	} {
		if err := registry.RegisterConfigOption(option); err != nil {
			return err
		}
	}
	if err := registry.RegisterRPCHandler("set_a2a_tools", setA2AToolsRPC); err != nil {
		return err
	}
	return registry.RegisterRPCHandler("get_a2a_agents", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		data := session.GetA2AConfig().Metadata()
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: data}
	})
}

func toolSearchEnabledFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PIGO_TOOL_SEARCH"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Session) visibleToolMetadata() []map[string]any {
	_, specs := s.ensureModuleRegistry().Tools()
	items := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		source := "extension"
		switch {
		case isBuiltinToolName(spec.Name):
			source = "builtin"
		case strings.HasPrefix(spec.Name, "mcp__"):
			source = "mcp"
		case researchToolName(spec.Name):
			source = "research"
		case spec.Name == "tool_search":
			source = "tool_search"
		}
		items = append(items, map[string]any{
			"name":        spec.Name,
			"description": spec.Description,
			"source":      source,
		})
	}
	return items
}

func isBuiltinToolName(name string) bool {
	for _, candidate := range BuiltinToolNames() {
		if name == candidate {
			return true
		}
	}
	return false
}

func researchToolName(name string) bool {
	for _, candidate := range researchadapter.ToolNames() {
		if name == candidate {
			return true
		}
	}
	return false
}

func registerResearchModule(registry *ModuleRegistry) error {
	registry.RegisterToolProvider(func(session *Session) ([]agentcore.Tool, []ai.Tool) {
		config := session.ResearchConfig
		config.Host = sessionResearchHost{session: session}
		config.EventSink = func(event agentcore.Event) {
			session.appendRuntimeEvent(event)
		}
		return researchadapter.Tools(config)
	})
	for _, option := range []ModuleConfigOption{
		{
			ID: "research_tools", Name: "Research tools", Category: "research", Type: "text",
			Get: func(session *Session) string { return strings.Join(session.GetResearchConfig().Tools, ",") },
			Set: func(session *Session, value string) error {
				config := session.GetResearchConfig()
				config.Tools = commaSeparatedList(value)
				return session.SetResearchConfig(config)
			},
		},
		{
			ID: "research_searxng_url", Name: "Research SearXNG URL", Category: "research", Type: "text",
			Get: func(session *Session) string { return session.GetResearchConfig().SearXNGURL },
			Set: func(session *Session, value string) error {
				config := session.GetResearchConfig()
				config.SearXNGURL = value
				return session.SetResearchConfig(config)
			},
		},
		{
			ID: "research_obscura_url", Name: "Research Obscura URL", Category: "research", Type: "text",
			Get: func(session *Session) string { return session.GetResearchConfig().ObscuraURL },
			Set: func(session *Session, value string) error {
				config := session.GetResearchConfig()
				config.ObscuraURL = value
				return session.SetResearchConfig(config)
			},
		},
		{
			ID: "research_nvd_api_key", Name: "Research NVD API key", Category: "research", Type: "text",
			Get: func(session *Session) string {
				if strings.TrimSpace(session.GetResearchConfig().NVDAPIKey) == "" {
					return ""
				}
				return "<configured>"
			},
			Set: func(session *Session, value string) error {
				config := session.GetResearchConfig()
				config.NVDAPIKey = value
				return session.SetResearchConfig(config)
			},
		},
	} {
		if err := registry.RegisterConfigOption(option); err != nil {
			return err
		}
	}
	if err := registry.RegisterRPCHandler("set_research_tools", setResearchToolsRPC); err != nil {
		return err
	}
	return registry.RegisterRPCHandler("get_research_tools", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		config := session.GetResearchConfig()
		data := config.Metadata()
		data["available"] = researchadapter.ToolNames()
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: data}
	})
}

func registerCommandCompressionModule(registry *ModuleRegistry) error {
	for _, option := range []ModuleConfigOption{
		{
			ID: "command_compression", Name: "Command compression", Category: "command_output", Type: "select",
			Options: []string{"off", "auto", "force"},
			Get:     func(session *Session) string { return session.GetCommandCompression().Mode },
			Set: func(session *Session, value string) error {
				config := session.GetCommandCompression()
				config.Mode = value
				return session.SetCommandCompression(config)
			},
		},
		{
			ID: "command_compression_enabled_filters", Name: "Command compression enabled filters", Category: "command_output", Type: "text",
			Get: func(session *Session) string {
				return strings.Join(session.GetCommandCompression().EnabledFilters, ",")
			},
			Set: func(session *Session, value string) error {
				config := session.GetCommandCompression()
				config.EnabledFilters = commaSeparatedList(value)
				return session.SetCommandCompression(config)
			},
		},
		{
			ID: "command_compression_disabled_filters", Name: "Command compression disabled filters", Category: "command_output", Type: "text",
			Get: func(session *Session) string {
				return strings.Join(session.GetCommandCompression().DisabledFilters, ",")
			},
			Set: func(session *Session, value string) error {
				config := session.GetCommandCompression()
				config.DisabledFilters = commaSeparatedList(value)
				return session.SetCommandCompression(config)
			},
		},
	} {
		if err := registry.RegisterConfigOption(option); err != nil {
			return err
		}
	}
	if err := registry.RegisterRPCHandler("set_command_compression", setCommandCompressionRPC); err != nil {
		return err
	}
	return registry.RegisterRPCHandler("get_command_compression", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetCommandCompression().Metadata()}
	})
}

func registerBashPermissionModule(registry *ModuleRegistry) error {
	for _, option := range []ModuleConfigOption{
		{
			ID: "bash_permission_mode", Name: "Bash permission mode", Category: "bash", Type: "select",
			Options: []string{"allow-all", "allow-list"},
			Get:     func(session *Session) string { return session.GetBashPermissionPolicy().Mode },
			Set: func(session *Session, value string) error {
				policy := session.GetBashPermissionPolicy()
				policy.Mode = value
				return session.SetBashPermissionPolicy(policy)
			},
		},
		{
			ID: "bash_permission_allow", Name: "Bash permission allow", Category: "bash", Type: "text",
			Get: func(session *Session) string { return strings.Join(session.GetBashPermissionPolicy().Allow, ",") },
			Set: func(session *Session, value string) error {
				policy := session.GetBashPermissionPolicy()
				policy.Allow = commaSeparatedList(value)
				return session.SetBashPermissionPolicy(policy)
			},
		},
		{
			ID: "bash_permission_deny", Name: "Bash permission deny", Category: "bash", Type: "text",
			Get: func(session *Session) string { return strings.Join(session.GetBashPermissionPolicy().Deny, ",") },
			Set: func(session *Session, value string) error {
				policy := session.GetBashPermissionPolicy()
				policy.Deny = commaSeparatedList(value)
				return session.SetBashPermissionPolicy(policy)
			},
		},
	} {
		if err := registry.RegisterConfigOption(option); err != nil {
			return err
		}
	}
	if err := registry.RegisterRPCHandler("set_bash_permission_policy", setBashPermissionPolicyRPC); err != nil {
		return err
	}
	return registry.RegisterRPCHandler("get_bash_permission_policy", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetBashPermissionPolicy().Metadata()}
	})
}

func registerUsageModule(registry *ModuleRegistry) error {
	for _, entryType := range []string{"usage_ledger", "usage_quota"} {
		handler := ModuleSessionEntryHandler{
			VisibleInTree: false,
			AffectsLeaf:   false,
		}
		if entryType == "usage_ledger" {
			handler.Apply = applyUsageLedgerEntry
		} else {
			handler.Apply = applyUsageQuotaEntry
		}
		handler.ApplyAfterBranch = func(session *Session, entry SessionEntry, _ map[string]struct{}) {
			if apply, ok := session.ensureModuleRegistry().SessionEntryHandler(entry.Type); ok && apply.Apply != nil {
				apply.Apply(session, entry)
			}
		}
		handler.ExportMetadata = func(session *Session, _ []SessionEntry) []SessionEntry {
			return session.metadataEntriesForType(entryType)
		}
		if err := registry.RegisterSessionEntryHandler(entryType, handler); err != nil {
			return err
		}
	}
	for _, option := range []ModuleConfigOption{
		{
			ID: "usage_quota", Name: "Usage quota", Category: "usage", Type: "select",
			Options: []string{"off", "enforce"},
			Get:     func(session *Session) string { return session.GetUsageQuota().Mode },
			Set: func(session *Session, value string) error {
				config := session.GetUsageQuota()
				config.Mode = value
				return session.SetUsageQuota(config)
			},
		},
		usageIntConfigOption("usage_max_input_tokens", "Usage max input tokens", func(config *UsageQuotaConfig, value int) { config.MaxInputTokens = value }, func(config UsageQuotaConfig) int { return config.MaxInputTokens }),
		usageIntConfigOption("usage_max_output_tokens", "Usage max output tokens", func(config *UsageQuotaConfig, value int) { config.MaxOutputTokens = value }, func(config UsageQuotaConfig) int { return config.MaxOutputTokens }),
		usageIntConfigOption("usage_max_cache_read_tokens", "Usage max cache read tokens", func(config *UsageQuotaConfig, value int) { config.MaxCacheReadTokens = value }, func(config UsageQuotaConfig) int { return config.MaxCacheReadTokens }),
		usageIntConfigOption("usage_max_cache_write_tokens", "Usage max cache write tokens", func(config *UsageQuotaConfig, value int) { config.MaxCacheWriteTokens = value }, func(config UsageQuotaConfig) int { return config.MaxCacheWriteTokens }),
		usageIntConfigOption("usage_max_total_tokens", "Usage max total tokens", func(config *UsageQuotaConfig, value int) { config.MaxTotalTokens = value }, func(config UsageQuotaConfig) int { return config.MaxTotalTokens }),
		{
			ID: "usage_max_cost", Name: "Usage max cost", Category: "usage", Type: "text",
			Get: func(session *Session) string { return fmt.Sprintf("%g", session.GetUsageQuota().MaxCost) },
			Set: func(session *Session, value string) error {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err != nil {
					return err
				}
				config := session.GetUsageQuota()
				config.MaxCost = parsed
				return session.SetUsageQuota(config)
			},
		},
	} {
		if err := registry.RegisterConfigOption(option); err != nil {
			return err
		}
	}
	if err := registry.RegisterRPCHandler("set_usage_quota", setUsageQuotaRPC); err != nil {
		return err
	}
	if err := registry.RegisterRPCHandler("get_usage_quota", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.UsageQuotaStatus(ai.Usage{})}
	}); err != nil {
		return err
	}
	return registry.RegisterRPCHandler("get_usage_ledger", func(_ context.Context, session *Session, command rpcCommand) rpcResponse {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: map[string]any{
			"entries": session.UsageLedgerEntries(command.Limit),
			"quota":   session.UsageQuotaStatus(ai.Usage{}),
		}}
	})
}

func usageIntConfigOption(id, name string, set func(*UsageQuotaConfig, int), get func(UsageQuotaConfig) int) ModuleConfigOption {
	return ModuleConfigOption{
		ID: id, Name: name, Category: "usage", Type: "text",
		Get: func(session *Session) string { return fmt.Sprint(get(session.GetUsageQuota())) },
		Set: func(session *Session, value string) error {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return err
			}
			config := session.GetUsageQuota()
			set(&config, parsed)
			return session.SetUsageQuota(config)
		},
	}
}

func commaSeparatedList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseConfigBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value %q", value)
	}
}

func applyLabelEntry(session *Session, entry SessionEntry) {
	if entry.TargetID == "" {
		return
	}
	if session.labelsByID == nil {
		session.labelsByID = map[string]string{}
	}
	if session.labelTimes == nil {
		session.labelTimes = map[string]string{}
	}
	if strings.TrimSpace(entry.Label) == "" {
		delete(session.labelsByID, entry.TargetID)
		delete(session.labelTimes, entry.TargetID)
		return
	}
	session.labelsByID[entry.TargetID] = entry.Label
	session.labelTimes[entry.TargetID] = entry.Timestamp
}

func applyUsageLedgerEntry(session *Session, entry SessionEntry) {
	if entry.UsageLedger != nil {
		session.UsageLedger = append(session.UsageLedger, *entry.UsageLedger)
	}
}

func applyUsageQuotaEntry(session *Session, entry SessionEntry) {
	if entry.UsageQuota != nil {
		session.UsageQuota = entry.UsageQuota.Normalized()
	}
}

func setCommandCompressionRPC(_ context.Context, session *Session, command rpcCommand) rpcResponse {
	config := session.GetCommandCompression()
	if strings.TrimSpace(command.Mode) != "" {
		config.Mode = command.Mode
	}
	if command.EnabledFilters != nil {
		config.EnabledFilters = command.EnabledFilters
	}
	if command.DisabledFilters != nil {
		config.DisabledFilters = command.DisabledFilters
	}
	if command.MaxBytes > 0 {
		config.MaxBytes = command.MaxBytes
	}
	if err := session.SetCommandCompression(config); err != nil {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
	}
	return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetCommandCompression().Metadata()}
}

func setBashPermissionPolicyRPC(_ context.Context, session *Session, command rpcCommand) rpcResponse {
	policy := session.GetBashPermissionPolicy()
	if strings.TrimSpace(command.Mode) != "" {
		policy.Mode = command.Mode
	}
	if command.Allow != nil {
		policy.Allow = command.Allow
	}
	if command.Deny != nil {
		policy.Deny = command.Deny
	}
	if err := session.SetBashPermissionPolicy(policy); err != nil {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
	}
	return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetBashPermissionPolicy().Metadata()}
}

func setBuiltinToolPolicyRPC(_ context.Context, session *Session, command rpcCommand) rpcResponse {
	policy := session.GetBuiltinToolPolicy()
	if command.Tools != nil {
		policy.Enabled = command.Tools
	}
	if command.EnabledTools != nil {
		policy.Enabled = command.EnabledTools
	}
	if command.DisabledTools != nil {
		policy.Disabled = command.DisabledTools
	}
	if err := session.SetBuiltinToolPolicy(policy); err != nil {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
	}
	return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetBuiltinToolPolicy().Metadata()}
}

func setPromptInjectionGuardRPC(_ context.Context, session *Session, command rpcCommand) rpcResponse {
	config := session.GetPromptInjectionConfig()
	if strings.TrimSpace(command.Mode) != "" {
		config.Mode = command.Mode
	}
	if command.Sources != nil {
		config.Sources = command.Sources
	}
	if command.SensitiveTools != nil {
		config.SensitiveTools = command.SensitiveTools
	}
	if err := session.SetPromptInjectionConfig(config); err != nil {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
	}
	return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetPromptInjectionConfig().Metadata()}
}

func setResearchToolsRPC(_ context.Context, session *Session, command rpcCommand) rpcResponse {
	config := session.GetResearchConfig()
	if command.Tools != nil {
		config.Tools = command.Tools
	}
	if command.ResearchTools != nil {
		config.Tools = command.ResearchTools
	}
	if strings.TrimSpace(command.SearXNGURL) != "" {
		config.SearXNGURL = command.SearXNGURL
	}
	if strings.TrimSpace(command.ObscuraURL) != "" {
		config.ObscuraURL = command.ObscuraURL
	}
	if strings.TrimSpace(command.NVDAPIKey) != "" {
		config.NVDAPIKey = command.NVDAPIKey
	}
	if err := session.SetResearchConfig(config); err != nil {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
	}
	return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetResearchConfig().Metadata()}
}

func setA2AToolsRPC(_ context.Context, session *Session, command rpcCommand) rpcResponse {
	config := session.GetA2AConfig()
	if command.Enabled != nil {
		config.Enabled = *command.Enabled
	}
	if command.Data != nil {
		data, err := json.Marshal(command.Data)
		if err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
		if err := json.Unmarshal(data, &config); err != nil {
			return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
		}
	}
	if err := session.SetA2AConfig(config); err != nil {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
	}
	return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetA2AConfig().Metadata()}
}

func setDomainConfigRPC(_ context.Context, session *Session, command rpcCommand) rpcResponse {
	config := session.GetDomainConfig()
	if strings.TrimSpace(command.Purpose) != "" {
		config.Purpose = command.Purpose
	}
	if command.ContextFiles != nil {
		config.ContextFiles = command.ContextFiles
	}
	if command.IncludeGitContext != nil {
		config.IncludeGitContext = command.IncludeGitContext
	}
	if command.IncludePackageContext != nil {
		config.IncludePackageContext = command.IncludePackageContext
	}
	if command.ExtraInstructions != nil {
		config.ExtraInstructions = *command.ExtraInstructions
	}
	if err := session.SetDomainConfig(config); err != nil {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
	}
	return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetDomainConfig().Metadata()}
}

func setUsageQuotaRPC(_ context.Context, session *Session, command rpcCommand) rpcResponse {
	config := session.GetUsageQuota()
	if strings.TrimSpace(command.Mode) != "" {
		config.Mode = command.Mode
	}
	if command.MaxInputTokens != nil {
		config.MaxInputTokens = *command.MaxInputTokens
	}
	if command.MaxOutputTokens != nil {
		config.MaxOutputTokens = *command.MaxOutputTokens
	}
	if command.MaxCacheReadTokens != nil {
		config.MaxCacheReadTokens = *command.MaxCacheReadTokens
	}
	if command.MaxCacheWriteTokens != nil {
		config.MaxCacheWriteTokens = *command.MaxCacheWriteTokens
	}
	if command.MaxTotalTokens != nil {
		config.MaxTotalTokens = *command.MaxTotalTokens
	}
	if command.MaxCost != nil {
		config.MaxCost = *command.MaxCost
	}
	if err := session.SetUsageQuota(config); err != nil {
		return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: false, Error: err.Error()}
	}
	return rpcResponse{ID: command.ID, Type: "response", Command: command.Type, Success: true, Data: session.GetUsageQuota().Metadata()}
}
