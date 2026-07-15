# Implement the Full Local REST API

Work in `/Users/marcus/code/tasks` on the current branch.

This is an immediate implementation work order. Do the work now. Do not capture
it in `tasks.jsonl`, create backlog tasks instead of implementing it, or stop at
a plan or scaffold.

## Objective

Implement the complete first local HTTP API slice defined by the repository's
current API snapshot. The result must be a working loopback-only Rack 3 API
served by Puma, backed by the same task files and `Tasks::Application` semantics
as the CLI and TUI.

“Complete” means the CRUD and health routes work through the real entrypoint,
the HTTP contract and security rules are enforced, cross-process behavior is
proved, documentation is current, and a separate final review has been
completed with its actionable findings fixed.

## Read First

Treat these as authoritative, in this order:

1. `AGENTS.md` and the injected repository instructions.
2. `docs/api/openapi.yaml` for paths, payloads, headers, status codes, and error
   codes.
3. `docs/plans/http-api-and-web-foundation.md` for first-slice scope, security,
   testing, and explicit later work.
4. `docs/adr/0005-application-boundary.md` through
   `docs/adr/0008-delete-semantics.md` for the accepted architectural decisions.
5. Existing application, Store, query, command, and test code. Trace the real
   call paths before changing them.

If two current documents disagree, resolve the discrepancy in the documents
before implementing that behavior. Do not silently choose one interpretation.

## Required Scope

Add the production runtime and adapter, including:

```text
config.ru
bin/tasks-api
lib/tasks/api/app.rb
lib/tasks/api/representation.rb
lib/tasks/api/errors.rb
```

Use other narrowly scoped files where they make the design clearer. Implement
these routes exactly:

| Method | Path | Requirement |
|---|---|---|
| `GET` | `/healthz` | Process liveness without touching the Store. |
| `GET` | `/readyz` | Safe readiness from the coherent checked-read boundary. |
| `GET` | `/api/v1/meta` | Capabilities and the global store revision; no paths. |
| `GET` | `/api/v1/sections` | Ordered live section resources. |
| `GET` | `/api/v1/tasks` | Ordered task resources with every documented filter. |
| `POST` | `/api/v1/tasks` | Atomic creation with `201`, `Location`, and `ETag`. |
| `GET` | `/api/v1/tasks/{id}` | Stable-id lookup in the exact requested source. |
| `PATCH` | `/api/v1/tasks/{id}` | Atomic changeset with required `If-Match`. |
| `DELETE` | `/api/v1/tasks/{id}` | Undoable live deletion with explicit cascade. |

Do not implement the later manager surface (`views`, history, archive sweep,
SSE, or static-client hosting) in this task. Do not enable non-loopback serving,
authentication, multi-user storage, or remote deployment. Those remain separate
decisions even though parts of the broader v1 contract describe them.

## Architecture Rules

- Keep HTTP as a thin adapter over `Tasks::Application`. Route code may parse
  transport input, create `OperationContext`, enforce HTTP policy, and map typed
  results. It must not shell out to `bin/tasks`, fuzzy-resolve titles, edit JSONL,
  or call Store mutation methods directly.
- Resolve task and archive paths once through `Tasks::Config.resolve`, using the
  same configuration as the CLI and TUI. Never expose those paths in responses
  or logs.
- Use the checked application read methods for task lists, task lookup,
  sections, meta, and readiness. Resource data and `store_revision` must come
  from the same captured snapshot.
- Preserve Store locking, atomic writes, post-write validation, rollback,
  recurrence, nesting, cascade rules, and the shared undo journal.
- Keep the global store revision separate from task revisions. The former is a
  refresh token; the latter is the `ETag`/`If-Match` write precondition.
- Keep the existing CLI and TUI behavior stable. Rack, Puma, and API test
  dependencies must not enter their boot paths or the core runtime.
- Keep `openapi_first` in the test toolchain. Runtime request handling remains
  ordinary Rack code.

## HTTP and Security Behavior

- Bind to `127.0.0.1` by default and refuse unsupported non-loopback binds.
- Enforce expected Host values and reject forwarded-host ambiguity.
- Reject untrusted mutation Origins and emit no wildcard CORS headers.
- Accept bounded `application/json` bodies only. Use a named, documented body
  limit and return the contract's error for oversized payloads.
