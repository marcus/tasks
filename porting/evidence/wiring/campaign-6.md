# CAMPAIGN 6 — seeding locking, revisions, the journal, undo/redo and coalescing

Companion to the eleven records in `campaign-6-records.jsonl`. `campaign-10.md`
holds the previous seeding pass's reasoning and this file has the same shape and
the same purpose: the next agent to read these records deserves to know why the
boundaries are where they are, and — as in campaign 10, and for entirely
different reasons — **what the conformance method cannot see**.

Input: `campaigns-5-6-9-12-inventory.md`, which is binding. This pass claims the
C6 bucket and nothing else. Four sibling passes (5, 9, 12) run in parallel.

Result: **11 new slices, 53 → 64.** One unclaimed source file
(`lib/tasks/journal.rb`) gains an owner. **102 previously unclaimed Ruby tests**
are claimed — the inventory's 100 (33 whole-file + 67 across nine mixed files)
plus the two `test_a_failed_write_*` cases §9 of the inventory adopted into C6
after the tree moved. No test is shared with any of the 53 existing records.

---

## 1. The inventory, taken as given

The inventory did the mechanical work and this pass did not redo it. What was
re-verified, because a wrong answer here is a collision with a sibling agent or
a bounced record:

| Check | Result |
|---|---|
| C6 whole-file: `test/test_journal.rb` unclaimed | **32** free, 8 claimed (§3f) — confirmed by reading the file against `manifest.jsonl` |
| C6 whole-file: `test/test_delegation.rb` unclaimed | **1** (`test_delegation_is_revision_aware_and_undoable`) — confirmed |
| C6 in the nine mixed files (Appendix A) | **67** — every name confirmed to exist as a `def` |
| The two `test_a_failed_write_*` adopted by §9 | present at `test_cli_mutations.rb:4431,4447` — committed since, claimed |
| Overlap with the 53 existing records | **empty** |
| Internal duplicates across the 11 records | **none**; 102 refs, 102 distinct |

The one unclaimed source file is `lib/tasks/journal.rb` (362 lines). Five of the
eleven records also *name* files another slice already owns, which the inventory
§2b requires rather than permits, because a closure that does not reach a file
does not watch it for drift: `lib/tasks/store.rb` (nine slices already name it),
`lib/tasks/application.rb`, `lib/tasks/task_changeset.rb`, `bin/tasks`, and
`lib/tasks/determinism.rb` for the coalesce-scope pin. **No campaign 6 record
names `lib/tasks/atomic.rb`** — see §2 first.

`lib/tasks/journal.rb` was already inside `drift`'s watch set as a
*transitive-only* file (inventory §1a) because `store.rb` requires it. After this
pass it is named, so the eleven slices watch it directly and a change to it is
attributable rather than merely detected.

---

## 2. §3f, handled first: atomic replacement is NOT in this campaign

The playbook's campaign 6 line reads "Locking, **atomic replacement**,
revisions, rollback, journal, undo/redo, and coalescing". Inventory §3f
overrides it and this pass honors the override in three concrete places, because
this is the highest-cost collision available to a campaign 6 agent:

1. **`lib/tasks/atomic.rb` appears in no campaign 6 `source_paths`.** It is
   `store-canonical-write`'s (campaign 4), whose behavior sentence already names
   "temp sibling, fsync, symlink following, permission carry-over, directory
   fsync".
2. **`test/test_journal.rb` is not a whole-file claim.** The eight
   `test_atomic_write_*` cases at lines 27–103, plus
   `test_mutation_through_a_symlinked_org_keeps_the_link`, are already claimed by
   `store-canonical-write`. Campaign 6 owns that file from line 119 down and
   nothing above it. The file was read line by line against the manifest rather
   than claimed by name — the 32 free tests were enumerated, not assumed.
3. **Where campaign 6 legitimately touches the same code, it says so.**
   `store-file-lock` ports `Store#lock_path`, which resolves the sidecar name
   through `Journal.canonical` — the same symlink resolution
   `store-canonical-write`'s own gap already describes for `Atomic.resolve`
   ("both the temp sibling and the lock resolve to the TARGET name"). Two slices
   prove two halves of one property against `valid/symlinked-store`, and
   `store-file-lock` names the other slice in its gaps so the pair is discoverable
   from either end.

The reverse trap was also avoided. `store-file-lock` does **not** claim
`test/test_journal.rb#test_mutation_through_a_symlinked_org_keeps_the_link`
even though it is a lock-path test in substance: it is claimed, and a
double-claim would be a silent duplicate the validator allows.

