# valid/id-pin-collision

A two-file store deliberately holding the first three ids the harness's pinned
mint sequence will produce: `bbbb0001` and `bbbb0002` in `archive.jsonl`,
`bbbb0003` in `tasks.jsonl`.

The runner pins `TASKS_PIN_IDS=bbbb0001`, and `Determinism::IdSequence` continues
by incrementing. So a mutation that mints on this store must walk past three
collisions and land on `bbbb0004`.

## Why `valid/`

Both files are healthy and both lint clean. The collision is arranged in the
*data*, against a pin the runner already sets — nothing here is broken, skewed,
or adversarial.

## What it exercises

`Store#gen_id(taken)` loops the id source until it draws one not in `taken`, and
`ensure_id_impl` / the capture path pass `ids_of(records) + archived_ids` — both
files. The comment in `store.rb` calls the archive half out explicitly: "cheap to
exclude across BOTH files so a fresh id can't clash with one already swept into
the archive."

Without a pinned mint, that loop is unobservable: `SecureRandom.hex(4)` never
collides in practice, so a port that forgot the archive entirely — or forgot the
loop entirely — would pass every other fixture in this corpus forever. This
fixture makes the loop mandatory by making the collision certain.

Three exclusions, three sources, one invocation:

| Id | Where it already is | What the mint must do |
|---|---|---|
| `bbbb0001` | archive, line 2 | skip — the archive is in `taken` |
| `bbbb0002` | archive, line 3 | skip — and keep skipping, not just once |
| `bbbb0003` | live, line 4 | skip — the live file is in `taken` too |
| `bbbb0004` | nowhere | mint this |

`bbbb0002` is not redundant with `bbbb0001`: one collision proves a guard, two
consecutive collisions prove a *loop*. An implementation that draws again exactly
once passes with one and fails with two.

## What a correct implementation must do

Mint `bbbb0004`. Report it in the `--json` payload's `touched[0].id`, write it
into `tasks.jsonl`, and leave `archive.jsonl` byte-identical — capture does not
touch the archive.

## Recorded Ruby outcomes

Under the pinned environment in the corpus README, plus the runner's
`TASKS_PIN_NOW=2026-03-14T15:09:26Z` and `TASKS_PIN_IDS=bbbb0001`.

### `tasks check` — exit 0

```console
$ tasks check
ok — 1 task parsed, no structural errors
exit 0

$ tasks check --all-files
ok — 3 records parsed, no structural errors
exit 0
```

### `tasks capture "Collision probe task" --json` — exit 0, mints `bbbb0004`

```console
$ tasks capture "Collision probe task" --json
{"touched":[{"id":"bbbb0004","state":"INBOX",…,"project":"Inbox","delegation":null}]}
exit 0
```

`tasks.jsonl` afterwards — the new record is spliced under `Inbox`, ahead of the
`Next Actions` section, in DFS pre-order:

```json
{"type":"meta","version":2}
{"type":"section","id":"1f000001","title":"Inbox"}
{"type":"task","id":"bbbb0004","parent":"1f000001","state":"INBOX","title":"Collision probe task","body":"Captured [2026-03-14].","updated":"2026-03-14T15:09:26Z#fixture"}
{"type":"section","id":"1f000002","title":"Next Actions"}
{"type":"task","id":"bbbb0003","parent":"1f000002","state":"NEXT","title":"Live task holding the third pinned mint id","tags":["@computer"]}
```

`archive.jsonl` is byte-identical.

## Finding

**The archive is read on every mint, and that is a cost as well as a
correctness rule.** `archived_ids` calls `parse_records(@archive)` and the result
is not cached — `store.rb` says the archive is "read rarely", which is true of
`list -x` but not of minting, since every capture pays for a full archive parse
to build the exclusion set. On `valid/scale-ordering`-sized archives that is
real work on the hot path of the most common mutation in the product.

Recorded, not fixed. It matters to the port twice: a port that caches the archive
ids for speed changes the behavior under a concurrent archive write, and a port
that skips the archive read for speed silently breaks this fixture.
