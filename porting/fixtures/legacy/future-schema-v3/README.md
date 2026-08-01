# legacy/future-schema-v3

`{"type":"meta","version":3}` — a schema version from the future.

## What it exercises

- The other end of the version guard: unknown-newer, as opposed to
  known-older (`schema-v1`).
- The distinction between "migrate me" and "I cannot read this at all". Version
  1 is migratable; version 3 is not.

## What a correct implementation must do

Report the same "unsupported meta version" error `check` emits, refuse to
operate, and — critically — refuse to *write*. A binary that treated an unknown
future version as writable would silently downgrade a newer store.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 1: unsupported meta version 3 (expected 2)
1 error(s), 0 warning(s)
exit 1
```
