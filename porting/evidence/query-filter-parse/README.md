# Query-filter parse oracle and translation

`ruby.jsonl` is the captured Ruby oracle. `go.jsonl` is the Go translation's
current direct-probe output; it is compared structurally, not promoted to an
expected result. Run `./porting/evidence/query-filter-parse/conformance` to
reproduce the 24-case differential result.

The package also has a state-intersection property test across every scope and
state vocabulary value. Source-fidelity and Go-idiom review remain independent
medium-tier steps and are not claimed by this translation handoff.
