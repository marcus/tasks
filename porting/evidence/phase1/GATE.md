# Phase 1 gate result

**Story:** td-34d915 · **Epic:** td-27fbf5
**Baseline:** `porting/evidence/phase1/ruby` @ `5eba4b0d2a52b58bd0ccdc1af7f853f8f37b0a15`
**Re-run:** `porting/evidence/gate`

> **Verdict: PASS, with two named gaps that are in the corpus and the CLI, not in
> the comparator.** All five seeded mismatch classes are detected and correctly
> classified. One of the five — rollback behavior — is detected only as a stderr
> byte difference and is **not labelled**, because `bin/tasks` has no
> machine-readable rollback signal. And two of the five are classes the Phase 1
> corpus never actually presents, so a green run on those classes is a statement
> about the comparator, not about the port.
>
> Read [§ What this gate does NOT prove](#what-this-gate-does-not-prove) before
> using a green comparison as evidence for anything.

---

## 1. The five seeded mismatch classes

Each class is seeded by perturbing the committed Ruby baseline
(`porting/compare/seed`), modelling a hypothetical Go implementation. Every seed
is deliberately **subtle**: a wholly different exit code proves almost nothing,
because any comparator catches it.

Detection is asserted three ways, not one — reported *at all*, reported on the
**expected field**, and **classified correctly** — plus a fourth check that the
perturbation did not leak into another case. Asserting only "something differs"
is how a gate passes for the wrong reason: a comparator that reported every case
as different would satisfy it perfectly.

### Control — the baseline matches itself

```text
PASS  control: the baseline matches itself
      27/27 cases match, 0 gate findings
```

It also matches itself across *runs*: two captures of the same commit are
byte-identical under `--pin-identity`, verified with `diff -r`. That is the
runner's own acceptance criterion, and without it a comparison could not
attribute any difference to the port.

Run first, because if the baseline does not match itself every result below is
noise.

### 1. Exit status

**Seeded:** `cli-undo-after-capture` exit `1` → `2`. Both sides nonzero — the
exact collapse `errors.md` calls "the most important single assertion in the
whole error surface", since `2` exists so an agent can refine a ref rather than
abort.

```text
MISMATCH  cli-undo-after-capture
  [go_defect/gate] cli: process.exit_status
      baseline:  1
      candidate: 2
      rule: errors.md § Exit status is the smallest and strongest contract — compared exactly on
            every case; 'both nonzero' is never a match, because 1 vs 2 is a product feature agents
            branch on
```

### 2. JSON output

**Seeded:** one row of `list --json` loses its `"tags": []` key — the commonest
Go/Ruby serialization divergence there is (`omitempty`). Not a value change: a
key that is simply absent.

```text
MISMATCH  cli-list-json-small-gtd
  [go_defect/gate] cli: process.stdout (json)
      rule: errors.md § Structured errors are compared as data, not as text — same keys, same values,
            same types; an omitted key and a null key are different answers; array order is significant
      · $[0].tags  key_only_in_baseline  [] -> null
```

The report names the JSON path, the reason, and which side dropped it.

### 3. Write bytes

**Seeded:** two adjacent keys transposed in one record of the store written by
`capture`. Identical parsed data, identical byte length, one reordered key —
Go map iteration order, or a struct field order that does not match Ruby's
canonical dump.

```text
MISMATCH  cli-capture-small-gtd
  [go_defect/gate] files: files.after[tasks.jsonl].sha256
      rule: errors.md § Structured errors — JSONL store bytes are compared byte for byte, INCLUDING key
            order and omitted defaults. The store is a durable format two implementations must round-trip,
            so a reordered key is a real difference here even though it is not on stdout.
      · line 15
        - {"type":"task","id":"1a2b3c0d","parent":"1a2b3c0b","state":"CANCELLED","title":"Take the intro …
        + {"type":"task","id":"1a2b3c0d","parent":"1a2b3c0b","state":"CANCELLED","closed":"2026-05-30","…
```

Eleven findings in total for that case, and correctly so: the store the port
wrote differently is also the store it snapshotted, so the journal blob, the
journal index's `org_sha`, `journal.states[1].store_sha256`, `journal.blob_sha256`
and four `files.deltas` entries all move with it. The comparator reports each
independently rather than collapsing them, so a port that got the store right and
the journal wrong is still visible.

### 4. Revision token

**Seeded:** the last hex character of one resource's revision flipped. Nothing
else in the observation moves.

```text
MISMATCH  cli-list-stale-revision
  [go_defect/gate] revisions: revisions.resources[33b7f795/task]
      rule: runners/README.md § Why revision tokens come from a probe; a resource revision is the token a
            conditional write is validated against, so one differing character is a lost-update bug for
            every HTTP client
      · $.revision  value  v1.fc4a5aca….78765de6b… -> v1.fc4a5aca….78765de6b…
```

The test asserts the seed is genuinely off-by-one: same length, exactly one
differing character.

### 5. Rollback behavior — **detected, not labelled**

**Seeded:** `cli-capture-torn-file`'s stderr changed from the never-wrote
diagnostic to the wrote-and-reverted one. Both strings are real bytes the Ruby
CLI emits. This is the pair `errors.md` names as indistinguishable on the
filesystem: same exit status, same empty deltas, same store bytes.

```text
MISMATCH  cli-capture-torn-file
  [go_defect/gate] cli: process.stderr (bytes)
      baseline:  could not capture (no "Inbox" section found?)\ntask file is already invalid — run `tasks check` (nothing was written)\n
      candidate: could not capture (no "Inbox" section found?)\nfile failed validation after the edit — run `tasks check`\n
      rule: errors.md § Diagnostic text is contract until proved otherwise — compared byte for byte, with
            only the copy-root rewrite applied.
      · first differing byte 46 (119 vs 106 bytes)
```

**The caveat, stated plainly.** `files.rolled_back` is `null` on both sides — on
every case, always — so nothing in the `files` dimension detected this. It was
caught one layer down, as stderr bytes. The difference is not *lost*; it is not
*labelled*. Full analysis in
[`porting/compare/README.md` § The rollback gap](../../compare/README.md#the-rollback-gap);
filed as **td-2bc4c5**.

### Negative control — stdout JSON key order

**Seeded:** every object key on `list --json` stdout reversed.

```text
summary
  cases       27  (27 match, 0 mismatch, 0 unpaired)
  findings    0  (0 gate-failing)
GATE PASS
```

Required to produce **no** finding. `errors.md` compares stdout JSON as parsed
data, and an over-strict comparator is as useless as a blind one — it fails in a
way that looks like diligence, with every run red and the real mismatch buried.
The same reordering applied to store bytes (class 3) is fatal. Both halves are
asserted together in `test_key_order_asymmetry_between_stdout_and_store`.

---

## 2. Fixture perturbation (playbook step 2)

An isolated copy of the entire fixture corpus, with one character changed in one
task title in `valid/small-gtd` — same byte length, one substitution. The real
`porting/fixtures/` was never touched.

Detected in **six independent places** across three dimensions:

| Dimension | Field | Class |
|---|---|---|
| cli | `fixture.root_sha256` | `harness_error` |
| cli | `process.stdout (bytes)` | `go_defect` |
| files | `files.before[tasks.jsonl].sha256` | `harness_error` |
| files | `files.after[tasks.jsonl].sha256` | `go_defect` |
| revisions | `revisions.store` | `go_defect` |
| revisions | `revisions.resources[1a2b3c02/task]` | `go_defect` |

The `harness_error` classification on `fixture.root_sha256` and `files.before` is
the *right* answer for a perturbed fixture: the two sides did not start from the
same tree, so nothing downstream is a statement about the port. A comparator that
reported only `go_defect` here would have sent someone hunting a port bug that
does not exist.

---

## 3. Mutation testing of the comparator

Three tests in an earlier session of this epic passed for the wrong reason, so
the assumption here was that these would too until proven otherwise. Each of the
five detection paths was broken with a **plausible mistake** — the kind someone
would actually write — and the corresponding test confirmed to fail. Every
mutation was reverted and the full suite re-run green afterwards.

### 1. Exit status — `lib/dimensions/cli.rb`

Collapse exit status to zero/nonzero, the exact defect `errors.md` warns about.

```diff
- ctx.equal!(NAME, "process.exit_status", ctx.a.dig("process", "exit_status"), ctx.b.dig("process", "exit_status"),
+ ctx.equal!(NAME, "process.exit_status", ctx.a.dig("process", "exit_status").to_i.positive?, ctx.b.dig("process", "exit_status").to_i.positive?,
```

```text
1) Failure: TestPortingCompare#test_detects_exit_status_collapse [test/test_porting_compare.rb:102]:
a 1 -> 2 exit status change must be reported.
Expected nil to not be nil.
1 runs, 1 assertions, 1 failures
```

### 2. JSON output — `lib/diffs.rb`

Treat an absent key as equal to a null or empty value — the classic `omitempty`
blind spot.

```diff
  if !a.key?(k)
-   out << { "path" => child, "reason" => "key_only_in_candidate", … }
+   next
  elsif !b.key?(k)
-   out << { "path" => child, "reason" => "key_only_in_baseline", … }
+   next
  else
```

```text
1) Failure: TestPortingCompare#test_detects_omitted_json_key [test/test_porting_compare.rb:123]:
an omitted stdout JSON key must be reported.
Expected nil to not be nil.
1 runs, 1 assertions, 1 failures
```

### 3. Write bytes — `lib/dimensions/files.rb`

Compare store records as parsed data instead of bytes — the over-normalization
that says "it is the same data, the key order does not matter". This is the
single most likely wrong turn in the whole comparator.

```diff
  return if fa["sha256"] == fb["sha256"]
+ if fa["content_base64"] && fb["content_base64"]
+   pa = Base64.decode64(fa["content_base64"]).split("\n").map { |l| JSON.parse(l) rescue l }
+   pb = Base64.decode64(fb["content_base64"]).split("\n").map { |l| JSON.parse(l) rescue l }
+   return if pa == pb
+ end
```

```text
1) Failure: TestPortingCompare#test_detects_reordered_keys_in_store_bytes [test/test_porting_compare.rb:144]:
a reordered key inside the JSONL store must be reported.
Expected nil to not be nil.
1 runs, 1 assertions, 1 failures
```

### 4. Revision token — `lib/dimensions/revisions.rb`

Compare only which resources exist, not their tokens — a plausible "the ids
match" shortcut.

```diff
- ctx.equal!(NAME, "revisions.resources[…]", a[key], b[key],
+ ctx.equal!(NAME, "revisions.resources[…]", a[key]&.slice("id", "kind"), b[key]&.slice("id", "kind"),
```

```text
1) Failure: TestPortingCompare#test_detects_one_character_revision_token_change [test/test_porting_compare.rb:167]:
a one-character revision token change must be reported.
Expected nil to not be nil.
1 runs, 1 assertions, 1 failures
```

### 5. Rollback behavior — `lib/dimensions/cli.rb`

Compare only the first line of a diagnostic, treating the trailing hint as
formatting. Note what this mutation costs: stderr is the *only* channel carrying
the rollback distinction, so this one change makes the class undetectable
entirely.

```diff
  return if bytes_a == bytes_b
+ return if bytes_a.lines.first == bytes_b.lines.first
```

```text
1) Failure: TestPortingCompare#test_detects_rollback_diagnostic_only_through_stderr_bytes [test/test_porting_compare.rb:186]:
a wrote-and-reverted vs never-wrote diagnostic difference must be reported.
Expected nil to not be nil.
1 runs, 1 assertions, 1 failures
```

**After restoring every mutation:** `24 runs, 173 assertions, 0 failures, 0 errors, 0 skips`.

---

## What this gate does NOT prove

### The corpus never presents two of the five classes

`porting/compare/audit` exits **nonzero** on the committed baseline, and it is
correct to:

```text
  PART exit_status      27 case(s)
       ! no case exits 2 (ref resolution failure). A port that collapsed 2 into 1
         would pass this corpus.
  ok   json_output      7 case(s)
  ok   write_bytes      3 case(s)
  ok   revision_token   27 case(s)
  GAP  rollback         0 case(s)
       ! files.rolled_back is null in every observation.

COVERAGE INCOMPLETE: rollback, exit_status
```

The distinction that matters: **the comparator detects both classes** — that is
what the seeding proves — but the *corpus* never asks the question, so a green
run on those two classes says nothing about a real port.

- **Exit 2 is never observed.** All 27 cases exit 0 or 1. Two cases against
  existing fixtures close it (a no-match ref; an ambiguous ref against
  `malformed/duplicate-open-titles`, which also exercises the byte-compared
  candidate list). Filed as **td-c51338**.
- **No rollback is ever observed.** Not an omission in the case list: it is
  unreachable through the runner protocol. `Store#create_preflight_failure`
  validates the same file set as `post_write_failure`, so an already-invalid
  store is refused *before* the write — verified by probing all 32 fixtures with
  `capture` and `done` (64 invocations, zero rollbacks). It *is* reachable
  through a write failure (read-only store directory with the lock sidecar
  present), but the runner creates the copy root itself, so no fixture can
  constrain its mode. Filed as **td-f2cb42**.

### The rollback class is modelled, not measured

Class 5's detection was demonstrated by seeding a real Ruby diagnostic into a
real observation, not by observing a real rollback — because no case produces
one (above). The stderr bytes on both sides are genuine CLI output; the pairing
is constructed. That is an honest demonstration of the comparator's capability
and it is not the same as coverage.

### Anything about Go

There is no Go implementation. Every "candidate" here is a perturbed Ruby
baseline. This gate proves the harness can see; it proves nothing about what the
harness will see.

### The baseline is a snapshot, and it drifted within the hour

This is not hypothetical. The baseline was first captured at `a832ecc`. While
this story was being written, `5eba4b0` ("refuse unsupported schema versions on
read, not just on write", td-9f3dd0) landed on main, and
`porting/evidence/capture --check` reported it immediately and exactly:

```text
MISMATCH  cli-capture-future-schema-v3
  [go_defect/gate] cli: process.stderr (bytes)
      baseline:  could not capture (no "Inbox" section found?)\nunsupported meta version 3 …
      candidate: unsupported meta version 3 (expected 2) — this build cannot read this task file …
      · first differing byte 0 (150 vs 104 bytes)
  [go_defect/gate] files: files.after[.tasks.jsonl.lock]
      baseline:  present
      candidate: absent
  [go_defect/gate] files: files.deltas[.tasks.jsonl.lock]
```

Three findings, one real cause, correctly localised: the refusal now happens
*before* the section lookup, so the "could not capture" prefix is gone — and it
happens before the store is locked, so the lock sidecar is no longer created.
That second consequence is exactly the platform-shaped side effect
`determinism.md` refuses to filter out of `files.deltas`; a harness that had
tidied the lock file away would have reported the wording change and missed the
locking change entirely.

The committed baseline has been re-captured at `5eba4b0`. Every future landing
under `bin/` or `lib/` should run:

```console
$ porting/evidence/capture --check
```

Exit 1 means the oracle has moved and every slice measured against this baseline
is measuring against a stale oracle. Re-capture, and say what changed — never
re-capture quietly.

### Case ids do not attribute to manifest slices

`porting/runners/README.md` says a case that exercises a manifest slice "should
carry that slice's id, because the observation's `case_id` is what ties evidence
back to the slice". The Phase 1 case ids are descriptive (`cli-capture-small-gtd`),
not manifest ids, so **no observation in this baseline attributes to a slice**.

Proposed alignment — recorded here, not acted on, because the case list and the
manifest are other agents' trees:

1. Keep the descriptive ids. They are readable, they are already the join key in
   this evidence, and renaming them would invalidate the baseline for no gain.
2. Add an optional `slice` key to the case-list format (`porting/runners/README.md`
   § "The case list") carrying one or more manifest ids, and a corresponding
   optional field on the observation. One case commonly exercises several
   slices, and one slice needs several cases, so the relation is many-to-many
   and a single id in `case_id` cannot express it anyway.
3. Have `porting/manifest.jsonl` entries point at cases through their evidence
   locator, which is the direction the manifest already reads.

Until then, attribution is by hand.

---

## Reproducing this result

```console
$ porting/compare/validate porting/evidence/phase1/ruby   # 27/27 schema-valid
$ porting/evidence/capture --check                        # has the oracle drifted?
$ porting/evidence/gate                                   # the five classes + control
$ porting/compare/audit porting/evidence/phase1/ruby      # what the corpus cannot prove
$ ruby test/test_porting_compare.rb                       # 24 runs, 173 assertions
```
