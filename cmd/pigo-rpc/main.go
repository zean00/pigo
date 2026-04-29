package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/badlogic/pigo/pkg/codingagent"
	"github.com/badlogic/pigo/pkg/conformance"
)

func main() {
	casePath := flag.String("case", "", "optional coding-agent conformance fixture to preload")
	cwd := flag.String("cwd", "", "workspace root")
	sessionFile := flag.String("session-file", "", "optional JSONL session file")
	authFile := flag.String("auth-file", "", "optional OAuth credential store")
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
	server := codingagent.RPCServer{Session: session}
	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
