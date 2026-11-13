SHELL := /bin/bash
GO    ?= go
NPM   ?= npm
BIN   := bin

.PHONY: all build server web test vet race run dev-web clean

all: build

# Build the React app into internal/webui/dist, then the Go binaries that
# embed it. Run `make server` to skip the web build (the committed dist is
# used as-is).
build: web server

server:
	$(GO) build -o $(BIN)/meridian-server ./cmd/meridian-server
	$(GO) build -o $(BIN)/meridian-tui ./cmd/meridian-tui
	$(GO) build -o $(BIN)/meridian-stress ./cmd/meridian-stress

web:
	$(NPM) --prefix web install --no-audit --no-fund
	$(NPM) --prefix web run build

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

race:
	$(GO) test -race ./internal/arbiter ./internal/balancer ./internal/ml

# Serve real llama-server workers. Set MODELS_DIR and LLAMA_BIN for your host.
MODELS_DIR ?= ~/models
LLAMA_BIN  ?= llama-server
run: build
	./$(BIN)/meridian-server serve --enable-llm=true \
		--models-dir $(MODELS_DIR) --llama-bin $(LLAMA_BIN)

# Vite dev server on :5173, proxying /v1 and /ml to :8080.
dev-web:
	$(NPM) --prefix web run dev

clean:
	rm -rf $(BIN)
