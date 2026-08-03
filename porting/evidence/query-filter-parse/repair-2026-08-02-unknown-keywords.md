# Repair — unknown constructor keywords (closes the last recorded oracle gap)

Session ses_2b32d7, resuming the handoff recorded by ses_62f164. This is a
repair, not a review: the fresh independent source-fidelity review and the
re-confirmation of the Go-idiom review are still owed, and medium tier forbids
self-approval.

## The divergence

`source-fidelity-repair-2026-08-02-scalar-kwargs.md` recorded it and left it on
purpose: Ruby's `TaskFilter#initialize` raises `ArgumentError` on an
unrecognised keyword before its body runs, while the Go probe unmarshalled
kwargs into a `map[string]json.RawMessage` and silently ignored unknown keys.
The message names every unrecognised key **in the order the caller gave them**,
which a Go map cannot carry.

## Order of work

Ruby was captured before Go changed, again. Beyond the three cases already in
`unknown-keyword-{cases,ruby}.jsonl`, the Ruby probe was asked four questions
the fix needed answers to rather than guesses:

| Question | Ruby |
|---|---|
| Is the order the given order, or sorted? | given — `{"also":2,"nope":1}` → `unknown keywords: :also, :nope` |
| Does an unknown keyword outrank a sibling's domain error? | yes — `{"state":"BLOCKED","nope":1}` → `unknown keyword: :nope`, not the state message |
| How is a name that is not a bare symbol rendered? | `Symbol#inspect` — `{"a b":1}` → `unknown keyword: :"a b"` |
| Operators, empty names, escapes? | `:+` bare, `:""`, `:"quote\"q"` |

Nine cases went into `porting/runners/cases/query-filter-parse.jsonl` under
corpus-style ids (the three `uk_*` ids in `unknown-keyword-cases.jsonl` are the
first three of them), and `ruby.jsonl` was re-captured from the Ruby probe.
Only then was Go touched.

`Symbol#inspect` itself is wider than nine cases, so it got its own capture:
`symbol-inspect-capture` prints `name` / `inspect` pairs for 73 name shapes into
`symbol-inspect-ruby.jsonl` — identifiers, constants, keywords, the `?`/`!`/`=`
suffixes, every operator method, instance/class/global variables, non-ASCII
names, and the quoted forms. Two results are worth naming because they are not
what the rule looks like from a distance: `:~@` and `:!@` are **quoted** while
`:+@` and `:-@` are bare, and `$1`, `$!`, `$;` are all bare while `@1` is not.

## The fix

- `query.InspectSymbol` (`go/internal/query/coerce.go`) is Ruby's
  `Symbol#inspect` for these shapes, reusing the existing `inspectString` for
  the quoted form. It sits with `CoerceBool`/`CoerceString` because it is the
  same kind of thing: a Ruby-language rule the dynamic boundary must apply.
- `keywordOrder` (`go/cmd/query-filter-parse-probe/main.go`) reads the kwargs
  object as a `json.Decoder` token stream, keeping key order; a repeated key
  keeps its first position, as Ruby's `Hash#[]=` does.
- `rejectUnknownKeywords` runs at the top of `decodeFilterOptions`, before every
  coercion and before `NewFilter`, because that is where Ruby raises.
  `NewFilter`'s typed signature is untouched — a Go caller cannot pass an
  unknown keyword at all, so this belongs at the boundary and nowhere else.

## Evidence

- Differential conformance: **49/49** via
  `./porting/evidence/query-filter-parse/conformance` (40 prior cases plus the
  nine unknown-keyword cases). `go.jsonl` re-captured from the same corpus.
- `gofmt -l` clean, `go vet ./...` clean, `go test -race ./...` passes.
- New tests: `TestInspectSymbolMatchesRubySymbolInspect` (43 of the captured
  names) and `TestDecodeFilterOptionsRejectsUnknownKeywordsInGivenOrder`
  (order, precedence over the domain error, quoting, duplicate keys).
- All **73** rows of `symbol-inspect-ruby.jsonl` were additionally checked
  against `InspectSymbol` by a throwaway test in this session; every row
  matched. It is not committed because a unit test may not read a path outside
  the Go module — the 43 rows in the committed test are the durable subset, and
  `symbol-inspect-capture` reproduces the full comparison on demand.

## Manifest

The fourth `oracle_gaps` entry (unknown constructor keywords) is removed — it is
captured and satisfied. The three original gaps (`String#inspect` escaping
inside coerced collection elements, `Kernel#Array` of a top-level Hash, and
float/large-integer elements) remain open and unclaimed by this tick.
