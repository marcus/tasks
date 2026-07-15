# ADR-0007: Composite opaque revisions and HTTP optimistic concurrency

Status: Accepted; ADR-0009 amendment pending implementation

Date: 2026-07-14

Implementation note: revision generation is implemented in `Tasks::Store`
(`task_revision`, `revision_components`, `changeset_revision_error`,
`delete_revision_error`). The loopback API now maps task revisions to quoted
ETag/`If-Match` headers and returns the separate `store_revision` refresh token
from the same coherent snapshot as each resource response. The current
`task_revision` still includes `location` in `own`; ADR-0009's accepted
amendment removes that duplicate structural input when placement is
implemented.

## Context

Stale browser edits must be refused with a machine-readable conflict instead of
overwriting newer CLI or TUI writes. Every task response therefore carries an
opaque `revision`, and the HTTP layer reuses it as the ETag; a write echoes it
back in `If-Match`.

A task's editable identity is not just its own fields. The legacy location
guard for a move depends on the ordered sibling list under the moving task's
source parent at snapshot time, and a cascading delete or a completion depends
on the whole subtree's lifecycle. If the revision were a single hash over own
fields plus location and lifecycle fingerprints, then capturing an unrelated
sibling or completing an unrelated descendant would change the hash, and an
ordinary title edit conditioned on the old value would then fail with a spurious
`412`. That is unacceptably brittle for the common case (a plain field edit).

## Decision drivers

- Clients treat the revision as opaque and compare it only for equality.
- An ordinary field edit must not fail because a sibling or descendant changed.
- A legacy location move must still fail if the source parent's siblings
  changed; a state change or cascading delete must still fail if the affected
  subtree changed. Anchor-relative placement is specified separately in
  ADR-0009.
- The precondition must derive from semantic baselines, never from a JSONL line
  number, mtime alone, or a client-supplied payload.

## Considered options

1. One monolithic digest over baselines plus fingerprints. Simple, but 412s
   spuriously whenever a sibling is captured or a descendant is completed.
2. Separate per-field ETags exposed to clients. Precise, but pushes structural
   knowledge into the client and makes multi-field browser saves incoherent.
3. One opaque revision string that is internally three digests, compared
   piecewise by operation.

## Decision

Choose option 3.

### Revision structure

The revision is the opaque string `v1.<own>.<location>.<lifecycle>`, where each
part is a SHA-256 hex digest:

- `own` — the task's own non-location editable-field baselines
  (`EditSnapshot::FIELDS - [:location]` after the ADR-0009 amendment), with
  dates normalized before hashing so equivalent snapshots never depend on Ruby
  object identity or JSONL serialization. `state` remains included; only
  structural location is excluded.
- `location` — the location fingerprint, including the sibling id list under the
  task's current (source) parent at snapshot time.
- `lifecycle` — the lifecycle fingerprint spanning the whole subtree (state,
  closed, dates, recurrence, defer marker).

The three-part shape is opaque to clients but lets the Store compare only the
parts an operation depends on. OpenAPI names the semantic `own` and `location`
guards to specify operation-level precondition scope, but it does not expose
component positions or digest recipes; clients must not parse the revision.

### Piecewise comparison per operation

- An ordinary field edit checks only `own`.
- A state change additionally checks `lifecycle`.
- A legacy location move additionally checks `location`. Anchor-relative
  `TaskPlacement` instead checks `own` and validates its live parent/anchor
  under the mutation lock, as decided in ADR-0009.
- A cascading delete checks all three (`own`, `location`, `lifecycle`), so a
  changed descendant, sibling, or the task itself refuses a stale cascade.

`changeset_revision_error` selects the required parts from the changeset's
ordered fields; `delete_revision_error` always compares all three.

ADR-0009 changes the digest recipe without changing the opaque three-component
shape. A task ETag cached before that deployment will conservatively fail once
with `412`; clients refetch on API reconnect and retry with the current ETag.
Because the backing JSONL need not change, `store_revision` alone is not a
deployment-version signal.

### HTTP mapping

- Each task response sets `ETag` to the task's `revision`; `PATCH` and `DELETE`
  require `If-Match`.
