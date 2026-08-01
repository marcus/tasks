# ADR-0015: Byte-compatible JSONL and journal, both directions

Status: Proposed — not accepted. Conditional on Phase 1 evidence (epic
`td-27fbf5`).

Date: 2026-08-01

## Context

A behavior-preserving port's escape hatch is that reverting means putting the
old executable back, not migrating user data backward. That property is not
free — it holds only if either implementation can open data the other wrote,
and it degrades quietly: a Go writer that produces *semantically* equivalent
JSONL passes every parse test, produces a noisy Git diff on the first write,
and makes a byte-exact undo impossible.

The store carries several coupled contracts, documented in
`docs/conventions.md` and exercised by `porting/fixtures/`:

- a metadata record first, declaring schema version 2;
- fixed key order and omitted defaults;
- stable eight-hex IDs;
- explicit parents and strict depth-first pre-order;
- live and archive files as one consistency boundary;
- semantic update stamps that exclude non-semantic rewrites; and
- validation after every mutation, with rollback on failure.

Undo is durable shared state, not a TUI convenience: CLI and TUI use one
content-addressed journal, editor saves coalesce across fresh Store instances,
redo branches truncate, and corruption is detected from hashes. Undo restores
**exact prior bytes**. Reconstructing a logically equivalent file would change
formatting, update stamps, and Git diffs — and would make
`test_undo_restores_byte_exact_file_and_redo_re_deletes` unportable as an
oracle.

Three findings from building the fixture corpus bear directly on this decision.

**`tasks check` does not validate key order.** `Check` works on parsed hashes
and never sees serialized order, so `porting/fixtures/malformed/wrong-key-order`
lints clean and exits 0. A port must match that lenient `check` *and* still
emit canonical order on write. Byte compatibility therefore cannot be verified
by running `check` on Go output; it is a `porting/compare/files` obligation.

**`Tasks::Atomic` is well covered except where it matters most.**
`test/test_journal.rb:25-100` holds seven direct `Atomic.write` tests: the temp
sibling, the inode-swapping rename, permission carry-over, symlink and
dangling-symlink following, and a chmod-refusing filesystem. Those are a usable
oracle and the port should be held to them.

What is genuinely unproven is **durability**: no test under `test/` references
`fsync` at all, so neither the file `fsync` nor the directory `fsync` that makes
a rename survive power loss is characterized. That is the narrow gap the
`store-canonical-write` manifest entry should record — writing a durability
characterization case before any Go is written, not a blanket claim that the
whole of `Atomic` is untested.

**The journal index records the store's canonical absolute path.** The `org`
field inside `index.json` is a real guard: a journal whose `org` does not match
refuses to apply. Two fixture copies at two paths therefore produce two
different index files, inside bytes the harness digests
(`porting/specs/determinism.md`).

## Decision drivers

- Rollback must remain "restore the old binary", never "migrate data".
- Git diffs must stay calm while the implementation underneath changes.
- Undo must survive an implementation swap mid-history.
- Verification must compare bytes, not re-derive equivalence.

## Considered options

1. **`encoding/json` plus a canonical-ish post-pass.** Will not reproduce
   Ruby's key order, float and escape formatting, or omitted defaults. It fails
   the requirement outright and hides the failure behind passing parse tests.
2. **Accept semantic equivalence, normalize the diff.** Cheapest to write, and
   it makes rollback a data migration, forfeits byte-exact undo, and turns the
   first Go write of the real store into a whole-file Git diff.
3. **A one-way migration to a Go-native format.** Ends the compatibility
   problem by ending reversibility. This is a stop condition, not an option
   (ADR-0020).
4. **A hand-written canonical emitter, fuzzed against parse–emit round trips,
   as the only writer.**

## Decision

Choose option 4.

The Go canonical emitter is the only writer. `encoding/json` may parse; it may
never produce a byte destined for a store, an archive, or a journal blob. This
is already a non-negotiable for the porting fleet (`porting/PORTING.md`) and
this ADR is where the reasoning lives.

The obligation is bidirectional and byte-level:

- The Go writer emits byte-identical canonical JSONL for every supported Ruby
  fixture, including nested key order, omitted defaults, and the preserved
  relative position of unknown keys from a newer binary
  (`porting/fixtures/compat/forward-compat-unknown-keys`).
