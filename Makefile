BINARY  := rx
PKG     := ./cmd/rx
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

export CGO_ENABLED ?= 0

.DEFAULT_GOAL := build

.PHONY: build install test cover lint fmt vet tidy snapshot clean help

build: ## Build bin/rx
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(PKG)

install: ## Install rx into GOBIN
	go install -trimpath -ldflags "$(LDFLAGS)" $(PKG)

test: ## Run tests (set TESTFLAGS=-race to enable the race detector, needs cgo)
	go test $(TESTFLAGS) ./...

cover: ## Run tests with a coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint: ## Run golangci-lint
	golangci-lint run

fmt: ## Format code
	gofmt -s -w .

vet: ## Run go vet
	go vet ./...

tidy: ## Tidy go.mod and fail if it changed
	go mod tidy
	git diff --exit-code -- go.mod go.sum

snapshot: ## Build a local release with goreleaser into dist/
	goreleaser release --snapshot --clean

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) dist coverage.out

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-10s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