- A missing `If-Match` is `428` (`missing_precondition`).
- A stale value is `412` (`stale_revision`), with the current resource in
  `details.current` when it is safe to disclose.
- HTTP conflicts are conservative whole-task conflicts even though the TUI keeps
  narrower per-field checks: a multi-field browser save is understood and
  atomic, so any relevant change since the client loaded the task refuses the
  whole save rather than merging.
- `POST` returns the created resource and its revision; retry idempotency keys
  are deferred until the server becomes remote.

### Global change token

List and meta responses expose an opaque global `store_revision`. It is a change
token for refreshing browser queries (poll `/meta`, or later subscribe to
`/events`), not a per-task write precondition.

Undo and redo are preconditioned on `store_revision`, not on a task revision:
`/history/undo` and `/history/redo` require the `store_revision` the client last
saw and return `409` (`conflict`) on mismatch, so a client cannot undo another
surface's newer write. `/history` peeks the next undo/redo labels plus the
`store_revision` they apply to without mutating anything.

## Known limitation accepted for v1

A descendant **title** change does not trip a cascade-delete (or move) `:stale`,
because titles are in no fingerprint: `own` covers only the target task's own
fields, and `location`/`lifecycle` cover structure and lifecycle, not
descendant titles. So a caller can confirm a cascading delete, an unrelated
descendant can be retitled, and the delete still proceeds against the "same"
revision. This is accepted for v1: the guard exists to prevent losing
*structural* or *lifecycle* work (a new descendant, a reparent, a completion),
and a pure retitle of a descendant that is about to be deleted is not lost work
worth a spurious conflict. If this ever matters, add descendant titles to the
lifecycle fingerprint; it is a fingerprint-scope change, not a protocol change.

## Implementation notes: task resource deltas from the current TaskView

The HTTP task resource in `docs/api/openapi.yaml` follows the accepted plan's
representation list. The current `Tasks::TaskView` (`lib/tasks/task_view.rb`)
differs; Phase 4 must reconcile these when building the HTTP representation:

- **Add `depth`** (integer, 0 for a top-level task). Not on `TaskView`; derive
  from the task's ancestry (e.g. `ancestor_ids.length`).
- **Split `deferred` into its own boolean.** `TaskView#deferred?` exists, but
  `to_h` currently emits the `defer` marker inside `tags`. The contract makes
  `deferred` a distinct boolean field and requires `tags` to exclude the defer
  marker.
- **Narrow `tags` to ordinary tags only.** `TaskView#to_h` currently puts
  `@context` tags and the defer marker into `tags` as well as into `contexts`.
  The contract's `tags` excludes both `@contexts` (their own field) and the
  defer marker (now `deferred`).
- **Add `archived`** (boolean). `TaskView` exposes `source` (`live`/`archive`)
  only; derive `archived` from it.
- **Add `descendant_count`** (integer). Not on `TaskView`; derive as the number
  of tasks in the subtree below this one.
- **Add `child_count`** (integer). `TaskView` exposes `child_ids` (an array),
  not a count; the contract exposes the count and omits the id array.
- **Rename `recur` to `recurrence`** in the JSON key.
- **Omit `headline`, `ancestor_ids`, `child_ids`, and `section_title`** from the
  HTTP resource. `TaskView` carries them for TUI presentation; the contract's
  representation list does not include them (the HTTP resource must not inherit
  a pre-rendered ANSI headline or expose internal id arrays).

`id`, `revision`, `source`, `parent_id`, `section_id`, `state`, `priority`,
`title`, `contexts`, `scheduled`, `deadline`, `body`, `closed`, `project`, and
`links` map through unchanged (dates serialized as ISO `YYYY-MM-DD` or null).

## Consequences

The common case — an ordinary field edit — never fails because a neighbor
changed, while moves, state changes, and cascading deletes still refuse when
their wider structural or lifecycle context shifted. Clients keep a single
opaque token per task and a single global token for query refresh, with no
knowledge of the internal digest structure. The accepted descendant-title
limitation is documented and bounded, and closing it later is a fingerprint
change behind the same opaque revision.
