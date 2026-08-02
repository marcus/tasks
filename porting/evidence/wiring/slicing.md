# SLICING — seeding the unsliced half

Companion to `slicing.json`. That file holds the records; this one holds the
reasoning, because the next agent to read the manifest deserves to know why the
boundaries are where they are and not somewhere else.

Scope: td-ee475f (three areas in no slice) and td-8df1bb (two orphaned
schema-gate tests), plus whatever the mechanical survey turned up that neither
issue named. Marcus's decision was to seed the second half now rather than defer
it with a record, so that `manifest-issues progress` counts an honest
denominator.

Result: **17 new slices, 27 → 44.** One new campaign record (7), one amended
campaign record (4). `validate` passes on a scratch copy at 44 slices; `reach`
is clean; no test is claimed by two slices.

---

## 1. The survey, and what it found that td-ee475f did not

The inventory was taken mechanically, not from td-ee475f's list: every file
under `lib/tasks/**` and `bin/`, and every `def test_` under `test/**`, checked
against the `source_paths` and `ruby_tests` of all 27 records.

**22 source files were in no slice's `source_paths`.** Worth separating from a
related number: only three files (`lib/tasks/api/*`) are outside every slice's
*drift closure*. So 19 files are watched for drift by a slice that does not
port them — the closure rule is doing its job, and the coverage claim in
`manifest.md` ("every file under `lib/tasks/` sits in some slice's closure
except `lib/tasks/api/`") is true and is a different claim from "every file is
ported by some slice", which was false for 22 files.

Three things the survey found that td-ee475f did not:

1. **`test/test_schema_gate_reads.rb` — ten tests, zero claimed, no slice.**
   This is the CLI read-side version gate, and it is the same rule six existing
   slices' `oracle_gaps` call contract and a data-corruption bug to omit. The
   failure it prevents is the nastiest read failure in the system: a v3 store
   that renamed `title`/`state`/`tags` made `tasks list` print "No matching
   tasks." and `list --json` print `[]`, exit 0 — byte-identical to the answer
   for an empty store. A port that carries `Store#unsupported_schema?` but not
   the CLI's registry gate reproduces that exactly, and would pass every test
   any existing slice claims. Seeded as `cli-schema-gate-reads`, campaign 3,
   risk high.

2. **`lib/tasks/opener.rb` and `tasks open` — in no slice, on purpose, and
   never picked up.** `links-read` excluded the four `open` cases with a stated
   reason ("a separate command with a launcher seam") and nothing claimed them
   afterwards. `open --json` is also one of the four envelope families
   td-ee475f named in item 3, and the only one that is a read. Seeded as
   `open-command`, campaign 3.

3. **`Item#headline` is ported by nothing.** It renders state, priority, title
   and tags into one string; it is a key in every mutation JSON row and in
   `tasks show`. Three tests, no slice, and no behavior statement mentions it.
   Not seeded as a slice — it belongs in `task-view-projection` — but recorded
   in `unclaimed_after` because a port that reconstructs it independently in the
   CLI and the view layer will drift between them.

The survey also found ~40 tests in `test/test_store.rb` and
`test/test_store_patches.rb` that prove behavior an existing slice already owns,
through the store's older line-based verbs (`set_priority`, `set_deferred`,
`move_under`, …) rather than through the changeset API. These are **not** new
slices — double-claiming behavior is exactly what the brief forbids — but
several of them are the only oracle for a branch an existing slice's
`fixtures_todo` says it cannot reach. The `set_deferred` group is the clearest:
`changeset-apply-basic` says the clear direction of `patch_deferred` has no
store, and `test_set_deferred_false_removes_defer_tag` is precisely that proof.
They are listed as suggested `ruby_tests` additions to named slices.

---

## 2. Campaign numbering: why there is no campaign 5 or 6 here

The brief asked for campaigns 5–7. I did not create a campaign 5 or 6, and the
reason is that those numbers are already spoken for.

`manifest.md` defines `campaign` as "campaign number from the playbook's *A
proposed Tasks sequence*", `campaigns.jsonl` rejects duplicate numbers, and the
playbook fixes the meanings: 5 is temporal/availability/recurrence/time zones, 6
is locking/atomic replacement/revisions/journal/undo-redo, 7 is archive and git
merge, 8 is full CLI grammar and human formatting. Those meanings are not only
in the playbook — **twenty-odd `oracle_gaps` sentences across the existing 27
slices use them as cross-references**: "campaign 5" *means* recurrence to a
reader of `check-task-fields`, "campaign 6" *means* revisions to a reader of
`delete-task`. Renumbering to make room would silently invalidate every one of
those sentences, which is a worse regression than the one this pass is fixing.

