package ai

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type OAuthCredentials struct {
	Refresh   string         `json:"refresh"`
	Access    string         `json:"access"`
	Expires   int64          `json:"expires"`
	ProjectID string         `json:"projectId,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type OAuthProviderID = string

type OAuthProviderInfo struct {
	ID                 OAuthProviderID `json:"id"`
	Name               string          `json:"name"`
	UsesCallbackServer bool            `json:"usesCallbackServer,omitempty"`
}

type OAuthPrompt struct {
	Message     string `json:"message"`
	Placeholder string `json:"placeholder,omitempty"`
	AllowEmpty  bool   `json:"allowEmpty,omitempty"`
}

type OAuthAuthInfo struct {
	URL          string `json:"url"`
	Instructions string `json:"instructions,omitempty"`
}

type OAuthLoginCallbacks struct {
	OnAuth   func(info OAuthAuthInfo)
	OnPrompt func(prompt OAuthPrompt) (string, error)
	Context  context.Context
}

type OAuthProviderInterface interface {
	ID() OAuthProviderID
	Name() string
	UsesCallbackServer() bool
	Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error)
	RefreshToken(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error)
	GetAPIKey(credentials OAuthCredentials) string
}

type OAuthModelMutator interface {
	ModifyModels(models []Model, credentials OAuthCredentials) []Model
}

type oauthProvider struct {
	id                 OAuthProviderID
	name               string
	usesCallbackServer bool
	login              func(callbacks OAuthLoginCallbacks) (OAuthCredentials, error)
	refreshToken       func(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error)
	getAPIKey          func(credentials OAuthCredentials) string
}

func (provider oauthProvider) ID() OAuthProviderID { return provider.id }
func (provider oauthProvider) Name() string        { return provider.name }
func (provider oauthProvider) UsesCallbackServer() bool {
	return provider.usesCallbackServer
}
func (provider oauthProvider) Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	if provider.login == nil {
		return OAuthCredentials{}, fmt.Errorf("oauth login is not supported for provider: %s", provider.id)
	}
	return provider.login(callbacks)
}
func (provider oauthProvider) RefreshToken(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
	if provider.refreshToken == nil {
		return OAuthCredentials{}, fmt.Errorf("oauth refresh is not supported for provider: %s", provider.id)
	}
	return provider.refreshToken(ctx, credentials)
}
func (provider oauthProvider) GetAPIKey(credentials OAuthCredentials) string {
	if provider.getAPIKey == nil {
		return credentials.Access
	}
	return provider.getAPIKey(credentials)
}

var (
	oauthProvidersMu sync.RWMutex
	oauthProviders   = map[string]OAuthProviderInterface{}

	oauthHTTPClient = http.DefaultClient

	anthropicOAuthTokenURL   = "https://platform.claude.com/v1/oauth/token"
	anthropicOAuthClientID   = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	googleOAuthTokenURL      = "https://oauth2.googleapis.com/token"
	geminiCLIOAuthClientID   = ""
	geminiCLIOAuthSecret     = ""
	antigravityOAuthClientID = ""
	antigravityOAuthSecret   = ""
	openAICodexTokenURL      = "https://auth.openai.com/oauth/token"
	openAICodexClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	anthropicAuthorizeURL    = "https://claude.ai/oauth/authorize"
	anthropicScopes          = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	anthropicRedirectURI     = "http://localhost:53692/callback"
	googleAuthorizeURL       = "https://accounts.google.com/o/oauth2/v2/auth"
	googleGeminiRedirectURI  = "http://localhost:8085/oauth2callback"
	googleGeminiScopes       = []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
	}
	antigravityRedirectURI = "http://localhost:51121/oauth-callback"
	antigravityScopes      = []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
		"https://www.googleapis.com/auth/cclog",
		"https://www.googleapis.com/auth/experimentsandconfigs",
	}
	defaultAntigravityProjectID = "rising-fact-p41fc"
	openAICodexAuthorizeURL     = "https://auth.openai.com/oauth/authorize"
	openAICodexRedirectURI      = "http://localhost:1455/auth/callback"
	openAICodexScope            = "openid profile email offline_access"
)

func init() {
	resetOAuthProvidersLocked()
}

func GetOAuthProvider(id OAuthProviderID) OAuthProviderInterface {
	oauthProvidersMu.RLock()
	defer oauthProvidersMu.RUnlock()
	return oauthProviders[canonicalProviderName(id)]
}

func RegisterOAuthProvider(provider OAuthProviderInterface) {
	oauthProvidersMu.Lock()
	defer oauthProvidersMu.Unlock()
	oauthProviders[canonicalProviderName(provider.ID())] = provider
}

func UnregisterOAuthProvider(id OAuthProviderID) {
	oauthProvidersMu.Lock()
	defer oauthProvidersMu.Unlock()
	canonical := canonicalProviderName(id)
	if builtIn, ok := builtInOAuthProviders()[canonical]; ok {
		oauthProviders[canonical] = builtIn
		return
	}
	delete(oauthProviders, canonical)
}

func ResetOAuthProviders() {
	oauthProvidersMu.Lock()
	defer oauthProvidersMu.Unlock()
	resetOAuthProvidersLocked()
}

func GetOAuthProviders() []OAuthProviderInterface {
	oauthProvidersMu.RLock()
	defer oauthProvidersMu.RUnlock()
	out := make([]OAuthProviderInterface, 0, len(oauthProviders))
	for _, provider := range oauthProviders {
		out = append(out, provider)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID() < out[j].ID()
	})
	return out
}

func GetOAuthProviderInfoList() []OAuthProviderInfo {
	providers := GetOAuthProviders()
	out := make([]OAuthProviderInfo, 0, len(providers))
	for _, provider := range providers {
		out = append(out, OAuthProviderInfo{
			ID:                 provider.ID(),
			Name:               provider.Name(),
			UsesCallbackServer: provider.UsesCallbackServer(),
		})
	}
	return out
}

func RefreshOAuthToken(ctx context.Context, providerID OAuthProviderID, credentials OAuthCredentials) (OAuthCredentials, error) {
	provider := GetOAuthProvider(providerID)
	if provider == nil {
		return OAuthCredentials{}, fmt.Errorf("unknown oauth provider: %s", providerID)
	}
	return provider.RefreshToken(ctx, credentials)
}

func GetOAuthAPIKey(ctx context.Context, providerID OAuthProviderID, credentials map[string]OAuthCredentials) (OAuthCredentials, string, bool, error) {
	provider := GetOAuthProvider(providerID)
	if provider == nil {
		return OAuthCredentials{}, "", false, fmt.Errorf("unknown oauth provider: %s", providerID)
	}
	creds, ok := credentials[providerID]
	if !ok {
		return OAuthCredentials{}, "", false, nil
	}
	needsRefresh := strings.TrimSpace(creds.Access) == "" && strings.TrimSpace(creds.Refresh) != ""
	if creds.Expires > 0 && time.Now().UnixMilli() >= creds.Expires {
		needsRefresh = true
	}
	if needsRefresh {
		refreshed, err := provider.RefreshToken(ctx, creds)
		if err != nil {
			return OAuthCredentials{}, "", false, fmt.Errorf("failed to refresh oauth token for %s", providerID)
		}
		creds = refreshed
	}
	return creds, provider.GetAPIKey(creds), true, nil
}

func builtInOAuthProviders() map[string]OAuthProviderInterface {
	return map[string]OAuthProviderInterface{
		"anthropic": oauthProvider{
			id:                 "anthropic",
			name:               "Anthropic (Claude Pro/Max)",
			usesCallbackServer: true,
			login: func(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
				return loginAnthropicOAuth(callbacks)
			},
			refreshToken: func(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
				return refreshAnthropicOAuthToken(ctx, credentials.Refresh)
			},
			getAPIKey: func(credentials OAuthCredentials) string { return credentials.Access },
		},
		"google-gemini-cli": oauthProvider{
			id:                 "google-gemini-cli",
			name:               "Google Cloud Code Assist",
			usesCallbackServer: true,
			login: func(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
				clientID, clientSecret, err := googleOAuthClientCredentials("google-gemini-cli", "PIGO_GEMINI_CLI_OAUTH_CLIENT_ID", "PIGO_GEMINI_CLI_OAUTH_CLIENT_SECRET", geminiCLIOAuthClientID, geminiCLIOAuthSecret)
				if err != nil {
					return OAuthCredentials{}, err
				}
				return loginGoogleOAuth(callbacks, clientID, clientSecret, googleGeminiRedirectURI, googleGeminiScopes, "")
			},
			refreshToken: func(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
				clientID, clientSecret, err := googleOAuthClientCredentials("google-gemini-cli", "PIGO_GEMINI_CLI_OAUTH_CLIENT_ID", "PIGO_GEMINI_CLI_OAUTH_CLIENT_SECRET", geminiCLIOAuthClientID, geminiCLIOAuthSecret)
				if err != nil {
					return OAuthCredentials{}, err
				}
				return refreshGoogleOAuthToken(ctx, clientID, clientSecret, credentials.Refresh, credentials.ProjectID)
			},
			getAPIKey: googleOAuthAPIKey,
		},
		"google-antigravity": oauthProvider{
			id:                 "google-antigravity",
			name:               "Antigravity",
			usesCallbackServer: true,
			login: func(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
				clientID, clientSecret, err := googleOAuthClientCredentials("google-antigravity", "PIGO_ANTIGRAVITY_OAUTH_CLIENT_ID", "PIGO_ANTIGRAVITY_OAUTH_CLIENT_SECRET", antigravityOAuthClientID, antigravityOAuthSecret)
				if err != nil {
					return OAuthCredentials{}, err
				}
				return loginGoogleOAuth(callbacks, clientID, clientSecret, antigravityRedirectURI, antigravityScopes, defaultAntigravityProjectID)
			},
			refreshToken: func(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
				clientID, clientSecret, err := googleOAuthClientCredentials("google-antigravity", "PIGO_ANTIGRAVITY_OAUTH_CLIENT_ID", "PIGO_ANTIGRAVITY_OAUTH_CLIENT_SECRET", antigravityOAuthClientID, antigravityOAuthSecret)
				if err != nil {
					return OAuthCredentials{}, err
				}
				return refreshGoogleOAuthToken(ctx, clientID, clientSecret, credentials.Refresh, credentials.ProjectID)
			},
			getAPIKey: googleOAuthAPIKey,
		},
		"openai-codex": oauthProvider{
			id:                 "openai-codex",
			name:               "ChatGPT Plus/Pro (Codex Subscription)",
			usesCallbackServer: true,
			login: func(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
				return loginOpenAICodexOAuth(callbacks)
			},
			refreshToken: func(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
				return refreshOpenAICodexOAuthToken(ctx, credentials.Refresh)
			},
			getAPIKey: func(credentials OAuthCredentials) string { return credentials.Access },
		},
	}
}

func googleOAuthClientCredentials(providerID, clientIDEnv, clientSecretEnv, fallbackClientID, fallbackClientSecret string) (string, string, error) {
	clientID := strings.TrimSpace(os.Getenv(clientIDEnv))
	if clientID == "" {
		clientID = strings.TrimSpace(fallbackClientID)
	}
	clientSecret := strings.TrimSpace(os.Getenv(clientSecretEnv))
	if clientSecret == "" {
		clientSecret = strings.TrimSpace(fallbackClientSecret)
	}
	if clientID == "" || clientSecret == "" {
		return "", "", fmt.Errorf("%s oauth requires %s and %s", providerID, clientIDEnv, clientSecretEnv)
	}
	return clientID, clientSecret, nil
}

func resetOAuthProvidersLocked() {
	oauthProviders = builtInOAuthProviders()
}

func refreshAnthropicOAuthToken(ctx context.Context, refreshToken string) (OAuthCredentials, error) {
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("client_id", anthropicOAuthClientID)
	values.Set("refresh_token", refreshToken)
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := postOAuthForm(ctx, anthropicOAuthTokenURL, values, &payload); err != nil {
		return OAuthCredentials{}, err
	}
	return OAuthCredentials{
		Refresh: payload.RefreshToken,
		Access:  payload.AccessToken,
		Expires: expiryFromNow(payload.ExpiresIn),
	}, nil
}

func loginAnthropicOAuth(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return OAuthCredentials{}, err
	}
	authURL := anthropicAuthorizeURL + "?" + url.Values{
		"code":                  []string{"true"},
		"client_id":             []string{anthropicOAuthClientID},
		"response_type":         []string{"code"},
		"redirect_uri":          []string{anthropicRedirectURI},
		"scope":                 []string{anthropicScopes},
		"code_challenge":        []string{challenge},
		"code_challenge_method": []string{"S256"},
		"state":                 []string{verifier},
	}.Encode()
	notifyAuth(callbacks, OAuthAuthInfo{
		URL:          authURL,
		Instructions: "Complete login in your browser, then paste the final redirect URL or authorization code.",
	})
	input, err := promptText(callbacks, OAuthPrompt{Message: "Paste redirect URL or authorization code"})
	if err != nil {
		return OAuthCredentials{}, err
	}
	code, state := parseAuthorizationInput(input)
	if code == "" {
		return OAuthCredentials{}, fmt.Errorf("missing authorization code")
	}
	if state == "" {
		state = verifier
	}
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("client_id", anthropicOAuthClientID)
	values.Set("code", code)
	values.Set("state", state)
	values.Set("redirect_uri", anthropicRedirectURI)
	values.Set("code_verifier", verifier)
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := postOAuthForm(callbackContext(callbacks), anthropicOAuthTokenURL, values, &payload); err != nil {
		return OAuthCredentials{}, err
	}
	return OAuthCredentials{
		Refresh: payload.RefreshToken,
		Access:  payload.AccessToken,
		Expires: expiryFromNow(payload.ExpiresIn),
	}, nil
}

func refreshGoogleOAuthToken(ctx context.Context, clientID, clientSecret, refreshToken, projectID string) (OAuthCredentials, error) {
	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("client_secret", clientSecret)
	values.Set("refresh_token", refreshToken)
	values.Set("grant_type", "refresh_token")
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := postOAuthForm(ctx, googleOAuthTokenURL, values, &payload); err != nil {
		return OAuthCredentials{}, err
	}
	nextRefresh := payload.RefreshToken
	if nextRefresh == "" {
		nextRefresh = refreshToken
	}
	return OAuthCredentials{
		Refresh:   nextRefresh,
		Access:    payload.AccessToken,
		Expires:   expiryFromNow(payload.ExpiresIn),
		ProjectID: projectID,
	}, nil
}

func loginGoogleOAuth(callbacks OAuthLoginCallbacks, clientID, clientSecret, redirectURI string, scopes []string, defaultProjectID string) (OAuthCredentials, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return OAuthCredentials{}, err
	}
	state, err := createState()
	if err != nil {
		return OAuthCredentials{}, err
	}
	authURL := googleAuthorizeURL + "?" + url.Values{
		"client_id":             []string{clientID},
		"response_type":         []string{"code"},
		"redirect_uri":          []string{redirectURI},
		"scope":                 []string{strings.Join(scopes, " ")},
		"code_challenge":        []string{challenge},
		"code_challenge_method": []string{"S256"},
		"state":                 []string{state},
		"access_type":           []string{"offline"},
		"prompt":                []string{"consent"},
	}.Encode()
	notifyAuth(callbacks, OAuthAuthInfo{
		URL:          authURL,
		Instructions: "Complete login in your browser, then paste the final redirect URL or authorization code.",
	})
	input, err := promptText(callbacks, OAuthPrompt{Message: "Paste redirect URL or authorization code"})
	if err != nil {
		return OAuthCredentials{}, err
	}
	code, _ := parseAuthorizationInput(input)
	if code == "" {
		return OAuthCredentials{}, fmt.Errorf("missing authorization code")
	}
	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("client_secret", clientSecret)
	values.Set("code", code)
	values.Set("grant_type", "authorization_code")
	values.Set("redirect_uri", redirectURI)
	values.Set("code_verifier", verifier)
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := postOAuthForm(callbackContext(callbacks), googleOAuthTokenURL, values, &payload); err != nil {
		return OAuthCredentials{}, err
	}
	projectID, err := promptText(callbacks, OAuthPrompt{
		Message:     "Google Cloud project ID",
		Placeholder: defaultProjectID,
		AllowEmpty:  defaultProjectID != "",
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		projectID = defaultProjectID
	}
	if projectID == "" {
		return OAuthCredentials{}, fmt.Errorf("missing projectId")
	}
	nextRefresh := payload.RefreshToken
	if nextRefresh == "" {
		return OAuthCredentials{}, fmt.Errorf("no refresh token received")
	}
	return OAuthCredentials{
		Refresh:   nextRefresh,
		Access:    payload.AccessToken,
		Expires:   expiryFromNow(payload.ExpiresIn),
		ProjectID: projectID,
	}, nil
}

func refreshOpenAICodexOAuthToken(ctx context.Context, refreshToken string) (OAuthCredentials, error) {
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", refreshToken)
	values.Set("client_id", openAICodexClientID)
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := postOAuthForm(ctx, openAICodexTokenURL, values, &payload); err != nil {
		return OAuthCredentials{}, err
	}
	return OAuthCredentials{
		Refresh: payload.RefreshToken,
		Access:  payload.AccessToken,
		Expires: time.Now().UnixMilli() + payload.ExpiresIn*1000,
	}, nil
}

func loginOpenAICodexOAuth(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return OAuthCredentials{}, err
	}
	state, err := createState()
	if err != nil {
		return OAuthCredentials{}, err
	}
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", openAICodexClientID)
	params.Set("redirect_uri", openAICodexRedirectURI)
	params.Set("scope", openAICodexScope)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", state)
	params.Set("id_token_add_organizations", "true")
	params.Set("codex_cli_simplified_flow", "true")
	notifyAuth(callbacks, OAuthAuthInfo{
		URL:          openAICodexAuthorizeURL + "?" + params.Encode(),
		Instructions: "Complete login in your browser, then paste the final redirect URL or authorization code.",
	})
	input, err := promptText(callbacks, OAuthPrompt{Message: "Paste redirect URL or authorization code"})
	if err != nil {
		return OAuthCredentials{}, err
	}
	code, _ := parseAuthorizationInput(input)
	if code == "" {
		return OAuthCredentials{}, fmt.Errorf("missing authorization code")
	}
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("code_verifier", verifier)
	values.Set("redirect_uri", openAICodexRedirectURI)
	values.Set("client_id", openAICodexClientID)
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := postOAuthForm(callbackContext(callbacks), openAICodexTokenURL, values, &payload); err != nil {
		return OAuthCredentials{}, err
	}
	return OAuthCredentials{
		Refresh: payload.RefreshToken,
		Access:  payload.AccessToken,
		Expires: time.Now().UnixMilli() + payload.ExpiresIn*1000,
	}, nil
}

func googleOAuthAPIKey(credentials OAuthCredentials) string {
	payload := map[string]any{
		"token": credentials.Access,
	}
	if strings.TrimSpace(credentials.ProjectID) != "" {
		payload["projectId"] = credentials.ProjectID
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func generatePKCE() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", fmt.Errorf("generate pkce verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw[:])
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	return verifier, challenge, nil
}

func createState() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate oauth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func parseAuthorizationInput(input string) (code string, state string) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", ""
	}
	if parsed, err := url.Parse(value); err == nil {
		code = parsed.Query().Get("code")
		state = parsed.Query().Get("state")
		if code != "" || state != "" {
			return code, state
		}
	}
	return value, ""
}

func notifyAuth(callbacks OAuthLoginCallbacks, info OAuthAuthInfo) {
	if callbacks.OnAuth != nil {
		callbacks.OnAuth(info)
	}
}

func promptText(callbacks OAuthLoginCallbacks, prompt OAuthPrompt) (string, error) {
	if callbacks.OnPrompt == nil {
		return "", fmt.Errorf("oauth prompt callback is required")
	}
	value, err := callbacks.OnPrompt(prompt)
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" && !prompt.AllowEmpty {
		return "", fmt.Errorf("missing input")
	}
	return value, nil
}

func callbackContext(callbacks OAuthLoginCallbacks) context.Context {
	if callbacks.Context != nil {
		return callbacks.Context
	}
	return context.Background()
}

func overrideOAuthEndpointForTests(target *string, value string) func() {
	previous := *target
	*target = value
	return func() { *target = previous }
}

func postOAuthForm(ctx context.Context, endpoint string, values url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(values.Encode()))
	if err != nil {
		return fmt.Errorf("create oauth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("call oauth endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("oauth endpoint error: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("parse oauth response: %w", err)
	}
	return nil
}

func expiryFromNow(expiresInSeconds int64) int64 {
	return time.Now().UnixMilli() + expiresInSeconds*1000 - int64(5*time.Minute/time.Millisecond)
}
