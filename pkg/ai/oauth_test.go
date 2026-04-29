package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetOAuthProvidersIncludesBuiltIns(t *testing.T) {
	ResetOAuthProviders()
	providers := GetOAuthProviders()
	if len(providers) < 4 {
		t.Fatalf("providers = %#v", providers)
	}
	if GetOAuthProvider("anthropic") == nil {
		t.Fatal("missing anthropic oauth provider")
	}
}

func TestRefreshOAuthTokenAnthropic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %q", req.Method)
		}
		if got := req.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
			t.Fatalf("content-type = %q", got)
		}
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer server.Close()

	defer overrideOAuthEndpointForTests(&anthropicOAuthTokenURL, server.URL)()

	creds, err := RefreshOAuthToken(context.Background(), "anthropic", OAuthCredentials{Refresh: "refresh"})
	if err != nil {
		t.Fatal(err)
	}
	if creds.Access != "new-access" || creds.Refresh != "new-refresh" {
		t.Fatalf("credentials = %#v", creds)
	}
}

func TestLoginAnthropicOAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if req.Form.Get("grant_type") != "authorization_code" {
			t.Fatalf("grant_type = %q", req.Form.Get("grant_type"))
		}
		_, _ = w.Write([]byte(`{"access_token":"token-a","refresh_token":"token-r","expires_in":3600}`))
	}))
	defer server.Close()
	defer overrideOAuthEndpointForTests(&anthropicOAuthTokenURL, server.URL)()

	provider := GetOAuthProvider("anthropic")
	if provider == nil {
		t.Fatal("missing provider")
	}
	var authURL string
	creds, err := provider.Login(OAuthLoginCallbacks{
		OnAuth: func(info OAuthAuthInfo) { authURL = info.URL },
		OnPrompt: func(prompt OAuthPrompt) (string, error) {
			return "http://localhost:53692/callback?code=abc&state=ignored", nil
		},
		Context: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if authURL == "" || !strings.Contains(authURL, "claude.ai/oauth/authorize") {
		t.Fatalf("authURL = %q", authURL)
	}
	if creds.Access != "token-a" || creds.Refresh != "token-r" {
		t.Fatalf("credentials = %#v", creds)
	}
}

func TestGetOAuthAPIKeyRefreshesExpiredGoogleCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"new-token","expires_in":3600}`))
	}))
	defer server.Close()

	defer overrideOAuthEndpointForTests(&googleOAuthTokenURL, server.URL)()

	creds, apiKey, ok, err := GetOAuthAPIKey(context.Background(), "google-gemini-cli", map[string]OAuthCredentials{
		"google-gemini-cli": {
			Refresh:   "refresh-token",
			Access:    "old-token",
			Expires:   time.Now().Add(-time.Hour).UnixMilli(),
			ProjectID: "project-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected oauth credentials")
	}
	if creds.Access != "new-token" {
		t.Fatalf("access = %q", creds.Access)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(apiKey), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["token"] != "new-token" || payload["projectId"] != "project-1" {
		t.Fatalf("api key payload = %#v", payload)
	}
}

func TestGetOAuthAPIKeyRefreshesMissingAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"new-token","expires_in":3600}`))
	}))
	defer server.Close()

	defer overrideOAuthEndpointForTests(&googleOAuthTokenURL, server.URL)()

	creds, apiKey, ok, err := GetOAuthAPIKey(context.Background(), "google-gemini-cli", map[string]OAuthCredentials{
		"google-gemini-cli": {
			Refresh:   "refresh-token",
			ProjectID: "project-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected oauth credentials")
	}
	if creds.Access != "new-token" {
		t.Fatalf("access = %q", creds.Access)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(apiKey), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["token"] != "new-token" || payload["projectId"] != "project-1" {
		t.Fatalf("api key payload = %#v", payload)
	}
}

func TestRegisterAndUnregisterOAuthProvider(t *testing.T) {
	ResetOAuthProviders()
	RegisterOAuthProvider(oauthProvider{
		id:   "custom",
		name: "Custom",
		login: func(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
			return OAuthCredentials{Access: "x"}, nil
		},
		getAPIKey: func(credentials OAuthCredentials) string {
			return credentials.Access
		},
	})
	if GetOAuthProvider("custom") == nil {
		t.Fatal("expected custom oauth provider")
	}
	UnregisterOAuthProvider("custom")
	if GetOAuthProvider("custom") != nil {
		t.Fatal("expected custom oauth provider to be removed")
	}
}
