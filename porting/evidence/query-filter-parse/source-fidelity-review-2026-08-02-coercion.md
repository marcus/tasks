# Source-fidelity review — query-filter-parse @ 4df99f7

- Reviewer session: `ses_5d055f` (top tier, independent — no prior involvement in
  this slice; distinct from every excluded session in the handoff)
- Subject commit: `4df99f7` on `port/query-filter-parse`
- Ruby oracle: `lib/tasks/task_queries.rb` (`Tasks::TaskFilter`) at
  `87d8cc201410`, Ruby 4.0.6
- Go subject: `go/internal/query/filter.go`, `go/internal/query/coerce.go`,
  `go/cmd/query-filter-parse-probe`
- **Verdict: FAIL — three Go defects.** No implementation file was read for
  expected values and none was edited; every expected value below was captured
  from Ruby before the Go probe ran.

## Method

1. Line-by-line read of `TaskFilter#initialize`, `.parse_cli`, `#states`,
   `#include_archive?`, `#text_query`, `#frozen_strings` against the Go port.
2. Independent exhaustive differentials, not a re-run of the committed corpus:
   - **downcase**, every non-surrogate codepoint U+0000–U+10FFFF: Ruby
     `String#downcase` vs `Filter.TextQuery`. **0 mismatches.** The Unicode 16/17
     override table added at `4df99f7` is confirmed complete and correct.
   - **upcase**, every non-surrogate codepoint: Ruby `String#upcase` vs
     `strings.ToUpper`. 157 codepoints diverge (Ruby's full case mapping vs Go's
     simple mapping), but none of their Ruby images is a substring of `A`, `B`,
     `C` or of any of the seven state names, so no divergent codepoint can flip
     the `priority`/`state` accept/reject decision. Only U+0041/61, U+0042/62,
     U+0043/63 upcase to `A`/`B`/`C` at all. `priority` and `state` are clean.
   - **Final_Sigma**: Ruby 4.0.6 `"ΟΔΟΣ".downcase` is `οδοσ`, not `οδος` — Ruby
     does not apply the context-sensitive rule, and neither does
     `strings.ToLower`. No divergence; `TextQuery` needs no context handling.
   - **String#inspect**, every non-surrogate codepoint: Ruby vs the port's
     `inspectString`. **814,789 mismatches** (finding 3).
3. An 8-case adversarial differential through the committed probes, captured
   Ruby-first. **1/8 matched.**
   - cases: `source-fidelity-coercion-cases-2026-08-02.jsonl`
   - Ruby: `source-fidelity-coercion-ruby-2026-08-02.jsonl`
   - Go: `source-fidelity-coercion-go-2026-08-02.jsonl`
   - generator + sweep harness: `review-2026-08-02-coercion/`

All three defects live in `coerce.go`, on the dynamic-kwargs boundary that
ports `Array(values).map(&:to_s)`. Everything in `filter.go` — scope vocabulary,
the five flag pairs, the two mutual-exclusion rules, the two scope-restriction
rules, `parse_cli`'s argument order and regex branches, `states`, the state
vocabularies, `include_archive?`, `TextQuery` — I could not fault.

## Finding 1 — Hash element key order is sorted, not insertion order

`coerce.go:190` `sortedKeys`, reached from `rubyInspect` (`coerce.go:174`) and
`rubyArray` (`coerce.go:125`).

Ruby Hash preserves insertion order, and `JSON.parse` inserts in document order,
so `to_s` of a coerced Hash renders keys in the order the caller wrote them. Go
decodes into `map[string]any` and sorts, which reorders any Hash with more than
one key. The comment at `coerce.go:186` calls the loss unavoidable at the JSON
boundary — it is not: the unknown-keyword path added at `febbdf6` already does an
order-preserving token-stream decode in the probe, so the same technique applies
here.

| case | Ruby | Go |
|---|---|---|
| `contexts: [{"b":1,"a":2}]` | `{"b" => 1, "a" => 2}` | `{"a" => 2, "b" => 1}` |
| `tags: {"z":1,"a":2}` | `["z", 1]`, `["a", 2]` | `["a", 2]`, `["z", 1]` |

The committed corpus masks this: `query-filter-collection-coercion` is the only
Hash case and its Hash has a single key.

