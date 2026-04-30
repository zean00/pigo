package codingagent

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const configDirName = ".pi"

type ResourceLoadOptions struct {
	AgentDir        string
	PromptPaths     []string
	SkillPaths      []string
	IncludeDefaults bool
}

func DefaultAgentDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, configDirName, "agent")
}

func (s *Session) LoadSlashCommandResources(options ResourceLoadOptions) {
	agentDir := options.AgentDir
	if agentDir == "" {
		agentDir = DefaultAgentDir()
	}
	prompts, promptDiagnostics := loadPromptTemplateCommands(s.Root, agentDir, options)
	skills, skillDiagnostics := loadSkillCommands(s.Root, agentDir, options)
	s.SetPromptTemplates(prompts)
	s.SetSkills(skills)
	s.mu.Lock()
	s.resourceDiagnostics = append(promptDiagnostics, skillDiagnostics...)
	s.mu.Unlock()
}

func loadPromptTemplateCommands(root, agentDir string, options ResourceLoadOptions) ([]SlashCommandInfo, []ResourceDiagnostic) {
	var commands []SlashCommandInfo
	var diagnostics []ResourceDiagnostic
	if options.IncludeDefaults {
		commands = append(commands, loadPromptCommandsFromDir(filepath.Join(agentDir, "prompts"), "user")...)
		commands = append(commands, loadPromptCommandsFromDir(filepath.Join(root, configDirName, "prompts"), "project")...)
	}
	for _, rawPath := range options.PromptPaths {
		path := resolveResourcePath(root, rawPath)
		info, err := os.Stat(path)
		if err != nil {
			diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: "prompt template path does not exist", Path: path})
			continue
		}
		if info.IsDir() {
			commands = append(commands, loadPromptCommandsFromDir(path, "temporary")...)
			continue
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".md") {
			if command, ok := loadPromptCommandFromFile(path, "temporary", filepath.Dir(path)); ok {
				commands = append(commands, command)
			}
		} else {
			diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: "prompt template path is not a markdown file", Path: path})
		}
	}
	deduped, collisions := dedupeResourceCommands(commands, "prompt")
	return deduped, append(diagnostics, collisions...)
}

func loadPromptCommandsFromDir(dir, scope string) []SlashCommandInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	commands := make([]SlashCommandInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if command, ok := loadPromptCommandFromFile(path, scope, dir); ok {
			commands = append(commands, command)
		}
	}
	return commands
}

func loadPromptCommandFromFile(path, scope, baseDir string) (SlashCommandInfo, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return SlashCommandInfo{}, false
	}
	frontmatter, body := parseSimpleFrontmatter(string(content))
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	description := strings.TrimSpace(frontmatter["description"])
	if description == "" {
		description = firstNonEmptyLine(body)
	}
	if len(description) > 60 {
		description = description[:60] + "..."
	}
	return SlashCommandInfo{
		Name:        name,
		Description: description,
		Source:      "prompt",
		SourceInfo:  sourceInfo(path, scope, baseDir),
		Content:     body,
		FilePath:    path,
		BaseDir:     baseDir,
	}, true
}

func loadSkillCommands(root, agentDir string, options ResourceLoadOptions) ([]SlashCommandInfo, []ResourceDiagnostic) {
	var commands []SlashCommandInfo
	var diagnostics []ResourceDiagnostic
	if options.IncludeDefaults {
		loaded, warnings := loadSkillCommandsFromDir(filepath.Join(agentDir, "skills"), "user", true)
		commands = append(commands, loaded...)
		diagnostics = append(diagnostics, warnings...)
		loaded, warnings = loadSkillCommandsFromDir(filepath.Join(root, configDirName, "skills"), "project", true)
		commands = append(commands, loaded...)
		diagnostics = append(diagnostics, warnings...)
	}
	for _, rawPath := range options.SkillPaths {
		path := resolveResourcePath(root, rawPath)
		info, err := os.Stat(path)
		if err != nil {
			diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: "skill path does not exist", Path: path})
			continue
		}
		if info.IsDir() {
			loaded, warnings := loadSkillCommandsFromDir(path, "temporary", true)
			commands = append(commands, loaded...)
			diagnostics = append(diagnostics, warnings...)
			continue
		}
		if strings.HasSuffix(info.Name(), ".md") {
			if command, warnings, ok := loadSkillCommandFromFile(path, "temporary"); ok {
				commands = append(commands, command)
				diagnostics = append(diagnostics, warnings...)
			} else {
				diagnostics = append(diagnostics, warnings...)
			}
		} else {
			diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: "skill path is not a markdown file", Path: path})
		}
	}
	deduped, collisions := dedupeResourceCommands(commands, "skill")
	return deduped, append(diagnostics, collisions...)
}

