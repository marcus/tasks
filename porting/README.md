# `porting/` — the Go-port control plane

**The port is not approved.** This tree exists so that Phase 1 of
[td-27fbf5](../docs/plans/active/tasks-go-port-plan.md) can produce an honest
estimate — a conformance harness that provably catches seeded mismatches, and
a manifest that says how much behavior there actually is to move. That
estimate is what informs the decision to port or not. Nothing here ports
application code, and no Go code lands until Marcus says so.

Everything in this tree is language-neutral on purpose: it describes `tasks`'
observable behavior, not its implementation. Ruby is the oracle; a second
implementation is the thing being judged against it.

## The tree

| Path | Holds | Filled by |
|---|---|---|
| `manifest.jsonl` | one record per proof-sized capability: id, deps, risk, `source_sha`, fixtures, status, evidence locator. Schema and field ownership: [`manifest.md`](manifest.md) | td-940935 |
| `campaigns.jsonl` | one record per campaign from the playbook's proposed sequence; the epics slices hang under | td-940935 |
| `manifest-issues` | projects the manifest into td — one epic per campaign, one issue per slice, `depends_on` as dependency edges. Idempotent; `plan` is the dry run | td-940935 |
| `intentional-differences.md` | every accepted divergence from Ruby, and only Marcus accepts one | as they arise |
| `specs/` | `observations.schema.json` (the shape a runner emits) and `errors.md` | td-3527b1 |
| `fixtures/{valid,compat,malformed,adversarial}/` | sanitized store copies. Agents operate on copies here and never on a live store | td-a1d16a |
| `runners/` | per-implementation drivers that execute an invocation and emit one observation | td-a23bad (ruby) |
| `compare/` | the comparator: observation vs observation, plus the Ruby baseline | td-34d915 |
| `evidence/<slice-id>/` | oracle captures, conformance reports, review findings. td logs point *at* these; evidence never lives only inside td | every slice |
| `logs/` | tick transcripts and `limit-<harness>.json` (gitignored) | `loop.sh` |
| `STOP` | touch it to halt the fleet at the next tick boundary (gitignored) | you |

## The runtime

| File | Audience | What it is |
|---|---|---|
| `PORTING.md` | agents | the prompt every tick reads verbatim. Policy: mission, non-negotiables, the slice loop, when to stop and escalate |
| `OPERATING.md` | you | the runbook — running, stopping, watching, warning signs, tuning knobs |
| `loop.sh` | — | the supervisor: worktree isolation, scheduling, timeouts, usage-limit parking. Contains no porting knowledge |
| `test-loop-limits.sh` | — | asserts `loop.sh`'s usage-limit detection and reset-time parsing. Run it after touching either |

## Which document is authoritative

- **What to port, in what order, and the phase gates** —
  [`docs/plans/active/tasks-go-port-plan.md`](../docs/plans/active/tasks-go-port-plan.md).
- **How to port it** — the conformance method, the control-plane shape this
  tree implements, the per-slice loop —
  [`docs/plans/active/language-porting-playbook.md`](../docs/plans/active/language-porting-playbook.md).
- **Why the fleet is built this way** — layers, td as coordination bus, model
  policy, branch strategy —
  [`docs/plans/active/tasks-go-port-fleet-ops.md`](../docs/plans/active/tasks-go-port-fleet-ops.md).
- **What an agent does on a tick** — `PORTING.md`. It is deliberately short;
  it points at the plans rather than restating them, because it is paid for on
  every tick.
- **Progress** — `manifest.jsonl` and `evidence/`, never a hand-written
  percentage and never a td query. `porting/manifest-issues progress` is the
  command that counts it.

## Getting started

```sh
porting/loop.sh --once --dry-run     # preflight only; touches no harness
porting/test-loop-limits.sh          # the one unit-tested part of loop.sh
porting/manifest-issues validate     # manifest is self-consistent and resolves
porting/manifest-issues plan         # what a td sync would do; touches nothing
```

Read [`OPERATING.md`](OPERATING.md) before starting the fleet for real, and
[`manifest.md`](manifest.md) before editing a slice record.
