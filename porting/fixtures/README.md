# `porting/fixtures/` — the sanitized store corpus

Every fixture here is a self-contained store an implementation can be pointed at.
The corpus is the input side of the conformance harness: `porting/runners/`
executes invocations against **copies** of these, and `porting/compare/` diffs the
observations two implementations produce.

**Nothing here is derived from real task data.** Every record was constructed for
this corpus. Titles, bodies, tags, assignees, work refs, and URLs are synthetic;
addresses use `example.com`, URLs use the reserved `.invalid` TLD, and device
slugs are `fixture`. Marcus's live store lives outside this repository and was
never read, copied, or sampled. See [Sanitization](#sanitization) below.

## The four classes

| Class | Holds |
|---|---|
| `valid/` | healthy stores, from the empty boundary to a 461-record ordering stress |
| `compat/` | version skew between binaries: bytes a *newer* binary produced |
| `malformed/` | broken stores, each paired with the diagnostic Ruby actually produces |
| `adversarial/` | concurrency, mid-write, revision, lock, and journal states |

## The fixture directory contract

```text
porting/fixtures/<class>/<name>/
  README.md           what it exercises, what a correct implementation must do,
                      and the recorded Ruby outcome. Always present.
  store/              the store bytes. Copy this whole directory; point
                      TASKS_DIR at the copy. Always present.
    tasks.jsonl       always present (occasionally zero bytes, on purpose)
    archive.jsonl     present only where the fixture exercises the archive
    .tasks.jsonl.lock          present only in the stale-lock fixture
    .tasks.jsonl.<pid>.<tid>.tmp   present only in the leftover-temp fixture
    tasks.real.jsonl           present only in the symlinked-store fixture, where
                               tasks.jsonl is a symlink pointing at it
  perms.json          optional. Modes git cannot record (a 0600 bit). The runner
                      applies these to the copy after copying and BEFORE
                      observing the pristine tree
  journal/            optional. Undo history — see "Installing a journal"
  <extra>.json        optional fixture-specific data (e.g. captured revision
                      tokens). The fixture's README says what it is
```

Rules for a runner:

1. **Copy before use.** `cp -a <fixture>/store/. <tmp>/` and run against `<tmp>`.
   Fixtures are read-only inputs; a runner that mutates one in place corrupts the
   corpus for every later case. Note the trailing `/.` — several fixtures carry
   dotfiles that a plain `cp -a <dir>/* ` would miss.
2. **Point the implementation at the copy** with `TASKS_DIR`, and unset
   `TASKS_FILE` / `TASKS_ARCHIVE` so a stray per-file override cannot redirect
   part of the store elsewhere.
3. **Isolate the environment.** The config file at `$XDG_CONFIG_HOME/tasks/config`
   is read even when `TASKS_DIR` is set, and it carries the user's timezone,
   urgent-day window, themes, and link shorthands. Point `XDG_CONFIG_HOME` at an
   empty directory.
4. **Pin the ambient inputs** the plan's Phase 1 list names: timezone, device
   slug, and the clock. The outcomes recorded in this corpus used
   `TASKS_TIMEZONE=UTC` and `TASKS_DEVICE=fixture`.
5. **Never point a runner at the live store.** `bin/tasks config` reports where
   that is; the only correct use of that information is to stay away from it.

The exact environment every recorded outcome below was produced under:

```sh
cd "$copy" && env -u TASKS_FILE -u TASKS_ARCHIVE \
  TASKS_DIR="$copy" XDG_CONFIG_HOME="$empty" XDG_STATE_HOME="$copy/.state" \
  TASKS_TIMEZONE=UTC TASKS_DEVICE=fixture \
  ruby bin/tasks check [--all-files]
```

Recorded against `9528bd6` on ruby 4.0.6 (arm64-darwin23), and re-verified
against every fixture in this corpus after **td-09f7de** removed the schema-v1
migration path. `Check` itself was not changed by that work, and no recorded
`check` outcome moved.

### Installing a journal

Five `adversarial/` fixtures ship a `journal/`. Three of them (`journal-cursor-behind-store`,
`journal-missing-blob`, `journal-foreign-org`) record a *refusal*; the two added
with the delete work (`journal-undo-redo-delete`, `journal-redo-pending-delete`)
are the only journal fixtures whose recorded outcome is a *success*, and the only
proof that a replay against a Ruby-written journal round-trips byte-exactly. Both
also carry `org_sha: null` at state 0 — the history predates the file — which is a
shape no earlier journal fixture has and is not a defect.

The undo journal is **not** part
of the store directory: it lives under `$XDG_STATE_HOME/tasks/journal/<key>/`,
where `<key>` is `sha256(realpath(tasks.jsonl))[0,16]`, and its `index.json`
records the canonical org path as well. Both depend on where the copy landed, so
a journal cannot ship as literal bytes.

A runner installs one like this:

```sh
org="$(cd "$copy" && pwd -P)/tasks.jsonl"   # symlink-resolved, as Ruby resolves it
key=$(printf %s "$org" | shasum -a 256 | cut -c1-16)
jd="$copy/.state/tasks/journal/$key"
mkdir -p "$jd" && cp -a "$fixture/journal/blobs" "$jd/"
sed "s|{{ORG_PATH}}|$org|" "$fixture/journal/index.json.template" > "$jd/index.json"
```

`adversarial/journal-foreign-org` is the exception: it ships a literal
`index.json` with no placeholder, because a *wrong* org path is what it tests.
Copy that one through unchanged.

## Index

`check` is `tasks check` (live file only). `check --all-files` is listed only
where it differs.

### `valid/`

| Fixture | Exercises | `check` |
|---|---|---|
| `empty-store` | header only; the empty-collection boundary | 0 — `ok — 0 tasks parsed` |
| `single-task` | a parentless task as a tree root; singular pluralization | 0 — `ok — 1 task parsed` |
| `small-gtd` | the ordinary healthy store: sections, projects, all open states | 0 — `ok — 8 tasks parsed` |
| `deep-nesting` | nine levels past `max_depth`; ancestor-stack unwinding | 0 — `ok — 6 tasks parsed` |
| `full-field-matrix` | every field: all states, times, `fold`, all six recur forms, lead, delegation, unicode | 0 — `ok — 32 tasks parsed` |
| `delegation-closed-provenance` | a delegation retained on a DONE and on a CANCELLED task; provenance, not routing | 0 — `ok — 2 tasks parsed` |
| `temporal-both-times` | one record with `scheduled` + `scheduled_time` + `deadline` + `deadline_time`; the KEY_ORDER interleave | 0 — `ok — 1 task parsed` |
| `interleaved-tags` | owned/unowned tag interleaving in both directions — the corpus's only plain tag between two contexts *and* context between two plain tags | 0 — `ok — 4 tasks parsed` |
| `recur-calendar-grammar` | twenty canonical recur forms, one per record — the accept side beyond the six simplest | 0 — `ok — 20 tasks parsed` |
| `symlinked-store` | `tasks.jsonl` is a relative symlink; the write follows it and the link survives | 0 — `ok — 1 task parsed` |
| `restricted-mode-store` | mode 0600 carried across an atomic replace; needs `perms.json` applied at copy time | 0 — `ok — 1 task parsed` |
| `archive-pair` | the two-file store; `archived` stamps; `--all-files` | 0 — `ok — 2 tasks parsed` (`--all-files`: 0 — `6 records`) |
| `id-pin-collision` | the first three pinned mint ids already taken, two archived and one live | 0 — `ok — 1 task parsed` (`--all-files`: 0 — `3 records`) |
| `scale-ordering` | 461 records, 50 sibling groups — ordering bugs at size | 0 — `ok — 400 tasks parsed` |
| `link-corpus` | every link construct: org labelled/unlabelled, punctuation and paren trimming, dedupe order, classification and host fallback | 0 — `ok — 18 tasks parsed` |
| `deferred-tags` | the `defer` hold: own, inherited two levels, and closed/proposed/archived carriers; the scope-dependent `--deferred` | 0 — `ok — 9 tasks parsed` (`--all-files`: 0 — `10 records`) |
| `project-rollup-edges` | `held_count` over own and inherited holds, plus the empty / done-only / sub-section / held-only-area exclusion edges | 0 — `ok — 13 tasks parsed` |

### `compat/`

| Fixture | Exercises | `check` |
|---|---|---|
| `forward-compat-unknown-keys` | a store from a *newer* binary: unknown top-level and delegation keys, preserved with warnings | 0 with 3 warnings |
| `future-schema-v3` | an unreadable schema version — refused on every surface, never converted | 1 — `unsupported meta version 3 (expected 2)` |

### `malformed/`

| Fixture | Exercises | `check` |
|---|---|---|
| `empty-file` | zero bytes — the `missing meta record` branch | 1 |
| `truncated-final-line` | a non-atomic write that died mid-record | 1 |
| `invalid-json` | a bad record between good ones; parser-message passthrough | 1 |
| `missing-meta` | no schema header | 1 |
| `meta-out-of-place` | header on line 2, plus a second header later | 1 — 3 errors |
| `duplicate-ids` | the id uniqueness invariant; error on the *last* line | 1 |
| `missing-id-single` | one task record with no `id`; the only store `ensure_id!` can repair | 1 — 1 error |
| `missing-ids-many` | three records with no `id`; the repair writes and is rolled back | 1 — 3 errors |
| `dangling-parent` | a parent that resolves to nothing; error containment | 1 |
| `wrong-key-order` | canonical key order violated | **0 — passes. See finding below** |
| `broken-dfs-order` | a subtree that is not a contiguous run of lines | 1 |
| `bad-utf8` | invalid encoding; the line-0 whole-file convention | 1 |
| `bom-prefixed` | a leading UTF-8 BOM; `valid/single-task` plus three bytes | **0 — passes, BOM stripped on read and not re-emitted** |
| `wrong-types` | 21 records, one violation each; the checker must not raise | 1 — 24 errors |
| `temporal-unknown-nested-key` | an unknown key inside `scheduled_time` / `deadline_time` — the drop half of `NESTED_FORWARD_COMPAT` | 1 — 2 errors |
| `recur-non-canonical` | twenty-seven rejected recur values, one per record; the grammar's reject side | 1 — 27 errors |
| `non-record-lines` | blank line, JSON array, bare JSON string | 1 — 3 errors |
| `cross-file-duplicate-id` | one id in both files — invisible without `--all-files` | 0 (`--all-files`: 1) |
| `duplicate-open-titles` | the warnings channel: a hazard that still exits 0 | 0 with 1 warning |

### `adversarial/`

Every store in this class is structurally valid — the breakage is in the tokens,
the sidecars, or the journal, none of which `check` inspects. The recorded
non-`check` behavior is in each fixture's README.

| Fixture | Exercises | `check` |
|---|---|---|
| `stale-revision` | `v1.<own>.<location>.<lifecycle>` tokens captured before an edit; `If-Match` 412 | 0 |
| `same-owner-retry` | a worker re-claiming its own claim (refused — see finding) | 0 |
| `conflicting-claim` | the genuine lost race, plus a release by a non-holder | 0 |
| `mid-write-leftover-tmp` | an orphaned `.tasks.jsonl.<pid>.<tid>.tmp` after a crash | 0 |
| `mid-write-torn-file` | a store torn mid-record: reads tolerate it, writes refuse | 1 |
| `stale-lock-sidecar` | an unheld `.tasks.jsonl.lock`; advisory, kernel-released locking | 0 |
| `journal-cursor-behind-store` | an out-of-band edit after the journal tip; undo refuses | 0 |
| `journal-missing-blob` | a journal blob deleted; undo degrades to "nothing to undo" | 0 |
| `journal-foreign-org` | a journal keyed to someone else's store; history discarded | 0 |
| `journal-undo-redo-delete` | the undo/redo happy path: a Ruby-written journal at the tip, byte-exact undo and redo of a leaf and a cascade delete | 0 — `ok — 1 task parsed` |
| `journal-redo-pending-delete` | the only cursor-behind-tip index; redo reachable, and the redo tail truncated by any new mutation | 0 — `ok — 4 tasks parsed` |

## What was found while building this

Ten things worth carrying into the port. Each is recorded, not fixed — no Ruby
code was changed for this corpus. Findings 4–10 came from the 2026-08-01 fixture
push; several are filed in td as well, and the id is named where one exists.

**1. `tasks check` does not validate key order.** `docs/conventions.md` names
canonical key order an invariant the tooling relies on, but `Check` works on
parsed hashes and never sees the serialized order. `malformed/wrong-key-order`
lints clean and exits 0. A port must match that — and must still emit canonical
order on write, which is a `porting/compare/files` obligation rather than a
`check` one.

**2. Reads tolerate a torn store; writes do not.** `Format.parse` returns the
records it managed to parse alongside its errors, and the read commands use the
records and ignore the errors. `tasks list` on `adversarial/mid-write-torn-file`
prints a store that is silently missing a record, exit 0. Only the mutation path
runs the preflight `Check` that refuses.

**3. A same-owner claim retry is refused, not idempotent.** `tasks claim` gates on
`delegation.status`, not on identity, so a worker retrying after a crash gets
`conflict: already claimed by <itself>`. Idempotent retry is the design a port is
likely to reach for; this corpus pins the actual behavior.

**4. The temporal drop path is unreachable, and the store wedges** (td-2addce).
`Format::NESTED_FORWARD_COMPAT` leaves `scheduled_time` / `deadline_time` out
deliberately, its comment calling the next write the repair path for an unknown
key there. That write never happens: `Check.check_temporal_time` makes the unknown
key a hard error, and every mutation preflights `Check` over the whole file, so
`malformed/temporal-unknown-nested-key` refuses every mutation with `task file is
already invalid` and writes nothing. Reads still work. The only exit is a hand
edit. A port must reproduce the refusal, not the comment.

**5. Link dedupe prefers the labelled form — as of the td-794997 fix, and not
before it.** Both written specs (`links-read`'s behavior sentence and
`Links.extract`'s comment) said the labelled form survives a dedupe. The
implementation disagreed: it recorded offsets, sorted by them, and `uniq(&:url)`
kept the earliest, so a label survived only when it happened to appear first.
`valid/link-corpus` carries the same url in both orders — `11c00009`
labelled-first, `11c00010` bare-first — which is how the divergence was caught,
and an implementation written from either written spec passed the first and
failed the second. Marcus decided the behavior was wrong rather than the prose,
so `Links.extract` was changed: the first LABELLED occurrence now wins outright,
a bare one stands in only when no occurrence is labelled, and order is untouched
(a later label replaces an earlier bare entry in place, so every surviving link
still sits at its url's first occurrence). This README's recorded output was
re-recorded from the fixed oracle — `11c00010` gains its label, `11c00009` is
byte-identical and is the control. **A port must not implement
`uniq`-by-first-occurrence**: that is the pre-fix oracle, and no fixture here
describes it any more.

**6. A held-only area is unreachable through the project surface.**
`TaskQueries#projects` lists every section child of `Projects` unconditionally,
but lists a top-level area only when its `open_count` is positive — and open work
that is on hold does not count towards `open_count`. So an area whose only open
task is deferred vanishes from `tasks projects`, and `tasks project show` on it
answers `no match` with exit 2, while a *project* in the identical state is still
listed. `valid/project-rollup-edges` carries both halves. Related: `held_count`
appears in `--json` only, never in the human rendering, so the human surface
cannot distinguish a project holding three parked tasks from one holding none.

**7. `--deferred` means two different things depending on scope.** At the default
open scope it is a pure availability test — identical to `--unavailable`, and it
lists a task that is merely dated in the future and carries no `defer` tag. At any
non-open scope (`--done`, `--archived`, `--proposed`, `--all`) it drops to the
literal `defer` tag test, where it returns exactly what `--someday` returns.
`--someday` is the only spelling that always means the tag, and it reports own
holds only — an inherited hold is an availability fact, not a tag fact.
`valid/deferred-tags` records every arm.

**8. Sidecar names follow the symlink; the lock's mode does not follow the
store's.** In `valid/symlinked-store` both the temp sibling and the lock resolve to
the *target* name (`.tasks.real.jsonl.lock`), by two independent code paths —
`Atomic.resolve` and `Journal.canonical`. In `valid/restricted-mode-store` the
store keeps 0600 across the replace but `Store#with_lock` opens the lock with a
literal `0o644`, so a deliberately private store acquires a world-readable
sidecar. The sidecar is empty, so only its existence and mtime are exposed;
recorded, not fixed.

**9. `Check` discards every reason `Recur` produced.** `Recur.parse_result`
returns richly specific rejections — `day of month must be 1–31: "32"`, `unknown
day of week: "xyz"`, the whole explanation of why `.+` cannot prefix a calendar
schedule. `check_task` calls the boolean `Recur.cookie?` instead, so all
twenty-seven rows of `malformed/recur-non-canonical` collapse into one message
that varies only by the inspected value. On the `check` surface a port therefore
owes `cookie?` fidelity only — accept/reject agreement and the one fixed message —
and reproducing Ruby's per-reason wording there would be a divergence, not an
improvement. Those messages *are* user-visible on the input surface (`tasks
recur`, `Recur.explain`), which is a different slice: one grammar, validated
twice, at two levels of diagnosis.

**10. Id repair is a record repair, not a store repair** (td-d6ed92).
`Store#ensure_id!` is reachable from exactly one command (`tasks id`) — never from
a read, never as a side effect of another write. It mints one id and then
`with_history`'s post-write `Check` validates the *whole file*, so it converges
only when that record was the file's last remaining error. A store with two id-less
records has no command that can fix it; the only exit is a hand edit. Recorded, not
fixed. Two further details a port must match: the repaired record gains an
`updated` stamp, because `stamp_changed_tasks!` indexes by id and the just-minted
id is in no original, so a repair is indistinguishable from a new task; and `tasks
capture` on such a store reports `could not capture (no "Inbox" section found?)` as
its first stderr line even when an Inbox is present — the true cause is the second
line, from the `store_invalid?` branch, and the misleading first line is contract
now.

## Why there is a `compat/` class and no `legacy/` one

The corpus originally shipped a `legacy/` class of seven, built from the formats
found in the code and history. Two of the three things it covered have since been
deleted from the Ruby, so the class was retired in favor of `compat/`:

- **JSONL schema v1 → v2: dead.** `Format::VERSION` is 2; the bump landed in
  `80691f4` with the temporal (timed-task) work, and the only structural
  difference is that v2 records may carry `scheduled_time` / `deadline_time`.
  `Store#migrate_schema!` and `tasks migrate` converted v1 stores until
  **td-09f7de** removed them: Marcus confirmed on 2026-08-01 that no schema-v1
  store exists, and carrying a migration into the Go port meant carrying ten
  dead guard branches through its riskiest slices. The four `schema-v1*`
  fixtures went with it. What survives is the *refusal*, which is contract:
  any declared `meta` version other than 2 is refused on read and on write, with
  nothing written and no command offered. `compat/future-schema-v3` pins it.
- **Forward skew: live.** Unknown top-level keys and unknown `delegation` keys
  round-trip with a warning rather than being dropped or refused. This is the one
  cross-version case that still happens in practice — Marcus runs several devices,
  so a store written by a newer binary is an ordinary Tuesday. Two fixtures, one
  per half of the contract (tolerate unknown keys, refuse an unknown version).
- **Org-mode: gone, twice over.** The store was an Org file until `7d70cff`.
  `15ad280` added an org → JSONL importer under the `tasks migrate` name, and
  `e5c505c` deleted it once the data repository was cut over; `tasks migrate`
  itself is now gone too. The `legacy/org-pre-jsonl` fixture was retired with it:
  it recorded a diagnostic (thirteen JSON parse errors) that is just the ordinary
  invalid-JSON path, already covered by `malformed/invalid-json`.

## Sanitization

Every fixture is constructed. Nothing was copied, sampled, or paraphrased from
the live store; the live store was never opened. The `adversarial/` fixtures were
produced by running the real CLI against a scratch directory seeded with invented
tasks, which is why their ids are randomly generated and their `updated` stamps
carry a real timestamp — the stamps' device slug is the injected `fixture`, not a
hostname.

**What the table below counts** (td-fc2c99). Every row is a count over
**fixture payload bytes only**: `porting/fixtures/*/*/store/**`,
`porting/fixtures/*/*/journal/**`, `perms.json`, and any other fixture-specific
data file. It deliberately excludes all prose — this README, and every per-fixture
`README.md` — because prose has to *name* the strings it is asserting about, and
counting it makes the table count itself. Two rows were literally false over the
whole committed tree for exactly that reason (`email addresses` matched its own
row; `marcus / aerie / vorwaller` matched four times in this file's prose,
including the row asserting no matches). The scope is now stated rather than the
numbers fudged. The prose exclusion is not a loophole: prose is reviewed, and the
only real name it contains is Marcus's own, by his choice, in a repository he owns.

Verified over the committed tree, payload bytes only:

| Check | Result |
|---|---|
| email addresses | one distinct address, `sam.rivera@example.com` (invented; reserved domain), used twice — in `valid/full-field-matrix` and `valid/delegation-closed-provenance` |
| URLs | all on `example.invalid` or `example.com` (both reserved). `valid/link-corpus` holds ~30 of them, one per link construct; no real host appears anywhere in the corpus, which is why the built-in `SYSTEMS` rows keyed to apex domains are proved by unit test rather than by a fixture |
| absolute home paths | one: `/home/someone-else/tasks/tasks.jsonl`, the deliberate wrong path in `journal-foreign-org` |
| `updated` device slugs | `fixture` only — no hostname leaked |
| `marcus` / `aerie` / `vorwaller`, case-insensitive | no matches |
| journal `index.json` org paths | templated to `{{ORG_PATH}}`, except the deliberate foreign path above |
| runner state (`.state/`, logs) | none present |
