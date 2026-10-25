# golangci-lint, pinned in its own module so this module's go.mod has no
# dependencies.
GOLANGCI := go tool -modfile=tools/lint/go.mod golangci-lint
SCHEMA_VERSION := $(shell cat schema/VERSION)
SCHEMA_URL := https://github.com/agentclientprotocol/agent-client-protocol/releases/download/schema-v$(SCHEMA_VERSION)
SCHEMA_FILES := schema.json schema.unstable.json meta.json meta.unstable.json
FUZZTIME ?= 30s

.PHONY: generate schema test lint check generated fuzz bench cover interop

# The Go code from the schema in schema/ (acpgen: internal/acpgen).
generate:
	go generate ./...

# Fetches the schema release schema/VERSION names, checks it against
# schema/SHA256SUMS, and generates the code again. To move to another
# release, put its version in schema/VERSION and its files' SHA-256s
# (on the release's page) in schema/SHA256SUMS.
schema:
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
	for f in $(SCHEMA_FILES); do curl -fsSL -o "$$tmp/$$f" "$(SCHEMA_URL)/$$f" || exit 1; done && \
	(cd "$$tmp" && shasum -a 256 -c "$(CURDIR)/schema/SHA256SUMS") && \
	cp "$$tmp"/*.json schema/
	$(MAKE) generate

test:
	go test -race ./...

lint:
	$(GOLANGCI) run ./...
	$(GOLANGCI) run --build-tags interop ./internal/interop/...

# Fails if the generated code wasn't what the schema and acpgen make (it
# is regenerated then).
generated:
	@state() { git status --porcelain -- '*_gen.go' unstable/; git diff -- '*_gen.go' unstable/; }; \
	before=$$(state | shasum); $(MAKE) -s generate; after=$$(state | shasum); \
	if [ "$$before" != "$$after" ]; then echo "the generated code was out of date: run make generate"; exit 1; fi

check: lint generated test

# Runs each fuzz target for FUZZTIME; new inputs go to Go's cache (copy
# the interesting ones into testdata/fuzz to keep them).
fuzz:
	go test -run '^$$' -fuzz '^FuzzStream$$' -fuzztime $(FUZZTIME) .
	go test -run '^$$' -fuzz '^FuzzDecode$$' -fuzztime $(FUZZTIME) .

bench:
	go test -run '^$$' -bench . -benchtime 2s .

cover:
	go test -coverprofile=coverage.out . ./steering
	go tool cover -func=coverage.out | tail -1

# Against the official TypeScript SDK; needs Node.js 24 or later and the
# network (internal/interop says what it downloads, into a temp folder).
interop:
	go test -tags interop -count=1 -v ./internal/interop
