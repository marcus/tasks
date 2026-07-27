# Agent task delegation and heartbeat pickup

Status: product direction; implementation design pending

Date: 2026-07-27

Related: [Agent task approval queue](agent-task-approval-queue.md)

## Goal

Let the owner mark an accepted task as eligible for an agent to pick up during a
heartbeat. The marker must say how far the agent is authorized to take the work:

- **refine** — improve the task definition, acceptance criteria, links, and
  implementation approach;
- **research** — investigate and attach a decision-quality result, but do not
  perform the underlying change; or
- **implement** — carry the task through implementation, verification, and
  normal in-scope repository handoff.

The motivating interaction is:

```text
owner creates or selects an accepted task
owner marks it "agent: research"
heartbeat sees the task in the agent-ready queue
one agent claims it atomically
agent works within the selected authority
owner sees progress and outcome in the task/TUI
```

This is a delegation queue, not an approval queue. A task must already be
accepted work before it can be offered to an agent.

## Relationship to task proposals

Approval and delegation are independent decisions:

| Question | Representation |
|---|---|
| Has the owner accepted this as tracked work? | Lifecycle state: `PROPOSED` versus an open state. |
| May a heartbeat agent pick it up? | Agent delegation marker on an accepted task. |
| How far may that agent go? | Delegation mode: `refine`, `research`, or `implement`. |
| Is the task currently actionable by the owner? | Existing GTD state and availability rules. |

Consequences:

- `PROPOSED` never means delegated.
- Approving a proposal does not automatically delegate it.
- Marking a task agent-ready does not approve a proposal.
- A rejected/cancelled task cannot remain agent-ready.
- Delegation must not be encoded as `@agent`, an `agent` project, or `WAITING`
  alone; those conventions cannot express authority or safe claiming.

The approval-queue tranche may ship without this feature. It should preserve a
clean future field and query seam rather than implement part of delegation
implicitly.

## Product contract

### Owner-controlled eligibility

Only an explicit owner action marks a task agent-ready. Agents may propose that
a task be delegated, but they may not add or widen their own delegation
authority.

The initial human-facing commands should read like:

```sh
tasks delegate <ref> refine
tasks delegate <ref> research
tasks delegate <ref> implement
tasks undelegate <ref>
tasks list --agent-ready
```

The TUI should expose the same operation from a selected accepted task and show
a compact mode marker in list/detail views. The exact shortcut needs a
keybinding audit; this plan does not reserve one yet.

### Authority modes

`refine` permits:

- reading the task and linked/local context;
- improving title, body, acceptance criteria, project placement, tags,
  contexts, and suggested dates;
- splitting the task into a small coherent set of subtasks when necessary; and
- leaving a concise rationale for material changes.

It does not permit doing the underlying work, contacting people, deploying,
sending messages, purchasing, deleting external data, or closing the task as
complete.

`research` includes refine authority plus:

- inspecting relevant repositories and connected read-only sources;
- running non-mutating diagnostics and experiments;
- writing a durable research brief or attaching findings to the task; and
- recommending a concrete next action.

It does not permit implementation or consequential external writes.

`implement` includes refine and research authority plus:

- changing code/docs/files within the task's named scope;
- running tests and product proof;
- committing and pushing when repository instructions normally require it; and
- completing the task only when its stated acceptance criteria are actually
  satisfied.

It still does not silently authorize scope expansion, deployment, messages,
purchases, destructive external actions, credential changes, or bypassing
repository-specific approval gates. The task text and repository instructions
remain the boundary.

Modes are monotonic in capability, but agents may never promote a mode. Only
the owner can change `refine → research → implement`.

### Claiming and duplicate-work prevention

Heartbeat discovery and claiming must be two separate operations:

```sh
tasks list --agent-ready --json
tasks agent claim <ref> --worker <stable-worker-id>
```

The claim is an atomic compare-and-set from ready to claimed. If another worker
claimed the task first, the command returns a conflict and the losing worker
chooses another task. A list read alone never grants ownership.

A claim records enough structured data to answer:

- which stable task is claimed;
- which worker/session claimed it;
- when the claim began;
- which authority mode applies; and
- whether the lease is still live.

Heartbeat systems need lease expiry or explicit release so a crashed agent does
not strand work forever. The implementation design should prefer a renewable
lease with a conservative timeout over an eternal claim. The owner may revoke a
claim at any time.

Do not use Git branches, process ids, or free-text body notes as the source of
truth for a claim. They may be supporting evidence but cannot provide an atomic
single-winner transition.

### Progress and outcomes

Agents should report durable progress through task operations rather than
ephemeral chat:

