.DEFAULT_GOAL := help

# Go modules in the workspace (go.work). Each is built and tested on its own:
# `go test ./...` at the repository root does not cover every module.
MODULES  := agent core gateway kit knowledge
SERVICES := agent core gateway knowledge

BIN_DIR     := bin
WEB_DIR     := web
FEATURE_DIR := .devcontainer/ballet-agent
DOCS_DIR    := docs
SITE_DIR    := site
SITE_OUT    := _site
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.1
# Where `make bundle` copies the built SPA for Core to embed (bindata tag).
WEBUI_DIST  := core/internal/transport/webui/dist
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

.PHONY: agent-root-test
agent-root-test: ## Test running sessions as a separate OS user (needs root, e.g. sudo -E make agent-root-test)
	cd agent && go test -count=1 -run SessionUser -v ./internal/process/

.PHONY: feature
feature: ## Bundle the agent binaries (amd64, arm64) into the devcontainer Feature (.devcontainer/ballet-agent)
	@mkdir -p $(FEATURE_DIR)/bin
	@set -e; for arch in amd64 arm64; do echo "==> agent linux/$$arch"; \
		(cd agent && CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -o ../$(FEATURE_DIR)/bin/ballet-agent-$$arch ./cmd/agent); done

.PHONY: feature-test
feature-test: feature ## Build the example agent pool with the Feature and check it (needs Docker and the devcontainer CLI)
	devcontainer build --workspace-folder . --config .devcontainer/agent-pool-example/devcontainer.json \
		--image-name ballet-pool-example
	docker run --rm --entrypoint sh ballet-pool-example -c \
		'ballet-agent -h 2>&1 | grep -q config && id ballet && grep -q "pool=example" /etc/ballet/agent.toml && claude --version && opencode --version'

.PHONY: failure-test
failure-test: ## Crash Core, the agent and the gateway mid-stage with the real binaries and check recovery
	cd core && BALLET_FAILURE_TESTS=1 go test -count=1 -v ./test/failure/

.PHONY: build
build: ## Build all service binaries into bin/
	@mkdir -p $(BIN_DIR)
	@set -e; for s in $(SERVICES); do echo "==> build $$s"; (cd $$s && go build -o ../$(BIN_DIR)/$$s ./cmd/$$s); done

.PHONY: tidy
tidy: ## Tidy every module and sync the workspace
	$(call each_module,go mod tidy)
	go work sync

##@ Run

.PHONY: bundle
bundle: web-deps ## Build all binaries into bin/, with the web UI embedded in core
	cd $(WEB_DIR) && bun run build
	rm -rf $(WEBUI_DIST) && cp -R $(WEB_DIR)/build $(WEBUI_DIST)
	@mkdir -p $(BIN_DIR)
	@set -e; for s in $(SERVICES); do echo "==> build $$s"; \
		tags=; [ $$s = core ] && tags=bindata; \
		(cd $$s && go build -tags "$$tags" -o ../$(BIN_DIR)/$$s ./cmd/$$s); done
	cd gateway && go build -o ../$(BIN_DIR)/fake-anthropic ./cmd/fake-anthropic

.PHONY: run
run: bundle ## Build everything and run Ballet on http://localhost:8080 (Ctrl-C stops)
	scripts/run.sh

##@ Containers

IMAGE_TARGETS := core gateway knowledge agent

.PHONY: images
images: ## Build the images (ballet-<service>), the agent image (ballet-agent) included
	@set -e; for t in $(IMAGE_TARGETS); do echo "==> image ballet-$$t"; \
		docker build -f deploy/Containerfile --target $$t -t ballet-$$t .; done

.PHONY: dashboards
dashboards: ## Regenerate the Grafana dashboards (deploy/monitoring/dashboards.py)
	python3 deploy/monitoring/dashboards.py

##@ Web

.PHONY: web-deps
web-deps: ## Install web dependencies unless installed
	@[ -d $(WEB_DIR)/node_modules ] || (cd $(WEB_DIR) && bun install --frozen-lockfile)

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

.PHONY: api-generate
api-generate: ## Regenerate the web API types from core/api/openapi.yaml
	cd $(WEB_DIR) && bun run generate:api

.PHONY: api-check
api-check: ## Fail if the web API types differ from core/api/openapi.yaml
	@tmp=$$(mktemp -d) && cd $(WEB_DIR) && \
		bunx openapi-typescript ../core/api/openapi.yaml -o $$tmp/schema.d.ts >/dev/null && \
		diff -q $$tmp/schema.d.ts src/lib/api/schema.d.ts >/dev/null || \
		{ echo "web/src/lib/api/schema.d.ts is stale: run 'make api-generate'"; exit 1; }

.PHONY: web-check
web-check: api-check web-lint web-typecheck web-test ## Check API types, lint, type-check and unit-test the web UI

##@ Development dependencies

.PHONY: dev-keycloak
dev-keycloak: ## Run development Keycloak on :8180 (Java distribution, realm "ballet")
	scripts/dev-keycloak.sh

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

.PHONY: site
site: docs ## Build the website (site/) with the docs under /docs/ into _site
	rm -rf $(SITE_OUT)
	cp -r $(SITE_DIR) $(SITE_OUT)
	cp -r $(DOCS_DIR)/_build/html $(SITE_OUT)/docs
	touch $(SITE_OUT)/.nojekyll

.PHONY: site-serve
site-serve: site ## Serve the built website on http://127.0.0.1:8001
	python3 -m http.server --directory $(SITE_OUT) --bind 127.0.0.1 8001

##@ Aggregate

.PHONY: go-check
go-check: format-check vet lint test-race test-standalone build ## Run all Go checks

.PHONY: check
check: go-check web-check ## Run all Go and web checks (CI entry point)

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)/core $(BIN_DIR)/gateway $(BIN_DIR)/knowledge $(BIN_DIR)/agent
	rm -rf $(BIN_DIR)/fake-anthropic $(WEBUI_DIST) $(FEATURE_DIR)/bin
	rm -rf $(DOCS_DIR)/_build $(SITE_OUT) $(WEB_DIR)/build $(WEB_DIR)/.svelte-kit
	rm -rf $(WEB_DIR)/test-results $(WEB_DIR)/playwright-report
