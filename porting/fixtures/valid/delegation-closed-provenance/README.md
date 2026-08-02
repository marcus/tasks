# valid/delegation-closed-provenance

Two closed tasks that still carry their `delegation` object: a DONE task whose
agent claim was never torn down, and a CANCELLED task that was still waiting on
a person when it was dropped. This is delegation as **provenance** — the record
of who the work went to — rather than as a live routing marker.

## Why `valid/`

Nothing here is broken. `Check` forbids a delegation only on a *proposed* task
(`check_delegation`), and forbids a `closed` date only on an open or proposed
one; DONE and CANCELLED with both a `closed` date and a retained delegation is a
healthy store, and `check` exits 0. The store is small and pointed: four
records, no dates, no times, no recurrence, no unknown keys.

## What it exercises

- The closed-provenance case the rest of the corpus lacked: every other
  delegation in `porting/fixtures/` sits on an open task, so `ready`, `claimed`,
  and `delegated` were covered on live work only.
- Both `kind`s survive closure: an `agent`/`claimed` delegation with `mode`,
  `assignee`, and `work_ref`, and a `human`/`delegated` delegation with an email
  assignee.
- `Delegation::KEY_ORDER` emission on a record that also carries `closed`, which
  follows `delegation` in `Format::KEY_ORDER`.
- The scope split on the read side: a delegation on a closed task is invisible
  to the default open scope and appears only under a closed scope.
- Retention through a write: a mutation on a closed task re-emits the delegation
  untouched rather than treating closure as a reason to clear it.

## What a correct implementation must do

Treat `delegation` as orthogonal to lifecycle state past PROPOSED. Do not clear
it on close, do not error on it, do not surface it in open-scope delegation
queries, and re-emit it byte-identically on any write that touches the record.

## Recorded Ruby outcome

Recorded against `c500866` on ruby 4.0.6 (arm64-darwin23), under the corpus
environment (`TASKS_TIMEZONE=UTC`, `TASKS_DEVICE=fixture`, empty
`XDG_CONFIG_HOME`, `TASKS_DIR` at a copy).

```console
$ tasks check
ok — 2 tasks parsed, no structural errors
exit 0
```

```console
$ tasks list --delegated
No matching tasks.
exit 0

$ tasks list --delegated --done
claimed by harness/model/session-0042: Agent finished the migration note
delegated → sam.rivera@example.com (CANCELLED): Person never took the handover
exit 0

$ tasks list --agent-ready
No matching tasks.
exit 0
```

Note the asymmetry in the `--done` listing: the CANCELLED row is annotated
`(CANCELLED)` and the DONE row is not. The closed scope is named `--done`, so
DONE is the unmarked case and CANCELLED is the one worth spelling out. A port
must reproduce the annotation on exactly that row.

```console
$ tasks show d1000002 --include-done
DONE Agent finished the migration note
  id:        d1000002
  project:   Closed provenance
  delegation: claimed by harness/model/session-0042 (implement) (since 2026-06-03T11:15:00Z)
  work ref:  https://example.invalid/runs/0042
  availability: closed
  closed:    2026-06-04
exit 0
```

A write to a closed task keeps the delegation, in `Delegation::KEY_ORDER`, in
its `Format::KEY_ORDER` position before `closed`:

```console
$ tasks tag d1000002 +audited --include-done
DONE Agent finished the migration note :audited:
exit 0
```

```json
{"type":"task","id":"d1000002","parent":"d1000001","state":"DONE","title":"Agent finished the migration note","tags":["audited"],"delegation":{"kind":"agent","mode":"implement","status":"claimed","assignee":"harness/model/session-0042","at":"2026-06-03T11:15:00Z","work_ref":"https://example.invalid/runs/0042"},"closed":"2026-06-04","updated":"<stamp>#fixture"}
```

The `updated` stamp is wall-clock, so it is elided here as `<stamp>`; only its
device slug (`fixture`, from the pinned `TASKS_DEVICE`) is contract. Note that
`d1000003` is not rewritten by this invocation and keeps no `updated` at all.
