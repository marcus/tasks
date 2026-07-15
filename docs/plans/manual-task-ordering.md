# Manual Task And Subtask Ordering

Status: implemented

Date: 2026-07-15

Architecture review outcome: implemented as approved; conditions satisfied

The placement contract and anchor-relative concurrency semantics in this plan
are recorded in OpenAPI and ADR-0009. Ordering is implemented as a shared
application capability; the HTTP, CLI, and TUI adapters do not splice JSONL
records or reconstruct Store rules themselves.

## Goal

Let a human using an HTML client drag a task or task subtree to an exact place
among its siblings, and let a TUI user do the same with org-style structure
keys. The same operation must support:

- moving a task before another task under the same parent;
- moving a whole subtree to a different task or section at a chosen position;
- placing a task first or last among its siblings; and
- reordering top-level tasks inside a section.

The operation must preserve the JSONL DFS pre-order invariant, stable ids,
maximum-depth rules, cycle protection, atomic writes, rollback, undo/redo, and
optimistic HTTP concurrency.

Section reordering is not part of this feature. The moving resource is always a
task, although its destination parent may be a task or a section.

## Current Behavior

The current model already has a canonical display order. `tasks.jsonl` is stored
in DFS pre-order, and every subtree occupies one contiguous record span.
Sibling order is the order in which sibling subtrees appear in the file. There
is no persisted `position` field.

The existing surfaces expose only coarse placement:

- `PATCH /api/v1/tasks/{id}` maps `parent_id` to the `location` field in a
  `Tasks::TaskChangeset`;
- `Store#patch_location` removes the source subtree, changes its root parent,
  and inserts it at `subtree_end(destination_parent)`, making it the last child;
- when the supplied parent is already the task's parent, the operation is a
  no-op unless an internal caller sets `force`;
- `tasks move` can move to a section, nest under a task, or unnest to the
  enclosing section, but it has no before/after ordering command; and
- `GET /api/v1/tasks` returns task resources in DFS order, while
  `GET /api/v1/sections` returns sections in display order.

This means the API can reparent a subtree but cannot express an arbitrary
sibling position. An HTML client cannot implement reliable manual ordering by
calling the current endpoint repeatedly.

## Product Decisions

### Record Order Remains Canonical

Do not add a `position`, rank, fractional index, or ordering key to task
records. The physical DFS sequence already carries this information, and every
Store mutation rewrites the checked file atomically. A second ordering value
would create two sources of truth and require migration and repair rules.

The collection order returned by the API remains presentation order. Physical
line numbers stay private and unstable.

### Extend Task PATCH

Manual placement changes the task resource's structural location, so extend
`PATCH /api/v1/tasks/{id}` instead of adding an action-style `/move` endpoint.
The operation is idempotent: applying the same placement twice produces the
same tree and the second request is a no-op.

Add a compound `placement` member to `PatchTaskRequest`:

```http
PATCH /api/v1/tasks/aaaa1111
If-Match: "v1.opaque-revision"
Content-Type: application/json

{
  "placement": {
    "parent_id": "bbbb2222",
    "before_id": "cccc3333"
  }
}
```

`parent_id` is required inside `placement` and identifies the destination task
or section. `before_id` is optional and identifies a direct child task of that
parent. Omitting `before_id` means append as the last child.

Only a before-anchor is needed. A client that wants to place a task after a
sibling sends the next sibling as `before_id`, or omits the anchor when the task
belongs at the end. This keeps the command vocabulary small and gives one
unambiguous insertion rule.

The existing top-level `parent_id` PATCH field remains supported for backward
compatibility:

- a task id nests and appends as the last child;
- a section id moves and appends at the top level of that section; and
- `null` retains its current meaning of unnesting into the task's current
  enclosing section.

`placement` and the legacy top-level `parent_id` field are mutually exclusive
in one request. New HTML clients should use `placement` because it names an
explicit destination section and position.

### Use Stable Anchors Instead Of Ordinals

Do not accept `position: 3`, a JSONL line, or a filtered-list index. Numeric
positions change whenever a sibling is inserted, hidden, completed, archived,
or moved. They are especially unsafe when a browser is showing only open or
available work.

`before_id` is a stable semantic anchor. The Store resolves it while holding the
same lock used for the write and verifies that it is still a direct child of
`parent_id`.

### Move The Whole Subtree

