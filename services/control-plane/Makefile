# Control Plane Service Makefile

SERVICE_NAME := control-plane
BINARY_NAME := control-plane
DOCKER_IMAGE := flow/control-plane:latest

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build       - Build the service binary"
	@echo "  run         - Run the service locally"
	@echo "  dev         - Run with hot reload"
	@echo "  test        - Run all tests"
	@echo "  test-unit   - Run unit tests"
	@echo "  test-integration - Run integration tests"
	@echo "  migrate-up  - Run database migrations"
	@echo "  migrate-down - Rollback database migrations"
	@echo "  docker-build - Build Docker image"
	@echo "  clean       - Clean build artifacts"

.PHONY: build
build:
	go build -o bin/$(BINARY_NAME) ./cmd

.PHONY: run
run: build
	./bin/$(BINARY_NAME)

.PHONY: dev
dev:
	air -c .air.toml

.PHONY: test
test: test-unit test-integration

.PHONY: test-unit
test-unit:
	go test -v -race -cover ./internal/...

.PHONY: test-integration
test-integration:
	go test -v -race -tags=integration ./tests/integration/...

.PHONY: migrate-up
migrate-up:
	migrate -path ./migrations -database "postgresql://localhost:5432/analytics?sslmode=disable" up

.PHONY: migrate-down
migrate-down:
	migrate -path ./migrations -database "postgresql://localhost:5432/analytics?sslmode=disable" down

.PHONY: docker-build
docker-build:
	docker build -t $(DOCKER_IMAGE) .

.PHONY: docker-push
docker-push:
	docker push $(DOCKER_IMAGE)

.PHONY: clean
clean:
	rm -rf bin/ tmp/ *.out

.PHONY: mock-gen
mock-gen:
	mockgen -source=internal/service/interfaces.go -destination=internal/mocks/service_mocks.go