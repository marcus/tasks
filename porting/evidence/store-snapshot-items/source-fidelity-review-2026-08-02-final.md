# `store-snapshot-items` independent final source-fidelity review

Reviewed `port/store-snapshot-items` at `0546a53a3b5f53214da2fb4a4fbd678163a432dc`, including the post-review repairs `60a3f67` and `58f1213`. No implementation was edited in this reviewing context.

## Finding: structured tag coercion remains divergent

`lib/tasks/store.rb#build_item` uses Ruby's `tags.map(&:to_s)` after
`JSON.parse` has already materialized each JSON value. The Go
`rubyStringJSON` renderer instead preserves the original JSON token stream.
That is observably different for JSON representations that Ruby normalizes:

| JSON tag value | Ruby `to_s` | Go output |
| --- | --- | --- |
| `{"a":1,"a":2}` | `{"a" => 2}` | `{"a" => 1, "a" => 2}` |
| `1e3` | `1000.0` | `1e3` |
| `-0` | `0` | `-0` |
| `1.2300` | `1.23` | `1.2300` |

The existing structured-tags differential case covers nesting and member order,
but not duplicate-key or numeric normalization. This is a Go defect, not an
intentional difference: malformed records must remain readable with Ruby's
actual `to_s` result. Repair the renderer (or use the same parsed-value
semantics as Ruby), add these cases to direct Ruby-vs-Go conformance, then
obtain a fresh source-fidelity review. Do not seek approval on this revision.

## Reproduction

The following JSONL task was supplied to the production Ruby and Go snapshot
probes:

```json
{"type":"task","id":"duplicate","state":"TODO","title":"x","tags":[{"a":1,"a":2},1e3,-0,1.2300,"\\u2028"]}
```

Ruby returned tags `[{"a" => 2}, 1000.0, 0, 1.23, U+2028]`; Go returned
`[{"a" => 1, "a" => 2}, 1e3, -0, 1.2300, U+2028]`.

The otherwise-required checks passed on the reviewed revision:

```console
$ cd go && go test ./... && go test -race ./... && go vet ./...
$ cd .. && porting/evidence/store-snapshot-items/conformance
store-snapshot-items direct conformance: 7/7 cases matched
$ ruby porting/manifest-issues validate
ok: 144 slices, 9 campaigns, every source path and oracle test resolves
```
