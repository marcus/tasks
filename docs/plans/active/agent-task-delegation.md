# Task delegation: humans, agents, and heartbeat pickup

Status: implementation-ready contract (supersedes the 2026-07-27
product-direction draft)

Date: 2026-07-27

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

- `kind: human` ⇒ `status: delegated`, `assignee` present and email-shaped
  (contains `@`, no whitespace), no `mode`;
- `kind: agent`, `status: ready` ⇒ no `assignee`;
- `kind: agent`, `status: claimed` ⇒ `assignee` present;
- `at` always present and a valid UTC timestamp;
- `work_ref`, when present, a non-empty single-line string (one reference;
  additional links belong in the task body/links as today);
- a `PROPOSED` task cannot carry `delegation`; and
- nested keys emit in the fixed order above with absent keys omitted, so
  one-line diffs stay readable.

### Identity conventions

- **Human assignee**: an email address, e.g. `pat@example.com`. It is an
  identifier, not a mail integration.
- **Agent worker id**: a free-form token, recommended form
  `<harness>/<model>/<session-id>`, e.g.
  `claude-code/claude-fable-5/313cf82e`. Validation requires only non-empty,
  no whitespace, ≤ 200 chars. The id needs to be stable only for the lifetime
  of one claim; there is no cross-session lease to renew.

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
tasks undelegate <ref>                            # clear marker / revoke claim
tasks workref <ref> <url-or-id>                   # set/replace work reference

# reads
tasks list --delegated [--json]                   # any delegation, incl. claimed
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
  across devices. Resolution rules:
  - only one side changed it → that side wins (normal field-level rule);
  - both sides claimed concurrently → deterministic single winner: earlier
    `at`, tiebreak lexicographically smaller `assignee`; record a merge
    event. The losing worker discovers the loss at its next worker-matched
    operation;
  - one side undelegated (owner) vs. any other change → the removal wins,
    honoring revocation;
  - close vs. claim → state follows existing terminal-state rules; the
    delegation object follows the atomic-field rules above and the
    provenance-on-close rule.
- Archive: `delegation` rides along verbatim; archived provenance is the
  point.
- Revisions (ADR-0007): delegation changes are `own`-field changes, so
  HTTP `If-Match` CAS covers API claims for free; the CLI claim gets its
  atomicity from the store mutation lock plus the ready-status precondition.

## HTTP API parity

- Task resources include `delegation`; metadata exposes the mode and status
  vocabularies.
- `GET /api/v1/tasks?scope=delegated` and `?scope=agent_ready` mirror the CLI
  list scopes.
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
  delegation operations fail their worker-match precondition.

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
| Shape | Valid objects accepted; every invariant violation rejected; fixed nested key order; no schema bump. |
| Eligibility | Only accepted live tasks delegable; PROPOSED/closed/archived refuse. |
| Human flow | Email validated; WAITING default; `--keep-state`; provenance after done. |
| Claim | CAS single winner under concurrency; conflict names holder; list never grants ownership. |
| Release | Worker match enforced; owner force works; note appended; back to ready. |
| Revocation | Undelegate clears claim; stale worker's next delegation op fails. |
| Work ref | Owner always; worker only while claim matches; survives close and archive. |
| Close | Ready clears; claimed/delegated retained verbatim. |
| Merge | Atomic object; deterministic claim-race winner; removal wins; no mixed-field claims. |
| Reads | `--delegated` / `--agent-ready` scopes compose with existing filters; JSON has rank/claim fields. |
| API | Scope, endpoints, ETag, conflict codes, representation parity. |
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

- The owner can delegate an accepted task to a person (email) or to the
  agent pool with `refine`/`research`/`implement`, and undelegate at any
  time.
- Heartbeat agents discover eligible tasks via `--agent-ready --json`
  without parsing display text.
- Exactly one worker can ever hold a claim, across processes and across
  multi-device merges; a losing claimer gets a clear conflict.
- A claimed agent cannot widen or promote its authority; revocation defeats
  stale workers.
- `work_ref` records where the work lives and survives completion and
  archival, along with who held the task.
- Human delegation defaults to `WAITING`; agent claims never change
  lifecycle state.
- Delegation state is visible in list rows and task detail; CLI and HTTP
  expose equivalent semantics.
- No leases, boards, notifications, or scoring shipped — the accepted
  limitation (stale claims need owner cleanup) is documented in the agent
  contract.
- All gates, the product smoke including a real claim race, and one
  independent review pass.