Placement always carries the target task and all descendants. Moving only the
root record would violate DFS order and leave descendants attached to a task in
another part of the file.

When `before_id` is present, the moving subtree is inserted immediately before
the anchor's root record. When it is omitted, the subtree is inserted at the
end of the destination parent's subtree. The root task's `parent` changes to
`parent_id`; descendant parent ids do not change.

### Reorder Only From Structural Views

The first HTML interface should enable drag ordering only in a complete live
outline, not in agenda, next, quadrants, search results, or another filtered
view. Hidden siblings make an ordinal drag result surprising even when the
server applies it correctly.

The client can build the initial structural view with the current API:

1. request `GET /api/v1/sections`;
2. request `GET /api/v1/tasks?scope=all`;
3. keep resources whose `source` is `live`; and
4. preserve the returned task sequence while grouping by `section_id` and
   `parent_id`.

No new tree endpoint is required for the first slice. If a later interface must
interleave nested sections and tasks as peer rows, add an ordered mixed-child
representation then. That is separate from task ordering.

## Placement Rules

The Store validates all placement inputs under its mutation lock:

1. The moving id must resolve to one live task.
2. `parent_id` must resolve to one live task or section.
3. `before_id`, when present, must resolve to a live task.
4. The destination parent and anchor cannot equal the moving task or be inside
   its subtree.
5. The anchor's direct parent must be `parent_id`.
6. Nesting under a task must satisfy the existing `max_depth` calculation using
   the moving subtree's full height.
7. The final record sequence must pass `Tasks::Check` before the mutation is
   considered successful.

A placement that already describes the task's exact slot returns success with
no file write and no undo entry. This includes a task that is already directly
before the anchor and a last child placed at the end again. No-op detection
runs only after every validation rule above passes, so a placement with an
invalid anchor or parent is refused even when the task already sits where the
request would have put it.

The Store must remove the moving span before it locates the final insertion
index. This avoids stale array indexes when the source precedes the destination
and makes it impossible to insert relative to a descendant that was part of the
removed span.

Validation precedence is contractual. Resolve the three ids first (`404` for a
missing live resource), then check whether the resolved parent or anchor is the
moving task or lies inside its subtree (`409 cycle`), and only then check the
anchor's direct parent (`409 conflict`). Thus a descendant anchor is always a
cycle even though it also cannot be a direct child of an external destination;
`conflict` is reserved for an ordinary anchor outside the moving subtree whose
live parent no longer matches `parent_id`.

## HTTP Contract

### Request Schema

Add this object to `docs/api/openapi.yaml`:

```yaml
TaskPlacement:
  type: object
  required: [parent_id]
  additionalProperties: false
  properties:
    parent_id:
      $ref: "#/components/schemas/TaskId"
    before_id:
      oneOf:
        - $ref: "#/components/schemas/TaskId"
        - type: "null"
```

Add `placement` to `PatchTaskRequest`. A JSON `null` `before_id` is equivalent
to omitting it and means append. `placement` itself cannot be null.

The successful response remains the ordinary updated `Task` resource with its
new ETag and the coherent post-write `store_revision`. No position number is
added to the resource.

### Status Mapping

Keep existing error vocabulary where it already fits:

| Condition | HTTP result |
|---|---|
| Moving task, parent, or anchor is absent from the live store | `404 not_found`, with the failing field in details |
| Parent or anchor equals the moving task or is inside its subtree | `409 cycle` |
| Anchor outside the moving subtree is not a direct child of `parent_id` | `409 conflict`, with current anchor parent when available |
| Result would exceed `max_depth` | `409 too_deep` |
| Both `placement` and legacy `parent_id` are supplied | `422 validation_failed` |
| Placement object is malformed or contains unknown fields | `422 validation_failed` |
| Subject task revision is stale (`own` component for placement; `own` and `location` for legacy `parent_id`) | `412 stale_revision` |
| `If-Match` is absent | `428 missing_precondition` |
| Store is invalid or unavailable | existing `503` mapping |

Use the normal request body, origin, content-type, and size protections. This
endpoint does not need transport-specific security behavior.

## Concurrency Decision

`If-Match` remains required and carries the moving task's opaque revision, but
a `placement` change compares only the `own` component of that revision. The
accepted fingerprint amendment in ADR-0009 removes `location` from the values
hashed into `own`; structural location lives only in the separate `location`
component. Placement does not compare that component. Its concurrency is
uniformly anchor-relative, for same-parent reorders and cross-parent moves
alike:

