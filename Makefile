BINARY      := sandcast
PKG         := github.com/rhuss/sandcast
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X $(PKG)/internal/version.Version=$(VERSION)
GO          ?= go
GOLANGCI    ?= golangci-lint
PLATFORMS   := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: all build test lint verify fmt test-integration test-gateway cross clean

all: verify build

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o ./$(BINARY) ./cmd/sandcast

test:
	$(GO) test ./...

lint:
	$(GOLANGCI) run

verify: test lint

fmt:
	$(GO) fmt ./...

test-integration: build
	$(GO) test -tags integration -count=1 -timeout 30m ./tests/integration/...

test-gateway: build
	$(GO) test -tags gateway -count=1 -timeout 30m ./tests/integration/...

cross:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/$(BINARY)-$$os-$$arch ./cmd/sandcast || exit 1; \
	done

clean:
	rm -rf ./$(BINARY) dist/
