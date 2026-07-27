# Agent task approval queue

Status: accepted implementation contract

Date: 2026-07-27

Architecture: [ADR-0011](../../adr/0011-task-proposals-as-lifecycle-state.md)

## Goal

Let agents record useful suggested tasks without turning them into accepted
commitments or interrupting the owner for permission. The owner reviews those
suggestions in the tasks TUI and accepts or rejects each with one key.

The motivating loop is:

```text
agent: tasks propose "Review recurring subscriptions" --note "Three renewal emails arrived."
owner: open 7 Approvals → inspect if needed → a to accept or r to reject
accepted: PROPOSED → INBOX
rejected: PROPOSED → CANCELLED
```

The feature succeeds when agents confidently create inert proposals, the owner
can see the pending count from any TUI view, and clearing the queue takes only a
few keystrokes.

## Scope

This tranche includes:

- `PROPOSED` as a distinct task lifecycle category;
- typed application operations to propose, approve, and reject;
- CLI commands and JSON output;
- HTTP API parity;
- a seventh TUI Approvals tab with a live count;
- single-key `a` / `r` decisions in that tab and task detail;
- editing a proposal before deciding;
- checked persistence, undo/redo, merge, and archive behavior;
- list-agent guidance explicitly authorizing inert proposals; and
- documentation and tests across every shipped surface.

This tranche does not include:

- automatic execution of accepted work;
- automatic acceptance based on confidence or source;
- bulk approve/reject;
- reminders or notifications;
- multi-reviewer workflows;
- persisted creator/reviewer identity or approval timestamps;
- a generalized approval engine; or
- task assignment/delegation metadata.