- resolve `parent_id` and `before_id` from the live records under the Store lock;
- proceed when the anchor is still a direct child of that parent;
- reject when the anchor was deleted or moved elsewhere; and
- tolerate unrelated inserts, removals, and reorders in both the source and
  destination sibling lists, because "before this stable sibling under this
  parent" keeps a precise meaning through them.

The location fingerprint cannot serve placement. It hashes the moving task's
current parent's full ordered sibling id list (`location_fingerprint` in
`lib/tasks/store.rb`), so comparing it would make every successful reorder
invalidate the cached revision of every other sibling — a client would hit
`412` on the second of two consecutive drags in one section — and an unrelated
CLI capture in the source parent would refuse a move it cannot affect. That
brittleness is exactly what this feature must avoid. Comparing `own` keeps
`If-Match` meaningful (the task content the user grabbed is the content being
moved) without coupling placement to sibling churn; the structural intent is
instead validated fresh under the mutation lock by the parent, anchor,
cycle, and depth rules above.

Accepted trade-off: two clients concurrently placing the same task resolve
last-writer-wins rather than `412`, matching the store's documented
last-writer-wins-plus-check-rollback model. A strict mode — a destination or
global `store_revision` precondition — can be added later without changing the
request shape; the first slice does not need it.

Legacy top-level `parent_id` moves keep their current behavior unchanged: they
continue to compare the location-free `own` component plus the separate
`location` component.

Add an ADR for anchor-relative placement. It must correct the statement in
ADR-0007 that a move revision guards destination siblings — the location
fingerprint actually captures the source parent's sibling list at snapshot
time — and record that placement supersedes the location comparison with the
stable-anchor guard, while legacy location values retain it. The opaque
revision string format does not change; only the component selection in
`changeset_revision_error` becomes placement-aware.

## Shared Application Design

### Typed Placement Value

Introduce an immutable transport-neutral value, tentatively
`Tasks::TaskPlacement`, with:

- `parent_id`;
- optional `before_id`; and
- validation-friendly accessors with defensive immutable copies.

The API adapter converts the JSON object to this value. The CLI adapter builds
the same value from resolved stable ids. Neither adapter calculates record
indexes.

Extend `TaskChangeset` so its existing `location` field accepts either:

- the legacy stable parent id;
- `TaskChangeset::UNNEST`; or
- a `TaskPlacement`.

This keeps placement in `TaskChangeset::FIELD_ORDER`, so a PATCH that combines
title edits and placement remains one checked, atomic, undoable write.

### Store Algorithm

Replace the append-only branch inside `patch_location` with a shared placement
helper:

1. Resolve the target, destination parent, and optional anchor from fresh
   records under lock.
2. Compute the source subtree span with `subtree_end`.
3. Validate cycle and maximum depth against the full span.
4. Copy and remove the span from a detached records array.
5. Resolve the destination again in that detached array.
6. Use the anchor index when supplied; otherwise use
   `subtree_end(destination_parent)`.
7. Update only the moved root's parent id and splice the full span.
8. Return every moved task id in DFS order and a summary containing old parent,
   new parent, and anchor.

The existing Store transaction continues to own serialization, atomic write,
post-write checking, rollback, journal recording, and the coherent post-write
snapshot.

One trap to design out explicitly: `patch_location` today returns an early
same-parent no-op whenever `rec["parent"] == parent_id` (unless `force`). That
branch must apply only to legacy append-style location values. A placement
under the task's current parent with a different anchor is a real reorder;
placement no-op detection compares the final slot — same parent and already
occupying the requested position — never the parent id alone.

### Revision Fingerprints

Keep the opaque revision shape `v1.<own>.<location>.<lifecycle>`, but amend the
fingerprint composition so structural location is represented once, not in
both `own` and `location`. Today `task_revision` hashes every
`EditSnapshot::FIELDS` entry into `own`, and that list includes `location`.
Phase 1 must introduce an explicit revision-own field set equivalent to
`EditSnapshot::FIELDS - [:location]`; `state` and every other existing own
field remain included. The separate `location` fingerprint remains unchanged:
it covers the current parent, that parent's ordered sibling ids, and the moving
subtree's structure. Legacy location writes continue to compare `own` plus
`location`; placement compares only the location-free `own` component and then
validates its live structural anchors under the Store lock.

