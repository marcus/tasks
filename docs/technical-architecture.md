# Technical architecture

Tasks is one Go core with three adapters: CLI, TUI, and loopback HTTP. The task
files are the source of truth; no surface owns a private database or privileged
business logic.

## Layers

```text
cmd/tasks        cmd/tasks-tui        cmd/tasks-api
       \              |              /
                internal/application
                         |
                  internal/store
       record · check · journal · merge · atomic
                         |
             tasks.jsonl / archive.jsonl
```

- `internal/application` exposes typed commands, checked reads, and immutable
  snapshots shared by every surface.
- `internal/store` owns file access, locking, semantic mutations, rollback, and
  history integration.
- `internal/record` parses and canonically emits ordered JSON records.
- `internal/check` enforces schema-v2, stable IDs, parent integrity, field
  vocabularies, and DFS pre-order.
- `internal/api` translates the shared application contract to OpenAPI v1.
- `internal/tui` is a Bubble Tea client of the same application boundary.

## Persistence

Each line of `tasks.jsonl` or `archive.jsonl` is one complete record. Tree
relationships use stable `parent` IDs; record order is strict DFS pre-order.
Canonical writers preserve fixed key ordering and omit absent fields so a task
usually produces one reviewable Git diff line.

Mutations write a sibling temporary file, flush it, atomically rename it over
the target, and flush the parent directory. Symlinks resolve to their target and
target permissions are preserved. The installed bytes are parsed and checked;
any failed invariant triggers rollback.

The shared content-addressed journal lives under
`$XDG_STATE_HOME/tasks/journal/`, keyed by canonical task-file path. CLI, TUI,
API, and agent-driven changes participate in the same undo/redo history.

## Concurrency

Writers serialize through an exclusive store lock and re-read under that lock
before applying a command. Snapshot reads take a shared lock. Acquire waits
at most 5s, then refuses with the live holder's pid and timestamp rather than
blocking. The sidecar file is diagnostic only; flock is the lock. Application
mutations carry expected values or revisions so stale decisions refuse rather
than overwrite newer data. HTTP mutations expose the same rule through quoted
ETags and `If-Match`.

Snapshots deep-copy mutable data and remain coherent while background writes
occur. Readers see a complete old or new file, never a torn intermediate state.

## Multi-device merge

`tasks install-merge-driver` configures Git to call the internal
`tasks merge-driver` plumbing command. It aligns records by stable ID, merges
independent fields, unions tags, uses `updated` stamps for genuine same-field
conflicts, and preserves valid tree order.

Malformed, unsupported-schema, or structurally invalid inputs refuse. The
driver leaves both sides inside ordinary conflict markers and records its
decision in `.tasks-merge.log`, so Git cannot silently stage a partial result.

## HTTP boundary

`tasks-api` binds only to `127.0.0.1` and validates Host, Origin, content type,
body size, and conditional-write headers. It deliberately has no remote/auth
mode. The complete wire contract is [`api/openapi.yaml`](api/openapi.yaml).

## Agent adapters

`internal/llm` isolates harness/provider differences. CLI and TUI agent paths
assemble the same embedded list-agent contract, current environment facts,
absolute executable/data paths, and optional task-set memory. Agents perform
task operations through the installed CLI; no provider receives a private data
mutation path.
