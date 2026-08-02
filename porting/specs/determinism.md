# determinism.md — pins, and the very short list of normalizations

Two pinned runs of the same command against copies of one fixture must produce
byte-identical stores, journals, and observations. This file is the complete
account of how that is achieved:

- **[Pins](#pins)** — inputs the harness fixes so the implementation produces
  the same bytes. Pinning is the preferred tool. It removes a difference by
  removing its *cause*.
- **[Normalizations](#normalizations)** — fields the comparator rewrites before
  comparing. Normalizing removes a difference by *hiding* it, so every entry
  carries the reason a user cannot observe the thing being hidden.
- **[Tempting but not normalized](#tempting-but-not-normalized)** — things that
  differ, that would be convenient to normalize, and that a user *can* observe.
  They stay compared.

The danger this file exists to guard against: over-normalization turns a
conformance harness into a difference-hiding machine. If you cannot write the
sentence "a user cannot observe this because …" without hedging, the answer is a
pin or a failing test, not a normalization.

## Where the seams live

All pins are read in exactly one place — `lib/tasks/determinism.rb`
(`Tasks::Determinism`) — and applied at the adapter boundary (`bin/tasks`,
`lib/tasks/config.rb`, the TUI's terminal-size query). The domain (`Store`,
`Journal`, `Application`, `TemporalContext`) takes plain injected values and
never reads the environment. Every pin resolves to `nil` when unset, and every
call site falls back to the constructor default it had before, so **unpinned
behavior is unchanged**.

`Tasks::Determinism` is not a configuration system and must not grow into one.
It can only fix values that are otherwise nondeterministic. It cannot select
files, change formatting, or alter semantics. A proposed pin that would do any of
those belongs in `Tasks::Config` or nowhere.

## Pins

### Pins added for the port harness

| Pin | What it fixes | How to set it | Unset default |
|---|---|---|---|
| `TASKS_PIN_NOW` | Every clock read: update stamps (`updated`), `Captured [date]` bodies, the temporal context used for relative-date parsing, and "today" for availability, agendas, quadrants, and recurrence projection. | ISO8601 instant with an offset, e.g. `2026-03-14T15:09:26Z`. Malformed values raise rather than falling back — a silent fallback to the wall clock produces a green run that proves nothing. | The real clock (`Time.now.utc`), and `Date.today` for the calendar day. |
| `TASKS_PIN_IDS` | Minted eight-hex task/section ids. | Comma-separated tokens; each is eight hex characters or the literal `seq` (= `00000000`). Ids are handed out in order; after the last token the sequence continues by incrementing it as a 32-bit counter, so a mutation that mints more ids than were listed stays deterministic instead of failing halfway. Example: `TASKS_PIN_IDS=bbbb0001,bbbb0002`. | `SecureRandom.hex(4)`. |
| `TASKS_PIN_COALESCE_SCOPE` | The journal's coalescing scope token, which is **persisted into `index.json`** and therefore appears in journal bytes. | Any non-empty string. | `SecureRandom.hex(16)`, fresh per process/adapter lifetime. |
| `TASKS_PIN_DELEGATION_KEYS` | The per-operation coalescing **key** minted for each delegation verb (`delegate`, `undelegate`, `claim`, `release`). Like the scope it is **persisted into `index.json`**; unlike the scope, one is minted per operation rather than per process, so it takes a sequence rather than a single value. | Comma-separated tokens; each is sixteen hex characters or the literal `seq` (= sixteen zeros). Same continuation rule as `TASKS_PIN_IDS`. | `SecureRandom.hex(8)`. |
| `TASKS_PIN_HOSTNAME` | The hostname used **by both hostname consumers**: the `host_context.<hostname>` selection in `Tasks::Config`, and the device half of an update stamp when `TASKS_DEVICE` is unset. One pin, both call sites — a pin that reached only the first would report `applied: true` while machine-specific bytes went into the store. | A hostname string. | `Socket.gethostname`. |
| `LINES` / `COLUMNS` | Terminal geometry for the full-screen TUI. Both must be set; either alone is ignored. Has no effect on the CLI, whose output does not depend on terminal width. | Positive integers. | The tty's own `winsize`, then `24`×`80`. |
| `TASKS_TEST_TODAY_SEQUENCE` | A **test-only** second clock seam: when set, the CLI's "now" becomes noon UTC of `Date.today` rather than the wall clock, so `test/support/sequenced_today.rb` can script a walk across day boundaries inside one process. `TASKS_PIN_NOW` **dominates** it. | A comma-separated list of ISO dates, consumed by the test support file; the *product* only branches on the variable being present. Presence, not non-emptiness: an empty string takes the branch. | The wall clock. |

`TASKS_TEST_TODAY_SEQUENCE` was for a long time read in `bin/tasks` instead of
in `Tasks::Determinism`, which made the sentence at the top of this section
false. It was harmless — `TASKS_PIN_NOW` out-ranks it, and every pinned run sets
that — and it was moved anyway, unchanged in precedence, because the value of
"all pins are read in exactly one place" is that a port can read *one file* to
find the clock inputs. An input the doc places there and the code does not is
worse than an undocumented one.

### Pre-existing settings the harness also pins

These are not new. They are product settings that happen to control otherwise
host-dependent values, and the harness sets them rather than inventing parallel
names.

| Setting | What it fixes | Unset default |
|---|---|---|
| `TASKS_DEVICE` | The device half of an update stamp (`…Z#device`). | A slug derived from `Socket.gethostname`. |
| `TZ` | The resolved IANA time zone, hence every local-time rendering and every date boundary. | `/etc/localtime` if it is a zoneinfo symlink, else `Etc/UTC` with a fallback warning. |
| `TASKS_DIR` / `TASKS_FILE` / `TASKS_ARCHIVE` | Which files the invocation operates on — pointed at the fixture copy. | The repo root. |
| `XDG_CONFIG_HOME` | Where `tasks/config` is read from — pointed at a fixture-controlled directory so the operator's real config cannot leak into a run. **Not optional.** Without it, a `host_context` entry in the operator's config silently adds a tag to every captured task, and the fixture stops meaning what it says. | `$HOME/.config`. |
| `XDG_STATE_HOME` | Where the undo journal lives — pointed inside the fixture copy so the journal is part of the observed tree. | `$HOME/.local/state`. |
| `HOME` | The fallback base for both XDG paths above; pinned so an unset XDG variable cannot reach the operator's home. | The real home directory. |
| `LANG` / `LC_ALL` | Process locale and default external encoding. Store writes are explicitly UTF-8 regardless (`Tasks::Atomic`), so this pins the *reading* and diagnostic side. | The host locale. |
| `TASKS_TIMEZONE` | The same thing `TZ` fixes, and it **out-ranks** `TZ`: `Config#pick_timezone` reads `TASKS_TIMEZONE` first and only then falls through to `TZ` detection. Pinned to the same value as `TZ` so the highest-precedence source and the fallback agree and nothing is ambiguous. | Unset; `TZ` detection decides. |
| `PATH` | Which interpreter and which helper binaries the invocation can reach. Pinned to a fixed value rather than inherited so a shim on the operator's `PATH` cannot change behavior. | The operator's `PATH`. |
| `NO_COLOR` / `TASKS_THEME` / `COLORTERM` / `TERM` | Colour. See [Colour](#colour) below — these four are the complete set the implementation reads, they are pinned to **unset**, and pinning them is not the same as covering them. | Various; see below. |

The mechanism that makes this list exhaustive rather than aspirational is
`unsetenv_others: true` on the spawn: the child receives the pinned map and
nothing else, so a variable the operator happens to export cannot reach the
implementation. The observation records the **union** of a documented floor and
the names actually passed, so a variable a case sets is visible in
`invocation.env` rather than silent.

### Pinned by the harness process, not by the environment

| Value | How it is pinned | Why it is not a host fact |
|---|---|---|
| **umask** | `File.umask(0o022)` in the runner before any child is spawned; inherited across `fork`/`exec`. | It is a per-process attribute one syscall away, not a property of the machine — and it moves `mode` on every file the implementation creates, which is a **compared** field (see [Tempting but not normalized](#tempting-but-not-normalized)). Left unpinned it would bake the capture operator's umask into the baseline, and a genuine regression that widens the journal index to 0644 would be indistinguishable from a different CI image. `environment.umask` is still recorded — as the proof the pin took, not as an excuse for a mismatch. |
| **terminal-ness** (`$stdout.tty?`) | The runner redirects stdin, stdout and stderr to files before `exec`, so no descriptor the implementation receives is a terminal. Recorded in `invocation.tty`, which is compared. | Like umask, it is a per-process attribute the harness chooses rather than a host fact — and it is the CLI's colour switch, so it changes stdout bytes. See [Colour](#colour). |

### Colour

Colour used to be the one input on this page that was not on this page: unpinned,
unrecorded and unexercised all at once, so a green run said nothing about it in
either direction. It is now pinned and recorded. It is still not covered, and
those are different things.

**The inputs, read off the source rather than assumed.** Four environment names,
and no others:

| Name | Where it is read | What it does |
|---|---|---|
| `NO_COLOR` | `Tasks::Config#pick_theme`, `Tui::Border.truecolor?` | Selects the attribute-only `mono` theme when nothing more explicit is set; disables truecolor gradients. |
| `TASKS_THEME` | `Tasks::Config#pick_theme` | Names a theme, and **out-ranks** `NO_COLOR`. |
| `COLORTERM` | `Tui::Border.truecolor?` | Enables 24-bit border gradients. |
| `TERM` | `Tui::Border.truecolor?`, `Tui::App#mouse_enabled?` | Empty or `dumb` disables truecolor and mouse tracking. |

`CLICOLOR` and `CLICOLOR_FORCE` are the two names a reader expects here and they
are deliberately absent: they appear nowhere in `bin/` or `lib/`. Pinning a
variable the product does not read would be a false assurance in the one
document that claims to be exhaustive.

**How they are pinned: to unset.** All four are absent from the constructed
environment, and `unsetenv_others: true` is what turns that into a pin rather
than a hope — the child receives the pinned map and nothing else, so an operator
with `TERM` exported cannot reach the implementation. They are pinned to *unset*
rather than to values on purpose: `NO_COLOR=1` resolves the theme to `mono` and
changes what `tasks config --json` prints, so pinning them to values would mean
the harness altering the behavior it exists to observe in order to tidy itself
up. All four are in the recorded floor, so every observation carries them
explicitly as `null` and "colour was configured by nothing" is proven per record
instead of asserted here. A case may still set any of them; the union rule makes
such a case visible in `invocation.env`, and the probe reports the theme the
implementation actually resolved.

**The limit that remains, stated plainly.** The CLI's colour switch is not any of
those four. It is `$stdout.tty?` (`bin/tasks`, `def color(str, code)`, used at
eight call sites, plus `color: $stdout.tty?` passed into the agent diff). The
harness redirects every stream to a file, so it is false in every case, in every
observation, on both sides. Therefore:

- **The colour rendering path is unexercised.** Zero observations in the corpus
  contain an ANSI escape, and none can while the protocol attaches files.
- **A green conformance run is not evidence about it.** A port that emitted no
  colour at all, or the wrong codes, or colour unconditionally on a tty, passes
  every case in the corpus identically.
- **Pinning it did not fix that, and was not meant to.** What pinning bought is
  that the input is now deterministic and *legible*: `invocation.tty` records
  what was attached, so the gap is visible in the evidence rather than inferable
  only by reading this file. Recording an input is not covering it, and this
  document must not let the two blur — the whole point of writing the limit down
  is that "we pinned colour" would otherwise read as "colour is handled".

Closing it needs a protocol change, not a pin: a case would have to attach a
pseudo-terminal to stdout and record `invocation.tty.stdout: true`. That is a
real option and a bounded one; it is simply not done, and until it is, colour is
out of the corpus's reach and this section is the record of that.

### Not pinnable — recorded instead

| Value | Why it cannot be pinned | Where it is recorded |
|---|---|---|
| IANA time-zone database version | It is a property of the installed tzdata/TZInfo, not of the process. Two implementations resolving the same zone against different tzdb releases can legitimately disagree about a historical offset. | `environment.tzdb_version` in the observation; also printed by `tasks config` / `tasks config --json` and returned by the HTTP config endpoint. Recorded, never compared for equality — but a comparison whose two sides disagree here is re-run before any difference is classified. |
| Platform, filesystem | Host facts. Locking, atomic replacement, and signal numbers are platform-shaped. | `environment.platform`, `environment.filesystem`. |

umask used to be filed here. It is not a host fact and it is now pinned — see
[Pinned by the harness process](#pinned-by-the-harness-process-not-by-the-environment).

### Not pinned, because nothing observable depends on it

- **Operation id** (`cli_<hex>` / `tui_<hex>` / the HTTP request id). It is
  carried on `Tasks::OperationContext` for future audit seams and is not written
  to the store, the journal, or any current output. If it ever becomes
  observable, it needs a pin — not a normalization.
- **Atomic-write temp filenames** (`.tasks.jsonl.<pid>.<tid>.tmp`). They exist
  only between `write` and `rename` and are gone before an observation is taken.
  A leftover temp file in an observation is a real finding (a crashed write), so
  the harness must *not* filter them out.

## Normalizations

Four, and they are the whole list. Every one is applied by the comparator to both
sides symmetrically.

### 1. `observation_id`

A UUID the harness assigns per record. It is not produced by the implementation
and names nothing a user can reach. Recorded for evidence provenance; replaced
with a constant before comparison.

### 2. `fixture.copy_root`, and the copy-root prefix inside path-bearing values

Each run operates on its own copy, so the copy's absolute path differs by
construction. The comparator rewrites that prefix to a fixed token in
`fixture.copy_root`, in the pinned path environment variables, and in captured
stdout/stderr. Everything *inside* the copy stays compared: the path relative to
the copy root, the file set, and the bytes.

A user cannot observe this because the path is chosen by the harness, not by the
implementation. Naming the wrong file *within* the copy is still a failure.

One wrinkle, and the reason `stream.sha256_normalized` exists. A stream past the
256 KiB embed limit survives in the observation only as a digest, and
`stream.sha256` digests **raw** bytes — so a large diagnostic that names the copy
root could never compare equal across two copy roots, whatever the comparator
did afterwards. Truncation and cross-path comparison were therefore mutually
exclusive, masked only by the same-absolute-path requirement below. The fix is
to apply this same rewrite **before** digesting, at capture time, where the bytes
still exist: `sha256_normalized` is the digest of the normalized stream, and it
is what a truncated stream is compared on. It must be the *same* rewrite — the
runner requires the comparator's implementation rather than carrying one of its
own, because two rewrites that drift silently reclassify a real difference as a
copy-root artifact, or the reverse. `sha256` is unchanged and still recorded.

This is applied to streams and to nothing else. File contents keep the refusal
stated below: bytes are never rewritten before being digested.

### 3. The journal directory name

The journal lives at `…/tasks/journal/<key>/`, where `<key>` is the first 16 hex
of a SHA-256 of the store's canonical absolute path. Two copies at two paths get
two keys. The comparator replaces `<key>` with a constant in observation paths.

A user cannot observe this because it is a private cache key under
`XDG_STATE_HOME`: no command prints it, no configuration names it, and its only
job is to keep two different task files from sharing one history. That property —
*different stores get different keys, the same store always gets the same key* —
is itself testable and is not what is being normalized.

### 4. `metrics.*`

Wall time, CPU time, RSS. Advisory only; never part of conformance equality in
either direction. Performance is a separate gate with its own budgets.

## Tempting but not normalized

Written down because each one cost real thought, and because the next person
will be tempted again.

- **The `org` field inside the journal's `index.json`.** It records the store's
  canonical absolute path, so two copies at two paths produce two different
  index files — and unlike the directory key, this value is *inside* bytes the
  harness digests. It would be easy to rewrite the copy root inside those bytes
  before hashing. It is not normalized, because a user can read the file, because
  the field is a real guard (a journal whose `org` does not match refuses to
  apply), and because rewriting bytes before digesting them is exactly the move
  that makes a byte comparison stop meaning anything. Instead, **the harness runs
  both sides against copies at the same absolute path** (sequentially, or via a
  per-side mount that resolves to the same path), which removes the cause rather
  than hiding the effect. Cross-path runs are still useful; they are compared
  with the journal index excluded and that exclusion is reported, not silent.

  Two things a cross-path run must know, so `--cross-path` is not advertised as
  more finished than it is. Streams are handled: `sha256_normalized` removed the
  truncated-stream half of the problem, which was the half that could never be
  fixed downstream. **`fixture.root_sha256` is not.** A journal-bearing
  fixture's installed journal directory is named for a digest of the copy's own
  absolute path, so the whole-tree digest moves with the copy root — and it is
  compared as a precondition, ahead of everything else. Fixing it would mean
  either normalizing the journal key inside a digest input, which is the move
  this very section refuses, or excluding `root_sha256` and reporting the
  exclusion the way `journal.index` already is. Neither is done here. A
  cross-path comparison of a journal-bearing case still reports a harness error,
  and that is the honest state rather than a silent pass.

- **The lock sidecar `.tasks.jsonl.lock`.** Every locked operation creates it if
  absent — *including reads*. So a read against a pristine copy is not
  delta-free: it produces `{"path": ".tasks.jsonl.lock", "kind": "created"}`. It
  would be tidy to filter the lock file out of `files.deltas` so "a read changes
  nothing" holds universally. It is not filtered: the file is visible in the
  user's task directory, it is committed to nobody's expectations by accident,
  and whether the port creates it, when, and with what mode is precisely the kind
  of platform-shaped behavior the port is most likely to get wrong. The schema
  handles this by making `files.mutated` a separate assertion from `deltas`
  being empty — a read has `mutated: false` and may still have a delta.

- **File mode bits.** They depend on umask and on whether the target already
  existed. Tempting to drop. Kept, because carrying the existing file's
  permission bits across an atomic replacement is a documented safety property —
  a chmod-600 store must not widen to 644 — and dropping mode from the
  comparison is exactly how that regression would ship. The umask half of the
  dependency is removed by pinning it rather than by tolerating it; the
  "already existed with a restrictive mode" half is what the corpus tests, via
  a fixture's `perms.json` (git records no permission bit but the executable
  one, so the mode has to be applied to the copy). `environment.umask` is still
  recorded, now as the proof the pin took.

- **Timestamps in `Captured [YYYY-MM-DD]` bodies.** Tempting to treat as
  cosmetic. They are store bytes and a user reads them. Pinned via
  `TASKS_PIN_NOW`, never normalized.

- **stderr wording.** See [`errors.md`](errors.md): compared byte for byte, with
  only the copy-root rewrite applied.

## Verifying a pin actually took effect

The failure mode a pinned harness cannot detect on its own is a pin that was
*set and ignored* — the run looks reproducible and is not. Two defences:

1. `invocation.pins[]` in an observation records the value the implementation
   **actually resolved**, alongside `applied`. `applied: false` with a non-null
   requested value is a hard failure, not a warning. Note both halves of that
   sentence: `applied: false` alone is not a failure, because some inputs are
   deliberately pinned to *unset* (the colour names above, the test-only clock
   seam) and honestly report themselves unapplied on every case. Nor is being
   legitimately out-ranked — `TASKS_DEVICE` over the hostname pin, `TASKS_THEME`
   over `NO_COLOR`, `TASKS_PIN_NOW` over `TASKS_TEST_TODAY_SEQUENCE` — because
   reporting a correct override as a dropped pin fails a correct case, which is
   how an invariant stops being believed. What the comparator adds on top is
   that the two sides must *agree* about `applied`.
2. `Tasks::Determinism.report` returns the same data from inside the process, so
   a runner can assert it without parsing output.
3. **Interception.** The first two ask the process what it thinks it did, and
   both compute `applied` from whether `Tasks::Determinism` *resolved* a value —
   not from whether every call site *used* it. That is a real gap, not a
   theoretical one: `Application` has some thirty methods with a
   `today: Date.today` default parameter, and one adapter call site that forgets
   to pass `today:` produces wall-clock output with `applied: true` recorded
   beside it. Two shipped defects had exactly that shape (an unpinned
   `SecureRandom.hex(8)` in journal bytes; `TASKS_PIN_HOSTNAME` not reaching the
   update stamp's device slug). So `test/test_porting_determinism_seams.rb`
   patches `Date.today`, `Time.now`, `Socket.gethostname`, and
   `SecureRandom.hex`/`uuid` **at the source** in a child process
   (`RUBYOPT=-r test/support/determinism_trap.rb`), runs the fully-pinned
   commands the corpus runs, and fails on any read from `bin/` or `lib/tasks/`.
   The only allowed unpinned mint is the operation id, matched by its enclosing
   method rather than by line number. That is what turns `applied` from a claim
   into a proof; adding a command to that test's `COMMANDS` list is the whole
   cost of covering a new code path.

   Pins are also proven end-to-end by the corpus: a case must exist that
   *exercises* each one. `TASKS_PIN_DELEGATION_KEYS` is the cautionary tale — the
   defect it fixes survived because no Phase-1 case ran a delegation, so a field
   that flapped on every re-run was permanently null in the baseline and looked
   stable.

## Changing this file

`porting/specs/determinism.md` and `lib/tasks/determinism.rb` are one artifact in
two files. A new pin, a renamed pin, or a changed default must land in both in the
same commit. A new **normalization** additionally needs the sentence "a user
cannot observe this because …" written out in full above — and if writing it
honestly is hard, that is the finding.
