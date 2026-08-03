# Go translation — nested writer

Translation commit: pending (this file is committed with the implementation).

`go/internal/record/emit.go` now applies the Ruby `NESTED_KEY_ORDER` rule
before top-level omission. `scheduled_time` and `deadline_time` retain only
`local`, `timezone`, and `fold`; `delegation` retains its declared keys followed
by unknown keys in parsed source order. Empty canonical nested objects are then
omitted, while non-object values remain unchanged for the future Check slice.

The direct Go probe mirrors the Ruby oracle's five cases. Its output matched
the captured Ruby observations after canonical JSON comparison; the observable
`dumped` JSONL strings themselves are compared exactly by the record tests.

```sh
(cd go && go test ./internal/record ./cmd/format-nested-key-order-probe)
(cd go && go vet ./internal/record ./cmd/format-nested-key-order-probe)
(cd go && TZ=UTC go run ./cmd/format-nested-key-order-probe \
  ../porting/runners/cases/format-nested-key-order-direct.jsonl \
  > /tmp/format-nested-go.jsonl)
diff -u <(jq -S . porting/evidence/format-nested-key-order/ruby-direct/observations.jsonl) \
  <(jq -S . /tmp/format-nested-go.jsonl)
```

The focused implementation tests cover temporal ordering and unknown-key drop,
empty nested omission, delegation forward compatibility, `false`/`0`
preservation, and malformed non-object pass-through. Medium-tier property
testing plus independent source-fidelity and Go-idiom reviews remain for the
next ticks; this commit is not ready for approval.
