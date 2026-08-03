# `porting/` — the Go-port control plane

> **Retired workflow.** The port is approved, but this control plane is no
> longer its working method. Do not restart `loop.sh`, schedule work from the
> 144-slice manifest, or add per-slice evidence. Follow the accepted
> [velocity plan](../docs/plans/active/tasks-go-port-velocity-plan.md).

This tree remains useful for fixtures, runners, and differential conformance.
The loop, manifest scheduler, evidence workflow, and material below describe
the earlier fleet approach and are retained as historical context.

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
| `conform` | the whole loop in one invocation: both runners, the comparator, the verdict. Its exit status *is* the comparator's, so it is a drop-in gate | the write-path slice |
| `evidence/<slice-id>/` | oracle captures, conformance reports, review findings. td logs point *at* these; evidence never lives only inside td | every slice |
| `logs/` | tick transcripts and `limit-<harness>.json` (gitignored) | `loop.sh` |
| `STOP` | touch it to halt the fleet at the next tick boundary (gitignored) | you |

## The runtime

| File | Audience | What it is |
|---|---|---|
| `PORTING.md` | historical | retired prompt that every fleet tick read verbatim |
| `OPERATING.md` | historical | retired runbook for running and diagnosing the fleet |
| `loop.sh` | historical | retired supervisor retained for diagnosis; do not start it for active work |
| `test-loop-limits.sh` | — | asserts `loop.sh`'s usage-limit detection and reset-time parsing. Run it after touching either |

## Document map

- **Current delivery plan** —
  [`tasks-go-port-velocity-plan.md`](../docs/plans/active/tasks-go-port-velocity-plan.md).
- **Deprecated original scope and phase gates** —
  [`docs/plans/deprecated/tasks-go-port-plan.md`](../docs/plans/deprecated/tasks-go-port-plan.md).
- **Deprecated porting method** — the conformance method and per-slice loop —
  [`docs/plans/deprecated/language-porting-playbook.md`](../docs/plans/deprecated/language-porting-playbook.md).
- **Deprecated fleet rationale** — layers, td coordination, model policy, and
  branch strategy —
  [`docs/plans/deprecated/tasks-go-port-fleet-ops.md`](../docs/plans/deprecated/tasks-go-port-fleet-ops.md).
- **Retired tick prompt and runbook** — `PORTING.md` and `OPERATING.md`.
- **Reusable verification tools** — fixtures, runners, `porting/conform`, and
  the focused persistence evidence named by the velocity plan.

The manifest and evidence directories no longer measure completion.

## Getting started

```sh
porting/conform                      # the conformance verdict, phase1, both sides
porting/conform --quick              # the eight-case smoke subset, ~4s
```

Do not start the fleet or edit slice records as part of the active port.
