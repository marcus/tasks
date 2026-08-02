# malformed/bom-prefixed

`valid/single-task`'s bytes with a UTF-8 BOM (`EF BB BF`) prepended, and nothing
else changed. A `diff` against that fixture's `store/tasks.jsonl` is exactly the
three leading bytes, so any behavioral difference between the two is
attributable to the BOM alone.

## Why `malformed/` and not `valid/`

The bytes violate the on-disk contract — `Format.dump` never emits a BOM, so no
`tasks` binary produced this file — even though `check` exits 0 on it. That is
the same shape as `malformed/wrong-key-order`: a store this tooling would never
write, whose recorded diagnostic happens to be silence. Putting it in `valid/`
would assert the BOM is part of the healthy format, which it is not.

## What it exercises

- `Format.parse`'s BOM-tolerance branch: `text.sub(/\A﻿/, "")` before the
  line split. Without it, line 1 is not valid JSON and the store has no `meta`
  record, so a BOM would turn a healthy store into "missing meta record" plus an
  invalid-JSON error — an editor's invisible byte locking the user out.
- The stripping is on the *text*, not the line: only a BOM at offset 0 is
  tolerated. A U+FEFF anywhere else stays part of whatever line carries it.
- The BOM is not preserved. It is dropped on the first write, because the
  re-emitted file comes from `Format.dump` over the parsed records.

## What a correct implementation must do

Strip one leading U+FEFF from the whole-file text before parsing lines, then
parse identically to the un-prefixed store: same records, same 1-based line
numbers (the BOM does not shift line 1), same `check` outcome. On write, emit no
BOM — the file is silently normalized.

## Recorded Ruby outcome

Recorded against `c500866` on ruby 4.0.6 (arm64-darwin23), under the corpus
environment (`TASKS_TIMEZONE=UTC`, `TASKS_DEVICE=fixture`, empty
`XDG_CONFIG_HOME`, `TASKS_DIR` at a copy).

```console
$ tasks check
ok — 1 task parsed, no structural errors
exit 0
```

Byte-identical to `valid/single-task`'s recorded outcome — which is the point.

```console
$ tasks list
NEXT
  Water the office plants  @home

exit 0
```

The BOM does not survive a write. After any mutation the file starts at `{`:

```console
$ tasks priority 00000001 A
NEXT [#A] Water the office plants :@home:
exit 0

$ xxd tasks.jsonl | head -1
00000000: 7b22 7479 7065 223a 226d 6574 6122 2c22  {"type":"meta","
```
