# Independent adversarial review — td-3527b1

**Target:** "Observation schema and determinism seams in the Ruby CLI"
**Artifacts under review:** `porting/specs/observations.schema.json`,
`porting/specs/determinism.md`, `porting/specs/errors.md`,
`porting/specs/examples/`, and the determinism seams in `lib/tasks/determinism.rb`
/ `lib/tasks/update_stamp.rb` / `bin/tasks`.
**Reviewer:** independent session; wrote nothing outside `porting/evidence/`.
**Date:** 2026-08-01. Tree state: `c500866` + the in-flight rollback-gap work in
`bin/tasks` and `porting/runners/ruby/run` (excluded from findings by instruction).

Everything below was executed, not asserted. Reproduction commands are given
inline; scratch work lived under the session scratchpad and was not committed.

---

## Verdict

**td-3527b1 should not be closed.** Finding 1 falsifies the task's own stated
acceptance criterion ("Two pinned runs of the same mutation … produce
byte-identical stores, journals, and observations") with a reproducible two-run
diff. Findings 2–4 are three further unpinned or mis-scoped inputs that move
compared fields. Findings 5–7 are rigor gaps in the schema and validator that let
nonsense through.

The artifact is well above average — the `Tempting but not normalized` section of
`determinism.md` is genuinely excellent and survived attack intact, and I give
**over-normalization a clean bill** (see § Clean bills). The defects are all of
one shape: *the doc describes a pin, and the pin does not reach every consumer.*

---

## MUST FIX BEFORE THE PORT LOOP STARTS

### 1. [Critical] An unpinned `SecureRandom.hex(8)` is persisted into journal bytes

`lib/tasks/application.rb:567`

```ruby
coalesce_key = "delegation-#{command.action}-#{SecureRandom.hex(8)}"
```

`lib/tasks/journal.rb:125` persists `coalesce_key` into `index.json` whenever it
is non-nil. So **every successful `tasks delegate` / `release` invocation writes
16 characters of unpinned randomness into durable journal bytes.**

`TASKS_PIN_IDS` does not cover it (that pins task/section ids). `TASKS_PIN_COALESCE_SCOPE`
does not cover it (that pins the *scope*, not the *key*). `determinism.md` does
not mention it at all.

**Evidence** — two fully-pinned runs of the same command against two pristine
copies of `valid/small-gtd`:

```
$ tasks delegate 1a2b3c02 implement     # TASKS_PIN_NOW / _IDS / _COALESCE_SCOPE /
                                        # _HOSTNAME / TZ / TASKS_DEVICE all pinned
run1 store sha: 2bfd93b235e573a17a5d     run2 store sha: 2bfd93b235e573a17a5d   ← identical
run1 index sha: 18a13d4a2ac346c8f6aa     run2 index sha: d0b784f0bc94e9190f69   ← DIFFERENT
run1 coalesce_key: delegation-delegate-c7bf3cb87c92034a
run2 coalesce_key: delegation-delegate-eaa5e40a843bf278
```

**Divergence it lets through / creates:** it does not hide a difference — it
*manufactures* one. Three observation fields flap on every re-run:
`journal.index.sha256`, `journal.index.content_base64`, and
`journal.states[].coalesce_key`. The Ruby baseline cannot match itself, so the
first delegation case wired into the harness fails, and the natural diagnosis
("the Go port writes journal metadata differently") is wrong.

**Why it is invisible today:** zero phase-1 cases exercise delegation. This is
exactly the "accidentally stable, will start flapping later, will be blamed on
the port" shape.

**Recommended fix:** mint the key through the existing pinned id seam
(`Tasks::Determinism.id_source`), or derive it deterministically from
`(id, action, sequence)`. Adding the seam is in scope for this task by its own
description; the value is nondeterminism, not behavior. Then add a delegation
case so the pin is proven rather than assumed, and give it a row in
`determinism.md § Pins`.

---

### 2. [High] `invocation.env` records a constant list, not the environment the process was given

`porting/runners/ruby/run` builds the env as `DEFAULT_PINS` + case overrides, but
emits `"env" => RECORDED_ENV.map { … }` — a frozen constant. **Any environment
variable a case sets outside that constant is applied to the process and does not
appear anywhere in the observation.**

This directly contradicts the schema's own prose for the field:

> … it is recorded in full — including entries with a null value — so 'this
> variable was unset' is proven rather than inferred.

**Evidence** — the same `case_id`, the same `argv`, run twice; the second adds
`"env": {"TASKS_DATE_ORDER": "dmy"}`:

```
$ tasks capture "ambiguous date" --deadline 03/04/2026 --json
invocation blocks BYTE-IDENTICAL?  True
no env   store sha 8534affd2cce  …"deadline":"2026-03-04"…
dmy env  store sha a680ac9379e6  …"deadline":"2026-04-03"…
```

Two observations whose entire `invocation` block — argv, env array, pins, stdin,
cwd — is byte-for-byte identical, and whose store bytes disagree on a semantic
field. The input that caused it is nowhere in the record.

**The unrecordable set.** Variables the product reads that `RECORDED_ENV` cannot
carry (`grep -rn ENV bin/tasks lib/tasks/`):

| Variable | Read at | Effect |
|---|---|---|
| `TASKS_DATE_ORDER` | `config.rb` | date parsing semantics (proven above) |
| `TASKS_TIME_FORMAT` | `config.rb` | time rendering |
| `TASKS_URGENT_DAYS` | `config.rb` | quadrant/agenda classification |
| `TASKS_MAX_DEPTH` | `config.rb` | tree depth limit |
| `TASKS_MEMORY` | `config.rb` | **path** to the memory file |
| `TASKS_THEME`, `TASKS_MOUSE`, `NO_COLOR` | `config.rb` | presentation |
| `TASKS_WORKER_ID` | `bin/tasks:506` | claim/release owner written into the store |
| `TASKS_TEST_TODAY_SEQUENCE` | `bin/tasks:2214` | clock |
| `TASKS_MERGE_VERBOSE` | `merge_driver_command.rb:30` | diagnostics on stdout |
| `PATH` | pinned by the runner | recorded nowhere |

**Second-order:** `TASKS_MEMORY` is a *path* variable and is **not** in
`PATH_VARS`, so a case may legally redirect the memory file outside the fixture
copy. That is a hole in the "never touch the live store" non-negotiable, not just
a recording gap.

**Recommended fix:** record `(RECORDED_ENV | actual_env.keys).sort` — the union
of the allowlist and what was actually passed, so an unexpected variable is
visible rather than silent. Additionally, refuse a case whose `env` names a key
outside a documented set, and add `TASKS_MEMORY` to `PATH_VARS`. (Runner is
GATE-owned; reported, not changed.)

---

### 3. [High] umask is unpinned and moves fields that are compared

`determinism.md` files umask under **"Not pinnable — recorded instead"**, reason
"Host facts." That classification is factually wrong: umask is a per-process
attribute, and one `File.umask(0o022)` in the runner pins it exactly as `TZ` is
pinned. Meanwhile `mode` *is* compared — `determinism.md § Tempting but not
normalized` insists on keeping it, correctly.

**Evidence** — the identical case captured under two umasks:

```
recorded umask: 0022 / 0077
mode differences:
  .state/tasks/journal/<key>/index.json                0644 -> 0600
  .state/tasks/journal/<key>/blobs/4afd0aee…           0644 -> 0600
  .state/tasks/journal/<key>/blobs/e0ade36b…           0644 -> 0600
  .tasks.jsonl.lock                                    0644 -> 0600
store sha equal? True
```

**Divergence it lets through, both directions:**
1. The committed 27-observation baseline silently encodes the capture operator's
   umask. Anyone re-capturing on a host or CI image with a different umask gets
   four spurious `mode` mismatches per journal-bearing case.
2. Worse, symmetrically: a genuine Go regression that widens the journal index to
   0644 is indistinguishable from a umask difference, and the documented defence
   ("`environment.umask` is recorded so a legitimate umask difference can be told
   from a defect") only works if a human reads it — nothing in the comparator
   consults it.

**Recommended fix:** pin it. Set the umask in the runner before spawning, move the
row out of "Not pinnable" into the pin table, and keep recording
`environment.umask` as the proof it took.

---

### 4. [High] `TASKS_PIN_HOSTNAME` does not reach the update-stamp device slug

`lib/tasks/update_stamp.rb:59`

```ruby
def device(env: ENV, hostname: Socket.gethostname)
  override = env["TASKS_DEVICE"].to_s
  slug(override.strip.empty? ? hostname : override)
end
```

The default argument reads the **real** `Socket.gethostname` and never consults
`Tasks::Determinism.hostname`. `determinism.rb:46` acknowledges this by design
("this module does not duplicate it"), but the composition is unsafe: there are
two hostname consumers and only one is pinned by `TASKS_PIN_HOSTNAME`.

**Evidence** — `TASKS_PIN_HOSTNAME=fixture-host`, `TASKS_DEVICE` unset (which a
case may legally do: `"env": {"TASKS_DEVICE": null}` — the runner only guards
`PATH_VARS`):

```
real hostname slug: aerie
store bytes: …"updated":"2026-03-14T15:09:26Z#aerie"
```

Driven through the actual runner, this produces a **schema-valid observation with
no invariant failure**:

```
pins: … ('TASKS_DEVICE', False, None), ('TASKS_PIN_HOSTNAME', True, 'fixture-host') …
files.after[store].content_base64 contains 'aerie': True
notes: None
```

The clock is pinned, the hostname pin reports `applied: true`, and the store bytes
are machine-specific. `check_invariants` does not fire because `TASKS_DEVICE` was
not *requested*.

**Divergence it lets through:** a baseline captured on host A cannot match a
re-capture on host B, and the observation asserts the hostname was pinned. This is
precisely the failure mode `invocation.pins[].applied` exists to catch, defeated
by a second consumer.

**Recommended fix:** default `hostname:` to
`Tasks::Determinism.hostname(env: env).call` so one pin covers both consumers
(behavior is unchanged when the pin is unset — the fallback is the same
`Socket.gethostname`). Belt and braces: have the runner refuse a case that unsets
`TASKS_DEVICE`.

---

### 5. [High] `contentEncoding: "base64"` constrains nothing

JSON Schema 2020-12 treats `contentEncoding` as an **annotation**, not an
assertion; `Draft202012Validator` never rejects on it. Five byte-bearing fields
rely on it alone: `invocation.stdin.bytes_base64`, `$defs/stream.bytes_base64`,
`$defs/file_state.content_base64`, and both HTTP `body_base64` fields.

**Evidence:**

```
VALIDATES (nonsense accepted) -- stdout.bytes_base64 = 'not base64!!!'
```

**Divergence it lets through:** an implementation (or a hand-edited evidence file)
that puts raw text where base64 belongs produces a document that validates, and
whose comparison is meaningless — the schema's central "bytes are carried base64
because a JSON string cannot round-trip invalid bytes" guarantee is unenforced.

**Recommended fix:** add `"pattern": "^[A-Za-z0-9+/]*={0,2}$"` alongside every
`contentEncoding` (strict base64, matching `Base64.strict_encode64`).

---

### 6. [High] Sortedness and uniqueness — the schema's whole reason for using arrays — are prose only

The schema's top-level description says:

> Every map that could have grown open-ended keys is instead a SORTED ARRAY of
> {name/path, value} objects, so that the serialized observation is itself
> byte-stable … a map with arbitrary keys would make observation equality depend
> on the emitting language's hash ordering.

No array in the schema carries `uniqueItems`, and none carries any ordering
constraint. The property is enforced only by the Ruby runner calling `.sort`.

**Evidence:**

```
VALIDATES -- invocation.env with duplicate + unsorted names
VALIDATES -- files.after with two entries for the same path
VALIDATES -- revisions.resources with same id twice, different revision
VALIDATES -- blob_count=0 with 3 blob_sha256 entries
```

**Divergence it lets through:** Go's map iteration order is *deliberately
randomized*. A Go runner that forgets one `sort.Slice` emits observations that
validate cleanly and then mismatch on every array field, non-reproducibly. The
diagnosis cost of a randomly-ordered array is high and the symptom looks like a
real behavioral difference.

**Recommended fix:** `uniqueItems: true` on `invocation.env`, `invocation.pins`,
`files.before`, `files.after`, `files.deltas`, `revisions.resources`,
`revisions.touched_ids`, `journal.blob_sha256`, and both header arrays; plus an
explicit sortedness assertion in `porting/compare/validate` (JSON Schema cannot
express ordering).

---

### 7. [Medium-High] `porting/compare/validate` does no cross-field checking — 24/24 nonsense mutations validate

I mutated a real baseline observation 24 ways, each producing a document that is
internally contradictory or physically impossible. **All 24 validate.**

```
VALIDATES -- delta kind=created with a non-null before_sha256
VALIDATES -- delta kind=deleted with non-null after_sha256
VALIDATES -- file_state present=true with null sha256/size
VALIDATES -- file_state present=false but sha256+content set
VALIDATES -- files.mutated=true with deltas=[]
VALIDATES -- files.after=[]            (the harness observed nothing at all)
VALIDATES -- journal present=false with states+cursor populated
VALIDATES -- journal cursor=999 with 0 states
VALIDATES -- exit_status=0 AND signal=9 simultaneously
VALIDATES -- exit_status=null AND signal=null
VALIDATES -- stream sha256 unrelated to bytes_base64
VALIDATES -- valid_utf8=true with bytes that are not utf8
VALIDATES -- surface=cli with a populated http[] array
VALIDATES -- timed_out=true with exit_status=0
VALIDATES -- blob_count=0 with 3 blob_sha256 entries
… (full list reproducible from the mutation script described below)
```

Two of these deserve singling out because they defeat the schema's *stated*
purpose:

- **`files.after: []` validates.** The `files` block's headline rationale is that
  "a missing `deltas` would be indistinguishable from a harness that never
  looked". An empty `after` array achieves exactly that indistinguishability.
  Likewise the `role` enum exists "so the harness can assert that the store and
  the archive were BOTH observed" — nothing asserts it.
- **`present: true` with null `sha256`** must stay legal (symlinks are recorded
  that way by design), so the correct constraint is conditional:
  `present && symlink_target == null ⇒ sha256, size_bytes, mode non-null`.

**Recommended fix:** the highest-leverage change in this review is a ~40-line
consistency pass appended to `porting/compare/validate`, run after schema
validation:

1. `sha256(base64_decode(bytes_base64)) == sha256` when `truncated_at_bytes` is null.
2. `size_bytes == len(decoded)` likewise; `valid_utf8` agrees with a real decode.
3. delta `kind` vs before/after nullability (`created ⇒ before null`, `deleted ⇒ after null`, `modified ⇒ both non-null and different`).
4. `present`/`symlink_target`/`sha256` conditional above.
5. exactly one `role: "store"` entry in each of `before` and `after`.
6. `exit_status` XOR `signal` non-null.
7. `journal.present == false ⇒ cursor null, states empty`; `0 <= cursor < len(states)`.
8. `blob_count == len(blob_sha256)`.
9. `surface == "cli" ⇒ http == []` and `http_etag == null`.
10. every array sorted by its documented key and free of duplicates.

Several of these belong in the schema too (`if`/`then` and `oneOf` express 3, 6,
7, 9 fine); the ones requiring a digest or an ordering check cannot be, and are
why `validate` should own the whole set.

---

## WORTH DOING LATER

### 8. Fields the schema declares that are permanently null in the committed baseline

Measured across all 27 files in `porting/evidence/phase1/ruby/` **as committed at
`c500866`** (the GATE stream began re-capturing this directory during the review;
reproduce with `git stash`-free `git show c500866:porting/evidence/phase1/ruby/…`
rather than the working tree). Each is a comparison that always passes — the
rollback gap's exact shape.

| Field | Non-null | Assessment |
|---|---|---|
| `files.rolled_back` | 0/27 | **Known** (td-2bc4c5), in flight. Excluded by instruction. |
| `files.{before,after}[].symlink_target` | 0/141 | Fixture `valid/symlinked-store` **exists** but no case drives it. See 8a. |
| `journal.states[].coalesce_key` | 0/29 | Never exercised — and see finding 1. |
| `journal.states[].coalesce_scope` | 0/29 | `TASKS_PIN_COALESCE_SCOPE` exists *solely* to stabilize this and no case proves it works. |
| `journal.states[].repair` | 0/29 | Schema calls it contract ("changes whether a later undo is refused"). Unexercised. |
| `journal.states[].archive_sha256` | 0/29 | No case pairs an archive with a journal. |
| `process.signal` | 0/27 | No crash case. `process.timed_out` is false in all 27. Crash-safety is entirely unobserved. |
| `stream.truncated_at_bytes` | 0/27 | No stream exceeds 256 KiB. See finding 10. |
| `revisions.http_etag`, `http[]` | 0/27 | Out of Phase-1 scope; honestly documented as provisional. Fine. |
| `metrics.{user_cpu_ms,sys_cpu_ms,peak_rss_bytes}` | 0/27 | Advisory by design. Fine. |
| `journal.index.line_count` | 0/27 | Correct — `index.json` is not JSONL. Fine. |

Nine fixtures that exist and no phase-1 case drives include `valid/symlinked-store`,
`valid/restricted-mode-store`, `valid/deep-nesting`, and eleven `malformed/*`
directories. Wiring the first two is what would move the two most contract-bearing
null columns above.

**8a. Driving the symlink fixture exposes a second defect.** I wired a throwaway
case against `valid/symlinked-store`. `symlink_target` populates correctly
(`Tree.walk` uses `File.lstat` + `File.readlink` — the code is right), but:

```
runner failure: symlink-capture: files.mutated=true … disagrees with observed
store/archive deltas [".state/…/index.json", ".tasks.real.jsonl.lock", "tasks.real.jsonl"]
```

`check_invariants` hardcodes `%w[tasks.jsonl archive.jsonl]` instead of using the
`role` field that exists for precisely this reason ("Roles, not names, so the port
can be compared even if a future layout renames a file"). Worse, `role_for` also
matches on literal names, so the file carrying the **actual store bytes**
(`tasks.real.jsonl`) is recorded with `role: "other"` — the schema's guarantee
that "the store and the archive were BOTH observed" does not hold for a symlinked
store. Fix both by resolving roles from the paths the implementation actually
resolved (`bin/tasks config --json`) rather than from a name table.

### 9. The `restricted-mode-store` fixture cannot test what it is named for

`porting/fixtures/valid/restricted-mode-store/store/tasks.jsonl` is `0644` in the
working tree — git does not track permission bits beyond the executable bit. So
the documented `mode` contract ("a chmod-600 store must not widen to 644 across an
atomic replacement") is **untestable from any committed fixture**, and `mode` is a
constant column in the baseline. The in-flight `copy_root_mode` case key applies a
mode to the copy *root*; a per-file equivalent (or a fixture-level `modes.json`
applied at copy time) is what this needs.

### 10. Stream truncation and copy-root normalization are mutually exclusive

`stream.sha256` digests the **raw** bytes, which include the un-normalized copy
root. The comparator normalizes the decoded bytes for the untruncated case, but
for a truncated stream `porting/compare/lib/dimensions/cli.rb:146` compares the
recorded `sha256` fields directly — and those can never be made copy-root-neutral.
Consequence: any stream over 256 KiB containing the copy root can never compare
equal across two different copy roots. Masked today by the same-absolute-path
requirement; it would fail spuriously under `--cross-path`. Consider carrying a
`sha256_normalized` alongside, or documenting the incompatibility in
`determinism.md`.

### 11. Colour is an unpinned, unrecorded, unobserved input

`bin/tasks:146` — `def color(str, code) = $stdout.tty? ? "\e[#{code}m…" : str`, used
at eight sites, plus `bin/tasks:2840` passing `color: $stdout.tty?`. The harness
always redirects to a file, so `tty?` is always false: **0 of 27 observations
contain a single ANSI escape**, and the entire colour path is unexercised. `NO_COLOR`
is read by `Tasks::Config` and is neither pinned nor recorded. `determinism.md`
does not mention tty-ness at all, even though it is an input that changes stdout
bytes. Either declare colour out of Phase-1 scope in writing, or pin it (`NO_COLOR`
/ a `--color` flag) and record the resolved value in `invocation.pins`.

### 12. A second clock seam lives outside `Tasks::Determinism`

`bin/tasks:2214` reads `TASKS_TEST_TODAY_SEQUENCE` and, when set, derives `now`
from `Date.today` rather than the wall clock. This contradicts `determinism.md`'s
"All pins are read in exactly one place — `lib/tasks/determinism.rb`". It is
currently dominated by `TASKS_PIN_NOW` (`Tasks::Determinism.now || …`) so it is
harmless today, but it is an undocumented clock input and belongs either in the
Determinism module or in the doc's pin table.

### 13. `determinism.md` omits pins the harness actually sets

`TASKS_TIMEZONE` is pinned by the runner (correctly — it out-ranks `TZ` in
`Config#pick_timezone`) and reported by the probe, but appears in neither table in
`determinism.md`. The probe's own source flags this: *"`porting/specs/determinism.md`
lists TZ only, and should name both."* `PATH` (pinned to a fixed value) and
`unsetenv_others: true` (the actual mechanism that prevents leakage) are also
undocumented. Since `determinism.md` bills itself as "the complete account", these
are real omissions.

### 14. `invocation.pins[].applied` is weaker than the doc claims

`determinism.md § Verifying a pin actually took effect` presents `applied` as the
defence against "a pin that was set and ignored". It is not: the probe computes
`applied` by asking whether `Tasks::Determinism` *resolved* a value, not whether
every call site *used* it. `lib/tasks/application.rb` has ~30 methods with a
`today: Date.today` default parameter; one `bin/tasks` call site that forgets to
pass `today:` yields wall-clock-dependent output with `applied: true` recorded.
Finding 4 is the same weakness realised for hostname.

**Mitigating evidence, gathered rather than assumed:** I intercepted `Date.today`,
`Time.now`, `Socket.gethostname`, and `SecureRandom.hex` via `RUBYOPT=-r<trap>`
and ran `list`, `list --json`, `agenda`, `capture`, and `done` fully pinned. Result:
**zero `Date.today` calls** — the `today_local` seam covers those five paths
cleanly. `Time.now` appeared only inside tzinfo's class body at load. `SecureRandom.hex`
appeared only at `bin/tasks:2224` (the operation id, correctly documented as
unobservable). `Socket.gethostname` appeared twice per run from `UpdateStamp.device`'s
eagerly-evaluated default argument — harmless when `TASKS_DEVICE` is set, and
finding 4 when it is not.

That is a good result, but it is a spot check, not a structural guarantee.
**Recommendation:** turn the interception into a test — run the whole case list
under it and fail on any unpinned clock/hostname/random read. That converts
`applied` from a claim into a proof.

### 15. Directory-only effects are invisible

`Tree.walk` records files only; directories are traversed, never recorded. A stray
empty directory the implementation creates, or one it fails to clean up, never
appears in `before`, `after`, or `deltas`. Low severity (no current case depends on
it), but it is a category of filesystem effect the schema cannot express at all —
`file_state` has no directory role and no `kind`.

---

## Clean bills — categories I genuinely attacked and found nothing

**Over-normalization (review category 3): clean.** I tried to construct a contract
divergence hidden by each of the four normalizations and could not.

1. `observation_id` — minted by the runner after the process exits; provably
   untouchable by the implementation. Nothing to hide.
2. Copy-root prefix — applied to `fixture.copy_root`, `invocation.env[].value`,
   `invocation.pins[].value`, and decoded stream bytes; **deliberately not to file
   contents**, which is the load-bearing restraint. I verified this in
   `normalize.rb`: the journal index's `org` field stays compared as bytes, so the
   "journal refuses to apply when `org` mismatches" guard remains observable. The
   `/private` spelling handling is correct for macOS and does not over-reach.
3. Journal directory key — path-only, never applied to contents. The property that
   matters (same store ⇒ same key, different stores ⇒ different keys) is tested
   separately and is not what is hidden. Correct.
4. `metrics.*` — implemented as a routing rule, not a rewrite; the `performance`
   dimension can only emit advisory findings. The doc is candid that this is the
   one entry where the honest sentence is "a user can observe it, but not here".

The only **exclusion** in the comparator is `journal.index` under `--cross-path`
(`comparator.rb:228`, `dimensions/journal.rb:69`), and it is reported in the output
rather than silent, exactly as `determinism.md` promises. I found no undocumented
normalization or exclusion anywhere in `porting/compare/lib/`.

**Hash-iteration and glob ordering: clean.** `Tree.walk` sorts `Dir.children`;
every emitted array is sorted before serialization; no observation field is a map
with open-ended keys. (The *contract* is unenforced — finding 6 — but the Ruby side
is right.)

**Environment leakage into the implementation: clean.** `Process.spawn(…,
unsetenv_others: true)` with a pinned `PATH` means the operator's environment
cannot reach the process. `XDG_CONFIG_HOME` is pinned inside the copy, so a real
`host_context` entry cannot leak. This is done well. (Finding 2 is about *recording*
what was passed, not about leakage.)

**Temp-file naming: clean as designed, with a latent gap.** `determinism.md` is
right to refuse to filter `.tasks.jsonl.<pid>.<tid>.tmp` out of `deltas` — a
leftover temp file is a crashed write and hiding it would hide the crash. The
latent gap: because the name embeds pid and tid, a leftover temp file's path can
**never** compare equal between two runs, so the crash case is unmatched by
construction. Not a present defect (no crash case exists); worth a sentence in the
doc before one is written.

**`errors.md` scope:** I read it and found its "diagnostic text is contract until
proved otherwise" stance defensible and consistent with the schema's byte-level
stderr comparison. No findings.

---

## Reproduction

All results above came from throwaway scripts in the session scratchpad, driving
the committed runner with throwaway case lists under `--work` inside the
scratchpad (never `/tmp/tasks-conformance`, never the live store). The four
load-bearing experiments were:

1. **Coalesce-key nondeterminism (finding 1)** — `tasks delegate 1a2b3c02 implement`
   twice against two pristine copies of `valid/small-gtd` under the full pin set;
   compare `tasks.jsonl` and `.state/tasks/journal/*/index.json` digests.
2. **Env blindness (finding 2)** — one case list with and one without
   `"env": {"TASKS_DATE_ORDER": "dmy"}`, same `case_id`, `argv` including
   `--deadline 03/04/2026`; compare `json.dumps(obs["invocation"], sort_keys=True)`
   and the store digests.
3. **umask (finding 3)** — the same capture case run under `(umask 022; …)` and
   `(umask 077; …)`; diff `mode` across `files.after`.
4. **Hostname leak (finding 4)** — `"env": {"TASKS_DEVICE": null}` with
   `TASKS_PIN_HOSTNAME=fixture-host`; grep the observed store bytes for the real
   host slug.

Plus a 24-mutation schema-rigor script (finding 7) and a `RUBYOPT` clock/random
interception harness (finding 14).

I changed no code and no fixture. The only file I wrote is this one.
