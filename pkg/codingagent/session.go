package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/badlogic/pigo/pkg/a2a"
	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
	"github.com/badlogic/pigo/pkg/orchestrator"
	"github.com/badlogic/pigo/pkg/researchadapter"
	"golang.org/x/text/unicode/norm"
)

const maxToolOutputBytes = 20_000

type AssistantTurn = agentcore.AssistantTurn

type SessionInput struct {
	WorkspaceFiles     map[string]string
	Prompts            []string
	Turns              []AssistantTurn
	ExpectedFiles      []string
	Provider           string
	ModelID            string
	ThinkingLevel      string
	ToolExecution      agentcore.ToolExecutionMode
	AutoRetry          bool
	AutoCompaction     bool
	ShellCommandPrefix string
	ToolOutputLimit    int
	CommandCompression CommandOutputCompressionConfig
	BashPermission     BashPermissionPolicy
	BuiltinToolPolicy  BuiltinToolPolicy
	A2AConfig          a2a.Config
	OrchestratorConfig orchestrator.Config
	UsageQuota         UsageQuotaConfig
	ResearchConfig     researchadapter.Config
	DomainConfig       SessionDomainConfig
	PromptInjection    PromptInjectionConfig
}

type SessionResult struct {
	Events            []agentcore.Event
	Messages          []agentcore.Message
	SessionEntryTypes []string
	Files             map[string]string
}

func RunHeadlessSession(ctx context.Context, root string, input SessionInput) (SessionResult, error) {
	for name, content := range input.WorkspaceFiles {
		if err := WriteWorkspaceFile(root, name, content); err != nil {
			return SessionResult{}, err
		}
	}

	session := NewSession(root, input.Turns)
	if input.Provider != "" || input.ModelID != "" {
		if _, err := session.SetModel(input.Provider, input.ModelID); err != nil {
			return SessionResult{}, err
		}
	}
	if input.ThinkingLevel != "" {
		if err := session.SetThinkingLevel(input.ThinkingLevel); err != nil {
			return SessionResult{}, err
		}
	}
	if input.ToolExecution != "" {
		if err := session.SetToolExecutionMode(string(input.ToolExecution)); err != nil {
			return SessionResult{}, err
		}
	}
	session.AutoRetry = input.AutoRetry
	session.AutoCompaction = input.AutoCompaction
	session.ShellCommandPrefix = input.ShellCommandPrefix
	session.ToolOutputLimit = input.ToolOutputLimit
	if input.CommandCompression.Mode != "" || len(input.CommandCompression.EnabledFilters) > 0 || len(input.CommandCompression.DisabledFilters) > 0 || input.CommandCompression.MaxBytes > 0 {
		session.CommandCompression = input.CommandCompression
	}
	if input.BashPermission.Mode != "" || len(input.BashPermission.Allow) > 0 || len(input.BashPermission.Deny) > 0 {
		if err := session.SetBashPermissionPolicy(input.BashPermission); err != nil {
			return SessionResult{}, err
		}
	}
	if len(input.BuiltinToolPolicy.Enabled) > 0 || len(input.BuiltinToolPolicy.Disabled) > 0 {
		if err := session.SetBuiltinToolPolicy(input.BuiltinToolPolicy); err != nil {
			return SessionResult{}, err
		}
	}
	if input.A2AConfig.Enabled || len(input.A2AConfig.Agents) > 0 {
		if err := session.SetA2AConfig(input.A2AConfig); err != nil {
			return SessionResult{}, err
		}
	}
	if input.OrchestratorConfig.Enabled || input.OrchestratorConfig.MaxParallel > 0 || input.OrchestratorConfig.TimeoutMillis > 0 || len(input.OrchestratorConfig.Agents) > 0 || input.OrchestratorConfig.Reducer != "" {
		if err := session.SetOrchestratorConfig(input.OrchestratorConfig); err != nil {
			return SessionResult{}, err
		}
	}
	if input.UsageQuota.Mode != "" || input.UsageQuota.MaxInputTokens > 0 || input.UsageQuota.MaxOutputTokens > 0 || input.UsageQuota.MaxCacheReadTokens > 0 || input.UsageQuota.MaxCacheWriteTokens > 0 || input.UsageQuota.MaxTotalTokens > 0 || input.UsageQuota.MaxCost > 0 {
		if err := session.SetUsageQuota(input.UsageQuota); err != nil {
			return SessionResult{}, err
		}
	}
	if len(input.ResearchConfig.Tools) > 0 || input.ResearchConfig.SearXNGURL != "" || input.ResearchConfig.ObscuraURL != "" {
		if err := session.SetResearchConfig(input.ResearchConfig); err != nil {
			return SessionResult{}, err
		}
	}
	if input.DomainConfig.Purpose != "" || len(input.DomainConfig.ContextFiles) > 0 || input.DomainConfig.IncludeGitContext != nil || input.DomainConfig.IncludePackageContext != nil || input.DomainConfig.ExtraInstructions != "" {
		if err := session.SetDomainConfig(input.DomainConfig); err != nil {
			return SessionResult{}, err
		}
	}
	if input.PromptInjection.Mode != "" || len(input.PromptInjection.Sources) > 0 || len(input.PromptInjection.SensitiveTools) > 0 {
		if err := session.SetPromptInjectionConfig(input.PromptInjection); err != nil {
			return SessionResult{}, err
		}
	}
	for _, prompt := range input.Prompts {
		if err := session.Prompt(ctx, prompt); err != nil {
			return SessionResult{}, err
		}
	}

	files := map[string]string{}
	for _, name := range input.ExpectedFiles {
		path, err := ResolveWorkspacePath(root, name)
		if err != nil {
			return SessionResult{}, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return SessionResult{}, err
		}
		files[name] = string(data)
	}

	return SessionResult{
		Events:            session.Events,
		Messages:          session.Messages,
		SessionEntryTypes: session.SessionEntryTypes,
		Files:             files,
	}, nil
}

