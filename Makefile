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

.PHONY: fmt lint tools run build

GOPKGS := $(shell go list -f '{{.Dir}}' ./...)

fmt:
	$(GOBIN)/golines -m 80 --base-formatter=$(GOBIN)/goimports -w $(GOPKGS)

lint:
	go vet ./...
	$(GOBIN)/golangci-lint run ./...

tools:
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/golangci/golines@v0.15.0
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
	go install github.com/air-verse/air@latest

build:
	go build -o bin/api$(EXT) ./cmd/api

run:
	$(GOBIN)/air --build.cmd "go build -o ./tmp/api$(EXT) ./cmd/api" --build.bin "./tmp/api$(EXT)"