The related §3e trap — campaign 9's cross-process HTTP concurrency — is honored
by omission: no record here names `test/api/**` or `bin/tasks-api`, and
`test/test_store.rb#test_ordinary_read_snapshot_does_not_run_the_api_structural_check`
is left for campaign 9 despite sitting in a file that is otherwise 5/6.

---

## 3. Where the cuts are

Eleven slices. The graph, in an order where every dependency can go green first:

```
                                       ┌─ journal-degradation
store-file-lock ─┬─ store-checked-write-rollback ─┬─ journal-index-and-blobs ─┤
                 │                                │                           └─ undo-redo-apply ─┬─ undo-redo-rollback
store-revision-token ─ revision-staleness-changeset ─ revision-staleness-verbs │                   ├─ history-coalescing
                                                                               └───────────────────┴─ cli-undo-redo
```

Written out, with the campaign-4/7 edges each one needs:

| # | Slice | Tests | Risk | Campaign 6 deps | Outside deps |
|---|---|---:|---|---|---|
| 1 | `store-file-lock` | 6 | high | — | store-snapshot-items, store-canonical-write, create-basic, delegation-claim-release |
| 2 | `store-revision-token` | 3 | medium | — | store-snapshot-items, tree-build |
| 3 | `store-checked-write-rollback` | 12 | high | 1 | store-canonical-write, changeset-apply-basic, task-placement, check-report-and-cli |
| 4 | `revision-staleness-changeset` | 9 | high | 2, 3 | changeset-apply-basic |
| 5 | `revision-staleness-verbs` | 12 | high | 4 | delete-task, task-placement, delegation-assign, delegation-claim-release |
| 6 | `journal-index-and-blobs` | 7 | high | 1, 3 | — |
| 7 | `journal-degradation` | 10 | high | 6 | — |
| 8 | `undo-redo-apply` | 13 | high | 3, 6 | store-repair, task-placement, delegation-assign, delegation-claim-release |
| 9 | `undo-redo-rollback` | 9 | high | 8 | archive-sweep |
| 10 | `history-coalescing` | 17 | high | 8 | store-repair |
| 11 | `cli-undo-redo` | 4 | medium | 8 | cli-mutation-json-envelopes, archive-cli |

Nine of eleven are tiered **high**, which is the tier table applied rather than
bent: "anything that writes: store, journal, locking, archive, merge". The two
exceptions are stated rather than assumed. `store-revision-token` is medium
because it computes a digest from an in-memory record list and writes nothing —
it is a pure function of the snapshot, in the same shape as campaign 3's
projections. `cli-undo-redo` is medium because the writing is `undo-redo-apply`'s
and what remains is argv, messages, exit statuses and one JSON envelope.

