package researchadapter

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
)

var toolNames = []string{"research", "search", "scrape", "security_search"}

type Config struct {
	Tools      []string              `json:"tools,omitempty"`
	SearXNGURL string                `json:"searxngUrl,omitempty"`
	ObscuraURL string                `json:"obscuraUrl,omitempty"`
	NVDAPIKey  string                `json:"-"`
	HTTPClient *http.Client          `json:"-"`
	Host       Host                  `json:"-"`
	EventSink  func(agentcore.Event) `json:"-"`
	Now        func() time.Time      `json:"-"`
}

type Host interface {
	Root() string
	Model() (provider string, model string)
	APIKey(ctx context.Context, provider string) string
	WorkspaceTools() ([]agentcore.Tool, []ai.Tool)
}

func ToolNames() []string {
	return append([]string(nil), toolNames...)
}

func ConfigFromEnv() Config {
	return Config{
		Tools:      splitList(os.Getenv("PIGO_RESEARCH_TOOLS")),
		SearXNGURL: firstNonEmpty(os.Getenv("PIGO_SEARXNG_URL"), os.Getenv("SEARXNG_URL")),
		ObscuraURL: firstNonEmpty(os.Getenv("PIGO_OBSCURA_URL"), os.Getenv("OBSCURA_URL")),
		NVDAPIKey:  firstNonEmpty(os.Getenv("PIGO_NVD_API_KEY"), os.Getenv("NVD_API_KEY")),
	}
}

func (c Config) Normalized() Config {
	c.Tools = normalizeList(c.Tools)
	c.SearXNGURL = strings.TrimRight(strings.TrimSpace(c.SearXNGURL), "/")
	c.ObscuraURL = strings.TrimRight(strings.TrimSpace(c.ObscuraURL), "/")
	c.NVDAPIKey = strings.TrimSpace(c.NVDAPIKey)
	return c
}

func (c Config) Validate() error {
	known := map[string]bool{}
	for _, name := range toolNames {
		known[name] = true
	}
	for _, name := range c.Tools {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if normalized == "" {
			continue
		}
		if !known[normalized] {
			return fmt.Errorf("unknown research tool %q", name)
		}
	}
	return nil
}

func (c Config) Metadata() map[string]any {
	c = c.Normalized()
	return map[string]any{
		"available":  ToolNames(),
		"tools":      append([]string(nil), c.Tools...),
		"searxngUrl": c.SearXNGURL,
		"obscuraUrl": c.ObscuraURL,
		"sources":    []string{"osv", "nvd", "cisa-kev"},
		"nvdApiKey":  c.NVDAPIKey != "",
	}
}

func (c Config) ToolEnabled(name string) bool {
	tools := stringSet(normalizeList(c.Tools))
	return tools[strings.ToLower(strings.TrimSpace(name))]
}

func (c Config) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return normalizeList(strings.Split(value, ","))
}

func normalizeList(values []string) []string {
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

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