func BuiltinTools(root string) []agentcore.Tool {
	return BuiltinToolsWithOptions(root, BuiltinToolOptions{})
}

type BuiltinToolOptions struct {
	OutputLimit        int
	ShellCommandPrefix string
	CommandCompression CommandOutputCompressionConfig
	BashPermission     BashPermissionPolicy
	BuiltinToolPolicy  BuiltinToolPolicy
}

func BuiltinToolsWithOptions(root string, options BuiltinToolOptions) []agentcore.Tool {
	outputLimit := options.OutputLimit
	if outputLimit <= 0 {
		outputLimit = maxToolOutputBytes
	}
	compression := options.CommandCompression.Normalized()
	if compression.MaxBytes <= 0 {
		compression.MaxBytes = outputLimit
	}
	tools := []agentcore.Tool{
		{
			Name: "bash",
			Execute: func(ctx context.Context, call ai.ContentBlock) agentcore.ToolResult {
				command, _ := call.Arguments["command"].(string)
				timeoutSeconds, _ := call.Arguments["timeout"].(float64)
				command = applyShellCommandPrefix(command, options.ShellCommandPrefix)
				decision := EvaluateBashPermission(command, options.BashPermission)
				if !decision.Allowed {
					details := bashPermissionDetails(decision)
					details["command"] = command
					details["exitCode"] = 126
					return agentcore.ToolResult{Text: decision.Reason, Details: details, IsError: true}
				}
				output, exitCode, err := RunBashCommand(ctx, root, command, timeoutSeconds)
				if err != nil {
					return agentcore.ToolResult{Text: err.Error(), IsError: true}
				}
				if output == "" {
					output = "(no output)"
				}
				compressed := CompressCommandOutput(command, output, exitCode, compression)
				output, truncated := truncateToolOutput(compressed.Output, outputLimit)
				compressed.Output = output
				compressed.Truncated = compressed.Truncated || truncated
				compressed.OutputBytes = len(output)
				details := compressionDetails(compressed, compression)
				details["command"] = command
				details["exitCode"] = exitCode
				if exitCode != 0 {
					return agentcore.ToolResult{
						Text:    fmt.Sprintf("%s\n\nCommand exited with code %d", strings.TrimRight(output, "\n"), exitCode),
						Details: details,
						IsError: true,
					}
				}
				return agentcore.ToolResult{Text: strings.TrimRight(output, "\n"), Details: details}
			},
		},
		{
			Name: "write",
			Execute: func(_ context.Context, call ai.ContentBlock) agentcore.ToolResult {
				path, _ := call.Arguments["path"].(string)
				content, _ := call.Arguments["content"].(string)
				details, err := WriteWorkspaceFileWithDetails(root, path, content)
				if err != nil {
					return agentcore.ToolResult{Text: err.Error(), IsError: true}
				}
				return agentcore.ToolResult{
					Text:    fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path),
					Details: details,
				}
			},
		},
		{
			Name: "read",
			Execute: func(_ context.Context, call ai.ContentBlock) agentcore.ToolResult {
				path, _ := call.Arguments["path"].(string)
				absolutePath, err := ResolveWorkspacePath(root, path)
				if err != nil {
					return agentcore.ToolResult{Text: err.Error(), IsError: true}
				}
				data, totalBytes, truncated, err := readWorkspaceFileBounded(absolutePath, outputLimit)
				if err != nil {
					return agentcore.ToolResult{Text: err.Error(), IsError: true}
				}
				if isLikelyBinary(data) {
					return agentcore.ToolResult{Text: fmt.Sprintf("%s appears to be a binary file and was not read.", path), IsError: true}
				}
				return agentcore.ToolResult{
					Text:    strings.TrimRight(string(data), "\n"),
					Details: map[string]any{"readFiles": []string{path}, "bytes": totalBytes, "truncated": truncated},
				}
			},
		},
		{
			Name: "edit",
			Execute: func(_ context.Context, call ai.ContentBlock) agentcore.ToolResult {
				path, _ := call.Arguments["path"].(string)
				edits, err := parseWorkspaceEdits(call.Arguments)
				if err != nil {
					return agentcore.ToolResult{Text: err.Error(), IsError: true}
				}
				details, err := EditWorkspaceFileWithDetails(root, path, edits)
				if err != nil {
					return agentcore.ToolResult{Text: err.Error(), IsError: true}
				}
				return agentcore.ToolResult{
					Text:    fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), path),
					Details: details,
				}
			},
		},
		{
			Name: "ls",
			Execute: func(_ context.Context, call ai.ContentBlock) agentcore.ToolResult {
				path, _ := call.Arguments["path"].(string)
				if path == "" {
					path = "."
				}
				absolutePath, err := ResolveWorkspacePath(root, path)
				if err != nil {
					return agentcore.ToolResult{Text: err.Error(), IsError: true}
				}
				entries, err := os.ReadDir(absolutePath)
				if err != nil {
					return agentcore.ToolResult{Text: err.Error(), IsError: true}
				}
				names := make([]string, 0, len(entries))
				for _, entry := range entries {
					name := entry.Name()
					if entry.IsDir() {
						name += "/"
					}
					names = append(names, name)
				}
				text, truncated := truncateToolOutput(strings.Join(names, "\n"), outputLimit)
				return agentcore.ToolResult{Text: text, Details: map[string]any{"path": path, "entries": len(names), "truncated": truncated}}
			},
		},
		{
			Name: "grep",
			Execute: func(_ context.Context, call ai.ContentBlock) agentcore.ToolResult {
				pattern, _ := call.Arguments["pattern"].(string)
				path, _ := call.Arguments["path"].(string)
				if path == "" {
					path = "."
				}
				result, err := GrepWorkspaceWithDetails(root, path, pattern)
				if err != nil {
					return agentcore.ToolResult{Text: err.Error(), IsError: true}
				}
				text := result.Text()
				text, truncated := truncateToolOutput(text, outputLimit)
				details := result.Metadata()
				details["truncated"] = truncated
				return agentcore.ToolResult{Text: text, Details: details}
			},
		},
		{
			Name: "find",
			Execute: func(_ context.Context, call ai.ContentBlock) agentcore.ToolResult {
				pattern, _ := call.Arguments["pattern"].(string)
				path, _ := call.Arguments["path"].(string)
				if path == "" {
					path = "."
				}
				text, err := FindWorkspace(root, path, pattern)
				if err != nil {
					return agentcore.ToolResult{Text: err.Error(), IsError: true}
				}
				text, truncated := truncateToolOutput(text, outputLimit)
				return agentcore.ToolResult{Text: text, Details: map[string]any{"path": path, "pattern": pattern, "truncated": truncated}}
			},
		},
	}
	return filterBuiltinTools(tools, func(tool agentcore.Tool) string { return tool.Name }, options.BuiltinToolPolicy)
}

