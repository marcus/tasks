# adversarial/stale-revision

A store plus `revisions.json`: per-task revision tokens captured **before** two
edits, alongside the tokens the same tasks carry now. `store/` holds the
post-edit state, so every token marked `"changed": true` is genuinely stale
against the shipped bytes.

## What it exercises

- The three-component task revision `v1.<own>.<location>.<lifecycle>`, each part
  a SHA-256 hex digest, and the rule that different mutations invalidate
  different components. Two of the three tasks were edited; the third's token is
  still current, which is what makes the fixture a *test* rather than a
  tautology.
- The `If-Match` precondition on the HTTP surface (`PATCH`/`DELETE` of a task),
  which is where revisions are user-visible; the CLI captures a revision
  internally for `delete` but never accepts one from the user.
- `expected_revision` parsing: a token that is not `v1.` + three 64-hex parts is
  `:invalid` (a malformed precondition), which is a different outcome from a
  well-formed but stale one.
- The store-level revision `s1.<sha256>` over the concatenated live and archive
  bytes, which is what the API returns as `meta.store_revision`.

## What a correct implementation must do

Recompute the same tokens from the same bytes — this is a byte-exact obligation,
not merely a semantic one — refuse a stale precondition with the conflict
outcome (HTTP 412), and distinguish stale from malformed.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 3 tasks parsed, no structural errors
exit 0
```

The store is healthy by design: the conflict lives in the token, not the file.
