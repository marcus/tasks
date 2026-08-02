# Query-filter parse oracle and translation

`ruby.jsonl` is the captured Ruby oracle. `go.jsonl` is the Go translation's
current direct-probe output; it is compared structurally, not promoted to an
expected result. Run `./porting/evidence/query-filter-parse/conformance` to
reproduce the 76-case differential result, including explicit `scope: null`,
scalar collection, mixed collection, collection-null, non-boolean boolean
keyword, non-string scope/priority/state, and unknown-constructor-keyword
inputs. `symbol-inspect-capture` captures Ruby's `Symbol#inspect` for 73 name
shapes into `symbol-inspect-ruby.jsonl`, the oracle for how an unknown keyword
is named.

The package also has a state-intersection property test across every scope and
state vocabulary value, plus `coerce_test.go` for the `Array(values).map(&:to_s)`
collection coercion described in `translation.md`. Source-fidelity and Go-idiom
review remain independent medium-tier steps and are not claimed by this
translation handoff.

`source-fidelity-review-2026-08-02-scalar-collections.md` is repaired as of the
40/40 run above; its reproduction case set is now part of the committed corpus.
So is `source-fidelity-review-2026-08-02-scalar-kwargs.md` — see
`source-fidelity-repair-2026-08-02-scalar-kwargs.md`, which also recorded one
new divergence (unknown constructor keywords) with its Ruby capture in
`unknown-keyword-{cases,ruby}.jsonl`. That divergence is repaired as of the
49/49 run above; see `repair-2026-08-02-unknown-keywords.md`.

`source-fidelity-review-2026-08-02-parse-cli-regexes.md` failed with three
`parse_cli` defects; all three are repaired as of the 60/60 run above, with
their cases in the committed corpus — see
`repair-2026-08-02-parse-cli-regexes.md`. That repair also gave both probes an
`argv_base64` argument encoding, because JSONL cannot carry the invalid-UTF-8
arguments finding 2 turns on.

That fresh independent review is
`source-fidelity-review-2026-08-02-unicode-tables.md`. It confirms all three
repairs and **fails** with one new Go defect: `text_query` downcases with Go's
Unicode 15.0.0 tables while Ruby 4.0.6 uses 17.0.0, leaving 55 codepoints
uppercased. Its adversarial corpus is
`source-fidelity-unicode-{cases,ruby,go}-2026-08-02.jsonl` (23/28 matched) and
the exhaustive whole-Unicode sweep is
`downcase-divergence-2026-08-02.jsonl`.

That defect is repaired as of the 65/65 run above: the five reproduction cases
now live in `porting/runners/cases/query-filter-parse.jsonl` as
`query-filter-text-query-*`, and the reviewer's 28-case adversarial corpus
re-runs 28/28 against the repaired Go. See
`repair-2026-08-02-unicode-tables.md`. The repair is a slice-local override
table; the coupling it works around belongs to any later slice that changes
case.

`source-fidelity-review-2026-08-02-coercion.md` failed with three defects in
the coercion boundary — sorted Hash keys, JSON-literal Float rendering, and a
`\xNN` `String#inspect` that escaped nothing above ASCII. All three are
repaired as of the 76/76 run above; see `repair-2026-08-02-coercion.md`. The
reviewer's own corpus goes 1/8 to 8/8, the exhaustive whole-Unicode inspect
differential is 0/1,112,064, and a 200,034-literal float differential is clean.
`review-2026-08-02-coercion/capture-ruby-inspect.rb` and
`generate-printable-table.rb` regenerate the ~815k-line capture and the
`rubyPrintable` table from it; the capture is regenerated, never committed.
`review-2026-08-02-coercion/number-to-s-ruby.jsonl` is the readable number
oracle.

`source-fidelity-review-2026-08-02-symbol-inspect.md` is the fresh independent
review at 84df4c5. It clears all three attacks that review was directed at —
the `point == 16` float rule, the `rubyPrintable` table's provenance, and
`DecodeValue`'s repeated-key handling — and **fails** with three
`InspectSymbol` defects, one of them a regression introduced by 84df4c5.
Reproduction is `source-fidelity-symbol-{cases,ruby,go}-2026-08-02.jsonl` at
30/78. `review-2026-08-02-symbol-inspect/` holds the four generators, including
`generate-symbol-sweep.rb`, which sweeps `Symbol#inspect` over every
non-surrogate codepoint and replaces the 73 hand-written name shapes that hid
those defects through four reviews.

All three of those defects are repaired as of the 154/154 run above; see
`repair-2026-08-02-symbol-inspect.md`. The reviewer's corpus goes 30/78 to
78/78 and is now folded into `porting/runners/cases/query-filter-parse.jsonl`,
and the exhaustive sweep runs clean for all four sigils — 17,376 of 17,376
cases, 4,448,256 names, 0 mismatches. Those sweep files are regenerated, never
committed; the four case files alone are ~500 MB.

`source-fidelity-review-2026-08-02-global-names.md` is the fresh independent
review at c2dbd9e, aimed at the gap the previous sweep left: names of more than
one codepoint behind a sigil. It **fails** with two `globalName` defects —
`$-x` prints bare in Ruby and quoted in Go, and `$00`/`$01` print quoted in Ruby
and bare in Go — and clears `ParseCLI`'s regex branches, `filter.go`'s
state-intersection vocabulary, and the `hexC0` split of the two escape
vocabularies. `review-2026-08-02-global-names/` holds three corpora with both
captures (96 symbol names at 86/96, 110 argv/constructor cases at 110/110, 206
escape cases at 206/206), their generators, and two Ruby probes that
characterise the exact `$-X` and leading-zero rules.

Every differential here compares parsed output, never bytes: Ruby's
`JSON.generate` emits U+2028 and U+2029 raw where Go's encoder escapes them, so
a byte diff reports a divergence for a value both probes agree on.

`source-fidelity-review-2026-08-02-composition.md` is the fresh independent
review at `b13485b`, and it **passes** — the first source-fidelity pass this
slice has had. It attacks the shapes the exhaustive single-code-point sweeps
cannot express: multi-code-point symbol names composing a sigil, a trailing
`?`/`!`/`=` and printable-versus-non-printable non-ASCII; Ruby's operator table
and its near misses; nested containers and the `point == 15/16/17` float-layout
boundaries; `parse_cli`'s regex-boundary argument shapes; and
unknown-keyword-versus-domain-error precedence. Its two corpora are
`review-2026-08-02-composition/{cases,ruby,go}.jsonl` at 188/188 and
`.../cases2.jsonl` at 18/18; `review-2026-08-02-composition/generate-cases.py`
regenerates the first. It also independently re-ran all six symbol sweeps —
26,064 cases, 6,672,384 names, 0 mismatches — which reproduces both `b13485b`
rules and shows the four earlier sigil sweeps unregressed. Go-idiom
re-confirmation and independent approval are what remain.
