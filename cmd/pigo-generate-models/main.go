package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	sourcePath = flag.String("source", "", "path to the TypeScript models.generated.ts source file")
	outputPath = flag.String("output", "", "path to write the generated models JSON file")
)

func main() {
	flag.Parse()

	source := strings.TrimSpace(*sourcePath)
	output := strings.TrimSpace(*outputPath)
	if source == "" {
		source = filepath.Join("..", "pi-mono", "packages", "ai", "src", "models.generated.ts")
	}
	if output == "" {
		output = filepath.Join("pkg", "ai", "models.generated.json")
	}

	input, err := os.ReadFile(source)
	if err != nil {
		panic(fmt.Sprintf("read source file %q: %v", source, err))
	}

	candidate, err := GenerateModelsJSON(string(input))
	if err != nil {
		panic(fmt.Sprintf("generate models json: %v", err))
	}

	if err := os.WriteFile(output, candidate, 0644); err != nil {
		panic(fmt.Sprintf("write output file %q: %v", output, err))
	}
}

var (
	reExportMarker       = regexp.MustCompile(`\bexport\s+const\s+MODELS\b`)
	reSatisfiesSuffix    = regexp.MustCompile(`}\s*satisfies\s+Model<[^>]+>`)
	reTrailingSeparators = regexp.MustCompile(`,\s*([}\]])`)
	reUnquotedKeys       = regexp.MustCompile(`(?m)([{,\n]\s*)([A-Za-z_][A-Za-z0-9_]*)\s*:`)
)

func GenerateModelsJSON(input string) ([]byte, error) {
	raw, ok := extractModelsObject(input)
	if !ok {
		return nil, fmt.Errorf("failed to locate MODELS export object in source")
	}

	candidate := reSatisfiesSuffix.ReplaceAllString(raw, "}")
	candidate = reTrailingSeparators.ReplaceAllString(candidate, "$1")
	candidate = reUnquotedKeys.ReplaceAllString(candidate, `$1"$2":`)

	if err := json.Unmarshal([]byte(candidate), &struct{}{}); err != nil {
		return nil, fmt.Errorf("parse converted JSON: %v", err)
	}

	var compacted bytes.Buffer
	if err := json.Compact(&compacted, []byte(candidate)); err != nil {
		return nil, fmt.Errorf("compact JSON: %v", err)
	}

	return compacted.Bytes(), nil
}

func extractModelsObject(input string) (string, bool) {
	match := reExportMarker.FindStringIndex(input)
	if match == nil {
		return "", false
	}
	start := match[1]
	open := strings.Index(input[start:], "{")
	if open < 0 {
		return "", false
	}
	pos := start + open

	depth := 0
	inSingle := false
	inDouble := false
	inBacktick := false
	escaped := false
	inLineComment := false
	inBlockComment := false

	for i := pos; i < len(input); i++ {
		ch := input[i]

		if escaped {
			escaped = false
			continue
		}

		if inSingle {
			if ch == '\\' {
				escaped = true
			} else if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inDouble = false
			}
			continue
		}
		if inBacktick {
			if ch == '\\' {
				escaped = true
			} else if ch == '`' {
				inBacktick = false
			}
			continue
		}

		if inLineComment && ch == '\n' {
			inLineComment = false
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(input) && input[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}

		if !inSingle && !inDouble && !inBacktick {
			if ch == '/' && i+1 < len(input) && input[i+1] == '/' {
				inLineComment = true
				i++
				continue
			}
			if ch == '/' && i+1 < len(input) && input[i+1] == '*' {
				inBlockComment = true
				i++
				continue
			}
		}

		switch ch {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '`':
			inBacktick = true
		case '{':
			if !inSingle && !inDouble && !inBacktick {
				depth++
			}
		case '}':
			if !inSingle && !inDouble && !inBacktick {
				depth--
				if depth == 0 {
					return input[pos : i+1], true
				}
			}
		}
	}

	return "", false
}
