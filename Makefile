.DEFAULT_GOAL := help

DOCS_DIR   := docs
DOCS_BUILD := $(DOCS_DIR)/_build
SPHINX     := cd $(DOCS_DIR) && uv run --frozen sphinx-build

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

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

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(DOCS_BUILD)
