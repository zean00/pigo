package codingagent

import (
	"fmt"
	"os"
	"strings"
)

const (
	SessionPurposeCoding   = "coding"
	SessionPurposeGeneric  = "generic"
	SessionPurposeResearch = "research"
	SessionPurposeReadonly = "readonly"
)

var defaultContextFileNames = []string{"AGENTS.md", "CLAUDE.md"}

type SessionDomainConfig struct {
	Purpose               string   `json:"purpose,omitempty"`
	ContextFiles          []string `json:"contextFiles,omitempty"`
	IncludeGitContext     *bool    `json:"includeGitContext,omitempty"`
	IncludePackageContext *bool    `json:"includePackageContext,omitempty"`
	ExtraInstructions     string   `json:"extraInstructions,omitempty"`
}

func SessionDomainConfigFromEnv() SessionDomainConfig {
	config := SessionDomainConfig{
		Purpose:      os.Getenv("PIGO_SESSION_PURPOSE"),
		ContextFiles: splitDomainList(os.Getenv("PIGO_CONTEXT_FILES")),
	}
	if value, ok := boolFromEnv("PIGO_INCLUDE_GIT_CONTEXT"); ok {
		config.IncludeGitContext = &value
	}
	if value, ok := boolFromEnv("PIGO_INCLUDE_PACKAGE_CONTEXT"); ok {
		config.IncludePackageContext = &value
	}
	config = config.Normalized()
	if err := config.Validate(); err != nil {
		return DefaultSessionDomainConfig()
	}
	return config
}

func DefaultSessionDomainConfig() SessionDomainConfig {
	git := true
	packages := true
	return SessionDomainConfig{
		Purpose:               SessionPurposeCoding,
		ContextFiles:          append([]string(nil), defaultContextFileNames...),
		IncludeGitContext:     &git,
		IncludePackageContext: &packages,
	}
}

func SessionPurposeValues() []string {
	return []string{SessionPurposeCoding, SessionPurposeGeneric, SessionPurposeResearch, SessionPurposeReadonly}
}

func (c SessionDomainConfig) Normalized() SessionDomainConfig {
	defaults := DefaultSessionDomainConfig()
	c.Purpose = strings.ToLower(strings.TrimSpace(c.Purpose))
	if c.Purpose == "" {
		c.Purpose = defaults.Purpose
	}
	c.ContextFiles = normalizeContextFileNames(c.ContextFiles)
	if len(c.ContextFiles) == 0 {
		c.ContextFiles = append([]string(nil), defaults.ContextFiles...)
	}
	if c.IncludeGitContext == nil {
		c.IncludeGitContext = defaults.IncludeGitContext
	}
	if c.IncludePackageContext == nil {
		c.IncludePackageContext = defaults.IncludePackageContext
	}
	c.ExtraInstructions = strings.TrimSpace(c.ExtraInstructions)
	return c
}

func (c SessionDomainConfig) Validate() error {
	c = c.Normalized()
	switch c.Purpose {
	case SessionPurposeCoding, SessionPurposeGeneric, SessionPurposeResearch, SessionPurposeReadonly:
	default:
		return fmt.Errorf("unknown session purpose %q", c.Purpose)
	}
	for _, name := range c.ContextFiles {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if strings.ContainsAny(name, `/\`) {
			return fmt.Errorf("context file name must not include path separators: %q", name)
		}
	}
	return nil
}

func (c SessionDomainConfig) Metadata() map[string]any {
	c = c.Normalized()
	return map[string]any{
		"purpose":               c.Purpose,
		"availablePurposes":     SessionPurposeValues(),
		"contextFiles":          append([]string(nil), c.ContextFiles...),
		"includeGitContext":     boolValue(c.IncludeGitContext),
		"includePackageContext": boolValue(c.IncludePackageContext),
		"extraInstructions":     c.ExtraInstructions,
	}
}

func normalizeContextFileNames(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func splitDomainList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return normalizeContextFileNames(strings.Split(value, ","))
}

func boolFromEnv(name string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func boolValue(value *bool) bool {
	return value != nil && *value
}
