.PHONY: build install test clean fmt lint

BINARY  := vv
CMD_DIR := ./cmd/vv
PREFIX  ?= /usr/local

build:
	go build -o $(BINARY) $(CMD_DIR)

install: build
	install -d $(PREFIX)/bin
	install -m 755 $(BINARY) $(PREFIX)/bin

test:
	go test ./...

clean:
	rm -f $(BINARY)
	go clean ./...

fmt:
	go fmt ./...

lint:
	golangci-lint run ./...

# Cross-compile for all targets.
release:
	GOOS=linux   GOARCH=amd64 go build -o $(BINARY)-linux-amd64   $(CMD_DIR)
	GOOS=linux   GOARCH=arm64 go build -o $(BINARY)-linux-arm64   $(CMD_DIR)
	GOOS=darwin  GOARCH=amd64 go build -o $(BINARY)-darwin-amd64  $(CMD_DIR)
	GOOS=darwin  GOARCH=arm64 go build -o $(BINARY)-darwin-arm64  $(CMD_DIR)
	GOOS=freebsd GOARCH=amd64 go build -o $(BINARY)-freebsd-amd64 $(CMD_DIR)