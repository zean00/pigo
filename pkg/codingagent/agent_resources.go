package codingagent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type AgentProfile struct {
	Name          string         `json:"name" yaml:"name"`
	Description   string         `json:"description,omitempty" yaml:"description"`
	Provider      string         `json:"provider,omitempty" yaml:"provider"`
	ModelID       string         `json:"modelId,omitempty" yaml:"modelId"`
	ThinkingLevel string         `json:"thinkingLevel,omitempty" yaml:"thinkingLevel"`
	Tools         []string       `json:"tools,omitempty" yaml:"tools"`
	Instructions  string         `json:"instructions,omitempty" yaml:"instructions"`
	Source        string         `json:"source" yaml:"-"`
	SourceInfo    map[string]any `json:"sourceInfo,omitempty" yaml:"-"`
	FilePath      string         `json:"-"`
	BaseDir       string         `json:"-"`
}

type AgentTeam struct {
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description,omitempty" yaml:"description"`
	Agents      []string       `json:"agents,omitempty" yaml:"agents"`
	Source      string         `json:"source" yaml:"-"`
	SourceInfo  map[string]any `json:"sourceInfo,omitempty" yaml:"-"`
	FilePath    string         `json:"-"`
}

type AgentChain struct {
	Name        string           `json:"name" yaml:"name"`
	Description string           `json:"description,omitempty" yaml:"description"`
	Steps       []AgentChainStep `json:"steps,omitempty" yaml:"steps"`
	Source      string           `json:"source" yaml:"-"`
	SourceInfo  map[string]any   `json:"sourceInfo,omitempty" yaml:"-"`
	FilePath    string           `json:"-"`
}

type AgentChainStep struct {
	Name   string `json:"name,omitempty" yaml:"name"`
	Agent  string `json:"agent,omitempty" yaml:"agent"`
	Prompt string `json:"prompt,omitempty" yaml:"prompt"`
}

func loadAgentProfiles(root, agentDir string, options ResourceLoadOptions) ([]AgentProfile, []ResourceDiagnostic) {
	var profiles []AgentProfile
	var diagnostics []ResourceDiagnostic
	if options.IncludeDefaults {
		profiles = append(profiles, loadAgentProfilesFromDir(filepath.Join(agentDir, "agents"), "user")...)
		profiles = append(profiles, loadAgentProfilesFromDir(filepath.Join(root, configDirName, "agents"), "project")...)
	}
	deduped, collisions := dedupeAgentProfiles(profiles)
	return deduped, append(diagnostics, collisions...)
}

func loadAgentProfilesFromDir(dir, scope string) []AgentProfile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	profiles := make([]AgentProfile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if profile, ok := loadAgentProfileFromFile(path, scope, dir); ok {
			profiles = append(profiles, profile)
		}
	}
	return profiles
}

func loadAgentProfileFromFile(path, scope, baseDir string) (AgentProfile, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return AgentProfile{}, false
	}
	frontmatter, body := parseSimpleFrontmatter(string(content))
	name := strings.TrimSpace(frontmatter["name"])
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".md")
	}
	description := strings.TrimSpace(frontmatter["description"])
	if description == "" {
		description = firstNonEmptyLine(body)
	}
	provider := strings.TrimSpace(frontmatter["provider"])
	modelID := strings.TrimSpace(frontmatter["modelId"])
	if modelID == "" {
		modelID = strings.TrimSpace(frontmatter["model"])
	}
	return AgentProfile{
		Name:          name,
		Description:   description,
		Provider:      provider,
		ModelID:       modelID,
		ThinkingLevel: strings.TrimSpace(frontmatter["thinkingLevel"]),
		Tools:         commaSeparatedList(frontmatter["tools"]),
		Instructions:  strings.TrimSpace(body),
		Source:        scope,
		SourceInfo:    sourceInfo(path, scope, baseDir),
		FilePath:      path,
		BaseDir:       baseDir,
	}, true
}

func dedupeAgentProfiles(profiles []AgentProfile) ([]AgentProfile, []ResourceDiagnostic) {
	seen := map[string]AgentProfile{}
	out := make([]AgentProfile, 0, len(profiles))
	var diagnostics []ResourceDiagnostic
	for _, profile := range profiles {
		name := strings.TrimSpace(profile.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if existing, ok := seen[key]; ok {
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type:    "collision",
				Message: `name "` + name + `" collision`,
				Path:    profile.FilePath,
				Collision: &ResourceCollision{
					ResourceType: "agent",
					Name:         name,
					WinnerPath:   existing.FilePath,
					LoserPath:    profile.FilePath,
				},
			})
			continue
		}
		profile.Name = name
		seen[key] = profile
		out = append(out, profile)
	}
	return out, diagnostics
}

