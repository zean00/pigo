package orchestrator

import (
	"os"
	"strings"
	"time"
)

const (
	StatePending   = "pending"
	StateRunning   = "running"
	StateCompleted = "completed"
	StateFailed    = "failed"
	StateCanceled  = "canceled"

	DefaultMaxParallel = 3
	DefaultTimeout     = 120 * time.Second
	DefaultReducer     = "markdown"
)

type Config struct {
	Enabled       bool          `json:"enabled,omitempty"`
	MaxParallel   int           `json:"maxParallel,omitempty"`
	Timeout       time.Duration `json:"-"`
	TimeoutMillis int           `json:"timeoutMillis,omitempty"`
	Agents        []string      `json:"agents,omitempty"`
	Reducer       string        `json:"reducer,omitempty"`
}

type Agent struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Skills      []string `json:"skills,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type TaskSpec struct {
	ID        string   `json:"id,omitempty"`
	Agent     string   `json:"agent,omitempty"`
	Skill     string   `json:"skill,omitempty"`
	Message   string   `json:"message"`
	DependsOn []string `json:"dependsOn,omitempty"`
	Optional  bool     `json:"optional,omitempty"`
}

type RunRequest struct {
	Goal          string     `json:"goal"`
	Agent         string     `json:"agent,omitempty"`
	Skill         string     `json:"skill,omitempty"`
	Agents        []string   `json:"agents,omitempty"`
	Steps         []TaskSpec `json:"steps,omitempty"`
	MaxParallel   int        `json:"maxParallel,omitempty"`
	TimeoutMillis int        `json:"timeoutMillis,omitempty"`
}

type Run struct {
	RunID     string     `json:"runId"`
	Goal      string     `json:"goal"`
	State     string     `json:"state"`
	CreatedAt string     `json:"createdAt"`
	UpdatedAt string     `json:"updatedAt"`
	Tasks     []Task     `json:"tasks,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	Errors    []string   `json:"errors,omitempty"`
	Result    string     `json:"result,omitempty"`
}

type Task struct {
	TaskID      string   `json:"taskId"`
	Agent       string   `json:"agent,omitempty"`
	Skill       string   `json:"skill,omitempty"`
	Message     string   `json:"message"`
	DependsOn   []string `json:"dependsOn,omitempty"`
	Optional    bool     `json:"optional,omitempty"`
	State       string   `json:"state"`
	A2ATaskID   string   `json:"a2aTaskId,omitempty"`
	ArtifactIDs []string `json:"artifactIds,omitempty"`
	Error       string   `json:"error,omitempty"`
}

type Artifact struct {
	ArtifactID string `json:"artifactId"`
	TaskID     string `json:"taskId"`
	Agent      string `json:"agent,omitempty"`
	Text       string `json:"text,omitempty"`
}

func ConfigFromEnv() Config {
	config := Config{}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PIGO_ORCHESTRATOR"))) {
	case "1", "true", "yes", "on":
		config.Enabled = true
	}
	if value := strings.TrimSpace(os.Getenv("PIGO_ORCHESTRATOR_MAX_PARALLEL")); value != "" {
		config.MaxParallel = atoiDefault(value, 0)
	}
	if value := strings.TrimSpace(os.Getenv("PIGO_ORCHESTRATOR_TIMEOUT_MS")); value != "" {
		config.TimeoutMillis = atoiDefault(value, 0)
	}
	if value := strings.TrimSpace(os.Getenv("PIGO_ORCHESTRATOR_AGENTS")); value != "" {
		config.Agents = strings.Split(value, ",")
	}
	config.Reducer = os.Getenv("PIGO_ORCHESTRATOR_REDUCER")
	return config.Normalized()
}

func (c Config) Normalized() Config {
	if c.MaxParallel <= 0 {
		c.MaxParallel = DefaultMaxParallel
	}
	if c.Timeout == 0 && c.TimeoutMillis > 0 {
		c.Timeout = time.Duration(c.TimeoutMillis) * time.Millisecond
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	c.TimeoutMillis = int(c.Timeout / time.Millisecond)
	c.Reducer = strings.ToLower(strings.TrimSpace(c.Reducer))
	if c.Reducer == "" {
		c.Reducer = DefaultReducer
	}
	c.Agents = normalizeList(c.Agents)
	return c
}

func (c Config) Metadata() map[string]any {
	c = c.Normalized()
	return map[string]any{
		"enabled":       c.Enabled,
		"maxParallel":   c.MaxParallel,
		"timeoutMillis": c.TimeoutMillis,
		"agents":        append([]string(nil), c.Agents...),
		"reducer":       c.Reducer,
	}
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

func atoiDefault(value string, fallback int) int {
	out := 0
	for _, r := range strings.TrimSpace(value) {
		if r < '0' || r > '9' {
			return fallback
		}
		out = out*10 + int(r-'0')
	}
	if out == 0 {
		return fallback
	}
	return out
}
