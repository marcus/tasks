# valid/small-gtd

A small, healthy, realistic GTD store: the shape most commands are exercised
against. Constructed, not derived from anyone's data.

## What it exercises

- The ordinary section vocabulary (Inbox, Next Actions, Projects, Someday /
  Maybe) with a project section nested under `Projects`.
- All four open states plus one `CANCELLED` task carrying a `closed` date.
- Priorities, `@context` tags, `scheduled` and `deadline` dates, bodies on both
  sections and tasks.
- Two levels of nesting and sibling ordering within each section.

## What a correct implementation must do

Parse cleanly, preserve file order as tree order, and round-trip byte-for-byte
when a command rewrites the file without changing anything semantic.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 8 tasks parsed, no structural errors
exit 0
```
