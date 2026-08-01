# legacy/org-pre-jsonl

The pre-JSONL org-mode file, placed at the path the tooling reads today.

Before `7d70cff` the store was an Org file. `15ad280` added a self-contained
`tasks migrate` that converted org → JSONL, and `e5c505c` **removed that
importer** once the data repository was cut over. `tasks migrate` today means
only "schema v1 → v2"; there is no code path in this repository that can read
org.

## What it exercises

- What a user with a genuinely pre-cutover data directory sees now: not a
  migration offer, but one `invalid JSON` error per line.
- The absence of a conversion path — recorded deliberately so a port does not
  invent one.

## What a correct implementation must do

Nothing special. Org is not a supported input; the generic per-line JSON
diagnostics are the correct and complete behavior. Do **not** port an org
reader — there is none to port.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 1: invalid JSON: unexpected character: '*' at line 1 column 1
error  line 2: invalid JSON: unexpected character: '**' at line 1 column 1
error  line 3: invalid JSON: unexpected character: 'Captured' at line 1 column 4
error  line 4: invalid JSON: unexpected character: '*' at line 1 column 1
error  line 5: invalid JSON: unexpected character: '**' at line 1 column 1
error  line 6: invalid JSON: unexpected character: 'DEADLINE:' at line 1 column 4
error  line 7: invalid JSON: unexpected character: '**' at line 1 column 1
error  line 8: invalid JSON: unexpected character: 'SCHEDULED:' at line 1 column 4
error  line 9: invalid JSON: unexpected character: '*' at line 1 column 1
error  line 10: invalid JSON: unexpected character: '**' at line 1 column 1
error  line 11: invalid JSON: unexpected character: '***' at line 1 column 1
error  line 12: invalid JSON: unexpected character: '***' at line 1 column 1
error  line 13: invalid JSON: unexpected character: 'CLOSED:' at line 1 column 5
13 error(s), 0 warning(s)
exit 1
```

The `at line 1 column N` suffixes come from the JSON parser, which sees each
JSONL line as its own document — so every message says "line 1" regardless of
the file line. A port must reproduce that quirk, not correct it.
