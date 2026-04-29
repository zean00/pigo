package ai

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ProviderSpec struct {
	Name          string
	BaseURL       string
	EnvKeys       []string
	DefaultModel  string
	DefaultHeader map[string]string
	Mode          string
}

type ModelInfo struct {
	Provider string
	ModelID  string
}

const (
	providerModeOpenAI       = "openai"
	providerModeBedrock      = "bedrock"
	providerModeAnthropic    = "anthropic"
	providerModeGoogle       = "google"
	providerModeGoogleCLI    = "google-cli"
	providerModeGoogleVertex = "google-vertex"
	providerModeMistral      = "mistral"
	providerModeCodex        = "codex"
	providerModeUnsupported  = "unsupported"
)

var (
	providerSpecs = map[string]ProviderSpec{
		"openai": {
			Name:         "openai",
			BaseURL:      "https://api.openai.com/v1",
			EnvKeys:      []string{"OPENAI_API_KEY"},
			DefaultModel: "gpt-5.4",
			Mode:         providerModeOpenAI,
		},
		"amazon-bedrock": {
			Name:         "amazon-bedrock",
			BaseURL:      "https://bedrock-runtime.us-east-1.amazonaws.com",
			EnvKeys:      []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_PROFILE", "AWS_BEARER_TOKEN_BEDROCK", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE"},
			DefaultModel: "us.anthropic.claude-opus-4-6-v1",
			Mode:         providerModeBedrock,
		},
		"anthropic": {
			Name:         "anthropic",
			BaseURL:      "https://api.anthropic.com",
			EnvKeys:      []string{"ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"},
			DefaultModel: "claude-opus-4-7",
			Mode:         providerModeAnthropic,
		},
		"azure-openai-responses": {
			Name:         "azure-openai-responses",
			BaseURL:      "",
			EnvKeys:      []string{"AZURE_OPENAI_API_KEY"},
			DefaultModel: "gpt-5.4",
			Mode:         providerModeOpenAI,
		},
		"deepseek": {
			Name:         "deepseek",
			BaseURL:      "https://api.deepseek.com",
			EnvKeys:      []string{"DEEPSEEK_API_KEY"},
			DefaultModel: "deepseek-v4-pro",
			Mode:         providerModeOpenAI,
		},
		"xai": {
			Name:         "xai",
			BaseURL:      "https://api.x.ai/v1",
			EnvKeys:      []string{"XAI_API_KEY"},
			DefaultModel: "grok-4.20-0309-reasoning",
			Mode:         providerModeOpenAI,
		},
		"groq": {
			Name:         "groq",
			BaseURL:      "https://api.groq.com/openai/v1",
			EnvKeys:      []string{"GROQ_API_KEY"},
			DefaultModel: "openai/gpt-oss-120b",
			Mode:         providerModeOpenAI,
		},
		"cerebras": {
			Name:         "cerebras",
			BaseURL:      "https://api.cerebras.ai/v1",
			EnvKeys:      []string{"CEREBRAS_API_KEY"},
			DefaultModel: "zai-glm-4.7",
			Mode:         providerModeOpenAI,
		},
		"fireworks": {
			Name:         "fireworks",
			BaseURL:      "https://api.fireworks.ai/inference",
			EnvKeys:      []string{"FIREWORKS_API_KEY"},
			DefaultModel: "accounts/fireworks/models/kimi-k2p6",
			Mode:         providerModeAnthropic,
		},
		"github-copilot": {
			Name:         "github-copilot",
			BaseURL:      "https://api.individual.githubcopilot.com",
			EnvKeys:      []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"},
			DefaultModel: "gpt-5.4",
			DefaultHeader: map[string]string{
				"User-Agent":             "GitHubCopilotChat/0.35.0",
				"Editor-Version":         "vscode/1.107.0",
				"Editor-Plugin-Version":  "copilot-chat/0.35.0",
				"Copilot-Integration-Id": "vscode-chat",
			},
			Mode: providerModeOpenAI,
		},
		"google": {
			Name:         "google",
			BaseURL:      "https://generativelanguage.googleapis.com/v1beta",
			EnvKeys:      []string{"GEMINI_API_KEY"},
			DefaultModel: "gemini-3.1-pro-preview",
			Mode:         providerModeGoogle,
		},
		"google-antigravity": {
			Name:         "google-antigravity",
			BaseURL:      "https://daily-cloudcode-pa.sandbox.googleapis.com",
			EnvKeys:      []string{"GEMINI_API_KEY"},
			DefaultModel: "gemini-3.1-pro-high",
			Mode:         providerModeGoogleCLI,
		},
		"google-gemini-cli": {
			Name:         "google-gemini-cli",
			BaseURL:      "https://cloudcode-pa.googleapis.com",
			EnvKeys:      []string{"GEMINI_API_KEY"},
			DefaultModel: "gemini-3.1-pro-preview",
			Mode:         providerModeGoogleCLI,
		},
		"google-vertex": {
			Name:         "google-vertex",
			BaseURL:      "https://{location}-aiplatform.googleapis.com",
			EnvKeys:      []string{"GOOGLE_CLOUD_API_KEY"},
			DefaultModel: "gemini-3.1-pro-preview",
			Mode:         providerModeGoogleVertex,
		},
		"minimax": {
			Name:         "minimax",
			BaseURL:      "https://api.minimax.io/anthropic",
			EnvKeys:      []string{"MINIMAX_API_KEY"},
			DefaultModel: "MiniMax-M2.7",
			Mode:         providerModeAnthropic,
		},
		"minimax-cn": {
			Name:         "minimax-cn",
			BaseURL:      "https://api.minimaxi.com/anthropic",
			EnvKeys:      []string{"MINIMAX_CN_API_KEY"},
			DefaultModel: "MiniMax-M2.7",
			Mode:         providerModeAnthropic,
		},
		"mistral": {
			Name:         "mistral",
			BaseURL:      "https://api.mistral.ai",
			EnvKeys:      []string{"MISTRAL_API_KEY"},
			DefaultModel: "devstral-medium-latest",
			Mode:         providerModeMistral,
		},
		"huggingface": {
			Name:         "huggingface",
			BaseURL:      "https://router.huggingface.co/v1",
			EnvKeys:      []string{"HF_TOKEN"},
			DefaultModel: "moonshotai/Kimi-K2.6",
			Mode:         providerModeOpenAI,
		},
		"opencode": {
			Name:         "opencode",
			BaseURL:      "https://opencode.ai/zen",
			EnvKeys:      []string{"OPENCODE_API_KEY"},
			DefaultModel: "kimi-k2.6",
			Mode:         providerModeAnthropic,
		},
		"opencode-go": {
			Name:         "opencode-go",
			BaseURL:      "https://opencode.ai/zen/go/v1",
			EnvKeys:      []string{"OPENCODE_API_KEY"},
			DefaultModel: "kimi-k2.6",
			Mode:         providerModeOpenAI,
		},
		"openrouter": {
			Name:         "openrouter",
			BaseURL:      "https://openrouter.ai/api/v1",
			EnvKeys:      []string{"OPENROUTER_API_KEY"},
			DefaultModel: "moonshotai/kimi-k2.6",
			Mode:         providerModeOpenAI,
		},
		"vercel-ai-gateway": {
			Name:         "vercel-ai-gateway",
			BaseURL:      "https://ai-gateway.vercel.sh",
			EnvKeys:      []string{"AI_GATEWAY_API_KEY"},
			DefaultModel: "zai/glm-5.1",
			Mode:         providerModeAnthropic,
		},
		"zai": {
			Name:         "zai",
			BaseURL:      "https://api.z.ai/api/coding/paas/v4",
			EnvKeys:      []string{"ZAI_API_KEY"},
			DefaultModel: "glm-5.1",
			Mode:         providerModeOpenAI,
		},
		"openai-codex": {
			Name:         "openai-codex",
			BaseURL:      "https://chatgpt.com/backend-api",
			EnvKeys:      []string{"OPENAI_API_KEY"},
			DefaultModel: "gpt-5.5",
			Mode:         providerModeCodex,
		},
		"kimi-coding": {
			Name:         "kimi-coding",
			BaseURL:      "https://api.kimi.com/coding",
			EnvKeys:      []string{"KIMI_API_KEY"},
			DefaultModel: "kimi-for-coding",
			Mode:         providerModeAnthropic,
		},
	}

	providerAliases = map[string]string{
		"deep-seek":      "deepseek",
		"grok":           "xai",
		"kimi coding":    "kimi-coding",
		"kimi_coding":    "kimi-coding",
		"opencode-go-v1": "opencode-go",
		"minimax-china":  "minimax-cn",
		"minimax cn":     "minimax-cn",
		"open-ai":        "openai",
		"open ai":        "openai",
		"gpt4":           "openai",
		"deep seek":      "deepseek",
		"gemini":         "google",
		"vertex":         "google-vertex",
	}
)

