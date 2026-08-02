# `store-snapshot-items` fresh source-fidelity review (post float repair)

Reviewed `port/store-snapshot-items` at `e0d88bf2d7502a09271bef98db459b5cf145ab5e`
in a fresh context (session `ses_d5f6e1`). No implementation file was edited here;
this review adds evidence only.

## The reviewed repair is correct

`rubyNumberString`'s Float branch now matches Ruby's `Float#to_s` across the
boundary and overflow corpus the prior review demanded. Differentially probed
production Ruby `build_item` (`porting/runners/ruby/store-snapshot-probe`)
against `go run ./cmd/store-snapshot-probe` on tags:

```
1e-400 -1e-400 5e-324 1e15 1e-4 1e-5 -0.0 0.1 100 1.0E2 3.0e+2 12345678901234567890
-0 0 1e29 1e309 -1e309 1.7976931348623157e308 1.7976931348623159e308
2.2250738585072011e-308 0e0 1e0 1E2 -1.5e-7 9007199254740993
0.30000000000000004 1e-323
```

Every value matched, including the fixed/scientific switch at `1e15` and
`1e-4`, two-digit exponents, signed zero, underflow to `0.0`/`-0.0`, overflow to
`Infinity`/`-Infinity`, and arbitrary-precision integer tokens. Scalar
passthrough (`state`, `priority`, `title`, `recur`, `lead`, `lead_skip`),
non-array `tags`, `null`/`true`/`false` tags, and per-record `closed` also
matched exactly.

**Verdict: not approvable.** Two pre-existing source-fidelity divergences
remain in this slice's own coercion contract.

## Finding 1 — structured `id` is rendered as JSON, not Ruby `to_s`

`lib/tasks/store.rb:1946` builds the id with `rec["id"]&.to_s`, the same
coercion the prior review corrected for tags. `go/internal/store/snapshot.go:116`
still calls `rubyString(value(id))`, whose composite branch falls through to
`json.Marshal` — JSON spelling, and (because `value` decodes objects into
`map[string]any`) sorted keys instead of source member order.

| record | Ruby | Go |
| --- | --- | --- |
| `{"type":"task","id":[1,"a",{"b":2}],...}` | `[1, "a", {"b" => 2}]` | `[1,"a",{"b":2}]` |
| `{"type":"task","id":{"z":1,"a":[true,null]},...}` | `{"z" => 1, "a" => [true, nil]}` | `{"a":[true,null],"z":1}` |

Scalar ids (string, bool, float, empty string) match. This is a Go defect, not
an intentional difference: the slice's own oracle test
`test/test_check.rb#test_non_string_id_reports_error_without_raising` pins that a
non-string id is coerced and reported, and a structured id also becomes an index
key in `liveByID`/`archiveByID`.

Correction: render the id through `rubyStringJSON(fields["id"])` (the renderer
already built for tags) rather than `rubyString(value(id))`, and add a
structured-id case to `porting/evidence/store-snapshot-items/conformance`.

## Finding 2 — `to_date` accepts far more than `YYYY-MM-DD`

`lib/tasks/store.rb:1953` uses `Date.iso8601(str)`, which accepts every ISO 8601
date form plus a leading date in a datetime string.
`go/internal/store/snapshot.go:167` uses `time.Parse("2006-01-02", text)`, which
accepts only the extended calendar date. Differential probe on `scheduled`
(identical for `deadline` and `closed`, all three share `isoDate`):

| `scheduled` | Ruby | Go |
| --- | --- | --- |
| `"20260802"` (basic format) | `2026-08-02` | `nil` |
| `"2026-08-02T10:30:00+02:00"` | `2026-08-02` | `nil` |
| `"2026-W31-1"` (week date) | `2026-07-27` | `nil` |
| `"2026-215"` (ordinal date) | `2026-08-03` | `nil` |
| `"2026-08-02 "` (trailing space) | `2026-08-02` | `nil` |
| `"2026-8-2"` | `nil` | `nil` |
| `"2026-02-30"` | `nil` | `nil` |

Silently dropping a date a Ruby reader accepts is user-observable on every
`scheduled`/`deadline`/`closed` surface, and this slice owns the
"unparseable date to nil" rule. Go defect.

Correction: implement `Date.iso8601`'s accepted grammar (extended and basic
calendar dates, ordinal dates, week dates, and the date part of a datetime,
with the same `Date::Error` rejections) behind `isoDate`, and add these cases to
direct conformance.

## Checks run on the reviewed revision

```console
$ cd go && go test ./... && go test -race ./... && go vet ./...
$ cd .. && porting/evidence/store-snapshot-items/conformance
store-snapshot-items direct conformance: 9/9 cases matched
$ ruby porting/manifest-issues validate
ok: 144 slices, 9 campaigns, every source path and oracle test resolves
```

The existing conformance corpus passes because it contains no structured id and
no non-extended date string; both findings are missing-coverage as well as
defects.
