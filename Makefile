BIN     := interlock
GO      ?= go
GOFLAGS ?= -trimpath

.PHONY: all build go web test lint fmt run dev docs docs-dev clean

## all: build the binary with the web interface embedded
all: build

## build: build the web interface, then the Go binary
build: web go

## go: build the Go binary (embeds whatever is in web/dist)
go:
	$(GO) build $(GOFLAGS) -o bin/$(BIN) ./cmd/interlock

## web: build the web interface into web/dist
web: web/node_modules
	cd web && npm run build && touch dist/.gitkeep

web/node_modules: web/package.json web/package-lock.json
	cd web && npm ci
	@touch web/node_modules

## test: run the Go tests
test:
	$(GO) test -race ./...

## lint: run golangci-lint and the TypeScript type check
lint:
	golangci-lint run
	@if [ -d web/node_modules ]; then cd web && npm run typecheck; else echo "web/node_modules missing; skipping typecheck (run make web)"; fi

## fmt: format Go and apply lint fixes
fmt:
	golangci-lint fmt

## run: run the server from source
run:
	$(GO) run ./cmd/interlock

## dev: run the Vite dev server (proxies /api to a running Go server on :8080)
dev: web/node_modules
	cd web && npm run dev

## docs: build the documentation site into docs/.vitepress/dist
docs: docs/node_modules
	cd docs && npm run build

## docs-dev: serve the documentation site with live reload
docs-dev: docs/node_modules
	cd docs && npm run dev

docs/node_modules: docs/package.json docs/package-lock.json
	cd docs && npm ci
	@touch docs/node_modules

## clean: remove build output
clean:
	rm -rf bin web/dist/* docs/.vitepress/dist docs/.vitepress/cache
	@touch web/dist/.gitkeep

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | column -t -s ':'
