# Phase 1 story reviews

Independent review of the six Phase 1 stories sitting in `in_review`, by a
session that implemented none of them. **td-3527b1 is reviewed elsewhere and is
not covered here.**

Method: every acceptance criterion was checked against the tree as it is, not
against the story's own handoff. Where a story claimed a test passes, a document
is accurate, or an outcome was recorded, the claim was re-derived from scratch —
`check` was re-run on all 32 fixtures, all 259 `ruby_tests` refs were resolved
without using `manifest-issues validate`, the runner was replayed twice, and the
sanitization greps were written fresh rather than read off the README.

Because three other streams are writing to this checkout concurrently
(`bin/tasks`, `porting/runners/`, `porting/compare/`, `porting/evidence/phase1/`,
and new `porting/fixtures/` directories), everything that could be affected by
in-flight edits was verified a second time in a detached worktree at `c500866`,
created with `git worktree add --detach` so no other stream's uncommitted work
was touched.

## Summary

| Story | Recommendation | Follow-ups |
|---|---|---|
| td-f4e1fd Land the porting scaffold | **CLOSE** | — |
| td-a1d16a Sanitized fixture corpus | **CLOSE WITH FOLLOW-UP** | td-fc2c99 |
| td-a23bad Ruby runner emitting observations | **CLOSE** | — |
| td-34d915 Comparator, baseline, seeded-mismatch gate | **CLOSE WITH FOLLOW-UP** | td-44d49b, td-36d27d |
| td-940935 Seed manifest.jsonl and generate fleet issues | **CLOSE WITH FOLLOW-UP** | td-d0f00b |
| td-09f7de Remove schema-v1 migration and org-mode remnants | **CLOSE** | — |

No story is recommended for SEND BACK. One genuine code defect was found
(td-44d49b); the rest are documentation claims that are wider than what the
artifact supports.

---

## td-f4e1fd — Land the porting scaffold

> **AC:** `porting/loop.sh --once --dry-run` passes preflight from repo root; all
> referenced doc paths resolve in-repo.

### Criterion 1 — `loop.sh --once --dry-run` passes preflight — **MET**

Run from the repo root:

```text
fleet start harness=codex slots=1 repo=/Users/marcus/code/tasks dry_run=1 max_ticks=0 on_limit=park timeout=3600s
slot=1 created worktree /Users/marcus/code/tasks-port-slots/slot-1
slot=1 tick=1 harness=codex model=sol ctx=port-slot1-… log=…
slot=1 tick=1 DRY RUN — harness not invoked
fleet stopped
```

Exit 0. Preflight ran (it resolved the harness, created the slot worktree, and
opened a log) rather than being short-circuited by `--dry-run`, which is the
distinction that makes this criterion mean anything.

`porting/test-loop-limits.sh`: **passed: 168  failed: 0**. The handoff recorded
43 assertions; the suite has since grown to 168, all green. It covers the
usage-limit park/resume state machine, the pattern-override file, and — usefully
— that sourcing `loop.sh` does not run the supervisor.

### Criterion 2 — all referenced doc paths resolve — **MET**

A fresh link check over `porting/{PORTING,OPERATING,README,intentional-differences}.md`
and the three moved `docs/plans/active/` documents: **75 refs, 6 non-resolving**,
and all six are correct as written:

- `porting/logs/YYYYMMDD/` — runtime placeholder, created per-day by `loop.sh`.
- `observations.schema.json`, `errors.md` — bare filenames inside a table row
  that has already named `specs/` as the directory; both exist under
  `porting/specs/`.
- `manifest.jsonl`, `PORTING.md` — bare filenames in prose; both exist.
- `CHANGES.md` — a citation of another project's convention in the playbook's
  literature review, not an in-repo path.

Note the six the handoff called out as placeholders are not the same six found
here (`porting/STOP` and `porting/limit-patterns.<harness>` no longer appear as
bare refs), but the conclusion is unchanged.

### Verdict: **CLOSE**

No defects. The one thing worth carrying forward is the handoff's own caution
that the content is split across `6dfcd2f` and `9528bd6` because a concurrent
session's `git add -A` swept it up — the tree is correct, only the attribution is
wrong, and rewriting history under a live session was correctly declined.

---

## td-a1d16a — Sanitized fixture corpus

> **AC:** Ruby `tasks check` outcome recorded for every fixture; corpus covers the
> valid/legacy/malformed/concurrent list in plan Phase 1.

