# Query-filter parse translation

`internal/query.Filter` is the immutable read input that ports Ruby's
`Tasks::TaskFilter`. `ParseCLI` owns the legacy argument spellings and passes
the normalized inputs to `NewFilter`, which preserves constructor validation,
derived state intersection, archive inclusion, and lowercase text joining.

## Collection coercion (`Array(values).map(&:to_s)`)

Ruby's `TaskFilter#frozen_strings` coerces `contexts`, `tags`, and `text` with
`Array(values).map { |value| value.to_s.dup.freeze }`, so a scalar, a `nil`, or
a non-string element are all accepted rather than rejected. Go's typed
`FilterOptions` cannot express that, and moving `NewFilter` to `any` would make
every static caller dynamic, so the rule is ported as `query.CoerceStrings` and
applied at each dynamic boundary — today the JSON kwargs probe — before
`FilterOptions` is built. `NewFilter` itself keeps `[]string` and stays the
same function every Go caller sees.

`CoerceStrings` implements `Kernel#Array` (nil → empty, Array → itself, Hash →
its `[key, value]` pairs, anything else → one element) over `Object#to_s`, which
falls through to `inspect` for non-strings: hence `nil` → `""`, `1` → `"1"`, and
`{"key" => "value"}` for a nested object, exactly as `ruby.jsonl` records. Two
divergences the recorded oracle forced and one it does not reach:

- `nil.to_s` is `""` while `nil.inspect` is `"nil"`; only the outermost element
  uses `to_s`, so a `nil` **inside** a nested collection still renders `nil`.
- The probe now encodes with `SetEscapeHTML(false)`. Ruby's `JSON.generate`
  leaves `>` unescaped, and inspected Hash elements contain `=>`.
- Go maps lose the insertion order Ruby's Hash keeps, so Hash rendering sorts
  keys. No captured case has more than one key; this is recorded as a manifest
  oracle gap rather than silently chosen.

The manifest's `oracle_gaps` also record the `String#inspect` escaping table and
the top-level-Hash branch as implemented-from-source but not yet captured from
Ruby. Those Go tests lock Go's behavior; they are not an expected result until a
capture tick proves them against Ruby.

The direct probe intentionally remains slice-local: this behavior accepts an
argument list and has no store input, so the fixture-copy CLI runner cannot
exercise it until the Go CLI adapter is ported. `conformance` compares decoded
Ruby and Go probe outputs for every captured case instead.
