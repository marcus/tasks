# valid/interleaved-tags

Four tasks whose `tags` arrays interleave **owned** and **unowned** entries in
every arrangement the tag-slice merge has to survive, plus two records that are
already deferred.

Class: `valid/`. The store is healthy and `check`-clean; what makes it worth a
fixture is the *shape* of its tag arrays, not any breakage. Nothing here is
adversarial, malformed, or version-skewed.

## Why it exists

`Store#patch_tag_slice` splits one `tags` array into three ownerships:

| Slice | Owns | Does not own |
|---|---|---|
| `contexts` | entries starting `@` | plain tags, `defer` |
| `tags` | plain entries | `@contexts`, `defer` |
| `deferred` | the literal `defer` marker | everything else |

Each slice may only rewrite its own entries, and `merge_owned_slice` must put
the replacements back **in the positions the owned entries occupied**, leaving
unowned entries exactly where they were. Every other multi-tag record in the
corpus is `@contexts` first and then plain tags, so the merge could be
implemented as "owned prefix, then keep the rest" and pass the whole corpus.
This fixture makes that implementation fail.

It also carries the only `defer` records in the corpus. Without one,
`patch_deferred` is only provable in its set direction; the `tags.delete` clear
branch and the equal-value no-op branch have no input.

## The records

| id | `tags` | The case |
|---|---|---|
| `c1a2b3d1` | `["@home","urgent","@errands"]` | a plain tag **between** two contexts |
| `c1a2b3d2` | `["alpha","@computer","beta"]` | a context **between** two plain tags |
| `c1a2b3d3` | `["@home","defer","research"]` | the defer marker between an owned and an unowned tag |
| `c1a2b3d4` | `["defer"]` | deferred and nothing else; also `owned_count == 0` for both slices |

## Recorded behavior

Driven through `Store#edit_snapshot` + `Store#patch_task!` — the patch protocol
this slice ports — under the pinned environment (`TASKS_TIMEZONE=UTC`,
`TASKS_DEVICE=fixture`, empty `XDG_CONFIG_HOME`, `TASKS_DIR` at a copy). Each
row is a fresh copy of the fixture; `tags` is the array in the rewritten record.

| Patch | Status | Resulting `tags` |
|---|---|---|
| `c1a2b3d1` `contexts: ["@calls"]` | `ok` | `["@calls","urgent"]` |
| `c1a2b3d1` `contexts: ["@a","@b","@c"]` | `ok` | `["@a","urgent","@b","@c"]` |
| `c1a2b3d1` `tags: ["zeta"]` | `ok` | `["@home","zeta","@errands"]` |
| `c1a2b3d2` `contexts: ["@x","@y"]` | `ok` | `["alpha","@x","@y","beta"]` |
| `c1a2b3d2` `tags: ["m"]` | `ok` | `["m","@computer"]` |
| `c1a2b3d3` `deferred: false` | `ok` | `["@home","research"]` |
| `c1a2b3d3` `deferred: true` | `no_change` | unchanged; **zero bytes written** |
| `c1a2b3d3` `tags: ["research"]` | `no_change` | unchanged; **zero bytes written** |
| `c1a2b3d3` `tags: ["defer"]` | `invalid` | unchanged; **zero bytes written** |
| `c1a2b3d4` `deferred: false` | `ok` | key removed entirely |
| `c1a2b3d4` `contexts: ["@home"]` | `ok` | `["defer","@home"]` |

Four facts worth stating outright, because each is a place a plausible
implementation diverges:

1. **Shrinking the owned slice drops from the tail, not the head.**
   `["@home","urgent","@errands"]` with `contexts: ["@calls"]` becomes
   `["@calls","urgent"]`: the first owned slot takes the replacement, the second
   owned slot is deleted, and `"urgent"` stays at index 1 — it does **not** slide
   to index 0 first and then get a context appended after it.
2. **Growing the owned slice spills at the last owned slot.**
   `contexts: ["@a","@b","@c"]` gives `["@a","urgent","@b","@c"]` — the surplus
   lands where the final owned entry was, after the unowned `"urgent"`, not at
   the end of a rebuilt list.
3. **`owned_count == 0` appends.** `c1a2b3d4`'s only tag is `defer`, which
   neither slice owns, so setting a context yields `["defer","@home"]` — the
   marker keeps position 0.
4. **Clearing the last tag deletes the key.** `c1a2b3d4` with `deferred: false`
   emits a record with no `tags` key at all (`replace_optional`), not
   `"tags":[]`. That is a byte-level obligation for the port's writer.

`no_change` writes nothing and consumes no undo step; `invalid` (a plain-tags
slice may not contain `defer`) is rejected before any write.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 4 tasks parsed, no structural errors
exit 0
```
