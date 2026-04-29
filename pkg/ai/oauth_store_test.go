package ai

import (
	"path/filepath"
	"testing"
)

func TestLoadOAuthStoreMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := LoadOAuthStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Providers) != 0 {
		t.Fatalf("providers = %#v", store.Providers)
	}
}

func TestUpsertOAuthStoreCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := UpsertOAuthStoreCredentials(path, "Anthropic", OAuthCredentials{
		Access:  "token-a",
		Refresh: "token-r",
		Expires: 123,
	}); err != nil {
		t.Fatal(err)
	}
	store, err := LoadOAuthStore(path)
	if err != nil {
		t.Fatal(err)
	}
	creds, ok := store.Providers["anthropic"]
	if !ok {
		t.Fatalf("providers = %#v", store.Providers)
	}
	if creds.Access != "token-a" || creds.Refresh != "token-r" || creds.Expires != 123 {
		t.Fatalf("credentials = %#v", creds)
	}
}
