# Phase 1 gate result

**Story:** td-34d915 · **Epic:** td-27fbf5
**Baseline:** `porting/evidence/phase1/ruby` @ `c50086663542ed3339711f90d36c09142e3fce97`
**Re-run:** `porting/evidence/gate`

> **Verdict: PASS. All five seeded mismatch classes are detected, correctly
> classified, and — as of this revision — actually exercised by the corpus.**
> `porting/compare/audit` exits 0: every class is presented by at least one case,
> including the two that used to be absent (an exit-2 ref failure, and a real
> write-then-revert). The rollback class is now caught as a **labelled field**
> rather than as a diagnostic sentence.
>
> What that costs in honesty: **the rollback class rests on exactly one case**,
> and that case reaches a rollback through a failing *write*, not through a
> failing post-write *validation* — the second route is still unreachable from a
> case list. Coverage of a class by one narrow case is coverage, not a regime.
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
      33/33 cases match, 0 gate findings
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

### 5. Rollback behavior — **labelled**

**Seeded:** `cli-capture-readonly-rollback`'s `files.rolled_back` flipped from
`true` to `false`, and the `--json` error envelope it is read from flipped with
it. This models the one port defect that used to be invisible: an
implementation that never wrote where Ruby wrote and then restored the previous
bytes, printing **byte-identical stderr**. Exit status, `files.deltas` and store
bytes are identical in both worlds; `errors.md` says so itself — "the filesystem
cannot tell you".

```text
MISMATCH  cli-capture-readonly-rollback
  [go_defect/gate] cli: process.stdout (json)
      baseline:  {"rolled_back":true,"error":"unavailable","action":"capture","message":"could not capture (no \"Inbox\" section found?)\…
      candidate: {"rolled_back":false,"error":"unavailable","action":"capture","message":"could not capture (no \"Inbox\" section found?)…
      · $.rolled_back  value  true -> false
  [go_defect/gate] files: files.rolled_back
      baseline:  true
      candidate: false
      rule: errors.md § Failure shapes the corpus must distinguish — wrote-and-reverted vs never-wrote
            differ in this field and in nothing else on the filesystem
```

stderr is deliberately left untouched by this seed, so the detection can have
come from nothing but the label.

**What made it labellable.** Two changes, one on each side:

1. `bin/tasks` `mutation_result_failed` now emits the error envelope `claim`
   already emitted, carrying `rolled_back`, on every refusal of a command that
   promises a `--json` result. Previously a failed mutation under `--json`
   printed **nothing at all** on stdout. Specified in
   [`porting/specs/errors.md` § The `--json` error envelope](../../specs/errors.md).
2. The corpus reaches a rollback at all. Case
   `cli-capture-readonly-rollback` declares `copy_root_mode: "0555"`, so the
   store directory is unwritable while its lock sidecar already exists: the
   mutation starts, the atomic replace fails, and the CLI restores the previous
   bytes. See
   [`porting/runners/README.md` § A failing write](../../runners/README.md#a-failing-write-and-why-the-mode-lives-on-the-case)
   for why the mode is a property of the case rather than of the fixture.

Closes **td-2bc4c5** and **td-f2cb42**.

### Negative control — stdout JSON key order

**Seeded:** every object key on `list --json` stdout reversed.

```text
summary
  cases       33  (33 match, 0 mismatch, 0 unpaired)
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

### 5. Rollback behavior — `lib/dimensions/files.rb`

Drop the label comparison on the grounds that stderr already carries the
sentence — the exact reasoning that left the class unlabelled in the first
place, and the one plausible mistake here.

```diff
-       rolled_back(ctx)
+       # rolled_back(ctx)  # stderr already carries the sentence
```

```text
1) Failure: TestPortingCompare#test_the_baseline_labels_the_rollback_case_and_only_that_case
the report must count the cases carrying no label, not leave it to be discovered.
Expected: 29
  Actual: 0
