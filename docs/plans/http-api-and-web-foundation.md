# HTTP API current-state snapshot

Status: ready for CRUD adapter implementation

Date: 2026-07-15

## Review outcome

The architecture is ready for a loopback REST API. The checked read boundary,
query-contract parity, and locked Rack/Puma/OpenAPI toolchain were completed and
reviewed on 2026-07-15. The original plan's Store, stable-id, query, application,
changeset, capture, and delete phases are also implemented; none is repeated
here as future work.

**Readiness decision: yes.** No architectural or foundation prerequisite remains
before implementation of the loopback CRUD API described in this document. Work
can start directly with the production Rack adapter and its route-level tests.
Use `docs/plans/http-api-implementation-prompt.md` as the self-contained work
order for that implementation.

“Ready to implement” does not mean “ready to ship.” The adapter, launcher,
security guards, HTTP representations, route contract tests, and cross-process
proof are the implementation itself and remain required before the local API is
complete. Manager features and remote deployment are later scopes, not blockers
for the first local API.

Do not do another broad CLI/TUI-to-Application migration before starting HTTP
work. Task CRUD is already transport-independent. The remaining direct Store
uses are the TUI's intentional long-lived editor/history/archive seams and the
CLI/TUI archive sweep path, which belongs to the later manager-support slice.

The next implementation slice is the production CRUD adapter. It does not
require a new database, web framework, authentication system, or redesign of
`Tasks::Store`.

## Current boundary

The codebase currently provides:

- `Tasks::Store` as the correctness boundary for JSONL parsing, the sidecar
  lock, atomic writes, post-write checking, rollback, and the shared journal;
- immutable `Store::ReadSnapshot`s and canonical `TaskView`/`SectionView`
  resources built by `TaskQueries`;
- a checked, revision-bearing application read result whose resource data,
  live/archive validation, and global refresh token come from one locked
  capture, with typed safe failures for invalid or unavailable stores;
- `Tasks::Application` over a fresh-Store-per-operation `StoreFactory`;
- validated single-state filtering and source-exact live/archive task lookup;
- typed, one-transaction `CreateTask`, `TaskChangeset`, `TaskPatch`, and
  `DeleteTask` commands with one `MutationResult` vocabulary and post-mutation
  global store revisions;
- stable task ids and opaque composite task revisions suitable for ETag/
  `If-Match` concurrency;
- CLI/TUI coverage for atomic create, update, guarded cascade delete, undo, and
  cross-surface visibility;
- accepted ADRs for the application boundary, Rack/Puma transport, revision
  semantics, and hard delete;
- an OpenAPI 3.1 contract at `docs/api/openapi.yaml`, including effective task
  availability;
- a locked Rack 3.2/Puma 8 toolchain plus an `openapi_first` compatibility gate
  that boots a real Puma/Rack::Lint fixture and validates the complete contract
  and every embedded JSON request/response example; and
- isolated boot tests proving the core CLI, TUI, and application layer do not
  load Rack or Puma.

There is no production HTTP adapter yet: `config.ru`, `bin/tasks-api`, route
code, HTTP representation mapping, security guards, and route-level integration
tests are the outstanding first slice. The committed Puma fixture is only a
toolchain proof and must not become the production application by accretion.

## Outstanding work

| Order | Slice | Scope | Required for first local API? |
|---:|---|---|---|
| 1 | Core HTTP adapter | production runtime, routing, representations, security guards, CRUD | Yes |
| 2 | Black-box proof | cross-process concurrency, stale writes, undo, invalid-store refusal | Yes |
| 3 | Manager support | named views, history, archive, polling/SSE, static client | No |
| 4 | Remote deployment | auth, authorization, TLS, rate limits, persistence review | No |

API construction can begin without another architecture phase.

## CRUD adapter slice

### Runtime and dependency boundary

Add the production adapter files:

```text
config.ru
bin/tasks-api
lib/tasks/api/app.rb
lib/tasks/api/representation.rb
lib/tasks/api/errors.rb
```

Use the committed Gemfile and lockfile. Keep routing in a small Rack application
served by a non-clustered Puma process. `bin/tasks-api` resolves `Tasks::Config`
once, defaults to `127.0.0.1`, prints only safe source labels, and fails clearly
when API gems are unavailable. Rack, Puma, and validator dependencies must
remain outside the normal CLI/TUI boot paths and `ruby test/all.rb`.

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
| `GET` | `/healthz` | Process liveness only. |
| `GET` | `/readyz` | Safe checked-read readiness summary. |
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
- reject unknown request fields, including an explicit query-key allowlist
  because the selected validator accepts undocumented query keys;
- emit request ids and safe structured method/route/status/duration logs; and
- never return task bodies in logs, configured paths, journal paths,
  backtraces, or raw exception messages.

The accepted local mode assumes a single-user machine. Non-loopback startup
remains unsupported until the separate remote design exists.

## Required proof for CRUD

### In-process contract tests

- Every route, content type, query/body validation rule, status, header, and
  machine error code.
- Route-produced requests and responses validated against OpenAPI. The existing
  toolchain gate already validates the complete document and all embedded JSON
  examples; route tests must prove the implementation matches it.
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
