# adversarial/stale-lock-sidecar

A healthy store with an empty `.tasks.jsonl.lock` sidecar already on disk and no
process holding it — what a `kill -9` during a mutation leaves behind.

## What it exercises

- The lock sidecar's name and placement: `.<basename>.lock` beside the
  **symlink-resolved** live file, so two spellings of one file lock in common.
- That the lock is `flock`-based and therefore **advisory and
  kernel-released**: the file's existence carries no state, and the kernel drops
  the lock when the owning process dies.
- That the sidecar is opened `RDWR|CREAT` and never unlinked, so it persists
  between runs by design.

## Recorded behavior

```console
$ tasks capture "Capture while a stale lock sidecar sits beside the store"
INBOX Capture while a stale lock sidecar sits beside the store
exit 0
```

No wait, no timeout, no error. The sidecar is reused in place and remains after
the command.

## What a correct implementation must do

Use a kernel-released advisory lock (`flock` / `LockFileEx`), never a
lock-file-exists protocol. A port that treated the presence of this file as a
held lock would deadlock on a store that is perfectly available — and would need
stale-lock timeouts and break-in logic that Ruby does not have and does not need.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 3 tasks parsed, no structural errors
exit 0
```
