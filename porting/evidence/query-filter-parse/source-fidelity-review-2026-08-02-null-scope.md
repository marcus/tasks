# `query-filter-parse` independent source-fidelity review

Reviewed `port/query-filter-parse` at
`68e4b7a0bfc66f8465c296a345654515bca08ed3` against the pinned Ruby source
`87d8cc201410669e5b4ed1987eb44a01946ae92f:lib/tasks/task_queries.rb`.

## Finding

### High — an explicit null constructor scope becomes the default scope

- Go location: `go/internal/query/filter.go`, `FilterOptions.Scope` and
  `NewFilter`.
- Ruby location: `lib/tasks/task_queries.rb`, `Tasks::TaskFilter#initialize`.
- Evidence: Ruby turns an explicit `scope: nil` into `"".to_sym` and rejects
  it. Go represents both omitted and JSON `null` scopes as a nil `*string`,
  then substitutes `ScopeOpen`. The direct probes disagree for this case:

  ```json
  {"case_id":"query-filter-null-scope","operation":"new","kwargs":{"scope":null}}
  ```

  Ruby returns `ArgumentError: unknown task scope: `; Go returns a successful
  open filter.
- Exact correction: make the external constructor representation preserve
  omitted-versus-explicit-null scope, reject the latter with Ruby's message,
  and add this case to the Ruby oracle corpus before changing Go. Do not bless
  the present Go output.

## Checks run

- `porting/evidence/query-filter-parse/conformance` — existing corpus matched
  24/24 cases (the corpus omits this input).
- `cd go && go test ./internal/query && go vet ./... && go test -race ./...`
  — passed.

The focused source comparison found no additional divergence in the declared
scope aliases, flag conflicts, state intersection, string normalization, or
defensive collection ownership. This review fails on the explicit-null scope
case above. The separate Go-idiom review remains outstanding.