func applyShellCommandPrefix(command, prefix string) string {
	command = strings.TrimSpace(command)
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || command == "" {
		return command
	}
	return prefix + " " + command
}

func readWorkspaceFileBounded(path string, limit int) ([]byte, int64, bool, error) {
	if limit <= 0 {
		limit = maxToolOutputBytes
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, false, err
	}
	totalBytes := info.Size()
	if totalBytes <= int64(limit) {
		data, err := io.ReadAll(file)
		return data, totalBytes, false, err
	}
	if limit < 200 {
		data := make([]byte, limit)
		n, err := io.ReadFull(file, data)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, 0, false, err
		}
		return data[:n], totalBytes, true, nil
	}
	head := limit / 2
	tail := limit - head - 96
	if tail < 0 {
		tail = 0
	}
	headBytes := make([]byte, head)
	headRead, err := io.ReadFull(file, headBytes)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, 0, false, err
	}
	tailBytes := make([]byte, tail)
	tailRead := 0
	if tail > 0 {
		n, err := file.ReadAt(tailBytes, totalBytes-int64(tail))
		if err != nil && err != io.EOF {
			return nil, 0, false, err
		}
		tailRead = n
	}
	omitted := totalBytes - int64(headRead) - int64(tailRead)
	preview := fmt.Sprintf("%s\n\n[... truncated %d bytes ...]\n\n%s", string(headBytes[:headRead]), omitted, string(tailBytes[:tailRead]))
	return []byte(preview), totalBytes, true, nil
}