func loadAgentTeams(root, agentDir string, options ResourceLoadOptions) ([]AgentTeam, []ResourceDiagnostic) {
	if !options.IncludeDefaults {
		return nil, nil
	}
	var teams []AgentTeam
	var diagnostics []ResourceDiagnostic
	if loaded, warnings := loadAgentTeamsFile(filepath.Join(agentDir, "agents", "teams.yaml"), "user", filepath.Join(agentDir, "agents")); len(loaded) > 0 || len(warnings) > 0 {
		teams = append(teams, loaded...)
		diagnostics = append(diagnostics, warnings...)
	}
	if loaded, warnings := loadAgentTeamsFile(filepath.Join(root, configDirName, "agents", "teams.yaml"), "project", filepath.Join(root, configDirName, "agents")); len(loaded) > 0 || len(warnings) > 0 {
		teams = append(teams, loaded...)
		diagnostics = append(diagnostics, warnings...)
	}
	deduped, collisions := dedupeAgentTeams(teams)
	return deduped, append(diagnostics, collisions...)
}

func loadAgentChains(root, agentDir string, options ResourceLoadOptions) ([]AgentChain, []ResourceDiagnostic) {
	if !options.IncludeDefaults {
		return nil, nil
	}
	var chains []AgentChain
	var diagnostics []ResourceDiagnostic
	if loaded, warnings := loadAgentChainsFile(filepath.Join(agentDir, "agents", "chains.yaml"), "user", filepath.Join(agentDir, "agents")); len(loaded) > 0 || len(warnings) > 0 {
		chains = append(chains, loaded...)
		diagnostics = append(diagnostics, warnings...)
	}
	if loaded, warnings := loadAgentChainsFile(filepath.Join(root, configDirName, "agents", "chains.yaml"), "project", filepath.Join(root, configDirName, "agents")); len(loaded) > 0 || len(warnings) > 0 {
		chains = append(chains, loaded...)
		diagnostics = append(diagnostics, warnings...)
	}
	deduped, collisions := dedupeAgentChains(chains)
	return deduped, append(diagnostics, collisions...)
}

func loadAgentTeamsFile(path, scope, baseDir string) ([]AgentTeam, []ResourceDiagnostic) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var payload struct {
		Teams []AgentTeam `yaml:"teams"`
	}
	if err := yaml.Unmarshal(data, &payload); err != nil {
		return nil, []ResourceDiagnostic{{Type: "warning", Message: "failed to parse agent teams", Path: path}}
	}
	for i := range payload.Teams {
		payload.Teams[i].Name = strings.TrimSpace(payload.Teams[i].Name)
		payload.Teams[i].Source = scope
		payload.Teams[i].SourceInfo = sourceInfo(path, scope, baseDir)
		payload.Teams[i].FilePath = path
	}
	return payload.Teams, nil
}

func loadAgentChainsFile(path, scope, baseDir string) ([]AgentChain, []ResourceDiagnostic) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var payload struct {
		Chains []AgentChain `yaml:"chains"`
	}
	if err := yaml.Unmarshal(data, &payload); err != nil {
		return nil, []ResourceDiagnostic{{Type: "warning", Message: "failed to parse agent chains", Path: path}}
	}
	for i := range payload.Chains {
		payload.Chains[i].Name = strings.TrimSpace(payload.Chains[i].Name)
		payload.Chains[i].Source = scope
		payload.Chains[i].SourceInfo = sourceInfo(path, scope, baseDir)
		payload.Chains[i].FilePath = path
	}
	return payload.Chains, nil
}

func dedupeAgentTeams(teams []AgentTeam) ([]AgentTeam, []ResourceDiagnostic) {
	seen := map[string]AgentTeam{}
	out := make([]AgentTeam, 0, len(teams))
	var diagnostics []ResourceDiagnostic
	for _, team := range teams {
		if team.Name == "" {
			continue
		}
		key := strings.ToLower(team.Name)
		if existing, ok := seen[key]; ok {
			diagnostics = append(diagnostics, resourceNameCollision("agent_team", team.Name, existing.FilePath, team.FilePath))
			continue
		}
		seen[key] = team
		out = append(out, team)
	}
	return out, diagnostics
}

func dedupeAgentChains(chains []AgentChain) ([]AgentChain, []ResourceDiagnostic) {
	seen := map[string]AgentChain{}
	out := make([]AgentChain, 0, len(chains))
	var diagnostics []ResourceDiagnostic
	for _, chain := range chains {
		if chain.Name == "" {
			continue
		}
		key := strings.ToLower(chain.Name)
		if existing, ok := seen[key]; ok {
			diagnostics = append(diagnostics, resourceNameCollision("agent_chain", chain.Name, existing.FilePath, chain.FilePath))
			continue
		}
		seen[key] = chain
		out = append(out, chain)
	}
	return out, diagnostics
}

func resourceNameCollision(resourceType, name, winnerPath, loserPath string) ResourceDiagnostic {
	return ResourceDiagnostic{
		Type:    "collision",
		Message: `name "` + name + `" collision`,
		Path:    loserPath,
		Collision: &ResourceCollision{
			ResourceType: resourceType,
			Name:         name,
			WinnerPath:   winnerPath,
			LoserPath:    loserPath,
		},
	}
}
