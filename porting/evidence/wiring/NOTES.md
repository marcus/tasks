# Cross-cutting items for the WIRING pass

Collected by the orchestrator as streams land. Each is something no single stream
owns, so it would otherwise fall between them.

## From FIXTURES-1

- `format-nested-key-order`'s `oracle_gaps` must record that
  `Format.dump_record`'s temporal unknown-key **drop branch is unit-only and
  unreachable end to end** — `Check` refuses the store before the repairing write
  can happen. Filed as **td-2addce**. Without this in `oracle_gaps`, a green
  conformance run reads as proof the port implements a branch no case can reach.

## From FIXTURES-4

- `delete-task`'s `oracle_gaps` must record that **journal blob storage is
  content-addressed and the dedup is externally observable** — eight states
  reference five blobs, because a delete can land back on a blob an earlier
  capture already produced. A port that stores deltas rather than whole-file
  blobs will not reproduce the index, and the difference is visible in the
  observation. This is a real constraint on the port's journal design, not a
  formatting detail.
- `porting/fixtures/README.md` § *Installing a journal* says "Three
  `adversarial/` fixtures ship a `journal/`". It is five now.
- `journal-undo-redo-delete` and `journal-redo-pending-delete` carry
  `org_sha: null` at state 0, a shape no existing journal fixture has. Worth a
  sentence in the corpus README so it is not read as a defect.
- Adjacency to check: FIXTURES-2's `valid/deferred-tags` and FIXTURES-4's
  `valid/interleaved-tags` both carry `defer` records and both carry an
  interleaved tag array. They target different slices and neither is redundant
  (only `interleaved-tags` has a plain tag between two contexts *and* a context
  between two plain tags), but confirm the two slices wire the right one.

## From FIXTURES-2

- **td-794997 is resolved; `links-read` describes its own oracle again.** Both
  written specs — the slice's behavior sentence and `Links.extract`'s comment —
  said dedupe prefers the labelled form, and the implementation kept the first
  occurrence instead. Marcus chose to change the code rather than the prose, so
  the labelled form now wins regardless of position and `valid/link-corpus` was
  re-recorded from the fixed oracle. The blocker on `characterizing` is lifted.
- `links-read`'s `fixtures_todo` is proposed null with two residuals moved to
  `oracle_gaps`: `SYSTEMS` rows keyed to real apex domains, and config-driven
  shorthands. Both are genuinely not store-shaped — the corpus bans real hosts and
  pins `XDG_CONFIG_HOME` empty by contract — and both are covered by the slice's
  `ruby_tests`. Confirm that reasoning before accepting the null.
- The corpus README's Sanitization table says the corpus holds "two URLs". Now
  wrong; FIXTURES-2 supplies a replacement row.

## From FIXTURES-3

- **`store-canonical-write` keeps a non-null `fixtures_todo` on purpose.** The
  corpus landed; the runner support did not. `valid/restricted-mode-store` needs
  one new copy-protocol step: apply `perms.json`'s chmod map to the copy between
  the copy step and the observe step, because git cannot record a 0600 bit and
  `cp -a` faithfully preserves the wrong 0644. This is a `porting/runners/`
  change and belongs to GATE, not to a fixture stream. `root_sha256` is defined
  over path strings and bytes only, so this cannot invalidate the baseline.
- `store-canonical-write`'s `oracle_gaps` should record two things the fixtures
  pinned: sidecar names **follow the symlink** by two independent code paths
  (`Atomic.resolve` and `Journal.canonical`), so a symlinked store locks
  `.tasks.real.jsonl.lock`; and the **lock sidecar's mode does not follow the
  store's** (`Store#with_lock` uses a literal `0o644`, so a 0600 store gets a
  0644 lock). The file is empty, so only existence and mtime leak.
- `check-task-fields`' `oracle_gaps` should record that **`Check` discards every
  reason `Recur` produced**, collapsing 27 distinct rejections into one message.
  A port owes `cookie?` fidelity on `check` and Ruby's per-reason wording on the
  input surface — one grammar, two levels of diagnosis. A port that wired the
  richer diagnostics into `check` would be *more* helpful and still wrong.
- `id-minting`'s `oracle_gaps` should record td-d6ed92 and the `updated`-stamp
  consequence of a repair.

## COMMIT BLOCKER — fix before anything in this push is committed

**td-44d49b: `porting/evidence/capture` can certify a dirty implementation as
clean.** `sh()` strips the whole `git status --porcelain` output, eating the
leading space of the first line; `l[3..]` then drops a character off the first
path. An unstaged-only change under `bin/` or `lib/` in the first porcelain entry
falls out of `implementation_paths_dirty`, and the file reports
`implementation_clean: true`.

This is not hypothetical and it is not historical. The working tree's
`provenance.json` right now records the path as `"in/tasks"` and claims the
implementation is clean **while `bin/tasks` is modified**. The comment above that
code calls this "the only claim that matters for a baseline's trustworthiness."
The committed `5eba4b0` provenance is correct only by luck — its first porcelain
line happened to be a staged delete, which has no leading space.

GATE's re-capture regenerates this file. So: fix `capture`, re-run it, and
confirm the regenerated `provenance.json` is honest, **before** the commit. Order
matters — committing the regeneration first would certify a baseline captured
against a modified `bin/tasks`.