func normalizeProviderName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func canonicalProviderName(name string) string {
	key := normalizeProviderName(name)
	if canonical, ok := providerAliases[key]; ok {
		return canonical
	}
	return key
}

func ProviderSpecForProvider(name string) (ProviderSpec, bool) {
	canonical := canonicalProviderName(name)
	spec, ok := providerSpecs[canonical]
	return spec, ok
}

// ProviderAPIKey returns the first configured API key found in the environment for the provider.
func ProviderAPIKey(name string) (string, bool) {
	spec, ok := ProviderSpecForProvider(name)
	if !ok {
		return "", false
	}

	if spec.Name == "amazon-bedrock" {
		if hasBedrockCredentials() {
			return "<authenticated>", true
		}
	}

	for _, key := range spec.EnvKeys {
		if key == "" {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		value := strings.TrimSpace(getenv(key))
		if value != "" {
			return value, true
		}
	}

	if spec.Name == "google-vertex" {
		if hasGoogleVertexCredentials() {
			return "<authenticated>", true
		}
	}
	return "", false
}

func hasBedrockCredentials() bool {
	if strings.TrimSpace(os.Getenv("AWS_PROFILE")) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID")) != "" && strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY")) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("AWS_BEARER_TOKEN_BEDROCK")) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI")) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI")) != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")) != ""
}

