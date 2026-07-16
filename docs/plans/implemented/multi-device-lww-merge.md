# Plan: multi-device merge — per-record timestamps (C) + a JSONL merge driver (B)

Status: implemented (2026-07-16); independent review complete

Implementation summary:

- `Tasks::Store#write_records` stamps only semantically changed task records;
  `Tasks::UpdateStamp` owns validation, device normalization, formatting, and
  comparison. Undo/redo remains a raw-byte restore and task revisions exclude
  `updated`.
- `Tasks::JsonlMerge` performs the checked, deterministic record/field merge;
  `tasks merge-driver` is the Git plumbing adapter and
  `bin/install-merge-driver` writes repository-local config with an absolute
  CLI path.
- The `tasks-marcus` data repo registers both JSONL files in `.gitattributes`,
  ignores `.tasks-merge.log`, documents per-machine setup, and validates both
  files before commit and again after rebase before push.
- The driver is installed in the current `tasks-marcus` checkout. Each
  additional syncing machine must run `~/code/tasks/bin/install-merge-driver
  ~/code/tasks-marcus` once after pulling these changes.
- Existing records remain migration-free and acquire stamps lazily. Until an
  actively edited record has stamps from both writers, a genuine same-field
  pre-C conflict remains deterministic ours-wins and is called out as
  low-confidence in `.tasks-merge.log`.

## Problem

`tasks.jsonl` is synced between two machines (work, home) via a git
push/pull loop (`tasks-marcus/bin/autocommit`). Both machines edit on the same
day, so the repo diverges and a rebase runs. Git merges `tasks.jsonl`
*textually* — one line per record — which produces a conflict whenever the
**same record** changed on both sides, even for non-overlapping fields (add a
tag here, reschedule there). Those land as `<<<<<<<` markers a human resolves by
hand.

Option A (pull-before-push in `autocommit`) is already in place and shrinks the
divergence window. This plan is the durable fix for the collisions A can't
auto-resolve.

## Strategy

Two independent pieces that compose:

- **C — per-record `updated` timestamp.** Every record carries an `updated`
  RFC3339 timestamp (UTC, with a device tag for tie-breaking). Stamped by the
  CLI on any record it actually changes. This is the *data* the merge needs.
- **B — a git merge driver for `*.jsonl`.** A script git invokes on
  merge/rebase instead of textual line-merging. It parses base/ours/theirs by
  `id` and does a **field-level 3-way merge**, using `updated` as the
  last-writer-wins tiebreaker for fields that genuinely diverged.

C makes B principled (a real tiebreaker instead of a guess); B makes C actually
resolve conflicts (a timestamp alone does nothing at merge time). Ship C first —
it's inert on its own and starts backfilling timestamps immediately — then B.

Estimate: C ≈ half a day, B ≈ 1–1.5 days incl. tests. Together they should
auto-resolve the ~90% of collisions that are edits to *different fields of the
same record*; the residue (same field, both sides) resolves by newest `updated`,
which is what a human would usually pick anyway.

---

## Part C — per-record `updated` timestamp

### C1. Schema

Add `updated` to the record schema in `lib/tasks/format.rb`:

- Append `"updated"` to `KEY_ORDER` (last data field, after `body`, so existing
  lines diff-minimally and the human-interesting fields stay leftmost).
- Value: RFC3339 UTC to the second, plus a device suffix for deterministic
  tie-breaking when two writes share a second, e.g.
  `"2026-07-16T14:03:11Z#home"`. The device tag is a short slug derived from
  `Socket.gethostname` (already used in `prompt_facts.rb`). Keep it in the
  string (not a separate field) so it travels as one comparable token and can't
  drift out of sync with the timestamp.
- Omission rule: unlike other fields, `updated` should **not** be dropped when
  empty — but it's never empty once stamped, so no `Format.omit?` change is
  needed. Records that predate C simply have no `updated` key; that's a valid
  "unknown/oldest" state the merge driver treats as losing every tiebreak.

