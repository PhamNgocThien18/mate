MODULE := github.com/nguyenngocanh94/mate
GO ?= go
GOFMT ?= gofmt
BIN ?= bin/mate

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X $(MODULE)/internal/config.Version=$(VERSION) \
	-X $(MODULE)/internal/config.Commit=$(COMMIT) \
	-X $(MODULE)/internal/config.BuildDate=$(BUILD_DATE)

# dash rejects `set -o pipefail`; keep bash for the test pipe.
SHELL := /bin/bash

.PHONY: all build test test-race vet fmt check-fmt check clean

all: check build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/mate

# gotestreport prints every skip and fails on any skip not declared in
# scripts/gotestreport/expected-skips.txt. Live Herdr/harness tests run
# only with MATE_LIVE=1.
test:
	set -o pipefail; $(GO) test -json ./... | $(GO) run ./scripts/gotestreport

test-race:
	set -o pipefail; $(GO) test -race -json ./... | $(GO) run ./scripts/gotestreport

vet:
	$(GO) vet ./...

fmt:
	$(GOFMT) -w cmd internal scripts assets

check-fmt:
	@unformatted=$$($(GOFMT) -l cmd internal scripts assets); \
	if [ -n "$$unformatted" ]; then \
		echo "files not formatted with gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

check: check-fmt vet vet-linux test

.PHONY: vet-linux check-linux

# vet-linux compiles every package, tests included, for both Linux
# architectures. check-linux only runs the host's architecture. ./... does
# not reach a nested module, so each module directory is listed here.
VET_LINUX_MODULES = .

vet-linux:
	@set -e; for dir in $(VET_LINUX_MODULES); do for arch in amd64 arm64; do \
		echo "GOOS=linux GOARCH=$$arch $(GO) vet ./... in $$dir"; \
		(cd $$dir && GOOS=linux GOARCH=$$arch $(GO) vet ./...); \
	done; done

# check-linux runs `make check` in a Linux container. Keep LINUX_GO_IMAGE on
# the Go release the host runs. The module and build caches live in their own
# Docker volumes, so a second run downloads nothing. The container runs as the
# caller, so files the tests leave in the repo are not owned by root; a fresh
# volume belongs to root, so a root helper hands it to the caller first. HOME
# is an empty tmpfs outside /tmp: tests treat a home under the temp directory
# as a lab home, not the operator's.
DOCKER ?= docker
LINUX_GO_IMAGE ?= golang:1.27
LINUX_GOMODCACHE_VOLUME ?= mate-linux-gomodcache
LINUX_GOCACHE_VOLUME ?= mate-linux-gocache
LINUX_CACHE_MOUNTS = -v $(LINUX_GOMODCACHE_VOLUME):/cache/gomod -v $(LINUX_GOCACHE_VOLUME):/cache/gobuild

check-linux:
	@command -v $(DOCKER) >/dev/null 2>&1 || { echo "docker not found; make check-linux needs Docker"; exit 1; }
	$(DOCKER) run --rm $(LINUX_CACHE_MOUNTS) $(LINUX_GO_IMAGE) \
		chown $$(id -u):$$(id -g) /cache/gomod /cache/gobuild
	$(DOCKER) run --rm --init -u $$(id -u):$$(id -g) \
		-v "$(CURDIR)":/src -w /src $(LINUX_CACHE_MOUNTS) \
		--mount type=tmpfs,destination=/home/mate,tmpfs-mode=1777 \
		-e HOME=/home/mate -e GOMODCACHE=/cache/gomod -e GOCACHE=/cache/gobuild \
		-e GOTOOLCHAIN=local -e GOFLAGS=-buildvcs=false \
		$(LINUX_GO_IMAGE) make check

clean:
	rm -rf bin coverage.out coverage.html skip-report.md skip-report-race.md
