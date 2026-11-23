BINARY_NAME=ytdlp-api
GO=go
SWAG=$(shell $(GO) env GOPATH)/bin/swag

.PHONY: all build clean test test-race vet swag run docker-build docker-up help

all: swag test-race vet build

## swag: Generate Swagger OpenAPI documentation
swag:
	@echo "Generating Swagger docs..."
	$(SWAG) init -g cmd/server/main.go

## build: Build the server binary
build:
	@echo "Building $(BINARY_NAME)..."
	$(GO) build -o bin/$(BINARY_NAME) ./cmd/server

## test: Run unit tests
test:
	$(GO) test -v -shuffle=on ./...

## test-race: Run unit tests with race detector
test-race:
	$(GO) test -v -race -shuffle=on ./...

## vet: Run go vet
vet:
	$(GO) vet ./...

## run: Generate docs and run the server locally
run: swag
	$(GO) run ./cmd/server

## docker-build: Build Docker image
docker-build:
	docker build -t $(BINARY_NAME):latest .

## docker-up: Start service with Docker Compose
docker-up:
	docker compose up -d --build

## clean: Remove build artifacts
clean:
	rm -rf bin/ coverage.out coverage.html

## help: Show available make targets
help:
	@echo "Usage: make [target]"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