The string shape and `v1` prefix do not change because clients treat the value
as opaque, but deployment changes the computed `own` digest for existing tasks.
There is no persisted-data migration. An `If-Match` cached before the upgrade
will conservatively receive one `412 stale_revision` even when the task did not
change; the response supplies the current resource/ETag for retry. A client
must invalidate cached task resources and refetch after reconnecting to a
restarted/upgraded local API instead of relying only on `store_revision`, which
may remain unchanged because the JSONL bytes did not change. Once refreshed,
legacy and placement component selection behaves as specified above.

A structural reorder still changes affected tasks' location components, so
refreshed resources reflect sibling changes without exposing order internals to
clients. The implementation change is the revision-own field set plus
placement-aware component selection in `changeset_revision_error`.

Add focused tests for which resources change revision after same-parent and
cross-parent moves, and for what a placement precondition tolerates: a client
must be able to perform several consecutive drags in one section from a single
collection fetch, and an unrelated sibling capture must not `412` a pending
placement. A concurrent edit to the moving task's own fields must still `412`
it. Do not make ordinary title edits conflict on unrelated ordering changes.

## CLI Parity

Add CLI access over the same application command:

```text
tasks move <ref> --before <ref>
tasks move <ref> --under <parent-ref> --before <sibling-ref>
tasks move <ref> <section> --before <sibling-ref>
```

`--before` by itself infers the anchor's current parent. When combined with
`--under` or a section destination, the resolved anchor must be a direct child
of that destination. Existing `--under`, `--top`, and positional section moves
continue to append as they do now.

`--before` cannot be combined with `--top`. Without `--before`, the existing
destination grammar remains unchanged and appends exactly as it does today.
With `--before`, zero or one explicit destination is allowed: no explicit
destination infers `parent_id` from the anchor's current direct parent;
`--under` supplies a task parent; and the positional form supplies a section
parent. A missing value for `--before`, more than one explicit destination, a
self-anchor, or an anchor that is not a direct child of the explicit
destination is a usage/domain error (exit 1). Source, parent, and anchor task
refs use the normal exact-id/line/fuzzy resolution and keep exit 2 for no match
or ambiguity; a missing positional section remains exit 1.

The three new `--before` forms always resolve a non-null anchor and support the
existing `--dry-run` and `--json` conventions. Successful human output prints a
placement summary followed by the moved task's standard post-write headline.
The summary names the task and destination and ends with `before "<anchor>"`;
a no-op prints the same current-state summary and headline without creating an
undo entry. `--dry-run` prefixes the summary with `would`, prints the current
headline, and writes nothing; as with the existing mutation commands,
`--dry-run` takes precedence over `--json` and stays human-readable. Non-dry-run
`--json` keeps the standard `touched` array and adds:

```json
{
  "placement": {
    "parent_id": "bbbb2222",
    "parent_type": "task",
    "parent_title": "Prepare launch",
    "before_id": "cccc3333",
    "before_title": "Send announcement"
  }
}
```

`parent_type` is `task` or `section`; `before_id` and `before_title` are
non-null for every `--before` invocation. Fuzzy refs and section names remain
CLI conveniences; the application receives only stable ids. Existing
positional section, `--under`, and `--top` append/unnest forms continue to build
legacy location values and keep their current human, JSON, and dry-run output.
They do not gain a placement summary or `placement` JSON member in this slice.

## TUI Reordering

The TUI gets keyboard reordering over the same application command. This
resolves the placeholder in `docs/cli-spec.md` that parent/subtree placement is
"pending a dedicated indent/outdent affordance in the tree views." No Store
behavior is added: every keystroke builds one `Tasks::TaskPlacement`, resolves
stable ids from the current snapshot, and submits one changeset — one atomic
write, one journal entry, undoable with the existing `u`.

### Keyboard Shortcuts

The bindings follow org-mode's structure-editing keys — the strongest TUI
precedent for outline reordering, and a natural fit for this app's org
lineage — with vim's shift-width idiom as the indent/outdent ASCII form:

