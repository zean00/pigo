package ai

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	defaultProviderMaxRetries    = 2
	defaultProviderBaseDelay     = time.Second
	defaultProviderMaxRetryDelay = 60 * time.Second
)

func retryLimit(options ChatOptions, providerDefault int) int {
	if options.MaxRetries < 0 {
		return 0
	}
	if options.MaxRetries > 0 {
		return options.MaxRetries
	}
	if providerDefault < 0 {
		return 0
	}
	return providerDefault
}

func shouldRetryHTTPStatus(status int, errorText string) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	text := strings.ToLower(errorText)
	return strings.Contains(text, "rate limit") ||
		strings.Contains(text, "resource exhausted") ||
		strings.Contains(text, "service unavailable") ||
		strings.Contains(text, "temporarily unavailable") ||
		strings.Contains(text, "overloaded")
}

func shouldRetryHTTPError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "timeout") ||
		strings.Contains(text, "temporarily unavailable") ||
		strings.Contains(text, "connection reset") ||
		strings.Contains(text, "broken pipe") ||
		strings.Contains(text, "unexpected eof") ||
		strings.Contains(text, "eof")
}

func isRetriableStreamError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "empty response") ||
		strings.Contains(text, "unexpected eof") ||
		strings.Contains(text, "eof")
}

func retryAfterDelay(resp *http.Response, errorText string) time.Duration {
	if resp != nil {
		if retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After")); retryAfter != "" {
			if seconds, parseErr := time.ParseDuration(retryAfter + "s"); parseErr == nil && seconds > 0 {
				return seconds + time.Second
			}
			if parsedTime, parseErr := http.ParseTime(retryAfter); parseErr == nil {
				if delay := time.Until(parsedTime) + time.Second; delay > 0 {
					return delay
				}
			}
		}
		if resetAfter := strings.TrimSpace(resp.Header.Get("x-ratelimit-reset-after")); resetAfter != "" {
			if seconds, parseErr := time.ParseDuration(resetAfter + "s"); parseErr == nil && seconds > 0 {
				return seconds + time.Second
			}
		}
	}
	if match := retryDelayFromText(errorText); match > 0 {
		return match
	}
	return 0
}

func retryDelayForAttempt(attempt int, serverDelay, baseDelay time.Duration) time.Duration {
	if serverDelay > 0 {
		return serverDelay
	}
	if attempt < 0 {
		attempt = 0
	}
	if baseDelay <= 0 {
		baseDelay = defaultProviderBaseDelay
	}
	return time.Duration(attempt+1) * baseDelay
}

func validateRetryDelay(options ChatOptions, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if options.MaxRetryDelay < 0 {
		return nil
	}
	maxDelay := options.MaxRetryDelay
	if maxDelay == 0 {
		maxDelay = defaultProviderMaxRetryDelay
	}
	if delay > maxDelay {
		return fmt.Errorf("provider requested retry delay %s exceeds configured maximum %s", delay, maxDelay)
	}
	return nil
}
