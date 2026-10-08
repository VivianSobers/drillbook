GO ?= go
BIN := bin/drillbook
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/VivianSobers/drillbook/internal/cli.Version=$(VERSION)

.PHONY: build test lint role-test chart-test env-up env-down
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/drillbook

test:
	$(GO) test ./...

lint:
	$(GO) vet ./...
	golangci-lint run ./...
	ansible-lint ansible/collections/ansible_collections/drillbook/faults tests/roles env/ansible

role-test:
	tests/roles/run.sh

chart-test:
	tests/chart/run.sh

env-up:
	env/up.sh

env-down:
	env/down.sh
