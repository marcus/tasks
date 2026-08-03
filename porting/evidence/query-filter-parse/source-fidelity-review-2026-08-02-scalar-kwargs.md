# Source-fidelity review — query-filter-parse @ f0f8dc1

Reviewer session: ses_992094 (independent of the implementer ses_78b97c and of
every prior repair session). Review only — no source file was edited.

**Verdict: FAILED.** The scalar-collection repair in `f0f8dc1` fixed
`contexts`/`tags`/`text` at the dynamic JSON boundary but left every *other*
constructor keyword at that same boundary typed. Ruby coerces or truth-tests
those values; Go decodes them strictly, so seven of eight probed inputs
diverge, two of them by accepting in Ruby and rejecting in Go.

## Finding 1 — boolean keywords: Ruby `!!value`, Go requires a JSON boolean

- Ruby: `lib/tasks/task_queries.rb:39-61` — `@deferred_only = !!deferred_only`
  and the same for `unavailable_only`, `someday_only`, `delegated_only`,
  `agent_ready_only`, `recurring_only`, `body_search`. Every value except
  `nil`/`false` is truthy, including `0` and `""`.
- Go: `go/internal/query/filter.go:39-45` types these as `bool`, and
  `go/cmd/query-filter-parse-probe/main.go:136` unmarshals them directly, so a
  non-boolean JSON value is a decode error before `NewFilter` runs.
- Observed (`source-fidelity-scalar-kwargs-{ruby,go}.jsonl`):

  | case | kwargs | Ruby | Go |
  |---|---|---|---|
  | `sf_bool_string` | `{"deferred_only":"yes"}` | ok, `deferred_only=true` | error `json: cannot unmarshal string into Go struct field FilterOptions.deferred_only of type bool` |
  | `sf_bool_zero` | `{"deferred_only":0}` | ok, `deferred_only=true` | error `json: cannot unmarshal number into …` |
  | `sf_bool_empty_string` | `{"agent_ready_only":""}` | ok, `agent_ready_only=true` | error `json: cannot unmarshal string into …` |

  `{"body_search":null}` (`sf_bool_null`) is the one agreeing case: Ruby
  `!!nil` is false and Go's `null` leaves the zero value.

  Note `0` and `""` are truthy in Ruby. A correction must not map JSON
  falsiness onto Ruby falsiness; only `null` and `false` are false.

## Finding 2 — `scope`, `priority`, `state`: Ruby `to_s`, Go requires a JSON string

- Ruby: `task_queries.rb:36` (`scope.to_s.to_sym`), `:64`
  (`priority&.to_s&.upcase`), `:68` (`state&.to_s&.upcase`). A number or
  boolean stringifies and is then rejected *by the domain rule*, with the
  domain message.
- Go: `filter.go:38,48,49` type them as `*string`; the probe's decode fails
  first, so the rejection message is a Go decoder message, not the ported one.
- Observed:

  | case | kwargs | Ruby message | Go message |
  |---|---|---|---|
  | `sf_scope_number` | `{"scope":5}` | `unknown task scope: 5` | `json: cannot unmarshal number into Go struct field FilterOptions.scope of type string` |
  | `sf_priority_number` | `{"priority":5}` | `priority must be A, B, C, or none` | `json: cannot unmarshal number into … .priority of type string` |
  | `sf_priority_true` | `{"priority":true}` | `priority must be A, B, C, or none` | `json: cannot unmarshal bool into … .priority of type string` |
  | `sf_state_number` | `{"state":5}` | `state must be one of PROPOSED, INBOX, TODO, NEXT, WAITING, DONE, CANCELLED` | `json: cannot unmarshal number into … .state of type string` |

  The slice's own `observable_outputs` list names "argument rejection message
  and exit status", so a differing message is a divergence, not a detail.

## Why the existing evidence stayed green

The 32-case corpus in `porting/runners/cases/query-filter-parse.jsonl` passes a
non-string value only for `contexts`/`tags`/`text`. No case gives a scalar
keyword a non-string, non-boolean value, so 32/32 conformance is real but does
not reach this behavior. The manifest's three recorded `oracle_gaps` cover
`String#inspect` escaping, top-level-Hash `Kernel#Array`, and float/large
integers — none of them covers scalar keyword coercion.

## Correction required

Apply the same treatment `f0f8dc1` gave collections, at the same boundary
(`decodeFilterOptions`), from the Ruby source rule:

1. Decode kwargs generically (already done for collections via
   `decodeGeneric`), then for each boolean keyword set the field to
   `value != nil && value != false` rather than type-asserting a `bool`.
2. For `scope`, `priority`, and `state`, apply `rubyToS` (already present in
   `coerce.go`) to a present non-null value instead of requiring a JSON string,
   keeping the existing omitted-vs-explicit-null distinction from `9391345`.
3. Extend `porting/runners/cases/query-filter-parse.jsonl` with the eight cases
   in `source-fidelity-scalar-kwargs-cases.jsonl` and re-capture Ruby before
   changing Go.

`NewFilter`'s typed signature is correct as-is and should not change; the fix
belongs at the dynamic boundary, exactly where the collection fix landed.

## State at review time

`gofmt -l` clean, `go vet ./...` clean, `go test ./...` passes; the recorded
32/32 conformance still reproduces. The failure is coverage-shaped, not a
regression in what is already captured.

## Reproduction

```
ruby porting/runners/ruby/query-filter-parse-probe \
  porting/evidence/query-filter-parse/source-fidelity-scalar-kwargs-cases.jsonl
cd go && go run ./cmd/query-filter-parse-probe \
  ../porting/evidence/query-filter-parse/source-fidelity-scalar-kwargs-cases.jsonl
```