The manifest notes do mention it ("CoerceStrings renders Hash keys sorted; no
captured case has more than one key"), but only as an aside: it is not one of the
three recorded `oracle_gaps`, not an `intentional_differences` entry, and no
captured case exercises it. PORTING.md requires every divergence to end in a
classification; an unclassified sentence in `notes` is not one. It is a Go defect
— Ruby's order is reproducible in Go — so it needs repairing, not recording.

Correction: decode kwargs values with an order-preserving decoder (the
`json.Decoder` token stream already used for unknown keywords) and carry Hash
entries as ordered pairs, so `rubyArray` and `rubyInspect` emit document order.

## Finding 2 — Float elements render the JSON literal, not `Float#to_s`

`coerce.go:177-180`. The comment claims `json.Number` "keeps the literal digits,
which is what Integer#to_s and Float#to_s produce for every literal Ruby's JSON
parser accepts". That is true for Integer and false for Float: Ruby parses the
literal to a Float and `Float#to_s` re-renders it in Ruby's own shortest form,
which switches to exponent notation below 1e-4 and at/above 1e16.

| element | Ruby | Go |
|---|---|---|
| `0.00001` | `1.0e-05` | `0.00001` |
| `100.0` | `100.0` | `100.0` (matches) |
| `12345678901234567890` (Integer) | `12345678901234567890` | same (matches) |

The manifest records "float/large-integer elements" as *implemented from the Ruby
source rule but not captured*. The capture now exists and the implementation is
wrong, so this is a defect, not a coverage gap.

Correction: for a JSON number carrying `.`, `e`, or `E`, parse to `float64` and
render with Ruby's `Float#to_s` algorithm (shortest round-trip digits, exponent
form for `exp < -4 || exp >= 16`, always a fractional digit); keep the literal
digits only for integer literals.

## Finding 3 — `String#inspect` uses `\xNN` and does not escape non-printables

`coerce.go:205` `inspectString`.

Ruby renders `\xNN` only for a binary (ASCII-8BIT) string. For a UTF-8 string —
which is all `JSON.parse` produces — every character without a named escape that
is not printable is rendered `\uNNNN`, including the C0 controls, DEL, the C1
range, and every unassigned codepoint. The port escapes `< 0x20` and `0x7F` as
`\xNN` and passes every non-ASCII character through raw.

Exhaustive sweep: **814,789 of 1,112,064 codepoints diverge**.

| element (a one-element Array holding the codepoint) | Ruby | Go |
|---|---|---|
| U+0001 | backslash-u 0001 | backslash-x 01 |
| U+007F | backslash-u 007F | backslash-x 7F |
| U+0085 | backslash-u 0085 | raw U+0085, unescaped |
| U+0378 (unassigned) | backslash-u 0378 | raw U+0378, unescaped |

(Written out in words because the literal escapes and raw control characters do
not survive a markdown round trip; the three JSONL captures are authoritative.)

Ruby emits uppercase hex in `inspect`, and `text_query` then downcases the
rendered text, so U+007F yields uppercase in `text` and lowercase in
`text_query` -- the `rev-inspect-del` case pins both.

The named escapes already in `stringEscapes` (`\n`, `\t`, `\r`, `\f`, `\v`, `\b`,
`\a`, `\e`, `\"`, `\\`) and the `\#` rule before `{`, `$`, `@` are correct; only
the fallback branch is wrong.

The manifest records "String#inspect escaping" as implemented-from-source and
uncaptured. As with finding 2, it is now captured and wrong.

Correction: replace the `\xNN` fallback with `\uNNNN` (uppercase hex, four
digits, `\u{NNNNN}` above U+FFFF — verify the >BMP form against Ruby before
implementing) and extend the escaped set from "C0 and DEL" to Ruby's printability
predicate. The generated capture
(`review-2026-08-02-coercion/capture-ruby-inspect.rb`) is the authority for
which codepoints are escaped; it is ~815k lines, so regenerate rather than commit.

## Scope note for the repair tick

Findings 2 and 3 are reachable only through the dynamic kwargs boundary in the
probe, never through `ParseCLI` or a typed Go caller. Finding 1 is likewise
boundary-only. If Marcus decides the dynamic boundary is probe scaffolding rather
than ported surface, the honest disposition is an intentional-difference record
that narrows the slice's claim — not silence. As the slice stands, the boundary
is committed code in `internal/query` with its own exported API
(`CoerceStrings`, `CoerceString`, `CoerceBool`, `InspectSymbol`), so it is
reviewed as ported surface.
