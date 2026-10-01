.DEFAULT_GOAL := help

# Go modules in the workspace (go.work). Each is built and tested on its own:
# `go test ./...` at the repository root does not cover every module.
MODULES  := core gateway kit knowledge runner
SERVICES := core gateway knowledge runner

BIN_DIR     := bin
WEB_DIR     := web
DOCS_DIR    := docs
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.1
SPHINX      := cd $(DOCS_DIR) && uv run --frozen sphinx-build

# Run a command in every Go module directory, failing on the first error.
define each_module
	@set -e; for m in $(MODULES); do echo "==> $$m: $(1)"; (cd $$m && $(1)); done
endef

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

##@ Go

.PHONY: format
format: ## Format Go code (gofmt -w)
	gofmt -w $(MODULES)

.PHONY: format-check
format-check: ## Fail if Go code is not gofmt-formatted
	@out=$$(gofmt -l $(MODULES)); if [ -n "$$out" ]; then echo "Not gofmt-formatted:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Run go vet in every module
	$(call each_module,go vet ./...)

.PHONY: lint
lint: ## Run staticcheck in every module
	$(call each_module,go run $(STATICCHECK) ./...)

.PHONY: test
test: ## Run Go tests in every module
	$(call each_module,go test ./...)

.PHONY: test-race
test-race: ## Run Go tests with the race detector in every module
	$(call each_module,go test -race ./...)

.PHONY: test-standalone
test-standalone: ## Test every module without the workspace (GOWORK=off)
	$(call each_module,GOWORK=off go test ./...)

.PHONY: build
build: ## Build all service binaries into bin/
	@mkdir -p $(BIN_DIR)
	@set -e; for s in $(SERVICES); do echo "==> build $$s"; (cd $$s && go build -o ../$(BIN_DIR)/$$s ./cmd/$$s); done

.PHONY: tidy
tidy: ## Tidy every module and sync the workspace
	$(call each_module,go mod tidy)
	go work sync

##@ Web

.PHONY: web-install
web-install: ## Install web dependencies (frozen lockfile)
	cd $(WEB_DIR) && bun install --frozen-lockfile

.PHONY: web-build
web-build: ## Build the web UI into web/build
	cd $(WEB_DIR) && bun run build

.PHONY: web-lint
web-lint: ## Lint and format-check the web UI
	cd $(WEB_DIR) && bun run lint

.PHONY: web-typecheck
web-typecheck: ## Type-check the web UI (svelte-check)
	cd $(WEB_DIR) && bun run check

.PHONY: web-test
web-test: ## Run web unit tests (Vitest)
	cd $(WEB_DIR) && bun run test

.PHONY: web-e2e
web-e2e: ## Run web end-to-end tests (Playwright, Google Chrome)
	cd $(WEB_DIR) && bun run test:e2e

.PHONY: web-check
web-check: web-lint web-typecheck web-test ## Lint, type-check and unit-test the web UI

##@ Documentation

.PHONY: docs
docs: ## Build the documentation site into docs/_build/html
	$(SPHINX) -b html . _build/html

.PHONY: docs-serve
docs-serve: ## Live-reloading docs preview on http://127.0.0.1:8000
	cd $(DOCS_DIR) && uv run --frozen sphinx-autobuild . _build/html --host 127.0.0.1 --port 8000

.PHONY: docs-check
docs-check: ## Strict docs build (warnings are errors) + linkcheck
	$(SPHINX) -W --keep-going -b html . _build/html
	$(SPHINX) -W --keep-going -b linkcheck . _build/linkcheck

##@ Aggregate

.PHONY: check
check: format-check vet lint test-race test-standalone build web-check ## Run all Go and web checks (CI entry point)

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)/core $(BIN_DIR)/gateway $(BIN_DIR)/knowledge $(BIN_DIR)/runner
	rm -rf $(DOCS_DIR)/_build $(WEB_DIR)/build $(WEB_DIR)/.svelte-kit
	rm -rf $(WEB_DIR)/test-results $(WEB_DIR)/playwright-report
