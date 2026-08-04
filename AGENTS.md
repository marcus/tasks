# AGENTS.md — Tasks application development

This checkout is the Go implementation of the Tasks CLI, TUI, and loopback API.
Application work belongs in this repository; task-list operations belong in the
separate configured data directory and must go through the installed `tasks`
CLI.

## Contracts

- `AGENTS.md` is for coding agents working on the application.
- `internal/agentcontext/TASK_AGENT.md` is embedded into `tasks -p` and the TUI
  queue for agents managing personal GTD data. Change it only when the
  list-agent contract changes, and keep the Tasks skills synchronized.

## Architecture

- Shared behavior lives under `internal/`, with `internal/application` as the
  command/query boundary and `internal/store` as the persistence boundary.
- `cmd/tasks`, `cmd/tasks-tui`, and `cmd/tasks-api` are thin adapters over the
  shared core. Do not put domain behavior only in a surface.
- CLI and HTTP capabilities have semantic parity by default. CLI fuzzy refs and
  friendly input may differ from HTTP stable IDs, JSON, status codes, and ETag
  mechanics, but the shared outcome must agree.
- Keep provider, renderer, transport, and persistence seams behind interfaces.
- Preserve schema-v2 JSONL, canonical key ordering, DFS pre-order, atomic
  replacement, rollback, and journal semantics unless a separately planned
  migration explicitly changes them.

## Development

- Interface contracts: `docs/cli-spec.md` and `docs/api/openapi.yaml`.
- Storage contract: `docs/conventions.md`.
- Tests: `go test ./...`; focused tests use the normal Go package path.
- Additional gates: `go test -race ./...`, `go vet ./...`, `gofmt -l`, and
  builds of all three commands.
- Use `testdata/fixtures`; never point tests at configured or real task files.
- Every code change requires independent review before completion.

When adding a CLI capability, update the command registry/help JSON, human help,
CLI spec, relevant skill, and adapter tests together. When the capability is
also HTTP-facing, update OpenAPI and API parity coverage.

## Task data safety

Never hand-edit `tasks.jsonl` or `archive.jsonl`. They depend on stable IDs,
fixed key order, DFS pre-order, and a schema header. Use `tasks` for real list
changes and `tasks check --all-files` for diagnostics.

Task data must not be committed here. Location resolution is explicit through
environment overrides or `~/.config/tasks/config`; an unconfigured binary
refuses to read or write.

The retired Ruby implementation is historical only and is preserved by the
annotated tag `ruby-final-2026-08-04`.
