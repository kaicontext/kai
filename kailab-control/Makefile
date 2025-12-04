.PHONY: all build test clean run dev lint fmt vet tidy help web

# Binary name
BINARY := kailab-control
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

# Default target
all: build

# Build the binary
build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/kailab-control

# Build for production (optimized)
build-prod:
	CGO_ENABLED=0 go build $(LDFLAGS) -trimpath -o $(BINARY) ./cmd/kailab-control

# Run tests
test:
	go test -v ./...

# Run tests with coverage
test-cover:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run tests with race detection
test-race:
	go test -v -race ./...

# Clean build artifacts
clean:
	rm -f $(BINARY)
	rm -f $(BINARY).db
	rm -f coverage.out coverage.html
	rm -rf frontend/node_modules frontend/.svelte-kit

# Run the server
run: build
	./$(BINARY)

# Development mode with debug logging
dev: build
	KLC_DEBUG=true ./$(BINARY)

# Lint
lint:
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run

# Format code
fmt:
	go fmt ./...
	gofmt -s -w .

# Vet
vet:
	go vet ./...

# Tidy dependencies
tidy:
	go mod tidy

# Build web frontend (requires npm)
web:
	cd frontend && npm install && npm run build

# Watch web frontend for development
web-dev:
	cd frontend && npm run dev

# Install dependencies
deps:
	go mod download
	cd frontend && npm install

# Docker build
docker:
	docker build -t kailab-control:$(VERSION) .

# Help
help:
	@echo "Available targets:"
	@echo "  build      - Build the binary"
	@echo "  build-prod - Build optimized production binary"
	@echo "  test       - Run tests"
	@echo "  test-cover - Run tests with coverage report"
	@echo "  test-race  - Run tests with race detection"
	@echo "  clean      - Clean build artifacts"
	@echo "  run        - Build and run the server"
	@echo "  dev        - Build and run with debug logging"
	@echo "  lint       - Run linter"
	@echo "  fmt        - Format code"
	@echo "  vet        - Run go vet"
	@echo "  tidy       - Tidy go modules"
	@echo "  web        - Build Svelte + Tailwind frontend"
	@echo "  web-dev    - Watch frontend for development"
	@echo "  deps       - Install all dependencies"
	@echo "  docker     - Build Docker image"
	@echo "  help       - Show this help"
