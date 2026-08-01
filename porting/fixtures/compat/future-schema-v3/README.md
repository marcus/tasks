# compat/future-schema-v3

`{"type":"meta","version":3}` — a schema version from the future.

## What it exercises

- The strict half of the version contract: unknown keys at the current version
  are tolerated (`forward-compat-unknown-keys`), an unknown schema *version* is
  not.
- The single rule that replaced the old v1/v3 split: any declared `meta` version
  other than `Format::VERSION` is refused identically, older or newer. There is
  no migration path in either direction — `tasks migrate` was removed in
  td-09f7de once Marcus confirmed no schema-v1 store exists.

## What a correct implementation must do

Report the same "unsupported meta version" error `check` emits, refuse to
operate, and — critically — refuse to *write*. A binary that treated an unknown
future version as writable would silently downgrade a newer store. Every surface
refuses: reads return `unsupported_schema`, mutations return the same status
with nothing written, and the API answers `503 unsupported_schema_version`.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 1: unsupported meta version 3 (expected 2)
1 error(s), 0 warning(s)
exit 1
```