Delegation is a deliberate follow-on described under
[Delegation boundary](#delegation-boundary) and specified further in
[Agent task delegation and heartbeat pickup](agent-task-delegation.md).

## Product contract

### Lifecycle categories

The state vocabulary becomes:

| Category | States | Meaning |
|---|---|---|
| Proposed | `PROPOSED` | Suggested but not accepted as the owner's work. |
| Open | `INBOX`, `TODO`, `NEXT`, `WAITING` | Accepted live work. |
| Closed | `DONE`, `CANCELLED` | Finished or rejected/dropped work. |

Code must not implement this as “closed versus everything else.” Introduce
canonical constants or predicates for all three categories and route every
state-sensitive behavior through them.

A proposed task:

- is a live task with a stable id and ordinary editable fields;
- may have notes, links, contexts, tags, dates, priority, and a parent;
- is visible only through proposal-explicit reads and the Approvals view;
- does not appear in Agenda, Next, Quadrants, Inbox, Projects, default Outline,
  default `list`, active task counts, project health, or availability reviews;
- does not make a project active or satisfy “project has a next action”;
- cannot recur or be completed while proposed; and
- is not archived until it has first become terminal.

Dates and priority may be captured on a proposal as suggested metadata, but
they do not promote it to `TODO` or make it active. The existing “dating an
`INBOX` task promotes it” rule remains specific to `INBOX`.

### Creation

Add:

```sh
tasks propose "Review recurring subscriptions"
tasks propose "Review recurring subscriptions" --note "Three renewal emails arrived."
tasks propose "Book lodging" --due 2026-08-15 --project "Trip"
```

`propose` is a thin creation adapter over the same typed `CreateTask` and
`Application#create_task` transaction as `capture`, with forced initial state
`PROPOSED`.

Supported flags should match `capture` where semantically useful:

- `--due`, `--scheduled`, `--priority`;
- repeatable `--tag` and `--context`;
- `--no-host-context`;
- `--project` or `--under`; and
- `--note` for an initial rationale/body entry.

Do not accept `--state`: the command itself owns the proposed state. Do not
accept `--recur`: recurrence describes accepted executable work and may be set
after approval.

Add `--note` to `capture` at the same shared parsing seam if doing so avoids a
proposal-only adapter path. `CreateTask` already supports initial `body` and
`notes`; no storage field is needed. One invocation must create the task and
initial rationale in one write and one undo step.

Human output:

```text
proposed: Review recurring subscriptions [abcd1234]
```

JSON output returns the canonical task resource with `state: "PROPOSED"`.

### Approval

Add:

```sh
tasks approve <ref>
```

Approval is valid only for `PROPOSED` tasks and transitions to `INBOX`.
Preserve title, body, tags, contexts, priority, dates, placement, and children.
Do not run date-driven `INBOX` promotion during this transition: the explicit
post-approval state is always `INBOX`, so the owner retains one normal GTD
processing inbox. Later edits use existing promotion rules.

The mutation is checked, atomic, revision-aware, and undoable. Human output:

```text
approved → INBOX: Review recurring subscriptions
```

Approving a non-proposed task is an invalid semantic request, not a no-op.
Return a clear error naming its current state.

### Rejection

Add:

```sh
tasks reject <ref>
```

Rejection is valid only for `PROPOSED` tasks and transitions to `CANCELLED`,
stamping `closed` through the existing terminal-transition path. It preserves
the task and rationale for undo, review, sync, and later archival.

Human output:

```text
rejected → CANCELLED: Review recurring subscriptions
```

Reject is not hard delete. The existing `undo` immediately restores the
proposal and its place in the queue.

### General state command

`tasks state <ref> PROPOSED` remains possible for repair and explicit manual
transitions. The intent-revealing commands are preferred:

- use `propose` to create;
- use `approve` to accept;
- use `reject` to decline.

Transitions into `PROPOSED` must clear `closed`. Transitions out follow the
ordinary destination rules. Recurring tasks cannot transition to `PROPOSED`
unless recurrence is removed first.

### Reads

Add an explicit collection scope:

```sh
tasks list --proposed
tasks list --proposed --json
```

`--proposed` composes with text, body, context, tag, priority, project, and date
filters. It is mutually exclusive with open/done/archived/all lifecycle scopes.
Default list behavior remains accepted open work only.

`tasks show <ref>` and direct id lookup may resolve a live proposal without
requiring `--proposed`, just as direct inspection is broader than collection
membership. Fuzzy lookup for mutating ordinary open-task commands should not
silently target proposals; `approve`, `reject`, `state`, `show`, `delete`, and
proposal-aware reads explicitly opt in.

Expose canonical vocabularies as:

```json
{
  "states": ["PROPOSED", "INBOX", "TODO", "NEXT", "WAITING", "DONE", "CANCELLED"],
  "proposed_states": ["PROPOSED"],
  "open_states": ["INBOX", "TODO", "NEXT", "WAITING"],
  "closed_states": ["DONE", "CANCELLED"]
}
```

### TUI Approvals view

Add a seventh tab after Outline:

```text
1 Agenda  2 Next  3 Quadrants  4 Inbox  5 Projects  6 Outline  7 Approvals 3
```

The final count treatment should use the theme's badge/count styling and remain
legible in monochrome. Hide the count when zero rather than showing a noisy
zero. The tab remains present when empty so its location and `7` shortcut are
stable.

The Approvals view:

- contains only `PROPOSED` tasks;
- follows canonical DFS order so project/nesting context is stable;
- supports the existing text and context filters;
- uses ordinary row selection, task details, links, and editable right panel;
- shows the proposal rationale/body prominently in details;
- has a clear empty state such as `No tasks pending approval`;
- updates its badge immediately after propose, approve, reject, undo, redo, or
  external store refresh; and
- keeps selection on the nearest remaining proposal after a decision.

The tab strip currently derives labels and hit targets from static `TABS`.
Refactor tab presentation just enough to accept a render-time count while
keeping the canonical tab keys and mouse hit spans shared. Do not create a
second badge geometry calculation in `Frame` or `HitMap`.

At narrow widths, preserve numeric navigation and the active tab. If seven full
labels no longer fit, use the existing clipping behavior if it remains usable;
otherwise introduce one shared compact label variant. Do not let the new count
desynchronize click targets.

### TUI decision keys

In the Approvals view and while its task detail panel is open:

- `a` approves the selected proposal;
- `r` rejects the selected proposal;
- `e` edits through the existing task editor;
- `Return` opens/closes details; and
- `u` / `Ctrl-R` undo and redo decisions.

`a` currently captures into a selected project and `r` edits recurrence. The
shortcut registry already scopes availability by current selection/view. Add
proposal-specific entries whose availability is limited to a selected
`PROPOSED` task. Preserve the existing project-capture and recurrence bindings
outside that context.

Approval is immediate because it is reversible and moves the item only to
`INBOX`. Rejection is also immediate and reversible; do not add a confirmation
modal in the first version. Both actions show the existing transient status
response. The command palette exposes `Approve proposal` and `Reject proposal`
under the same availability rules.

Editing must preserve `PROPOSED` until the owner presses `a`. Save-on-blur must
not accidentally approve or trigger date-driven state promotion.

## Application and persistence design

### Canonical state taxonomy

Define the vocabulary once, close to `Tasks::Store` / `Tasks::Check`, and remove
duplicated assumptions where practical:

```ruby
PROPOSED_STATES = %w[PROPOSED].freeze
OPEN_STATES = %w[INBOX TODO NEXT WAITING].freeze
CLOSED_STATES = %w[DONE CANCELLED].freeze
STATES = (PROPOSED_STATES + OPEN_STATES + CLOSED_STATES).freeze
```

The current `DONE_STATES` name should migrate to `CLOSED_STATES`; retain a
temporary alias only if it keeps a coherent commit green. New code must use the
semantic name. Audit at least:

- store validation and transition stamping;
- `Tasks::Check`;
- task view predicates;
- task queries and collection scopes;
- project rollups and open descendant counts;
- availability;
- recurrence and completion;
- archive eligibility;
- JSONL merge terminal-state handling;
- ref resolution;
- CLI grouping/output;
- API metadata/schema;
- TUI views, counts, editing, and actions; and
- fixtures that assume six states.

Adding an enum value does not change record shape, so do not bump schema
version solely for `PROPOSED`. The implementation must still be released as a
coherent whole because an older binary will reject the new value.

### Typed operations

Keep adapters thin:

- proposal creation uses `CreateTask`;
- approval/rejection use shared application commands or narrowly named
  `Application#approve_task` / `#reject_task` methods backed by the same Store
  state transition primitive;
- the Store owns eligibility checks, stamping, validation, write, and result;
  and
- CLI, API, and TUI translate inputs/outputs only.

Do not implement approval by issuing a CLI subprocess from the TUI or API.

The typed mutation result should identify the old and new state, touched ids,
and canonical post-write resource. This keeps adapter output and later audit
work aligned.

### Notes and provenance

Initial rationale uses the task body. Do not add `created_by`,
`proposal_source`, `proposed_at`, `approved_at`, or `reviewed_by` fields in this
tranche.

`OperationContext` already carries source and optional actor but intentionally
does not persist them. Preserve that seam. If real use demonstrates a need to
answer who proposed or approved an item after archival, design one general
operation journal rather than adding approval-only metadata ad hoc.

### Children and projects

A proposal may be filed under a section/project or another task. Until accepted:

- it does not contribute to project open counts or project health;
- its proposed descendants remain individually reviewable;
- an accepted/open child beneath a proposed parent must not be created through
  normal operations, because it would hide accepted work behind unaccepted
  structure; and
- approval/rejection affects only the selected proposal in v1.

Rejecting or approving a proposal with proposed descendants should therefore
refuse with a clear message directing the owner to decide the children first,
unless current tree invariants make a leaf-only creation rule substantially
simpler. Do not silently cascade approval. Record a follow-up only if real agent
usage needs proposal trees; the expected common case is a leaf proposal.

## HTTP API parity

Update the API alongside the CLI:

- `POST /api/v1/tasks` accepts `state: "PROPOSED"` as the generic creation
  equivalent of `tasks propose`;
- `GET /api/v1/tasks?scope=proposed` lists proposals;
- task resources include the new enum value;
- metadata exposes `proposed_states`;
- `POST /api/v1/tasks/{id}/approve` and `/reject` provide intent-revealing
  action endpoints, or use the existing PATCH action convention if the API
  architecture has standardized on state PATCH by implementation time; and
- optimistic concurrency and origin/host mutation protections remain intact.

Choose one HTTP mutation shape during implementation and record it in OpenAPI
before code. Prefer explicit action endpoints because approval and rejection
carry preconditions beyond arbitrary state replacement and match existing
project action endpoints. Both return the canonical post-write resource and
ETag.

`docs/api/openapi.yaml`, application tests, and adapter parity tests are part of
the same tranche.

## Agent contract

Update `TASK_AGENT.md` and the tasks CLI skill together.

The core instruction:

> You may create an inert `PROPOSED` task without asking first when you identify
> useful follow-up work that is plausibly valuable but the user has not asked
> to add as an accepted task. Use `tasks propose`, include a concise rationale
> when it is not obvious, and do not perform the proposed work. A proposal is
> not permission to create an ordinary task, contact anyone, or take the
> underlying action.

Agents should:

- use `capture` when the user explicitly asks to add/capture/remind;
- use `propose` for agent-initiated suggestions worth the owner's review;
- avoid proposing speculative cleanup with no concrete value;
- consolidate one coherent outcome rather than flooding the queue with tiny
  implementation steps;
- include links/evidence in the body when they motivated the suggestion;
- never self-approve unless the user explicitly asks to accept it; and
- report proposal creation in the final audit line like every other mutation.

The TUI agent prompt uses the same contract, so a proposal created by the
embedded agent appears in the Approvals tab and count on the next refresh.

## Delegation boundary

Delegation is adjacent but orthogonal:

```text
approval:   Has Marcus accepted this as tracked work?
delegation: Who owns the next action for accepted work?
```

The existing `WAITING` state already means accepted work delegated to or blocked
on another party. It does not record who owns it or distinguish delegation from
other waiting reasons.

Do not solve that inside this feature. In particular:

- do not encode an assignee in `PROPOSED`;
- do not make approval transition to `WAITING`;
- do not use approval tags as assignee tags; and
- do not require delegation metadata to ship proposals.

A later delegation plan should investigate a structured optional field such as
`delegated_to` or `waiting_for`, TUI views/actions, aging, and whether assigning
an accepted task should imply `WAITING`. That field would remain meaningful
across lifecycle transitions, while `WAITING` describes current actionability.
The proposal design leaves room for that without sharing implementation beyond
the existing typed mutation and TUI detail seams.

The follow-on plan now lives at
[Agent task delegation and heartbeat pickup](agent-task-delegation.md). It
defines owner-controlled `refine`, `research`, and `implement` modes plus an
atomic heartbeat claim/lease boundary. Those details remain outside this
approval-queue tranche.

## Implementation phases

Each commit must be green on its own. In particular, do not land tests for
`PROPOSED` in a refactor-only state-taxonomy commit.

### Phase 1: State taxonomy and domain behavior

1. Add canonical proposed/open/closed state categories.
2. Refactor duplicated `DONE_STATES` assumptions to `CLOSED_STATES`.
3. Add `PROPOSED` validation and transition behavior.
4. Exclude proposals from open queries, availability, project rollups,
   completion, recurrence, and archive eligibility.
5. Define merge behavior: `DONE`/`CANCELLED` remain terminal; `PROPOSED` is
   non-terminal but not open.
6. Add focused Store, Check, query, project, recurrence, archive, merge, and
   schema-version tests.

This phase is a behavior change and should land with its own tests. If a
preparatory rename is useful, keep it behavior-neutral and green independently.

### Phase 2: Application, CLI, and docs

1. Add proposal creation through `CreateTask`, including atomic initial notes.
2. Add shared approve/reject operations and mutation results.
3. Add `propose`, `approve`, `reject`, and `list --proposed`.
4. Update help, `docs/cli-spec.md`, `docs/conventions.md`, README examples, and
   shell completions if present.
5. Add CLI unit/integration tests for success, invalid state, ambiguity,
   dates-without-promotion, notes, undo/redo, children, and JSON.
6. Exercise real commands against a temporary task store.

### Phase 3: HTTP API parity

1. Extend metadata, enum, collection scope, and creation.
2. Add the accepted approval/rejection mutation shape.
3. Preserve ETag preconditions and post-state resource responses.
4. Update OpenAPI examples and error codes.
5. Run API parity, origin/host, concurrency, and schema tests.

### Phase 4: TUI view and interaction

1. Add the Approvals query/view and empty state.
2. Make tab labels/counts render-time data while sharing hit geometry.
3. Add the live nonzero badge count.
4. Add `7`, view navigation, mouse targeting, and session restore.
5. Add scoped `a` / `r` bindings and palette actions.
6. Preserve edit-before-decision, save-on-blur, undo/redo, external refresh,
   and nearest-row selection behavior.
7. Test narrow widths, Unicode/cell geometry, monochrome themes, zero/nonzero
   count, binding conflicts, panel-open decisions, and queue-empty transitions.
8. Capture Betamax/browser-style terminal proof of the finished flow if the
   repository's current TUI documentation workflow supports it.

### Phase 5: Agent guidance and end-to-end certification

1. Update `TASK_AGENT.md`, tasks CLI skill copies, and prompt contract tests.
2. Run an embedded-agent prompt that creates a proposal in a temporary store.
3. Observe the Approvals count/view in the real TUI.
4. Approve one proposal and reject another with `a` / `r`.
5. Verify the accepted item appears in Inbox, the rejected item is
   `CANCELLED`, and undo restores each decision.
6. Run all repository gates and independent review.

## Test matrix

At minimum, cover:

| Boundary | Required proof |
|---|---|
| Validation | `PROPOSED` accepted; unknown states rejected; no schema bump. |
| Classification | Proposed is neither open nor closed. |
| Visibility | Absent from every ordinary view/count; present in explicit proposal reads. |
| Creation | CLI/API/app create title, metadata, and rationale atomically. |
| Approval | Only proposal → `INBOX`; fields preserved; undo/redo works. |
| Rejection | Only proposal → `CANCELLED`; `closed` stamped; undo/redo works. |
| Dates | Proposed dates do not promote or activate the task. |
| Recurrence | Proposed recurring task is rejected by validation/transition. |
| Projects | Proposal does not affect open counts or next-action health. |
| Trees | No hidden accepted child; descendant decision behavior is explicit. |
| Merge | Proposal edits merge; terminal conflict semantics remain correct. |
| Archive | Proposal cannot sweep; rejected proposal can sweep. |
| Refs | Direct show/decision resolves; ordinary fuzzy mutations do not target it accidentally. |
| API | Scope, enum, ETag, mutation errors, and representation parity. |
| TUI count | Correct at startup, mutation, undo/redo, and external refresh. |
| TUI keys | `a`/`r` scoped correctly; existing project/recurrence keys unchanged elsewhere. |
| TUI layout | Seven tabs and badge preserve click spans at narrow and wide widths. |
| Agent | Agent proposes without asking, explains why, does not self-approve or execute. |

## Verification gates

Before calling the feature complete:

```sh
ruby test/all.rb
bundle exec ruby test/api/all.rb
bin/tasks check
git diff --check
```

Also run product-level smoke tests with isolated temporary data:

```sh
tasks propose "Review recurring subscriptions" --note "Three renewal emails arrived."
tasks list --proposed
tasks approve <id>
tasks list --proposed
tasks list
tasks undo
tasks reject <id>
tasks show <id>
```

Launch `tasks-tui` against that store and verify:

1. `7` opens Approvals.
2. The nonzero badge matches the queue.
3. `Return` shows rationale.
4. `e` edits without accepting.
5. `a` moves the proposal to Inbox immediately.
6. `r` removes it from Approvals and records `CANCELLED`.
7. `u` restores the last decision and count.
8. Project capture `a` and recurrence `r` still work outside Approvals.

Run one independent review in proportion to the cross-cutting state and TUI
risk. Address one rejection cycle if needed; record non-critical follow-ups in
`td`.

## Rollout and compatibility

This is an additive enum change at schema version 2, not a record-shape
migration. Roll out the complete binary before allowing agents to create the
first proposal. Once a store contains `PROPOSED`, older binaries will fail
`check`; document the minimum compatible release.

No data backfill is required. Existing tasks remain unchanged. If rollout must
be reversed before proposals exist, revert the code normally. If proposals
already exist, transition them through the new binary to `INBOX` or
`CANCELLED` before downgrading.

## Acceptance criteria

- An agent can create a rationale-bearing proposal with one CLI operation and
  no permission prompt.
- Proposals never appear as actionable accepted work or affect project health.
- The TUI always offers a stable seventh Approvals tab and accurate nonzero
  pending count.
- From the Approvals list or detail panel, `a` accepts and `r` rejects with no
  confirmation.
- Approval lands in `INBOX`; rejection lands in `CANCELLED`; both are undoable.
- Editing a proposal does not approve it.
- Existing `a` and `r` shortcuts retain their behavior outside proposal
  context.
- CLI and HTTP APIs expose equivalent creation, listing, approval, and rejection
  semantics.
- Agent guidance clearly authorizes proposals while forbidding self-approval
  and underlying execution.
- Delegation remains explicitly separate and unimplemented.
- Core tests, API tests, `tasks check`, product smoke tests, TUI visual proof,
  and independent review all pass.