| Key | Action | Precedent |
|---|---|---|
| `Alt+↑` / `Alt+k` | Move the task and its subtree up one slot among its visible siblings | org `M-↑`; `Alt+k` mirrors the `k` selection key |
| `Alt+↓` / `Alt+j` | Move the task and its subtree down one slot among its visible siblings | org `M-↓`; `Alt+j` mirrors `j` |
| `>` | Indent: nest under the preceding sibling, appended as its last child | vim `>>`; org `M-→` |
| `<` | Outdent: move to the parent's level, placed immediately after the old parent | vim `<<`; org `M-←` |

`<`, `>`, and the Alt-modified keys are unbound in the list context today
(`J`/`K` are priority; `h`/`l` are collapse/expand), so nothing in
`lib/tui/shortcuts.rb` moves. All four actions register through the existing
`Shortcuts` registry with palette entries ("Move up", "Move down", "Indent",
"Outdent") so `:` search and the `?` help modal pick them up automatically,
and `Shortcuts.validate!` guards collisions.

Terminals encode Alt-modified keys two ways — xterm-style CSI (`\e[1;3A`) and
ESC-prefixed (`\e\e[A`, `\ek`). Both variants must be registered, and the
ESC-prefixed forms must go through the same atomic sequence reader that
already protects `Shift-Tab`, so a split read cannot decay into a lone
destructive Escape plus a stray letter.

### Placement Mapping

Each action translates to one placement against the current parent's sibling
list as ordered in the snapshot:

- **Up**: `before_id` = the previous sibling under the same parent. Already
  first: no request, footer notice.
- **Down**: `before_id` = the sibling after the next sibling, or append when
  the next sibling is last. Already last: no request, footer notice.
- **Indent**: `parent_id` = the preceding sibling, appended. No preceding
  sibling: unavailable. Over `max_depth`: the refusal surfaces in the footer,
  mirroring the existing over-cap move message.
- **Outdent**: `parent_id` = the grandparent (task or section),
  `before_id` = the old parent's next sibling, or append when the old parent
  is its last child. This lands the task directly after its old parent, org
  style. Already at section level: footer notice, matching `move --top`.

Up and down never cross a parent boundary — org's `M-↑`/`M-↓` semantics.
Moving between parents is what indent, outdent, and the agent prompt are for.

### View Gating

None of the current Agenda, Next, Quadrants, Inbox, or Projects tabs is safe for
reordering. Each one omits live tasks by state, date, availability, or project,
and several regroup or sort rows away from DFS sibling order. Phase 4 therefore
adds one dedicated sixth tab, **Outline**, as the smallest complete structural
surface: it renders every live section and task in canonical DFS order,
including DONE/CANCELLED and unavailable tasks. Sections are non-selectable
structure rows; task rows remain selectable. Collapse may hide descendants,
but it never hides a selected row's direct siblings, so moving a collapsed task
still has the true sibling sequence.

The availability predicate on each ordering shortcut requires the Outline tab,
no active `/` text or `@` context filter, and a selected task row. In every
other tab or while either filter is active, the keys are consumed and the
footer explains that ordering requires the unfiltered Outline tab, rather than
falling through. This gate is part of the contract: the existing five tabs
must not be treated as structural merely because they render tree indentation.

The TUI's mtime-poll refresh needs no special handling: placement's
anchor-relative concurrency means an external write between poll and
keystroke either leaves the resolved anchor valid (the move applies as
intended) or is refused with a visible message, and the 250ms poll then
redraws the current truth.

## Implementation Phases

### Phase 0: Contract And Decision Record

- Add the placement request and examples to OpenAPI.
- Add the anchor-relative concurrency ADR.
- Update `docs/cli-spec.md` with the exact `--before` grammar and errors.
- Add contract-only schema and example tests that pass before the adapter is
  implemented; runtime assertions land with the adapter.

This phase settles compatibility before source changes begin.

### Phase 1: Core Placement Command

- Add `Tasks::TaskPlacement`.
- Extend `TaskChangeset` validation and immutable normalization.
- Implement same-parent and cross-parent ordered splicing in `Tasks::Store`.
- Preserve append-only behavior for legacy location values.
- Add Store and Application tests for success, refusal, no-op, undo, and
  revision behavior.

### Phase 2: API And CLI Adapters

- Parse and validate `placement` in `Tasks::Api::App`.
- Keep `placement` and legacy `parent_id` mutually exclusive.
- Map typed outcomes to the documented HTTP statuses and details.
- Add CLI `--before` resolution, dry-run output, and JSON output.
- Keep both adapters thin over `Tasks::Application`.

