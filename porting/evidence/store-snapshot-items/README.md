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

## Go translation progress

The initial `internal/store` projection is present on the slice branch. Its
accessor boundary deep-copies every JSON-valued Item field, including malformed
objects and arrays that ordinary reads must carry until Check reports them. The
property-style fuzz test mutates nested values returned by an accessor and
proves a later read of the held snapshot retains the original values.

Verified on this branch:

```sh
(cd go && go test ./... && go test -race ./... && go vet ./...)
```

The remaining medium-risk work is a Go probe that can be differentially
compared to the Ruby observations above, then separate source-fidelity and
Go-idiom reviews. It must not turn lenient reads into eager validation, invent
a mixed live/archive snapshot, or treat a Go output as an expectation.

## File-backed capture step

`internal/store.Capture` now reads the live file and optional archive under
one caller-provided `ReadLocker` acquisition and builds the existing immutable
projection from those exact descriptor reads. The lock is deliberately an
interface rather than a new lock implementation: persistence and locking are a
later high-risk slice. `Unlocked` is limited to fixtures and single-reader
callers. A missing archive remains empty; a missing live store and lock failure
are returned to the caller; parse defects remain lenient so valid preceding
records can still be read.

Verified on this branch:

```sh
(cd go && go test ./... && go test -race ./... && go vet ./...)
```

## Direct differential conformance

The full runner compares CLI observations, but its `list` projection depends
on later query and CLI slices. Until those surfaces exist, this slice compares
the Store-owned Item projection directly. The Ruby probe uses Store's actual
`build_item` coercion seam; the Go probe uses `store.Capture` and its immutable
snapshot accessors. Both emit only the fields owned here, for each live and
archive source, so later rendering semantics cannot mask a read-model defect.

```sh
porting/evidence/store-snapshot-items/conformance
# store-snapshot-items direct conformance: 5/5 cases matched
```

This is differential evidence for Item field coercion and source separation.
Snapshot identity, freezing, and coherent capture remain covered by the Go
tests above; tree/query behavior remains with later slices.

Next: obtain separate independent source-fidelity and Go-idiom reviews.
