package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type OAuthStore struct {
	Providers map[string]OAuthCredentials `json:"providers"`
}

func LoadOAuthStore(path string) (OAuthStore, error) {
	if path == "" {
		return OAuthStore{}, fmt.Errorf("missing oauth store path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return OAuthStore{Providers: map[string]OAuthCredentials{}}, nil
		}
		return OAuthStore{}, err
	}
	var store OAuthStore
	if err := json.Unmarshal(data, &store); err != nil {
		return OAuthStore{}, fmt.Errorf("parse oauth store: %w", err)
	}
	if store.Providers == nil {
		store.Providers = map[string]OAuthCredentials{}
	}
	return store, nil
}

func SaveOAuthStore(path string, store OAuthStore) error {
	if path == "" {
		return fmt.Errorf("missing oauth store path")
	}
	if store.Providers == nil {
		store.Providers = map[string]OAuthCredentials{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func UpsertOAuthStoreCredentials(path string, provider string, credentials OAuthCredentials) error {
	store, err := LoadOAuthStore(path)
	if err != nil {
		return err
	}
	if store.Providers == nil {
		store.Providers = map[string]OAuthCredentials{}
	}
	store.Providers[canonicalProviderName(provider)] = credentials
	return SaveOAuthStore(path, store)
}
