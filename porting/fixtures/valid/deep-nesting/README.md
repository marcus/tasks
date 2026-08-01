# valid/deep-nesting

A nine-level chain of alternating sections and tasks, with a sibling branch at
two different depths.

## What it exercises

- Nesting well past the default `max_depth` of 4 — depth limiting is a *display*
  policy, never a storage or validation rule.
- Sections parented by tasks and tasks parented by tasks, alternating.
- A DFS pre-order walk where the ancestor stack must be popped correctly: the
  two sibling records only validate if the walker unwinds to the right ancestor.

## What a correct implementation must do

Accept arbitrary depth on disk, apply `max_depth` only where the spec says a
surface truncates, and reproduce Ruby's parent-stack unwinding exactly — this is
the fixture that catches a walker that pops one frame too few or too many.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 6 tasks parsed, no structural errors
exit 0
```
