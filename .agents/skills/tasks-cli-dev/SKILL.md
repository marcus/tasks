---
name: tasks-cli-dev
description: Add or change Tasks Go CLI commands, shared application behavior, tests, and interface documentation.
---

# Developing Tasks CLI commands

## Boundaries

- `cmd/tasks` owns argument parsing, human/JSON rendering, exit codes, aliases,
  and executable plumbing.
- `internal/application` owns shared commands and checked query results.
- `internal/store` owns locking, mutations, atomic replacement, validation,
  rollback, and journal integration.
- `internal/check` and `internal/record` own schema-v2 validation and canonical
  JSONL shape.
- `internal/api` and `cmd/tasks-api` adapt the same application behavior to the
  OpenAPI contract.

Do not duplicate domain behavior in a command handler or HTTP route. Keep all
tests on temporary stores or `testdata/fixtures`.

## Adding a command

1. Define behavior and structured output in `docs/cli-spec.md`.
2. Add shared application/store behavior where required, including validation,
   atomicity, history, and stale-read protection.
3. Add the handler under `cmd/tasks` and register its canonical spelling and
   aliases in the registry. Update human help and `help --json` metadata.
4. Add focused unit tests plus command-adapter tests for success, invalid input,
   ambiguous/missing refs, JSON output, and file integrity.
5. Update the list-agent skill/contract. If HTTP owns the same capability,
   update OpenAPI, the API adapter, and semantic parity tests.

Every mutating command must prove the resulting record fields, structural
integrity, write refusal on invalid input, and undo/redo behavior where it
records history. Use stable IDs internally; line numbers are display/fuzzy-ref
conveniences only.

## Verification

```sh
go test ./path/to/focused/package
go test ./...
go test -race ./...
go vet ./...
test -z "$(gofmt -l cmd internal)"
```

Build `./cmd/tasks`, `./cmd/tasks-api`, and `./cmd/tasks-tui` after any shared
change. All code requires independent review before completion.
