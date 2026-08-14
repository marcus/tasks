PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS = -s -w -X github.com/marcus/tasks/internal/buildinfo.Version=$(VERSION) -X github.com/marcus/tasks/internal/buildinfo.Commit=$(COMMIT)

release_goals := $(filter check-release-state release release-tap,$(MAKECMDGOALS))
ifneq ($(release_goals),)
ifneq ($(origin RELEASE_VERSION),environment)
$(error set RELEASE_VERSION in the environment, for example: RELEASE_VERSION=v1.0.0 make $(firstword $(release_goals)))
endif
endif

.PHONY: build install install-local install-worktree use-homebrew verify-homebrew install-status test test-race vet fmt fmt-check clean screenshots release-snapshot check-release-state release release-tap

build:
	mkdir -p bin
	go build -ldflags '$(LDFLAGS)' -o bin/tasks ./cmd/tasks
	go build -ldflags '$(LDFLAGS)' -o bin/tasks-api ./cmd/tasks-api
	go build -ldflags '$(LDFLAGS)' -o bin/tasks-tui ./cmd/tasks-tui

install:
	install -d '$(BINDIR)'
	go build -ldflags '$(LDFLAGS)' -o '$(BINDIR)/tasks' ./cmd/tasks
	go build -ldflags '$(LDFLAGS)' -o '$(BINDIR)/tasks-api' ./cmd/tasks-api
	go build -ldflags '$(LDFLAGS)' -o '$(BINDIR)/tasks-tui' ./cmd/tasks-tui

install-local:
	./scripts/dev-install.sh install-local

install-worktree:
	./scripts/dev-install.sh install-worktree

use-homebrew:
	./scripts/dev-install.sh use-homebrew

verify-homebrew:
	./scripts/dev-install.sh verify-homebrew

install-status:
	./scripts/dev-install.sh status

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

fmt-check:
	@test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; exit 1; }

clean:
	rm -rf bin dist

screenshots:
	./scripts/update-screenshots.sh

release-snapshot:
	goreleaser release --snapshot --clean

check-release-state:
	@test -n "$${RELEASE_VERSION:-}" || { echo 'RELEASE_VERSION=vX.Y.Z is required' >&2; exit 2; }
	./scripts/check-release-state.sh pre-tag

release:
	@test -n "$${RELEASE_VERSION:-}" || { echo 'RELEASE_VERSION=vX.Y.Z is required' >&2; exit 2; }
	./scripts/publish-release.sh

release-tap:
	@test -n "$${RELEASE_VERSION:-}" || { echo 'RELEASE_VERSION=vX.Y.Z is required' >&2; exit 2; }
	./scripts/publish-homebrew-tap.sh