func truncateToolOutput(text string, limit int) (string, bool) {
	if limit <= 0 || len(text) <= limit {
		return text, false
	}
	if limit < 200 {
		return text[:limit], true
	}
	head := limit / 2
	tail := limit - head - 96
	if tail < 0 {
		tail = 0
	}
	omitted := len(text) - head - tail
	return fmt.Sprintf("%s\n\n[... truncated %d bytes ...]\n\n%s", text[:head], omitted, text[len(text)-tail:]), true
}

func BuiltinToolSpecs() []ai.Tool {
	return BuiltinToolSpecsWithPolicy(BuiltinToolPolicy{})
}

func BuiltinToolSpecsWithPolicy(policy BuiltinToolPolicy) []ai.Tool {
	specs := []ai.Tool{
		{
			Name:        "bash",
			Description: "Execute a shell command",
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"command": map[string]any{"type": "string"}, "timeout": map[string]any{"type": "number"}},
				"required":             []string{"command"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "write",
			Description: "Write a complete file",
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}},
				"required":             []string{"path", "content"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "read",
			Description: "Read a file",
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"path": map[string]any{"type": "string"}},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "edit",
			Description: "Apply text edits to a file",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
					"edits": map[string]any{
						"type":     "array",
						"items":    map[string]any{"type": "object"},
						"required": []string{"oldText", "newText"},
					},
				},
				"required":             []string{"path", "edits"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "ls",
			Description: "List files in a directory",
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"path": map[string]any{"type": "string"}},
				"additionalProperties": false,
			},
		},
		{
			Name:        "grep",
			Description: "Search file contents",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{"type": "string"},
					"path":    map[string]any{"type": "string"},
				},
				"required":             []string{"pattern", "path"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "find",
			Description: "Find files using glob pattern",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{"type": "string"},
					"path":    map[string]any{"type": "string"},
				},
				"required":             []string{"pattern", "path"},
				"additionalProperties": false,
			},
		},
	}
	return filterBuiltinTools(specs, func(tool ai.Tool) string { return tool.Name }, policy)
}

func RunBashCommand(ctx context.Context, root, command string, timeoutSeconds float64) (string, int, error) {
	if command == "" {
		return "", 0, fmt.Errorf("bash command is empty")
	}
	if timeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds*float64(time.Second)))
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		text := string(output)
		if text != "" {
			text += "\n\n"
		}
		return "", 0, fmt.Errorf("%sCommand timed out after %g seconds", text, timeoutSeconds)
	}
	exitCode := 0
	if err != nil {
		exitCode = 1
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		}
	}
	return string(output), exitCode, nil
}

func WriteWorkspaceFile(root, name, content string) error {
	_, err := WriteWorkspaceFileWithDetails(root, name, content)
	return err
}

