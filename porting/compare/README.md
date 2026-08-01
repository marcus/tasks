# `porting/compare/` — the conformance comparator

The runner produces observations. This turns two sets of them into a **verdict
with a reason**: for every difference, which field, which layer of the contract
it belongs to, and which of the playbook's classifications it falls under.

```console
$ porting/compare/compare porting/evidence/phase1/ruby /tmp/go-observations
$ porting/compare/compare --json BASELINE CANDIDATE     # structured report
$ porting/compare/audit porting/evidence/phase1/ruby    # what can this corpus prove?
$ porting/compare/validate porting/evidence/phase1/ruby # schema conformance
$ porting/compare/seed --list                           # the gate's seeded defects
```

Exit status, so CI and an agent can gate on it without parsing anything:

| | |
|---|---|
| `0` | every paired case matched, or differed only where Marcus has recorded an intentional difference |
| `1` | at least one gate-failing difference |
| `2` | usage or configuration error |

The contract is not defined here. It is defined in
[`porting/specs/errors.md`](../specs/errors.md) (what is contract and what is
formatting) and [`porting/specs/determinism.md`](../specs/determinism.md) (pins
and the four normalizations). Every comparison rule in this directory cites the
sentence it implements, and that citation travels into the report as the
finding's `rule` — so a mismatch arrives with its justification attached rather
than requiring the reader to go and find it.

---

## Structure

```text
porting/compare/
  compare              the comparator; exit status is the gate
  audit                corpus coverage — what a green run does NOT prove
  seed                 the gate's instrument: deliberately-wrong observation sets
  validate             schema conformance (python jsonschema, Draft202012Validator)
  dispositions.jsonl   machine-readable index into ../intentional-differences.md
  lib/
    comparator.rb      pairing, orchestration, exclusions, the report
    normalize.rb       the four normalizations, each with its justification
    finding.rb         the typed result
    diffs.rb           difference descriptions (JSON paths, byte offsets, line diffs)
    report.rb          formatting, and only formatting
    dimensions/
      cli.rb           process surface: argv, pins, exit status, stdout, stderr
      files.rb         store and archive bytes, the file set, deltas, rollback
      journal.rb       index bytes, states, blobs
      revisions.rb     store and resource revision tokens
      performance.rb   metrics — advisory, structurally unable to fail anything
      http.rb          Phase 5 stub, compared structurally in the meantime
```

