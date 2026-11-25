BINARY  := iadu-sim
MODULE  := github.com/zoneBen/longuv
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(MODULE)/internal/buildinfo.Version=$(VERSION) \
	-X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(MODULE)/internal/buildinfo.Date=$(DATE)

.PHONY: build test vet race run linux snapshot clean fmt

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/iadu-sim

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

run:
	go run ./cmd/iadu-sim --dir ./.iadu-sim

linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 ./cmd/iadu-sim

snapshot:
	goreleaser release --snapshot --clean

fmt:
	gofmt -w cmd internal

clean:
	rm -rf bin dist
