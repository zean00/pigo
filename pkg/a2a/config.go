package a2a

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Config struct {
	Enabled          bool          `json:"enabled,omitempty"`
	Agents           []RemoteAgent `json:"agents,omitempty"`
	Timeout          time.Duration `json:"-"`
	TimeoutMillis    int           `json:"timeoutMillis,omitempty"`
	MaxResponseBytes int           `json:"maxResponseBytes,omitempty"`
}

type RemoteAgent struct {
	Name          string            `json:"name"`
	CardURL       string            `json:"cardUrl,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	BearerToken   string            `json:"bearerToken,omitempty"`
	AllowInsecure bool              `json:"allowInsecure,omitempty"`
}

func ConfigFromEnv(root string) Config {
	config := Config{}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PIGO_A2A_TOOLS"))) {
	case "1", "true", "yes", "on":
		config.Enabled = true
	}
	if raw := strings.TrimSpace(os.Getenv("PIGO_A2A_CONFIG_JSON")); raw != "" {
		_ = json.Unmarshal([]byte(raw), &config)
	} else if path := strings.TrimSpace(os.Getenv("PIGO_A2A_CONFIG")); path != "" {
		_ = loadConfigFile(path, &config)
	} else {
		for _, path := range defaultConfigPaths(root) {
			if _, err := os.Stat(path); err == nil {
				_ = loadConfigFile(path, &config)
				break
			}
		}
	}
	config = config.Normalized()
	if err := config.Validate(); err != nil {
		return Config{}
	}
	return config
}

func defaultConfigPaths(root string) []string {
	paths := []string{}
	if strings.TrimSpace(root) != "" {
		paths = append(paths, filepath.Join(root, ".pi", "a2a.json"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".pi", "agent", "a2a.json"))
	}
	return paths
}

func loadConfigFile(path string, out *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (c Config) Normalized() Config {
	if c.Timeout == 0 && c.TimeoutMillis > 0 {
		c.Timeout = time.Duration(c.TimeoutMillis) * time.Millisecond
	}
	if c.Timeout == 0 {
		c.Timeout = 60 * time.Second
	}
	if c.TimeoutMillis == 0 {
		c.TimeoutMillis = int(c.Timeout / time.Millisecond)
	}
	if c.MaxResponseBytes <= 0 {
		c.MaxResponseBytes = 2 << 20
	}
	out := Config{
		Enabled:          c.Enabled,
		Timeout:          c.Timeout,
		TimeoutMillis:    c.TimeoutMillis,
		MaxResponseBytes: c.MaxResponseBytes,
	}
	for _, agent := range c.Agents {
		agent.Name = ToolSafeName(agent.Name)
		agent.CardURL = strings.TrimSpace(agent.CardURL)
		agent.URL = strings.TrimSpace(agent.URL)
		if agent.CardURL == "" && agent.URL != "" {
			agent.CardURL = cardURLFromEndpoint(agent.URL)
		}
		if agent.Headers == nil {
			agent.Headers = map[string]string{}
		}
		if agent.BearerToken != "" && agent.Headers["Authorization"] == "" {
			agent.Headers["Authorization"] = "Bearer " + agent.BearerToken
		}
		out.Agents = append(out.Agents, agent)
	}
	sort.Slice(out.Agents, func(i, j int) bool { return out.Agents[i].Name < out.Agents[j].Name })
	return out
}

func (c Config) Validate() error {
	seen := map[string]bool{}
	for _, agent := range c.Agents {
		if agent.Name == "" {
			return fmt.Errorf("a2a agent name cannot be empty")
		}
		if seen[agent.Name] {
			return fmt.Errorf("duplicate a2a agent %q", agent.Name)
		}
		seen[agent.Name] = true
		endpoint := agent.CardURL
		if endpoint == "" {
			endpoint = agent.URL
		}
		if endpoint == "" {
			return fmt.Errorf("a2a agent %q missing cardUrl or url", agent.Name)
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("a2a agent %q has invalid URL", agent.Name)
		}
		if parsed.Scheme != "https" && !agent.AllowInsecure && !isLocalHost(parsed.Hostname()) {
			return fmt.Errorf("a2a agent %q requires https unless allowInsecure is true for localhost/test endpoints", agent.Name)
		}
	}
	return nil
}

func (c Config) Metadata() map[string]any {
	c = c.Normalized()
	agents := make([]map[string]any, 0, len(c.Agents))
	for _, agent := range c.Agents {
		agents = append(agents, map[string]any{
			"name":          agent.Name,
			"cardUrl":       agent.CardURL,
			"url":           agent.URL,
			"allowInsecure": agent.AllowInsecure,
			"headers":       redactedHeaders(agent.Headers),
		})
	}
	return map[string]any{
		"enabled":          c.Enabled,
		"agents":           agents,
		"timeoutMillis":    c.TimeoutMillis,
		"maxResponseBytes": c.MaxResponseBytes,
	}
}

func ToolSafeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	re := regexp.MustCompile(`[^a-z0-9_]+`)
	name = re.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	return name
}

func isLocalHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasPrefix(host, "127.")
}

func cardURLFromEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(endpoint, "/") + "/.well-known/agent-card.json"
	}
	parsed.Path = "/.well-known/agent-card.json"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func redactedHeaders(headers map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range headers {
		if strings.EqualFold(key, "authorization") || strings.Contains(strings.ToLower(key), "token") || strings.Contains(strings.ToLower(key), "key") {
			if value != "" {
				out[key] = "<configured>"
			}
			continue
		}
		out[key] = value
	}
	return out
}
