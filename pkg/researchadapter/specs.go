package researchadapter

import "github.com/badlogic/pigo/pkg/ai"

func searchSpec() ai.Tool {
	return ai.Tool{
		Name:        "search",
		Description: "Search the web through a configured SearXNG instance and return URLs, titles, and snippets.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":      map[string]any{"type": "string"},
				"queries":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"maxResults": map[string]any{"type": "number", "minimum": 1, "maximum": maxSearchResults},
				"freshness":  map[string]any{"type": "string", "enum": []string{"any", "day", "week", "month", "year"}},
				"sourceType": map[string]any{"type": "string", "enum": []string{"general", "news", "github"}},
			},
			"additionalProperties": false,
		},
	}
}

func scrapeSpec() ai.Tool {
	return ai.Tool{
		Name:        "scrape",
		Description: "Fetch and extract readable text from one or more HTTP(S) URLs.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":  map[string]any{"type": "string"},
				"urls": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": maxScrapeURLs},
			},
			"additionalProperties": false,
		},
	}
}

func securitySpec() ai.Tool {
	return ai.Tool{
		Name:        "security_search",
		Description: "Search public vulnerability sources such as OSV, NVD, and CISA KEV for CVEs, packages, or security topics.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":     map[string]any{"type": "string"},
				"ecosystem": map[string]any{"type": "string"},
				"package":   map[string]any{"type": "string"},
				"limit":     map[string]any{"type": "number", "minimum": 1, "maximum": 25},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
	}
}
