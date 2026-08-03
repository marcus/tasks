# `query-filter-parse` independent source-fidelity re-review

Reviewed `port/query-filter-parse` at
`a7587359e8e6e5ad23de2eefa4172656056c1406` (including the explicit-null
scope repair at `9391345d461384fb52bb1b3a7f42443448cdb4e6`) against pinned Ruby
source `87d8cc201410669e5b4ed1987eb44a01946ae92f:lib/tasks/task_queries.rb`.

## Finding

### High — scalar collection constructor inputs are accepted by Ruby and rejected by Go

- Ruby location: `Tasks::TaskFilter#frozen_strings`, called for `contexts`,
  `tags`, and `text` from `#initialize`.
- Go location: `go/cmd/query-filter-parse-probe/main.go`,
  `decodeFilterOptions`, decoding into `query.FilterOptions`; its collection
  fields are `[]string`.
- Reproduction: `source-fidelity-probe.jsonl` supplies
  `{"case_id":"query-filter-scalar-context","operation":"new","kwargs":{"contexts":"@work"}}`.
  The Ruby probe succeeds and returns `contexts: ["@work"]`. The Go probe
  returns `ArgumentError: json: cannot unmarshal string into Go struct field
  FilterOptions.contexts of type []string` before `NewFilter` runs.

Ruby deliberately uses `Array(values).map { |value| value.to_s ... }`, so this
is not limited to `contexts`: scalar `tags` and `text`, and non-string elements
inside any of those collections, also have observable coercion semantics. The
direct JSON probe is the slice's constructor conformance boundary, and it must
preserve those inputs rather than reject them during JSON decoding. Add Ruby
oracle cases before changing Go; do not bless Go output.

## Checks

- `porting/runners/ruby/query-filter-parse-probe porting/evidence/query-filter-parse/source-fidelity-probe.jsonl` — Ruby accepted the scalar context.
- `cd go && go run ./cmd/query-filter-parse-probe ../porting/evidence/query-filter-parse/source-fidelity-probe.jsonl` — Go rejected it as described above.
- Reviewed the null-scope repair and its focused test; it correctly distinguishes omitted scope from explicit JSON null.

The prior Go-idiom review remains valid, but this source-fidelity review fails.
