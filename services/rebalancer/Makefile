.PHONY: build run test clean dev

# Binary name
BINARY_NAME=rebalancer

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod
GOCLEAN=$(GOCMD) clean
GOGET=$(GOCMD) get

# Build flags
LDFLAGS=-ldflags "-w -s"

build:
	$(GOBUILD) $(LDFLAGS) -o $(BINARY_NAME) cmd/main.go

run: build
	./$(BINARY_NAME)

run-simulate: build
	./$(BINARY_NAME) --simulate --verbose

dev:
	$(GOCMD) run cmd/main.go --simulate --verbose

test:
	$(GOTEST) -v ./...

test-unit:
	$(GOTEST) -v -short ./...

test-coverage:
	$(GOTEST) -v -cover -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f coverage.out coverage.html

deps:
	$(GOMOD) download
	$(GOMOD) tidy

docker-build:
	docker build -t flow-rebalancer:latest .

docker-run:
	docker run -p 8086:8086 flow-rebalancer:latest

fmt:
	gofmt -s -w .

lint:
	golangci-lint run

.DEFAULT_GOAL := build