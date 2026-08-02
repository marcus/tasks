# `store-snapshot-items` Ruby oracle capture

This directory pins the Ruby behavior for manifest slice `store-snapshot-items`
at source revision `87d8cc201410669e5b4ed1987eb44a01946ae92f`. Ruby remains the
oracle; this capture contains no Go result.

`ruby/` is produced from
[`porting/runners/cases/store-snapshot-items.jsonl`](../../runners/cases/store-snapshot-items.jsonl).
It exercises the declared fixture set through read-only `list` surfaces:

- `full-field-matrix` exposes fields and date forms from a lenient live
  snapshot;
- `archive-pair` observes archive-sourced items explicitly;
- `wrong-types` proves readers stay defensive until structural validation
  reports malformed values;
- `mid-write-torn-file` proves an invalid trailing line does not discard prior
  parsed records; and
- `empty-store` pins the meta-only boundary.

The runner cannot observe in-process snapshot identity, freezing, or the
deliberately locked live/archive capture. Those direct domain boundaries are
instead pinned by the focused Ruby tests below. In particular, the pre/post
archive-sweep coherence test drives a simulated archive transition; it does not
claim that archive mutation is ported.

## Focused Ruby characterization

```sh
ruby test/test_store.rb -n '/test_parse_finds_all_items|test_parse_reads_priority_tags_and_dates|test_parse_reads_closed_date|test_closed_is_per_record_parent_and_child|test_read_snapshot_cannot_mix_pre_sweep_live_with_post_sweep_archive/'
# 5 runs, 19 assertions, 0 failures, 0 errors, 0 skips

ruby test/test_tree.rb -n '/test_read_snapshot_remains_coherent_and_immutable_across_store_reload/'
# 1 runs, 20 assertions, 0 failures, 0 errors, 0 skips

ruby test/test_check.rb -n '/test_non_string_id_reports_error_without_raising/'
# 1 runs, 5 assertions, 0 failures, 0 errors, 0 skips

ruby test/test_schema_v2.rb -n '/test_checked_read_refuses_a_v1_store_and_names_no_migration/'
# 1 runs, 6 assertions, 0 failures, 0 errors, 0 skips
```

These establish the direct contract the Go slice must retain: Item fields are
defensively coerced (`id` to a string, non-array tags to an empty array, and
unparseable dates to `nil`); each record owns its own closed date; snapshots,
their records, items, tree, and derived indices are immutable; a held snapshot
does not change after reload; live and archive are captured under one lock; and
an unsupported schema produces a checked-read refusal without a snapshot.

## Verification

```sh
porting/evidence/capture --out porting/evidence/store-snapshot-items/ruby \
  --cases porting/runners/cases/store-snapshot-items.jsonl \
  --work /tmp/tasks-store-snapshot-items
porting/compare/validate porting/evidence/store-snapshot-items/ruby
```

The next step is a **medium-risk translation** by a different agent. It must
implement only `internal/store` snapshot construction against this capture,
then add property tests and obtain separate source-fidelity and Go-idiom
reviews before seeking independent approval. It must not turn lenient reads
into eager validation, invent a mixed live/archive snapshot, or treat a Go
output as an expectation.
