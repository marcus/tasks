# HTTP API current-state snapshot

Status: approved with three focused prerequisites

Date: 2026-07-15

## Review outcome

The architecture is ready for a loopback REST API once the read boundary is
made as coherent and typed as the write boundary. The original plan's Store,
stable-id, query, application, changeset, capture, and delete phases are already
implemented; they are not repeated here as future work.

Do not do another broad CLI/TUI-to-Application migration before starting HTTP
work. Task CRUD is already transport-independent. The remaining direct Store
uses are the TUI's intentional long-lived editor/history/archive seams and the
CLI/TUI archive sweep path, which belongs to the later manager-support slice.

The review decision is **approved with conditions**: complete the three
prerequisites below, then build the CRUD adapter. None requires a new database,
web framework, authentication system, or redesign of `Tasks::Store`.

## Current boundary

The codebase currently provides:

- `Tasks::Store` as the correctness boundary for JSONL parsing, the sidecar
  lock, atomic writes, post-write checking, rollback, and the shared journal;
- immutable `Store::ReadSnapshot`s and canonical `TaskView`/`SectionView`
  resources built by `TaskQueries`;
- `Tasks::Application` over a fresh-Store-per-operation `StoreFactory`;
- typed, one-transaction `CreateTask`, `TaskChangeset`, `TaskPatch`, and
  `DeleteTask` commands with one `MutationResult` vocabulary;
- stable task ids and opaque composite task revisions suitable for ETag/
  `If-Match` concurrency;
- CLI/TUI coverage for atomic create, update, guarded cascade delete, undo, and
  cross-surface visibility;
- accepted ADRs for the application boundary, Rack/Puma transport, revision
  semantics, and hard delete;
- an OpenAPI 3.1 contract at `docs/api/openapi.yaml`, including effective task
  availability; and
- isolated boot tests proving the core CLI, TUI, and application layer do not
  load Rack or Puma.

There is no HTTP runtime yet: no Gemfile, Rack application, Puma launcher,
route code, HTTP representation mapper, or API integration test suite exists.

## Prerequisites before route implementation

### P1. Add a checked, revision-bearing application read result

This is the only substantial foundation gap.

The OpenAPI contract puts `meta.store_revision` on every successful response
and maps a structurally invalid store to `503 store_invalid`. Current
application reads return a `TaskQueryResult`, `TaskView`, or section array
without the change token from the exact snapshot that produced those resources.
`Store#read_snapshot` also parses coherent bytes but does not expose a typed
structural-check outcome. Computing either value in a later Rack call would
create a race and would make the HTTP adapter understand persistence details.

Add one immutable, transport-neutral read result or query snapshot that:

- captures live and archive bytes under the existing Store lock;
- validates those exact captured records rather than checking the path in a
  separate read;
- carries an opaque global `store_revision` derived from the captured content;
- exposes list, exact-source get, section, and safe meta/readiness results from
  that same snapshot;
- distinguishes `ok`, `not_found`, `store_invalid`, and `unavailable` without
  exposing paths or backtraces; and
- keeps existing CLI/TUI query methods compatible while the HTTP adapter uses
  the stronger result.

The global revision covers both live and archive content. It is a refresh token,
not a task-write precondition. Tests must prove that the resource data and token
cannot straddle an external write, that an archive-only change advances the
token, and that invalid captured records produce the typed refusal.

This same seam should power `/meta` and readiness. Do not implement readiness by
calling `Tasks::Check.check(path)` and then performing a second unlocked read.

### P2. Close the two query-contract deltas

The OpenAPI list contract has a single-state filter, while `TaskFilter` currently
derives states only from `scope`. Add an optional validated state to
`TaskFilter`, intersect it with the scope, and cover empty intersections such as
`scope=open&state=DONE`.

Also make task lookup source-exact at the application boundary. The contract's
`source=archive` means archive only; a boolean `include_archive` lookup that
searches live and archive together is too implicit for that promise. Preserve
the current convenience method if CLI callers need it, but give the HTTP
adapter an unambiguous `live` or `archive` query.