No `Format::VERSION` bump required: `updated` is an additive field an old reader
round-trips untouched (forward-compat is already a Format guarantee, see its
header comment). Bump only if we later make behavior *depend* on it in a way old
readers would corrupt — they won't here.

No `check.rb` edit is required for acceptance: `Check::KNOWN_KEYS` is derived
from `Format::KEY_ORDER` (check.rb:34), so adding `updated` to `KEY_ORDER`
silences the unknown-key warning automatically. But no value validation runs
either — teach `check_task` the `RFC3339Z#slug` pattern so a malformed
`updated` is reported instead of passing silently.

### C2. Where to stamp — one choke point, diff-based

Every mutation ends in the flock'd read-modify-write that calls
`write_records(path, records)` (`store.rb:2254`). Rather than hunt every
`patch_*` method, stamp at persist time by **diffing working vs. original**:

1. In the locked mutation wrapper, keep the pre-mutation records (the changeset
   path already holds the before/after serialized text as `original_records` /
   `proposed_records` at `store.rb:767`/`778`; `working_records` at `:768` is
   the mutable record array — generalize that to a by-`id` comparison).
2. Before `write_records`, for each record whose **serialized form changed**
   (new record, or any field differs) *excluding the `updated` key itself*,
   set `updated` to `now()`.
3. Records that didn't change keep their old `updated`. This is what lets the
   merge driver tell "home touched this record" from "home just happened to
   rewrite the whole file."

Stamp granularity is **per record**, not per field. Per-field timestamps would
resolve more collisions exactly but roughly triple the on-disk size and
complicate the schema; per-record LWW is the right call for a personal tool and
still auto-merges the common case (see B's field rules — non-overlapping fields
merge *without* needing the timestamp at all; the timestamp only arbitrates the
rare same-field clash).

Implementation notes:
- Inject the clock. Store already threads `today:`/`Date.today`; add a
  `now:` seam (default `-> { Time.now.utc }`) so tests are deterministic.
- Device slug: compute once (`Socket.gethostname`, take the first label,
  downcase, strip to `[a-z0-9]`), memoize. Allow `TASKS_DEVICE` env override for
  when hostnames collide or aren't stable.
- The `meta` and `section` records don't need `updated` (sections rarely change
  and have no cross-device edit story); scope stamping to `type == "task"` to
  keep noise down. Revisit if sections start colliding.
- Undo/redo is naturally safe: journal restore goes through
  `restore_file`/`Atomic.write` with raw snapshot bytes (`store.rb:2342-2356`),
  bypassing `write_records` entirely, so a restore never re-stamps. Add a test
  pinning that.
