package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"

	"github.com/badlogic/pigo/pkg/codingagent"
	"github.com/badlogic/pigo/pkg/conformance"
)

func main() {
	casePath := flag.String("case", "", "optional coding-agent conformance fixture to preload")
	cwd := flag.String("cwd", "", "workspace root")
	sessionFile := flag.String("session-file", "", "optional JSONL session file")
	authFile := flag.String("auth-file", "", "optional OAuth credential store")
	agentDir := flag.String("agent-dir", "", "agent config directory for prompts and skills")
	discoverResources := flag.Bool("discover-resources", true, "discover prompt templates and skills from default resource directories")
	promptPaths := flag.String("prompt-path", "", "comma-separated prompt template files or directories")
	skillPaths := flag.String("skill-path", "", "comma-separated skill files or directories")
	flag.Parse()
	root := *cwd
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			log.Fatal(err)
		}
	}
	var session *codingagent.Session
	if *casePath != "" {
		testCase, err := conformance.ReadJSON[conformance.CodingAgentCase](*casePath)
		if err != nil {
			log.Fatal(err)
		}
		session, err = conformance.NewCodingAgentSessionFromCase(root, testCase)
		if err != nil {
			log.Fatal(err)
		}
	} else {
		session = codingagent.NewSession(root, nil)
	}
	if *sessionFile != "" {
		session.Store = codingagent.NewSessionStore(*sessionFile)
	}
	if *authFile != "" {
		if err := session.LoadOAuthStore(*authFile); err != nil {
			log.Fatal(err)
		}
	}
	session.LoadSlashCommandResources(codingagent.ResourceLoadOptions{
		AgentDir:        *agentDir,
		PromptPaths:     splitCSV(*promptPaths),
		SkillPaths:      splitCSV(*skillPaths),
		IncludeDefaults: *discoverResources,
	})
	server := codingagent.RPCServer{Session: session}
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
