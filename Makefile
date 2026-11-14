ROOT_DIR := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))

BINARY  := rx
PKG     := ./cmd/rx
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Dev tools are installed into test/bin, versioned so a bump triggers a reinstall.
# Keep versions in sync with .github/workflows.
LOCALBIN              ?= $(ROOT_DIR)/test/bin
GOLANGCI_LINT_VERSION ?= v2.15.0
GORELEASER_VERSION    ?= v2.20.2
GOLANGCI_LINT         ?= $(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
GORELEASER            ?= $(LOCALBIN)/goreleaser-$(GORELEASER_VERSION)

export CGO_ENABLED ?= 0

.DEFAULT_GOAL := build

.PHONY: build install test cover lint fmt vet tidy snapshot clean tools clean-tools help

$(LOCALBIN):
	mkdir -p $@

$(GOLANGCI_LINT): | $(LOCALBIN)
	GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	mv $(LOCALBIN)/golangci-lint $@

$(GORELEASER): | $(LOCALBIN)
	GOBIN=$(LOCALBIN) CGO_ENABLED=0 go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)
	mv $(LOCALBIN)/goreleaser $@

build: ## Build bin/rx
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(PKG)

install: ## Install rx into GOBIN
	go install -trimpath -ldflags "$(LDFLAGS)" $(PKG)

test: ## Run tests (set TESTFLAGS=-race to enable the race detector, needs cgo)
	go test $(TESTFLAGS) ./...

cover: ## Run tests with a coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint: $(GOLANGCI_LINT) ## Run golangci-lint
	$(GOLANGCI_LINT) run

fmt: ## Format code
	gofmt -s -w .

vet: ## Run go vet
	go vet ./...

tidy: ## Tidy go.mod and fail if it changed
	go mod tidy
	git diff --exit-code -- go.mod go.sum

snapshot: $(GORELEASER) ## Build a local release with goreleaser into dist/
	$(GORELEASER) release --snapshot --clean

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) dist coverage.out

tools: $(GOLANGCI_LINT) $(GORELEASER) ## Install dev tools into test/bin

clean-tools: ## Remove dev tools from test/bin
	rm -rf $(LOCALBIN)

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-10s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