- `refine`: edit the task and append a short change note;
- `research`: attach/link the durable brief and summarize the decision;
- `implement`: attach commit/proof references and update or complete the task;
- blocked: record the blocker and release or park the claim according to the
  heartbeat policy.

Claim state is not task lifecycle. A task can remain `TODO`, `NEXT`, or
`WAITING` while claimed. Completing/cancelling a task clears any delegation
marker and live claim atomically.

Whether an active claim should temporarily set lifecycle state to `WAITING`
needs user testing. The default recommendation is **no**: `WAITING` currently
means the next action is outside the owner's control, while an agent claim is a
separate execution fact that the TUI can show directly.

## Persistence direction

Do not finalize the record shape until the heartbeat runtime and lease
requirements are inspected. The likely model is one optional structured object
owned by the task schema, conceptually:

```json
{
  "agent": {
    "mode": "research",
    "status": "ready",
    "worker": null,
    "claimed_at": null,
    "lease_until": null
  }
}
```

Exact keys, timestamps, and merge behavior remain a design task. Required
properties:

- absent means not delegated;
- mode is owner-controlled;
- ready/claimed transitions are atomic and undo/audit aware;
- ordinary task edits cannot overwrite a concurrent claim;
- multi-device JSONL merge cannot produce two valid claim owners;
- expired claims are derived or repaired deterministically; and
- fixed key order and one-line diffs remain intact.

`OperationContext.actor` may help identify workers, but a claim needs persisted
coordination state. Do not overload the non-persisted operation context.

## Heartbeat selection

`tasks list --agent-ready --json` should be the stable machine-readable
entrypoint. It returns only:

- accepted live tasks;
- available tasks, unless an explicit heartbeat policy includes deferred work;
- tasks with a ready delegation marker;
- tasks whose prerequisites/ancestor constraints permit work; and
- fields needed to rank and claim without parsing display text.

The first ranking can reuse task priority, due dates, and canonical file order.
Do not build autonomous scoring until real heartbeat usage shows that the queue
needs it.

A heartbeat agent should:

1. read ready candidates;
2. choose one task within its configured capabilities;
3. claim it atomically;
4. re-read the claimed task and authority mode;
5. perform only the authorized work;
6. attach progress/proof;
7. complete, block, or release; and
8. never recursively delegate or widen authority.

## Safety boundaries

- The marker grants task-scoped authority, not general machine authority.
- Connected-service writes remain approval-gated unless the task explicitly
  authorizes that exact action and normal connector policy permits it.
- Deployments retain their own explicit approval requirement.
- A vague `implement` task must be refined or blocked, not interpreted
  expansively.
- Secrets and private data follow repository and connector rules.
- Agents must expose blockers rather than converting research/refinement into
  implementation.
- Owner revocation wins over a stale worker; subsequent writes from the revoked
  claim must fail their precondition.

## Open design questions

Resolve these against a concrete heartbeat runtime before implementation:

1. What stable worker/session id exists across heartbeat invocations?
2. What lease duration and renewal cadence match actual jobs?
3. Should `implement` imply permission to commit and push everywhere, or should
   repository instructions remain the sole authority? The recommendation is
   the latter.
4. How should a blocked agent return a task: ready for another agent, still
   claimed but flagged, or owner-review required?
5. Should a proposal be approvable directly into an agent-ready mode through an
   explicit combined owner action? This can be useful later but must not be the
   default approval behavior.
6. How should multi-device merge resolve a claim made concurrently offline?
   A lease alone may not be sufficient without a single-writer coordinator.

## Recommended implementation sequence

1. Inspect the real heartbeat runner, stable identity, cadence, and failure
   behavior.
2. Write an ADR for the delegation/claim record and merge semantics.
3. Add typed delegation and atomic claim/release operations in
   `Tasks::Application` / Store.
4. Add CLI and HTTP parity plus explicit precondition errors.
5. Add TUI markers, agent-ready view/filter, owner controls, and claim status.
6. Update `TASK_AGENT.md` with mode-specific authority.
7. Certify single-winner claims, lease expiry, revocation, crash recovery,
   multi-device conflicts, and each authority boundary.

## Acceptance criteria for the future feature

- The owner can mark an accepted task `refine`, `research`, or `implement`.
- Heartbeat agents can discover eligible tasks without parsing task text.
- Exactly one agent can claim a task.
- The claimed agent cannot widen its authority.
- Claim status and mode are visible in the TUI.
- Revocation and lease expiry prevent stale writes.
- Lifecycle, approval, delegation, and claim state remain distinct.
- Progress and outcomes are durable and linked from the task.
- Multi-device sync cannot silently authorize duplicate implementation.
- Tests and product proof cover heartbeat pickup through completion/release.
