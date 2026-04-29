package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/badlogic/pigo/pkg/ai"
)

func main() {
	listOnly := flag.Bool("list", false, "list built-in OAuth providers")
	providerID := flag.String("provider", "", "provider to log into")
	authFile := flag.String("auth-file", defaultAuthFile(), "path to auth.json store")
	flag.Parse()

	if *listOnly {
		if err := json.NewEncoder(os.Stdout).Encode(ai.GetOAuthProviderInfoList()); err != nil {
			log.Fatal(err)
		}
		return
	}

	providerName := strings.TrimSpace(*providerID)
	if providerName == "" {
		log.Fatal("missing --provider")
	}
	provider := ai.GetOAuthProvider(providerName)
	if provider == nil {
		log.Fatalf("unknown oauth provider: %s", providerName)
	}

	reader := bufio.NewReader(os.Stdin)
	credentials, err := provider.Login(ai.OAuthLoginCallbacks{
		Context: context.Background(),
		OnAuth: func(info ai.OAuthAuthInfo) {
			if strings.TrimSpace(info.Instructions) != "" {
				_, _ = fmt.Fprintln(os.Stderr, info.Instructions)
			}
			_, _ = fmt.Fprintln(os.Stderr, info.URL)
		},
		OnPrompt: func(prompt ai.OAuthPrompt) (string, error) {
			label := prompt.Message
			if strings.TrimSpace(prompt.Placeholder) != "" {
				label += " [" + prompt.Placeholder + "]"
			}
			_, _ = fmt.Fprintf(os.Stderr, "%s: ", label)
			line, err := reader.ReadString('\n')
			if err != nil {
				return "", err
			}
			line = strings.TrimSpace(line)
			if line == "" && prompt.AllowEmpty {
				return prompt.Placeholder, nil
			}
			return line, nil
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := ai.UpsertOAuthStoreCredentials(*authFile, providerName, credentials); err != nil {
		log.Fatal(err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "saved %s credentials to %s\n", provider.ID(), *authFile)
}

func defaultAuthFile() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return "auth.json"
	}
	return filepath.Join(configDir, "pigo", "auth.json")
}
