.PHONY: build build-all build-linux build-darwin test test-coverage lint clean run install deps release release-dry-run release-snapshot release-check docs-check list

list: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | sort | awk -F ':.*## ' '{printf "  %-24s %s\n", $$1, $$2}'

BINARY=pmox
BUILD_DIR=bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

build: ## Build the binary
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/pmox

build-all: build-linux build-darwin ## Build for all platforms

build-linux: ## Build for Linux (amd64, arm64)
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/pmox
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-arm64 ./cmd/pmox

build-darwin: ## Build for macOS (amd64, arm64)
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 ./cmd/pmox
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 ./cmd/pmox

test: ## Run unit tests
	go test -v -short ./...

test-coverage: ## Run tests with coverage report
	go test -short -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint: ## Run linter
	golangci-lint run

docs-check: ## Validate internal links in README, llms.txt, docs/, examples/
	@if command -v lychee >/dev/null 2>&1; then \
		lychee --offline README.md llms.txt docs/ examples/; \
	else \
		go run ./internal/tools/doccheck; \
	fi

clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

run: ## Run pmox directly via go run
	go run ./cmd/pmox

install: build ## Build and install to /usr/local/bin
	cp $(BUILD_DIR)/$(BINARY) /usr/local/bin/

deps: ## Install dependencies
	go mod tidy

release: ## Create and push a release tag
	@if [ -z "$(TAG)" ]; then \
		LATEST=$$(git tag --sort=-version:refname 2>/dev/null | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | head -1); \
		if [ -z "$$LATEST" ]; then \
			SUGGESTED="v0.1.0"; \
		else \
			MAJOR=$$(echo $$LATEST | sed 's/v\([0-9]*\)\.\([0-9]*\)\.\([0-9]*\)/\1/'); \
			MINOR=$$(echo $$LATEST | sed 's/v\([0-9]*\)\.\([0-9]*\)\.\([0-9]*\)/\2/'); \
			PATCH=$$(echo $$LATEST | sed 's/v\([0-9]*\)\.\([0-9]*\)\.\([0-9]*\)/\3/'); \
			PATCH=$$((PATCH + 1)); \
			SUGGESTED="v$$MAJOR.$$MINOR.$$PATCH"; \
		fi; \
		echo "Latest tag: $${LATEST:-none}"; \
		printf "Enter tag [$$SUGGESTED]: "; \
		read INPUT_TAG; \
		TAG=$${INPUT_TAG:-$$SUGGESTED}; \
		echo "Creating release $$TAG..."; \
		git tag -a $$TAG -m "Release $$TAG" && \
		git push origin $$TAG && \
		echo "Release $$TAG pushed. GitHub Actions will build and publish artifacts."; \
	else \
		echo "Creating release $(TAG)..."; \
		git tag -a $(TAG) -m "Release $(TAG)" && \
		git push origin $(TAG) && \
		echo "Release $(TAG) pushed. GitHub Actions will build and publish artifacts."; \
	fi

release-dry-run: ## Test release without publishing
	goreleaser release --snapshot --clean --skip=publish

release-snapshot: ## Create snapshot release
	goreleaser release --snapshot --clean

release-check: ## Check GoReleaser configuration
	goreleaser check