### Phase 3: Integration Proof

- Exercise drag-style same-parent and cross-parent sequences through HTTP.
- Prove a CLI change between browser read and PATCH produces the intended
  stale or anchor-relative result.
- Prove undo and redo restore exact record bytes after a subtree reorder.
- Boot the real API and validate emitted traffic against OpenAPI.
- Run the full core and API test gates.

An HTML client may begin after Phase 3. Building that client is outside this
plan.

### Phase 4: TUI Reordering

- Add the complete live **Outline** tab in canonical DFS order; none of the
  existing filtered/sorted tabs is eligible for ordering.
- Register the four shortcuts, palette entries, and availability predicates in
  `lib/tui/shortcuts.rb`.
- Add both Alt-key encodings to the atomic sequence reader.
- Map each action to a `TaskPlacement` over the snapshot's sibling order and
  route it through the shared application command.
- Surface boundary notices and depth/cycle refusals in the footer.
- Update the TUI interaction contract in `docs/cli-spec.md`, replacing the
  "pending a dedicated indent/outdent affordance" placeholder.

Phase 4 depends only on Phase 1 and can proceed in parallel with the HTML
client; it must not begin before the integration proof gates in Phase 3 have
run once against the core placement command.

## Required Tests

### Store And Application

- Move first, middle, and last siblings before another sibling.
- Append a sibling already under the same parent.
- Move a subtree across parents before an existing child.
- Move a subtree to an empty destination.
- Move a nested task to a section and a top-level task under another task.
- Preserve descendant order and parent ids.
- Reject self anchors, descendant anchors, cycles, bad parents, and excessive
  depth without writing; classify a descendant anchor as `cycle` before testing
  its direct parent, while an unrelated wrongly-parented anchor is `conflict`.
- Reject an anchor whose live parent no longer matches the request.
- Treat an already-satisfied placement as a no-op with no journal entry.
- Refuse, not no-op, an invalid anchor even when the task already occupies the
  requested slot.
- Same-parent placement with an anchor performs the reorder rather than taking
  the legacy same-parent no-op branch.
- Undo and redo restore byte-identical files.
- A failed post-write check restores the original bytes.
- A placement succeeds after an unrelated sibling is captured, moved, or
  reordered in the source or destination parent (revision fetched before that
  churn), and consecutive placements in one section work from one snapshot.
- A placement refuses with a stale revision after the moving task's own fields
  change.
- Legacy `parent_id` moves still trip the location precondition on same-parent
  structural changes, and unrelated field edits keep their current merge
  behavior.

### HTTP Adapter

- Accept a valid placement with `If-Match` and return the updated task, ETag,
  and store revision.
- Reject missing, malformed, archived, and wrongly parented anchors with the
  documented status and safe details.
- Reject `placement` plus legacy `parent_id`.
- Reject unknown placement fields.
- Preserve body-size, content-type, Host, and Origin protections.
- Validate every new request and response example against OpenAPI.

### CLI Adapter

- Resolve `--before` to stable ids before mutation.
- Infer the anchor parent when no other destination is supplied.
- Refuse contradictory destinations and ambiguous refs.
- Cover human, JSON, and dry-run output.
- Prove CLI and API placement produce identical JSONL bytes for equivalent
  stable-id commands.

### TUI Adapter

- Up, down, indent, and outdent each produce the documented placement for
  first, middle, and last siblings, including collapsed subtrees.
- Boundary cases (first up, last down, no preceding sibling, section-level
  outdent) send no mutation and show a footer notice.
- Indent past `max_depth` is refused visibly without writing.
- The shortcuts are unavailable in filtered and non-structural views, and the
  keys are consumed rather than falling through.
- Both Alt-key encodings resolve, including across split reads.
- One reorder is one journal entry; `u` restores the prior bytes.
- `Shortcuts.validate!` passes with the new entries.

## Verification Gates

Run these before calling the work complete:

```sh
ruby test/all.rb
bundle exec ruby test/api/all.rb
bin/tasks check
git diff --check
```

The API boundary tests must continue to prove that `bin/tasks`, `bin/tasks-tui`,
and `lib/tui/app` boot without Rack or Puma. HTTP behavior must also be tested
through the real `bin/tasks-api` process so locking and cross-process revisions
are exercised rather than stubbed.

