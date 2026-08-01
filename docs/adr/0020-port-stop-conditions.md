# ADR-0020: Measurable stop conditions for the Go port

Status: Proposed — not accepted. Conditional on Phase 1 evidence (epic
`td-27fbf5`). This ADR is the one that must be accepted *before* any Go is
written, because a stop condition adopted after the sunk cost accumulates is
not a stop condition.

Date: 2026-08-01

## Context

The port plan names three conditions under which the port should stop rather
than push through sunk cost: a required data-format break, an unmatchable
temporal behavior, and a permanent dual domain implementation
(`docs/plans/active/tasks-go-port-plan.md`, Phase 0). As prose they are
correct and useless. "Cannot match temporal behavior" is exactly the sentence a
motivated engineer at month five reads as "cannot match *yet*".

The failure mode this ADR guards against is specific and well documented in
this repository's own porting rules: the temptation to resolve a disagreement
by moving the expected result. `porting/PORTING.md` calls updating an expected
result because Go produced it "the one unforgivable move". A stop condition is
the same hazard at a larger scale — the pressure is not to edit one expected
value but to redefine what the port was for.

So each condition needs a **trip test**: an observable, recorded fact that
either exists or does not, that a porting agent can evaluate without
authorization and without judgment about whether continuing is wise.

## Decision drivers

- A condition must be evaluable from recorded evidence, not from opinion.
- It must be evaluable by an *agent* mid-slice, because that is who encounters
  it first, and agents start each iteration with no memory.
- It must have false-positive protection, or it will be ignored after the third
  spurious trip.
- Tripping must halt work and route to Marcus. It must not be resolvable by the
  agent that found it, and it must not be resolvable by argument.

## Considered options

1. **Prose conditions, judged when they arise.** What the plan has today.
   Unfalsifiable under pressure.
2. **A single overall budget** (time, or slices-per-week). Measurable, but it
   measures cost rather than feasibility, and a port can be on schedule while
   producing a product that cannot roll back.
3. **Named conditions, each with a trip test, an exclusion list, a declaring
   role, and an evidence location.**

## Decision

Choose option 3. Four conditions, three from the plan plus a cost condition
recorded separately because it is a different kind of stop.

Any agent may **trip** a condition; only Marcus may clear one. Tripping is not a
recommendation to abandon the port — it is a mandatory halt-and-decide. The
outcomes available to Marcus are: accept the condition as an intentional
difference (`porting/intentional-differences.md`), narrow the port's scope so
the condition no longer applies, or stop.

### Condition 1 — Data-format break

**Trip test.** Any one of the following is recorded as an evidence artifact:

- a `porting/compare/files` report showing Go output that is not byte-identical
  to Ruby output for a supported fixture, where the divergence has been
  classified as *unfixable in Go without changing the on-disk format* — not as a
  Go defect;
- a proposal that any existing store or journal be rewritten, migrated, or
  re-canonicalized for the Go implementation to read or write it;
- a journal entry Ruby cannot apply, or a Ruby journal entry Go cannot apply,
  after the cross-version proof in Phase 4; or
- the `meta` version being advanced past 2 for the port's benefit.

**Not tripped by.** A byte mismatch still classified as a Go defect. An emitter
bug. A missing oracle case. A fixture that was wrong. Two copies at different
absolute paths producing different journal `index.json` bytes — that is the
staging requirement in `porting/specs/determinism.md`, not a format break.

**Why this one is fatal.** Byte compatibility is what makes rollback a binary
swap (ADR-0015). Lose it and ADR-0013's reversibility goes with it, which
means the port stops being a replacement and starts being a migration — a
different, much larger decision that Marcus has not been asked to make.

**Declared by.** Any agent, on the slice's td issue, marked blocked. **Evidence:**
`porting/evidence/<slice-id>/`.

### Condition 2 — Unmatchable temporal behavior

**Trip test.** A temporal conformance case is recorded as failing where all
three hold:

- the case is drawn from the table-driven corpus extracted from the Ruby tests
  (DST gaps and folds, leap days, month ends, multiple weekdays, time-zone
  fallback, 12/24-hour presentation, date-order configuration, out-of-range
  years, recurrence on parent tasks);
- the divergence is attributable to a semantic difference between Ruby's TZInfo
  2.x resolution and Go's `time`/zoneinfo resolution — not to the port's own
  logic; and
- both sides are running the same pinned zoneinfo version.

The third clause matters: ADR-0010 exposes the time-zone database version
through the config/meta surfaces precisely so a tzdata skew is diagnosable
rather than mysterious. A mismatch under differing tzdata is a harness defect.

**Not tripped by.** A parsing bug. A recurrence bug. A missing case. A
divergence that a Go-side implementation of the same rule would fix, however
tedious that implementation is — tedium is cost, not infeasibility.

**Why this one is fatal.** ADR-0010's semantics are load-bearing product
behavior: an all-day deadline stays on time through its calendar day, a timed
one does not; floating values resolve in the evaluation zone, fixed ones in
their stored zone; an ambiguous local time defaults to its earlier instant
unless `fold: 1`; a candidate landing in a DST gap is skipped by advancing
again. A Go core that cannot reproduce these produces *silently wrong dates* —
the failure users notice last and trust least.