### Criterion 1 — `check` outcome recorded for every fixture — **MET, re-derived**

"There is a recorded outcome" is not the check. Every one of the 32 fixtures was
copied to a temp directory and re-run under the exact environment the README
documents:

```sh
cd "$copy" && env -u TASKS_FILE -u TASKS_ARCHIVE \
  TASKS_DIR="$copy" XDG_CONFIG_HOME="$empty" XDG_STATE_HOME="$copy/.state" \
  TASKS_TIMEZONE=UTC TASKS_DEVICE=fixture \
  ruby bin/tasks check [--all-files]
```

**32/32 match the README index exactly**, including every case where a naive
reader would expect the opposite answer and where a wrong record would be
invisible:

| Fixture | README | Observed |
|---|---|---|
| `malformed/wrong-key-order` | **0 — passes** | 0, `ok — 3 tasks parsed` |
| `malformed/cross-file-duplicate-id` | 0 (`--all-files`: 1) | 0 / 1 |
| `malformed/duplicate-open-titles` | 0 with 1 warning | 0, one `duplicate open title` warning |
| `compat/forward-compat-unknown-keys` | 0 with 3 warnings | 0, exactly 3 `unknown key` warnings |
| `compat/future-schema-v3` | 1 — `unsupported meta version 3 (expected 2)` | verbatim |
| `malformed/meta-out-of-place` | 1 — 3 errors | 3 errors |
| `malformed/wrong-types` | 1 — 24 errors | 24 errors |
| `malformed/non-record-lines` | 1 — 3 errors | 3 errors |
| `adversarial/mid-write-torn-file` | 1 | 1 |
| all other `adversarial/` | 0 | 0 |

Every fixture directory has a `README.md` (checked, none missing) and a `store/`.

### Criterion 2 — corpus covers the plan's Phase 1 list — **MET**

`docs/plans/active/tasks-go-port-plan.md` § Phase 1 names "valid, compat,
malformed, and adversarial (concurrent) cases — the four classes in
`porting/fixtures/`". The plan text has itself been updated by td-09f7de to say
`compat/` rather than `legacy/`, and to state why. All four classes exist and are
populated (7 / 2 / 14 / 9 = 32). The AC's word "legacy" is the pre-td-09f7de
spelling of `compat/`; the reconciliation is documented in the fixtures README
§ "Why there is a `compat/` class and no `legacy/` one".

The story handoff says "37 fixtures"; the tree has 32. That is not drift — the
four `schema-v1*` fixtures and `org-pre-jsonl` were deliberately retired by
td-09f7de, which lands after this story. The README index is internally
consistent at 32.

### The sanitization claim — **HOLDS IN SUBSTANCE**

This was checked hardest, because it is the one claim in this tree with
consequences outside the repository. Greps written independently of the README's
table, over the whole corpus including per-fixture READMEs, journal blobs, and
dotfiles:

| Probe | Result |
|---|---|
| `[\w.%+-]+@[\w.-]+\.\w{2,}` | one address, `sam.rivera@example.com`, in `valid/full-field-matrix` — invented, reserved domain |
| `https?://…` | two, both `example.invalid` |
| `marcus\|aerie\|vorwaller` (case-insensitive) over `*.json`/`*.jsonl` | **zero** |
| `/Users/…` or `/home/…` | one, `/home/someone-else/tasks/tasks.jsonl` — the deliberate wrong path in `journal-foreign-org` |
| every `"updated"` stamp's device slug | `#fixture` on all of them; one stamp carries no slug at all; **no hostname anywhere** |
| all distinct `"title"` values | combinatorial synthetic ("Book the plant food (2.3.7)", "Call the spare key (1.2.1)", …) — no personal content |

The same probes were run over the 17 new untracked fixture directories other
streams are adding: also clean.

**Verdict on the claim: correct.** Nothing here derives from real task data.

### Defect — the verification table misreports itself (minor)

Two rows of the README's own table are false as literal statements over the tree
they say they cover:

- `marcus / aerie / vorwaller, case-insensitive | no matches` — five matches, all
  in prose, including the row itself and the sentence "Marcus's live store lives
  outside this repository".
- `email addresses | one: sam.rivera@example.com` — two occurrences of the
  string; the store record, and the table row.

