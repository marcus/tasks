# valid/empty-store

A store containing nothing but the schema header — the state a brand-new data
directory is in before the first capture.

## What it exercises

- The one-line file: `{"type":"meta","version":2}` and nothing else.
- Empty-collection paths across every read surface (list, agenda, next, inbox,
  projects, quadrants) and the JSON envelopes they emit.
- The lower boundary of record counting and pluralization ("0 tasks").
- No `archive.jsonl` on disk at all — absence, not an empty archive.

## What a correct implementation must do

Parse it without error, report zero tasks, and emit empty (not null) collections
from every read command. A capture into this store must create the section it
needs rather than assuming one exists.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 0 tasks parsed, no structural errors
exit 0
```