2) Failure: TestPortingCompare#test_detects_a_rollback_that_differs_in_nothing_but_the_label
a wrote-and-reverted vs never-wrote difference must be reported on the label.
Expected nil to not be nil.
2 runs, 6 assertions, 2 failures
```

The producing side was mutated too, since a label the CLI never states is worth
nothing: `rolled_back: result.rolled_back?` → `rolled_back: false` in
`bin/tasks`.

```text
1) Failure: TestCliMutations#test_a_failed_write_is_reported_as_rolled_back_with_the_file_unchanged
the write failed after it began; the bytes were restored.
Expected: true
  Actual: false
```

**After restoring every mutation:** `27 runs, 192 assertions, 0 failures, 0 errors, 0 skips`.

---

## What this gate does NOT prove

### The rollback class rests on one narrow case

`porting/compare/audit` now exits **0**, and the two classes the corpus used to
skip are presented:

```text
  ok   exit_status      33 case(s)
  ok   json_output      11 case(s)
  ok   write_bytes       6 case(s)
  ok   revision_token   33 case(s)
  ok   rollback          1 case(s)

COVERAGE COMPLETE
```

Read that last line as it is written: **one case**. Everything below is what
that one case does not cover.

- **Only one of the two routes to a rollback is reachable.** Ruby reports
  `rolled_back` from a failed post-write *validation* and from a failed *write*.
  Only the write route is reachable from a case list, and it is the one the case
  uses. `Store#create_preflight_failure` validates exactly the same file set as
  `post_write_failure`, so a store that is already invalid is refused *before*
  the write — verified by probing all 32 fixtures with `capture` and `done`
  (64 invocations, zero rollbacks). Covering the validation route needs real
  fault injection (playbook step 7: "crash points around lock, write, flush,
  validation, rename, and journal append"). Not done here.
- **The mechanism is a directory mode, not the product's own failure path.** The
  case declares `copy_root_mode: "0555"` and the operating system does the rest.
  That is deliberate — it needs no test seam in either implementation, which
  would otherwise have to be ported and trusted before it could be compared —
  but it means the case proves the *reporting* of a rollback, not the full space
  of ways a write can fail.
- **`files.rolled_back` is null in the other 32 cases.** Those invocations report
  nothing: a read, a success, or a refusal without `--json`. Null on both sides
  is not a mismatch, and the comparator says so out loud rather than leaving it
  to be discovered:

  ```text
    NOTE  files.rolled_back is null on both sides in 32 of 33 case(s)
  ```

- **Exit-2 refusals carry no envelope.** Ref resolution fails before a command's
  `--json` handling is reached, so `done "no such task" --json` prints nothing on
  stdout on either side. That is the recorded oracle behavior, and a port must
  reproduce it; it is not an endorsement of it.

### The exit-status class is exercised, at two of its regimes

Two cases exit `2`: `cli-done-no-match-ref` (no title matches) and
`cli-done-ambiguous-ref` (two open tasks share a title, against
`malformed/duplicate-open-titles`). The second also captures the byte-compared
candidate list `errors.md` names as a contract in its own right:

```text
ambiguous: Replace the bathroom bulb — matches 2 tasks:
  L3: NEXT Replace the bathroom bulb :@home:
  L8: TODO Replace the bathroom bulb
```

What is still untested by the corpus: the other exit-2 producers (`L<n>` line
refs, `:ID:` refs, and the "ref outside scope" refusals, which have their own
wording), and exit `2` from any command other than `done`.

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

The committed baseline was re-captured at `5eba4b0`, and again at `c500866` for
this revision — the `--json` error envelope is itself a change to `bin/tasks`,
so the baseline moved with it. Note the consequence: **this baseline was captured
against an uncommitted `bin/tasks`**, which its `provenance.json` records
(`implementation_clean: false`). It must be re-captured once that change is
committed, and `test_provenance_records_a_clean_implementation_tree` says so in a
clean checkout rather than leaving it to be noticed.

Every future landing under `bin/` or `lib/` should run:

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

## 4. Revision after the independent adversarial review (td-3527b1)

[`porting/evidence/td-3527b1/review-independent.md`](../td-3527b1/review-independent.md)
falsified this gate's own headline claim — "two pinned runs produce
byte-identical stores, journals, and observations" — with a reproducible two-run
diff, and found five further rigor gaps. What changed, and what each change buys:

| Finding | Change | Evidence it is closed |
|---|---|---|
| 1 — unpinned `SecureRandom.hex(8)` in journal bytes | `TASKS_PIN_DELEGATION_KEYS` (`lib/tasks/determinism.rb`, `lib/tasks/application.rb`, `bin/tasks`) and in the runner's pin set; new case `cli-delegate-agent-ready` | Two `--pin-identity` runs of the delegation case are byte-identical; the same case with the pin unset differs on 12 lines including `journal.states[].coalesce_key` and `journal.index.sha256` |
| 3 — umask unpinned | `File.umask(0o022)` in the runner; `determinism.md` row moved out of "Not pinnable" | `test_the_umask_is_pinned_not_inherited` launches the runner from a 0077 process and asserts the created journal index is still 0644 |
| 4 — `TASKS_PIN_HOSTNAME` misses the device slug | `UpdateStamp.device` defaults through `Determinism.hostname`; the probe reports `applied` only when **both** consumers agree | `test_the_hostname_pin_reaches_the_update_stamp_device_slug` |
| 5 — `contentEncoding` constrains nothing | strict-base64 `pattern` on all five byte fields, plus a decode check in `validate` | `bytes_base64 = "not base64!!!"` is now rejected |
| 6 — sortedness and uniqueness were prose | `uniqueItems` on ten arrays; sortedness asserted in `validate` | unsorted `invocation.env`, duplicate `files.after` paths, duplicate `revisions.resources` ids all rejected |
| 7 — 24/24 nonsense mutations validated | ~200-line consistency pass appended to `porting/compare/validate` | a 28-mutation script over a real baseline observation: **28/28 rejected**, 0 false positives on the 33-case baseline |
| 8a — roles keyed off hardcoded filenames | roles resolved from the probe's reported paths; journal key computed symlink-resolved | new case `cli-capture-symlinked-store`: both `tasks.jsonl` and `tasks.real.jsonl` carry `role: "store"`, `symlink_target` populates, the journal is found, the mutation invariant holds |
| 9 — `restricted-mode-store` could not test its own name | `perms.json` applied to the copy between the copy step and the observe step | new case `cli-capture-restricted-mode`: the store is `0600` in `files.before` **and** in `files.after` |
| 14 — `applied` is a claim, not a proof | `test/test_porting_determinism_seams.rb` intercepts `Date.today` / `Time.now` / `Socket.gethostname` / `SecureRandom` via `RUBYOPT` across five fully-pinned commands | 0 clock reads, 0 hostname reads, 0 unpinned mints outside the documented operation id |

Finding 2 (the recorded environment was a constant list) was also closed:
`invocation.env` now records the union of the documented floor and the names
actually passed, so a case that sets `TASKS_DATE_ORDER` is visible rather than
silent, and `TASKS_MEMORY` — a *path* variable — is refused to cases.

Three null columns the review named are no longer null:
`journal.states[].coalesce_key`, `journal.states[].coalesce_scope`, and
`files[].symlink_target`. `journal.states[].repair`, `archive_sha256`,
`process.signal`, and `stream.truncated_at_bytes` remain unexercised.

Still open from the review, deliberately: findings 10 (truncated streams cannot
be copy-root-normalized), 11 (colour is unpinned and unobserved), 12 (a second
clock seam in `bin/tasks`), 13 (partially closed — `TASKS_TIMEZONE`, `PATH` and
`unsetenv_others` are now documented), and 15 (directory-only effects).

---

## Reproducing this result

```console
$ porting/compare/validate porting/evidence/phase1/ruby   # 33/33 valid AND internally coherent
$ porting/evidence/capture --check                        # has the oracle drifted?
$ porting/evidence/gate                                   # the five classes + control
$ porting/compare/audit porting/evidence/phase1/ruby      # what the corpus cannot prove
$ ruby test/test_porting_runner.rb                        # 27 runs, 221 assertions
$ ruby test/test_porting_compare.rb                       # 27 runs, 198 assertions
$ ruby test/test_porting_determinism_seams.rb             # 5 runs, 84 assertions
```