func hasGoogleVertexCredentials() bool {
	if strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_API_KEY")) != "" {
		return true
	}

	if strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")) != "" {
		path := strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"))
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}

	if !hasGoogleEnvForADCProjectLocation() {
		return false
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	credentialPath := filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
	_, err = os.Stat(credentialPath)
	return err == nil
}

func hasGoogleEnvForADCProjectLocation() bool {
	project := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT"))
	if project == "" {
		project = strings.TrimSpace(os.Getenv("GCLOUD_PROJECT"))
	}
	location := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_LOCATION"))
	return project != "" && location != ""
}

func providerProfiles() []ProviderSpec {
	profiles := make([]ProviderSpec, 0, len(providerSpecs))
	for _, spec := range providerSpecs {
		profiles = append(profiles, spec)
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})
	return profiles
}

// DefaultModels returns the configured default provider/model pairs.
func DefaultModels() []ModelInfo {
	profiles := providerProfiles()
	result := make([]ModelInfo, 0, len(profiles))
	for _, profile := range profiles {
		if strings.TrimSpace(profile.DefaultModel) == "" {
			continue
		}
		result = append(result, ModelInfo{
			Provider: profile.Name,
			ModelID:  profile.DefaultModel,
		})
	}
	return result
}

var getenv = func(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}
