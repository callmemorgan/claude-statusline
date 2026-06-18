## claude-statusline dev tasks.
## Go is the primary language; JS/TS (npm shim, build script, pi extension) is linted with Biome.

GO_LINT      := golangci-lint
BIOME        := ./node_modules/.bin/biome

.PHONY: lint lint-go lint-js fmt fmt-go fmt-js vet test check clean install-tools

## Install dev tooling (golangci-lint via brew, biome via npm).
install-tools:
	@command -v $(GO_LINT) >/dev/null 2>&1 || brew install golangci-lint
	@[ -x $(BIOME) ] || npm install

## Lint everything (Go + JS/TS).
lint: lint-go lint-js
lint-go:
	$(GO_LINT) run ./...
lint-js:
	@[ -x $(BIOME) ] || $(MAKE) install-tools
	$(BIOME) check

## Format.
fmt: fmt-go fmt-js
fmt-go:
	gofmt -w .
	goimports -w -local github.com/callmemorgan/claude-statusline . 2>/dev/null || true
fmt-js:
	@[ -x $(BIOME) ] || $(MAKE) install-tools
	$(BIOME) format --write

## Go vet + tests.
vet:
	go vet ./...
test:
	go test ./...

## Full pre-commit gate: lint + vet + test.
check: lint vet test