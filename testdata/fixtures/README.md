# Store fixtures

Sanitized JSONL stores used by Go package and command tests. Tests always copy
fixtures into a temporary directory before running a mutation.

- `valid/` contains accepted schema-v2 stores and read-ordering cases.
- `malformed/` contains inputs that parsing, checking, or repair must refuse.
- `adversarial/` contains interrupted-write, locking, journal, and claim cases.
- `compat/` contains schema-gate and forward-compatible-field cases.

These are product regression fixtures, not live task data. The retired
cross-language harness and its provenance are preserved by the annotated Git
tag `ruby-final-2026-08-04`.