- ETag-neutral by construction: `updated` is not in `EditSnapshot::FIELDS`, so
  it stays out of `REVISION_OWN_FIELDS` and the location/lifecycle
  fingerprints — stamping must not churn task revisions. Keep it that way
  (don't add it to those field lists).

### C3. Don't stamp on non-edits

Guard against the whole file re-stamping when nothing semantically changed
(e.g. a formatting-only rewrite, or `line` bookkeeping): the diff in C2 compares
*serialized records with `line` and `updated` stripped*, so a pure re-dump
stamps nothing. Verify with a test: load → save with no mutation → file byte
count and every `updated` unchanged.

### C4. Tests (C)

- Format round-trips `updated`; `KEY_ORDER` places it after `body`.
- A single-field patch stamps only the touched record's `updated`.
- A no-op save stamps nothing.
- Create stamps the new record; delete removes it (no ghost `updated`).
- Clock + device slug are injectable; slug sanitization handles a hostname like
  `Marcus-MBP.local` → `marcus`.

### C5. Migration / rollout

No migration pass needed — existing records get `updated` lazily the next time
each is edited. If we want a clean baseline, a one-shot `tasks maintenance
stamp-updated` can stamp every record once with a shared timestamp; optional,
low value. Land C, let it push from both machines for a few days, *then* land B —
by then most active records carry timestamps.

---

## Part B — the `*.jsonl` merge driver

### B1. Registration (lives in the DATA repo, `tasks-marcus`)

The driver is configured where the data and the conflicts are:

- `tasks-marcus/.gitattributes`:
  ```
  tasks.jsonl   merge=tasksjsonl
  archive.jsonl merge=tasksjsonl
  ```
- Git config (must exist on **both machines** — `.gitattributes` selects the
  driver, but the command is machine-local config, so document it and add a
  one-liner installer):
  ```
  git config merge.tasksjsonl.name  "tasks jsonl 3-way record merge"
  git config merge.tasksjsonl.driver "tasks-merge %O %A %B %P"
  ```
  `%O` base, `%A` ours (also the output path git reads back), `%B` theirs,
  `%P` the real pathname. The driver writes the merged result to `%A` and exits
  0 on success. On non-zero exit git does **not** re-run the textual merge — it
  marks the path conflicted with whatever the driver left in `%A` (initially
  ours). So on hard failure the driver must itself produce today's conflict
  markers (`git merge-file %A %O %B`, which writes markers into `%A`) — or
  leave `%A` untouched as plain ours — before exiting non-zero.
- Ship `tasks-merge` as a CLI subcommand (`bin/tasks merge-driver`) or a small
  standalone script in the CLI repo, so the merge logic is versioned and tested
  with the app, not pasted into git config. The `driver` line must use the
  **absolute path** to `bin/tasks` (e.g.
  `/Users/marcus/code/tasks/bin/tasks merge-driver %O %A %B %P`): `tasks` is a
  shell alias, not a PATH entry, and `autocommit` runs under launchd with a
  fixed PATH that doesn't include the CLI repo. The installer should resolve
  and embed the absolute path per machine.
- Add `bin/install-merge-driver` (or a `tasks setup sync` command) that writes
  the two `git config` lines into `tasks-marcus`'s `.git/config`. Run once per
  machine. Document in `tasks-marcus/README.md`.

Because a driver only runs when it's configured, **guard for its absence**: an
unconfigured driver name in `.gitattributes` falls back to git's normal
textual merge (verified: markers appear as today), and `autocommit`'s existing
"abort + log on conflict" fallback (rebase --abort + `.autocommit.log`)
handles the conflict, so a machine that hasn't installed the driver degrades
to today's manual resolution rather than a broken merge.

### B2. Algorithm (3-way, by id, field-level)

Input: three JSONL files (base/ours/theirs). Parse each with the *same*
lenient `Format.parse`, index records by `id`. Also preserve non-task lines
(`meta`, `section`) and record order (see B4).

For the union of all ids across the three sides:

1. **Present in one side only** → take it (a genuine add on that side).
2. **Deleted on one side, unchanged on the other** (present in base + one side,
   value equals base) → honor the delete.
3. **Deleted on one side, edited on the other** → *keep* the edited version
   (deletion loses to a concurrent edit) and log it. Data loss is worse than a
   resurrected task in a personal tool; revisit if it's annoying. (Note:
   completion moves tasks to `archive.jsonl` rather than deleting, but a real
   hard-delete exists — `tasks delete` / `delete_task!` removes the subtree
   outright, no archive — so this branch is rare, not theoretical.)
4. **Present on both, identical** → take as-is.
5. **Present on both, differ** → **field-level 3-way merge**:
   - For each field key across ours/theirs (excluding `line` bookkeeping and
     `updated` itself, which gets the max-of-inputs rule below):
     - unchanged vs base on one side → take the other side's value (classic
       3-way: only one side moved).
     - both sides changed it to the *same* value → that value.
     - both sides changed it to *different* values → **last-writer-wins by
       `updated`**: take the field from whichever record has the newer `updated`
       (compare timestamp, then device slug as a stable tiebreaker). If neither
       has `updated` (pre-C records), fall back to a deterministic rule
       (ours) and log the field as a low-confidence resolution.
   - Special-cased fields (semantic, not raw LWW):
     - `tags`: **union**, preserving first-seen order. (Matches the by-hand
       resolution earlier — an added tag is virtually never meant to be
       dropped.)
     - `state`: prefer a **terminal/progressed** state — `DONE`/`CANCELLED`
       beat `TODO`/`NEXT`; if both terminal and different, newest `updated`.
       Carry `closed` along with the winning state so they stay consistent.
     - `body`: if both sides appended (one is a prefix of the other), take the
       longer; else LWW. Keep simple — bodies rarely collide.
   - Re-stamp the merged record's `updated` to the max of the two inputs (so a
     later merge on the third machine still sees it as recent), not `now()` —
     the merge isn't an edit.

