.DEFAULT_GOAL := help

GO ?= go
DOCKER ?= docker
GOLANGCI_LINT ?= golangci-lint

BINARY ?= collection-sync
PACKAGE ?= ./cmd/collection-sync
IMAGE ?= collection-sync
ENV_FILE ?= .env
ARGS ?= run --dry-run
GOLANGCI_LINT_VERSION ?= v2.11.4

.PHONY: help build test lint lint-install fmt tidy run docker-build docker-run clean

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_.-]+:.*## / {printf "%-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the collection-sync binary
	$(GO) build -v -o $(BINARY) $(PACKAGE)

test: ## Run the Go test suite
	$(GO) test -v ./...

lint: ## Run golangci-lint using the repository config
	$(GOLANGCI_LINT) run --timeout=5m ./...

lint-install: ## Install the pinned golangci-lint version locally
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

fmt: ## Format Go code
	$(GO) fmt ./...

tidy: ## Tidy Go module dependencies
	$(GO) mod tidy

run: ## Run from source; override ARGS to pass CLI arguments
	$(GO) run $(PACKAGE) $(ARGS)

docker-build: ## Build the Docker image
	$(DOCKER) build -t $(IMAGE) .

docker-run: docker-build ## Run the Docker image with ENV_FILE and ARGS overrides
	$(DOCKER) run --rm --env-file $(ENV_FILE) $(IMAGE) $(ARGS)

clean: ## Remove the built binary
	rm -f $(BINARY)