The existing availability filter already rejects unavailable-only queries
outside the open scope. HTTP parsing should map that typed validation failure to
the contract's `422 validation_failed` rather than reimplementing the rule.

### P3. Prove the dependency and OpenAPI validation toolchain

Before locking gems, run a small compatibility spike against the actual
`docs/api/openapi.yaml`:

- choose supported Rack 3 and Puma versions for Ruby 3.4 and boot a minimal app
  through `config.ru` under `Rack::Lint`;
- choose a validator that demonstrably supports the OpenAPI 3.1 and JSON Schema
  constructs used by this document, including nullable union types and local
  references;
- validate the complete contract plus every embedded request/response example;
- verify strict unknown query/body-field behavior can be enabled or implemented
  explicitly; and
- only then commit `Gemfile` and `Gemfile.lock`.

Do not assume that a tool describing support for "OpenAPI 3" implements all
OpenAPI 3.1/JSON Schema 2020-12 behavior. The selected validator is a test
dependency; runtime request handling remains ordinary Rack code.

## Outstanding work

| Order | Slice | Scope | Blocks CRUD routes? |
|---:|---|---|---|
| 1 | Coherent API read result | checked snapshot, global revision, typed read failures, safe meta/readiness | Yes |
| 2 | Query parity | single-state filtering and exact-source lookup | Yes |
| 3 | Dependency spike | Rack/Puma boot and real OpenAPI 3.1 example validation | Yes |
| 4 | Core HTTP adapter | runtime, routing, representations, security guards, CRUD | — |
| 5 | Black-box proof | cross-process concurrency, stale writes, undo, invalid-store refusal | — |
| 6 | Manager support | named views, history, archive, polling/SSE, static client | No |
| 7 | Remote deployment | auth, authorization, TLS, rate limits, persistence review | No |

The first three are small enough to land as separate reviewed changes. After
they pass the core test gate, API construction can begin without another
architecture phase.

## CRUD adapter slice

### Runtime and dependency boundary

Add:

```text
Gemfile
Gemfile.lock
config.ru
bin/tasks-api
lib/tasks/api/app.rb
lib/tasks/api/representation.rb
lib/tasks/api/errors.rb
```

Keep routing in a small Rack application served by a non-clustered Puma process.
`bin/tasks-api` resolves `Tasks::Config` once, defaults to `127.0.0.1`, prints
only safe source labels, and fails clearly when API gems are unavailable. Rack,
Puma, and validator dependencies must remain outside the normal CLI/TUI boot
paths and `ruby test/all.rb`.

The representation mapper is intentionally HTTP-specific. It converts the
canonical task view to the accepted schema by:

- separating ordinary tags, contexts, and the own indefinite-hold marker;
- mapping `recur` to `recurrence`;
- deriving `depth`, `archived`, `child_count`, and `descendant_count`; and
- omitting `headline`, `ancestor_ids`, `child_ids`, `section_title`, physical
  lines, and filesystem data.

Do not expand `TaskView#to_h` into the HTTP contract and thereby change the
existing CLI/TUI representation.

### Routes in the first slice

Implement only the resource and health surface needed for complete CRUD:

| Method | Path | Behavior |
|---|---|---|
| `GET` | `/healthz/live` | Process liveness only. |
| `GET` | `/healthz/ready` | Safe checked-read readiness summary. |
| `GET` | `/api/v1/meta` | Capabilities and global store revision; never paths. |
| `GET` | `/api/v1/sections` | Ordered live placement resources. |
| `GET` | `/api/v1/tasks` | Ordered filtered resources. |
| `POST` | `/api/v1/tasks` | Atomic create; `201`, `Location`, and ETag. |
| `GET` | `/api/v1/tasks/{id}` | Exact live or requested archive resource. |
| `PATCH` | `/api/v1/tasks/{id}` | Atomic changeset with required `If-Match`. |
| `DELETE` | `/api/v1/tasks/{id}` | Undoable live delete; explicit cascade for descendants. |

The OpenAPI document is authoritative for fields, examples, status codes, and
machine error codes. Route code parses transport input, creates an
`OperationContext`, calls `Tasks::Application`, and maps the typed result. It
must not call CLI functions, fuzzy-resolve titles, or mutate Store directly.

