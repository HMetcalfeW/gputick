BINARY  := gputick
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.DEFAULT_GOAL := build

.PHONY: build test cover lint fmt tidy clean

build:
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) ./cmd/gputick

test:
	go test -race ./...

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR) coverage.out
