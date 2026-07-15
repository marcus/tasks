# ADR-0009: Anchor-relative task placement

Status: Accepted

Date: 2026-07-15

## Context

The existing task location mutation can reparent a subtree only by appending it
to a task or section. Its optimistic-concurrency guard compares the moving
task's `location` revision component, which fingerprints the ordered sibling id
list under the task's source parent at snapshot time. That is appropriate for
the legacy append command, but it is too broad for interactive ordering: one
successful reorder changes the location component of every sibling, so the
next drag from the same collection snapshot would fail even when its intended
anchor is still valid.

Manual ordering needs an intent that survives unrelated sibling churn without
accepting stale content or calculating against client-side ordinals.

## Decision drivers

- Preserve one opaque task revision and the existing wire format.
- Let several drag operations proceed from one coherent collection fetch.
- Keep physical lines and numeric positions out of the API.
- Validate structural intent against fresh records while holding the Store
  mutation lock.
- Preserve legacy `parent_id` PATCH and CLI append behavior.

## Considered options

1. Compare the existing `location` component for every ordered placement.
   Rejected because unrelated source-sibling churn and the first reorder make
   later valid placements spuriously stale.
2. Accept a numeric sibling position. Rejected because filters, concurrent
   inserts, and completed or archived siblings make ordinals ambiguous.
3. Name a stable destination parent plus an optional stable before-anchor,
   compare only the moving task's `own` revision component, and validate the
   structural intent under the Store lock.

## Decision

Choose option 3. `TaskPlacement` contains a required stable `parent_id` and an
optional `before_id`. The parent may be a live task or section. A non-null
anchor must be a live task whose direct parent is `parent_id`; omitting the
anchor or sending it as null means append as the last child.

The Store resolves the moving task, parent, and anchor from fresh records while
holding the same lock that protects the checked write. Under that lock it also
validates direct parentage, self/descendant cycles, full-subtree depth, and the
final DFS record sequence. The moving subtree is removed before the insertion
index is resolved, so a source span cannot invalidate an array index or serve
as its own destination. An already-satisfied placement is a successful no-op,
but only after every live validation has passed.

HTTP still requires the moving task's opaque revision in `If-Match`. A
placement compares its `own` component only; it deliberately does not compare
`location`. A concurrent edit to the moving task's own content therefore
returns `412 stale_revision`, while unrelated inserts, removals, and reorders
are tolerated as long as the named parent/anchor relationship still holds. A
missing parent or anchor is `404 not_found`; an anchor that moved under another
parent is `409 conflict`; cycles and excessive depth keep their existing `409`
codes.

Legacy top-level `parent_id` changes are unchanged. They keep append/unnest
semantics and continue to compare both `own` and `location`. The revision
string remains `v1.<own>.<location>.<lifecycle>` and stays opaque to clients;
only component selection in the shared changeset guard becomes aware of the
typed placement value.

## Consequences

Parent-plus-anchor is the concurrency guard for ordered structure, so clients
can perform consecutive drags from one fetch without coupling each write to an
entire sibling list. Two clients that concurrently place the same unchanged
task are last-writer-wins, matching the Store's checked-write model. A stricter
future mode may add a destination or global store precondition without changing
the placement request shape. Existing clients keep their current append
behavior and conflict scope.