**Why the lock is first and alone.** `Store#with_lock` is 30 lines and could have
been folded into the checked-write slice. It is separate because its failure
modes are not write failures: a `(Thread, Fiber)` reentrancy key, an explicit
`CrossFiberLockError` refusal instead of a block (a Fiber that waits on its own
thread's flock deadlocks the owner that would release it), a documented
per-*instance* limit that would deadlock two Stores on one file in one thread,
and a sidecar created by **reads** as well as writes. Every one of those is a
concurrency review, not a persistence review, and PORTING.md wants those to be
different sittings. The slice also carries the two external-change detectors
(`Store#changed?`, `Application::ReadModel#stale?`) because they are the lock's
stated complement — the Ruby's own comment says the lock "does NOT constrain
out-of-band editors; those are caught by the post-write Check and the journal's
conflict detection".

**Why revisions split at the token / the comparison.** `store-revision-token`
ports `task_revision` — `v1.<own>.<location>.<lifecycle>`, three SHA-256 digests
over three deliberately different field sets, with dates normalized before
hashing so "equivalent Store snapshots never depend on Ruby object identity or
JSONL serialization details". The two staleness slices port the *comparison*,
which is a different decision each verb makes differently: an ordinary field edit
compares `own` only; a changeset adds `location` when it moves and `lifecycle`
when it touches state; a cascading delete compares **all three**; delegation
rides on `own` because ADR-0007 made the marker an own-field. A port that
computes the token correctly and compares all three components everywhere passes
nothing in slice 4 and looks conservative rather than wrong. That asymmetry is
the campaign's second-best defect and it wants its own review.

The verbs are split from the changeset (slice 5 from slice 4) on a scheduling
argument, not a conceptual one: slice 5's twelve tests drive `delete_task!`,
`TaskPlacement`, `undelegate_task!` and the CLI patch adapter, so it cannot go
green until four campaign-4 slices have. Slice 4's nine drive
`apply_task_changeset!` only.

**Why the journal splits three ways.** `journal-index-and-blobs` is the happy
path and the identity rules: the XDG directory keyed by
`sha256(realpath(org))[0,16]`, the `org` field recorded *inside* `index.json` as
a second guard, content-addressed dedup, the `limit`-based cap and the blob GC.
`journal-degradation` is ten tests whose entire content is that a corrupt,
missing, non-regular or hostile journal **degrades to "nothing to undo"** and
never crashes a command or touches task bytes — an out-of-range cursor, a
directory where a blob belongs, a symlink at the current blob that must never be
followed, a foreign `org`, an `EACCES` on the index, and a fatal error that must
still propagate. That is a containment contract, reviewed against a threat model,
not a feature. Folding it into the happy path produces a slice whose reviewer
reads seventeen tests and checks the two interesting ones.

`undo-redo-apply` is `Store#history_step` + `Journal#plan`: the label on the
higher-indexed state, the `expect` precondition that refuses on mismatch, the
restore, the archive-first ordering when a sweep is being replayed, the
restore-validity gate, and the `repair` exemption from it. `undo-redo-rollback`
is the nine tests that inject a failure *inside* that sequence — cursor commit
fails after installing the index, org restore fails, archive delete fails during
undo, a transient failure that must be retried exactly once, a persistent one
that must leave a loss-safe state, and a fatal exception that rolls files back
and *then* propagates. Nine tests, one shape, and the shape is "what does the
store look like when the recovery itself fails". It is the single most
port-hostile slice in the campaign and it should be claimed by an agent with
nothing else in flight.

**Why coalescing is one slice and a large one.** Seventeen tests, and they are
one decision seen from every side: a keyed tip is extended in place only when the
key matches, **and** the scope matches, **and** the cursor is at the tip, **and**
the cursor is positive, **and** the previous state's blobs still verify, **and**
the recorded tip still matches the caller's `before`. Six conjuncts, and the six
"break" tests exist one per conjunct. Splitting them would produce slices that
each prove one `&&` and none that proves the conjunction. The four
`test/test_determinism.rb` pin tests are here (inventory §3k) rather than with
campaign 1's harness because `TASKS_PIN_COALESCE_SCOPE` is **persisted into
`index.json`** — it is journal bytes, and `test_two_pinned_runs_produce_identical_stores_and_journals`
compares a whole index (minus `org`) across two runs, which is this slice's
headline property stated as a determinism assertion.

**Why `cli-undo-redo` exists at all with four tests.** Same reason
`cli-prompt-command` did in campaign 10: it is thin because everything else about
undo lives below it, and what it owns is nevertheless real and owned by nobody
else — that `tasks undo` from a **cold second process** reaches the same history
(the whole reason the journal is on disk rather than in the TUI), the exact
refusal wordings (`nothing to undo`, `changed since that edit`), the exit status
on an empty history, and the fact that a refused undo must not clobber the file.
Three of its four tests are the only place in the suite where two real `tasks`
processes share one history.

---

## 4. What is deliberately out of scope, and why

Recorded here **and** in the owning records' `oracle_gaps`, because
`slicing.md` §1 item 2 is the standing evidence that an exclusion stated only in
prose goes missing: `lib/tasks/opener.rb` sat in no slice for a whole pass
because one record excluded it with a reason.

- **Atomic replacement — campaign 4.** §2 above. Named in `store-file-lock`'s
  and `store-checked-write-rollback`'s gaps by slice id.
- **The TUI's undo affordances — campaign 12** (inventory §3a). Every keypress
  that reaches `undo!` and every repaint or flash message that follows it is in
  `test/test_app.rb`, which is wholly C12. `undo-redo-apply` and `cli-undo-redo`
  say so; neither claims a `lib/tui/` path.
- **Two processes racing through HTTP — campaign 9** (inventory §3e).
  `test/api/test_black_box.rb` spawns a real `bin/tasks-api` and races an API
  thread against a CLI thread; that proves the *server* observes a CLI write,
  which is the HTTP adapter's read model and its ETag. `store-file-lock` names
  the file and declines it, so the symmetric trap (campaign 9 claiming
  `test_concurrent_writers_do_not_lose_updates` because it looks like the same
  property) has a written counterpart on this side.
- **The nine registry-wide tests in `test/test_cli_json_coverage.rb` — campaign
  8** (inventory §3m, §7 item 5). Campaign 6 landing is what *unblocks* the final
  parity slice; it may not claim its tests. `cli-undo-redo` says this explicitly,
  because a reader who has just ported the undo envelope will find
  `test_every_json_command_prints_exactly_one_json_document` and think it is now
  claimable. It is not: it still reads the whole RECIPES table, which still
  contains `recur` and `lead` from unseeded campaign 5.
- **The six PRIOR tests in `test/test_delete_task.rb`** (`test_missing_or_blank_id_is_invalid`
  and siblings) — plain input validation with no revision content, an existing
  coverage hole in `delete-task`, not campaign 6's (inventory §3i).
  `revision-staleness-verbs` claims the five revision-aware cases named in
  `delete-task`'s own reservation and nothing else from that file.
- **`test/test_lead_matrix.rb`'s other seven tests** — four C5, three C12
  (inventory §3j). `revision-staleness-changeset` claims only
  `test_a_lead_write_conflicts_like_any_other_field`, which sits under the file's
  own `-- concurrency --` heading, and its record says the other seven are not
  free.
- **`lib/tasks/operation_context.rb`** stays unclaimed, for the third time. It
  is the application facade's, no campaign owns that yet, and "it has a `SOURCES`
  list and shows up near the journal" is not a reason.

---

## 5. What the conformance method cannot observe here, precisely

Per the brief, and following inventory §5b. **No schema extension is proposed.**
Every record carries the subset of this section that applies to it; this is the
consolidated statement.

Campaign 6 is not blind the way campaign 10 was, and saying so first keeps the
rest honest. `porting/runners/README.md` gives the observation a **`journal.*`
section**: the index parsed structurally — `version`, `cursor`, `states[]` with
labels, restored digests, coalesce key/scope **and the repair flag** — plus the
index file's raw bytes, the blob count and the sorted blob digests, with
`present:false` and a would-be path when no journal exists. The corpus ships five
journal fixtures, two of which (`journal-undo-redo-delete`,
`journal-redo-pending-delete`) were generated by driving the real CLI. So the
journal slices have a real differential path, and `delete-task`'s existing gap is
right that the content-addressed dedup is externally observable and constrains
the port's design. What follows is what remains dark.

### 5a. No field records a lock being *acquired* — only that a sidecar appeared

`determinism.md` § "Tempting but not normalized" keeps `.tasks.jsonl.lock` in
`files.deltas` on purpose, so a run against a pristine copy is *not* delta-free:
a **read** produces `{"path": ".tasks.jsonl.lock", "kind": "created"}`. The
harness therefore sees **that a lock file appeared**. It cannot see that mutual
exclusion held, that `flock(LOCK_EX)` was called rather than the file merely
being opened, that the descriptor stayed open across the read-modify-write, or
that the lock was released. Concretely, for `store-file-lock`:

- `test_with_lock_rejects_cross_fiber_contention_and_cleans_up` **has no
  conformance expression at all.** It asserts that a second Fiber on the owning
  thread raises rather than blocks. There is no case-list vocabulary for a second
  Fiber, and no observation field would record the refusal if there were.
- `test_shared_store_reads_stay_coherent_across_threads` is the same shape one
  level out: threads inside one invocation, invisible to a protocol whose unit is
  a process.
- A port that opened the sidecar `RDWR|CREAT`, never called `flock`, and wrote
  emits a **byte-identical observation** to a correct one on every single-process
  case in the corpus. `adversarial/stale-lock-sidecar` narrows this usefully —
  it proves a port did not implement a *lock-file-exists* protocol, because such
  a port would deadlock on a store that is perfectly available — but it does not
  prove the positive.

### 5b. fsync, the temp sibling and the rename are entirely unobservable

`files.deltas` carries `{path, kind, before_sha256, after_sha256}` and
`files.after[]` carries mode and content. There is **no field for durability, no
field for the temp sibling** (determinism.md: atomic-write temp filenames "are
gone before an observation is taken"), **no field for the directory fsync, and no
field for the rename itself**. The consequence, stated as bluntly as it deserves:
**an implementation that writes in place, correctly, produces an observation
identical in every byte to one that writes a temp sibling, fsyncs it, fsyncs the
parent directory, and renames over the target.** `store-canonical-write`'s own
gap already records that `fsync` appears nowhere in `test/` either, so neither
the oracle nor the harness covers it.

This lands on campaign 6 rather than staying campaign 4's problem because every
recovery path in slices 3, 8 and 9 is *built on* atomicity. `rollback_history_files`
retries "because atomic replacement means a failed attempt leaves either the
complete old or complete new file" — the comment is in the Ruby. A port whose
writer is not actually atomic makes those retries produce torn state, and no case
in the corpus can tell.

The runner's own §"A failing write" is explicit about the ceiling: `copy_root_mode`
"buys exactly one crash point — the write — from outside the process", against
the playbook's step 7 which wants "crash points around lock, write, flush,
validation, rename, and journal append". Six wanted, one available, and the one
available is the only reason slice 3's rollback tests have any differential path
at all.

### 5c. `files.rolled_back` is null unless the caller asked for `--json`

The field is read from the implementation's own `--json` error envelope, "never
inferred from stderr wording, and never from the deltas: a write-then-revert and
a never-wrote leave identical bytes, which is why the field exists". The runner's
known-gaps list adds that null means "not reported" and **never** "did not roll
back".

`store-checked-write-rollback` is the slice this bites. Its subject is precisely
the distinction between *wrote nothing* and *wrote and reverted*, and on any case
that does not pass `--json` the observation is silent about which happened.
Worse, the two newest tests in the campaign
(`test_a_failed_write_never_blames_validation`,
`test_a_failed_write_diagnostic_carries_no_path_and_no_exception_text`) assert
things the schema cannot express as assertions at all: that the diagnostic does
**not** contain `failed validation after the edit`, does not contain the run's
own absolute path, and does not contain `rb_sysopen` or `Permission denied`. A
byte comparison of stderr catches a port that emits the wrong string only if the
Ruby baseline was captured on the same path — and the path is exactly what the
test says must not be there. The absence assertions survive translation as unit
tests; they do not survive as a diff.

The same slice inherits a structural limit from `store-repair`'s territory: the
post-write check can never fire from fixture content alone, because a store that
is already invalid is refused by the *preflight*. The only route is a write that
*fails*, which needs `copy_root_mode` on the case — a key no existing case uses.

### 5d. The journal index's `org` key forces same-absolute-path runs

`index.json` records the store's canonical absolute path, and unlike the
directory key that value is *inside* bytes the harness digests. determinism.md
refuses to normalize it — "rewriting bytes before digesting them is exactly the
move that makes a byte comparison stop meaning anything" — and solves it by
running "both sides against copies at the same absolute path (sequentially, or
via a per-side mount)".

Two consequences campaign 6 must not discover late:

1. A cross-path run is compared **with the journal index excluded**, which
   excludes precisely the bytes slices 6–10 exist to prove. A green `--cross-path`
   run on a journal case proves the store bytes and nothing about the history.
2. It is worse than that today. `fixture.root_sha256` is compared **as a
   precondition, ahead of everything else**, and a journal-bearing fixture's
   installed journal directory is named for a digest of the copy's own absolute
   path — so the whole-tree digest moves with the copy root and a cross-path
   comparison of any journal case **reports a harness error rather than running**.
   The honest operating instruction for this campaign is: capture and compare at
   one absolute path, sequentially.

### 5e. One case is one invocation — the dominant structural constraint

The case list has `case_id`, `fixture`, `argv`, `surface`, `cwd`, `env`, `stdin`,
`timeout_ms`, `install_journal`, `copy_root_mode`, `notes`. One argv, one
process, one before/after pair. **There is no vocabulary for a second process, a
second invocation against the same copy, an interleaving, or an ordering between
two of them.** Campaign 6 is the campaign this constraint was written for, and
the count is large enough to name:

| Slice | Tests with no differential path, and why |
|---|---|
| `store-file-lock` | `test_concurrent_writers_do_not_lose_updates` (8 concurrent `tasks capture` processes), `test_cli_claim_race_leaves_exactly_one_holder` (two racing claims), `test_with_lock_rejects_cross_fiber_contention_and_cleans_up` and `test_shared_store_reads_stay_coherent_across_threads` (threads/fibers inside one process) — 4 of 6 |
| `journal-index-and-blobs` | `test_two_stores_share_one_history`, `test_shared_history_across_two_path_spellings`, both `*_survives_a_new_store_instance`, `test_capping_keeps_only_recent_history_across_instances` — sequences of instantiations, 5 of 7 |
| `history-coalescing` | every "break" test requires a **second store instance** or an **intervening CLI write against the same copy**; `test_two_pinned_runs_produce_identical_stores_and_journals` is by definition two runs — 12 of 17 |
| `cli-undo-redo` | all four: mutate in process A, `undo` in process B, is the behavior |
| `undo-redo-apply` | `test_new_mutation_clears_redo`, `test_undo_stacks_multiple_mutations_in_order`, `test_undo_history_is_capped` — n mutations then an undo |

**Concurrency behavior has no differential path at all.** Not a weak one: none.
Every proof of it in this campaign is a translated unit test, which is the same
honest-but-not-differential position `agent-diff-report` and `agent-request-queue`
occupy in campaign 10, and it is why nine slices here are tiered high and why
PORTING.md's high tier asks for "real competing processes" as a *separate*
obligation from differential conformance.

There is a partial escape and the records name it rather than pretend otherwise:
a fixture may ship a `journal/` that *encodes the result of* a prior sequence.
That is exactly how `journal-undo-redo-delete` and `journal-redo-pending-delete`
were built — "generated, not hand-assembled", by running the real CLI through a
scripted sequence under pinned environment. So a *state reachable only by a
sequence* can be a case's input; the *sequence itself* still cannot be a case.
Every `fixtures_todo` in this campaign that asks for a new journal fixture is
asking for that, and nothing that needs a protocol change.

### 5f. The corpus has no journal carrying a `coalesce_key`, `coalesce_scope` or `repair` flag

Found by reading all five shipped indexes. The runner *parses* those three fields
into the observation; **no fixture exercises any of them.** That is the sharpest
corpus gap this pass found and it is the mirror of campaign 10's
`llm-provider-registry` finding: a fixture set that reads as adequate to whoever
built it, against which a port that dropped coalescing entirely, or dropped the
repair exemption, emits a byte-identical observation on every existing case.
`history-coalescing` and `undo-redo-apply` carry it in `oracle_gaps` and in their
`fixtures_todo`, with the shape spelled out — a store plus a journal whose tip
carries `coalesce_key` and `coalesce_scope` (generated under
`TASKS_PIN_COALESCE_SCOPE`, which exists precisely so this is reproducible), and
a second whose tip carries `repair: true` over an invalid before-state.

### 5g. Two things that are nondeterministic rather than unobservable

Recorded separately because the answer is different:

1. **`Store#changed?` is `stat`-based**, and `test_changed_detects_external_writes`
   has to call `File.utime(future, future, org)` with a comment saying "avoid
   same-mtime flakiness". Filesystem timestamp granularity is not in the pin
   table and cannot be; a port using a different staleness token (a revision
   compare, an inode+size pair) is not wrong and will not match a byte-level
   expectation of the token. Compare the *decision* (`stale?` true/false), never
   the token.
2. **The default coalesce scope is `SecureRandom.hex(16)`, fresh per process.**
   It is journal bytes, so an unpinned run's `index.json` differs from itself run
   twice. `TASKS_PIN_COALESCE_SCOPE` is the answer and the runner already sets it
   to `pinned-scope` — but `test_store_factory_defaults_to_a_random_coalesce_scope`
   asserts the *unpinned* behavior, so that one test asserts the very thing the
   pin removes and can never be a conformance case.

### 5h. `reach` cannot see any of this

Inventory §5e, and this pass hit it exactly as predicted. `VERB_OWNERS` maps
**store mutation verb methods** only. An oracle that reaches downstream through
`Journal#undo`, `history_step`, `with_lock` or `Journal#record` is invisible to
the tool, which means `reach` will keep reporting zero unexplained no matter what
these eleven records claim. The whole campaign's dependency correctness was
therefore checked by reading test bodies, and the check found six reaches — five
into `archive_swept!` from `undo-redo-rollback` (undo of an archive sweep is a
two-file restore, which is why those tests exist), answered with a real
`archive-sweep` dependency edge; and one into `patch_task!` from
`store-revision-token`, answered with a gap sentence naming the test, because the
revision is computed when the snapshot is built and the patch is only how the
test *observes* it. Reading the body is the only defense and every characterizing
agent on this campaign should do it.

---

## 6. Existing records this pass falsifies

Not amended here — `manifest.jsonl` is merged centrally. Recorded with the id and
the exact sentence, in the shape inventory §4 asked for.

### Falsified — the sentence becomes factually wrong

1. **`archive-cli`**, `oracle_gaps` (fourth), in full:

   > "`undo` and `redo` have no slice, so this slice cannot prove that the three
   > lifecycle commands share one envelope; it proves archive's. The shared shape
   > is a campaign 6 obligation."

   `undo` and `redo` now have a slice: **`cli-undo-redo`**. The rest of the
   sentence stays true and the obligation stays owed — it should read that the
   shared shape is `cli-undo-redo`'s to close, and `cli-undo-redo` depends on
   `archive-cli` so it can be closed from this side.

2. **`archive-cli`**, `oracle_gaps` (first), the clause:

   > "Its other two thirds cannot pass at this position: `undo`/`redo` are
   > campaign 6's journal and `open --json` has no slice at all."

   Half wrong twice. `open --json` has had a slice (`open-command`) since before
   this pass — pre-existing staleness, not caused here. The undo/redo half now
   names `cli-undo-redo` rather than a campaign number.

3. **`cli-read-json-envelopes`**, `oracle_gaps[2]`:

   > "The mutation envelopes, the lifecycle ones (archive/undo/redo), `open
   > --json`, and the {error, action, message} refusal object are not seeded
   > anywhere yet — they need their own slice before the port can claim CLI
   > structured-output parity (td-ee475f)."

   Already three-quarters stale before this pass (`cli-mutation-json-envelopes`,
   `archive-cli`, `open-command` all exist). Campaign 6 makes the last quarter
   stale: `cli-undo-redo` seeds the undo/redo envelope. Nothing in the sentence
   is true any more.

### Stays true, acquires an id it should name

4. **`delete-task`**: *"…need revisions, which are campaign 6. Excluded."* → all
   five are `revision-staleness-verbs`', claimed verbatim from that list.
5. **`changeset-apply-basic`**: *"Revision staleness (test_changeset_returns_stale_for_*)
   is campaign 6."* → `revision-staleness-changeset`.
6. **`delegation-assign`** and **`delegation-claim-release`**: *"expected-revision
   checking is campaign 6"* → `revision-staleness-verbs`.
   `delegation-assign` additionally excludes
   `test_delegation_is_revision_aware_and_undoable`, which `undo-redo-apply`
   claims.
7. **`proposal-decisions`**: *"Expected-revision checking is campaign 6, so that
   third assertion cannot pass here"* → `revision-staleness-changeset`. Note the
   test itself (`test/test_proposals.rb#test_decision_refuses_non_proposals_stale_revisions_and_proposal_trees`)
   stays where it is; campaign 6 does **not** claim into `test_proposals.rb`.
8. **`delegation-claim-release`**: *"…which is a locking property and campaign 6
   owns locking. It stays because 'exactly one holder' is the whole point of a
   compare-and-set claim; characterization must record that the proof depends on
   the lock and re-run it once campaign 6 lands."* → `store-file-lock`. This is
   the one amendment with an operational instruction attached: the re-run is owed
   the moment `store-file-lock` is green, and `store-file-lock` depends on
   `delegation-claim-release` so the edge exists in the right direction.
9. **`state-transitions`**: *"test_set_state_is_undoable … reaches the journal,
   which is campaign 6"* → `undo-redo-apply`.
10. **`state-cascade-close`**: *"test_complete_cascade_is_one_undo_step_restoring_bytes
    reaches the journal (campaign 6)"* → `undo-redo-apply`.
11. **`section-create-and-rename`**: *"test_rename_section_retitles_and_round_trips_through_undo
    reaches the journal (campaign 6)"* → `undo-redo-apply`.
12. **`archive-sweep`**: two sentences — the `undo!` half of
    `test_archive_and_history_against_v1_are_refused_as_an_unsupported_schema`,
    and *"test_undo_archive_sweep_restores_both_files and the undo half of
    #test_archive_nested_subtree_preserves_dfs_structure_and_undo reach the
    journal (campaign 6)"* → `undo-redo-apply` for the first,
    `undo-redo-rollback` for the two-file restore ordering, which is the property
    those tests actually pin.
13. **`archive-project`**: *"…undo deletes a fresh archive … reaches campaign 6's
    journal"* → `undo-redo-apply`.
14. **`store-canonical-write`**, `notes`: *"Sliced here deliberately; the locking,
    revision, and journal halves stay in campaign 6."* Correct, and now nameable:
    `store-file-lock`, `store-revision-token`, `journal-index-and-blobs`.
15. **`cli-mutation-json-envelopes`**, `oracle_gaps[0]`: names *"`undo` and `redo`
    (campaign 6)"* among the RECIPES-table blockers. The blocker becomes a named
    slice rather than an absence — but **the nine tests stay campaign 8's**, and
    the sentence's substance is unchanged, because `recur`/`lead` (campaign 5,
    unseeded) is still in the same table.

