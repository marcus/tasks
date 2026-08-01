# adversarial/mid-write-leftover-tmp

A healthy store with an orphaned atomic-write temp file beside it:
`.tasks.jsonl.48211.7040.tmp`, containing the store contents plus a record cut
off mid-key — what `Atomic.write` leaves if the process dies between the write
and the rename.

## What it exercises

- The temp-file naming contract: `.<basename>.<pid>.<thread id>.tmp` in the same
  directory as the target, so the rename stays on one filesystem.
- That the real store is untouched: rename is atomic, so a crash before it is
  invisible to readers.
- That nothing collects orphaned temps — a port must not "helpfully" clean the
  directory, and must not mistake a temp file for a store.

## Recorded behavior

```console
$ tasks list
(3 tasks, as expected)
exit 0

$ tasks capture "New capture after the crash"
INBOX New capture after the crash
exit 0
```

After the capture the orphan `.tasks.jsonl.48211.7040.tmp` is **still present**,
untouched, beside the new `.tasks.jsonl.lock`. The new write used its own
pid/thread-derived temp name and cleaned up after itself.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 3 tasks parsed, no structural errors
exit 0
```

`check` reads `tasks.jsonl` only; the temp file is not part of the store.
