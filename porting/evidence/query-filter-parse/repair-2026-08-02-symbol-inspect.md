# Repair — the three `InspectSymbol` defects of the 84df4c5 source-fidelity review

Repairs Findings 1, 2, and 3 of
`source-fidelity-review-2026-08-02-symbol-inspect.md`. Mid tier, as the review
and the handoff both directed: each finding came with an exact correction, so
this tick applied them and proved them, and did not re-judge them.

- Repair session: `ses_6f2cf9`, branch `port/query-filter-parse`, at 2cb5247.
- Subject: `go/internal/query/coerce.go`.
- Oracle: Ruby 4.0.6, `lib/tasks/task_queries.rb` at `87d8cc201410`, driven
  through `porting/runners/ruby/query-filter-parse-probe`. Ruby is unmodified;
  every Ruby capture below comes from that unchanged oracle, and no expected
  result anywhere is derived from Go output.
- Baseline reproduced before the edit: `conformance` → 76/76, and the review's
  own corpus at 30/78.

## What changed

**Finding 1 — the C0/DEL spelling.** `inspectString` was the shared quoted-form
renderer for both `String#inspect` and `Symbol#inspect`, so 84df4c5's correct
change to String's spelling silently changed Symbol's too. It is now
`quoteRuby(text, hexC0 bool)` with two named callers: `inspectString` passes
false and keeps `\uNNNN`, `quoteSymbol` passes true and emits `\x%02X` for a
character below U+0020 or at U+007F. The named escapes, the `#` rule, and the
`\u{NNNNN}` above-BMP form stay shared, which is what the review's passing
`sym-named-escapes` case pins.

**Finding 2 — non-ASCII in a bare symbol.** `identifier`'s
`character > unicode.MaxASCII` arm is replaced by an explicit non-ASCII arm
gated on `symbolPrintable`, which is `unicode.Is(rubyPrintable, character) ||
character == nextLine`. `nextLine` is U+0085, the one codepoint `String#inspect`
escapes and `Symbol#inspect` prints bare; the review verified that exception by
set equality against a fresh exhaustive capture, so it is not weakened here.
Splitting non-ASCII into its own arm also stops `unicode.IsLetter` and
`unicode.IsDigit` from re-admitting a non-printable codepoint through the back
door: above ASCII, the printability rule is now the only rule.

**Finding 3 — the single-character global fallback.** `len([]rune(name)) == 1`
is now `len([]rune(name)) == 1 && strings.ContainsAny(name, specialGlobals)`,
where `specialGlobals` is the fixed twenty-character set the review enumerated.
The digit-global and identifier branches are untouched, `$` alone still quotes
via the empty-name guard, and non-ASCII after `$` still falls to Finding 2's
rule through `identifier`.

## Evidence

**The review's reproduction corpus: 30/78 → 78/78.** Compared structurally
against the review's committed Ruby capture
`source-fidelity-symbol-ruby-2026-08-02.jsonl`, which predates this edit.

**Exhaustive symbol sweep: 0 mismatches over 4,448,256 names.** Every
non-surrogate codepoint (1,112,064) as a single-character unknown keyword, run
four times — bare and behind the `$`, `@`, and `@@` sigils — through
`review-2026-08-02-symbol-inspect/generate-symbol-sweep.rb` into 4,344 cases per
sigil, captured from Ruby and from Go and compared structurally: **17,376 of
17,376 cases matched, all four sigils.** This is the sweep that found the three
defects; it now runs clean. It is regenerated, not committed — the four case
files alone are ~500 MB.

**Committed conformance: 76/76 → 154/154.** The review's 78 cases are folded
into `porting/runners/cases/query-filter-parse.jsonl` under a comment block, so
the committed corpus is no longer blind to symbol rendering. No case_id
collides with an existing one.

**Unit pins:** `go/internal/query/coerce_symbol_test.go` pins each of the three
corrections directly — the `\xNN` C0 spelling against String's `\uNNNN`, the
printable/non-printable non-ASCII split with U+0085 as its exception on all
three sigils, and the twenty accepted against the twelve rejected globals.
`TestInspectStringUsesTheUnicodeEscapeForms` still passes unchanged, which is
what proves the two vocabularies are now separate rather than swapped.

**Toolchain:** `gofmt -l` empty, `go vet ./...` clean, `go test ./...` and
`go test -race` on `internal/query` and the probe all pass.

## What this repair does not claim

It is a repair, not a review: it applied three corrections a different session
derived, and its own verification is the review's oracle plus the exhaustive
sweep. The fresh independent source-fidelity review at the repaired commit is
still owed, and the handoff's direction for it stands — multi-codepoint symbol
names beyond the alphabet cross-product, `ParseCLI`'s regex branches at this
commit, and `filter.go`'s state-intersection vocabulary, which no review has
re-read since 4df99f7. The Go-idiom re-confirmation still predates every commit
since f0f8dc1, and this repair adds one more open question to it: whether
`quoteRuby`'s boolean parameter is the right shape for the String/Symbol split,
or whether two separate escapers would read better.