func WriteWorkspaceFileWithDetails(root, name, content string) (map[string]any, error) {
	path, err := ResolveWorkspacePath(root, name)
	if err != nil {
		return nil, err
	}
	before := ""
	beforeBytes := 0
	if data, err := os.ReadFile(path); err == nil {
		before = string(data)
		beforeBytes = len(data)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return map[string]any{
		"modifiedFiles": []string{name},
		"bytes":         len(content),
		"beforeBytes":   beforeBytes,
		"afterBytes":    len(content),
		"diff":          simpleUnifiedDiff(name, before, content),
	}, nil
}

type WorkspaceEdit struct {
	OldText string
	NewText string
}

const utf8BOM = "\ufeff"

func normalizeToLF(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

func detectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx == -1 {
		return "\n"
	}
	if crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

func restoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

func stripBOM(content string) (string, string) {
	if !strings.HasPrefix(content, utf8BOM) {
		return "", content
	}
	return utf8BOM, content[len(utf8BOM):]
}

func normalizeForFuzzyMatch(text string) string {
	normalized, _ := normalizeForFuzzyMatchWithMap(text)
	return normalized
}

func normalizeForFuzzyMatchWithMap(text string) (string, []int) {
	var builder strings.Builder
	normalizedToOriginal := []int{0}
	lineStart := 0
	for lineStart <= len(text) {
		lineEnd := strings.IndexByte(text[lineStart:], '\n')
		hasNewline := lineEnd != -1
		if hasNewline {
			lineEnd += lineStart
		} else {
			lineEnd = len(text)
		}

		appendNormalizedLine(&builder, &normalizedToOriginal, text, lineStart, lineEnd)
		if hasNewline {
			builder.WriteByte('\n')
			normalizedToOriginal = append(normalizedToOriginal, lineEnd+1)
			lineStart = lineEnd + 1
			continue
		}
		break
	}
	return builder.String(), normalizedToOriginal
}

func appendNormalizedLine(builder *strings.Builder, normalizedToOriginal *[]int, text string, start, end int) {
	trimEnd := end
	for trimEnd > start {
		r, size := runeBefore(text[start:trimEnd])
		if !unicode.IsSpace(r) {
			break
		}
		trimEnd -= size
	}

	for index := start; index < trimEnd; {
		r, size := rune(text[index]), 1
		if r >= utf8.RuneSelf {
			r, size = utf8.DecodeRuneInString(text[index:trimEnd])
		}
		normalizedRune := normalizeReplacementRunes(norm.NFKC.String(string(r)))
		builder.WriteString(normalizedRune)
		for i := 0; i < len(normalizedRune); i++ {
			*normalizedToOriginal = append(*normalizedToOriginal, index+size)
		}
		index += size
	}
}

func runeBefore(text string) (rune, int) {
	r, size := utf8.DecodeLastRuneInString(text)
	return r, size
}

func normalizeReplacementRunes(text string) string {
	normalized := norm.NFKC.String(text)
	normalized = strings.NewReplacer(
		"\u2018", "'",
		"\u2019", "'",
		"\u201A", "'",
		"\u201B", "'",
		"\u201C", "\"",
		"\u201D", "\"",
		"\u201E", "\"",
		"\u201F", "\"",
		"\u2010", "-",
		"\u2011", "-",
		"\u2012", "-",
		"\u2013", "-",
		"\u2014", "-",
		"\u2015", "-",
		"\u2212", "-",
		"\u00A0", " ",
		"\u2002", " ",
		"\u2003", " ",
		"\u2004", " ",
		"\u2005", " ",
		"\u2006", " ",
		"\u2007", " ",
		"\u2008", " ",
		"\u2009", " ",
		"\u200A", " ",
		"\u202F", " ",
		"\u205F", " ",
		"\u3000", " ",
	).Replace(normalized)
	return normalized
}

type fuzzyMatchResult struct {
	found                 bool
	index                 int
	matchLength           int
	usedFuzzyMatch        bool
	contentForReplacement string
}

type matchedEdit struct {
	index     int
	length    int
	newText   string
	editIndex int
}

func fuzzyFindText(content, oldText string) fuzzyMatchResult {
	exactIndex := strings.Index(content, oldText)
	if exactIndex != -1 {
		return fuzzyMatchResult{
			found:                 true,
			index:                 exactIndex,
			matchLength:           len(oldText),
			usedFuzzyMatch:        false,
			contentForReplacement: content,
		}
	}

	fuzzyContent := normalizeForFuzzyMatch(content)
	fuzzyOldText := normalizeForFuzzyMatch(oldText)
	fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText)
	if fuzzyIndex == -1 {
		return fuzzyMatchResult{
			found: false,
			index: -1,
		}
	}
	return fuzzyMatchResult{
		found:                 true,
		index:                 fuzzyIndex,
		matchLength:           len(fuzzyOldText),
		usedFuzzyMatch:        true,
		contentForReplacement: fuzzyContent,
	}
}

func editNotFoundError(path string, editIndex int, totalEdits int) string {
	if totalEdits == 1 {
		return fmt.Sprintf("Could not find the exact text in %s. The old text must match exactly including all whitespace and newlines.", path)
	}
	return fmt.Sprintf("Could not find edits[%d] in %s. The old text must match exactly including all whitespace and newlines.", editIndex, path)
}

func editDuplicateError(path string, editIndex int, totalEdits int, occurrences int) string {
	if totalEdits == 1 {
		return fmt.Sprintf(
			"Found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique.",
			occurrences,
			path,
		)
	}
	return fmt.Sprintf(
		"Found %d occurrences of edits[%d] in %s. Each oldText must be unique. Please provide more context to make it unique.",
		occurrences,
		editIndex,
		path,
	)
}

func editEmptyOldTextError(path string, editIndex int, totalEdits int) string {
	if totalEdits == 1 {
		return fmt.Sprintf("oldText must not be empty in %s.", path)
	}
	return fmt.Sprintf("edits[%d].oldText must not be empty in %s.", editIndex, path)
}

func editNoChangeError(path string, totalEdits int) string {
	if totalEdits == 1 {
		return fmt.Sprintf(
			"No changes made to %s. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.",
			path,
		)
	}
	return fmt.Sprintf("No changes made to %s. The replacements produced identical content.", path)
}

func applyWorkspaceEdits(content string, edits []WorkspaceEdit, path string) ([]matchedEdit, string, error) {
	if len(edits) == 0 {
		return nil, "", errors.New("Edit tool input is invalid. edits must contain at least one replacement.")
	}

	normalizedEdits := make([]WorkspaceEdit, 0, len(edits))
	for _, edit := range edits {
		normalizedEdits = append(normalizedEdits, WorkspaceEdit{
			OldText: normalizeToLF(edit.OldText),
			NewText: normalizeToLF(edit.NewText),
		})
	}

	contentForMatching := content
	oldTextForMatching := make([]string, 0, len(normalizedEdits))
	for _, edit := range normalizedEdits {
		oldTextForMatching = append(oldTextForMatching, edit.OldText)
	}
	usedFuzzyMatching := false
	var normalizedToOriginal []int
	for _, edit := range normalizedEdits {
		match := fuzzyFindText(contentForMatching, edit.OldText)
		if match.usedFuzzyMatch {
			usedFuzzyMatching = true
			contentForMatching, normalizedToOriginal = normalizeForFuzzyMatchWithMap(content)
			for i := range normalizedEdits {
				oldTextForMatching[i] = normalizeForFuzzyMatch(normalizedEdits[i].OldText)
			}
			break
		}
	}

	matched := make([]matchedEdit, 0, len(normalizedEdits))
	for i := range edits {
		oldText := oldTextForMatching[i]
		if oldText == "" {
			return nil, "", errors.New(editEmptyOldTextError(path, i, len(edits)))
		}

		count := strings.Count(contentForMatching, oldText)
		if count == 0 {
			return nil, "", errors.New(editNotFoundError(path, i, len(edits)))
		}
		if count > 1 {
			return nil, "", errors.New(editDuplicateError(path, i, len(edits), count))
		}

		index := strings.Index(contentForMatching, oldText)
		if index < 0 {
			return nil, "", errors.New(editNotFoundError(path, i, len(edits)))
		}
		length := len(oldText)
		if usedFuzzyMatching {
			end := index + length
			if index >= len(normalizedToOriginal) || end >= len(normalizedToOriginal) {
				return nil, "", errors.New(editNotFoundError(path, i, len(edits)))
			}
			originalStart := normalizedToOriginal[index]
			originalEnd := normalizedToOriginal[end]
			index = originalStart
			length = originalEnd - originalStart
		}
		matched = append(matched, matchedEdit{
			index:     index,
			length:    length,
			newText:   normalizeToLF(normalizedEdits[i].NewText),
			editIndex: i,
		})
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].index == matched[j].index {
			return matched[i].editIndex < matched[j].editIndex
		}
		return matched[i].index < matched[j].index
	})

	for i := 1; i < len(matched); i++ {
		previous := matched[i-1]
		current := matched[i]
		if previous.index+previous.length > current.index {
			return nil, "", fmt.Errorf("edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.", previous.editIndex, current.editIndex, path)
		}
	}

	updated := content
	for i := len(matched) - 1; i >= 0; i-- {
		e := matched[i]
		updated = updated[:e.index] + e.newText + updated[e.index+e.length:]
	}

	if updated == content {
		return nil, "", errors.New(editNoChangeError(path, len(edits)))
	}

	return matched, updated, nil
}

