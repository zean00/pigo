package conformance

import (
	"flag"
	"fmt"
)

func CasePathFromFlags() (string, error) {
	casePath := flag.String("case", "", "path to conformance fixture")
	flag.Parse()
	if *casePath == "" {
		return "", fmt.Errorf("missing --case <path>")
	}
	return *casePath, nil
}