So the work went where the playbook puts it:

- **Lifecycle, projects, delegation, proposals, mutation envelopes → campaign 4.**
  Playbook item 4 reads "Basic mutations and placement, *followed by lifecycle,
  proposals, and delegation*". It is one item. The existing `campaign-4-mutations`
  record says the second half "is not sliced yet"; that sentence becomes false
  the moment this proposal is applied, so `slicing.json` carries a
  `campaign_updates` entry retitling and resummarizing that record rather than
  inventing a number for half of one playbook item.

- **Archive sweep, section archiving, three-way merge, merge driver →
  campaign 7**, a new record, matching item 7 exactly.

- **The `open` command and the CLI read gate → campaign 3**, which is where
  reads and JSON envelopes live.

Campaign 4 now holds 15 slices, which is large. That is a consequence of the
playbook's item 4 being large, not of the boundary being drawn wrong; the
`plan_phase` is Phase 3 for all fifteen, and the dependency graph inside it is
linear enough that the epic still reads as one sequence.

---

## 3. Campaign 4's second half: where the cuts are

Eight slices, sequenced so each one's dependencies are true edges rather than
plausible ones.

```
changeset-apply-basic ──> state-transitions ──> state-cascade-close
                     └──> delegation-assign ──> delegation-claim-release
create-basic ──> section-create-and-rename ──> project-complete-and-close
create-basic + state-transitions + delegation-assign ──> proposal-decisions
                                    all of the above ──> cli-mutation-json-envelopes
```

**`state-transitions` / `state-cascade-close` split.** Both live in
`Store#patch_state`, and the temptation is one slice. They are two because the
cascade is a different failure mode: the single-record transition is about
`closed` stamp arithmetic (set on entry only, delete on exit — a port that
stamps today on every close loses the original date on DONE→CANCELLED), while
the cascade is about *how many writes* and *what the subtree boundary is*.
`close_open_descendants` walks the contiguous DFS run, not the parent/child
graph, so a port that reparents by id lookup cascades into records outside the
run whenever file order is broken. Those are two reviews, not one.

**Recurrence is cut out of both, and that is the sharpest edge in this
proposal.** `patch_state` short-circuits into `advance_recurrence_records`
whenever the record carries a recur cookie. A port could implement `patch_state`
without the recur guard, pass every test `state-transitions` claims, and
silently close a repeating task. The slice's `oracle_gaps` says so in those
words, because it is the kind of gap that reads as scheduling and behaves as a
bug.

**`section-create-and-rename` vs `project-complete-and-close` vs
`archive-project`.** Three section verbs, three slices, because they have three
different shapes: creation mints ids and can write two records in one
transaction (the Projects root bootstrap — the Ruby says explicitly that
splitting it would leave an orphan root plus a stray undo entry), completion is
a cascade close over descendants, and archiving is a two-file move that belongs
with the sweep in campaign 7. `create_project!` is also the slice that carries
one of td-8df1bb's two orphaned tests.

