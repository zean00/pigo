package researchadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestLiveResearchSearch(t *testing.T) {
	if os.Getenv("PIGO_LIVE_RESEARCH_TESTS") != "1" {
		t.Skip("set PIGO_LIVE_RESEARCH_TESTS=1 to run live research smoke tests")
	}
	config := ConfigFromEnv().Normalized()
	if config.SearXNGURL == "" {
		t.Skip("set PIGO_SEARXNG_URL or SEARXNG_URL to run live search smoke test")
	}
	tools, _ := Tools(Config{Tools: []string{"search"}, SearXNGURL: config.SearXNGURL})
	result := tools[0].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{
		"query":      "pigo golang coding agent",
		"maxResults": 3,
	}})
	if result.IsError {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(result.Text, "http") {
		t.Fatalf("result text = %q", result.Text)
	}
}

func TestLiveResearchSecuritySearch(t *testing.T) {
	if os.Getenv("PIGO_LIVE_RESEARCH_TESTS") != "1" {
		t.Skip("set PIGO_LIVE_RESEARCH_TESTS=1 to run live research smoke tests")
	}
	config := ConfigFromEnv().Normalized()
	tools, _ := Tools(Config{Tools: []string{"security_search"}, NVDAPIKey: config.NVDAPIKey})
	result := tools[0].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{
		"query": "CVE-2021-44228",
		"limit": 3,
	}})
	if result.IsError {
		t.Fatalf("result = %#v", result)
	}
	for _, want := range []string{"CVE-2021-44228", "NVD"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("result missing %q: %q", want, result.Text)
		}
	}
}

func TestLiveResearchObscuraScrape(t *testing.T) {
	if os.Getenv("PIGO_LIVE_RESEARCH_TESTS") != "1" {
		t.Skip("set PIGO_LIVE_RESEARCH_TESTS=1 to run live research smoke tests")
	}
	config := ConfigFromEnv().Normalized()
	if config.ObscuraURL == "" {
		t.Skip("set PIGO_OBSCURA_URL or OBSCURA_URL to run live Obscura scrape smoke test")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Obscura Smoke</title></head><body><script>document.body.append(" Rendered text")</script></body></html>`))
	}))
	defer server.Close()
	tools, _ := Tools(Config{Tools: []string{"scrape"}, ObscuraURL: config.ObscuraURL})
	result := tools[0].Execute(context.Background(), ai.ContentBlock{Arguments: map[string]any{
		"url":    server.URL,
		"render": true,
	}})
	if result.IsError || !strings.Contains(result.Text, "Rendered text") {
		t.Fatalf("result = %#v", result)
	}
}
