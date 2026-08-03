# Property testing — nested writer

Property test commit: pending (this file is committed with the test).

`TestNestedCanonicalizationAcrossPermutations` exhaustively checks the writer's
ordering properties instead of sampling a single convenient source order:

- all 120 permutations of three temporal declared keys and two unknown keys
  produce the declared temporal order and drop both unknown keys;
- all 720 permutations of the six declared delegation keys produce
  `DelegationKeyOrder`; and
- all 120 permutations of two declared delegation keys and three unknown keys
  keep those unknown keys in their relative parsed source order after the
  declared prefix.

The test ran clean alongside the existing direct Ruby differential and the
record package's vet and race checks:

```sh
(cd go && go test ./internal/record ./cmd/format-nested-key-order-probe)
(cd go && go vet ./internal/record ./cmd/format-nested-key-order-probe)
(cd go && go test -race ./internal/record)
TZ=UTC go run ./go/cmd/format-nested-key-order-probe \
  porting/runners/cases/format-nested-key-order-direct.jsonl \
  > /tmp/format-nested-go.jsonl
diff -u <(jq -S . porting/evidence/format-nested-key-order/ruby-direct/observations.jsonl) \
  <(jq -S . /tmp/format-nested-go.jsonl)
```

All commands exited zero; the differential emitted no output. This finishes
the medium-tier property-test obligation only. Independent source-fidelity and
Go-idiom reviews are still required before approval.
