# malformed/duplicate-open-titles

Two **open** tasks with the same title in different sections.

## What it exercises

- The warnings channel: this is a hazard (fuzzy title refs become ambiguous),
  not an error, so the file is valid and the exit status is 0.
- Case folding: the comparison is on the downcased title, and the *downcased*
  form is what the message quotes.
- The open-states restriction: closed tasks do not participate, because a ref
  never resolves to them by default.

## What a correct implementation must do

Warn, exit 0, and still print the `ok — …` summary line with the warning count
in parentheses. A port that treats any diagnostic as a failure gets the exit
status wrong here.

## Recorded `tasks check` outcome

```console
$ tasks check
warn   line 8: duplicate open title "replace the bathroom bulb" (lines 3, 8) — fuzzy refs will be ambiguous
ok — 4 tasks parsed, no structural errors (1 warning)
exit 0
```
