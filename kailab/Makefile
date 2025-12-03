.PHONY: build run test clean fmt lint

# Build the kailabd binary
build:
	go build -o kailabd ./cmd/kailabd

# Run the server with default settings
run:
	go run ./cmd/kailabd --listen=:7447 --data=.kailab --tenant=default --repo=main

# Run tests
test:
	go test ./...

# Run tests with verbose output
test-v:
	go test -v ./...

# Run tests with coverage
test-cover:
	go test -cover ./...

# Format code
fmt:
	go fmt ./...

# Run linter (requires golangci-lint)
lint:
	golangci-lint run

# Clean build artifacts
clean:
	rm -f kailabd
	rm -rf .kailab

# Update dependencies
deps:
	go mod tidy

# Build for multiple platforms
build-all:
	GOOS=linux GOARCH=amd64 go build -o kailabd-linux-amd64 ./cmd/kailabd
	GOOS=darwin GOARCH=amd64 go build -o kailabd-darwin-amd64 ./cmd/kailabd
	GOOS=darwin GOARCH=arm64 go build -o kailabd-darwin-arm64 ./cmd/kailabd
	GOOS=windows GOARCH=amd64 go build -o kailabd-windows-amd64.exe ./cmd/kailabd

# Docker build (example)
docker:
	docker build -t kailabd .

# Development: run with auto-reload (requires entr)
dev:
	find . -name '*.go' | entr -r go run ./cmd/kailabd --listen=:7447 --data=.kailab --tenant=default --repo=main
