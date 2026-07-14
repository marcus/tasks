# ADR-0005: A typed application boundary over a request-scoped Store

Status: Accepted and implemented

Date: 2026-07-14

## Context

The CLI, TUI, and a future HTTP adapter all need the same task semantics:
filtering, named views, canonical representations, and atomic create/update/
delete. Historically that logic lived as top-level functions in `bin/tasks`, and
`Tasks::Store` returned an inconsistent mix of booleans, symbols, line numbers,
arrays, and structs depending on which mutation was called. An in-process HTTP
adapter cannot safely reuse executable code from `bin/tasks`, and it cannot map
Store return values to HTTP responses without re-deriving domain meaning per
call site.

`Tasks::Store` also carries mutable read caches and reload state. Its file
mutations are serialized by a sidecar `flock`, but the object itself is not a
documented thread-safe shared service, so a long-lived server sharing one Store
instance across Puma threads would let one request's cached reads leak into
another.

## Decision drivers

- One place owns input normalization, command orchestration, stable-id lookup,
  and canonical views, so domain rules cannot drift between interfaces.
- Adapters map results to their own surface (exit codes, TUI messages, HTTP
  status) rather than re-interpreting Store internals.
- A command is one validated, journaled transaction, not a sequence the adapter
  stitches together.
- The read model handed to a long-lived server must not retain a mutable Store
  cache across requests or external writes.

## Considered options

1. Keep query/mutation logic in `bin/tasks` and have each new adapter re-derive
   it. Smallest immediate diff, but guarantees drift and blocks in-process
   reuse.
2. Make `Tasks::Store` a shared, thread-safe service and speak to it directly
   from every adapter. Removes the boundary but commits to hardening the
   Store's caches for concurrency before any measurement shows it is needed,
   and still leaks Store return-value shapes into adapters.
3. Add a persistence-neutral `Tasks::Application` facade over a `StoreFactory`,
   with typed command inputs and one result vocabulary, and build a fresh Store
   per operation.

## Decision

Choose option 3.

`Tasks::Application` (`lib/tasks/application.rb`) is the reusable seam. It
accepts typed Ruby inputs and returns immutable query/view objects and
`MutationResult`s. It deliberately knows nothing about ARGV, terminal rendering,
Rack request objects, or HTTP status codes.

Commands are typed, immutable, transport-neutral inputs, each mapping to exactly
one checked Store transaction and one journal entry:

- `Tasks::CreateTask` (`lib/tasks/create_task.rb`) — the full create attribute
  set, including recurrence and initial notes, as one transaction.
- `Tasks::TaskChangeset` (`lib/tasks/task_changeset.rb`) — an atomic multi-field
  update against one expected task revision, applied in a documented
  deterministic field order (`TaskChangeset::FIELD_ORDER`). `TaskPatch` remains
  a one-field convenience that delegates to the same machinery.
- `Tasks::DeleteTask` (`lib/tasks/delete_task.rb`) — an undoable hard delete with
  a descendant guard.

Every command returns a `Tasks::MutationResult` (`lib/tasks/patch_result.rb`) —
the single result vocabulary. Its statuses are
`ok · no_change · not_found · stale · invalid · conflict · cycle · too_deep ·
store_invalid · unavailable`. The result carries the fresh post-mutation
snapshot, touched ids, structured field/form errors, and a consequence summary.
Adapters translate that one vocabulary into their own surface: `#cli_exit_code`,
`#tui_status`/`#tui_message`, and (in Phase 4) an HTTP status and error code.
The mappings are adapter concerns and never alter `#status`.

Reads go through `TaskQueries` built from an immutable `read_snapshot`. For
presentation adapters that still need the legacy Items plus canonical
`TaskView`s from one coherent read, `Application#read_tasks` returns a frozen
`TaskReadModel`.

The Store is created per operation by `Tasks::StoreFactory`, which owns only
immutable construction settings and returns a new mutable Store from each
`#call`. The existing sidecar file lock still serializes mutations across API
requests, CLI processes, and the TUI, and atomic file replacement means readers
see complete old or complete new bytes. If profiling later shows parsing to be
material, a synchronized immutable snapshot cache can be added behind the
boundary; a shared Store instance is not made thread-safe speculatively.

## Consequences

The CLI, TUI, and HTTP adapter share one definition of every command and view,
and cross-surface parity tests can assert they select and order the same ids.
Adapters stay thin: they normalize input to a typed command and map one result
vocabulary outward. The request-scoped Store keeps the concurrency story simple
— the file lock, not in-process synchronization, is the serialization point —
at the cost of re-parsing small JSONL files per operation, which is acceptable
for local task lists and revisited only if measured.
