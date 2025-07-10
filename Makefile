# automagic GitLab Automation Makefile

# Build variables
BINARY_NAME=automagic
VERSION?=dev
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME?=$(shell date -u '+%Y-%m-%d %H:%M:%S UTC')

# Go build flags
LDFLAGS=-ldflags "-X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.buildTime=$(BUILD_TIME)'"

# Default target
.PHONY: build
build:
	@echo "Building $(BINARY_NAME)..."
	@echo "Version: $(VERSION)"
	@echo "Commit: $(COMMIT)"
	@echo "Build Time: $(BUILD_TIME)"
	go build $(LDFLAGS) -o $(BINARY_NAME) .

# Development build (same as build)
.PHONY: dev
dev: build

# Release build with version tag
.PHONY: release
release:
	@if [ -z "$(VERSION)" ] || [ "$(VERSION)" = "dev" ]; then \
		echo "Error: VERSION must be set for release builds"; \
		echo "Usage: make release VERSION=v1.0.0"; \
		exit 1; \
	fi
	@echo "Building release $(VERSION)..."
	go build $(LDFLAGS) -o $(BINARY_NAME) .

# Clean build artifacts
.PHONY: clean
clean:
	rm -f $(BINARY_NAME)

# Install to GOPATH/bin
.PHONY: install
install:
	go install $(LDFLAGS) .

# Run tests
.PHONY: test
test:
	go test ./...

# Show build info without building
.PHONY: info
info:
	@echo "Build Information:"
	@echo "  Binary: $(BINARY_NAME)"
	@echo "  Version: $(VERSION)"
	@echo "  Commit: $(COMMIT)"
	@echo "  Build Time: $(BUILD_TIME)"

# Help target
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build     - Build the binary (default)"
	@echo "  dev       - Development build (alias for build)"
	@echo "  release   - Release build (requires VERSION=x.x.x)"
	@echo "  clean     - Remove build artifacts"
	@echo "  install   - Install to GOPATH/bin"
	@echo "  test      - Run tests"
	@echo "  info      - Show build information"
	@echo "  help      - Show this help"
	@echo ""
	@echo "Examples:"
	@echo "  make build"
	@echo "  make release VERSION=v1.2.0"
	@echo "  VERSION=v1.0.0 make build"