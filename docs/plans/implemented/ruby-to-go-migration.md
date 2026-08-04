# Ruby-to-Go migration

Tasks completed its in-place Ruby-to-Go migration on 2026-08-04. The Go CLI,
TUI, API, JSONL store, journal, and merge driver were proven against copied real
data and used on all known consumer machines before the old implementation was
removed from `main`.

The final repository tree containing both implementations is preserved by the
annotated tag `ruby-final-2026-08-04`, which resolves to commit `c0571ca`. That
tag also retains the differential harness, captured evidence, and detailed
delivery plans. It is historical source, not a supported release line.

The first normalized Go release is `v1.0.0`. Schema-v2 JSONL and journal bytes
remain compatible with data written before the release; no data migration was
part of the repository cleanup.
