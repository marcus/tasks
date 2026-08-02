# `store-snapshot-items` independent Go-idiom review

Reviewed `port/store-snapshot-items` at `58f12137f22a1a8e4f22e17e722095e0a0326516` without implementation edits.

## Finding

Pass. The read-model boundary is small and explicit: `Snapshot` owns its
unexported slices and indexes, while accessors return defensive copies rather
than leaking implementation state. `ReadLocker` is the narrow dependency seam
appropriate for a lock implementation owned by a later persistence slice, and
the `Unlocked` fixture adapter makes the non-concurrent path explicit.

Ruby-specific compatibility is contained in private helpers. In particular,
the ordered `rubyValue` representation is justified by an observable
malformed-tag contract: `encoding/json` cannot reproduce Ruby's object
spelling or source member order. The ordinary Go model remains conventional:
typed source constants, `(value, ok)` lookup, wrapped parse values only where
the incomplete schema requires `any`, and `time.Time` values for successfully
parsed ISO dates. Tests are focused, including a fuzz/property-style
accessor-isolation check, and no unnecessary generalization or public surface
was introduced.

No Go-idiom divergence requiring correction was found in this slice boundary.

## Reproduction

```console
$ cd go && gofmt -d internal/store/snapshot.go internal/store/capture.go internal/store/snapshot_test.go internal/store/capture_test.go cmd/store-snapshot-probe/main.go
$ go test ./...
ok   tasks-go/internal/record
ok   tasks-go/internal/store
$ go test -race ./...
ok   tasks-go/internal/record
ok   tasks-go/internal/store
$ go vet ./...
$ cd .. && porting/evidence/store-snapshot-items/conformance
store-snapshot-items direct conformance: 7/7 cases matched
$ ruby porting/manifest-issues validate
ok: 144 slices, 9 campaigns, every source path and oracle test resolves
$ ruby test/test_store.rb -n '/test_parse_finds_all_items|test_parse_reads_priority_tags_and_dates|test_parse_reads_closed_date|test_closed_is_per_record_parent_and_child|test_read_snapshot_cannot_mix_pre_sweep_live_with_post_sweep_archive/'
5 runs, 19 assertions, 0 failures, 0 errors, 0 skips
$ ruby test/test_tree.rb -n '/test_read_snapshot_remains_coherent_and_immutable_across_store_reload/'
1 runs, 20 assertions, 0 failures, 0 errors, 0 skips
$ git diff --check 14d7035..HEAD
```

This is the Go-idiom half of the required medium-risk split review. A repaired
slice needs a fresh independent source-fidelity review before independent
approval; the earlier source-fidelity review found defects that were corrected
in commits `60a3f67` and `58f1213`, so it cannot certify this final revision.
