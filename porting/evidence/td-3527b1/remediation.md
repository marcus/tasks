# Remediation of the independent adversarial review — td-3527b1

Companion to [`review-independent.md`](review-independent.md). Everything below
was executed against the working tree, not asserted. The summary table lives in
[`../phase1/GATE.md` § 4](../phase1/GATE.md); this file is the transcript.

## Finding 1 — unpinned `SecureRandom.hex(8)` in journal bytes (td-3cf46c)

**Reproduced.** Two fully-pinned `tasks delegate 1a2b3c02 implement --json` runs
against two pristine copies of `valid/small-gtd`:

```text
store sha (both runs):  2bfd93b235e573a17a5d …   identical
index sha run 1:        dd0723a0e27c61ad210e …
index sha run 2:        99e620ff7e1c578307be …   DIFFERENT
coalesce_key run 1:     delegation-delegate-486942e6c9368b6f
coalesce_key run 2:     delegation-delegate-712a0f40a6b931f2
```

**Fixed** by minting the key through a new pinned seam
(`Tasks::Determinism::DELEGATION_KEYS` = `TASKS_PIN_DELEGATION_KEYS`, injected
into `Tasks::Application` as `delegation_key_source`, wired in `bin/tasks`), and
by adding it to the runner's pin set. `IdSequence` was generalized to an
arbitrary token width — 8 hex for ids, 16 for delegation keys — rather than
duplicated.

**Proved, end to end, through the runner.** The gap that let the defect survive
was that no Phase-1 case ran a delegation, so a new case was added:

```console
$ porting/runners/ruby/run --out A --pin-identity --case cli-delegate-agent-ready …
$ porting/runners/ruby/run --out B --pin-identity --case cli-delegate-agent-ready …
$ diff A/cli-delegate-agent-ready.json B/cli-delegate-agent-ready.json   # no output
```

And the negative control — the same case with `"env": {"TASKS_PIN_DELEGATION_KEYS": null}`:

```text
12 differing lines, including
<   "coalesce_key": "delegation-delegate-f2cc7adaa1495124"
>   "coalesce_key": "delegation-delegate-3eaa59171f5e387e"
```

## Finding 3 — umask (td-38aed8)

Pinned in the runner (`File.umask(0o022)` before any copy or spawn; it is
inherited across `fork`/`exec`). `porting/specs/determinism.md` moved the row out
of "Not pinnable — recorded instead" into a new "Pinned by the harness process"
section, and the `mode` entry in "Tempting but not normalized" was corrected.

Regression test launches the runner from a process whose umask is `0077` and
asserts the journal index the invocation *creates* is still `0644` —
`test_the_umask_is_pinned_not_inherited`. The store file is deliberately not the
subject: it already exists and keeps its own mode across the atomic replacement,
so it would read `0644` either way and prove nothing.

## Findings 5 and 6 — base64 and array contracts (td-050763, td-297e4b)

`contentEncoding` is an annotation in JSON Schema 2020-12. All five byte-bearing
fields now carry a strict-base64 `pattern`, and `porting/compare/validate`
decodes them and checks the digest and size recorded beside them. Ten arrays
gained `uniqueItems`; sortedness — which JSON Schema cannot express — is asserted
in `validate`, keyed on each array's documented sort key.

## Finding 7 — cross-field coherence (td-aac6ec)

A ~200-line consistency pass appended to `porting/compare/validate`, implementing
all ten checks the review proposed. One deliberate deviation: check 5 is "at
least one `role: "store"` entry", not "exactly one", because a store reached
through a symlink is legitimately two paths carrying that role.

**Measured.** 28 deliberately incoherent mutations of a real baseline
observation (the review's 24, plus four ordering mutations): **28/28 rejected**,
including `files.after: []`, `exit_status=0 AND signal=9`, `blob_count=0` with 3
blob digests, `bytes_base64 = "not base64!!!"`, unsorted `invocation.env`, and
duplicate `revisions.resources` ids. Zero false positives across the 33-case
baseline.

## Finding 8a — the roles resolver

Driving `valid/symlinked-store` reproduced both halves of the review's finding
and exposed a third:

```text
runner failure: cli-capture-symlinked-store: files.mutated=true … disagrees with
observed store/archive deltas [".state/…/index.json", ".tasks.real.jsonl.lock",
"tasks.real.jsonl"]
```

1. `role_for` matched literal filenames, so `tasks.real.jsonl` — the file
   carrying the store's bytes — was recorded as `role: "other"`.
2. `check_invariants` compared delta paths against `%w[tasks.jsonl
   archive.jsonl]` instead of against roles, so a correct run failed.
3. **New defect, not in the review.** The runner computed the journal key from
   `realpath(<copy>)/tasks.jsonl` without resolving the store symlink itself,
   while the implementation resolves it (`Journal.canonical`). The two keys
   disagreed, so `journal.present` read `false` for a run that had just written a
   journal, and the fixture journal would have been installed in a directory the
   implementation never reads.

All three fixed: roles come from the probe's reported paths (`store`,
`store_canonical`, `archive`, `archive_canonical`, `memory`, `config`), the
journal is matched by shape, and `org_path` is symlink-resolved. Both spellings
of the store carry `role: "store"`.

