# malformed/wrong-key-order

One record whose keys are serialized in a scrambled order
(`title, state, id, parent, type, tags`) instead of the canonical
`Format::KEY_ORDER`.

## What it exercises

- The canonical key order documented in `docs/conventions.md` as an invariant
  "the tooling relies on".
- **A coverage gap in `tasks check`.** `Check` operates on parsed hashes and
  never inspects key order, so this file lints clean.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 3 tasks parsed, no structural errors
exit 0
```

## Finding

`tasks check` passes a store that violates a documented invariant. This is
recorded, not fixed — the fixture exists precisely to pin the current behavior.

It matters for the port in two ways:

1. A port must **also** pass this file. Adding key-order validation would be a
   behavior change, not a fidelity improvement.
2. Key order is nonetheless load-bearing on the **write** side: the first
   command that rewrites this record must emit it in canonical order, and the
   byte comparison in `porting/compare/files` is where that gets proved. Read
   acceptance and write canonicalization are separate obligations, and this
   fixture is the one that separates them.
