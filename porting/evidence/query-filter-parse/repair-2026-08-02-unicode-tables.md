# Repair — query-filter-parse finding 1 (Unicode 16/17 downcase tables)

- Repair session: ses_28e24e (mid tier per the handoff).
- Subject: finding 1 of
  `source-fidelity-review-2026-08-02-unicode-tables.md` (FAIL at 7880665) —
  `TextQuery` used Go 1.26.5's Unicode 15.0.0 tables while Ruby 4.0.6's
  `String#downcase` uses Unicode 17.0.0, leaving 55 codepoints uppercased.

## What changed

- `go/internal/query/filter.go` — the `İ` special case is now one of two
  documented differences. The second is `newerUnicodeLower`, a 55-entry
  uppercase→lowercase override table applied by `strings.Map` **before**
  `strings.ToLower`. Its entries are exactly the divergences enumerated in
  `downcase-divergence-2026-08-02.jsonl`; all 55 are simple 1:1 mappings, so no
  multi-character expansion is involved. Because the overrides land on the
  lowered form, `ToLower` is identity over them and the table stays correct
  when Go's own tables reach Unicode 17.
- `go/internal/query/filter_test.go` — `TestNewerUnicodeLowerOverrides` pins
  the table: 55 entries, no entry maps to itself, every lowered form is
  `ToLower`-stable, and `TextQuery` of each uppercase codepoint equals Ruby's
  lowered form. The stability assertion is what makes the table forward-safe.
- `porting/runners/cases/query-filter-parse.jsonl` — the review's five
  reproduction cases moved into the committed corpus as
  `query-filter-text-query-{cyrillic-1c89,latin-a7cb,garay-10d50,16ea0,mixed-new-unicode}`
  (the reviewer's `sfr2-text-query-*` ids, renamed to the corpus convention).
  Conformance now guards the fix.

## Evidence

- `./porting/evidence/query-filter-parse/conformance` — **65/65** cases
  matched (was 60/60 plus the five known-failing cases held out).
- `ruby.jsonl` and `go.jsonl` recaptured over the 65-case corpus; they are
  byte-identical. Ruby was captured from the probe, not from Go.
- The reviewer's independent 28-case adversarial corpus
  (`source-fidelity-unicode-cases-2026-08-02.jsonl`) re-run against the
  repaired Go: **28/28** matched, up from 23/28. Its Go capture
  `source-fidelity-unicode-go-2026-08-02.jsonl` is refreshed; the Ruby capture
  is untouched.
- `gofmt -l .` clean, `go vet ./...` clean, `go test ./...` and
  `go test -race ./...` pass.

## Not repaired here

Only finding 1 was in scope. Still owed on this slice: a fresh independent
source-fidelity review at the repaired commit (top tier), the Go-idiom
re-confirmation, the three implemented-from-source oracle gaps, and
independent approval.

## Carried forward

The two-runtime table-version coupling is not slice-local. This table repairs
`text_query` only; the next slice that downcases or upcases inherits the same
gap and should promote the table to a shared helper rather than copy it. The
manifest note records this.
