# `porting/evidence/` — the Ruby oracle, captured

Every Go slice is measured against what the Ruby CLI *actually did*, not against
what anyone remembers it doing. This directory holds that record.

```text
porting/evidence/
  capture              take (or re-check) a baseline, with provenance
  gate                 the Phase 1 acceptance criterion, as one command
  phase1/
    provenance.json    commit, corpus digest, pin set, runner version, host
    GATE.md            the Phase 1 gate result — including what it does NOT prove
    ruby/              one observation per case in phase1.jsonl
```

## The baseline

`phase1/ruby/` is the Ruby result set for
[`porting/runners/cases/phase1.jsonl`](../runners/cases/phase1.jsonl), captured
under the protocol's full pin set with `--pin-identity`, so two captures of the
same commit are byte-identical and a `diff -r` is a valid comparison.

Every record validates against
[`observations.schema.json`](../specs/observations.schema.json)
(`Draft202012Validator`, jsonschema 4.26.0) **and** passes the consistency pass
the schema cannot express — digests that match their own bytes, deltas whose
`kind` agrees with their nullability, arrays that are sorted and duplicate-free:

```console
$ porting/compare/validate porting/evidence/phase1/ruby
validate: 33/33 observations valid against observations.schema.json and internally coherent
```

## Re-capturing it

```console
$ porting/evidence/capture
```

Two things about that command matter.

**It must run against a clean `bin/` and `lib/`.** A baseline captured over
uncommitted implementation changes cannot be reproduced by anyone, including its
author. `capture` records `repo.implementation_clean` and warns loudly when it
is false. If the working tree has in-flight implementation changes, capture from
a detached worktree at the commit you mean:
`git worktree add --detach <dir> <commit>`.

**It must use the same `--work` root as the side it will be compared against**
(default `/tmp/tasks-conformance`). The journal's directory key and its
`index.json` both encode the store's canonical absolute path, and
`porting/specs/determinism.md` refuses to normalize those bytes. See
`porting/runners/README.md` § "The same-absolute-path requirement".

## Knowing it has drifted

```console
$ porting/evidence/capture --check
```

Re-runs the corpus against today's Ruby and compares the result to the committed
baseline. Exit `0` means the oracle still says exactly what the evidence says it
said. Exit `1` means the Ruby CLI's observable behavior changed, and **every Go
slice compared against this baseline is now measuring against a stale oracle**.

That is the playbook's step 10 ("scan for drift immediately") reduced to one
command, and it is the intended CI hook: run it after any change under `bin/` or
`lib/`, and require one of the three dispositions — ported to Go, explicitly not
applicable, or blocking cutover.

`--check` also catches the ordinary case of the baseline simply being old. It is
expected to fail the first time the Ruby CLI's behavior legitimately changes; the
answer is to re-capture *and say what changed*, never to re-capture quietly.

## What provenance records, and why each field is there

| Field | Answers |
|---|---|
| `repo.commit`, `repo.implementation_clean` | which Ruby produced this, and whether it corresponds to a commit at all |
| `inputs.case_list_sha256`, `case_count` | which questions were asked |
| `inputs.fixture_corpus_sha256` | which inputs they were asked against — a tree digest over every fixture file, so a single edited byte anywhere in the corpus changes it |
| `inputs.runner_sha256`, `probe_sha256` | which harness took the reading; the probe is what supplies revision tokens and resolved pins |
| `inputs.schema_sha256` | which shape the records claim to be |
| `invocation.command`, `work_root`, `pin_identity` | the exact command to re-run |
| `host.ruby`, `platform`, `uname` | what a legitimate `environment.*` difference would be attributable to |

The pin set itself is not duplicated here — it is in every observation, as
`invocation.env` (what was requested) and `invocation.pins` (what the
implementation actually resolved, which is the half that catches a pin set and
silently ignored).

## The gate

```console
$ porting/evidence/gate            # human
$ porting/evidence/gate --json     # machine
```

Runs the Phase 1 acceptance criterion end to end and exits nonzero if any part of
it stops holding. The result, with its caveats, is in
[`phase1/GATE.md`](phase1/GATE.md).