Effect: none. Significance: a verification table that miscounts itself stops
being a thing a reader can rely on, and this table is the artifact standing
between the corpus and a real-data leak. Filed as **td-fc2c99**; the fix is to
scope the rows to store/journal/sidecar bytes and say so.

### Findings the story recorded, spot-checked

All three reproduce, and no Ruby was changed for any of them:

1. `check` does not validate key order — `malformed/wrong-key-order` exits 0.
   Confirmed above.
2. Reads tolerate a torn store; writes do not — `list` on
   `adversarial/mid-write-torn-file` exits 0 while `check` exits 1. Confirmed.
3. Same-owner claim retry is refused, not idempotent — recorded in
   `adversarial/same-owner-retry` and exercised by the runner case
   `cli-claim-same-owner-retry`.

### Verdict: **CLOSE WITH FOLLOW-UP ISSUE (td-fc2c99)**

---

## td-a23bad — Ruby runner emitting observations

> **AC:** Runner replays a scripted case list against fixture copies and produces
> schema-valid observations; repeated runs are byte-identical under pins.

Verified in the detached `c500866` worktree, because `porting/runners/ruby/run`
and `porting/runners/cases/phase1.jsonl` are being modified right now by the GATE
stream.

### Criterion 1 — schema-valid observations from a scripted case list — **MET**

`porting/compare/validate porting/evidence/phase1/ruby` → **27/27 observations
valid against `observations.schema.json`** at HEAD (30/30 in the working tree,
where GATE has added three cases). The case list is a real script: 27 cases
across all four fixture classes and eight commands (`list`, `list --json`,
`check`, `check --all-files`, `agenda`, `capture`, `done`, `priority`, `claim`,
`undo`), with argv, env, stdin, and expected surface per case.

`ruby test/test_porting_runner.rb` at HEAD: **19 runs, 183 assertions, 0
failures**.

### Criterion 2 — repeated runs byte-identical under pins — **MET, re-derived**

Two full replays at the same `--work` root with `--pin-identity`:

```console
$ diff -r $scratch/r1 $scratch/r2
IDENTICAL
```

And the pin is doing real work, not hiding a determinism bug behind a
normalization: the same two runs **without** `--pin-identity` differ in **628
lines**, exactly at `observation_id` and `metrics.wall_ms`.

A third replay at the baseline's own work root reproduces the committed baseline
byte for byte across all 27 files, with a single exception — see below.

### Criterion 3 (implied, from the description) — never touches a live store — **MET**

