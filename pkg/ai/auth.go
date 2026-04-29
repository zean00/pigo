package ai

import "sort"

type AuthMethod string

const (
	AuthMethodAPIKey     AuthMethod = "api-key"
	AuthMethodOAuth      AuthMethod = "oauth"
	AuthMethodADC        AuthMethod = "adc"
	AuthMethodAWS        AuthMethod = "aws"
	AuthMethodBearer     AuthMethod = "bearer"
	AuthMethodContainer  AuthMethod = "container"
	AuthMethodWebIdentity AuthMethod = "web-identity"
)

type ProviderAuthInfo struct {
	Provider  string
	EnvKeys   []string
	Methods   []AuthMethod
	BaseURL   string
	Configured bool
}

func ProviderAuthInfoForProvider(name string) (ProviderAuthInfo, bool) {
	spec, ok := ProviderSpecForProvider(name)
	if !ok {
		return ProviderAuthInfo{}, false
	}
	info := ProviderAuthInfo{
		Provider:  spec.Name,
		EnvKeys:   append([]string(nil), spec.EnvKeys...),
		BaseURL:   spec.BaseURL,
		Configured: hasAnyAuthConfigured(spec.Name),
	}
	info.Methods = authMethodsForProvider(spec.Name)
	return info, true
}

func ProviderAuthInfos() []ProviderAuthInfo {
	profiles := providerProfiles()
	out := make([]ProviderAuthInfo, 0, len(profiles))
	for _, profile := range profiles {
		info, ok := ProviderAuthInfoForProvider(profile.Name)
		if ok {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Provider < out[j].Provider
	})
	return out
}

func authMethodsForProvider(name string) []AuthMethod {
	switch canonicalProviderName(name) {
	case "anthropic":
		return []AuthMethod{AuthMethodOAuth, AuthMethodAPIKey}
	case "google-gemini-cli", "google-antigravity":
		return []AuthMethod{AuthMethodOAuth, AuthMethodAPIKey}
	case "google-vertex":
		return []AuthMethod{AuthMethodAPIKey, AuthMethodADC}
	case "openai-codex":
		return []AuthMethod{AuthMethodOAuth, AuthMethodAPIKey}
	case "amazon-bedrock":
		return []AuthMethod{AuthMethodAWS, AuthMethodBearer, AuthMethodContainer, AuthMethodWebIdentity}
	default:
		return []AuthMethod{AuthMethodAPIKey}
	}
}

func hasAnyAuthConfigured(name string) bool {
	if _, ok := ProviderAPIKey(name); ok {
		return true
	}
	return false
}
