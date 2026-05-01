package codingagent

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	CommandCompressionOff   = "off"
	CommandCompressionAuto  = "auto"
	CommandCompressionForce = "force"
)

type CommandOutputCompressionConfig struct {
	Mode            string   `json:"mode"`
	EnabledFilters  []string `json:"enabledFilters,omitempty"`
	DisabledFilters []string `json:"disabledFilters,omitempty"`
	MaxBytes        int      `json:"maxBytes,omitempty"`
}

type CommandOutputInput struct {
	Command  string
	Output   string
	ExitCode int
	MaxBytes int
}

type CommandOutputResult struct {
	Output        string
	Filter        string
	Compressed    bool
	Truncated     bool
	OriginalBytes int
	OutputBytes   int
	Error         string
}

type CommandOutputFilter interface {
	ID() string
	Match(command string) bool
	Compress(input CommandOutputInput) CommandOutputResult
}

type CommandOutputFilterRegistry struct {
	filters []CommandOutputFilter
}

func NewCommandOutputFilterRegistry(filters ...CommandOutputFilter) CommandOutputFilterRegistry {
	return CommandOutputFilterRegistry{filters: append([]CommandOutputFilter(nil), filters...)}
}

func DefaultCommandOutputFilters() CommandOutputFilterRegistry {
	return NewCommandOutputFilterRegistry(
		gitStatusOutputFilter{},
		gitDiffOutputFilter{},
		goTestOutputFilter{},
		grepOutputFilter{},
		listOutputFilter{},
		genericOutputFilter{},
	)
}

func (r CommandOutputFilterRegistry) IDs() []string {
	ids := make([]string, 0, len(r.filters))
	for _, filter := range r.filters {
		ids = append(ids, filter.ID())
	}
	return ids
}

func DefaultCommandOutputCompressionConfig() CommandOutputCompressionConfig {
	return CommandOutputCompressionConfig{
		Mode:     CommandCompressionAuto,
		MaxBytes: maxToolOutputBytes,
	}
}

func CommandOutputCompressionConfigFromEnv() CommandOutputCompressionConfig {
	config := DefaultCommandOutputCompressionConfig()
	if mode := strings.TrimSpace(os.Getenv("PIGO_COMMAND_COMPRESSION")); mode != "" {
		config.Mode = mode
	}
	config.EnabledFilters = splitFilterList(os.Getenv("PIGO_COMMAND_COMPRESSION_ENABLE"))
	config.DisabledFilters = splitFilterList(os.Getenv("PIGO_COMMAND_COMPRESSION_DISABLE"))
	if value := strings.TrimSpace(os.Getenv("PIGO_COMMAND_COMPRESSION_MAX_BYTES")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			config.MaxBytes = parsed
		}
	}
	return config.Normalized()
}

func (c CommandOutputCompressionConfig) Normalized() CommandOutputCompressionConfig {
	mode := strings.TrimSpace(strings.ToLower(c.Mode))
	switch mode {
	case CommandCompressionOff, CommandCompressionForce:
	default:
		mode = CommandCompressionAuto
	}
	c.Mode = mode
	c.EnabledFilters = normalizeFilterList(c.EnabledFilters)
	c.DisabledFilters = normalizeFilterList(c.DisabledFilters)
	if c.MaxBytes <= 0 {
		c.MaxBytes = maxToolOutputBytes
	}
	return c
}

func (c CommandOutputCompressionConfig) Validate() error {
	switch strings.TrimSpace(strings.ToLower(c.Mode)) {
	case CommandCompressionOff, CommandCompressionAuto, CommandCompressionForce:
		return nil
	default:
		return fmt.Errorf("invalid command compression mode %q", c.Mode)
	}
}

func (c CommandOutputCompressionConfig) Metadata() map[string]any {
	c = c.Normalized()
	return map[string]any{
		"mode":             c.Mode,
		"enabledFilters":   append([]string(nil), c.EnabledFilters...),
		"disabledFilters":  append([]string(nil), c.DisabledFilters...),
		"maxBytes":         c.MaxBytes,
		"availableFilters": DefaultCommandOutputFilters().IDs(),
	}
}

func (c CommandOutputCompressionConfig) filterEnabled(id string) bool {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return false
	}
	disabled := stringSet(c.DisabledFilters)
	if disabled[id] {
		return false
	}
	enabled := stringSet(c.EnabledFilters)
	return len(enabled) == 0 || enabled[id]
}

func CompressCommandOutput(command, output string, exitCode int, config CommandOutputCompressionConfig) CommandOutputResult {
	return DefaultCommandOutputFilters().Compress(command, output, exitCode, config)
}