func loadSkillCommandsFromDir(dir, scope string, includeRootFiles bool) ([]SlashCommandInfo, []ResourceDiagnostic) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		if entry.Name() == "SKILL.md" && !entry.IsDir() {
			if command, diagnostics, ok := loadSkillCommandFromFile(filepath.Join(dir, entry.Name()), scope); ok {
				return []SlashCommandInfo{command}, diagnostics
			} else {
				return nil, diagnostics
			}
		}
	}
	var commands []SlashCommandInfo
	var diagnostics []ResourceDiagnostic
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			loaded, warnings := loadSkillCommandsFromDir(path, scope, false)
			commands = append(commands, loaded...)
			diagnostics = append(diagnostics, warnings...)
			continue
		}
		if includeRootFiles && strings.HasSuffix(entry.Name(), ".md") {
			if command, warnings, ok := loadSkillCommandFromFile(path, scope); ok {
				commands = append(commands, command)
				diagnostics = append(diagnostics, warnings...)
			} else {
				diagnostics = append(diagnostics, warnings...)
			}
		}
	}
	return commands, diagnostics
}

func loadSkillCommandFromFile(path, scope string) (SlashCommandInfo, []ResourceDiagnostic, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return SlashCommandInfo{}, []ResourceDiagnostic{{Type: "warning", Message: "failed to read skill file", Path: path}}, false
	}
	frontmatter, _ := parseSimpleFrontmatter(string(content))
	description := strings.TrimSpace(frontmatter["description"])
	if description == "" {
		return SlashCommandInfo{}, []ResourceDiagnostic{{Type: "warning", Message: "description is required", Path: path}}, false
	}
	name := strings.TrimSpace(frontmatter["name"])
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}
	diagnostics := validateSkillMetadata(path, name, filepath.Base(filepath.Dir(path)), description)
	return SlashCommandInfo{
		Name:        "skill:" + name,
		Description: description,
		Source:      "skill",
		SourceInfo:  sourceInfo(path, scope, filepath.Dir(path)),
		Content:     string(content),
		FilePath:    path,
		BaseDir:     filepath.Dir(path),
		Disabled:    parseFrontmatterBool(frontmatter["disable-model-invocation"]),
	}, diagnostics, true
}

var validSkillNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

func validateSkillMetadata(path, name, parentDir, description string) []ResourceDiagnostic {
	var diagnostics []ResourceDiagnostic
	if name != parentDir {
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: `name "` + name + `" does not match parent directory "` + parentDir + `"`, Path: path})
	}
	if len(name) > 64 {
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: "name exceeds 64 characters", Path: path})
	}
	if !validSkillNamePattern.MatchString(name) {
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: "name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)", Path: path})
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: "name must not start or end with a hyphen", Path: path})
	}
	if strings.Contains(name, "--") {
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: "name must not contain consecutive hyphens", Path: path})
	}
	if len(description) > 1024 {
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: "description exceeds 1024 characters", Path: path})
	}
	return diagnostics
}

func dedupeResourceCommands(commands []SlashCommandInfo, resourceType string) ([]SlashCommandInfo, []ResourceDiagnostic) {
	seen := map[string]SlashCommandInfo{}
	out := make([]SlashCommandInfo, 0, len(commands))
	var diagnostics []ResourceDiagnostic
	for _, command := range commands {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if existing, ok := seen[key]; ok {
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type:    "collision",
				Message: `name "` + name + `" collision`,
				Path:    command.FilePath,
				Collision: &ResourceCollision{
					ResourceType: resourceType,
					Name:         name,
					WinnerPath:   existing.FilePath,
					LoserPath:    command.FilePath,
				},
			})
			continue
		}
		command.Name = name
		seen[key] = command
		out = append(out, command)
	}
	return out, diagnostics
}

func resolveResourcePath(root, rawPath string) string {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return root
	}
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, path)
}

func sourceInfo(path, scope, baseDir string) map[string]any {
	return map[string]any{
		"path":    path,
		"source":  "local",
		"scope":   scope,
		"origin":  "top-level",
		"baseDir": baseDir,
	}
}

func parseSimpleFrontmatter(content string) (map[string]string, string) {
	frontmatter := map[string]string{}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return frontmatter, content
	}
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return frontmatter, content
	}
	block := normalized[4 : 4+end]
	body := normalized[4+end:]
	if strings.HasPrefix(body, "\n---") {
		body = strings.TrimPrefix(body, "\n---")
	}
	body = strings.TrimPrefix(body, "\n")
	for _, line := range strings.Split(block, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			frontmatter[key] = value
		}
	}
	return frontmatter, body
}

func firstNonEmptyLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func parseFrontmatterBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "1":
		return true
	default:
		return false
	}
}