- Reject malformed JSON, invalid media types, malformed ids, invalid values,
  unknown body fields, and unknown query keys with the documented status and
  machine error code. `openapi_first` does not reject extra query keys, so add
  an explicit per-operation query allowlist.
- Require `If-Match` for PATCH and DELETE. Return `428` when it is missing and
  `412` with the current safe resource when it is stale.
- Return no-op PATCH as `200` without adding a journal entry.
- Generate a request id for every request. Log safe structured
  method/route/status/duration data without task bodies, raw query contents,
  configured paths, journal locations, exception messages, or backtraces.
- Convert unexpected failures to safe error envelopes. Do not leak internal
  exception text.

## Representations and Contract

- Build an HTTP-specific representation mapper. Do not expand or redefine
  `TaskView#to_h` to match the wire format.
- Map contexts, ordinary tags, the own hold marker, recurrence, tree counts,
  archive state, availability, links, and dates exactly as OpenAPI specifies.
- Never expose line numbers, headlines, persistence records, ancestor/child id
  internals, section titles used only for presentation, or filesystem details.
- Validate route-produced requests and responses against
  `docs/api/openapi.yaml`, not only hand-written expected hashes.
- Keep the OpenAPI document and operator/development documentation synchronized
  with the implemented behavior.

## Required Proof

Add in-process tests for every implemented route and all documented success and
failure behavior, including headers, envelopes, content types, filters,
source-exact lookup, body limits, unknown fields, Host/Origin policy, readiness,
and safe exception mapping.

Add black-box tests that start the real `bin/tasks-api`/Puma entrypoint on an
ephemeral port with sandbox task files and prove:

- clean startup and shutdown;
- CLI capture racing API mutation leaves every successful write intact and the
  store structurally valid;
- a task loaded over HTTP becomes stale after a fresh CLI process changes it,
  and the old ETag is refused;
- an API mutation can be undone by a fresh CLI process, restoring the exact
  prior bytes and resource;
- live-only and archive-only external changes advance the global refresh token
  and subsequent reads return fresh data; and
- an invalid external edit causes safe read and mutation refusal without being
  overwritten or leaking a path.

Extend isolated boot tests to cover the real `bin/tasks-api` entrypoint while
retaining proof that `bin/tasks`, `bin/tasks-tui`, `lib/tui/app`, and
`ruby test/all.rb` do not load Rack or Puma.

## Work and Commit Discipline

- Inspect the worktree first and preserve unrelated user changes.
- Establish the existing green baseline before implementation.
- Organize the work into small, coherent, independently tested commits. Commit
  as you go; do not leave the whole API in one final commit.
- Do not hand-edit `tasks.jsonl` or `archive.jsonl`. All tests must use sandbox
  files.
- Do not stop after unit tests if the real Puma entrypoint or cross-process
  behavior has not been exercised.
- Do not push or open a pull request unless explicitly asked.

Run at least these final gates:

```sh
bundle check
bundle exec ruby test/api/all.rb
ruby test/all.rb
bin/tasks check
git diff --check
```

## Final Review

Only after implementation and all required proof are complete, request one
fresh independent review of the entire API change. Do not use that reviewer for
implementation and do not request piecemeal reviews earlier.

Give the reviewer the base commit, the complete diff, the authoritative docs,
and the test commands. Ask for an adversarial review focused on:

- contract fidelity and missing route/error cases;
- concurrency, revision, rollback, and cross-process correctness;
- Host, Origin, content-type, body-limit, logging, and information-leak risks;
- preservation of the Store/Application boundary and CLI/TUI dependency
  isolation; and
- test gaps, brittle tests, lifecycle cleanup, and production-entrypoint
  behavior.

Wait for the review. Address every actionable finding, add regression tests for
each bug fixed, rerun the full gates, and commit the review fixes separately.
Record any finding you reject with concrete code or test evidence rather than
dismissing it silently.

## Completion Report

Finish with:

- the implemented route and security surface;
- the commits created;
- exact test commands and final counts;
- the independent review findings and how each was resolved;
- any intentionally deferred manager or remote work; and
- confirmation that the worktree is clean.

Do not claim completion while a required route, black-box proof, review finding,
or repository gate remains outstanding.
