# Phase 1, Ruby vs Go — the first real conformance report

This directory holds the Go port's observations for
`porting/runners/cases/phase1.jsonl`, the counterpart of `../ruby/`. The
comparison of the two is `../ruby-vs-go.txt` (human) and `../ruby-vs-go.json`
(structured).

Until this run, `porting/compare` had never been pointed at Go. It had only
ever been exercised Ruby-against-perturbed-Ruby (`porting/compare/seed`),
because there was no Go binary and no Go runner to ask. There is now.

## The verdict

```text
cases 33   25 match   8 mismatch   0 unpaired
GATE FAIL
```

**Every one of the 8 mismatches is a case that writes.** The Go port has a read
path and no write path — no atomic replace, no journal append — which was the
deliberate scope of this stage. Nothing else in the corpus differs.

| | Cases | Verdict |
|---|---|---|
| `check` | 7 | match |
| `list` (human) | 5 | match |
| `list --json` | 3 | match |
| `undo` (all refusals) | 4 | match |
| `done` ref refusals (exit 2) | 2 | match |
| `agenda` | 1 | match |
| `capture` preflight refusals | 3 | match |
| **writes** (`capture` ×3, `done`, `priority`, `delegate`, `claim`, rollback) | 8 | **mismatch — not implemented** |

Both runners exited `0`, which is the stronger half of the result: every runner
invariant held on the Go side too. No pin was set and silently ignored, and
`files.mutated` — taken from Go's *own* store revision token — agreed with the
harness's independently measured store/archive deltas on all 33 cases.

## Reproduce it

```console
$ porting/runners/ruby/run --out /tmp/conf-ruby --pin-identity porting/runners/cases/phase1.jsonl
$ porting/runners/go/run   --out /tmp/conf-go   --pin-identity porting/runners/cases/phase1.jsonl
$ porting/compare/validate /tmp/conf-go
$ porting/compare/compare  /tmp/conf-ruby /tmp/conf-go
```

Both sides must use the same `--work` (the default `/tmp/tasks-conformance` is
fine) — see `porting/runners/README.md` § "The same-absolute-path requirement".
`porting/runners/go/run` builds `go/bin/tasks` and `go/bin/tasks-probe` first,
in the operator's environment, because the pinned `PATH` an invocation gets has
no toolchain on it.

`porting/compare/validate /tmp/conf-go` reports 33/33 schema-valid: the Go
runner satisfies `porting/specs/observations.schema.json` and the cross-field
coherence pass, so the mismatches below are differences in *behaviour*, not in
the shape of the evidence.

A second, narrower gate covers the layer underneath the CLI:

```console
$ bash go/testdata/probe-parity.sh              # the 33 phase1 fixtures
$ bash go/testdata/probe-parity.sh --fixtures   # all 61 fixtures, pristine
```

It runs BOTH probes under the protocol's pinned environment and diffs
`revisions`, `paths` and `pins` field for field. **All 33 phase1 fixtures and
all 61 fixtures in the corpus are identical**, which is what makes the CLI
results above trustworthy: config
resolution, the store read path, the `s1.` store token and the
`v1.<own>.<location>.<lifecycle>` per-task tokens are already proven to agree
with the oracle before any command renders a byte.

## The 8 mismatches, classified

`porting/specs/errors.md` requires each difference to be classified rather than
counted. All 8 fall in one class.

**Class: intentional difference — scope, not defect.** The Go port implements
the read path only. Each of these cases reaches the point at which Ruby would
write and stops there, with a nonzero exit status and a stated reason on
stderr, rather than performing a partial write. What differs is exactly the
store bytes, the journal blobs and index, the success payload, and the exit
status that a completed write would have produced — 233 gate findings spread
over `files` (118), `journal` (66), `cli` (30) and `revisions` (19), all
downstream of one absence.

- `cli-capture-small-gtd`, `cli-capture-restricted-mode`,
  `cli-capture-symlinked-store` — `capture` writes.
- `cli-done-small-gtd` — the `done` state transition writes. Note that the two
  `done` cases which *refuse* (`cli-done-no-match-ref`,
  `cli-done-ambiguous-ref`) both match, including the exit-2 distinction
  `errors.md` calls the most important single assertion in the error surface,
  and the ambiguous-ref candidate list byte for byte.
- `cli-priority-forward-compat` — `priority` writes, and must preserve unknown
  keys while doing it.
- `cli-delegate-agent-ready`, `cli-claim-same-owner-retry` — the delegation
  verbs write.
- `cli-capture-readonly-rollback` — the corpus's only write-then-revert. It
  cannot be reached without a write path at all: `files.rolled_back` is `true`
  on the Ruby side and `null` on the Go side, which is the field doing exactly
  its job.

**No Go defect is hiding in this set.** That claim is checkable rather than
asserted: the read-only half of every one of these fixtures is already covered
by a matching case (`valid/small-gtd` by `cli-list-small-gtd`,
`compat/forward-compat-unknown-keys` by `cli-check-forward-compat`,
`adversarial/stale-lock-sidecar` by `cli-list-stale-lock`), and all 8 fixtures
are probe-identical.