### HTTP concurrency

- Set each task response's ETag from its opaque task revision.
- Require `If-Match` for PATCH and DELETE; missing is `428`, stale is `412`.
- Return a no-op PATCH as `200` without creating a journal entry.
- Return the same-snapshot global revision in response metadata.
- Keep task revisions and the global store revision distinct: the former is a
  write precondition, the latter is an invalidation token.

### Local security

Loopback is local, not trusted. The first adapter must:

- bind only to `127.0.0.1` by default;
- enforce expected Host values and reject forwarded-host ambiguity;
- accept mutation bodies only as bounded `application/json`;
- reject untrusted browser mutation Origins;
- send no wildcard CORS headers;
- reject unknown request fields;
- emit request ids and safe structured method/route/status/duration logs; and
- never return task bodies in logs, configured paths, journal paths,
  backtraces, or raw exception messages.

The accepted local mode assumes a single-user machine. Non-loopback startup
remains unsupported until the separate remote design exists.

## Required proof for CRUD

### In-process contract tests

- Every route, content type, query/body validation rule, status, header, and
  machine error code.
- Request and response validation against OpenAPI, including all examples.
- Exact source lookup, unavailable/state filters, unknown fields, malformed
  ids, body limits, Host/Origin guards, and safe exception mapping.
- HTTP representation fixtures proving no CLI-only or persistence fields leak.

### Cross-process tests

- Start the real Puma entry point on an ephemeral port and stop it cleanly.
- Race CLI capture with API create/update and prove all successful writes
  survive `Tasks::Check`.
- Load through HTTP, mutate through a fresh CLI process, and reject the old
  ETag.
- Mutate through HTTP, undo through a fresh CLI process, and verify the exact
  bytes/resource return.
- Modify live or archive data externally and prove both refresh-token behavior
  and fresh reads.
- Introduce an invalid external edit and prove reads/mutations refuse without
  overwriting it or leaking a path.

### Repository gates

```sh
ruby test/all.rb
bundle exec ruby test/api/all.rb
bin/tasks check
git diff --check
```

The API suite must use sandbox files. Add isolated real-entrypoint tests for
`bin/tasks`, `bin/tasks-tui`, `lib/tui/app`, and `bin/tasks-api` so the
stdlib/web dependency boundary is proved at the processes users launch.

## Later manager support

After CRUD is proven, extend the application and HTTP adapter with:

1. named views needed by the web manager, including any missing projects view;
2. typed history preview plus revision-guarded undo/redo;
3. typed archive preview/sweep, replacing the remaining direct adapter calls;
4. conditional polling on `/meta`;
5. SSE invalidation only after a Puma thread-budget test; and
6. same-origin static client hosting and operator documentation.

These are not prerequisites for the first API. In particular, do not block CRUD
on a frontend framework, SSE, service management, or a complete browser task
manager.

## Remote deployment remains a separate decision

Do not advertise or enable non-loopback mode by configuration alone. A remote
deployment needs its own threat model and decisions for identity,
authorization, per-user data, TLS/proxy trust, audit retention, backups, rate
limits, and whether JSONL plus file locking still satisfies availability and
multi-user requirements. Persistence can change behind `Tasks::Application`
without changing the v1 resource contract.

## CRUD acceptance criteria

- The three prerequisites above are implemented and reviewed.
- `bin/tasks-api` starts on loopback over the same resolved files as CLI/TUI.
- Every implemented request and response validates against OpenAPI 3.1.
- Stable ids are the only HTTP locators; no route shells out to `bin/tasks`.
- GET data and `store_revision` come from one checked snapshot.
- Create, PATCH, and DELETE retain one transaction, one Check, one journal
  entry, rollback, recurrence, nesting, and cascade behavior.
- PATCH/DELETE enforce ETags without weakening the TUI's field-level conflict
  behavior.
- CLI/API concurrency, cross-process undo, external refresh, and invalid-store
  refusal have black-box proof.
- Core CLI/TUI startup and `ruby test/all.rb` remain free of web dependencies.
