# valid/scale-ordering

461 records — 10 top-level sections, 50 project sections, 400 tasks, many with a
nested child — generated deterministically.

## What it exercises

- Ordering at a size where an unstable sort, a hash-iteration-order dependency,
  or an off-by-one in the DFS walk actually changes the output. A 10-record
  fixture hides all three.
- Sibling order preservation across 50 sibling groups of varying size.
- Every state, several priorities, tags, and a spread of `scheduled` /
  `deadline` dates across all twelve months of 2026, so date bucketing and
  agenda grouping have real data.
- Read-path performance: this is the fixture to time a port against.

## What a correct implementation must do

Produce output identical to Ruby's for every read command, in particular
preserving file order as the tie-break wherever the spec does not name another
sort key.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 400 tasks parsed, no structural errors
exit 0
```