## From IN-REVIEW (story-reviews.md)

- **td-d0f00b is WIRING's to close.** `porting/manifest-issues plan` is NOT clean
  today: `skip=22 update=20`. Sync was never re-run after `c500866` re-pinned 20
  slices, so `td-c4d282` still advertises a stale `source_sha`. Also
  `check-report-and-cli` and `cli-read-json-envelopes` carry `notes` prose saying
  "Re-pinned to 9132699" that contradicts their own `source_sha` — `drift` cannot
  structurally catch prose, so fix the prose while wiring.
- **td-36d27d:** three caveats missing from `GATE.md` — the baseline was captured
  from a dirty worktree (all 27 observations record `…-dirty` while the header
  cites the bare sha, and `implementation.*` is never compared, so nothing
  surfaces it); the HTTP dimension has zero observations and `audit` does not
  audit dimension coverage at all; and `ok` in the audit means merely ">= 1 case",
  so `write_bytes ok 3 case(s)` reads as a coverage verdict it is not.
- **td-fc2c99:** the fixtures README's own verification table miscounts itself —
  two rows are false as literal statements because the table's prose contains the
  strings it is counting. Fix while splicing the `corpus_readme` blocks.

## From SLICING

- **Do not renumber campaigns.** SLICING deliberately did not create campaigns 5
  or 6: ~20 existing `oracle_gaps` sentences already use "campaign 5" to mean
  recurrence and "campaign 6" to mean revisions/journal, and renumbering would
  silently invalidate every one of them. Instead: campaign 4 is amended (+8,
  retitled — the playbook's item 4 literally reads "…followed by lifecycle,
  proposals, and delegation", so its "not sliced yet" sentence was simply false),
  campaign 7 is new (+7), campaign 3 gains 2. 27 → 44 slices.
- **`manifest-issues`' `VERB_OWNERS` must be updated in the same change as the new
  records.** It maps 12 verbs to `nil` today; until it is updated, `reach` flags
  36 unexplained reaches. The additions are in `slicing.json` under
  `verb_owners_additions`, verified against a patched copy — after which `reach`
  reports "no unreachable oracles" across all 44, including existing slices whose
  reaches into `archive_swept!` resolve for the first time.
- **`store-canonical-write`'s fourth `oracle_gap` is stale in its final clause**
  and must be rewritten — characterizing the schema gate proved it is **five
  implementations in Ruby, not one**: `unsupported_schema_refusal` via
  `with_history`; `create_project!` inline in its own `with_lock`;
  `archive_swept_impl`'s direct check; `JsonlMerge.parse_side` (a genuinely
  *different* rule — `allow_older` for base, strict for sides); and the
  `CliCommands` gate registry. A port of what the manifest proved before this pass
  writes the first and leaves three doors open.
- **`Item#headline` is ported by nothing** — it is a key in every mutation JSON row
  and in `tasks show`. It belongs in `task-view-projection`; add it there rather
  than leaving it in `unclaimed_after`.
- SLICING lists ~40 tests in `test_store.rb`/`test_store_patches.rb` as suggested
  `ruby_tests` additions to existing slices. Several are the **only** oracle for a
  branch whose `fixtures_todo` says no store exists — the `set_deferred` clear
  direction especially. Worth wiring; they do not double-claim.

## Standing checks for WIRING

- Every `fixtures_todo` set to null must have been null-ed for the right reason:
  the corpus gap is closed, not merely narrowed. Where a stream narrowed it,
  the string must stay and say what remains.
- After wiring, `test/test_manifest_issues.rb:327` asserts no slice has its
  fixtures wired while a `porting-fixture-gate` issue is still open for it. Run
  `porting/manifest-issues sync` before closing those issues, and re-run the
  suite after.

---

## Resolution (orchestrator, end of push)

All streams landed. Final state: `audit` COVERAGE COMPLETE (exit 0), `validate`
33/33 observations valid and internally coherent, manifest `ok: 44 slices, 4
campaigns`, no drift, 21 reaches 0 unexplained, `plan` clean, `GATE PASS`, suite
2189 runs / 0 failures, loop limits 168/0. Every `fixtures_todo` is null and all
12 fixture-gap issues are closed.

One correction to `gate-remediation-cases.json`: it proposes slices `atomic-write`
and `journal-key-identity`, which do not exist. The real slice is
`store-canonical-write`, which already had both fixtures wired and whose
`fixtures_todo` described exactly the `perms.json` gap that the same stream
closed. Resolved by clearing that todo — verified first against
`cli-capture-restricted-mode`, which now records `0600` in `files.before` AND
`files.after`, so the "a chmod-600 store must not widen to 644 across an atomic
replacement" contract is genuinely proved rather than asserted. td-0ba1da closed.

Also corrected: WIRING-2 reported the commit blocker (td-44d49b) as unfixed. It
was fixed by GATE — `porting/evidence/capture` grew an unstripped `sh_raw` and
provenance now honestly reports `implementation_clean: false`. WIRING-2 inferred
this from the red provenance test without reading the diff; that test was red for
staleness, not for the porcelain bug.
