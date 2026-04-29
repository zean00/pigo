package ai

import "regexp"

var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)prompt is too long`),
	regexp.MustCompile(`(?i)request_too_large`),
	regexp.MustCompile(`(?i)input is too long for requested model`),
	regexp.MustCompile(`(?i)exceeds the context window`),
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),
	regexp.MustCompile(`(?i)reduce the length of the messages`),
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),
	regexp.MustCompile(`(?i)exceeds the limit of \d+`),
	regexp.MustCompile(`(?i)exceeds the available context size`),
	regexp.MustCompile(`(?i)greater than the context length`),
	regexp.MustCompile(`(?i)context window exceeds limit`),
	regexp.MustCompile(`(?i)exceeded model token limit`),
	regexp.MustCompile(`(?i)too large for model with \d+ maximum context length`),
	regexp.MustCompile(`(?i)model_context_window_exceeded`),
	regexp.MustCompile(`(?i)prompt too long; exceeded (?:max )?context length`),
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),
	regexp.MustCompile(`(?i)too many tokens`),
	regexp.MustCompile(`(?i)token limit exceeded`),
	regexp.MustCompile(`(?i)^4(?:00|13)\s*(?:status code)?\s*\(no body\)`),
}

var nonOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(Throttling error|Service unavailable):`),
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)too many requests`),
}

func IsContextOverflow(result NormalizedResult, contextWindow int) bool {
	if result.StopReason == "error" && result.ErrorMessage != "" {
		for _, pattern := range nonOverflowPatterns {
			if pattern.MatchString(result.ErrorMessage) {
				return false
			}
		}
		for _, pattern := range overflowPatterns {
			if pattern.MatchString(result.ErrorMessage) {
				return true
			}
		}
	}

	if contextWindow > 0 && result.StopReason == "stop" && result.Usage != nil {
		inputTokens := result.Usage.Input + result.Usage.CacheRead
		return inputTokens > contextWindow
	}
	return false
}

func OverflowPatterns() []*regexp.Regexp {
	return append([]*regexp.Regexp(nil), overflowPatterns...)
}
