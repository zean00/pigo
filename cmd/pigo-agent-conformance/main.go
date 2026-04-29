package main

import (
	"log"

	"github.com/badlogic/pigo/pkg/conformance"
)

func main() {
	casePath, err := conformance.CasePathFromFlags()
	if err != nil {
		log.Fatal(err)
	}
	testCase, err := conformance.ReadJSON[conformance.AgentCase](casePath)
	if err != nil {
		log.Fatal(err)
	}
	output, err := conformance.RunAgent(testCase)
	if err != nil {
		log.Fatal(err)
	}
	if err := conformance.WriteJSON(output); err != nil {
		log.Fatal(err)
	}
}
