# valid/symlinked-store

A healthy store whose `tasks.jsonl` is a **relative symlink** to
`tasks.real.jsonl` beside it. The dotfiles/Dropbox shape: the store lives
somewhere else and the path the tool is given is a link to it.

```text
store/
  tasks.jsonl       -> tasks.real.jsonl     (symlink)
  tasks.real.jsonl                          (the actual bytes)
```

## Why `valid/`

The store parses clean and exits 0. What varies is the *shape of the path*, not
the health of the data.

## Why the link ships as bytes rather than being materialized

git records symlinks natively and `cp -a <fixture>/store/. <copy>/` preserves
them, so unlike a permission bit this needs no help from the runner. The link
target is **relative and inside `store/`** on purpose: an absolute target would
point outside the copy, the write would land outside the observed tree, and
`files.after` would record nothing at all.

`porting/runners/README.md` already accounts for this fixture in two places: the
`root_sha256` definition says "a symlink contributes `-` in place of its digest",
and `files.before`/`after` record a symlink target per entry. Nothing about this
fixture needs a protocol change.

## What it exercises

`Atomic.write` calls `Atomic.resolve(path)` first, and `resolve` follows a
symlink to `File.realpath` before choosing where the temp sibling goes and what
the rename lands on. The comment in `atomic.rb` states the stakes: "a rename over
a symlink would replace the link itself, orphaning a Dropbox/dotfiles setup".

Three things a naive atomic write gets wrong here, all observable:

1. **The link survives.** `tasks.jsonl` must still be a symlink afterwards, with
   the same target. A `rename(tmp, "tasks.jsonl")` replaces the link with a
   regular file — the store is then correct but the user's setup is silently
   severed, and `tasks.real.jsonl` is stale forever.
2. **The bytes land on the target.** `tasks.real.jsonl` gets the new content.
3. **The temp sibling is named after the resolved target**, not after the link:
   `Atomic.write` builds it from `File.basename(target)`, so a crash mid-write
   leaves `.tasks.real.jsonl.<pid>.<tid>.tmp`, not `.tasks.jsonl.<pid>.<tid>.tmp`.
   `adversarial/mid-write-leftover-tmp` pins the unlinked spelling; this fixture
   is the only place the resolved spelling is reachable.

The lock sidecar is a fourth observable and it follows the link too, by a
different route: `Store#lock_path` resolves through `Journal.canonical` rather
than through `Atomic.resolve`, and lands on `.tasks.real.jsonl.lock`. Two
sidecars, two independent resolution paths, one required answer — a port that
names either of them from the *configured* path leaves `.tasks.jsonl.lock` or
`.tasks.jsonl.<pid>.<tid>.tmp` in the tree, and the runner records every file in
the copy, so both show up as deltas.

## What a correct implementation must do

Follow the link, replace the target, leave the link. Exit 0.

## Recorded Ruby outcomes

Under the pinned environment in the corpus README, plus the runner's
`TASKS_PIN_NOW=2026-03-14T15:09:26Z` and `TASKS_PIN_IDS=bbbb0001`.

### `tasks check` — exit 0

```console
$ tasks check
ok — 1 task parsed, no structural errors
exit 0
```

### `tasks capture "Wrote through the symlink"` — exit 0

```console
$ tasks capture "Wrote through the symlink"
INBOX Wrote through the symlink
exit 0
```

The tree afterwards — `tasks.jsonl` is still a symlink, same target, and the
inode it points at was replaced:

```console
$ ls -a
.tasks.real.jsonl.lock
tasks.jsonl -> tasks.real.jsonl
tasks.real.jsonl
```

Note the lock sidecar's name: `.tasks.real.jsonl.lock`, not `.tasks.jsonl.lock`.

`tasks.real.jsonl`:

```json
{"type":"meta","version":2}
{"type":"section","id":"25000001","title":"Inbox"}
{"type":"task","id":"bbbb0001","parent":"25000001","state":"INBOX","title":"Wrote through the symlink","body":"Captured [2026-03-14].","updated":"2026-03-14T15:09:26Z#fixture"}
{"type":"section","id":"25000002","title":"Next Actions"}
{"type":"task","id":"25000003","parent":"25000002","state":"NEXT","title":"Confirm the symlinked store is followed","tags":["@computer"]}
```

## Not covered here

`Atomic.resolve` has a third branch this fixture does not reach: a **dangling**
symlink, followed via `File.readlink` + `File.expand_path` to the path it
intended rather than being overwritten into a regular file — the "target on a
briefly-unmounted volume" case in the source comment. A dangling link cannot
carry a store to read, so it is not a `check` fixture; it would be a
create-on-write case for a later slice. Named here so the omission is visible.
