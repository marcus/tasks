# valid/duplicate-closed-titles

The negative half of the duplicate-title rule: repeated titles that must **not**
warn. `malformed/duplicate-open-titles` holds the positive half.

## What it exercises

- The open-states restriction on the duplicate-title warning. `Check` collects a
  title only when the record's state is in `OPEN_STATES` (`INBOX/TODO/NEXT/
  WAITING`), so three closed tasks sharing "Rotate the tyres" (two `DONE`, one
  `CANCELLED`) produce nothing.
- `PROPOSED` is not an open state either: two proposals sharing "Book the ferry"
  are silent for the same reason.
- The one-open-carrier boundary: a fourth "Rotate the tyres" *is* open, but a
  group of one never warns. A port that collected every title regardless of
  state would warn here on a group of four.

## What a correct implementation must do

Report no errors and no warnings, and exit 0.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 6 tasks parsed, no structural errors
exit 0
```
