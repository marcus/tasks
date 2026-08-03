# ADR-0013: Go becomes the authoritative core; Ruby retires last

Status: Proposed — not accepted. Conditional on Phase 1 evidence (epic
`td-27fbf5`); see ADR-0020 for the conditions that would end it instead.

Date: 2026-08-01

## Context

`tasks` is a Ruby product: roughly 12,000 lines under `lib/tasks`, another
12,000 in the TUI, a 2,900-line CLI entrypoint, a 1,500-line HTTP adapter, a
3,500-line OpenAPI contract, and more than 2,000 named tests. It runs where MRI
runs, which in practice means the Macs. It cannot be compiled for Windows as a
single artifact, cannot be linked into an iOS or Android application, and
cannot be imported as a Bubble Tea component by sidecar (ADR-0017).

Those are the only reasons on the table. There is no performance complaint, no
maintainability complaint, and no feature that Ruby is blocking. A port bought
for any reason other than reach is a port bought for nothing.

The counterweight is that the Ruby implementation is currently the only
definition of what `tasks` does. Much of the product's real behavior lives in
small semantics — revision scopes, rollback paths, availability inheritance,
recurrence edges — that no document fully captures. Replacing the
implementation means replacing the specification with a reconstruction of it.

## Decision drivers

- Reach: macOS, Windows, iPhone, Android, and embeddable in a Go host process.
- The port must be behavior-preserving, because behavior preservation is the
  only property that can be mechanically checked against the existing product.
- There must be exactly one authoritative implementation at any moment. Two
  authorities is the failure mode this decision exists to prevent.
- The decision must remain reversible for as long as it is cheap to reverse.

## Considered options

1. **Stay on Ruby.** Zero risk to today's product; permanently forecloses
   Windows, mobile, and sidecar embedding. This remains the option if Phase 1's
   estimate is worse than the reach is worth.
2. **Keep Ruby and add a thin Go shell** that shells out to the Ruby CLI.
   Cheap, but ships MRI to every platform and gains nothing on mobile or inside
   sidecar, where the whole point is in-process embedding.
3. **Rewrite in Go with a redesigned model, format, or CLI.** Tempting while
   the code is open anyway, and fatal: it removes the oracle. If the Go output
   is allowed to differ by design, no automated comparison can tell an intended
   difference from a defect.
4. **Behavior-preserving port to Go, Ruby authoritative until conformance is
   green, Ruby retired only after a rollback window closes.**

## Decision

Choose option 4, conditionally.

The final shape is a Go domain and application core with Go CLI, HTTP, TUI, and
native-binding adapters over it. Ruby's role for the duration of the port is
**oracle, not legacy**: it defines correct behavior, and a Ruby/Go disagreement
is a Go defect until Marcus rules otherwise (`porting/intentional-differences.md`).

Authority transfers once, late, and in one step. During the port the Go
executables ship under `tasks-go`, `tasks-api-go`, and `tasks-tui-go` and are
never the default. Promotion to the public command names happens only against
the recorded evidence in the port plan's Phase 8 checklist — a green manifest,
an empty mainline-parity queue, both implementations passing the same fixture
corpus, preview survival under real daily use, macOS and Windows install tests,
and a rollback rehearsed against data last written by Go.

**Ruby is deleted last and separately.** Removing `lib/tasks` is housekeeping
performed after the compatibility window closes and no rollback path still
depends on it. It is not the definition of success, and scheduling it earlier
would convert a reversible decision into an irreversible one for no gain.

Never dual-write a live store. Shadow reads are safe; parallel writers are not
(`porting/PORTING.md`, "Fixtures only").

## What this ADR cannot decide yet

**Whether the port is worth its cost.** This ADR records the shape a port would
take, not an approval to take it. Phase 1 — the conformance harness — is the
honest estimate, and the evidence that would settle it is concrete: the number
of manifest slices that reach green per unit of effort once the harness exists,
and the size of the oracle gap the harness exposes. The corpus has already
found gaps the Ruby test suite does not cover (ADR-0015, ADR-0020), and each
one is characterization work the port pays for before it writes any Go.

Marcus decides after Phase 1. Nothing in this ADR should be read as arguing he
should say yes.

## Consequences

- Two implementations coexist for the length of the port. Every Ruby change
  during that window must end as **ported**, **not applicable**, or **blocking
  cutover** in `porting/manifest.jsonl`, or CI fails on drift. That tax is paid
  on ordinary Ruby feature work, by whoever does it, for months.
- Feature work on `tasks` competes with the port for the same attention, and
  every feature landed in Ruby during the port lengthens the port.
- The existing ADRs 0001–0012 remain the domain specification. The port
  preserves their decisions; it does not get to revisit them opportunistically
  (`porting/PORTING.md`, "Behavior-preserving only").
- Rollback stays trivially cheap only because of byte compatibility
  (ADR-0015). If that property is lost, this decision loses its escape hatch,
  which is why it is a stop condition in ADR-0020.
- Ruby's eventual retirement is a consequence of this decision, not a separate
  goal. If the port stalls at, say, a green desktop core and an unfinished
  mobile binding, Ruby staying is a legitimate resting state, not a failure.

## Related

- Plan: [`docs/plans/deprecated/tasks-go-port-plan.md`](../plans/deprecated/tasks-go-port-plan.md)
- Method: [`docs/plans/deprecated/language-porting-playbook.md`](../plans/deprecated/language-porting-playbook.md)
- Fleet rules: [`porting/PORTING.md`](../../porting/PORTING.md)
- Stop conditions: [ADR-0020](0020-port-stop-conditions.md)
