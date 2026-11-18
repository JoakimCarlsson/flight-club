ifeq ($(OS),Windows_NT)
EXT := .exe
else
EXT :=
endif

GOBIN := $(subst \,/,$(shell go env GOPATH))/bin

ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: fmt lint tools run build web-build

GOPKGS := $(shell go list -f '{{.Dir}}' ./...)

fmt:
	$(GOBIN)/golines -m 80 --base-formatter=$(GOBIN)/goimports -w $(GOPKGS)
	cd web && bun run check

lint:
	go vet ./...
	$(GOBIN)/golangci-lint run ./...
	cd web && bun run lint

tools:
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/golangci/golines@v0.15.0
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
	go install github.com/air-verse/air@latest

web-build:
	cd web && bun install --frozen-lockfile && bun run build

build: web-build
	go build -o bin/api$(EXT) ./cmd/api

AIR := $(GOBIN)/air --build.cmd "go build -o ./tmp/api$(EXT) ./cmd/api" --build.bin "./tmp/api$(EXT)"

run:
	@trap 'kill 0' INT TERM EXIT; \
	$(AIR) & \
	(cd web && bun install && bun run dev) & \
	wait