func EditWorkspaceFile(root, name string, edits []WorkspaceEdit) error {
	_, err := EditWorkspaceFileWithDetails(root, name, edits)
	return err
}

func EditWorkspaceFileWithDetails(root, name string, edits []WorkspaceEdit) (map[string]any, error) {
	path, err := ResolveWorkspacePath(root, name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	rawContent := string(data)
	bom, text := stripBOM(rawContent)
	lineEnding := detectLineEnding(text)
	baseContent := normalizeToLF(text)

	_, updated, err := applyWorkspaceEdits(baseContent, edits, name)
	if err != nil {
		return nil, err
	}

	finalContent := bom + restoreLineEndings(updated, lineEnding)
	if err := os.WriteFile(path, []byte(finalContent), 0o644); err != nil {
		return nil, err
	}
	return map[string]any{
		"modifiedFiles": []string{name},
		"editCount":     len(edits),
		"beforeBytes":   len(rawContent),
		"afterBytes":    len(finalContent),
		"diff":          simpleUnifiedDiff(name, rawContent, finalContent),
	}, nil
}

func simpleUnifiedDiff(name, before, after string) string {
	if before == after {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("--- ")
	builder.WriteString(name)
	builder.WriteString("\n+++ ")
	builder.WriteString(name)
	builder.WriteString("\n")
	beforeLines := strings.SplitAfter(before, "\n")
	afterLines := strings.SplitAfter(after, "\n")
	for _, line := range beforeLines {
		if line == "" {
			continue
		}
		builder.WriteByte('-')
		builder.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			builder.WriteByte('\n')
		}
	}
	for _, line := range afterLines {
		if line == "" {
			continue
		}
		builder.WriteByte('+')
		builder.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func parseWorkspaceEdits(toolArgs map[string]any) ([]WorkspaceEdit, error) {
	if toolArgs == nil {
		return nil, errors.New("edits must be an array")
	}

	var edits []WorkspaceEdit
	editsValue, hasEdits := toolArgs["edits"]
	if hasEdits {
		switch typed := editsValue.(type) {
		case string:
			if err := json.Unmarshal([]byte(typed), &edits); err != nil {
				return nil, errors.New("edits must be an array")
			}
		case []any:
			parsed, err := parseWorkspaceEditList(typed)
			if err != nil {
				return nil, err
			}
			edits = append(edits, parsed...)
		default:
			return nil, errors.New("edits must be an array")
		}
	}

	legacyOldText, legacyOldTextOk := toolArgs["oldText"].(string)
	legacyNewText, legacyNewTextOk := toolArgs["newText"].(string)
	if legacyOldTextOk && legacyNewTextOk {
		edits = append(edits, WorkspaceEdit{
			OldText: legacyOldText,
			NewText: legacyNewText,
		})
	}

	if len(edits) == 0 {
		return nil, errors.New("Edit tool input is invalid. edits must contain at least one replacement.")
	}

	return edits, nil
}

func parseWorkspaceEditList(rawEdits []any) ([]WorkspaceEdit, error) {
	if len(rawEdits) == 0 {
		return nil, errors.New("Edit tool input is invalid. edits must contain at least one replacement.")
	}

	edits := make([]WorkspaceEdit, 0, len(rawEdits))
	for index, item := range rawEdits {
		raw, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("edit %d must be an object", index)
		}
		oldText, oldTextOk := raw["oldText"].(string)
		newText, newTextOk := raw["newText"].(string)
		if !oldTextOk || !newTextOk {
			return nil, errors.New("edit input must contain string oldText and newText")
		}
		edits = append(edits, WorkspaceEdit{OldText: oldText, NewText: newText})
	}
	return edits, nil
}

func ResolveWorkspacePath(root, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("workspace path is empty")
	}
	clean := filepath.Clean(name)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("workspace path escapes root: %s", name)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(rootAbs, clean)
	if path != rootAbs && !strings.HasPrefix(path, rootAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("workspace path escapes root: %s", name)
	}
	return path, nil
}

type GrepMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type GrepWorkspaceResult struct {
	Root          string      `json:"root"`
	Path          string      `json:"path"`
	Pattern       string      `json:"pattern"`
	FilesSearched int         `json:"filesSearched"`
	FilesMatched  int         `json:"filesMatched"`
	Matches       []GrepMatch `json:"matches"`
}

func (r GrepWorkspaceResult) Text() string {
	lines := make([]string, 0, len(r.Matches))
	for _, match := range r.Matches {
		lines = append(lines, fmt.Sprintf("%s:%d: %s", match.Path, match.Line, match.Text))
	}
	return strings.Join(lines, "\n")
}

func (r GrepWorkspaceResult) Metadata() map[string]any {
	return map[string]any{
		"path":          r.Path,
		"pattern":       r.Pattern,
		"filesSearched": r.FilesSearched,
		"filesMatched":  r.FilesMatched,
		"matchCount":    len(r.Matches),
		"matches":       append([]GrepMatch(nil), r.Matches...),
	}
}

func GrepWorkspace(root, path, pattern string) (string, error) {
	result, err := GrepWorkspaceWithDetails(root, path, pattern)
	if err != nil {
		return "", err
	}
	return result.Text(), nil
}

func GrepWorkspaceWithDetails(root, path, pattern string) (GrepWorkspaceResult, error) {
	if pattern == "" {
		return GrepWorkspaceResult{}, fmt.Errorf("grep pattern is empty")
	}
	absolutePath, err := ResolveWorkspacePath(root, path)
	if err != nil {
		return GrepWorkspaceResult{}, err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return GrepWorkspaceResult{}, err
	}
	result := GrepWorkspaceResult{Root: rootAbs, Path: path, Pattern: pattern}
	matchedFiles := map[string]bool{}
	err = filepath.WalkDir(absolutePath, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if shouldSkipWorkspaceEntry(entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return nil
		}
		if isLikelyBinary(data) {
			return nil
		}
		result.FilesSearched++
		relative, err := filepath.Rel(rootAbs, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		for lineIndex, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, pattern) {
				matchedFiles[relative] = true
				result.Matches = append(result.Matches, GrepMatch{
					Path: relative,
					Line: lineIndex + 1,
					Text: line,
				})
			}
		}
		return nil
	})
	if err != nil {
		return GrepWorkspaceResult{}, err
	}
	result.FilesMatched = len(matchedFiles)
	return result, nil
}

