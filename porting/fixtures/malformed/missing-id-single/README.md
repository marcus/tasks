# malformed/missing-id-single

A healthy store with exactly one task record that carries no `id` — the shape a
hand edit or a naive append produces. Line 5.

## Why `malformed/` and not `compat/`

`compat/` means bytes a *different version* of this binary legitimately wrote.
No version of this binary ever wrote an id-less task: ids are minted at capture
and have been since the JSONL store existed. So this is not skew, it is damage.

And `Check` agrees, in as many words:

```text
error  line 5: record missing id
```

`check_id` treats a nil or empty id as an **error**, not a warning. A store this
class describes is one `check` rejects, and `check` rejects this one. That
settles it: `malformed/`.

The `ensure_id!` repair path is therefore, by construction, a repair of a
malformed store — which is exactly why it needs a fixture that a lint refuses.
It is the only mutation in the product that is *expected* to run against a file
`check` fails.

## What it exercises

Three separate questions the `id-minting` slice must answer, and this fixture
answers all three because the id-less record is the file's **only** error:

1. Does a read repair? No.
2. Does an ordinary mutation repair, or refuse? Refuse — before writing.
3. Does the explicit path repair? Yes, and only here.

The store carries an `Inbox` section on purpose, so that question 2 is answered
by the invalid file and not by a missing capture target.

## What a correct implementation must do

Leave the bytes alone on every read. Refuse every mutation except `tasks id`.
On `tasks id`, mint one id into that one record, stamp `updated` on it and on
nothing else, and leave every other byte identical.

## Recorded Ruby outcomes

Under the pinned environment in the corpus README, plus the runner's
`TASKS_PIN_NOW=2026-03-14T15:09:26Z` and `TASKS_PIN_IDS=bbbb0001`.

### `tasks check` — exit 1

```console
$ tasks check
error  line 5: record missing id
1 error(s), 0 warning(s)
exit 1
```

`check --all-files` reports the same single error, prefixed `tasks.jsonl:`.

### `tasks list` — exit 0, **no repair**

```console
$ tasks list
TODO
  Hand-appended task with no id  @computer
  Renew the domain registration

NEXT
  Book the meeting room for the review  @computer
exit 0
```

The store is byte-identical afterwards. Reads never mint. The id-less task is
listed like any other — it is only unaddressable *by id*, not invisible.

### `tasks capture` — exit 1, refused **before** the write

```console
$ tasks capture "New task from capture"
could not capture (no "Inbox" section found?)
task file is already invalid — run `tasks check` (nothing was written)
exit 1
```

Byte-identical afterwards. See the finding below about that first line.

### `tasks id "Hand-appended"` — exit 0, **repaired**

```console
$ tasks id "Hand-appended"
bbbb0001
TODO Hand-appended task with no id :@computer:
exit 0
```

Line 5 becomes:

```json
{"type":"task","id":"bbbb0001","parent":"1d000002","state":"TODO","title":"Hand-appended task with no id","tags":["@computer"],"updated":"2026-03-14T15:09:26Z#fixture"}
```

Every other line is unchanged, including the other tasks' absent `updated`
fields. The file now passes `check`.

## Findings

**1. The repair is on an explicit path only — not on read, and not as a
side effect of any other write.** `Store#ensure_id!` is reachable from exactly
one command, `tasks id`. `Store#ensure_id_impl` is the whole of it: locate,
return early if an id is already present (no write at all), otherwise mint,
rewrite the file, reload. There is no lazy repair anywhere in the read path, and
no other mutation reaches it. A port that mints ids opportunistically while
parsing would pass every valid fixture in this corpus and silently rewrite this
one on `tasks list`.

**2. `locate` falls back to line-and-title when there is no id.** This is what
makes the repair possible at all — `Store#locate` matches on `item.id` when
present and otherwise on `record["line"] == item.line` *plus* an exact
`record["title"]` match. So the id-less record is addressable, but only by its
position and its exact title. Two id-less tasks with the same title on different
lines still resolve correctly; the line number is doing the work.

**3. The repaired record gets an `updated` stamp, because `stamp_changed_tasks!`
cannot tell a repair from a new task.** It indexes the pre-write records by id,
looks the proposed record up by *its* id — which is the id that was just minted,
present in no original — finds nothing, and concludes the task is new. So minting
an id is observable twice: as the `id` field and as a fresh `updated` stamp on a
task whose semantics did not change. This is the recorded behavior, and a port
must reproduce it; suppressing the stamp would be more defensible and would still
be a divergence.

**4. The `capture` refusal names the wrong cause.** The store *has* an `Inbox`
section, yet stderr's first line is `could not capture (no "Inbox" section
found?)`. That line is `cmd_capture`'s established wording for a nil result; the
actual cause is on the second line, from `mutation_result_failed`'s
`store_invalid?` branch. The truth is present but demoted. A port must emit both
lines in this order — the misleading one is contract now.
