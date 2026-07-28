# Task delegation: humans, agents, and heartbeat pickup

Status: **implemented** (phases 1–5 landed; see [What shipped differs from this
contract](#what-shipped-differs-from-this-contract) for the rules that changed
during implementation and review, and [Acceptance
criteria](#acceptance-criteria) for which criteria hold with which caveats).
Supersedes the 2026-07-27 product-direction draft.

Date: 2026-07-27 (contract); 2026-07-28 (implementation notes)

Related: [Agent task approval queue](agent-task-approval-queue.md),
[ADR-0007](../../adr/0007-concurrency-and-revisions.md) (revisions/CAS),
[ADR-0011](../../adr/0011-task-proposals-as-lifecycle-state.md)

## Goal

Let the owner delegate an accepted task to either:

- **a human** — identified by email address; the task records who owns the
  next action; or
- **an agent pool** — marked with an authority mode (`refine`, `research`,
  `implement`); a heartbeat agent later claims it atomically and works within
  that authority.

In both cases the task carries a durable reference to where the work happened
(a ticket, PR, research brief, or agent session), visible after the task
closes.

The motivating interactions:

```text
# human
owner: tasks delegate 4f2a --to pat@example.com
task shows "delegated → pat@example.com", state moves to WAITING
later: owner records the ticket:  tasks workref 4f2a https://github.com/acme/x/issues/12

# agent
owner: tasks delegate 4f2a research
heartbeat agent: tasks list --agent-ready --json
heartbeat agent: tasks claim 4f2a --worker claude-code/claude-fable-5/313cf82e
agent researches, attaches a brief, records a work ref, releases or the owner completes
```

This is a lightweight delegation marker, not a kanban board and not a
distributed lock service. The one hard guarantee is **single pickup**: two
agents cannot both believe they own the same task. Everything beyond that
(lease expiry, write fencing, reassignment workflows) is deliberately out.

## Scope

This tranche includes:

- one optional structured `delegation` field on task records;
- human delegation with an email assignee;
- agent delegation with `refine` / `research` / `implement` authority modes;
- atomic ready → claimed pickup with a stable worker id;
- explicit release, owner undelegate/revocation, and a `work_ref` reference;
- `list --delegated` and `list --agent-ready` (human and JSON);
- HTTP API parity;
- minimal TUI surfacing (list marker, detail section, palette actions);
- merge, archive, undo, and check coverage; and
- `TASK_AGENT.md` mode-authority guidance.

This tranche does not include:

- leases, claim expiry, renewal, or heartbeat liveness tracking — a crashed
  agent leaves a visibly claimed task until the owner intervenes, and that is
  accepted for now;
- write fencing of ordinary task edits while claimed;
- multiple assignees, reassignment chains, or delegation history;
- notifications, reminders, or aging reports;
- autonomous ranking/scoring of the agent-ready queue;
- a delegation TUI tab or board; and
- delegating `PROPOSED` tasks (approval and delegation stay independent
  decisions, per the approval-queue plan).

## Delegation model

### Record shape

One optional object owned by the task schema. Absent means not delegated.

```json
"delegation": {
  "kind": "agent",
  "mode": "research",
  "status": "claimed",
  "assignee": "claude-code/claude-fable-5/313cf82e",
  "at": "2026-07-27T18:04:11Z",
  "work_ref": "https://github.com/acme/x/pull/42"
}
```

Fields, in fixed emission order:

| Key | Meaning |
|---|---|
| `kind` | `"human"` or `"agent"`. |
| `mode` | Agent authority: `refine`, `research`, `implement`. Required for agent, forbidden for human. |
| `status` | `delegated` (human), `ready` (agent, unclaimed), `claimed` (agent, claimed). |
| `assignee` | Human: email address. Agent: worker id, present only when `claimed`. |
| `at` | UTC timestamp of the last status transition. |
| `work_ref` | Optional single string: URL or identifier for where the work lives (ticket, PR, brief, session). |

Invariants, enforced by Store validation and `tasks check`:

- `kind: human` ⇒ `status: delegated`, `assignee` present and address-shaped
  (non-empty local part, exactly one `@`, dotted domain — `Delegation::EMAIL_RE`;
  `@work` and `pat@localhost` are refused), no `mode`;
- `kind: agent`, `status: ready` ⇒ no `assignee`;
- `kind: agent`, `status: claimed` ⇒ `assignee` present;
- `at` always present and a valid UTC timestamp;
- `work_ref`, when present, a non-empty single-line string of at most 500
  characters with no control characters (one reference; additional links belong
  in the task body/links as today);
- `assignee`, worker ids, and `work_ref` all reject C0 controls, `DEL`, the C1
  block, and Unicode whitespace (NBSP, U+2028/U+2029, the ideographic space —
  Ruby's `\s` is ASCII-only). Four surfaces render these strings raw, including
  the conflict line that names a holder to the agent that just lost a race, so
  the bytes are refused at the schema boundary rather than sanitized per
  surface;
- a `PROPOSED` task cannot carry `delegation`; and
- known keys emit in the fixed order above with absent keys omitted, so
  one-line diffs stay readable; *unknown* nested keys are preserved in their own
  order after them and reported by `check` as a WARNING, never an error —
  forward compatibility must not invert one level down, or a
  `delegation.lease_until` from a newer binary would fail `check`, make
  `JsonlMerge` refuse the whole merge, and block every write store-wide.

### Identity conventions

- **Human assignee**: an email address, e.g. `pat@example.com`. It is an
  identifier, not a mail integration.
- **Agent worker id**: a free-form token, recommended form
  `<harness>/<model>/<session-id>`, e.g.
  `claude-code/claude-fable-5/313cf82e`. Validation requires valid UTF-8,
  non-empty, no Unicode whitespace, no control/escape characters, ≤ 200 chars.
  The id needs to be stable only for the lifetime of one claim; there is no
  cross-session lease to renew.

`OperationContext.actor` remains non-persisted and is not the claim record.
The CLI may default `--worker` from an environment variable (e.g.
`TASKS_WORKER_ID`) so heartbeat harnesses can set it once; the flag always
wins.

### Status machine

```text
(absent) ──delegate --to email──────────▶ delegated ──undelegate──▶ (absent)
(absent) ──delegate <mode>──▶ ready ──claim──▶ claimed ──release──▶ ready
                                │                 │
                                └──undelegate─────┴──undelegate──▶ (absent)
task closes (DONE/CANCELLED):
  status ready      → delegation cleared (nothing happened yet)
  status claimed/delegated → delegation retained verbatim as provenance

recurring task completed (rolls forward instead of closing):
  claimed → ready   (fresh `at`, work_ref dropped, mode retained)
  delegated → delegated (fresh `at`, work_ref dropped, person retained)
```

Rules:

- Only the owner delegates, undelegates, or changes `mode`. Agents never
  create, widen, or promote their own delegation (`refine → research →
  implement` is owner-only).
- **Claim is an atomic compare-and-set** from `ready` to `claimed` under the
  store mutation lock. A losing worker gets a conflict error naming the
  current holder and picks another task. A list read never grants ownership.
- **Release** returns `claimed → ready` and requires the matching worker id;
  the owner may always force it (or `undelegate`). A blocked agent releases
  with a note appended to the task body via the existing note seam.
- **Revocation wins**: after `undelegate`, a stale worker's `release` /
  `workref` fails its worker-match precondition. No further fencing in v1.
- Completing or cancelling a task with a live claim or human delegation
  retains the `delegation` object as inert provenance — this is how "who did
  it and where" survives into the archive. `work_ref` is preserved.
- **Completing a delegated *recurring* task rolls the standing intent
  forward** instead (it does not close, so there is no provenance to keep).
  The next occurrence inherits the agent `mode`, or the person, always
  **unclaimed**, with a fresh `at` and **no** `work_ref`: the claim and the
  reference belong to the cycle that just finished, and carrying them over
  would hand the new occurrence to a worker who never picked it up — invisible
  to `--agent-ready` (it looks claimed) and unpickupable by anyone else. A
  marker of no recognized kind is dropped rather than carried. To stop a
  standing delegation the owner must `undelegate`; completing a cycle will not.
- Claim/delegation state is **not** lifecycle state, with one pragmatic
  exception below.

### Lifecycle interaction

- `tasks delegate <ref> --to <email>` sets lifecycle to `WAITING` by default —
  that is exactly what `WAITING` means in this system (next action outside the
  owner's control). `--keep-state` opts out. Undelegating does not
  automatically leave `WAITING`; the owner decides.
- Replacing that person with the agent pool undoes it: a task that is `WAITING`
  *because* it was delegated to someone returns to `TODO`, since agent-ready
  work is actionable again and a `WAITING` marker would describe it wrongly.
  Only that inherited `WAITING` is cleared — a `WAITING` the owner set on an
  undelegated task is theirs to keep. `--keep-state` opts out here too.
- Agent delegation and claims never change lifecycle. A task can be `TODO` or
  `NEXT` while ready or claimed; the TUI shows the delegation marker
  directly.
- Only accepted live tasks (`INBOX`, `TODO`, `NEXT`, `WAITING`) can be
  delegated. `PROPOSED`, closed, and archived tasks refuse with a clear error
  naming the state.

### Authority modes

Unchanged from the product draft, restated as the contract for
`TASK_AGENT.md`:

`refine` permits reading the task and linked/local context; improving title,
body, acceptance criteria, placement, tags, contexts, and suggested dates;
splitting into a small coherent set of subtasks; and leaving a concise
rationale for material changes. It does not permit doing the underlying work,
contacting people, deploying, sending messages, purchasing, deleting external
data, or completing the task.

`research` adds inspecting relevant repositories and connected read-only
sources; running non-mutating diagnostics and experiments; writing a durable
research brief linked as `work_ref` or attached to the task; and recommending
a concrete next action. No implementation or consequential external writes.

`implement` adds changing code/docs/files within the task's named scope;
running tests and product proof; committing and pushing when repository
instructions normally require it; and completing the task only when its
stated acceptance criteria are satisfied, with `work_ref` pointing at the
result. Repository instructions remain the sole authority for commit/push
policy; the mode does not override per-repo approval gates. It never
authorizes scope expansion, deployment, messaging, purchases, destructive
external actions, or credential changes.

A vague `implement` task must be refined or released with a blocker note, not
interpreted expansively.

## CLI

```sh
# owner
tasks delegate <ref> refine|research|implement    # agent-ready with mode
tasks delegate <ref> --to <email> [--keep-state]  # human delegation
tasks undelegate <ref>                            # clear marker / revoke claim (also the repair route)
tasks workref <ref> <url-or-id|off|none>          # set/replace/clear work reference

# reads
tasks list [--all] --delegated [--json]           # any delegation, incl. claimed
tasks list --agent-ready [--json]                 # agent kind, ready, available

# agent (heartbeat)
tasks claim <ref> --worker <worker-id> [--json]
tasks release <ref> --worker <worker-id> [--note "blocker..."]
```

Semantics:

- `delegate` with a mode on an already agent-ready task updates the mode (a
  claimed task refuses; undelegate first). `delegate --to` on an
  agent-delegated task (and vice versa) replaces the marker — one delegation
  per task.
- `claim` re-outputs the full task JSON with `--json` so an agent can claim
  and re-read authority in one step (heartbeat steps 3–4 below).
- `workref` is valid for the owner always, and for a worker only while its
  claim matches; it overwrites (one reference; more detail goes in the body).
  `off` and `none` both clear it, on every surface (CLI argument, TUI `W`
  form, HTTP body), normalized once in `DelegationCommand`.
- `list --delegated` selects within the lifecycle scope it is given, and the
  default `--open` scope still hides effectively unavailable tasks — so a
  delegated task with a future `scheduled` date or a blocked ancestor needs
  `--all --delegated` (or `--unavailable --delegated`) to appear. `--all
  --delegated` is also the closed-provenance review.
- `done`, `cancel`, and `state` need no new flags; `done --work-ref <url>` is
  a nice-to-have only if it falls out of the shared parsing seam trivially.
- Errors are explicit preconditions: not delegated, already claimed by
  `<holder>`, worker mismatch, proposed/closed task, invalid email/worker id.
- Human output examples:

```text
delegated → pat@example.com (WAITING): Renew office lease
agent-ready (research): Compare CRDT libraries
claimed by claude-code/claude-fable-5/313cf82e: Compare CRDT libraries
conflict: already claimed by claude-code/claude-opus-5/9a11d0c2 at 2026-07-27T18:04:11Z
```

All mutations go through typed `Tasks::Application` operations
(`delegate_task`, `undelegate_task`, `claim_task`, `release_task`,
`set_work_ref`) backed by Store primitives that own eligibility checks,
stamping, validation, and the atomic write. CLI/API/TUI translate only; no
subprocess round-trips. Every mutation is revision-aware, journaled, and
undoable through the existing undo path.

## Heartbeat selection

`tasks list --agent-ready --json` is the stable machine entrypoint. It
returns only accepted live tasks that are available (per existing
availability rules), carry `kind: agent, status: ready`, and whose
prerequisite/ancestor constraints permit work — with the fields needed to
rank and claim without parsing display text (id, title, mode, priority,
dates, project path, delegation object).

Ranking reuses task priority, due dates, and canonical file order. No
autonomous scoring until real heartbeat usage demands it.

The heartbeat contract for agents:

1. read ready candidates;
2. choose one task within configured capabilities;
3. claim it atomically (`claim --worker ... --json`);
4. use the claim's returned task + mode as the authority of record;
5. perform only the authorized work;
6. attach progress/proof and set `work_ref`;
7. complete (implement mode, criteria met), release with a blocker note, or
   leave claimed only while actively working; and
8. never recursively delegate, self-promote a mode, or claim more than it can
   finish in one session.

## Persistence, merge, and compatibility

- Add `delegation` to `Format::KEY_ORDER` (after `recur`, before `closed`),
  omitted when absent; add nested-shape validation to `Tasks::Check`;
  `KNOWN_KEYS` picks it up from `KEY_ORDER`.
- **No schema version bump.** Like `PROPOSED`, this is additive: absent means
  not delegated, no backfill, no migration. Older binaries will fail `check`
  on a store containing `delegation`, so roll the binary to all devices
  before creating the first delegation record; document the minimum
  compatible release.
- **Merge treats `delegation` as one atomic field** (add to
  `SPECIAL_FIELDS` in `JsonlMerge`): a merged record takes exactly one side's
  whole object, never a mix — this is what makes two-owner claims impossible
  across devices.

  **As implemented, the winner is a maximum over a SINGLE total order** on the
  two values, not the field-level "whichever side changed it" rule this plan
  originally specified. The base is consulted only to detect a removal. In
  order:

  1. **removal absorbs** — if either side dropped a marker the base carried,
     the merge drops it (`removal_wins`). Owner `undelegate` is the always-wins
     override and the escape hatch for every rule below;
  2. a **`claimed`** marker beats any non-claimed one (`claim_holds`), so a
     live claim is never silently downgraded to `ready` or replaced by a
     concurrent edit that did not go through revocation;
  3. **two claims** — earlier `at`, then the lexicographically smaller
     `assignee`, then canonical bytes (`earlier_claim`). The losing worker
     discovers the loss at its next worker-matched operation;
  4. **two non-claims** — later `at`, then canonical bytes (`later_intent`):
     the most recent owner intent.

  Why it changed: the original rule (one-sided change wins, then
  last-write-wins) is **not associative**. The merge is applied pairwise, so
  three devices syncing in different orders could converge on *different* claim
  holders — which breaks the single-pickup guarantee this whole tranche exists
  to provide. A maximum over one total order is associative and commutative.

  Honest consequences of the change:
  - an owner's `release --force` racing a worker's concurrent write on another
    device now **loses** the merge (rule 2 outranks it). `undelegate` is the
    operation that always wins; `release --force` is a same-device override
    only;
  - a one-sided release can be overturned when the devices meet, for the same
    reason. That is deliberate: a claim is only ever given up by the holder,
    the owner's explicit revocation, or a later claim of its own;
  - the merge-event reason vocabulary for this field is `removal_wins` /
    `claim_holds` / `earlier_claim` / `later_intent`, plus the reconciliation
    clears `cleared_on_non_task` / `cleared_on_proposal` / `cleared_on_close`.
    `last_write` no longer appears for `delegation`.
- **Reconciliation after resolution.** The marker and the rest of the record are
  resolved independently, so only some combinations are legal: a record whose
  `type` did not resolve to `task` drops the marker (`cleared_on_non_task`); a
  task the other side turned back into a proposal drops it
  (`cleared_on_proposal`); a task the other side closed keeps a claim or human
  delegation verbatim but drops a merely `ready` one (`cleared_on_close`) — the
  same normalization a local close performs. Without this the merge could emit a
  record `Check` rejects and fail over two individually legal sides.
- **Known residual divergence.** A *third* device concurrently closing the task
  can still diverge, because `merge_state!`'s own rule (one-sided change wins →
  terminal state wins → last-write-wins) is **not** associative, and
  `cleared_on_close` reads the merged state. This predates delegation and is a
  property of state merging; fixing it is a separate change to `resolve_state`.
- Archive: `delegation` rides along verbatim; archived provenance is the
  point.
- Revisions (ADR-0007): delegation changes are `own`-field changes, so
  HTTP `If-Match` CAS covers API claims for free; the CLI claim gets its
  atomicity from the store mutation lock plus the ready-status precondition.

## HTTP API parity

- Task resources include `delegation`; metadata exposes the mode and status
  vocabularies.
- `GET /api/v1/tasks?scope=delegated` and `?scope=agent_ready` mirror the CLI
  list scopes. Both are open-live slices, so **`?delegated=true` is the real
  parity surface for `--delegated`**: it composes with every lifecycle scope,
  and `scope=done|archived|all&delegated=true` is how an HTTP client answers
  the archive-provenance question this plan exists for ("which closed tasks
  were delegated, to whom, and where did the work land"). `scope=delegated`
  remains the documented shorthand for `scope=open&delegated=true`.
  `scope=agent_ready` stays open-only — a closed task is not claimable — and an
  incompatible combination (`scope=agent_ready&delegated=…`,
  `scope=delegated&delegated=false`, either delegation scope with a non-open
  `state`) is a `422` naming the query that would answer it, never a silently
  empty `200` a client cannot tell from "nothing is delegated".
- Action endpoints, matching the approve/reject convention:
  `POST /api/v1/tasks/{id}/delegate` (body: `{kind, mode?|assignee?}`),
  `/undelegate`, `/claim` (body: `{worker}`), `/release`, and
  `PUT .../work_ref`. All honor `If-Match`, return the canonical post-write
  resource and ETag, and return a 409-style conflict for a lost claim race.
- OpenAPI (`docs/api/openapi.yaml`), examples, and error codes land in the
  same tranche as the code.

## TUI (deliberately minimal, inline-first)

The edit panel is rarely used, so every delegation operation must be reachable
inline from the list or detail panel — the same interaction shape as `z`
(defer until) and `r` (recur): one key opens a one-line form whose hint lists
the accepted words, and the block parses free text.

### Keybindings

| Key | Contexts | Action |
|---|---|---|
| `D` | list, detail | delegate — `email` · `refine` · `research` · `implement` · `release` · `off` |
| `W` | list, detail | work reference — URL/id · `off` |

`D` opens `Delegate` (`Tui::Form` kind `:delegate`, `return_mode: :list`) with
hint `pat@example.com · refine · research · implement · release · off · esc
cancels` and a `(now …)` suffix showing current delegation, exactly like the
recur popup's `(now +1w)`. One prompt covers every owner operation:

- text containing `@` → human delegation to that email (sets `WAITING`);
- `refine` / `research` / `implement` (accept unambiguous prefixes `ref`,
  `res`, `imp`) → agent delegation at that mode;
- `release` → force a stale claim back to `ready` (owner override);
- `off` / `none` → undelegate.

`W` opens `Work reference` prefilled with the current `work_ref`; `off`
clears it. Both are also palette entries (`Delegate…`, `Set work
reference…`).

Availability mirrors `defer_selected`: a selected task, not a project, not
`PROPOSED`, not closed. `D` and `W` are unbound today in list/detail; the
uppercase pair matches the existing `H`/`L`/`K`/`J`/`Y`/`Z` convention for
less-frequent variants of a lowercase concept (`d` = date, `D` = delegate).

### Display

- List rows show one compact themed marker on delegated tasks: e.g.
  `→pat@…` for humans, `→refine` / `→research` / `→implement` for ready
  agent work, and a distinct claimed marker; exact glyph and truncation
  decided in implementation against the theme and narrow widths.
- The task detail panel gains a delegation section: kind, assignee, mode,
  status, `at`, and `work_ref` (openable through the existing links seam).
- No new tab, no board, no badges/counts, no agent-ready view, no claim
  dashboard.

## Safety boundaries

- The marker grants task-scoped authority, not general machine authority.
- Connected-service writes stay approval-gated unless the task explicitly
  authorizes that exact action and normal connector policy permits it;
  deployments keep their own approval requirement.
- Secrets and private data follow repository and connector rules.
- Agents surface blockers (release + note) rather than converting refine or
  research authority into implementation.
- Owner revocation wins over a stale worker; the revoked claim's subsequent
  delegation operations fail their worker-match precondition. Note the
  multi-device caveat: `undelegate` (a removal) always wins a merge, but
  `release --force` does not — see the merge order above.

## What shipped differs from this contract

Four rules were rewritten during implementation and review. Each is corrected
in place above; this is the index.

1. **Merge resolution** is a single total order over the two values, not the
   field-level "only one side changed → that side wins" plus last-write-wins
   this plan first specified. The old rule was not associative, so devices
   syncing in different orders could converge on different claim holders. See
   [Persistence, merge, and compatibility](#persistence-merge-and-compatibility)
   for the order, the reason vocabulary, the consequences for
   `release --force`, and the known residual (a third device closing the task
   concurrently can still diverge, because `merge_state!` is itself
   non-associative — a pre-existing property of state merging).
2. **Recurrence rolls the standing delegation forward**, which the contract did
   not mention at all: mode or person retained, always unclaimed, fresh `at`,
   no `work_ref`. `undelegate` is the only way to stop a standing delegation.
   See [Lifecycle interaction](#lifecycle-interaction).
3. **Validation is tighter**: address-shaped assignees, control/Unicode-
   whitespace refusal on all three identity/reference fields, a 500-character
   `work_ref` bound, `off`/`none` both clearing a work reference on every
   surface, and unknown nested keys preserved across a rewrite and reported as
   a `check` WARNING rather than failing the file. See
   [Record shape](#record-shape) and [Identity conventions](#identity-conventions).
4. **`undelegate` gained a repair power**: it may clear an invalid marker on
   its own record even when `tasks check` calls the file invalid, provided that
   record is the only problem and no `expected_revision`/`If-Match` was
   supplied. It is journaled as a repair, so `undo` deliberately restores the
   malformed bytes. It is the only delegation verb that repairs, because it is
   the only one that does not have to read the broken marker to decide what to
   write.

Two smaller adjustments: replacing a human delegation with the agent pool
returns an inherited `WAITING` to `TODO` (`--keep-state` opts out for both
kinds of `delegate`), and the HTTP surface gained a `delegated=true` query
parameter composing with every lifecycle scope — see
[HTTP API parity](#http-api-parity).

## Resolved design questions

Decisions replacing the draft's open questions:

1. **Worker identity**: `<harness>/<model>/<session-id>` string, valid for
   one claim; no cross-invocation stability required because there are no
   leases.
2. **Leases**: dropped. Stale claims are visible in `list --delegated` and
   the TUI and are cleared by the owner (`undelegate` or forced release).
   Revisit only if real heartbeat usage strands work often enough to hurt.
3. **Commit/push authority**: repository instructions remain the sole
   authority; `implement` does not override them.
4. **Blocked agent**: release back to `ready` with a body note. No
   owner-review quarantine state in v1.
5. **Approve-directly-into-delegated**: out of scope; approval and
   delegation stay two owner actions.
6. **Concurrent offline claims**: resolved deterministically at merge to a
   single winner (atomic-field resolution above); the loser fails its next
   worker-matched write. Good enough without a single-writer coordinator.

## Implementation phases

Each commit green on its own; each phase carries its tests.

### Phase 1: Store schema and merge

1. Add `delegation` to `KEY_ORDER`, emission, and `Check` validation
   (shape, kind/status/mode/assignee invariants, PROPOSED exclusion).
2. Add Store primitives: set/clear delegation, atomic claim CAS, release,
   work_ref, provenance-on-close behavior, undo journaling.
3. Add `delegation` to `JsonlMerge::SPECIAL_FIELDS` with atomic-object
   resolution, claim-race winner rule, and removal-wins revocation.
4. Tests: validation matrix, claim CAS under the mutation lock, close/cancel
   provenance vs. ready-clearing, archive round-trip, merge race/revocation
   cases, undo/redo.

### Phase 2: Application operations and CLI

1. Typed `Application` operations with explicit precondition errors and
   revision awareness.
2. `delegate`, `undelegate`, `claim`, `release`, `workref`, `list
   --delegated`, `list --agent-ready`, JSON output, `TASKS_WORKER_ID`
   default.
3. Update `docs/cli-spec.md`, `docs/conventions.md`, README examples.
4. CLI tests: success paths, WAITING default and `--keep-state`, mode
   update, replace human↔agent, conflict output, worker mismatch,
   proposed/closed refusal, JSON shapes, undo.

### Phase 3: HTTP API parity

1. Resource field, metadata vocabularies, list scopes, action endpoints.
2. ETag/If-Match preconditions and claim-conflict responses.
3. OpenAPI, parity tests, concurrency tests.

### Phase 4: TUI surfacing

1. Compact list marker and detail-panel delegation section.
2. Palette actions with availability limited to selected accepted tasks.
3. Tests: marker at narrow widths and in monochrome, panel rendering,
   palette availability, external-refresh consistency.

### Phase 5: Agent contract and certification

1. Update `TASK_AGENT.md` and the tasks CLI skill: mode authorities, the
   heartbeat contract, never self-delegate/promote, claim before work,
   release with note when blocked, always set `work_ref`.
2. End-to-end proof against a temporary store: delegate → two workers race
   `claim` (exactly one wins) → work → `workref` → done → provenance
   visible on the closed task; human flow with WAITING default; revocation
   mid-claim.
3. Full repository gates and one independent review.

## Test matrix

| Boundary | Required proof |
|---|---|
| Shape | Valid objects accepted; every invariant violation rejected; fixed nested key order; unknown nested keys preserved and warned, never fatal; no schema bump. |
| Eligibility | Only accepted live tasks delegable; PROPOSED/closed/archived refuse. |
| Human flow | Email validated; WAITING default; `--keep-state`; provenance after done. |
| Claim | CAS single winner under concurrency; conflict names holder; list never grants ownership. |
| Release | Worker match enforced; owner force works; note appended; back to ready. |
| Revocation | Undelegate clears claim; stale worker's next delegation op fails. |
| Work ref | Owner always; worker only while claim matches; survives close and archive. |
| Close | Ready clears; claimed/delegated retained verbatim. |
| Recurrence | Completion rolls intent forward: mode/person retained, unclaimed, fresh `at`, no `work_ref`. |
| Repair | `undelegate` clears a malformed marker on an otherwise-valid file; refused under `expected_revision`; `undo` restores the malformed bytes. |
| Merge | Atomic object; one total order (removal > claimed > earlier claim > later intent); associativity across three devices; reason vocabulary; no mixed-field claims. |
| Reads | `--delegated` / `--agent-ready` scopes compose with existing filters; `--delegated` under `--open` inherits the availability rule; JSON has rank/claim fields. |
| API | Scopes, `delegated=true` over every lifecycle scope, 422 on incompatible combinations, endpoints, ETag, conflict codes, representation parity. |
| TUI | Marker, detail section, palette availability, narrow/monochrome. |
| Agent | Follows mode authority; claims before working; releases with blocker; never widens mode. |

## Verification gates

```sh
ruby test/all.rb
bundle exec ruby test/api/all.rb
bin/tasks check
git diff --check
```

Product smoke against isolated temporary data:

```sh
tasks capture "Compare CRDT libraries"
tasks delegate <id> research
tasks list --agent-ready --json
tasks claim <id> --worker claude-code/claude-fable-5/aaaa1111        # wins
tasks claim <id> --worker claude-code/claude-opus-5/bbbb2222         # conflicts
tasks workref <id> https://example.com/brief
tasks release <id> --worker claude-code/claude-fable-5/aaaa1111
tasks delegate <id> --to pat@example.com                             # replaces, sets WAITING
tasks done <id> && tasks show <id>                                   # provenance retained
tasks undo
```

## Acceptance criteria

Kept verbatim, each annotated with what actually holds.

- The owner can delegate an accepted task to a person (email) or to the
  agent pool with `refine`/`research`/`implement`, and undelegate at any
  time. — **Holds.** Caveat: the email must be address-*shaped*
  (`local@domain.tld`), so `@work` and `pat@localhost` are refused.
- Heartbeat agents discover eligible tasks via `--agent-ready --json`
  without parsing display text. — **Holds.**
- Exactly one worker can ever hold a claim, across processes and across
  multi-device merges; a losing claimer gets a clear conflict. — **Holds**,
  and only because the merge rule was rewritten as one total order; the
  originally specified rule did not hold this across three devices. Residual:
  a third device concurrently *closing* the task can still diverge, via the
  non-associative `state` merge that predates delegation.
- A claimed agent cannot widen or promote its authority; revocation defeats
  stale workers. — **Holds** on one device. Across devices only `undelegate`
  always wins a merge; an owner's `release --force` racing a worker's
  concurrent write loses.
- `work_ref` records where the work lives and survives completion and
  archival, along with who held the task. — **Holds** for a task that closes.
  For a *recurring* task there is no close: the next occurrence starts with no
  `work_ref` and no claim, by design.
- Human delegation defaults to `WAITING`; agent claims never change
  lifecycle state. — **Holds**, with the one intended exception: replacing a
  person with the agent pool returns an inherited `WAITING` to `TODO`
  (`--keep-state` opts out). Claims themselves never move state.
- Delegation state is visible in list rows and task detail; CLI and HTTP
  expose equivalent semantics. — **Holds.** HTTP needed a `delegated=true`
  query parameter to reach parity with `--delegated` over non-open scopes;
  before that, closed provenance was unreachable over HTTP and
  `scope=delegated&state=DONE` returned a misleading empty `200`.
- No leases, boards, notifications, or scoring shipped — the accepted
  limitation (stale claims need owner cleanup) is documented in the agent
  contract. — **Holds.**
- All gates, the product smoke including a real claim race, and one
  independent review pass. — **Holds**; four rounds of review fixes are folded
  into the corrections indexed above.
