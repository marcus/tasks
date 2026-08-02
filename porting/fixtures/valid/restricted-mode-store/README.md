# valid/restricted-mode-store

A healthy store whose `tasks.jsonl` must be mode **0600** when the
implementation runs against it — the private-store shape, and the case where an
atomic replace silently widens permissions if it is done naively.

## Why `valid/`

Clean store, exit 0. The variable is the inode's mode, not the data.

## The one thing this fixture needs from the runner

**git cannot record `0600`.** It records one bit — executable or not — so a
checked-out `tasks.jsonl` is `0644` and `cp -a` faithfully preserves the wrong
mode. Unlike the symlink in `valid/symlinked-store`, this cannot ship as bytes.

So the fixture declares it, in `perms.json` at the fixture root (the
`<extra>.json` slot in the directory contract):

```json
{
  "chmod": { "tasks.jsonl": "0600" }
}
```

**The copy protocol does not yet apply this.** A runner must, between step 1
(copy) and step 4 (observe `files.before` and compute `root_sha256`), apply every
entry in `perms.json` to the copy — the mode has to be in place *before* the
pristine observation, or `files.before` records `0644` and the comparison asserts
the wrong precondition. `root_sha256` is unaffected either way: it is defined over
path strings and file bytes only, so applying the chmod cannot invalidate a
baseline captured before this fixture existed.

Until that step exists, this fixture's recorded outcome below was produced by
applying the chmod by hand. **Named as a protocol gap, not implemented here** —
`porting/runners/` is another stream's path.

## What it exercises

`Atomic.copy_mode(target, tmp)` — `File.chmod(File.stat(target).mode, tmp)`
between the fsync and the rename. The reason it exists is stated in
`atomic.rb`: "a fresh temp is born at the umask, so a chmod-600 tasks.jsonl
would otherwise silently widen to 644."

That is the whole failure mode, and it is invisible unless the fixture arrives at
0600 and the process umask is permissive. Under the default `umask 022` a
`File.open(tmp, "w")` produces `0644`; without `copy_mode`, the rename installs
that inode and a private store becomes world-readable with no error, no warning,
and no change to a single byte of content. A byte-level comparator cannot see it.
Only `files.after[].mode` can.

`copy_mode` is also deliberately best-effort — it rescues `SystemCallError` and
carries on, so a filesystem that rejects chmod does not turn a working write into
a failure. A port must swallow that error the same way rather than aborting the
write.

## What a correct implementation must do

Leave `tasks.jsonl` at mode `0600` after a mutation that rewrites it. Exit 0.

## Recorded Ruby outcomes

Under the pinned environment in the corpus README, plus the runner's
`TASKS_PIN_NOW=2026-03-14T15:09:26Z` and `TASKS_PIN_IDS=bbbb0001`, with
`chmod 0600 tasks.jsonl` applied to the copy and a process `umask` of `022`.

### `tasks check` — exit 0

```console
$ tasks check
ok — 1 task parsed, no structural errors
exit 0
```

### `tasks capture "Wrote under a 0600 store"` — exit 0, mode preserved

```console
$ stat -f '%Sp' tasks.jsonl
-rw-------
$ tasks capture "Wrote under a 0600 store"
INBOX Wrote under a 0600 store
exit 0
$ stat -f '%Sp' tasks.jsonl
-rw-------
```

## Finding

**The lock sidecar does not inherit the store's mode.** After the mutation the
tree holds `.tasks.jsonl.lock` at `0644` — `Store#with_lock` opens it with an
explicit literal `0o644`, so a store the user deliberately made private acquires
a world-readable sidecar beside it.

The sidecar is empty, so nothing is disclosed by its contents; what leaks is its
existence and its mtime. Recorded, not fixed — but it is a real asymmetry, and a
port that "helpfully" carries 0600 onto the lock file would diverge on
`files.after[].mode` for every mutation fixture in this corpus, not just this one.