### `source_paths` — no existing record becomes wrong

Four files gain a co-owner in the `prompt-facts`/`config-resolution` pattern:
`lib/tasks/store.rb` (a tenth owner), `lib/tasks/application.rb`,
`lib/tasks/task_changeset.rb` and `lib/tasks/determinism.rb`. Each campaign 6
record that names one says in `notes` that it ports only part of the file, as
`campaign-10.md` §6 required. `lib/tasks/determinism.rb` is the one to watch: it
is in several closures transitively and named by **no** record before this pass,
so `history-coalescing` naming it is a small strengthening of `drift`, not just
bookkeeping.

---

## 7. What I recommend, and what I did not defer

**Nothing in the C6 bucket was deferred.** All 102 tests and the one source file
are claimed.

Four recommendations, none of them a slice:

1. **Build the two coalescing/repair journal fixtures early** (§5f). They are the
   campaign's cheapest large win: both are generated by running the real CLI
   under `TASKS_PIN_COALESCE_SCOPE`, exactly as the two existing generated
   journal fixtures were, and they convert `history-coalescing` and the repair
   exemption in `undo-redo-apply` from "unit-test-proved" to "differentially
   proved" without touching the protocol.
2. **Add the first `copy_root_mode` case before `store-checked-write-rollback`
   is claimed** (§5c). The key exists in the protocol and no case uses it; it is
   the only route to a *performed-then-reverted* write, and an agent that claims
   the slice first will hit it in the first hour — the same shape as campaign
   10's recommendation about the git work tree.
