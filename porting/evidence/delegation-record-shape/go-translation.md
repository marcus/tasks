# Go translation evidence

Translator session `ses_7740c1` implemented the schema-only target in
`go/internal/record/delegation.go` and added the direct conformance probe at
`go/cmd/delegation-probe`.

Evidence (2026-08-02):

- `go test ./...`, `go vet ./...`, and `go test -race ./...` passed from `go/`.
- The 102-case Ruby direct oracle was re-run structurally against the Go probe:
  `direct conformance: 102/102 observations match`.
- The generic `porting/compare/compare` runner was not used for that comparison:
  it expects fixture CLI observations and dereferences their `streams` field.
  This slice's direct module oracle deliberately has no CLI stream. The direct
  comparison parses one JSON object per `case_id` and requires exact structural
  equality; it did not normalize or bless Go output.

The fixture CLI half remains observation-only until the later `check` and read
slices provide the relevant Go command surfaces. This change does not implement
claim/release lifecycle behavior, canonical nested emission, or store checks.

Next: medium-risk property review and two separate reviews (source fidelity and
Go idiom) by later sessions; independent approval is required.
