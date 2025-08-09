.PHONY: build run test clean docker-build docker-run

# Variables
BINARY_NAME=watermark-service
DOCKER_IMAGE=flow-watermark-service
GO_FILES=$(shell find . -name '*.go' -type f)

# Build the service
build:
	go build -o bin/$(BINARY_NAME) cmd/main.go

# Run the service locally
run: build
	./bin/$(BINARY_NAME)

# Run with debug logging
run-debug: build
	./bin/$(BINARY_NAME) -log-level=debug

# Run tests
test:
	go test -v -race ./...

# Run tests with coverage
test-coverage:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
	rm -rf bin/ coverage.out coverage.html

# Install dependencies
deps:
	go mod download
	go mod tidy

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run

# Build Docker image
docker-build:
	docker build -t $(DOCKER_IMAGE):latest .

# Run in Docker
docker-run:
	docker run -p 8084:8084 $(DOCKER_IMAGE):latest

# Generate mocks (if needed)
mocks:
	mockgen -source=internal/store/store.go -destination=internal/store/mock_store.go -package=store

# Development mode with hot reload (requires air)
dev:
	air

# Quick test endpoint
test-endpoint:
	@echo "Testing watermark endpoints..."
	@curl -s http://localhost:8084/health | jq .
	@curl -s http://localhost:8084/api/v1/watermark/system | jq .
	@curl -s http://localhost:8084/api/v1/freshness | jq .

# Simulate watermark update
test-update:
	@echo "Sending test watermark update..."
	@curl -X POST http://localhost:8084/api/v1/watermark/update \
		-H "Content-Type: application/json" \
		-d '{"service_id":"test-service","service_type":"gateway","namespace":"default","table":"events","shard_id":0,"event_time_ms":1234567890000,"processing_time_ms":1234567891000,"event_count":1000}' | jq .

# Help
help:
	@echo "Watermark Service Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  build          - Build the binary"
	@echo "  run            - Build and run the service"
	@echo "  run-debug      - Run with debug logging"
	@echo "  test           - Run tests"
	@echo "  test-coverage  - Run tests with coverage"
	@echo "  clean          - Remove build artifacts"
	@echo "  deps           - Download dependencies"
	@echo "  fmt            - Format code"
	@echo "  lint           - Lint code"
	@echo "  docker-build   - Build Docker image"
	@echo "  docker-run     - Run in Docker"
	@echo "  dev            - Run with hot reload"
	@echo "  test-endpoint  - Test API endpoints"
	@echo "  test-update    - Send test watermark update"
	@echo "  help           - Show this help message"