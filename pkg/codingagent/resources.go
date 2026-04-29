package codingagent

import (
	"os"
	"path/filepath"
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
	s.SetPromptTemplates(loadPromptTemplateCommands(s.Root, agentDir, options))
	s.SetSkills(loadSkillCommands(s.Root, agentDir, options))
}

func loadPromptTemplateCommands(root, agentDir string, options ResourceLoadOptions) []SlashCommandInfo {
	var commands []SlashCommandInfo
	if options.IncludeDefaults {
		commands = append(commands, loadPromptCommandsFromDir(filepath.Join(agentDir, "prompts"), "user")...)
		commands = append(commands, loadPromptCommandsFromDir(filepath.Join(root, configDirName, "prompts"), "project")...)
	}
	for _, rawPath := range options.PromptPaths {
		path := resolveResourcePath(root, rawPath)
		info, err := os.Stat(path)
		if err != nil {
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
		}
	}
	return dedupeSlashCommands(commands)
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

func loadSkillCommands(root, agentDir string, options ResourceLoadOptions) []SlashCommandInfo {
	var commands []SlashCommandInfo
	if options.IncludeDefaults {
		commands = append(commands, loadSkillCommandsFromDir(filepath.Join(agentDir, "skills"), "user", true)...)
		commands = append(commands, loadSkillCommandsFromDir(filepath.Join(root, configDirName, "skills"), "project", true)...)
	}
	for _, rawPath := range options.SkillPaths {
		path := resolveResourcePath(root, rawPath)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			commands = append(commands, loadSkillCommandsFromDir(path, "temporary", true)...)
			continue
		}
		if strings.HasSuffix(info.Name(), ".md") {
			if command, ok := loadSkillCommandFromFile(path, "temporary"); ok {
				commands = append(commands, command)
			}
		}
	}
	return dedupeSlashCommands(commands)
}

func loadSkillCommandsFromDir(dir, scope string, includeRootFiles bool) []SlashCommandInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		if entry.Name() == "SKILL.md" && !entry.IsDir() {
			if command, ok := loadSkillCommandFromFile(filepath.Join(dir, entry.Name()), scope); ok {
				return []SlashCommandInfo{command}
			}
			return nil
		}
	}
	var commands []SlashCommandInfo
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			commands = append(commands, loadSkillCommandsFromDir(path, scope, false)...)
			continue
		}
		if includeRootFiles && strings.HasSuffix(entry.Name(), ".md") {
			if command, ok := loadSkillCommandFromFile(path, scope); ok {
				commands = append(commands, command)
			}
		}
	}
	return commands
}

func loadSkillCommandFromFile(path, scope string) (SlashCommandInfo, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return SlashCommandInfo{}, false
	}
	frontmatter, _ := parseSimpleFrontmatter(string(content))
	description := strings.TrimSpace(frontmatter["description"])
	if description == "" {
		return SlashCommandInfo{}, false
	}
	name := strings.TrimSpace(frontmatter["name"])
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}
	return SlashCommandInfo{
		Name:        "skill:" + name,
		Description: description,
		Source:      "skill",
		SourceInfo:  sourceInfo(path, scope, filepath.Dir(path)),
		Content:     string(content),
		FilePath:    path,
		BaseDir:     filepath.Dir(path),
		Disabled:    parseFrontmatterBool(frontmatter["disable-model-invocation"]),
	}, true
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
