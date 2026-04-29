package ai

import (
	"os"
	"strings"
)

func resolveCacheRetention(options ChatOptions) CacheRetention {
	if options.CacheRetention != "" {
		return options.CacheRetention
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("PI_CACHE_RETENTION")), "long") {
		return CacheRetentionLong
	}
	return CacheRetentionShort
}

func cacheSessionID(options ChatOptions) string {
	if resolveCacheRetention(options) == CacheRetentionNone {
		return ""
	}
	return strings.TrimSpace(options.SessionID)
}

func cacheRetentionValue(options ChatOptions) string {
	if resolveCacheRetention(options) == CacheRetentionLong {
		return "24h"
	}
	return ""
}