Output: `Format.dump` the merged records in canonical order (B4), write to `%A`.

### B3. Determinism

The driver must be a pure function of (base, ours, theirs) — no clock, no
randomness — so the same merge on both machines yields byte-identical output and
doesn't itself create a new divergence. All tie-breaks (device slug ordering,
"ours" fallback) are deterministic. Add a test that runs the merge with ours/
theirs swapped and asserts the result is equivalent modulo the documented
ours-wins fallback.

### B4. Ordering & non-task lines

`tasks.jsonl` is ordered (sections, then tasks under a `parent`, in a
manual/meaningful order — see `manual-task-ordering.md`). The driver must not
scramble it:

- Keep `meta` first.
- Preserve each section's position and each task's position relative to its
  neighbors. Practical approach: take **ours** as the ordering skeleton, insert
  theirs-only records adjacent to their `parent`/anchor (mirror the
  `parent_id`/`before_id` anchor *model* in `task_placement.rb` — the actual
  resolution algorithm lives in Store), and drop records deleted per B2. Order
  collisions
  (both sides reordered) are rare and low-stakes → ours wins, logged.
- This is the fiddliest part. If it proves hard to get right, a defensible v1 is
  "ours ordering skeleton + append theirs-only tasks at the end of their
  section" and refine later.

### B5. Logging & escape hatch

- The driver writes a short summary to `tasks-marcus/.tasks-merge.log`
  (gitignored) — per record: `id`, decision, any low-confidence field. So a
  silent auto-merge is auditable.
- If parsing any side fails hard, or an invariant is violated (e.g. duplicate
  ids within one side), **exit non-zero** → git falls back to conflict markers.
  Never emit a corrupt file.
- **Extend `autocommit` to run `tasks check` after a merge, before pushing**
  (the validator exists; autocommit doesn't call it today — nor any `tasks`
  command). On failure it aborts and logs, so a bad merge can't propagate.
  Needs the absolute `bin/tasks` path for the same launchd-PATH reason as B1.

### B6. Tests (B)

Drive the driver as a pure function over fixture triples:

- Non-overlapping field edits (add tag on A, reschedule on B) → both applied, no
  conflict. *This is the headline 90% case.*
- Same field, both changed → newer `updated` wins; swap → same winner.
- Pre-C records (no `updated`) → deterministic ours-wins + logged.
- tags union; state DONE-beats-TODO; delete-vs-edit keeps edit.
- Add-on-both (different new ids) → both kept, order sane.
- Ordering preserved; `meta` stays line 1.
- Malformed side → non-zero exit (fallback), no output written.
- Determinism: swapped inputs → equivalent output.
- End-to-end: construct the real divergence from 2026-07-15 (the Sixt/PSE/Stash
  collision) as a fixture and assert it auto-resolves to the by-hand result.

---

## Sequencing

1. **C** (schema + stamping + tests). Land, deploy to both machines, let it run
   a few days so records accrue `updated`.
2. **B** (driver + registration + installer + tests). Land in the CLI repo;
   `.gitattributes` + installer in the data repo.
3. Keep `autocommit`'s abort-and-log fallback throughout — it's the safety net
   for an un-installed driver or a driver bug.
4. Watch `.tasks-merge.log` / `.autocommit.log` for a couple weeks. If residual
   conflicts cluster on one field, tighten that field's rule (or promote it to
   per-field timestamps).

## Out of scope / later

- Per-field timestamps (only if per-record LWW proves too lossy).
- Section/`parent` reordering conflicts beyond "ours wins."
- Replacing git transport entirely (options D/E from the earlier discussion).
