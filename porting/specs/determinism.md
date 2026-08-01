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
| `TASKS_PIN_HOSTNAME` | The hostname `Tasks::Config` uses to select a `host_context.<hostname>` entry, which decides whether a captured task gets an implicit context tag. | A hostname string. | `Socket.gethostname`. |
| `LINES` / `COLUMNS` | Terminal geometry for the full-screen TUI. Both must be set; either alone is ignored. Has no effect on the CLI, whose output does not depend on terminal width. | Positive integers. | The tty's own `winsize`, then `24`×`80`. |

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

### Not pinnable — recorded instead

| Value | Why it cannot be pinned | Where it is recorded |
|---|---|---|
| IANA time-zone database version | It is a property of the installed tzdata/TZInfo, not of the process. Two implementations resolving the same zone against different tzdb releases can legitimately disagree about a historical offset. | `environment.tzdb_version` in the observation; also printed by `tasks config` / `tasks config --json` and returned by the HTTP config endpoint. Recorded, never compared for equality — but a comparison whose two sides disagree here is re-run before any difference is classified. |
| Platform, filesystem, umask | Host facts. Locking, atomic replacement, permission bits, and signal numbers are platform-shaped. | `environment.platform`, `environment.filesystem`, `environment.umask`. |

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
  comparison is exactly how that regression would ship. `environment.umask` is
  recorded so a legitimate umask difference can be told from a defect.

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
   requested value is a hard failure, not a warning.
2. `Tasks::Determinism.report` returns the same data from inside the process, so
   a runner can assert it without parsing output.

## Changing this file

`porting/specs/determinism.md` and `lib/tasks/determinism.rb` are one artifact in
two files. A new pin, a renamed pin, or a changed default must land in both in the
same commit. A new **normalization** additionally needs the sentence "a user
cannot observe this because …" written out in full above — and if writing it
honestly is hard, that is the finding.