Each case copies its fixture into a per-case directory under `--work` and points
`TASKS_DIR` at the copy; `TASKS_FILE`/`TASKS_ARCHIVE` are unset and
`XDG_CONFIG_HOME` is redirected. `porting/fixtures/` was unmodified after four
full replays (verified: `git status --porcelain porting/fixtures` shows no
modifications to tracked fixtures, only the other streams' new directories).

### Observation carried to td-34d915

The committed baseline records `implementation.version =
"5eba4b0d…-dirty"`, so my clean-worktree replay differed from it in exactly that
one string in all 27 files and nothing else. That is a provenance issue with the
baseline capture (td-34d915), not a runner defect — the runner is reporting the
truth it was given. It is covered in **td-36d27d**.

The runner's known gap, `files.rolled_back` always null, was correctly
identified by the story as a downstream problem and is filed as td-2bc4c5.

### Verdict: **CLOSE**

---

## td-34d915 — Comparator, Ruby baseline, and seeded-mismatch gate

> **AC:** All five seeded mismatch classes detected and correctly classified;
> baseline capture committed as evidence; gate result recorded in this issue and
> the epic.

### Criterion 1 — five classes detected and correctly classified — **MET, with one honestly-disclosed asterisk**

At HEAD: `porting/evidence/gate` → **GATE PASS**, and
`ruby test/test_porting_compare.rb` → **24 runs, 173 assertions, 0 failures** —
matching GATE.md's recorded numbers exactly, which is itself worth stating since
GATE.md is the artifact under review.

(In the working tree that same suite currently shows 5 failures. That is the GATE
stream mid-edit on `files.rb`, `report.rb` and `seed`, not a regression in this
story.)

The gate is not "a test exists". Each class asserts three ways — reported at all,
reported on the expected field, classified correctly — plus a no-leak check, plus
a negative control (reordered stdout JSON keys) that must stay silent, plus the
asymmetry assertion that the same reordering **inside store bytes** is fatal.
Each of the five detection paths was then mutation-tested with a plausible
mistake and the corresponding test confirmed to fail. That is the right shape,
and given the project's history of three tests passing for the wrong reason,
mutation-testing the detector rather than the detection is the correct response.

The asterisk: class 5 (rollback) is detected only as a stderr byte difference and
is not labelled, because the CLI has no machine-readable rollback signal. The
story says so in the verdict, in the class-5 section, and in the caveats section,
and filed td-2bc4c5. The demonstration also pairs a real Ruby diagnostic into a
constructed observation rather than observing a real rollback — GATE.md
§ "The rollback class is modelled, not measured" says exactly that. This is
disclosure done properly, and it is being closed by the GATE stream now
(`cli-capture-readonly-rollback` has appeared in the working tree).

### Criterion 2 — baseline committed as evidence — **MET, with a provenance defect**

`porting/evidence/phase1/ruby`, 27 observations, all schema-valid, reproducible
from `5eba4b0`. `porting/evidence/capture --check` at HEAD reports **no drift**,
and the story demonstrated the detector working live when td-9f3dd0 landed
mid-story.

### Criterion 3 — fixture perturbation detected — **MET**

Six independent detections across three dimensions, with `fixture.root_sha256`
and `files.before` correctly classified `harness_error` rather than `go_defect`.
That classification is the substantive part: a comparator that shouted
`go_defect` at a perturbed fixture would send someone hunting a port bug that
does not exist.

### Defect 1 — `capture`'s provenance can claim a clean implementation while `bin/` is dirty (**real**)

`porting/evidence/capture`:

```ruby
def sh(*cmd) = `#{...}`.strip
dirty = sh("git","-C",REPO,"status","--porcelain").lines.map { |l| l[3..].to_s.strip }...
implementation_dirty = dirty.select { |p| p.start_with?("bin/", "lib/") }
```

`sh()` strips the **whole** porcelain output, which eats the leading space of the
first line when that entry is an unstaged-only change. `l[3..]` then drops one
character from the first path. Reproduced on the current tree:

```text
RAW first line:  " M bin/tasks\n"
PARSED first 3:  ["in/tasks", "porting/compare/lib/dimensions/files.rb", …]
implementation_dirty=[]   implementation_clean=true
TRUTH: bin/tasks modified? true
```

`porting/evidence/phase1/provenance.json` in the working tree right now records
`"in/tasks"` and `"implementation_clean": true` while `bin/tasks` is modified.
The comment directly above the code calls this "the only claim that matters for a
baseline's trustworthiness".

The committed `provenance.json` at `5eba4b0` is correct, but only by luck: its
first porcelain line was a staged delete (`D  porting/compare/.gitkeep`), which
has no leading space. A latent, input-dependent falsehood in exactly the field
that certifies the oracle is worse than a consistent one. Filed as **td-44d49b**.

This does not invalidate the committed baseline — the paths dirty at capture were
`loop.sh`, `test-loop-limits.sh`, `test_porting_compare.rb` and harness
directories, none under `bin/` or `lib/`, and my clean replay confirms identical
bytes.

### Defect 2 — the caveats section is not complete (**doc**)

GATE.md's "What this gate does NOT prove" is unusually good and covers the two
corpus gaps, the modelled rollback, Go, baseline drift, and case-id attribution.
Three limits are missing, and the section's whole value is that a reader can
treat it as exhaustive:

1. **The baseline was captured from a dirty worktree.** All 27 observations carry
   `implementation.version = "5eba4b0d…-dirty"`; GATE.md's header cites the bare
   sha. Verified harmless, but `implementation.*` is never compared, so nothing
   would ever surface it.
2. **The HTTP dimension is never exercised.** Every Phase 1 observation has an
   empty `http` array. `http.rb` documents its own stub status honestly, but
   GATE.md does not list it, and `porting/compare/audit` audits only the five
   gate classes — it does not audit dimension coverage at all.
3. **`ok` in the audit means ≥1 case, not adequate coverage** (`audit:137`:
   `mark = r["exercised"] ? (r["notes"].empty? ? "ok  " : "PART") : "GAP "`).
   GATE.md reproduces the audit block where `write_bytes` reads `ok  3 case(s)`.
   Three mutating cases prove the comparator can see store bytes; they do not
   make a green run a statement about a port's write paths.

Filed as **td-36d27d** (which also notes the baseline is host-bound to
arm64-darwin23 / ruby 4.0.6; environment mismatches are handled in code via
`requires_rerun`, but are not caveated).

### Things checked and found sound

- `porting/compare/dispositions.jsonl` is empty, requires a `record` pointing at
  `intentional-differences.md`, forbids wildcards, and never suppresses a
  finding — only changes its severity. This is the most obvious place a
  comparator turns into a difference-hiding machine and it is closed properly.
- `normalize.rb` contains exactly four normalizations, each carrying a written
  "a user cannot observe this because…" justification, and explicitly refuses to
  rewrite the journal index path inside digested bytes.
- Exclusions live next to the comparison they suppress, and the cross-path
  journal-index exclusion is never automatic and never silent.

### Verdict: **CLOSE WITH FOLLOW-UP ISSUES (td-44d49b, td-36d27d)**

Neither follow-up undermines the gate result. The 27 port slices can be measured
against this baseline today.

---

## td-940935 — Seed manifest.jsonl and generate fleet issues from it

> **AC:** Running the generator twice creates no duplicates; every manifest slice
> has a td issue with correct dependency edges.

### Criterion 1 — idempotent generator — **MET**

Two consecutive `sync --dry-run` runs produce byte-identical plans
(`skip=22  update=20  total=42`). No CREATE lines: every slice, epic, and
fixture-gap issue already exists and would not be recreated. `manifest-issues`
also refuses a sync outright on a duplicate `slice:` label, which closes the
obvious way a projection grows a twin.

### Criterion 2 — every slice has a td issue with correct dependency edges — **MET**

27 slice issues, 3 campaign epics, 8 fixture-gap issues. Spot-checking
`create-basic` (`td-c4d282`) against the manifest: manifest `depends_on` is
`[store-canonical-write, check-report-and-cli, id-minting, tree-build,
update-stamp]`; td BLOCKED BY lists exactly those five issues. No dep changes
appear in `plan`, which is the generator asserting the edges already match.

### Independent integrity re-derivation (not trusting `validate`)

`manifest-issues validate` says "every source path and oracle test resolves". It
does — but a validator that passes is the beginning of the check. Re-derived
from the raw JSONL:

| Probe | Result |
|---|---|
| slice ids | 27, all unique |
| `depends_on` edges | 41, **zero dangling**, acyclic |
| `source_paths` | all present on disk |
| `ruby_tests` | 259 refs; **259 resolve** to a real `def test_*` in the named file; zero whole-file claims |
| `fixtures` | every ref resolves to a real fixture directory |
| `source_sha` | 6 distinct commits, all real; `drift` reports every slice pinned to its closure's last-touch commit |
| `closure` | transitive require closure guarded; 22 extra `lib/tasks` files now watched; 0 slices mispinned |
| `reach` | 16 cross-slice oracle reaches, **16 explained, 0 unexplained** |
| `fixtures_todo` | 12 slices retain an honest narrowed todo, each naming the exact missing case |

The `ruby_tests` re-derivation is the one that mattered most: a manifest that
names tests which do not exist would look identical to a correct one from the
outside, and the story's own history includes correcting 8 misattributions and
enumerating 3 whole-file claims.

### "No Ruby behavior claimed by two slices or by none"

**By none:** the story found the holes itself and filed them —
**td-ee475f** (three-way merge: `jsonl_merge.rb` and `merge_driver_command.rb`
in no slice's `source_paths`, `test_jsonl_merge.rb` claimed by nobody; lifecycle
transitions; mutation `--json` envelopes) and **td-8df1bb** (the schema gate for
project mutation and the archive sweep). `reach` also names `archive_swept!` as
`(unseeded)` rather than silently attributing it. This is the correct behavior
for a wiring pass and it is why this story should close rather than go back.

Relevant to td-09f7de: `jsonl_merge`'s surviving `allow_older` path — which still
parses a schema-v1 **merge base** — falls inside td-ee475f's item 1, so it is
tracked.

**By two:** four tests appear in two slices each —

```text
test/test_check.rb#test_non_string_id_reports_error_without_raising         check-meta-and-ids, store-snapshot-items
test/test_task_queries.rb#test_list_filter_uses_snapshot_bodies…            query-list-filters, task-view-projection
test/test_cli_projects.rb#test_projects_json_is_an_array_of_project_objects query-projects, cli-read-json-envelopes
test/test_delete_task.rb#test_undo_restores_byte_exact_file_and_redo…       store-canonical-write, delete-task
```

Each is a genuinely dual-purpose test asserting one thing for each claimant, and
in each pair the two slices are dependency-related. Not a defect; recorded so the
next reviewer does not re-derive it.

### Defect — the td projection is stale, and two pin notes contradict their field

1. **`porting/manifest-issues plan` is not clean.** `skip=22 update=20`. All 20
   are description updates left by `c500866` ("re-pin 20 slices after the
   schema-refusal commit"), after which `sync` was never re-run. The divergence
   is visible in td: `td-c4d282` still advertises "Ruby source (as of
   `fc84567fe270`)" while the manifest pins `5eba4b0d2a`. The BRIEF requires
   `plan` to be clean, and the fleet reads the td issue, not the manifest.

2. **Two slices' `notes` contradict their own `source_sha`:**
   `check-report-and-cli` and `cli-read-json-envelopes` both say "Re-pinned to
   `9132699`" while `source_sha` is `5eba4b0d…`. `9132699` is a real commit and
   was the pin before `c500866` moved it. `drift` cannot catch this because it
   only compares `source_sha` against the closure's last-touch commit; the prose
   is unchecked. This is precisely a claim satisfied in form (the record has a
   pin note) but not in substance (the note is wrong).

Both filed as **td-d0f00b**, addressed to WIRING, which owns the manifest and
runs last.

### Verdict: **CLOSE WITH FOLLOW-UP ISSUE (td-d0f00b)**

Close only after WIRING's final pass leaves `plan` clean; the follow-up records
what must be true at that point.

---

## td-09f7de — Remove schema-v1 migration and org-mode remnants from the Ruby CLI

> **AC:** `tasks migrate` is gone; a schema-v1 store is still refused with a clear
> diagnostic; full suite plus API suite green; legacy fixture class reconciled;
> manifest `oracle_gaps` and `source_sha` updated.

### Criterion 1 — `tasks migrate` is gone, genuinely rather than unreferenced — **MET**

```console
$ ruby bin/tasks migrate
unknown command: "migrate"
… (help text)
$ echo $?
1
```

It is not a hidden command, an alias, or a dispatch entry that merely lost its
help row. Grepped across `lib/`, `bin/`, `docs/cli-spec.md` and `docs/openapi.yaml`
for `migrate_schema`, `migration_required`, `schema_migration_required`,
`MigrationResult`, `MIGRATION`: **zero hits**. The API's `409
schema_migration_required` is gone from the response, the examples, and the
`ErrorCode` enum — `test/api/test_toolchain.rb` asserts the code appears on no
operation. The TUI's migrate prompt and its key handler are gone. What survives
in `lib/` is the word "migrate" in unrelated comments (editor code, adapter
boundaries) and `paths.org` as the store-path variable name, which is naming
residue from the pre-JSONL era, not org-mode functionality.

The only remaining v1-aware code path is `jsonl_merge`'s `allow_older` exception,
which lets a schema-v1 **merge base** be parsed and version-validated. It
converts nothing and is documented at length in place with a concrete
justification (nine commits in the task-data repo carry a v1 `tasks.jsonl` before
the 2026-07-16 upgrade, and `git rebase`/`cherry-pick` reach for ancestors
arbitrarily far back). It is correctly *not* a migration path, and it is tracked
for the port under td-ee475f.

### Criterion 2 — a schema-v1 store is still refused with a clear diagnostic — **MET**

Against a hand-built v1 store, on every surface:

| Command | Exit | Diagnostic | Bytes changed |
|---|---|---|---|
| `check` | 1 | `error  line 1: unsupported meta version 1 (expected 2)` | — |
| `list` | 1 | `unsupported meta version 1 (expected 2) — this build cannot read this task file (nothing was written)` | — |
| `show <id>` | 1 | same | — |
| `add hello` | 1 | same | **unchanged** (md5 identical) |
| `done a0000001` | 1 | same | **unchanged** |

A v1 `archive.jsonl` beside a v2 live store is refused under `--all-files`:
`error  line 1: archive.jsonl: unsupported meta version 1 (expected 2)`.

Two things this gets right beyond the letter of the criterion: the diagnostic
**names no command that no longer exists**, and the gate is generalized from
`version == 1` to "any declared Integer meta version ≠ 2", so
`compat/future-schema-v3` is refused by the identical code path with identical
wording. A v1-only guard would have satisfied the criterion and left a hole at
v3.

### Criterion 3 — full suite plus API suite green — **MET**

```text
ruby test/all.rb                    2170 runs, 31772 assertions, 0 failures, 0 errors, 0 skips
bundle exec ruby test/api/all.rb     108 runs,  2660 assertions, 0 failures, 0 errors, 0 skips
```

Both higher than the commit message's recorded 2086 / 107, i.e. the removal is
covered by *added* tests, not by deleted ones. `test/test_cli_mutations.rb`
carries `test_cli_help_and_dispatch_no_longer_carry_a_migrate_command`, which
asserts `unknown command: "migrate"` and `refute_match(/migrate/i, …)` on both
help output and refusal stderr — the right assertion, since the failure mode
here is a diagnostic that keeps recommending a deleted command.

### Criterion 4 — legacy fixture class reconciled — **MET, and the re-verification claim is true**

`legacy/` is gone; `compat/` holds the two survivors. The fixtures README
documents which fixtures were retired and why, per behavior rather than in bulk.

The README asserts: "re-verified against every fixture in this corpus after
td-09f7de removed the schema-v1 migration path. `Check` itself was not changed by
that work, and no recorded `check` outcome moved."

**Independently confirmed.** All 32 fixtures were re-run against the current tree
(§ td-a1d16a, criterion 1) and all 32 outcomes match the recorded table. So the
removal did not change any recorded fixture outcome, and the claim of
re-verification is not merely asserted.

### Criterion 5 — manifest `oracle_gaps` and `source_sha` updated — **MET**

`manifest-issues drift`: no drift; every slice's Ruby source is unchanged since
its `source_sha`. The ten `oracle_gaps` that previously said "Ruby carries these
guard branches, excluded from scope" now state the opposite and correct thing —
that the migration is gone and the version gate that replaced it **must** be
ported, with `Omitting that gate is a data-corruption bug, not a skipped dead
branch` written into the record. That is the substantive version of this
criterion: the risk of deleting a branch is that the port also drops the refusal,
and the manifest now says so at each of the ten sites.

### Verdict: **CLOSE**

No defects. This is the cleanest of the six.

---

## Follow-up issues filed

| Issue | Story | Summary |
|---|---|---|
| **td-44d49b** | td-34d915 | `capture`'s porcelain parsing can report `implementation_clean=true` while `bin/` or `lib/` is dirty — currently doing so |
| **td-36d27d** | td-34d915 | GATE.md's "What this gate does NOT prove" is missing three caveats (dirty-tree baseline, unexercised HTTP dimension, `ok` ≠ adequate coverage) |
| **td-d0f00b** | td-940935 | `manifest-issues plan` is not clean (20 stale td descriptions); two slices' pin notes contradict their `source_sha` |
| **td-fc2c99** | td-a1d16a | Two rows of the fixtures README sanitization table are false as written (self-referential miscounts) |

All are P2, labelled `porting`. None blocks the 27 port slices from starting.

## In-flight work observed, and whether it invalidates anything signed off here

At review time the working tree carried uncommitted changes from other streams:
`bin/tasks`, `porting/compare/lib/dimensions/files.rb`,
`porting/runners/{ruby/run,cases/phase1.jsonl}`, a regenerated
`porting/evidence/phase1/` (30 observations, including
`cli-capture-readonly-rollback`, `cli-done-ambiguous-ref`,
`cli-done-no-match-ref`), `porting/evidence/wiring/`, and 17 new fixture
directories.

- Nothing above is reported as a defect of these six stories.
- Every criterion signed off was verified either against artifacts the in-flight
  work does not touch, or in a detached worktree at `c500866`.
- The in-flight work **strengthens** rather than invalidates: it is closing
  td-c51338 and td-f2cb42, the two gaps GATE.md discloses. When it lands,
  GATE.md's caveat section will need updating anyway — td-36d27d should be
  folded into that same edit.
- The 17 new fixtures were sanitization-scanned and are clean.
- One consequence to watch: `porting/evidence/phase1/provenance.json` has already
  been regenerated by the in-flight capture and now carries the false
  `implementation_clean: true` described in td-44d49b. Whoever commits that
  regeneration should fix `capture` first, or the committed provenance will
  certify a baseline captured against a modified `bin/tasks`.
