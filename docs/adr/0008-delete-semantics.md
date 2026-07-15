# ADR-0008: DELETE is an undoable, guarded hard delete of live tasks

Status: Accepted and implemented

Date: 2026-07-14

Implementation note: implemented as `Tasks::DeleteTask` +
`Tasks::Store#delete_task!`, the CLI `tasks delete` command
(`docs/cli-spec.md`), and `DELETE /api/v1/tasks/{id}` in the loopback HTTP
adapter. All three surfaces share the same guarded, journaled Store mutation.

## Context

"CRUD" over the task system needs a delete, but the existing lifecycle already
has `CANCELLED` and archival for closing and filing work. Overloading the word
"delete" to mean "cancel" would be a lasting source of confusion. The one
genuinely new operation is permanent removal of a task the user should never
have created — a mistake, not a completed or abandoned piece of work.

Deletion also touches structure: a task may have descendants, and removing a
parent must not silently lose or reparent hidden work. The archive is history;
deletion must not rewrite it. And the API must not become the only route to a
new domain mutation.

## Decision drivers

- Keep `delete` and `cancel` as distinct, non-overloaded operations.
- Never lose hidden descendant work to an unguarded parent delete.
- Never silently reparent or hoist children.
- Keep the archive read-only.
- Refuse to operate on a structurally invalid backing file rather than "repair"
  it by deletion.
- Ship the behavior on the CLI before exposing it over HTTP.

## Considered options

1. No `DELETE`; map it to `PATCH state=CANCELLED`. Rejected: overloads delete
   with cancel semantics and still leaves no way to remove a genuine mistake.
2. A soft delete / trash bin. Rejected for v1: adds a second lifecycle state and
   storage surface for a single-user local list that already has an undo
   journal.
3. An undoable hard delete from the live file, guarded for descendants.

## Decision

Choose option 3. `DELETE /tasks/{id}` (and `tasks delete <ref>`) is an undoable
hard delete of a task's subtree from the live file. It is not an alias for
`CANCELLED` and it never touches `archive.jsonl`.

- A leaf task deletes directly with a matching revision.
- A task that still has descendants is refused (`409` / `conflict` over HTTP;
  exit 1 on the CLI) unless the caller explicitly opts in with `cascade=true`
  (`--cascade`). The refusal carries the descendant and open-descendant counts.
- A cascading delete removes the contiguous subtree as one journal entry, so a
  single `undo` restores it exactly.
- Deletion never hoists or reparents children.
- Archived tasks are read-only and are not deletable in v1. An archived-only id
  is simply not found in the live namespace (`404` over HTTP; `not_found` /
  exit 2 on the CLI).
- A section id is rejected: delete targets tasks.
- Deletion is never a repair route. Because it gets no repair mode, any
  preflight `Tasks::Check` failure refuses outright and writes nothing, rather
  than deleting a record to make an invalid file parse.
- `If-Match` is required over HTTP and, when supplied, is checked against all
  three revision components. The guard trips on the task's own fields, subtree
  structure (descendant add/remove/move), descendant lifecycle changes
  (state/dates/defer/recur), and sibling identity changes; descendant or
  sibling scalar edits (title, priority, body) sit outside every fingerprint
  and do not trip it — the precise scope and its rationale live in ADR-0007.
  On the CLI a nil expected revision skips the concurrency check as a
  convenience.
- The delete is undoable through the shared journal (`tasks undo`, TUI, and
  across CLI runs), restoring the exact prior bytes.

CLI parity shipped first: `tasks delete` and its `docs/cli-spec.md` entry exist
before the HTTP verb, so the API is not the only route to this domain mutation.

## Consequences

Users get a real "remove a mistake" operation without confusing it with
cancellation or archival, and every deletion is reversible and structurally
safe. Parents cannot be emptied silently: hidden descendants force an explicit
cascade, and that cascade is one undoable journal entry. The archive stays
authoritative history, and an invalid backing file is refused rather than
mutated. Cancellation/archival remains the usual path; `delete` is reserved for
genuine mistakes.