- The Go journal reads all existing Ruby journal data, and Ruby reads journals
  Go wrote. Cross-version proof runs both ways: Ruby writes and Go undoes; Go
  writes and Ruby undoes.
- Undo restores prior bytes exactly. A logically equivalent reconstruction is a
  defect.
- The version gate is contract in both implementations. Any declared `meta`
  version other than 2 is refused identically — older or newer — with nothing
  written and no migration offered. Schema v1 and the org importer are deleted,
  not ported (`td-09f7de`); what a port carries is the refusal, pinned by
  `porting/fixtures/compat/future-schema-v3`. A binary that treated an unknown
  future version as writable would silently downgrade a newer store.

  The **read** half of that clause had no oracle behind it until `td-9f3dd0`:
  the CLI's read commands used the lenient read path and printed a foreign
  store with exit 0, so "refused on read" was an assertion the ADR made and
  nothing tested. It is now enforced by `test/test_schema_gate_reads.rb`
  (every read command, against both an older and a structurally different
  newer store, with exit status and message matched against the strict paths)
  and by `test/test_cli_json_coverage.rb`, which enumerates every `--json`
  command against an unsupported store so the refusal is structured on all of
  them. A port that reproduces the lenient read now fails the oracle instead of
  inheriting the gap. The gate applies to both files: `unsupported_schema_source`
  consults live and archive, and default `tasks check` reports an archive whose
  version header is foreign so the refusal's own advice terminates.

The canonical emitter is a first-phase cost, not a discovery. It belongs in the
estimate before the first slice that writes.

Conformance runs are staged so the journal-index path is not a difference to
normalize. **Both implementations run against copies at the same absolute
path** — sequentially, or via per-side mounts resolving to one path. Rewriting
the copy root inside bytes before digesting them is the move that makes a byte
comparison stop meaning anything. Cross-path runs remain useful and are
compared with the journal index excluded, with that exclusion reported rather
than silent.

The persistence port needs failure injection at each step: lock acquisition,
temp creation, write, file flush, validation, rename, directory flush, journal
append, and cleanup. A green happy path does not prove crash safety.

## What this ADR cannot decide yet

**Whether byte identity is achievable on Windows.** Windows has no
rename-over-existing with the same semantics as the macOS/Unix path the store
relies on today. Whether an `AtomicWriter` implementation can preserve
durability and byte identity there, while another process holds the file open,
is what the `store-canonical-write` slice must surface — deliberately early,
rather than at cutover. If it cannot, ADR-0020's data-format stop condition is
what that finding trips.

**Whether `Tasks::Atomic`'s durability contract can be matched.** Symlink
following, permission carry-over, and the rename semantics are already
characterized by `test/test_journal.rb:25-100` and are a contract Go must meet.
The `fsync` behavior is not characterized at all, so it will be *defined* by the
durability case that slice writes. Until it exists, no ADR can assert that half
of the contract; it can only require that it be captured from Ruby before Go is
written, and never blessed from Go output.

## Consequences

- Rollback stays a binary swap. This is the single largest reason the port
  remains reversible, and ADR-0013 depends on it.
- Writing a canonical emitter and fuzzing it is real, unglamorous work that
  arrives before any user-visible capability does.
- Every write-path slice is high risk under `porting/PORTING.md`: fault
  injection at every boundary, real competing processes, two independent
  reviews, independent approval.
- The port inherits Ruby's lenient `check` alongside its strict writer. Making
  `check` validate key order would be an improvement, and improvements are
  intentional-difference records for Marcus, never something a porting agent
  lands.
- Reads tolerating a torn store while writes refuse it
  (`porting/fixtures/adversarial/mid-write-torn-file`) is contract too: `tasks
  list` prints a silently incomplete store and exits 0. A port that "fixed"
  this would fail conformance, correctly.

## Related

- [ADR-0007](0007-concurrency-and-revisions.md) — revision scopes carried across
- [ADR-0008](0008-delete-semantics.md) — undoable delete, byte-exact restore
- [ADR-0010](0010-temporal-values-and-time-zones.md) — the schema-version-2
  barrier this refusal enforces
- Fixtures and findings: `porting/fixtures/README.md`
- Normalization rules: `porting/specs/determinism.md`
