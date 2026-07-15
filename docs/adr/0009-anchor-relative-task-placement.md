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
accepting stale content or calculating against client-side ordinals. The
current `own` digest cannot provide that by component selection alone:
`task_revision` hashes every `EditSnapshot::FIELDS` value, and that list
includes `location`. A cross-parent move therefore changes both `own` and
`location` today.

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
   make `own` content-only by removing `location` from its digest input, compare
   only that component for placement, and validate structural intent under the
   Store lock.

## Decision

Choose option 3. `TaskPlacement` contains a required stable `parent_id` and an
optional `before_id`. The parent may be a live task or section. A non-null
anchor must be a live task whose direct parent is `parent_id`; omitting the
anchor or sending it as null means append as the last child.

The Store resolves the moving task, parent, and anchor from fresh records while
holding the same lock that protects the checked write. Validation order is
part of the decision: after id resolution, self/descendant cycle checks run
before anchor direct-parentage. A descendant anchor is therefore `409 cycle`
even though it is also wrongly parented for an external destination; `409
conflict` applies only to an anchor outside the moving subtree whose live parent
does not match. Depth and final DFS validation follow. The moving subtree is
removed before the insertion index is resolved, so a source span cannot
invalidate an array index or serve as its own destination. An
already-satisfied placement is a successful no-op, but only after every live
validation has passed.

HTTP still requires the moving task's opaque revision in `If-Match`. A
placement compares its `own` component only; it deliberately does not compare
`location`. To make that true for cross-parent placement, revision generation
will hash an explicit own-field set equivalent to `EditSnapshot::FIELDS -
[:location]`. `state` and the other existing own fields remain included. A
concurrent edit to the moving task's own content therefore
returns `412 stale_revision`, while unrelated inserts, removals, and reorders
are tolerated as long as the named parent/anchor relationship still holds. A
missing parent or anchor is `404 not_found`; an anchor that moved under another
parent is `409 conflict` unless it moved inside the subject subtree, which is
`409 cycle`; excessive depth remains `409 too_deep`.

Legacy top-level `parent_id` changes are unchanged. They keep append/unnest
semantics and continue to compare both `own` and `location`; removing location
from the `own` digest does not weaken that separate structural comparison. The
revision string remains `v1.<own>.<location>.<lifecycle>` and stays opaque to
clients. Implementation requires both the revised own-field composition and
placement-aware component selection in the shared changeset guard.

This is a computed-token migration, not a persisted-data migration. Deploying
the new digest composition changes `own` for existing task resources without
changing JSONL bytes or necessarily changing `store_revision`. A pre-deployment
ETag used after upgrade conservatively receives `412 stale_revision`; the
current resource and ETag let the caller refresh and retry. Clients must refetch
task resources after reconnecting to a restarted/upgraded local API rather than
assuming an unchanged `store_revision` preserves cached task ETags. The `v1`
prefix remains because the opaque three-component wire shape is unchanged and
clients never interpret the digest recipe.

## Consequences

Parent-plus-anchor is the concurrency guard for ordered structure, so clients
can perform consecutive drags from one fetch without coupling each write to an
entire sibling list. Two clients that concurrently place the same unchanged
task are last-writer-wins, matching the Store's checked-write model. A stricter
future mode may add a destination or global store precondition without changing
the placement request shape. Existing clients keep their current append
behavior and conflict scope after the one-time ETag refresh at deployment.
