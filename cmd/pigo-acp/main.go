package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"

	"github.com/badlogic/pigo/pkg/acpadapter"
)

func main() {
	authFile := flag.String("auth-file", "", "optional OAuth credential store")
	agentDir := flag.String("agent-dir", "", "agent config directory for prompts and skills")
	discoverResources := flag.Bool("discover-resources", true, "discover prompt templates and skills from default resource directories")
	promptPaths := flag.String("prompt-path", "", "comma-separated prompt template files or directories")
	skillPaths := flag.String("skill-path", "", "comma-separated skill files or directories")
	flag.Parse()

	server := acpadapter.New(acpadapter.ServerOptions{
		AuthFile:          *authFile,
		AgentDir:          *agentDir,
		DiscoverResources: *discoverResources,
		PromptPaths:       splitCSV(*promptPaths),
		SkillPaths:        splitCSV(*skillPaths),
	})
	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
