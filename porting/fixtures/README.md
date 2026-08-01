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

Three `adversarial/` fixtures ship a `journal/`. The undo journal is **not** part
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
| `archive-pair` | the two-file store; `archived` stamps; `--all-files` | 0 — `ok — 2 tasks parsed` (`--all-files`: 0 — `6 records`) |
| `scale-ordering` | 461 records, 50 sibling groups — ordering bugs at size | 0 — `ok — 400 tasks parsed` |

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
| `dangling-parent` | a parent that resolves to nothing; error containment | 1 |
| `wrong-key-order` | canonical key order violated | **0 — passes. See finding below** |
| `broken-dfs-order` | a subtree that is not a contiguous run of lines | 1 |
| `bad-utf8` | invalid encoding; the line-0 whole-file convention | 1 |
| `wrong-types` | 21 records, one violation each; the checker must not raise | 1 — 24 errors |
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

## What was found while building this

Three things worth carrying into the port. Each is recorded, not fixed — no Ruby
code was changed for this corpus.

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

Verified over the committed tree:

| Check | Result |
|---|---|
| email addresses | one: `sam.rivera@example.com` (invented; reserved domain) |
| URLs | two, both on the reserved `example.invalid` TLD |
| absolute home paths | one: `/home/someone-else/tasks/tasks.jsonl`, the deliberate wrong path in `journal-foreign-org` |
| `updated` device slugs | `fixture` only — no hostname leaked |
| `marcus` / `aerie` / `vorwaller`, case-insensitive | no matches |
| journal `index.json` org paths | templated to `{{ORG_PATH}}`, except the deliberate foreign path above |
| runner state (`.state/`, logs) | none present |