## Two advisories that are not defects

**`environment.platform` differs on every case** — `arm64-darwin23` (Ruby's
`RUBY_PLATFORM`) vs `arm64-darwin` (Go's `GOARCH-GOOS`). Classified
`nondeterminism/advisory`; it cannot fail the gate. It does, however, set
`requires_rerun` on every other finding, and the report's NOTE then asks for "a
re-run with the environments matched" — which is impossible here and misleading.

This is a **gap in the protocol, not in either implementation**, and it is left
for Marcus to decide rather than papered over: `environment.platform` was
specified for cross-*host* attribution ("a difference elsewhere can be
attributed to a tzdb release or a platform"), where both sides are the same
runtime on different machines. Across two *runtimes* on one machine it can never
agree, because each side honestly reports its own build identity at its own
granularity. Making Go synthesise the Darwin release number so the strings match
would be conformance theatre — and wrong the first time the binary is
cross-compiled. The options are to compare `environment.platform` only within an
implementation, to define it as a host fact the *harness* supplies (it already
supplies `filesystem` and `umask`), or to leave the advisory and document it.
Nothing here is blocked on the answer.

**`files.rolled_back` is null on both sides in 32 of 33 cases.** Pre-existing
and unchanged by this run — see `porting/compare/README.md` § "The rollback
gap".

## What is NOT implemented

Stated plainly, because everything downstream will trust this file.

- **No write path at all.** No atomic replace, no preflight-then-write, no
  journal append, no undo/redo *apply* (only the refusals), no rollback. This
  is the single largest remaining piece and it is what the 8 mismatches are.
- **The `check` linter is believed complete but is only corpus-tested.**
  `check_parsed` was ported in full during this stage, which closed five
  divergences the fixture sweep had found (`malformed/broken-dfs-order`,
  `dangling-parent`, `recur-non-canonical`, `temporal-unknown-nested-key`,
  `unknown-type-no-id` all reported `ok` where Ruby reports `store_invalid`).
  All 61 fixtures are now probe-identical, and a spot-check case list running
  `check` against those five under both runners reports 5/5 match, GATE PASS.
  That is corpus evidence, not a proof of completeness: any rule no fixture
  exercises is still unverified in either direction.
- **`http` / the HTTP adapter**, the TUI, and the colour path. Out of scope for
  Phase 1 by protocol; no case reaches a terminal, so no observation contains an
  ANSI escape and a green run says nothing about colour in either direction.
- **Commands outside the corpus.** `go/cmd/tasks` dispatches only what phase1
  exercises plus `config --json` (which the harness needs before it will run at
  all). Everything else refuses.

## Notes for Marcus — found, not fixed

Each was ported faithfully rather than "improved", per the porting rules.

1. **Possible oracle defect in `task_revision`.** The `own` component runs its
   values through `revision_value`, which key-sorts nested objects; the
   `lifecycle` component digests `record["scheduled_time"]` raw, in source
   order. Two stores that mean the same thing therefore get different lifecycle
   fingerprints purely from a writer's member ordering, which invalidates every
   `If-Match` in the subtree after a re-serialisation. Reproduced on both sides
   and asserted as-is in
   `go/internal/store` § `TestRevisionNormalizesOwnButNotLifecycleAcrossNestedKeyOrder`.
2. **`store.semanticTags` drops a non-string tag** where Ruby's
   `tags.map(&:to_s)` converts it (`123` → `"123"`). No phase1 case reaches it.
   Worth a slice.
3. **`config --json` link/colour/host-context maps sort their keys**; Ruby
   preserves config-file line order, which a Go map cannot recover. No fixture
   configures those keys.
4. **`LANG`/`LC_ALL` resolve to a constant `"UTF-8"` in Go**, which has no
   per-process external encoding. Identical under the pinned environment; a case
   setting `LANG=en_US.ISO-8859-1` would make Ruby report `applied: true` and Go
   `applied: false`.

## What the next agent should build

In this order, because each unlocks corpus coverage the one before it cannot:

1. **The write path** — `internal/store`'s atomic replace with its preflight and
   post-write validation, then `capture`. That alone converts three mismatches,
   and it is the prerequisite for every other one.
2. **The journal write path** — append, coalesce key and scope, index bytes —
   then `done`, `priority`. The `journal` dimension's 66 gate findings are all
   here.
3. **The delegation verbs** (`delegate`, `claim`), which need the per-operation
   coalesce key `TASKS_PIN_DELEGATION_KEYS` exists to pin.
4. **The rollback path**, which `cli-capture-readonly-rollback` is the corpus's
   only witness to, and which is the one case that needs the `--json` error
   envelope to carry `rolled_back: true`.
5. Corpus coverage beyond phase1. The 61-fixture sweep is a *probe* gate; the
   CLI is only gated on 33 cases. New case lists over the fixtures phase1 does
   not name are cheap now that both runners exist, and they are where the next
   real defect will be found.

Re-run the two commands at the top after each; the number to move is
`25 match` and the gate to reach is `GATE PASS`.
