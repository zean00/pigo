package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	BashPermissionAllowAll  = "allow-all"
	BashPermissionAllowList = "allow-list"
)

type BashPermissionPolicy struct {
	Mode  string   `json:"mode"`
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

type BashPermissionDecision struct {
	Allowed         bool
	Reason          string
	MatchedRule     string
	MatchedRuleType string
	Mode            string
	Error           string
}

func DefaultBashPermissionPolicy() BashPermissionPolicy {
	return BashPermissionPolicy{Mode: BashPermissionAllowAll}
}

func BashPermissionPolicyFromEnv() BashPermissionPolicy {
	policy := DefaultBashPermissionPolicy()
	if mode := strings.TrimSpace(os.Getenv("PIGO_BASH_PERMISSION_MODE")); mode != "" {
		policy.Mode = mode
	}
	policy.Allow = splitRuleList(os.Getenv("PIGO_BASH_ALLOW"))
	policy.Deny = splitRuleList(os.Getenv("PIGO_BASH_DENY"))
	return policy
}

func (p BashPermissionPolicy) Normalized() BashPermissionPolicy {
	mode := strings.TrimSpace(strings.ToLower(p.Mode))
	switch mode {
	case BashPermissionAllowList:
	default:
		mode = BashPermissionAllowAll
	}
	p.Mode = mode
	p.Allow = normalizeRuleList(p.Allow)
	p.Deny = normalizeRuleList(p.Deny)
	return p
}

func (p BashPermissionPolicy) Validate() error {
	mode := strings.TrimSpace(strings.ToLower(p.Mode))
	switch mode {
	case "", BashPermissionAllowAll, BashPermissionAllowList:
	default:
		return fmt.Errorf("invalid bash permission mode %q", p.Mode)
	}
	for _, rule := range append(append([]string{}, p.Allow...), p.Deny...) {
		if _, _, err := parseBashPermissionRule(rule); err != nil {
			return err
		}
	}
	return nil
}

func (p BashPermissionPolicy) Metadata() map[string]any {
	p = p.Normalized()
	return map[string]any{
		"mode":  p.Mode,
		"allow": append([]string(nil), p.Allow...),
		"deny":  append([]string(nil), p.Deny...),
	}
}

func EvaluateBashPermission(command string, policy BashPermissionPolicy) BashPermissionDecision {
	if err := policy.Validate(); err != nil {
		return BashPermissionDecision{
			Allowed: false,
			Reason:  "bash permission policy is invalid",
			Mode:    strings.TrimSpace(policy.Mode),
			Error:   err.Error(),
		}
	}
	policy = policy.Normalized()
	for _, rule := range policy.Deny {
		matched, ruleType, err := bashRuleMatches(command, rule)
		if err != nil {
			return BashPermissionDecision{
				Allowed:         false,
				Reason:          "bash permission policy is invalid",
				MatchedRule:     rule,
				MatchedRuleType: ruleType,
				Mode:            policy.Mode,
				Error:           err.Error(),
			}
		}
		if matched {
			return BashPermissionDecision{
				Allowed:         false,
				Reason:          "bash command denied by policy",
				MatchedRule:     rule,
				MatchedRuleType: ruleType,
				Mode:            policy.Mode,
			}
		}
	}
	if policy.Mode == BashPermissionAllowAll {
		return BashPermissionDecision{Allowed: true, Mode: policy.Mode}
	}
	for _, rule := range policy.Allow {
		matched, ruleType, err := bashRuleMatches(command, rule)
		if err != nil {
			return BashPermissionDecision{
				Allowed:         false,
				Reason:          "bash permission policy is invalid",
				MatchedRule:     rule,
				MatchedRuleType: ruleType,
				Mode:            policy.Mode,
				Error:           err.Error(),
			}
		}
		if matched {
			return BashPermissionDecision{
				Allowed:         true,
				MatchedRule:     rule,
				MatchedRuleType: ruleType,
				Mode:            policy.Mode,
			}
		}
	}
	return BashPermissionDecision{
		Allowed: false,
		Reason:  "bash command is not in allow list",
		Mode:    policy.Mode,
	}
}

func bashPermissionDetails(decision BashPermissionDecision) map[string]any {
	details := map[string]any{
		"permissionDenied": true,
		"permissionMode":   decision.Mode,
		"reason":           decision.Reason,
	}
	if decision.MatchedRule != "" {
		details["matchedRule"] = decision.MatchedRule
	}
	if decision.MatchedRuleType != "" {
		details["matchedRuleType"] = decision.MatchedRuleType
	}
	if decision.Error != "" {
		details["error"] = decision.Error
	}
	return details
}

func deniedBashResult(command string, decision BashPermissionDecision) BashResult {
	if decision.Reason == "" {
		decision.Reason = "bash command denied by policy"
	}
	return BashResult{
		Command:    command,
		Output:     decision.Reason,
		ExitCode:   126,
		Permission: bashPermissionDetails(decision),
	}
}

func parseBashPermissionRule(rule string) (string, string, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return "", "", fmt.Errorf("empty bash permission rule")
	}
	kind := "exact"
	value := rule
	if before, after, ok := strings.Cut(rule, ":"); ok {
		switch strings.TrimSpace(strings.ToLower(before)) {
		case "exact", "glob", "regex":
			kind = strings.TrimSpace(strings.ToLower(before))
			value = strings.TrimSpace(after)
		default:
			return "", "", fmt.Errorf("invalid bash permission rule type %q", before)
		}
	}
	if value == "" {
		return "", "", fmt.Errorf("empty bash permission %s rule", kind)
	}
	if kind == "regex" {
		if _, err := regexp.Compile(value); err != nil {
			return "", "", fmt.Errorf("invalid bash permission regex %q: %w", value, err)
		}
	}
	if kind == "glob" {
		if _, err := filepath.Match(value, ""); err != nil {
			return "", "", fmt.Errorf("invalid bash permission glob %q: %w", value, err)
		}
	}
	return kind, value, nil
}

func bashRuleMatches(command, rule string) (bool, string, error) {
	kind, value, err := parseBashPermissionRule(rule)
	if err != nil {
		return false, kind, err
	}
	switch kind {
	case "glob":
		matched, err := filepath.Match(value, command)
		if err != nil {
			return false, kind, fmt.Errorf("invalid bash permission glob %q: %w", value, err)
		}
		return matched, kind, nil
	case "regex":
		matched, err := regexp.MatchString(value, command)
		return matched, kind, err
	default:
		return command == value, kind, nil
	}
}

func splitRuleList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return normalizeRuleList(strings.Split(value, ","))
}

func normalizeRuleList(values []string) []string {
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
