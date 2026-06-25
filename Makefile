BINARY_NAME=nexus
MODULE=github.com/vishnuvardhansankoti/ai-nexus
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
VERSION=v0.0.0-dev

.PHONY: build test lint generate clean

build:
	go build -ldflags "-X 'main.Version=$(VERSION)' -X 'main.Commit=$(COMMIT)'" -o bin/$(BINARY_NAME) ./cmd/nexus

test:
	go test ./...

lint:
	golangci-lint run ./...

generate:
	go generate ./...

clean:
	rm -rf bin/
