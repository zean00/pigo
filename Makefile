.PHONY: test fmt vet conformance verify-conformance parity live-openrouter-smoke generate-models

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

conformance:
	go run ./cmd/pigo-ai-conformance --case testdata/conformance/basic-text.json
	go run ./cmd/pigo-ai-conformance --case testdata/conformance/thinking.json
	go run ./cmd/pigo-ai-conformance --case testdata/conformance/image-content.json
	go run ./cmd/pigo-agent-conformance --case testdata/conformance/agent-basic-tool.json
	go run ./cmd/pigo-agent-conformance --case testdata/conformance/agent-multi-tool.json
	go run ./cmd/pigo-agent-conformance --case testdata/conformance/agent-missing-tool.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-write-read.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-edit.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-edit-ambiguous.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-bash.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-file-discovery.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-read-error.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-edit-error.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-bash-error.json

verify-conformance:
	mkdir -p tmp/conformance
	go run ./cmd/pigo-ai-conformance --case testdata/conformance/basic-text.json > tmp/conformance/basic-text.out.json
	go run ./cmd/pigo-ai-conformance --case testdata/conformance/tool-call.json > tmp/conformance/tool-call.out.json
	go run ./cmd/pigo-ai-conformance --case testdata/conformance/empty-message.json > tmp/conformance/empty-message.out.json
	go run ./cmd/pigo-ai-conformance --case testdata/conformance/thinking.json > tmp/conformance/thinking.out.json
	go run ./cmd/pigo-ai-conformance --case testdata/conformance/image-content.json > tmp/conformance/image-content.out.json
	go run ./cmd/pigo-agent-conformance --case testdata/conformance/agent-basic-tool.json > tmp/conformance/agent-basic-tool.out.json
	go run ./cmd/pigo-agent-conformance --case testdata/conformance/agent-multi-tool.json > tmp/conformance/agent-multi-tool.out.json
	go run ./cmd/pigo-agent-conformance --case testdata/conformance/agent-missing-tool.json > tmp/conformance/agent-missing-tool.out.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-write-read.json > tmp/conformance/coding-agent-headless-write-read.out.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-edit.json > tmp/conformance/coding-agent-headless-edit.out.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-edit-ambiguous.json > tmp/conformance/coding-agent-headless-edit-ambiguous.out.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-bash.json > tmp/conformance/coding-agent-headless-bash.out.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-file-discovery.json > tmp/conformance/coding-agent-headless-file-discovery.out.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-read-error.json > tmp/conformance/coding-agent-headless-read-error.out.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-edit-error.json > tmp/conformance/coding-agent-headless-edit-error.out.json
	go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-bash-error.json > tmp/conformance/coding-agent-headless-bash-error.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/verify-cli.ts --case ../pigo/testdata/conformance/basic-text.json --output ../pigo/tmp/conformance/basic-text.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/verify-cli.ts --case ../pigo/testdata/conformance/tool-call.json --output ../pigo/tmp/conformance/tool-call.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/verify-cli.ts --case ../pigo/testdata/conformance/empty-message.json --output ../pigo/tmp/conformance/empty-message.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/verify-cli.ts --case ../pigo/testdata/conformance/thinking.json --output ../pigo/tmp/conformance/thinking.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/verify-cli.ts --case ../pigo/testdata/conformance/image-content.json --output ../pigo/tmp/conformance/image-content.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/agent-verify-cli.ts --case ../pigo/testdata/conformance/agent-basic-tool.json --output ../pigo/tmp/conformance/agent-basic-tool.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/agent-verify-cli.ts --case ../pigo/testdata/conformance/agent-multi-tool.json --output ../pigo/tmp/conformance/agent-multi-tool.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/agent-verify-cli.ts --case ../pigo/testdata/conformance/agent-missing-tool.json --output ../pigo/tmp/conformance/agent-missing-tool.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/coding-agent-verify-cli.ts --case ../pigo/testdata/conformance/coding-agent-headless-write-read.json --output ../pigo/tmp/conformance/coding-agent-headless-write-read.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/coding-agent-verify-cli.ts --case ../pigo/testdata/conformance/coding-agent-headless-edit.json --output ../pigo/tmp/conformance/coding-agent-headless-edit.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/coding-agent-verify-cli.ts --case ../pigo/testdata/conformance/coding-agent-headless-edit-ambiguous.json --output ../pigo/tmp/conformance/coding-agent-headless-edit-ambiguous.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/coding-agent-verify-cli.ts --case ../pigo/testdata/conformance/coding-agent-headless-bash.json --output ../pigo/tmp/conformance/coding-agent-headless-bash.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/coding-agent-verify-cli.ts --case ../pigo/testdata/conformance/coding-agent-headless-file-discovery.json --output ../pigo/tmp/conformance/coding-agent-headless-file-discovery.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/coding-agent-verify-cli.ts --case ../pigo/testdata/conformance/coding-agent-headless-read-error.json --output ../pigo/tmp/conformance/coding-agent-headless-read-error.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/coding-agent-verify-cli.ts --case ../pigo/testdata/conformance/coding-agent-headless-edit-error.json --output ../pigo/tmp/conformance/coding-agent-headless-edit-error.out.json
	cd ../pi-mono && npx tsx packages/ai-conformance/src/coding-agent-verify-cli.ts --case ../pigo/testdata/conformance/coding-agent-headless-bash-error.json --output ../pigo/tmp/conformance/coding-agent-headless-bash-error.out.json

parity:
	go run ./cmd/pigo-parity --pi-mono ../pi-mono

live-openrouter-smoke:
	go run ./cmd/pigo-parity --pi-mono ../pi-mono --live-openrouter

generate-models:
	go run ./cmd/pigo-generate-models
