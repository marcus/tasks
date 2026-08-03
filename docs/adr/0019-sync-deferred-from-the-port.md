# ADR-0019: Sync is deferred from the language port

Status: Proposed — not accepted. Conditional on Phase 1 evidence (epic
`td-27fbf5`).

Date: 2026-08-01

## Context

A Go core gives the phone an offline engine. It does not decide how Mac,
Windows, iPhone, and Android converge. The moment a device other than the
desktop can write, "how do these agree" becomes a real question — and it is a
question the port cannot answer, because it has no oracle for it. Ruby has no
multi-device sync behavior to preserve; today's convergence is a Git repository
and a merge driver run by a human on a desktop.

That is the whole argument for deferral: the port's method is behavior
preservation against a Ruby oracle (`porting/PORTING.md`). Sync has no oracle.
Putting it in the port's acceptance criteria would mean the port could not
finish until a *new product* was designed, and would mean the conformance
harness could no longer say whether the port was correct.

The risk of deferring is the opposite one: shipping a core whose architecture
makes sync expensive to add later.

## Decision drivers

- The port's acceptance criteria must stay mechanically checkable.
- Sync's hard part must not be re-solved later from scratch.
- Today's desktop Git reconciliation must keep working unchanged throughout.
- The port must not foreclose the sync shapes that look plausible.

## Considered options

1. **Design and build sync as part of the port.** Mixes an unverifiable new
   product with a verifiable replacement, and removes the only trustworthy
   signal for whether the port is correct.
2. **Defer sync entirely and think about it later.** Risks an architecture that
   assumes one writer and one filesystem, making the merge library expensive to
   lift out.
3. **Keep sync out of the port's acceptance criteria, but port the merge rules
   as a transport-independent library and leave a `SyncTransport` seam.**

## Decision

Choose option 3.

Sync is **not** in the port's acceptance criteria. The local Go core must reach
parity first, and "parity" never means "and also converges across devices".

What the port does decide, because it shapes the architecture:

**The field-aware merge is ported as a library, not as a Git driver.** The merge
is the hard part of sync, it already exists, and it is transport-independent:
stable IDs align records; tags union; state progression and timestamps resolve
selected fields; a delegation marker merges as one atomic value; output remains
canonical. Port it with property tests for determinism, commutativity,
associativity where promised, idempotence, and refusal without overwriting
malformed input.

**Transport goes behind a `SyncTransport` adapter.** Git then becomes one
transport rather than the architecture, and today's desktop reconciliation keeps
working unchanged — the Git merge driver and installer are ported as they exist
(Phase 4), because they are current behavior with an oracle.

That is the entire commitment. Everything below is recorded direction, not
decision.

## Direction, recorded so the port leaves room for it

For the phone, the first transport candidate is the same Git hub, embedded: a
pure-Go client (go-git) that fetches, three-way merges the live and archive
files with the ported merge rules, commits, and pushes at foreground and
background-refresh boundaries. Git is normally a poor invisible phone protocol
because conflicts need a human; a deterministic, self-resolving merge removes
exactly that objection, and a two-file store measured in kilobytes removes the
size one.

The named risks: credential storage in the keychain; iOS background-execution
limits; go-git's partial merge support — the three-way merge is application
code, Git supplies only the merge base; and unbounded archive growth, for which
the answer is a shallow clone on the phone and archive compaction as a desktop
job.

If embedded Git proves fragile, the fallback is the same merge library behind a
dumber transport: iCloud file coordination on Apple platforms, or later a small
sync service replaying the same merge server-side. The merge library survives
every variant, which is why it is the part the port commits to.

## What this ADR cannot decide yet

Identity, authentication, deletion semantics across devices, ordering
conflicts, retries, encryption, and recovery from a long-offline device are all
unsolved in every variant above. None of them can be settled by evidence this
port produces — they need a product decision about what multi-device `tasks`
*is*. That is precisely why sync is a separate product rather than a phase.

The one piece of port evidence that bears on it: whether the ported merge
rules hold under property testing (Phase 4 gate). A merge that turns out not to
be deterministic or idempotent in practice would invalidate the go-git
direction above, and would be worth knowing before any sync work starts.

## Consequences

- A phone with the native binding (ADR-0018) and no sync is the expected
  outcome of a successful port. If Marcus's actual want is "my tasks on my
  phone, current", this port delivers the engine and not the want. That gap
  should be stated plainly whenever the port's value is being weighed.
- The merge library gets ported at full rigor for a consumer (sync) that may
  never exist. It is not speculative work — the Git merge driver is current
  desktop behavior and must be ported anyway — but the *library shape* and the
  `SyncTransport` seam are.
- Desktop Git reconciliation is unchanged by the port, before and after
  cutover.
- Archive growth becomes a live concern the first time a phone clones the
  repository. Nothing in this port addresses it.

## Related

- [ADR-0015](0015-byte-compatible-jsonl-and-journal.md) — canonical output, on
  which merge determinism depends
- [ADR-0018](0018-native-binding-contract.md) — the offline engine sync would
  eventually serve
- Plan: [`docs/plans/deprecated/tasks-go-port-plan.md`](../plans/deprecated/tasks-go-port-plan.md),
  "Mobile sync remains a separate product"