func (r CommandOutputFilterRegistry) Compress(command, output string, exitCode int, config CommandOutputCompressionConfig) CommandOutputResult {
	config = config.Normalized()
	result := CommandOutputResult{
		Output:        output,
		OriginalBytes: len(output),
		OutputBytes:   len(output),
	}
	if config.Mode == CommandCompressionOff {
		result.Output, result.Truncated = truncateToolOutput(output, config.MaxBytes)
		result.OutputBytes = len(result.Output)
		return result
	}
	for _, filter := range r.filters {
		if !config.filterEnabled(filter.ID()) || !filter.Match(command) {
			continue
		}
		if config.Mode == CommandCompressionAuto && len(output) <= config.MaxBytes && filter.ID() == "generic" {
			break
		}
		return normalizeCompressionResult(filter.ID(), filter.Compress(CommandOutputInput{
			Command:  command,
			Output:   output,
			ExitCode: exitCode,
			MaxBytes: config.MaxBytes,
		}), output, config.MaxBytes)
	}
	result.Output, result.Truncated = truncateToolOutput(output, config.MaxBytes)
	result.OutputBytes = len(result.Output)
	result.Compressed = result.Truncated
	if result.Truncated {
		result.Filter = "truncate"
	}
	return result
}

func normalizeCompressionResult(filterID string, result CommandOutputResult, original string, maxBytes int) CommandOutputResult {
	if result.Output == "" && original != "" {
		result.Output = original
	}
	if result.Filter == "" {
		result.Filter = filterID
	}
	result.OriginalBytes = len(original)
	if len(result.Output) > maxBytes {
		var truncated bool
		result.Output, truncated = truncateToolOutput(result.Output, maxBytes)
		result.Truncated = result.Truncated || truncated
	}
	result.OutputBytes = len(result.Output)
	result.Compressed = result.Compressed || result.OutputBytes < result.OriginalBytes || result.Truncated
	return result
}

func compressionDetails(result CommandOutputResult, config CommandOutputCompressionConfig) map[string]any {
	config = config.Normalized()
	return map[string]any{
		"compressed":        result.Compressed,
		"compressionMode":   config.Mode,
		"compressionFilter": result.Filter,
		"originalBytes":     result.OriginalBytes,
		"compressedBytes":   result.OutputBytes,
		"truncated":         result.Truncated,
		"availableFilters":  DefaultCommandOutputFilters().IDs(),
	}
}

func splitFilterList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return normalizeFilterList(strings.Split(value, ","))
}

func normalizeFilterList(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value != "" {
			out[value] = true
		}
	}
	return out
}

type genericOutputFilter struct{}

func (genericOutputFilter) ID() string        { return "generic" }
func (genericOutputFilter) Match(string) bool { return true }
func (genericOutputFilter) Compress(input CommandOutputInput) CommandOutputResult {
	output, truncated := truncateToolOutput(input.Output, input.MaxBytes)
	return CommandOutputResult{Output: output, Filter: "generic", Truncated: truncated}
}

type gitStatusOutputFilter struct{}

func (gitStatusOutputFilter) ID() string { return "git-status" }
func (gitStatusOutputFilter) Match(command string) bool {
	command = normalizedCommand(command)
	return strings.HasPrefix(command, "git status") || strings.Contains(command, " git status")
}
func (gitStatusOutputFilter) Compress(input CommandOutputInput) CommandOutputResult {
	lines := nonEmptyLines(input.Output)
	if len(lines) == 0 {
		return CommandOutputResult{Output: input.Output, Filter: "git-status"}
	}
	text := strings.Join(lines, "\n")
	if len(lines) > 80 {
		text = strings.Join(lines[:80], "\n") + fmt.Sprintf("\n... %d more status lines omitted ...", len(lines)-80)
	}
	return CommandOutputResult{Output: text, Filter: "git-status", Compressed: len(text) < len(input.Output), Truncated: len(lines) > 80}
}

type gitDiffOutputFilter struct{}