The playbook's control-plane sketch lists five subdivisions (`cli`, `http`,
`files`, `journal`, `performance`). This has six. **`revisions` was added
deliberately**: a revision token is one of the five mismatch classes the Phase 1
gate must detect, and it belongs to neither the process surface nor the file
surface. It comes from the implementation's own probe precisely so that a port
computing it differently produces a mismatch rather than being silently agreed
with by a harness-side derivation (`runners/README.md` § "Why revision tokens
come from a probe"). Folding it into `files` would have buried the one signal
the gate names.

### Typed first, formatted second

`lib/` produces a `Finding` per difference and a JSON report; `report.rb` renders
that report for a terminal and can change nothing. The verdict, the exit status
and the classification are all decided before any formatting happens, so the
human view and the `--json` view can never disagree.

---

## What is compared, and how

Three strengths of binding, from `errors.md`:

| Layer | Compared as | Why |
|---|---|---|
| Exit status | exactly, every case | `1` vs `2` is a product feature agents branch on; "both nonzero" is never a match |
| Structured stdout | **parsed data** | consumed by parsers: same keys, same values, same types, array order significant, object key order **not** compared |
| stderr, human stdout | **bytes** | the error UX; wording, whitespace, punctuation and line order are all contract |
| Store / archive files | **bytes**, including key order | a durable format two implementations must round-trip |
| Journal index and blobs | **bytes** | the index is the proof; the structure above it is for readability |
| Revision tokens | exactly | one wrong character is a lost-update bug for every conditional write |
| `environment.*` | recorded, advisory | a tzdb or platform difference is attribution, never an assertion |
| `metrics.*` | never | performance is a separate gate with its own budgets |
| `observation_id`, `fixture.copy_root`, `implementation.*`, `notes`, stream `text` | never | harness and provenance metadata |

### The asymmetry is deliberate

The same reordering of object keys is **invisible on stdout** and **fatal in the
store**. That is not an oversight in either direction:

- stdout JSON is a *message*. It is parsed. Key order carries no information, so
  comparing it would generate noise that buries real findings.
- the JSONL store is a *durable format*. Its bytes are what a merge driver
  resolves, what `git diff` shows, and what the next reader must round-trip.
  A port that writes the same records in a different key order has changed the
  file format.

Both halves are asserted together in `test/test_porting_compare.rb`
(`test_key_order_asymmetry_between_stdout_and_store`), so neither can be
"tidied" away without the other failing.

---

## Classification

Every finding carries one of the playbook's step-6 classes:

| Class | When | Gate |
|---|---|---|
| `go_defect` | the default for any real difference in a compared field | fails |
| `legacy_ruby_rule` | a Ruby behavior known to be wrong but preserved as the compatibility rule; only ever set from `dispositions.jsonl` | fails (Go must still match) |
| `nondeterminism` | `environment.*` differs — pin it, do not normalize the output | advisory |
| `intentional_difference` | recorded by **Marcus** in `../intentional-differences.md` and indexed in `dispositions.jsonl` | accepted |
| `missing_oracle_coverage` | a case observed on one side only; an HTTP exchange with no Phase 5 rules yet | fails |
| `harness_error` | the comparison itself is untrustworthy | fails |

`harness_error` is a sixth class, added on purpose and named here rather than
folded into one of the five. It covers: the two sides ran different argv, cwd,
stdin or fixture; they started from different tree digests; a pin was reported
`applied: false`; a process timed out; a stream was truncated before it could be
compared; or the two sides ran at different absolute paths without
`--cross-path`. None of those are statements about the port. Calling them
`go_defect` would blame the port for a harness fault; calling them
`missing_oracle_coverage` would make a broken run look like a gap in the corpus.
They fail the gate, because a comparison you cannot trust must not report PASS.

### Classification is never silent

An accepted difference is still **reported**, with its disposition attached and
severity `accepted`. Suppressing it entirely would silence the whole comparison
for that field, which is how a known exception quietly becomes a blind spot.
`dispositions.jsonl` requires a `record` field pointing at the section in
`../intentional-differences.md`, and loading refuses an entry without one — the
file's own rule is that a comparator exception with no written record is a
difference-hiding machine.

### `environment` differs

`errors.md`: a run whose two sides disagree in `environment` **and** also
elsewhere must be re-run with the environments matched before any difference is
classified. The comparator does not suppress those findings — they are real and
they still fail — it marks each one `requires_rerun` and says so in the summary.
What is withheld is the confident attribution, not the finding.

---

## The four normalizations, and why each is safe

The whole list, matching `determinism.md` § Normalizations one for one. Every
entry carries the sentence "a user cannot observe this because …". The report
echoes all four in its `normalizations` block, so a reader sees what was hidden
without reading this file.

**1. `observation_id` → `<observation-id>`.**
A user cannot observe this because it is minted by the harness *after* the
invocation exited. It is never written to the store, the journal, stdout or
stderr; no command prints it and no setting names it. It exists so a piece of
evidence can be cited.

**2. The copy-root prefix → `<copy-root>`.**
Applied to `fixture.copy_root`, `invocation.env[].value`,
`invocation.pins[].value`, and the decoded bytes of stdout and stderr. A user
cannot observe this because the prefix is the harness's choice of working
directory, handed to the implementation as `TASKS_DIR`/`HOME`/`XDG_*` and echoed
back. Everything *inside* the copy stays compared — the relative path, the file
set, the modes, every byte — so naming the wrong file within the copy is still a
failure. Both spellings of the root are rewritten (`/tmp/…` and the
`/private/tmp/…` form macOS canonicalises to), because they name one directory
and both are the harness's choice.

**Not** applied to file contents, including the journal index's `org` field.
`determinism.md` refuses that rewrite by name: rewriting bytes before digesting
them is exactly the move that makes a byte comparison stop meaning anything.

**3. The journal directory key → `<journal-key>`.**
Applied to **paths only**: `files.before[].path`, `files.after[].path`,
`files.deltas[].path`, `journal.index.path`. A user cannot observe this because
it is a private cache key under `XDG_STATE_HOME`: no command prints it, no
configuration names it, no documentation gives it a name, and its only job is to
keep two different task files from sharing one history. The property that
actually matters — different stores get different keys, the same store always
gets the same key — is a separate testable claim and is not what is hidden; only
the key's literal value is.

**4. `metrics.*` → advisory only.**
This is the one entry where the honest sentence is *"a user can observe it, but
not here"*: a slow port is a real user-visible problem, which is exactly why it
gets its own gate with its own budgets instead of being folded into byte
equality. `errors.md` requires that metrics can neither fail a conformance case
nor pass one, so the `performance` dimension is structurally incapable of
emitting a gate finding. `metrics.bytes_written` is deterministic and tempting to
promote; it stays out, because every byte it counts is already compared far more
precisely in the `files` dimension.

### Things deliberately NOT normalized

Each was considered and rejected, because a user *can* observe it:

- **The `org` path inside journal `index.json` bytes.** Cause removed instead:
  both sides run at the same absolute path. See below.
- **The lock sidecar `.tasks.jsonl.lock`,** including on reads. Whether the port
  creates it, when, and with what mode is exactly the platform-shaped behavior a
  port gets wrong.
- **Leftover atomic-write temp files.** A leftover `.tasks.jsonl.<pid>.<tid>.tmp`
  is a real finding — a crashed write — not noise.
- **File mode bits.** A chmod-600 store must not widen to 644 across an atomic
  replacement; dropping mode from the comparison is how that regression ships.
- **`Captured [YYYY-MM-DD]` bodies.** Store bytes a user reads. Pinned via
  `TASKS_PIN_NOW`, never normalized.
- **stderr wording.** Byte for byte, with only the copy-root rewrite.

### The same-absolute-path requirement

The journal index records the store's canonical absolute path *inside bytes the
harness digests*. Two sides at two paths therefore differ in `journal.index` by
construction.

The comparator refuses that comparison rather than quietly de-scoping it: if the
copy roots differ and `--cross-path` was not passed, it emits a `harness_error`
telling you to re-run both sides with the same `--work`. With `--cross-path`, the
journal index is excluded and the exclusion is **recorded in the report** —
`determinism.md` requires that a cross-path comparison "must exclude
`journal.index` and say so in its report; a silent exclusion is a defect."

---

## The rollback gap

**Read this before treating a green comparison as complete.**

`files.rolled_back` is `null` in every observation, on both sides, always.

The Ruby CLI performs a genuine write-then-revert — `Store#post_write_failure`
restores the previous bytes and the result carries `rolled_back: true` — but it
surfaces that fact to the outside world only as an extra sentence on stderr:

```text
could not capture (no "Inbox" section found?)
file failed validation after the edit — run `tasks check`
```

There is no machine-readable signal. `--json` on a failed mutation of this shape
prints **nothing at all** on stdout; the entire structured-error layer is absent
for that path. The runner therefore left `files.rolled_back` honestly unset
rather than parsing Ruby prose into a language-neutral protocol, which was the
right call: a protocol that pattern-matches one implementation's wording cannot
be implemented from its specification.

### What this means for the gate

A rollback divergence **is detected** — as a `process.stderr` byte difference,
classified `go_defect`. It is **not labelled**. Concretely:

- A port that performs the write-then-revert but prints a different sentence:
  **detected** (stderr bytes).
- A port that never writes and prints the never-wrote sentence where Ruby prints
  the rollback sentence: **detected** (stderr bytes).
- A port that fails to restore the previous bytes after a failed validation:
  **detected** (store bytes).
- A port that takes the *wrong internal path* — never writing where Ruby wrote
  and reverted — while printing byte-identical stderr: **not detected.** Exit
  status, `files.deltas` and store bytes are identical in both cases; `errors.md`
  says so itself ("the filesystem cannot tell you"), which is why the field
  exists.

That last row is the real hole, and it is narrow. It is also not closable by the
harness: the harness can only compare what the implementation reports.

### What `bin/tasks` would need

One field. On a failed mutation under `--json`, emit the error envelope that
`claim` already emits, with a rollback flag:

```json
{"error": "store_invalid",
 "action": "capture",
 "rolled_back": true,
 "message": "file failed validation after the edit — run `tasks check`"}
```

`bin/tasks` already computes this — `mutation_result_failed` branches on
`result.rolled_back?` to choose the sentence, then discards the boolean. The
change is to emit the envelope on stdout under `--json` instead of only warning,
and the runner then reads `files.rolled_back` from it exactly as it already reads
`revisions.touched_ids` from the `--json` mutation payload. Filed as a td issue;
not done here, because `bin/tasks` is another agent's tree.

### Also missing from the corpus

`porting/compare/audit` reports two gaps in the Phase 1 case list, both real:

1. **No case exits `2`.** Every one of the 27 cases exits 0 or 1. A port that
   collapsed `2` into `1` would pass the entire corpus — and `errors.md` calls
   the `1` vs `2` distinction "the most important single assertion in the whole
   error surface". The comparator detects the collapse (proven by seeding it);
   the *corpus* never presents it. One case with an unresolvable or ambiguous ref
   closes this.
2. **No case produces a rollback.** Not an oversight in the case list: it is
   unreachable through the runner protocol. `Store#create_preflight_failure`
   validates exactly the same files as `post_write_failure`, so a store that is
   already invalid is refused *before* the write, and post-write validation
   cannot fire from fixture content alone. It is reachable through a write
   *failure* — a read-only store directory with the lock sidecar already
   present, which reproduces the diagnostic above — but the runner creates the
   copy root itself, so no fixture can make it read-only. Closing this needs
   either a fixture that can constrain its own copy root, or a fault-injection
   hook in the runner.

Neither gap is fixable in this directory. Both are reported by `audit`, which
exits nonzero while they stand, so they cannot be forgotten.

---

## Seeding, and why the negative control matters

`porting/compare/seed` writes a deliberately-wrong copy of an observation set. A
harness that has never been shown a mismatch is an untested assertion, so the
Phase 1 gate stands up a hypothetical Go implementation by perturbing the Ruby
baseline (`porting/evidence/gate`).

Every seed is **subtle on purpose**. A wholly different exit code proves almost
nothing — any comparator catches it. A real port defect is an off-by-one token,
one reordered key, one omitted field, so those are what is seeded, each
annotated with the Go failure mode it models (`seed --list`).

One seed, `json-key-order`, is a **negative control**: it must produce *no*
finding. An over-strict comparator is as useless as a blind one, and it fails in
a way that looks like diligence — every run red, every finding noise, the real
mismatch invisible. Proving silence where the spec requires silence is part of
the gate.

---

## Tests

`test/test_porting_compare.rb`, in the main suite (`ruby test/all.rb`). It runs
against the committed baseline rather than hand-written observations: a
hand-written observation can accidentally be shaped to fit the comparator, and
then the test passes because both sides are wrong.

The tests were **mutation-tested**. Each of the five detection paths was broken
in turn — with a plausible mistake, not a syntax error — and the corresponding
test was confirmed to fail. The mutations and their failures are recorded in
[`../evidence/phase1/GATE.md`](../evidence/phase1/GATE.md).