func FindWorkspace(root, path, pattern string) (string, error) {
	absolutePath, err := ResolveWorkspacePath(root, path)
	if err != nil {
		return "", err
	}
	matches := []string{}
	err = filepath.WalkDir(absolutePath, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current != absolutePath && shouldSkipWorkspaceEntry(entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if current == absolutePath {
			return nil
		}
		name := entry.Name()
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if pattern == "" || matchesFindPattern(relative, name, pattern) {
			displayPath := relative
			if entry.IsDir() {
				displayPath += "/"
			}
			matches = append(matches, filepath.ToSlash(displayPath))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return strings.Join(matches, "\n"), nil
}

func shouldSkipWorkspaceEntry(entry os.DirEntry) bool {
	name := entry.Name()
	switch name {
	case ".git", "node_modules", ".next", "dist", "build", "vendor":
		return true
	default:
		return false
	}
}

func isLikelyBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	limit := len(data)
	if limit > 8000 {
		limit = 8000
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}

func matchesFindPattern(relativePath, name, pattern string) bool {
	if hasGlobMeta(pattern) {
		target := name
		if strings.Contains(pattern, "/") {
			target = filepath.ToSlash(relativePath)
		}
		matched, err := filepath.Match(filepath.ToSlash(pattern), target)
		return err == nil && matched
	}
	return strings.Contains(name, pattern) || strings.Contains(filepath.ToSlash(relativePath), pattern)
}

func hasGlobMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}
