package codingagent

import (
	"fmt"
	"os"
	"strings"
)

var builtinToolNames = []string{"bash", "write", "read", "edit", "ls", "grep", "find"}

type BuiltinToolPolicy struct {
	Enabled  []string `json:"enabled,omitempty"`
	Disabled []string `json:"disabled,omitempty"`
}

func BuiltinToolNames() []string {
	return append([]string(nil), builtinToolNames...)
}

func BuiltinToolPolicyFromEnv() BuiltinToolPolicy {
	return BuiltinToolPolicy{
		Enabled:  splitToolList(os.Getenv("PIGO_BUILTIN_TOOLS")),
		Disabled: splitToolList(os.Getenv("PIGO_DISABLED_BUILTIN_TOOLS")),
	}
}

func (p BuiltinToolPolicy) Normalized() BuiltinToolPolicy {
	p.Enabled = normalizeToolList(p.Enabled)
	p.Disabled = normalizeToolList(p.Disabled)
	return p
}

func (p BuiltinToolPolicy) Validate() error {
	known := builtinToolNameSet()
	for _, name := range append(append([]string{}, p.Enabled...), p.Disabled...) {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if normalized == "" {
			continue
		}
		if !known[normalized] {
			return fmt.Errorf("unknown built-in tool %q", name)
		}
	}
	return nil
}

func (p BuiltinToolPolicy) Metadata() map[string]any {
	p = p.Normalized()
	return map[string]any{
		"available": BuiltinToolNames(),
		"enabled":   append([]string(nil), p.Enabled...),
		"disabled":  append([]string(nil), p.Disabled...),
	}
}

func (p BuiltinToolPolicy) ToolEnabled(name string) bool {
	p = p.Normalized()
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return false
	}
	disabled := stringSet(p.Disabled)
	if disabled[normalized] {
		return false
	}
	if len(p.Enabled) == 0 {
		return true
	}
	enabled := stringSet(p.Enabled)
	return enabled[normalized]
}

func filterBuiltinTools[T any](items []T, name func(T) string, policy BuiltinToolPolicy) []T {
	if len(items) == 0 {
		return items
	}
	policy = policy.Normalized()
	filtered := make([]T, 0, len(items))
	for _, item := range items {
		if policy.ToolEnabled(name(item)) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func builtinToolNameSet() map[string]bool {
	set := make(map[string]bool, len(builtinToolNames))
	for _, name := range builtinToolNames {
		set[name] = true
	}
	return set
}

func splitToolList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return normalizeToolList(strings.Split(value, ","))
}

func normalizeToolList(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