A fourth, subtler one surfaced while fixing it: `Tree.relative` returns its
argument unchanged when the path is not under the root, so a candidate that is
still absolute has to be rejected explicitly — and on macOS the canonical paths
are spelled `/private/tmp` while the copy root is `/tmp`, so both spellings of
the root must be tried.

## Finding 9 — `perms.json` (the copy protocol)

Kept as a **fixture-level** manifest, separate from the case-level
`copy_root_mode`. The three grounds are argued in
`porting/runners/README.md` § "Two modes, and why they are not one key": different
owners (the fixture *is* a 0600 store, for every case that uses it), different
scopes (a path→mode map on the case would let any case chmod any file and make
`files.before` a case-authored fiction), and different lifecycles (the root mode
must *not* reach the throwaway probe copy; the file modes must).

Result: `cli-capture-restricted-mode` records `0600` in `files.before` **and**
`files.after`, so the "a chmod-600 store must not widen to 644" contract is
tested rather than merely documented.

## Finding 14 — `applied` is a claim, not a proof

`test/test_porting_determinism_seams.rb` + `test/support/determinism_trap.rb`.
The trap patches `Date.today`, `Time.now`, `Socket.gethostname`, and
`SecureRandom.hex`/`uuid` **at the source** and is loaded into a `bin/tasks`
child via `RUBYOPT=-r`. Five fully-pinned commands (`list`, `agenda`, `capture`,
`done`, `delegate`) are run; any read originating in `bin/` or `lib/tasks/` fails
the test, except the operation id, allowlisted by its enclosing method name.

The trap's own fidelity is demonstrated by running it **unpinned**, where it
records exactly the seams the review named:

```text
1 SecureRandom.hex      /bin/tasks:2226:in 'Object#cli_operation_context'
1 SecureRandom.hex      /lib/tasks/application.rb:593:in 'Tasks::Application#mint_delegation_key'
1 SecureRandom.hex      /lib/tasks/application.rb:95:in 'Tasks::StoreFactory#initialize'
1 SecureRandom.hex      /lib/tasks/journal.rb:85:in 'Tasks::Journal#initialize'
3 Socket.gethostname    /lib/tasks/determinism.rb:176:in 'block in Tasks::Determinism.hostname'
1 Time.now              /bin/tasks:2220:in 'Object#temporal_context'
2 Time.now              /lib/tasks/application.rb:81:in 'block in Tasks::StoreFactory#initialize'
```

Under the pins, all of these disappear except the allowlisted operation id, and
`Date.today` never appears in either regime — the `today_local` seam covers those
paths cleanly, as the review found.

## Not fixed

- **Finding 10** — a stream over 256 KiB containing the copy root can never
  compare equal across two copy roots, because `stream.sha256` digests raw
  bytes. Masked today by the same-absolute-path requirement; would fail under
  `--cross-path`. Needs either a `sha256_normalized` field or a documented
  incompatibility, and both are schema changes beyond this remediation.
- **Finding 11** — colour is an unpinned, unrecorded, unobserved input. The
  harness always redirects to a file so `tty?` is always false; the entire colour
  path is unexercised. Needs a decision (declare out of Phase-1 scope in writing,
  or pin and record it), not a patch.
- **Finding 12** — `bin/tasks` reads `TASKS_TEST_TODAY_SEQUENCE` outside
  `Tasks::Determinism`. Dominated by `TASKS_PIN_NOW` today, so harmless; moving
  it is a product change, not a pinning change.
- **Finding 15** — directory-only effects are invisible; `file_state` has no
  directory role and no `kind`. A schema change.

## Re-establishing the gate

```console
$ porting/evidence/capture                              # 33 observations; provenance honest
$ porting/compare/audit porting/evidence/phase1/ruby    # COVERAGE COMPLETE, exit 0
$ porting/evidence/gate                                 # GATE PASS
$ porting/compare/validate porting/evidence/phase1/ruby # 33/33 valid and internally coherent
$ ruby test/all.rb                                      # 2189 runs, 0 failures
$ porting/test-loop-limits.sh                           # passed: 168  failed: 0
```

Two `--pin-identity` captures of the 33-case list are byte-identical (`diff -r`,
no output).
