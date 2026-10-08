GO ?= go
BIN := bin/drillbook

.PHONY: build test lint
build:
	$(GO) build -o $(BIN) ./cmd/drillbook

test:
	$(GO) test ./...

lint:
	$(GO) vet ./...
