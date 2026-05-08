package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/badlogic/pigo/pkg/a2aadapter"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4388", "HTTP listen address")
	baseURL := flag.String("base-url", "", "public base URL used in the agent card")
	cwd := flag.String("cwd", "", "workspace root")
	provider := flag.String("provider", "", "default provider for new A2A tasks")
	model := flag.String("model", "", "default model for new A2A tasks")
	name := flag.String("name", "pigo", "agent card name")
	description := flag.String("description", "", "agent card description")
	bearerToken := flag.String("bearer-token", os.Getenv("PIGO_A2A_BEARER_TOKEN"), "optional bearer token required for /a2a")
	flag.Parse()

	root := *cwd
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			log.Fatal(err)
		}
	}
	server := a2aadapter.New(a2aadapter.ServerOptions{
		Root:        root,
		BaseURL:     *baseURL,
		Name:        *name,
		Description: *description,
		Provider:    *provider,
		ModelID:     *model,
		BearerToken: *bearerToken,
	})
	log.Printf("pigo A2A listening on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, server))
}
