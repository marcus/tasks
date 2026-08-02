# valid/temporal-both-times

One task carrying all four temporal keys at once: `scheduled`,
`scheduled_time`, `deadline`, and `deadline_time`. Nothing in the corpus did
before — `full-field-matrix` has four records with a `scheduled_time` and one
with a `deadline_time`, never both on one record.

## Why `valid/`

A healthy store: three records, `check` exits 0. The fixture adds no error
condition at all — its whole job is to make one byte-ordering obligation
falsifiable.

## What it exercises

`Format::KEY_ORDER` **interleaves** the date and its time object:

```text
… scheduled  scheduled_time  deadline  deadline_time  recur …
```

A writer that grouped the two dates first and the two time objects after —
`scheduled deadline scheduled_time deadline_time` — is byte-identical to the
correct one on every other fixture in the corpus, because no other record has
more than one time object. This fixture is the single case that distinguishes
them.

It also pins `NESTED_KEY_ORDER` (`local`, `timezone`, `fold`) on both temporal
fields in the same record, each with its own timezone, so a port cannot pass by
hard-coding one zone or by reusing one object for both.

## What a correct implementation must do

Emit the four keys in `KEY_ORDER` position, each time object's own keys in
`NESTED_KEY_ORDER`, and preserve that layout across a write that touches an
unrelated field.

## Recorded Ruby outcome

Recorded against `c500866` on ruby 4.0.6 (arm64-darwin23), under the corpus
environment (`TASKS_TIMEZONE=UTC`, `TASKS_DEVICE=fixture`, empty
`XDG_CONFIG_HOME`, `TASKS_DIR` at a copy).

```console
$ tasks check
ok — 1 task parsed, no structural errors
exit 0
```

A write that touches only `priority` re-emits the temporal keys interleaved and
in nested order:

```console
$ tasks priority e1000002 A
NEXT [#A] Window with a start time and a due time
exit 0
```

```json
{"type":"task","id":"e1000002","parent":"e1000001","state":"NEXT","priority":"A","title":"Window with a start time and a due time","scheduled":"2026-06-16","scheduled_time":{"local":"09:30","timezone":"Europe/London"},"deadline":"2026-06-18","deadline_time":{"local":"17:00","timezone":"America/Los_Angeles"},"updated":"<stamp>#fixture"}
```

The `updated` stamp is wall-clock and is elided here as `<stamp>`; only its
device slug (`fixture`, from the pinned `TASKS_DEVICE`) is contract.
