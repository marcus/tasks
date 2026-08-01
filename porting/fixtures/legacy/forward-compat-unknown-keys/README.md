# legacy/forward-compat-unknown-keys

A version 2 store written by a **newer** binary: it carries top-level keys and a
nested `delegation` key this version does not know.

Forward compatibility is the same mechanism legacy support rests on — one
binary reading bytes another binary's schema produced — which is why it lives in
this class rather than in `malformed/`.

## What it exercises

- Unknown top-level keys (`energy`, `review_after`) and an unknown delegation
  key (`budget_tokens`).
- The warn-and-preserve contract: unknown keys are a hazard, not an error.
- Emission order for unknown keys: known keys in `Format::KEY_ORDER` first, then
  unknown keys in their original insertion order — at the top level and inside
  `delegation`.

## What a correct implementation must do

Warn once per unknown key, keep the value, and re-emit it in the same relative
position after any write. Dropping an unknown key silently deletes a newer
binary's data; failing the file locks the user out of their own store.

## Recorded `tasks check` outcome

```console
$ tasks check
warn   line 3: unknown key "energy"
warn   line 3: unknown delegation key "budget_tokens"
warn   line 4: unknown key "review_after"
ok — 2 tasks parsed, no structural errors (3 warnings)
exit 0
```