**Declared by.** Any agent, on the temporal campaign's issue. **Evidence:**
`porting/evidence/<slice-id>/` plus the failing corpus rows.

### Condition 3 — Permanent dual domain implementation

**Trip test.** At the end of any campaign, a domain rule exists in both Ruby and
Go with **no scheduled removal of either**, and one of the following is
recorded:

- a manifest entry that cannot end **ported**, **not applicable**, or **blocking
  cutover** (`porting/PORTING.md`, "Drift") because the behavior must exist in
  both languages indefinitely;
- a design in which the Go binary shells out to Ruby, or Ruby to Go, in a
  shipped configuration; or
- a shipped configuration in which both implementations write task data.

**Not tripped by.** Ruby and Go coexisting *during* the port — that is the plan.
Ruby remaining authoritative while Go is a preview. Ruby being retained after
cutover for the rollback window (ADR-0013 explicitly schedules its removal
last). Two *adapter* implementations of one port, which is ADR-0014's design.

The distinction is scheduled removal. "Both exist" is the plan; "both exist
forever" is the stop.

**Why this one is fatal.** A permanent dual implementation is the outcome where
the port's costs are paid and none of its benefits arrive: two places to change
every rule, two sets of tests, and no platform reach, because a mobile or
Windows target cannot carry the Ruby half.

**Declared by.** The campaign's reviewer, at campaign close. **Evidence:** the
manifest entry.

### Condition 4 — The estimate stops being credible (added here, not in the plan)

**Trip test.** Both of:

- fewer than half the slices in a campaign reach green within twice the effort
  the campaign's first three green slices required; and
- the shortfall is attributable to newly discovered behavior rather than to
  known, sized work.

**Not tripped by.** A hard campaign taking longer than an easy one. A single
slice being expensive. Effort spent on oracle gaps that were *identified* in
Phase 1 — those are already in the estimate.

**Why it is here.** Phase 1 exists to produce an honest estimate
(`docs/plans/active/tasks-go-port-plan.md`, closing paragraph). An estimate is
only honest while it is still being checked. This condition is the one that
makes "the port is bigger than we thought" a recorded fact rather than an
accumulating mood.

**Declared by.** Marcus, from manifest progress. This is the one condition an
agent should not declare, because it requires judgment about what was known
when.

## Rules that make the conditions real

- **A tripped condition halts the slice.** The agent files it in td, marks the
  slice blocked, and exits (`porting/PORTING.md`, "Stop and escalate"). It does
  not attempt a workaround, and it does not continue on the same slice while
  awaiting a ruling.
- **Only Marcus clears a trip.** Not the finding agent, not a reviewer, not two
  agents agreeing. This mirrors the intentional-difference rule and exists for
  the same reason.
- **A cleared trip is recorded where the comparator can see it.** An accepted
  difference gets a section in `porting/intentional-differences.md` *and* a
  comparator disposition. A difference recorded but not reflected in the
  comparator will be re-reported forever; a comparator exception not recorded is
  a difference-hiding machine. Both are review failures.
- **No condition may be narrowed by editing an expected result.** If a trip test
  references a fixture or corpus row, changing that fixture to un-trip it is the
  unforgivable move at a larger scale.
- **Stopping is a success state.** A port that stops at Condition 1 in month
  three, with a working conformance harness and a documented reason, has
  produced something valuable: an executable specification of `tasks` that Ruby
  keeps benefiting from, and a decision made on evidence. That is the outcome
  this ADR is written to make available.

## Consequences

- Agents can halt the port without authorization. That is the point, and it
  will occasionally be wrong — a false trip costs Marcus a ruling. The
  exclusion lists exist to keep that rate low; if it is not low, the trip tests
  are wrong and should be revised deliberately, not ignored.
- Conditions 1–3 are evaluable only once the conformance harness and evidence
  layout exist. Before Phase 1, they are unenforceable, which is another reason
  the harness precedes the port rather than accompanying it.
- Condition 4 requires that per-campaign effort actually be recorded. If it is
  not, the condition is decorative.
- These conditions do not cover every way the port could go badly — packaging,
  Windows filesystem behavior, and `gomobile` lifetimes are all real risks with
  no stop condition here. They are covered by phase gates instead, because they
  are *cost* risks rather than *feasibility* risks. Only feasibility gets a
  stop condition.

## Related

- [ADR-0013](0013-go-as-the-authoritative-core.md) — the decision these
  conditions bound
- [ADR-0015](0015-byte-compatible-jsonl-and-journal.md) — Condition 1's subject
- [ADR-0010](0010-temporal-values-and-time-zones.md) — Condition 2's subject
- [ADR-0014](0014-go-package-boundary.md) — why two adapters are not two domains
- Escalation procedure: [`porting/PORTING.md`](../../porting/PORTING.md)
- Difference record: [`porting/intentional-differences.md`](../../porting/intentional-differences.md)
