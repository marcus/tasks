# `porting/compare/` — the conformance comparator

The runner produces observations. This turns two sets of them into a **verdict
with a reason**: for every difference, which field, which layer of the contract
it belongs to, and which of the playbook's classifications it falls under.

```console
$ porting/compare/compare porting/evidence/phase1/ruby /tmp/go-observations
$ porting/compare/compare --json BASELINE CANDIDATE     # structured report
$ porting/compare/audit porting/evidence/phase1/ruby    # what can this corpus prove?
$ porting/compare/validate porting/evidence/phase1/ruby # schema + cross-field coherence
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
                       plus the cross-field consistency pass JSON Schema cannot express
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
| `environment.tzdb_version`, `.locale` | recorded, advisory | a tzdb or locale difference is attribution, never an assertion |
| `environment.platform`, `.filesystem`, `.umask` | recorded, gate | harness-supplied host facts; identical by construction, so a disagreement is `harness_error`, not attribution |
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
| `nondeterminism` | `environment.tzdb_version`/`.locale` differs — pin it, do not normalize the output | advisory |
| `intentional_difference` | recorded by **Marcus** in `../intentional-differences.md` and indexed in `dispositions.jsonl` | accepted |
| `missing_oracle_coverage` | a case observed on one side only; an HTTP exchange with no Phase 5 rules yet | fails |
| `harness_error` | the comparison itself is untrustworthy | fails |

`harness_error` is a sixth class, added on purpose and named here rather than
folded into one of the five. It covers: the two sides ran different argv, cwd,
stdin or fixture; they started from different tree digests; a pin was **set and
then reported `applied: false`**; the two sides were handed different
`invocation.tty` descriptors; a process timed out; the two sides ran at
different absolute paths without `--cross-path`; or `environment.platform`,
`.filesystem`, or `.umask` disagree — these are harness-computed once per side
on the same machine, so a disagreement means the harness or the run is broken,
never the port (`porting/runners/README.md` § "environment.platform is a host
fact, not a probe answer").

"Set and then" is the whole of the pin rule, and the weaker reading —
`applied: false` alone — is wrong. Several inputs are deliberately pinned to
*unset* (the colour names, the test-only clock seam) and honestly report
themselves unapplied on every case; treating that as a harness error would
punish the runner for recording an input instead of leaving it invisible, which
is backwards. What replaces the blanket rule is that the two sides must AGREE
about `applied`, which is where one implementation honouring an input the other
ignores shows up. A truncated stream is no longer in this list either: it is
compared by `sha256_normalized`, which is a real comparison rather than an
untrustworthy one, and the report says so instead of raising a harness error. None of those are statements about the port. Calling them
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

`errors.md`: a run whose two sides disagree in `environment.tzdb_version` or
`.locale` **and** also elsewhere must be re-run with the environments matched
before any difference is classified. The comparator does not suppress those
findings — they are real and they still fail — it marks each one
`requires_rerun` and says so in the summary. What is withheld is the confident
attribution, not the finding.

`environment.platform`, `.filesystem`, and `.umask` do not participate in this
cascade. They are harness-supplied host facts (see Classification above), so a
disagreement there is a `harness_error` in its own right rather than a signal
that clouds every other finding — and unlike a tzdb or locale difference, no
re-run could ever resolve it: it would require two different implementations,
run on the same machine, to disagree about which machine they are on, forever.
Stamping every other finding `requires_rerun` because of it would be an
unsatisfiable instruction, not attribution.

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

## The rollback gap — closed, and by how much

**Read this before treating a green comparison as complete.**

A write that is performed and then reverted is a distinct product outcome that
leaves no trace on the filesystem: same exit status, same empty `files.deltas`,
same store bytes as a mutation that was refused before it wrote. `errors.md`
says it outright — "the filesystem cannot tell you" — which is why
`files.rolled_back` exists as its own field.

It used to be `null` in every observation, on both sides, always. The CLI knew
the fact and spent it on a sentence:

```text
could not capture (no "Inbox" section found?)
file failed validation after the edit — run `tasks check`
```

Under `--json` that path printed **nothing at all** on stdout. The runner
therefore left the field honestly unset rather than pattern-matching Ruby prose
into a language-neutral protocol.

Both halves are now closed, and it took a change on each side:

1. **`bin/tasks` states it.** `mutation_result_failed` emits the same error
   envelope `claim` already emits, carrying `rolled_back`, on every refusal of a
   command that promises a `--json` result. `porting/specs/errors.md` §
   "The `--json` error envelope" is the spec the Go port implements.
2. **The corpus reaches it.** `cli-capture-readonly-rollback` runs `capture`
   against a copy root the case declares unwritable (`copy_root_mode`), with the
   lock sidecar already present. The write raises, the previous bytes are
   restored, and stdout carries `"rolled_back":true`. See
   `porting/runners/README.md` § "A failing write, and why the mode lives on the
   case" for why the mode is a property of the case and not of the fixture.

### What the gate now proves, and what it still does not

Four port defects, and where each is caught:

- Performs the write-then-revert but prints a different sentence: **stderr
  bytes.**
- Never writes and prints the never-wrote sentence: **stderr bytes.**
- Fails to restore the previous bytes after a failed write: **store bytes.**
- Takes the *wrong internal path* — never writing where Ruby wrote and
  reverted — while printing byte-identical stderr: **`files.rolled_back`.**
  This was the hole. It is the seeded `rollback` mismatch, which touches only
  the label and the envelope it is read from and leaves stderr alone, so the
  detection can come from nothing else.

Still true, and stated rather than buried:

- **Exactly one case exercises the class.** One case is not a regime.
- **It is a failing *write*, not a failing post-write *validation*.** Ruby
  reaches the same rolled-back result by both routes, but only the write route
  is reachable from a case list: `Store#create_preflight_failure` validates the
  same file set as `post_write_failure`, so an already-invalid store is refused
  before the write and post-write validation cannot fire from fixture content
  alone. Genuine fault injection (playbook step 7) is what would cover the
  other route.
- **`files.rolled_back` is null in the other 29 cases**, because those
  invocations report nothing — a read, a success, or a refusal without
  `--json`. Null on both sides is not a mismatch; the comparator counts those
  cases and says so in its report.
- **Exit-2 refusals carry no envelope on either side.** Ref resolution fails
  before a command's `--json` handling is reached. Recorded oracle behavior,
  not an endorsement.

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
