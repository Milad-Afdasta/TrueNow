# Root Makefile for Real-Time Analytics Platform

.PHONY: help
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*##"; printf "\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  %-20s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# Infrastructure
.PHONY: infra-up
infra-up: ## Start infrastructure dependencies (Kafka, PostgreSQL, Redis)
	docker-compose -f infrastructure/docker/docker-compose.yml up -d

.PHONY: infra-down
infra-down: ## Stop infrastructure dependencies
	docker-compose -f infrastructure/docker/docker-compose.yml down

.PHONY: infra-clean
infra-clean: ## Clean infrastructure data
	docker-compose -f infrastructure/docker/docker-compose.yml down -v

# Database
.PHONY: migrate-all
migrate-all: ## Run all database migrations
	@echo "Running control-plane migrations..."
	cd services/control-plane && make migrate-up

# Building
.PHONY: build-all
build-all: ## Build all services into build/ directory
	@$(MAKE) -f Makefile.services build-all

.PHONY: build-gateway
build-gateway: ## Build gateway service
	@$(MAKE) -f Makefile.services build-gateway

.PHONY: build-control-plane
build-control-plane: ## Build control-plane service
	@$(MAKE) -f Makefile.services build-control-plane

.PHONY: build-hot-tier
build-hot-tier: ## Build hot-tier service
	@$(MAKE) -f Makefile.services build-hot-tier

.PHONY: build-ingester
build-ingester: ## Build stream-ingester service
	@$(MAKE) -f Makefile.services build-stream-ingester

.PHONY: clean-builds
clean-builds: ## Clean all built binaries
	@$(MAKE) -f Makefile.services clean

.PHONY: build-sizes
build-sizes: ## Show sizes of built binaries
	@$(MAKE) -f Makefile.services sizes
	cd services/stream-ingester && make build

.PHONY: build-query-api
build-query-api: ## Build query-api service
	cd services/query-api && make build

# Running
.PHONY: run-all
run-all: ## Run all services locally
	@echo "Starting all services..."
	@for service in control-plane gateway hot-tier stream-ingester query-api watermark-service; do \
		echo "Starting $$service..."; \
		cd services/$$service && make run & \
		cd ../..; \
	done
	@echo "All services started. Press Ctrl+C to stop."
	@wait

.PHONY: run-control-plane
run-control-plane: ## Run control-plane service
	cd services/control-plane && make run

.PHONY: run-gateway
run-gateway: ## Run gateway service
	cd services/gateway && make run

.PHONY: run-hot-tier
run-hot-tier: ## Run hot-tier service
	cd services/hot-tier && make run

.PHONY: run-hot-tier-cluster
run-hot-tier-cluster: ## Run hot-tier cluster (3 nodes)
	cd services/hot-tier && make run-cluster

.PHONY: run-ingester
run-ingester: ## Run stream-ingester service
	cd services/stream-ingester && make run

.PHONY: run-query-api
run-query-api: ## Run query-api service
	cd services/query-api && make run

# Testing
.PHONY: test-all
test-all: ## Run all tests
	@for service in services/*/; do \
		echo "Testing $$service..."; \
		cd $$service && make test && cd ../..; \
	done

.PHONY: test-unit
test-unit: ## Run unit tests for all services
	@for service in services/*/; do \
		echo "Unit testing $$service..."; \
		cd $$service && make test-unit && cd ../..; \
	done

.PHONY: test-integration
test-integration: ## Run integration tests
	cd tests/integration && go test -v ./...

.PHONY: test-e2e
test-e2e: ## Run end-to-end tests
	cd tests/integration && make test-e2e

.PHONY: load-test
load-test: ## Run load tests
	cd tests/load && make run

.PHONY: chaos-test
chaos-test: ## Run chaos tests
	cd tests/chaos && make run

# Development
.PHONY: dev
dev: ## Run services in development mode with hot reload
	@echo "Starting development environment..."
	make infra-up
	@for service in control-plane gateway hot-tier stream-ingester query-api; do \
		echo "Starting $$service in dev mode..."; \
		cd services/$$service && make dev & \
		cd ../..; \
	done
	@wait

.PHONY: proto-gen
proto-gen: ## Generate protobuf code
	cd shared/proto && make generate

.PHONY: mock-gen
mock-gen: ## Generate mocks
	@for service in services/*/; do \
		echo "Generating mocks for $$service..."; \
		cd $$service && make mock-gen && cd ../..; \
	done

# Docker
.PHONY: docker-build-all
docker-build-all: ## Build Docker images for all services
	@for service in services/*/; do \
		echo "Building Docker image for $$service..."; \
		cd $$service && make docker-build && cd ../..; \
	done

.PHONY: docker-push-all
docker-push-all: ## Push Docker images for all services
	@for service in services/*/; do \
		echo "Pushing Docker image for $$service..."; \
		cd $$service && make docker-push && cd ../..; \
	done

# Kubernetes
.PHONY: k8s-deploy
k8s-deploy: ## Deploy to Kubernetes
	cd infrastructure/kubernetes && make deploy

.PHONY: k8s-delete
k8s-delete: ## Delete from Kubernetes
	cd infrastructure/kubernetes && make delete

# Utilities
.PHONY: fmt
fmt: ## Format all Go code
	@for service in services/*/; do \
		echo "Formatting $$service..."; \
		cd $$service && go fmt ./... && cd ../..; \
	done

.PHONY: lint
lint: ## Lint all Go code
	@for service in services/*/; do \
		echo "Linting $$service..."; \
		cd $$service && golangci-lint run && cd ../..; \
	done

.PHONY: clean
clean: ## Clean build artifacts
	@for service in services/*/; do \
		echo "Cleaning $$service..."; \
		cd $$service && make clean && cd ../..; \
	done

.PHONY: deps
deps: ## Download all dependencies
	@for service in services/*/; do \
		echo "Downloading dependencies for $$service..."; \
		cd $$service && go mod download && cd ../..; \
	done

# Monitoring
.PHONY: metrics
metrics: ## Show metrics endpoints
	@echo "Service metrics endpoints:"
	@echo "  Control Plane: http://localhost:8001/metrics"
	@echo "  Gateway:       http://localhost:8080/metrics"
	@echo "  Hot Tier:      http://localhost:8090/metrics"
	@echo "  Query API:     http://localhost:8081/metrics"
	@echo "  Ingester:      http://localhost:8082/metrics"

.PHONY: logs
logs: ## Tail logs from all services
	docker-compose -f infrastructure/docker/docker-compose.yml logs -f

# Quick commands for development
.PHONY: up
up: infra-up migrate-all build-all run-all ## Start everything

.PHONY: down
down: ## Stop everything
	@pkill -f "services/" || true
	make infra-down

.PHONY: restart
restart: down up ## Restart everything

.PHONY: status
status: ## Check status of all components
	@echo "Checking infrastructure..."
	@docker-compose -f infrastructure/docker/docker-compose.yml ps
	@echo "\nChecking services..."
	@ps aux | grep -E "services/.*/cmd" | grep -v grep || echo "No services running"