func (gitDiffOutputFilter) ID() string { return "git-diff" }
func (gitDiffOutputFilter) Match(command string) bool {
	command = normalizedCommand(command)
	return strings.HasPrefix(command, "git diff") || strings.Contains(command, " git diff")
}
func (gitDiffOutputFilter) Compress(input CommandOutputInput) CommandOutputResult {
	lines := strings.Split(input.Output, "\n")
	selected := make([]string, 0, len(lines))
	omitted := 0
	inHunk := false
	hunkLines := 0
	for _, line := range lines {
		keep := strings.HasPrefix(line, "diff --git ") ||
			strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "--- ") ||
			strings.HasPrefix(line, "+++ ") ||
			strings.HasPrefix(line, "@@ ")
		if strings.HasPrefix(line, "@@ ") {
			inHunk = true
			hunkLines = 0
		} else if strings.HasPrefix(line, "diff --git ") {
			inHunk = false
		} else if inHunk && hunkLines < 12 && (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ")) {
			keep = true
			hunkLines++
		}
		if keep {
			selected = append(selected, line)
		} else if line != "" {
			omitted++
		}
	}
	if omitted > 0 {
		selected = append(selected, fmt.Sprintf("... %d diff lines omitted ...", omitted))
	}
	text := strings.Join(selected, "\n")
	return CommandOutputResult{Output: text, Filter: "git-diff", Compressed: omitted > 0, Truncated: omitted > 0}
}

type goTestOutputFilter struct{}

func (goTestOutputFilter) ID() string { return "go-test" }
func (goTestOutputFilter) Match(command string) bool {
	command = normalizedCommand(command)
	return strings.HasPrefix(command, "go test") || strings.Contains(command, " go test")
}
func (goTestOutputFilter) Compress(input CommandOutputInput) CommandOutputResult {
	lines := strings.Split(input.Output, "\n")
	selected := []string{}
	omitted := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		keep := strings.HasPrefix(trimmed, "--- FAIL:") ||
			strings.HasPrefix(trimmed, "FAIL") ||
			strings.HasPrefix(trimmed, "ok ") ||
			strings.Contains(trimmed, ".go:") ||
			strings.Contains(strings.ToLower(trimmed), "panic:") ||
			strings.Contains(strings.ToLower(trimmed), "error")
		if keep {
			selected = append(selected, line)
			if strings.HasPrefix(trimmed, "--- FAIL:") {
				for j := i + 1; j < len(lines) && j <= i+6; j++ {
					if strings.TrimSpace(lines[j]) != "" {
						selected = append(selected, lines[j])
					}
				}
			}
		} else if trimmed != "" {
			omitted++
		}
	}
	if len(selected) == 0 {
		return CommandOutputResult{Output: input.Output, Filter: "go-test"}
	}
	if omitted > 0 {
		selected = append(selected, fmt.Sprintf("... %d go test lines omitted ...", omitted))
	}
	text := dedupeAdjacentLines(strings.Join(selected, "\n"))
	return CommandOutputResult{Output: text, Filter: "go-test", Compressed: omitted > 0, Truncated: omitted > 0}
}

type grepOutputFilter struct{}

func (grepOutputFilter) ID() string { return "grep" }
func (grepOutputFilter) Match(command string) bool {
	command = normalizedCommand(command)
	return strings.HasPrefix(command, "rg ") || strings.HasPrefix(command, "grep ") ||
		strings.Contains(command, " rg ") || strings.Contains(command, " grep ")
}
func (grepOutputFilter) Compress(input CommandOutputInput) CommandOutputResult {
	lines := nonEmptyLines(input.Output)
	if len(lines) <= 120 {
		return CommandOutputResult{Output: input.Output, Filter: "grep"}
	}
	selected := append([]string(nil), lines[:120]...)
	selected = append(selected, fmt.Sprintf("... %d search result lines omitted ...", len(lines)-120))
	return CommandOutputResult{Output: strings.Join(selected, "\n"), Filter: "grep", Compressed: true, Truncated: true}
}

type listOutputFilter struct{}

func (listOutputFilter) ID() string { return "list" }
func (listOutputFilter) Match(command string) bool {
	command = normalizedCommand(command)
	return strings.HasPrefix(command, "ls ") || command == "ls" ||
		strings.HasPrefix(command, "find ") || strings.Contains(command, " find ")
}
func (listOutputFilter) Compress(input CommandOutputInput) CommandOutputResult {
	lines := nonEmptyLines(input.Output)
	if len(lines) <= 120 {
		return CommandOutputResult{Output: input.Output, Filter: "list"}
	}
	head := append([]string(nil), lines[:80]...)
	tail := lines[len(lines)-20:]
	out := append(head, fmt.Sprintf("... %d entries omitted ...", len(lines)-100))
	out = append(out, tail...)
	return CommandOutputResult{Output: strings.Join(out, "\n"), Filter: "list", Compressed: true, Truncated: true}
}

func normalizedCommand(command string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(command)), " ")
}

func nonEmptyLines(text string) []string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func dedupeAdjacentLines(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	previous := ""
	for _, line := range lines {
		if line == previous {
			continue
		}
		out = append(out, line)
		previous = line
	}
	return strings.Join(out, "\n")
}