**Delegation split at the owner/worker line.** `delegation-assign` is the owner's
verbs (`delegate_task!`, `undelegate_task!`); `delegation-claim-release` is the
worker's (`claim_task!`, `release_task!`, `set_work_ref!`) plus settlement on
close. The line is not arbitrary: claim is the only compare-and-set in the store
and the only place two processes race for one field, so it wants its own review
and its own expected defect ("a port that reads, decides and writes without
holding the lock across all three passes every single-process test").
`test_close_clears_a_ready_marker_and_retains_a_claim_as_provenance` and
`test_completing_a_parent_settles_its_cascaded_descendants` sit here rather than
with the lifecycle slices, with `state-cascade-close` as a declared dependency —
`settle_delegation_on_close` is delegation's rule, the close is only how it is
triggered.

**`cli-mutation-json-envelopes` claims the per-verb envelopes and not the
registry table.** This is the deliberate half-measure of the proposal and is
explained in §5.

---

## 4. Campaign 7: archive and merge

Seven slices.

```
store-canonical-write + state-transitions + delegation-claim-release ──> archive-sweep
archive-sweep ──> archive-project
archive-sweep + cli-mutation-json-envelopes ──> archive-cli
format-canonical-emit + update-stamp ──> jsonl-merge-records ──> jsonl-merge-delegation
                                                            └──> jsonl-merge-structure
                                          all three merges ──> merge-driver-git
```

**Archive preview is inside `archive-sweep`, not beside it.** I drafted it as a
separate read-only slice and folded it back: the preview's `candidate_ids` and
`fingerprint` exist to be handed back into `archive_swept!` as
`expected_preview`, and a slice that ports the preview without the validation it
feeds proves a struct rather than a behavior.

**The sweep is the highest-risk record in this proposal** and its notes say why:
the write order *is* the safety property. Archive first, re-read the archive,
then rewrite live. A port that writes live first, or that trusts its own writer
instead of re-reading, converts an interruption from "retry-safe duplicates"
into silent data loss. It is also a second consumer of `format-canonical-emit`'s
byte contract — the fingerprint is a SHA over `Format.dump(moved)`, so a
different canonical writer produces a different fingerprint and every
`expected_preview` check fails.

**`archive-project` spans the store and `bin/tasks` on purpose.** The Ruby says
`archive_project!` does not block on open descendants because "blocking is
caller policy" — the refusal and the `--force` override live in the CLI. A slice
that stopped at the store would port a verb whose only safety gate is somewhere
else, which is not a portable unit.

**The merge is three slices, cut by resolution rule, not by file.** All three
port the same 558-line file, and the cut is: scalar/temporal/tags/body/state
(`jsonl-merge-records`), the delegation object (`jsonl-merge-delegation`), and
adds/deletes/ordering/ancestors (`jsonl-merge-structure`). Two reasons. First,
these are genuinely independent review surfaces — the delegation rules alone are
15 tests and encode a claim-arbitration policy that contradicts the store's.
Second, the merge's version rule is *not* the store's (`allow_older` for the
base, strict for both sides), and burying that inside a single 37-test slice
would hide the one thing most likely to be misported.

The delegation merge deserves reading next to `delegation-claim-release`: the
store gives the claim to the first writer and refuses the second; the merge
gives it to the *earlier `at`*, tiebreaking on the smaller assignee. Two
different correct answers to "who holds the claim" is the design — the store
arbitrates within one file, the merge arbitrates between two histories that were
both locally valid — and a port that shares one function between them will hand
work to the wrong worker in exactly the situation the feature exists for.

**`merge-driver-git` merges the driver command and the installer** because they
are one capability (the thing a user installs into their repo) and because
neither has enough oracle to stand alone: the driver's success path is proved
only by a test that shells out to real `git merge`.

---

## 5. What I recommend deferring, and why

Three deferrals, in decreasing order of confidence.

**a. Campaigns 5 (temporal) and 6 (journal/locking/revisions). Defer.**
This is ~290 and ~90 unclaimed tests respectively, and unlike td-ee475f's three
areas it is *not* invisible: eleven existing `oracle_gaps` name campaign 5 as
the home for availability, lead and recurrence, and nine name campaign 6 for
revisions and the journal. The boundary is legible today because those
exclusion sentences are specific. Seeding these badly would replace legible
exclusions with thin records and make the denominator honest at the cost of
making the content dishonest. They need their own pass, in the same shape as
this one. Campaign 6 is the closer of the two — its fixtures already exist
(`adversarial/journal-*`).

**b. The whole-registry CLI parity tests. Defer, and file a td issue.**
This is the recommendation I would most like reviewed. Thirteen tests in
`test/test_cli_json_coverage.rb` assert over the entire `CliCommands::ALL` table
— every alias dispatches, every `json: true` command prints exactly one JSON
document, every gated command refuses an unsupported store. They are the real
statement of "CLI structured-output parity", and no slice can claim them until
`recur`/`lead` (5), `undo`/`redo` (6) and `-p` (10) exist. Seeding a parity
slice now would put an issue in `td ready` that no agent can finish, with
`depends_on` edges that cannot be written because the slices they point at do
not exist — and td-940935 removed two of these from `cli-read-json-envelopes`
for precisely this reason. Re-adding them under a new slice id would undo that
finding. What they need is one final parity slice seeded when those campaigns
land. Recommend recording that obligation as a td issue now so it is not carried
only by this file.

The consequence, stated plainly: **`cli-mutation-json-envelopes` closes
td-ee475f item 3 for the mutation verbs and not for the registry.** The port can
claim "every campaign-4 mutation emits the documented envelope and the
documented refusal object"; it cannot yet claim "the CLI's `--json` coverage is
universal", which is what `docs/cli-spec.md` asserts.

**c. Agent execution (campaign 10). Defer pending a scope decision from
Marcus.** `tasks -p` is the one command that opts out of `--json` with a stated
reason, and an agent harness is exactly the kind of capability a port might
leave in Ruby or drop. A slice seeded and then marked `not_applicable` costs
more than a decision taken first — the same shape as the schema-v1/org-mode
decision already recorded in `manifest.md`.

The TUI (campaign 12, ~640 unclaimed tests) and the API (campaign 9, ~108) are
deferred without argument: both are last in the playbook and both are surfaces
over capabilities the earlier campaigns own. One note on the API, though: it is
the only part of `lib/tasks/` outside every slice's drift closure, which means
it is also the only Ruby whose change no `drift` run would report. That is worth
fixing when campaign 9 is seeded.

---

## 6. td-8df1bb: option (a), and a third gate nobody had counted

Marcus chose (a): seed the slices and let them claim the two orphaned tests.

| Test | Now claimed by |
|---|---|
| `test_project_mutation_against_v1_is_refused_as_an_unsupported_schema` | `section-create-and-rename` (campaign 4) |
| `test_archive_and_history_against_v1_are_refused_as_an_unsupported_schema` | `archive-sweep` (campaign 7) |

Characterizing them turned up the thing that makes td-8df1bb's warning concrete:
**the unsupported-schema gate is not one implementation in Ruby, it is five.**

1. `Store#unsupported_schema_refusal`, reached through `with_history` — the task
   mutation path (`store-canonical-write`).
2. `Store#create_project!` — checks inline inside its own `with_lock`
   (`section-create-and-rename`).
3. `Store#archive_swept_impl` — a direct `unsupported_schema?` test returning an
   `ArchiveRefusal` (`archive-sweep`).
4. `JsonlMerge.parse_side` — a *different rule*: `allow_older` for the base,
   strict for both sides (`jsonl-merge-records`).
5. `CliCommands`' `gate:` / `GATE_EXEMPT` registry — the CLI-level gate with
   three declared exemptions (`cli-schema-gate-reads`).

A port that implements exactly what the manifest proved before this pass would
have written (1) and left (2), (3) and (5) open — which is the corruption
td-8df1bb describes, reached by three different doors. All five now sit in a
slice, and each of those slices' `oracle_gaps` names its own implementation so
whoever ports it knows they are not re-porting the same code.

`store-canonical-write`'s fourth `oracle_gap` ("Two schema-gate tests have no
slice at all…") is now stale in its final clause. WIRING should rewrite it to
name the two slices that carry them and keep the substance — that the gate has
several independent implementations and porting one is not porting the rule.

---

## 7. Verification, and one thing WIRING must not skip

Checked mechanically against a scratch copy at
`/private/tmp/.../scratchpad/manifest.jsonl`:

- `manifest-issues validate` → `ok: 44 slices, 4 campaigns, every source path
  and oracle test resolves`.
- No test appears in two slices. The four pre-existing shared tests are
  unchanged; the 17 new slices add none.
- Every `source_paths` entry and every `fixtures` entry exists on disk. No
  fixture path was invented. Most new slices carry an honest `fixtures_todo`
  instead — the whole merge campaign has empty `fixtures`, because a three-way
  merge needs a base/ours/theirs triple and every fixture in the corpus is a
  single store.
- `source_sha` is `5eba4b0` for all 17: it is the last commit touching every one
  of their closures, computed the way `manifest.md` prescribes rather than
  pinned to HEAD.

**The one thing that must land with these records:**
`porting/manifest-issues`' `VERB_OWNERS` table maps twelve verbs to `nil`
(`archive_swept!`, `create_project!`, `claim_task!`, `decide_proposal!`, …).
Until it names the new owners, `reach` reports every oracle in these slices as
reaching an "(unseeded)" verb and exits non-zero on the unexplained ones — 36 of
them. `slicing.json` carries the mapping under `verb_owners_additions`.
`porting/manifest-issues` is GATE's path, not SLICING's, so this is a request
rather than a change.

Verified with a patched copy: with those twelve verbs mapped, `reach` reports
**"no unreachable oracles"** across all 44 slices — including the existing ones,
whose recorded reaches into `archive_swept!` (`store-snapshot-items`,
`tree-build`, `id-minting`, `store-canonical-write`) resolve for the first time,
because the slice they were waiting for now exists.
