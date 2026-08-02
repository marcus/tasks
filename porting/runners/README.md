# `porting/runners/` — the invocation protocol

A **runner** drives one implementation of `tasks`. It reads a scripted case
list, and for each case it copies a fixture, runs one invocation against the
copy under a pinned environment, and emits one observation conforming to
[`porting/specs/observations.schema.json`](../specs/observations.schema.json).

```text
porting/runners/
  README.md            this file — the contract, language-neutral
  cases/               case lists (phase1.jsonl is the Phase 1 gate's input)
  ruby/run             the Ruby oracle's runner
  ruby/probe           the Ruby oracle's probe (see § The probe)
  go/…                 the Go port's runner, later phase, same contract
```

This file is the specification. `ruby/run` is one implementation of it; the Go
runner must be implementable from this document alone, without reading Ruby.
Anything that cannot be stated here without naming a language does not belong in
the protocol.

Quick start:

```console
$ porting/runners/ruby/run porting/runners/cases/phase1.jsonl        # JSONL on stdout
$ porting/runners/ruby/run --out evidence/ruby porting/runners/cases/phase1.jsonl
$ porting/runners/ruby/run --dry-run porting/runners/cases/phase1.jsonl
```

Exit status: `0` every case observed and every runner invariant held; `1` a case
violated a runner invariant (see [Invariants](#invariants)); `2` usage or
configuration error. **A non-zero exit status from the implementation under test
is not a runner failure — it is the observation.**

---

## The case list

A case list is JSONL: one JSON object per line. Blank lines, and lines whose
first non-whitespace character is `#`, are skipped so a list can carry section
comments. Unknown keys are an error, not a warning — a typo'd key would
otherwise silently change nothing.

| Key | Required | Meaning |
|---|---|---|
| `case_id` | yes | Stable id, unique within the list, matching `[a-z0-9][a-z0-9._-]*`. It names the fixture copy directory and the emitted file, and it is the join key the comparator uses to pair a Ruby observation with a Go one. |
| `fixture` | yes | `"<class>/<name>"` from [`porting/fixtures/`](../fixtures/README.md); class is one of `valid`, `compat`, `malformed`, `adversarial`. |
| `argv` | yes | Array of strings, **after** the program name. Passed through verbatim; no shell is involved at any point. |
| `surface` | no | `"cli"` (default). `"http"` is reserved for the phase that ports the HTTP adapter. |
| `cwd` | no | Working directory, relative to the fixture copy root. Default `"."`. Must resolve inside the copy. |
| `env` | no | Object of extra or overriding environment variables. A `null` value means *unset this pin*. The runner's own path variables cannot be set here (below). |
| `stdin` | no | Text written to the invocation's standard input. |
| `stdin_base64` | no | Base64 of the exact stdin bytes. Mutually exclusive with `stdin`; use it for payloads that are not valid UTF-8. |
| `timeout_ms` | no | Per-case wall budget. Default 60000. |
| `install_journal` | no | Default `true`: install the fixture's `journal/` when it ships one. Set `false` to observe the same fixture with no history. |
| `copy_root_mode` | no | Octal string (`"0555"`, or `"555"`) applied to the **copy root directory itself** after the copy is complete. Absent means the mode `cp -a` produced is left alone. See [A failing write](#a-failing-write-and-why-the-mode-lives-on-the-case). |
| `notes` | no | Free text; copied into the observation's `notes`. Never compared. |

Example:

```json
{"case_id":"cli-capture-small-gtd","fixture":"valid/small-gtd","argv":["capture","port the runner","--json"],"notes":"mutation"}
```

Case ids are also manifest ids: a case that exercises a slice in
`porting/manifest.jsonl` should carry that slice's id, because the observation's
`case_id` is what ties evidence back to the slice.

---

## The copy protocol

Per case, in this order:

1. **Copy.** `cp -a <fixture>/store/. <copy>/` into a directory the runner owns
   and has just emptied. The trailing `/.` is load-bearing: several fixtures
   carry dotfiles that a `<dir>/*` glob drops. Modes are preserved; the fixture
   itself is never written to.
2. **Apply the fixture's `perms.json`** when it ships one — see
   [Two modes](#two-modes-and-why-they-are-not-one-key). It is a JSON object
   `{"chmod": {"<path relative to the copy root>": "<octal>"}}`; apply each entry
   to the copy, in sorted key order, and refuse the case if a named path escapes
   the copy or does not exist. Applied to **both** copies (the case copy and the
   throwaway copy of step 6), because a fixture that declares a mode declares it
   for the corpus and the two copies must start identical.
3. **Create the isolated roots** inside the copy: `<copy>/.config/tasks/` and
   `<copy>/.state/`. Both live *inside* the copy so that everything the
   implementation writes is inside the observed tree and every recorded path is
   relative to one root.
4. **Install the journal** when the fixture ships `journal/` and the case did
   not opt out. The journal cannot ship as literal bytes: it lives at
   `<copy>/.state/tasks/journal/<key>/` where
   `<key> = sha256(canonical(<copy>/tasks.jsonl))[0:16]` and `canonical` is
   absolute **and symlink-resolved** — `valid/symlinked-store` spells the store
   as a link, and a key computed from the unresolved name names a directory the
   implementation never writes to. Its `index.json` records that same canonical
   path. Copy `journal/blobs/` through unchanged; if
   `journal/index.json.template` exists, substitute `{{ORG_PATH}}` with the
   canonical store path; if instead a literal `journal/index.json` exists, copy
   it verbatim — that fixture's subject is a *wrong* org path, and templating it
   would delete the thing under test.
5. **Apply `copy_root_mode`** when the case declares one — after the copy and
   the journal install, before anything observes the tree, and to the case copy
   only (never to the throwaway copy of step 6, which exists to read a revision
   token and would fail the probe if it were unwritable). The runner restores a
   writable mode on the copy root after step 10, and again before re-emptying the
   directory on a later run, so a crashed run cannot leave files behind that
   would leak into the next run's `files.before`. Nothing below the root is ever
   chmod'd: every file mode in `files.after` is the implementation's own doing.
6. **Run the before-probe** — against a *second, throwaway copy*, never against
   the case copy. See [The probe](#the-probe). It runs before the tree is
   observed because it is also where `files[].role` comes from: see
   [Roles](#roles-come-from-the-implementation-not-from-a-name-table).
7. **Observe the pristine tree** (`files.before`) and compute `fixture.root_sha256`.
8. **Invoke** the implementation.
9. **Observe the resulting tree** (`files.after`) and compute `files.deltas`.
10. **Run the after-probe** against the case copy.
11. **Emit** the observation; delete the copies unless `--keep`.

The probe deliberately runs *after* the tree is observed in step 10 and *before*
it in step 6–7, and both orderings are load-bearing: taking a snapshot acquires
the store lock, so a probe against the case copy before the invocation would
create the lock sidecar the invocation is supposed to be observed creating.

### Roles come from the implementation, not from a name table

`files[].role` must be resolved from the paths the implementation reports it
resolved (probe § `paths`), not from a table of filenames. The difference is not
cosmetic: `valid/symlinked-store` puts the store's bytes in `tasks.real.jsonl`
and makes `tasks.jsonl` a link to it, so a name table records the file carrying
the store as `role: "other"` — which voids the schema's guarantee that "the store
and the archive were BOTH observed" and makes the mutation invariant below fail
on a *correct* run.

Both spellings get the role: the link is the store the user named, the target is
the store the bytes are in. `files.before` and `files.after` may therefore each
contain more than one `role: "store"` entry, which is why the store assertion is
"at least one", not "exactly one".

Two details a port has to get right:

- The probe reports paths from the **throwaway** copy, so only the *relative*
  store, archive, memory, and config paths transfer to the case copy. The
  journal directory does not — its name is a digest of the copy's own absolute
  path — so use the key the runner computed for the case copy in step 4.
- Canonical paths are symlink-resolved and the copy root may not be (on macOS
  `/tmp` is a link to `/private/tmp`). Compare against both spellings of the root
  before concluding a path is outside the tree.

Everything the implementation does not name is resolved by **shape**: the
journal layout (`<state>/tasks/journal/<key>/index.json`, `…/blobs/<digest>`)
and the `.lock` suffix are patterns, not names, and stay correct under any store
spelling.

`fixture.root_sha256` is defined exactly as: SHA-256 over the concatenation, in
ascending byte order of path, of `<relative-path> 0x00 <file-sha256> 0x0A` for
every regular file in the tree (a symlink contributes `-` in place of its
digest). It depends on nothing but path strings and file bytes, so two
implementations compute it identically.

**Nothing is filtered from the tree.** A leftover `.tasks.jsonl.<pid>.<tid>.tmp`
is a real finding (a crashed write); the lock sidecar is a real observable
effect, including for reads. See `porting/specs/determinism.md` § "Tempting but
not normalized".

### A failing write, and why the mode lives on the case

One product outcome cannot be reached by choosing a fixture: a **write that is
performed and then reverted**. The reason is structural. A store that is already
invalid is refused by the *preflight* check, which validates the same file set
the post-write check does, so post-write validation can never fire from fixture
content alone. The reachable route is a write that *fails*: an unwritable store
directory whose lock sidecar already exists lets the mutation start and makes
the atomic replace raise, and the implementation must then restore the previous
bytes and say so.

The mode has to come from the case rather than the fixture because **the runner
creates the copy root** — a fixture cannot carry the mode of a directory that
does not exist until the copy is made. Two other designs were considered:

1. **A fixture-level manifest the runner applies after copying.** Rejected *for
   this purpose*: the mode is a property of one invocation, not of the corpus,
   and the same fixture is used by cases that need it writable. (That manifest
   exists for a different purpose — see
   [Two modes](#two-modes-and-why-they-are-not-one-key).)
2. **A fault-injection hook in the implementation** — an env var that makes one
   write fail. Rejected for Phase 1: it is a test seam every port would have to
   reimplement identically before it could be compared, and a seam whose own
   fidelity nothing checks. `copy_root_mode` needs nothing from the
   implementation at all; it is the operating system doing what it does.

The playbook's step 7 still wants real fault injection ("crash points around
lock, write, flush, validation, rename, and journal append"). This is not that.
It buys exactly one crash point — the write — from outside the process.

Portability: `copy_root_mode` is a POSIX directory mode. A runner on a platform
where an unwritable directory does not prevent replacing a file inside it cannot
produce this outcome, and must report that rather than emit a passing case.

### Two modes, and why they are not one key

There are two ways a mode reaches a fixture copy, and they were kept separate
deliberately.

| | `copy_root_mode` (case key) | `perms.json` (fixture file) |
|---|---|---|
| Applies to | the copy **root directory** | **files inside** the copy |
| Owned by | one case | the fixture, for every case that uses it |
| Applied to the throwaway probe copy | no | yes |
| Exists because | the runner creates the root, so no fixture can carry its mode | git records no permission bit but the executable one, so no fixture can carry a 0600 in its content |
| Buys | a write that fails and is reverted | a restrictive starting mode that must survive an atomic replacement |

Folding `perms.json` into `copy_root_mode` was considered and rejected on three
grounds:

1. **Different owners.** `valid/restricted-mode-store` *is* a 0600 store — that
   is what the fixture is named for and what its README promises. Restating it
   on every case that uses the fixture makes the promise a per-case opt-in that
   a new case can silently forget, and a forgotten one does not fail: it
   produces a 0644 store and a green comparison of the wrong thing.
2. **Different scopes.** `copy_root_mode` is one mode on one directory the runner
   made; `perms.json` is a map over files the fixture shipped. Widening the case
   key to carry a path→mode map would let a case chmod any file in the corpus
   copy, which is fault injection through the back door and would make
   `files.before` a case-authored fiction rather than an observation of the
   fixture.
3. **Different lifecycles.** The root mode must *not* reach the throwaway probe
   copy (an unwritable root fails the probe instead of the invocation); the file
   modes *must*, so both copies start identical. One key cannot have both
   behaviors.

Portability: `perms.json` modes are POSIX file modes. A runner on a platform
without them must report the fixture as unsupported rather than observe it at a
mode nobody asked for.

### Never a live store

A runner must refuse to operate anywhere near the operator's real task files.
Before any case runs, ask the implementation where the live store is
(`tasks config --json`, a read, made with the operator's own environment) and
abort if the work directory contains or is contained by the directory of `org`,
`archive`, or `memory`. That answer is used for exactly one thing: staying away.

---

## The pinned environment

The invocation's environment is **constructed, not inherited**: the child gets
this map and nothing else (no `PATH` inheritance, no locale leakage, no
toolchain variables that could change behavior).

| Variable | Value | Why |
|---|---|---|
| `PATH` | `/usr/bin:/bin:/usr/sbin:/sbin` | Pinned so a shim on the operator's `PATH` cannot change behavior. |
| `TASKS_DIR` | the copy root | The store under test. |
| `TASKS_FILE`, `TASKS_ARCHIVE` | **unset** | A per-file override would redirect half a store. Recorded as `null`, so "unset" is proven rather than assumed. |
| `HOME` | the copy root | The fallback base for both XDG paths; pinned so an unset XDG variable cannot reach the operator's home. |
| `XDG_CONFIG_HOME` | `<copy>/.config` | **Not optional.** Without it, a `host_context.<hostname>` entry in the operator's real config silently adds a tag to every captured task and the fixture stops meaning what it says. |
| `XDG_STATE_HOME` | `<copy>/.state` | Puts the undo journal inside the observed tree. |
| `TZ` | `UTC` | |
| `TASKS_TIMEZONE` | `UTC` | Out-ranks `TZ` in the product's timezone precedence, and is the setting the fixture corpus recorded its outcomes under. Pinning the highest-precedence source and the fallback to the same value leaves nothing ambiguous. |
| `LANG`, `LC_ALL` | `en_US.UTF-8` | |
| `TASKS_DEVICE` | `fixture` | The device half of an update stamp. |
| `TASKS_PIN_NOW` | `2026-03-14T15:09:26Z` | Every clock read. |
| `TASKS_PIN_IDS` | `bbbb0001` | Minted ids; the sequence continues by incrementing, so one token is enough for a mutation that mints several. |
| `TASKS_PIN_COALESCE_SCOPE` | `pinned-scope` | Persisted into journal bytes; one per process. |
| `TASKS_PIN_DELEGATION_KEYS` | `cccc000000000001` | The per-operation coalescing **key** every delegation verb mints. Also persisted into journal bytes, but one per operation rather than one per process, so it is a sequence. Without it two identical `delegate` runs agree on store bytes and disagree on `index.json` — see `porting/specs/determinism.md`. |
| `TASKS_PIN_HOSTNAME` | `fixture-host` | Host-context selection **and** the device half of the update stamp when `TASKS_DEVICE` is unset. Both consumers, one pin. |
| `LINES`, `COLUMNS` | `40`, `100` | Terminal geometry (no effect on the CLI; pinned for the TUI surface). |

### Every one of these is mandatory

The table above is the authoritative pin set for the protocol — set all of it,
not a subset. Two of the rows are easy to skip and fatal to skip:

- **`TASKS_PIN_NOW`.** `capture` writes `Captured [YYYY-MM-DD]` into the store
  *body*, and every mutation writes an `updated` stamp. Without the clock pin
  those are today's date and this second: two runs of one case produce different
  store bytes, and a case list captured last week stops reproducing. Those bytes
  are user-visible, so they are pinned and never normalized.
- **`TASKS_PIN_IDS`.** Minted ids are otherwise random, so every mutation
  produces a different store.

Note for anyone cross-reading the corpus: `porting/fixtures/README.md` records
its outcomes under `TASKS_TIMEZONE=UTC TASKS_DEVICE=fixture` only. That predates
`TASKS_PIN_NOW` / `TASKS_PIN_IDS` (added one commit later) and is not a
sufficient pin set — a `capture` run under it is not reproducible. The corpus
README needs that correction; this file, not that one, is the protocol.

A case's `env` may override any pin, or unset one with `null`. It may **not**
set `PATH`, `TASKS_DIR`, `TASKS_FILE`, `TASKS_ARCHIVE`, `TASKS_MEMORY`, `HOME`,
`XDG_CONFIG_HOME` or `XDG_STATE_HOME`: those are what isolation is made of.
`TASKS_MEMORY` belongs on that list for the strongest reason of all — it names a
*file path*, so a case that set it could point the memory sidecar outside the
copy, outside the observed tree, and potentially at something real.

`invocation.env` records the **union** of these names and the names actually
handed to the process, sorted, including the ones that are unset (as `null`).
The union half is not decoration. The product reads variables outside this table
— `TASKS_DATE_ORDER` changes date parsing, `TASKS_WORKER_ID` is written into the
store, `TASKS_URGENT_DAYS` changes agenda classification — and with a fixed
allowlist a case could set one and produce two observations whose entire
`invocation` block is byte-identical and whose store bytes disagree, with the
input that caused it recorded nowhere. It is still not a dump of the process
environment: a dump would be unstable, would leak secrets, and would make every
observation differ on irrelevant grounds.

### The umask is pinned too

Set the process umask to **0022** before spawning anything. It is inherited
across `fork`/`exec`, so setting it once in the runner pins it for every child.

It is not a host fact, and filing it as one was a defect: it is a per-process
attribute one syscall away, and it moves `mode` on every file the implementation
creates — a **compared** field. Left unpinned, the committed baseline silently
encodes the capture operator's umask, anyone re-capturing on a different CI image
gets four spurious `mode` mismatches per journal-bearing case, and — worse,
symmetrically — a genuine regression that widens the journal index to 0644 is
indistinguishable from a different image. Keep recording `environment.umask`:
after pinning it is the proof the pin took, not an excuse for a mismatch.

Standard input is always attached — to the case payload, or to an empty file
when the case supplies none. A runner never lets the implementation inherit a
terminal. `invocation.stdin.provided` distinguishes "the case supplied a
payload" from "the runner attached an empty one".

---

## The same-absolute-path requirement

**Both implementations must run against copies at the same absolute path.**

The journal's directory name is a digest of the store's canonical absolute path,
and the journal's `index.json` records that path *inside bytes the harness
digests*. `porting/specs/determinism.md` deliberately refuses to normalize that
value: rewriting bytes before digesting them is exactly the move that makes a
byte comparison stop meaning anything. The cause is removed instead — run the
two sides sequentially against the same path, or give each side a mount that
resolves to the same path.

Concretely: both runners take `--work DIR` (default `/tmp/tasks-conformance`)
and place each case at `<work>/<case_id>`. Run Ruby, collect its observations,
then run Go with the same `--work`. A cross-path comparison is still useful, but
it must exclude `journal.index` and say so in its report; a silent exclusion is
a defect.

The same requirement is what makes *repeated* runs byte-identical, which is the
runner's own acceptance criterion — a per-run `mktemp` directory would change
the journal key and the index bytes on every run.

---

## The probe

Some of an observation cannot be read off the invocation's output. The protocol
answers that with a **probe**: a small program each implementation ships next to
its runner, which prints one JSON object describing what that implementation
knows about a store on disk and about its own pins.

```console
$ porting/runners/<impl>/probe <copy-root>     # run under the same pinned env
{"probe_version":1,"revisions":{…},"pins":[…],"environment":{…}}
```

```jsonc
{
  "probe_version": 1,
  "revisions": {
    "status": "ok",              // ok | unsupported_schema | store_invalid | unavailable | probe_error
    "store": "s1.<sha256>",      // null when the implementation reports none
    "resources": [               // sorted by (id, kind)
      { "id": "1a2b3c02", "kind": "task", "revision": "v1.<own>.<location>.<lifecycle>" }
    ]
  },
  "pins": [                      // sorted by name; the schema's invocation.pins verbatim
    { "name": "TASKS_PIN_NOW", "applied": true, "value": "2026-03-14T15:09:26Z" }
  ],
  "environment": { "tzdb_version": "…", "platform": "…", "locale": "…", "runtime": "…" }
}
```

The probe is read-only with respect to store bytes, but it is **not side-effect
free**: taking a snapshot acquires the store lock, which creates
`.tasks.jsonl.lock` when absent. Hence the ordering rules above — the
after-probe runs only after `files.after` has been captured, and the before-probe
runs against a separate throwaway copy so a harness-created lock file can never
mask the lock creation the port is supposed to reproduce.

### Why revision tokens come from a probe

`revisions.store` / `revisions.resources` are one of the five seeded-mismatch
classes the Phase 1 gate must detect, so they have to be *populated* — and no
CLI command prints them (they are user-visible only over HTTP, which Phase 1 does
not port). Three options were considered:

1. **Parse them out of `--json` output.** Rejected: the CLI's JSON rows do not
   carry a revision, on any command.
2. **Compute them in the harness from the store bytes.** Rejected, and this is
   the important one: the tokens are a pure function of the bytes, so the
   harness *could* derive them — and then a port that computed them differently
   would still produce matching observations, because both sides' tokens would
   have come from the harness. A harness that cannot fail is not a harness.
3. **Ask the implementation.** Adopted. Each side reports the tokens *its own*
   code computes, so a divergence in the revision algorithm shows up as an
   observation mismatch, which is the whole point.

The probe is per-implementation by construction (the Go probe links the Go
store package). The *protocol* stays language-neutral because the object it
prints is fixed here, and because the observation records only that object's
contents — never how it was obtained.

### Why `pins` comes from a probe

The failure a pinned harness cannot detect on its own is a pin that was set and
silently ignored: the run looks reproducible and is not. Only the implementation
knows what it actually resolved, so `applied` and the resolved `value` are the
implementation's self-report. `applied: false` alongside a non-null request is a
**hard failure**, not a warning — the runner fails the case.

Note that `value` is the *resolved* value, not the string that was handed in:
`TZ=UTC` reports the zone the implementation ended up with, so a silent fallback
reports `applied: false`. Comparing resolved values is what catches two
implementations parsing one pin differently.

---

## What the runner fills in

| Observation field | Source |
|---|---|
| `observation_id` | Harness-assigned UUID. Never compared. `--pin-identity` fixes it to `obs_<case_id>`. |
| `implementation.version` | Build identity of the implementation (git sha, `-dirty` when the tree is not clean). |
| `fixture.*` | The case's fixture, the pristine tree digest, the copy root. |
| `invocation.*` | The case, plus the pinned environment as constructed; `pins` from the after-probe. |
| `process.*` | Exit status, signal, timeout flag, and the exact stdout/stderr bytes (base64; `text` only as a convenience decode when the bytes are valid UTF-8). |
| `files.before` / `after` | Every file in the copy, sorted by relative path, with digest, size, mode, symlink target, whole content when ≤ 64 KiB, and line count for the store and archive. |
| `files.deltas` | Paths whose presence or content changed. Empty is a meaningful assertion, not an omission. |
| `files.mutated` | **Not** derived from `deltas`: it is true when the implementation's own store revision changed across the invocation (before-probe vs after-probe). The two are then cross-checked — see [Invariants](#invariants). |
| `files.rolled_back` | The implementation's own `--json` error envelope (`{"error":…,"rolled_back":true\|false}`) read from stdout — see `porting/specs/errors.md` § "The `--json` error envelope". Never inferred from stderr wording, and never from the deltas: a write-then-revert and a never-wrote leave identical bytes, which is why the field exists. `null` when the invocation reported nothing. |
| `journal.*` | The index parsed structurally (`version`, `cursor`, `states[]` with labels, restored digests, coalesce key/scope, repair flag) *and* the index file's bytes; blob count and sorted blob digests. When no journal exists, `present:false` with the path where one *would* have been. |
| `revisions.store` / `resources` | The after-probe. |
| `revisions.touched_ids` | The implementation's own `--json` mutation payload (`{"touched":[{"id":…}]}`), when the case asked for one. Read from stdout, never inferred from the store diff. |
| `environment.*` | `tzdb_version`, `platform`, `locale`, `runtime` from the probe; `filesystem` from the runner's host, and `umask` as the value the runner pinned. Recorded, never compared — but `umask` is now a tautology by design, and one that reads anything but `0022` means the pin did not take. |
| `metrics.wall_ms` | Harness-measured. Advisory; never part of conformance equality. `--pin-identity` fixes it to 0. |
| `metrics.bytes_written` | Total size of the changed files. Deterministic, so it stays in the byte-identical output. |

### Known gaps

- **`files.rolled_back` is null for every invocation that made no rollback
  report.** That is most of them: reads, successes, and any refusal the caller
  did not ask for `--json` on. Null means "not reported" and never "did not roll
  back", and a null on both sides is not a mismatch — but it is the absence of
  the label, so the comparator counts it and says so.
- **`metrics.user_cpu_ms` / `sys_cpu_ms` / `peak_rss_bytes` are `null`.** Not
  portably available for a child process; they are advisory-only fields and
  emitting host-noisy values would break byte-identity for no gain.
- **`http` is always empty and `surface` is always `cli`.** The HTTP adapter is
  not ported in Phase 1.

---

## Invariants

Checked by the runner after each case; a violation is reported on stderr and
makes the run exit `1`, while the observation is still emitted as evidence.

1. **No requested pin was dropped.** For every pin the runner set, the probe
   must report `applied: true`.
2. **`files.mutated` agrees with the store/archive deltas.** The two are measured
   independently — one from the implementation's revision token, one from the
   harness's own digests — precisely so that disagreement is detectable. It
   catches both directions of harness error: a write the harness failed to
   notice, and a "no change" claim it never verified.

---

## Determinism, and what varies between runs

Two runs of the same case list produce byte-identical observations except for
two fields, both produced by the harness rather than by the implementation:

- `observation_id` — a fresh UUID per record, by design (it is provenance).
- `metrics.wall_ms` — wall time.

`--pin-identity` fixes both (`obs_<case_id>` and `0`), which makes a byte
comparison a plain `diff -r`. Nothing else is normalized: over-normalization is
the failure mode `porting/specs/determinism.md` exists to prevent.

```console
$ porting/runners/ruby/run --out /tmp/a --pin-identity porting/runners/cases/phase1.jsonl
$ porting/runners/ruby/run --out /tmp/b --pin-identity porting/runners/cases/phase1.jsonl
$ diff -r /tmp/a /tmp/b && echo identical
```

---

## Options

```text
--work DIR        parent directory for fixture copies (default /tmp/tasks-conformance).
                  Both implementations must use the same value — see § The
                  same-absolute-path requirement.
--out DIR         write <case_id>.json per case (pretty-printed) instead of
                  streaming compact JSONL on stdout
--case ID         run only this case (repeatable)
--pin-identity    fix observation_id and metrics.wall_ms so runs are byte-identical
--timeout MS      default per-case wall budget
--keep            keep the fixture copies after the run (for post-mortem)
--quiet           no per-case progress on stderr
--dry-run         print the resolved plan as JSON and exit
```

Every runner in this directory must accept these options with these meanings; a
harness script drives them interchangeably.