## Acceptance Criteria

The feature is complete when:

- an HTTP client can place a task subtree before a stable sibling or at the end
  of any valid task/section parent;
- a client can perform successive placements from one collection fetch without
  spurious precondition failures, while stale anchors and stale task content
  are still refused;
- same-parent ordering and cross-parent ordering use one atomic Store mutation;
- the resulting file always passes `Tasks::Check` and undo restores exact prior
  bytes;
- stale and invalid placements return documented machine-readable outcomes;
- the CLI exposes equivalent semantics through the shared application layer;
- the TUI reorders, indents, and outdents subtrees from the outline view with
  org-style keys, through the same application command, with visible refusals
  and single-entry undo;
- existing `parent_id` PATCH and `tasks move` callers keep their append behavior;
- OpenAPI, CLI docs, ADRs, and tests agree with the implementation; and
- all core and API gates pass with no Rack/Puma leakage into stdlib surfaces.

## Implementation Evidence

Implemented on `main` in the inclusive range `4effb52^..7410d7e`. Every
implementation task was independently reviewed before closure; cumulative
adversarial review `td-4f2d29` rejected the first complete pass, remediation
`td-bff290` was independently approved, and the repeated audit at `7410d7e`
found no remaining actionable issue.

| Phase | td task | Commits |
|---|---|---|
| Contract and ADR | `td-a5253b` | `4effb52`, `582382a` |
| Typed placement | `td-4897a2` | `3e8f0f5` |
| Store/Application | `td-fd8a7b` | `9f5da12`, `103b7b3` |
| HTTP adapter | `td-246bae` | `d7712d7` |
| CLI adapter | `td-5c412a` | `f229c80`, `e656687` |
| Cross-process proof | `td-0bc53b` | `161ef96` |
| TUI ordering | `td-adb25c` | `3345810` |
| Adversarial remediation | `td-bff290` | `7410d7e` |

Final verification for `td-207991` on 2026-07-15:

- `ruby test/all.rb`: 1,345 runs, 17,096 assertions, zero failures/errors/skips.
- `bundle exec ruby test/api/all.rb`: 31 runs, 882 assertions, zero
  failures/errors/skips.
- `ruby test/test_tasks_require_boundary.rb`: 3 runs, 4 assertions; proves
  `bin/tasks`, `bin/tasks-tui`, and `lib/tui/app` boot without Rack/Puma.
- `bundle exec ruby test/api/test_black_box.rb`: 8 runs, 131 assertions through
  the real `bin/tasks-api` process, including cross-process anchor churn,
  API/CLI byte parity, coherent revisions, and byte-exact undo/redo.
- `ruby test/test_task_placement.rb`: 21 runs, 139 assertions; placement
  validation, no-op/journal behavior, rollback, revision precedence, and exact
  undo/redo bytes.
- `ruby test/test_cli_mutations.rb`: 218 runs, 1,267 assertions; stable-anchor
  resolution and human/JSON/dry-run CLI behavior.
- `ruby test/test_shortcuts.rb`, `ruby test/test_text_input.rb`,
  `ruby test/test_views.rb`, and `ruby test/test_app.rb`: 187 runs and 2,609
  assertions total; shortcut encodings, split input, Outline gating, all four
  structural moves, boundary notices, selection, and single-entry undo.
- A generated isolated JSONL fixture passed
  `TASKS_FILE=$tmp/tasks.jsonl TASKS_ARCHIVE=$tmp/archive.jsonl bin/tasks check`
  with `ok — 1 task parsed, no structural errors`; no configured user task data
  was read or written. `git diff --check` passed and the pre-closure worktree
  was clean.

Adversarial review findings resolved before the final gates included placement
target resolution before stale-own comparison, exact OpenAPI/runtime validation
messages, current ADR revision semantics, the documented sixth Outline view,
complete CLI positional-section guidance, atomic ESC-prefixed Alt parsing, and
first/middle/last TUI placement coverage. The final proof task is `td-207991`;
the parent epic is `td-f62256`.

## Out Of Scope

- section reordering;
- persisting numeric or fractional positions;
- ordering archived tasks;
- drag ordering inside filtered or named views;
- multi-select or bulk subtree movement;
- remote-server authentication and authorization; and
- implementation of the HTML client itself.
