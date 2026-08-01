# adversarial/journal-foreign-org

A real, internally consistent journal whose `index.json` names a **different**
store path (`/home/someone-else/tasks/tasks.jsonl`). Unlike the other two
journal fixtures, this one ships a literal `index.json` rather than a template —
the wrong path is the payload.

## What it exercises

- The journal's identity guard: the directory is keyed by
  `sha256(realpath(tasks.jsonl))[0,16]`, and the index *also* records the
  canonical org path. A 16-hex prefix collision, a restored backup, or a copied
  home directory can put someone else's history at your key.
- The guard's severity: a mismatched `org` invalidates the **entire** history
  rather than replaying another store's states over your file.
- That the journal is keyed on the symlink-resolved path, so a fixture's journal
  is not portable byte-for-byte between copies.

## Recorded behavior

```console
$ tasks undo
nothing to undo
exit 1
```

## What a correct implementation must do

Discard the history silently and continue. Restoring blobs from a journal that
describes a different file would overwrite the user's store with a stranger's
content — the worst failure mode in the system, and the reason the guard is a
hard equality check on the canonical path rather than a heuristic.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 3 tasks parsed, no structural errors
exit 0
```
