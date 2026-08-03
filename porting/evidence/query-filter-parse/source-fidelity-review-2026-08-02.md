# `query-filter-parse` independent source-fidelity review

Reviewed `port/query-filter-parse` at
`b43e7f9e840f61efcf3f689181af3fcee8d1d3b0` against the pinned Ruby source
`87d8cc201410669e5b4ed1987eb44a01946ae92f:lib/tasks/task_queries.rb`.

## Findings

### High — `NewFilter` accepts constructor values Ruby rejects

- Go location: `go/internal/query/filter.go`, `NewFilter`
- Ruby location: `lib/tasks/task_queries.rb`, `Tasks::TaskFilter#initialize`
- Evidence: Ruby uses `scope.to_s.to_sym`, so `scope: "OPEN"` is not the
  supported `:open` value. It also preserves a present empty `priority` or
  `state` long enough for vocabulary validation. Go lowercases scope and uses
  the empty string both as the zero value and as “not supplied.” The following
  direct cases therefore disagree:

  | kwargs | Ruby | Go |
  | --- | --- | --- |
  | `{"scope":"OPEN"}` | `unknown task scope: OPEN` | accepted as `open` |
  | `{"priority":""}` | `priority must be A, B, C, or none` | accepted as absent |
  | `{"state":""}` | state-vocabulary error | accepted as absent |

- Exact correction: preserve omitted-versus-present constructor inputs and
  validate a supplied scope without lowercasing it. Add all three cases to the
  Ruby oracle corpus before repairing Go; do not bless the current Go result.

### Medium — the translated value does not preserve Ruby's immutability

- Go location: `go/internal/query/filter.go`, `Filter`, `ParsedFilter`, and
  `copyStrings`
- Ruby location: `lib/tasks/task_queries.rb`, `Tasks::TaskFilter#initialize`
  and `Tasks::TaskFilter::Parsed#initialize`
- Evidence: Ruby duplicates and freezes every string collection, freezes the
  filter, and freezes the parsed wrapper. Go copies input slices once, but
  exports every scalar and slice field. A caller can mutate the filter after
  construction or mutate `Contexts`, `Tags`, and `Text` in place. The Go
  comment calling `Filter` immutable is therefore an unenforced contract.
- Exact correction: make constructed filter state private and expose read-only
  accessors, returning copies for slice values; likewise prevent mutation of
  the parsed result. Add tests that mutate constructor inputs and accessor
  results and prove the constructed value is unchanged.

### Medium — the oracle omits translated delegation branches and masks empty slices

- Go locations: `go/internal/query/filter.go`, `ParseCLI` and `NewFilter`;
  `go/cmd/query-filter-parse-probe/main.go`, `nonNil`
- Ruby locations: `lib/tasks/task_queries.rb`, `Tasks::TaskFilter.parse_cli`;
  `test/test_task_queries.rb`,
  `test_delegation_filter_parsing_and_scope_rules`
- Evidence: the Go slice translates `--delegated`, `--agent-ready`, their
  mutual exclusion, and the open-scope restriction, but none of the 17 oracle
  cases exercises those branches and the manifest does not claim the Ruby test
  that does. Separately, `nonNil` converts nil Go slices to empty arrays in the
  probe, hiding that a default Go `Filter` has nil `Contexts`, `Tags`, and
  `Text` while Ruby constructs three empty arrays.
- Exact correction: add accepted and rejected delegation cases and the existing
  Ruby delegation parser test to this slice's manifest/evidence. Stop
  normalizing nil slices in the probe; construct owned non-nil empty slices in
  `NewFilter` so the typed value itself matches the Ruby observation.

## Reproduction

The committed corpus remains green but does not reach the findings:

```console
$ porting/evidence/query-filter-parse/conformance
query-filter-parse direct conformance: 17/17 cases matched
$ ruby test/test_task_queries.rb
25 runs, 198 assertions, 0 failures, 0 errors, 0 skips
$ cd go && go test ./... && go vet ./... && go test -race ./...
ok
$ ruby porting/manifest-issues validate
ok: 144 slices, 9 campaigns, every source path and oracle test resolves
```

The three constructor mismatches were reproduced by feeding the kwargs shown
above to both direct probes. This source-fidelity review fails. A translation
repair, expanded Ruby oracle capture, fresh differential evidence, and a new
independent source-fidelity review are required. The separate Go-idiom review
has not been performed in this iteration.
