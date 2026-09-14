GO ?= go
BIN := bin
DIST := dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w

.PHONY: all build server agent run tidy fmt vet test clean dist

all: build

build: server agent

server:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/cnc-server ./cmd/server

agent:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/cnc-agent ./cmd/agent

# Cross-compiled agent binaries for the machines under management.
dist:
	@mkdir -p $(DIST)
	GOOS=linux  GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST)/cnc-agent-linux-amd64  ./cmd/agent
	GOOS=linux  GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST)/cnc-agent-linux-arm64  ./cmd/agent
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST)/cnc-agent-darwin-arm64 ./cmd/agent
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST)/cnc-agent-darwin-amd64 ./cmd/agent
	GOOS=linux  GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST)/cnc-server-linux-amd64 ./cmd/server

# Run the server locally over plain HTTP for development.
run: server
	CNC_ADMIN_USER=admin CNC_ADMIN_PASSWORD=changeme \
		$(BIN)/cnc-server -addr 127.0.0.1:8080 -data ./data -insecure-cookie

tidy:
	$(GO) mod tidy

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

clean:
	rm -rf $(BIN) $(DIST)
