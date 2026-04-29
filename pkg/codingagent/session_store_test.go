package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/badlogic/pigo/pkg/ai"
)

func TestSessionStoreDoesNotPersistOAuthCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := NewSessionStore(path)

	if err := store.Append(SessionEntry{
		Type:          "oauth_login",
		OAuthProvider: "anthropic",
		OAuthCredentials: &ai.OAuthCredentials{
			Access:  "access-secret",
			Refresh: "refresh-secret",
		},
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(data)
	if strings.Contains(raw, "access-secret") || strings.Contains(raw, "refresh-secret") || strings.Contains(raw, "oauthCredentials") {
		t.Fatalf("session log contains oauth secrets: %s", raw)
	}

	entries, err := store.ReadEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].OAuthProvider != "anthropic" {
		t.Fatalf("oauth provider = %q", entries[0].OAuthProvider)
	}
	if entries[0].OAuthCredentials != nil {
		t.Fatalf("oauth credentials were restored from session log: %#v", entries[0].OAuthCredentials)
	}
}