3. **Fix the campaign-6 half of `--cross-path` expectations in whoever's head
   holds them, not in the harness** (§5d). Journal cases are same-path-only
   today, by a precondition that fires before anything else runs. That is the
   honest state and this campaign should plan around it rather than file a bug.
4. **Consider extending `VERB_OWNERS` when campaign 6 lands** (§5h) — `undo!`,
   `redo!` and `patch_task!`-adjacent journal entry points would make `reach`
   able to see this campaign at all. This is a `manifest-issues` change, not a
   slice, and it is **not** proposed as part of this pass because it changes a
   tool that judges the port.

I did **not** seed the CLI-registry parity slice (inventory §7 item 5). Campaign
6 removes `undo`/`redo` from the blocker list that `cli-mutation-json-envelopes`'
gap and `campaign-10.md` §5 both still owe, but `recur`/`lead` (campaign 5) are
still in the RECIPES table, so the recommendation stands unchanged and the slice
remains campaign 8's.

---

## 8. Verification

Run against `campaign-6-records.jsonl` and the tree at `6c97990`:

```
102 ruby_tests, 102 distinct                                       ✓
0 refs shared with the 53 records in porting/manifest.jsonl        ✓
every ref resolves to a real `def test_` under test/               ✓
0 whole-file claims (every ref carries #test_name)                 ✓
every depends_on id exists in manifest.jsonl or in this file       ✓
the 11 records are a DAG; the file's order is a topological order  ✓
reach (VERB_OWNERS, computed by hand): 6 downstream reaches —
  5 answered by the archive-sweep edge on undo-redo-rollback,
  1 (store-revision-token → patch_task!) answered by a gap
  sentence naming the test                                          ✓
every fixture path named exists under porting/fixtures/            ✓
```

`porting/manifest.jsonl`, `porting/campaigns.jsonl`, all source, all tests and
all fixtures are **unmodified by this pass**. `source_sha` is `PENDING` on all
eleven records by instruction; the pins are Marcus's to set centrally, and
`validate` will reject the records until he does — that is the intended state,
not an oversight. The campaign record for campaign 6 is likewise not written
here.

One inherited condition the next agent should not mistake for its own doing: the
tree at this commit still carries the pre-existing drift and red test the
inventory recorded in §0 item 1, from `9b9e6e9` touching `bin/tasks`. It is not
campaign 6's to fix, and any campaign 6 record naming `bin/tasks` — only
`cli-undo-redo` does — must be pinned to its own closure's last touch rather than
to `e75019a3`.
