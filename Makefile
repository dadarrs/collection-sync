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
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: help build test test-cover test-cover-html test-cover-check lint lint-install fmt tidy run docker-build docker-run clean

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_.-]+:.*## / {printf "%-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the collection-sync binary
	$(GO) build -v -ldflags="-X main.version=$(VERSION)" -o $(BINARY) $(PACKAGE)

test: ## Run the Go test suite
	$(GO) test -v ./...

test-cover: ## Run the Go test suite with a coverage profile and summary
	$(GO) test -covermode=atomic -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

test-cover-html: ## Generate an HTML coverage report
	$(GO) test -covermode=atomic -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

test-cover-check: ## Enforce overall and package-level coverage gates
	$(GO) test -covermode=atomic -coverprofile=coverage.out ./...
	sh ./scripts/coverage-check.sh coverage.out

lint: ## Run golangci-lint using the repository config
	$(GOLANGCI_LINT) run --timeout=5m ./...

lint-install: ## Install the pinned golangci-lint version locally
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

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
