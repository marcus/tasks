# adversarial/mid-write-torn-file

`tasks.jsonl` truncated 42 bytes from the end, cutting the final `section`
record mid-key (`{"type":"sec`) — a store caught mid-write by a writer that did
not go through atomic replacement.

## What it exercises

- The split between the tolerant read path and the strict write path.
- Preflight validation on mutation: a write refuses on an already-invalid file
  and reports that nothing was written.
- Error line numbering on a partial trailing line with no terminating newline.

## Recorded behavior

```console
$ tasks list
(lists all three tasks — the torn trailing record is silently dropped)
exit 0

$ tasks capture "New capture on a torn store"
could not capture (no "Inbox" section found?)
task file is already invalid — run `tasks check` (nothing was written)
exit 1
```

## Finding

**Reads succeed on a torn store.** `Format.parse` collects the unparseable line
as an error and returns the records it did parse; the read commands use the
records and ignore the errors. So `tasks list` renders a store that is missing a
record, with no warning to the user.

The mutation path does gate — but note the message pair: the *first* line blames
a missing Inbox section (the section that was torn off), and only the second
names the real problem. A port that emits only one of those two lines, or emits
them in the other order, diverges observably.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 7: invalid JSON: unexpected end of input, expected closing " at line 1 column 13
1 error(s), 0 warning(s)
exit 1
```
