# Query-filter parse translation

`internal/query.Filter` is the immutable read input that ports Ruby's
`Tasks::TaskFilter`. `ParseCLI` owns the legacy argument spellings and passes
the normalized inputs to `NewFilter`, which preserves constructor validation,
derived state intersection, archive inclusion, and lowercase text joining.

The direct probe intentionally remains slice-local: this behavior accepts an
argument list and has no store input, so the fixture-copy CLI runner cannot
exercise it until the Go CLI adapter is ported. `conformance` compares decoded
Ruby and Go probe outputs for every captured case instead.
