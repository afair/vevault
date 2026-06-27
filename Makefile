.PHONY: build install test clean fmt lint

# Go binary. Override if not in make's PATH:
#   make GO=/path/to/go build
GO ?= $(shell command -v go 2>/dev/null \
	|| ls -d $(HOME)/.local/share/mise/installs/go/*/bin/go 2>/dev/null | head -1 \
	|| ls -d $(HOME)/.asdf/shims/go 2>/dev/null \
	|| echo go)

BINARY  := vv
CMD_DIR := ./cmd/vv
PREFIX  ?= /usr/local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.Version=$(VERSION)"

build:
	$(GO) build $(LDFLAGS) -o $(BINARY) $(CMD_DIR)

install:
	@test -f $(BINARY) || { echo "Binary not found. Run: make build"; exit 1; }
	install -d $(PREFIX)/bin
	install -m 755 $(BINARY) $(PREFIX)/bin

test:
	$(GO) test ./...

clean:
	rm -f $(BINARY)
	$(GO) clean ./...

fmt:
	$(GO) fmt ./...

lint:
	golangci-lint run ./...

# Cross-compile for all targets.
release:
	GOOS=linux   GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BINARY)-linux-amd64   $(CMD_DIR)
	GOOS=linux   GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BINARY)-linux-arm64   $(CMD_DIR)
	GOOS=darwin  GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BINARY)-darwin-amd64  $(CMD_DIR)
	GOOS=darwin  GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BINARY)-darwin-arm64  $(CMD_DIR)
	GOOS=freebsd GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BINARY)-freebsd-amd64 $(CMD_DIR)