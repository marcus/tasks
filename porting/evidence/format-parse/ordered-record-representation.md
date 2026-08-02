# Ordered parsed-record representation

Commit: recorded with this handoff.

`internal/record.Record` now retains each parsed object member as an ordered
`Field` list rather than a Go map. This is a representation repair required
before `format-canonical-emit`: Ruby emits forward-compatible unknown keys in
the insertion order of its parsed Hash, while a Go map has no usable ordering.

The parser keeps Ruby's duplicate-key behavior too: the first key position is
retained and the final value replaces the earlier value. The direct probe still
uses a map only as its order-insensitive diagnostic transport; it does not
define the record representation or writer behavior.

Verification on 2026-08-02:

```console
$ go -C go test ./...
ok   tasks-go/internal/record
$ go -C go test -race ./...
ok   tasks-go/internal/record
$ go -C go vet ./...
$ porting/evidence/format-parse/conformance
format-parse direct conformance: 28/28 cases matched
$ ruby porting/manifest-issues validate
ok: 44 slices, 4 campaigns, every source path and oracle test resolves
```

The direct Ruby probe for a duplicated `title` confirmed the retained key
order (`type`, `future_a`, `title`, `future_b`) and the final `title` value.
No Go output was used as an oracle, and the slice remains `characterizing`:
the five existing Ruby diagnostic mismatches still require a source-aware
repair and fresh independent medium-risk reviews.
