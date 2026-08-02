# Source-fidelity review — query-filter-parse @ 7880665

- Reviewer session: ses_782e5b (top tier; no prior involvement with this slice)
- Subject: `Tasks::TaskFilter.parse_cli` / `TaskFilter#initialize` / `#text_query`
  (`lib/tasks/task_queries.rb:18-144`, source_sha 87d8cc201410) against
  `go/internal/query/filter.go` at `port/query-filter-parse` commit 7880665.
- Scope: the fresh independent review owed by
  `source-fidelity-review-2026-08-02-parse-cli-regexes.md` after its three
  repairs, plus the constructor paths `parse_cli` reaches.
- **Verdict: FAIL — one Go defect (finding 1).** The three previously reported
  `parse_cli` regex defects are confirmed repaired.

## Method

1. Line-by-line read of `parse_cli`, `initialize`, `states`, `text_query`
   against the Go translation.
2. A fresh 28-case adversarial differential run built by this reviewer, not
   reused from the committed corpus:
   `source-fidelity-unicode-cases-2026-08-02.jsonl` with its captures in
   `source-fidelity-unicode-{ruby,go}-2026-08-02.jsonl`. Ruby was captured
   first; no Go source was edited by this review. Result: **23/28 matched**,
   5 mismatches, all one root cause (finding 1).
3. An exhaustive whole-Unicode sweep: every non-surrogate codepoint
   U+0000–U+10FFFF through Ruby `String#downcase` and through the Go
   `TextQuery` expression. 55 codepoints diverge; the full list is
   `downcase-divergence-2026-08-02.jsonl`.
4. `./porting/evidence/query-filter-parse/conformance` — 60/60, reproduced.

## Finding 1 — `TextQuery` misses every case mapping added after Unicode 15 (Go defect)

- **Ruby:** `lib/tasks/task_queries.rb:137` — `def text_query = text.join(" ").downcase`.
  Ruby 4.0.6 ships Unicode **17.0.0** tables.
- **Go:** `go/internal/query/filter.go:255-258` — `strings.ToLower` after an
  explicit `İ` replacement. Go 1.26.5's `unicode` package is Unicode
  **15.0.0** (`unicode.Version`).
- **Divergence:** 55 codepoints that Ruby downcases are left unchanged by Go.
  They are the case pairs added in Unicode 16.0/17.0: U+1C89, the Latin
  Extended-D block additions U+A7CB–U+A7DC, Garay U+10D50–U+10D65, and
  U+16EA0–U+16EB8. Enumerated with both mappings in
  `downcase-divergence-2026-08-02.jsonl`.
- **Observable failure:** `text_query` is the string every text term is matched
  against. `tasks list Ᲊfoo` (U+1C89) yields `text_query = "ᲊfoo"` under Ruby
  and `"Ᲊfoo"` under Go, so a task whose body carries the lowercase form
  matches in Ruby and does not match in Go. Reproduced differentially by cases
  `sfr2-text-query-cyrillic-1c89`, `sfr2-text-query-latin-a7cb`,
  `sfr2-text-query-garay-10d50`, `sfr2-text-query-16ea0`,
  `sfr2-text-query-mixed-new-unicode`.
- **Classification:** Go defect. It is not nondeterminism and not an
  intentional difference: Ruby's answer is well-defined and the Go answer is
  simply computed from an older table.
- **Correction:** extend the existing `İ` special-case in `TextQuery` into a
  generated override table covering the 55 codepoints in
  `downcase-divergence-2026-08-02.jsonl`, applied before `strings.ToLower`.
  The overrides map uppercase→lowercase, so they stay correct if a future Go
  release picks up Unicode 17 (`ToLower` of the already-lowered form is
  identity). The repair must also add a Go test that regenerates or asserts the
  table, and move the five failing cases above into
  `porting/runners/cases/query-filter-parse.jsonl` so conformance guards it.
  This coupling to two runtimes' table versions is worth a manifest note: it
  will recur in any later slice that downcases or upcases.

## Confirmed correct

- **The three prior findings are repaired.** `tagArgument`
  (`filter.go:182-191`) reproduces `/\A\+(.+)/` including "`+` immediately
  followed by a newline does not take the branch": cases
  `sfr2-bare-plus`, plus the corpus's four `query-filter-tag-*-newline` cases
  match. The invalid-UTF-8 guard (`filter.go:115-117`) raises in argument
  order, after an earlier unknown flag and before the mutually-exclusive-scope
  check — the corpus's four `query-filter-invalid-utf8-*` cases match.
  `İ` is handled (`sfr2-text-query-*`, corpus `query-filter-text-query-dotted-capital-i`).
- **Priority regex `\z` boundary.** `len(arg) == 2 && arg[0] == '-' && …`
  (`filter.go:153`) is exactly `/\A-([ABC])\z/`: `-A\n`, `\n-A`, `-AB`, `-Á`,
  `-c`, `-`, `--` all agree (`sfr2-priority-*`, `sfr2-bare-dash`,
  `sfr2-double-dash`).
- **Branch order.** The Go `default` arm tests priority → `@` → `+` → `/` →
  `-` → text, matching the Ruby `when` order, and both raise `unknown flag`
  inside the loop before the post-loop scope check
  (`sfr2-unknown-flag-before-scope-conflict`).
- **`lifecycle_scopes.uniq.length > 1`** is faithfully a set membership count:
  `sfr2-repeated-same-scope`, `sfr2-scope-repeat-then-conflict`.
- **Constructor error order** — scope, deferred+someday, unavailable-scope,
  delegated+agent-ready, agent-ready-scope, priority, state — matches
  `initialize` line for line, and `""` for priority/state is rejected rather
  than treated as absent (Ruby `""` is truthy).
- **`states`** intersection and the four scoped vocabularies match
  `Tasks::Check::{STATES,OPEN_STATES,PROPOSED_STATES,CLOSED_STATES}` verified
  against the live Ruby constants.
- **`upcase` is not exposed to the same table problem.** Ruby's `upcase` does
  expand `ß`→`SS` and `ﬁ`→`FI` where Go's `strings.ToUpper` does not, but no
  such expansion and no Unicode 16/17 addition can produce `A`/`B`/`C` or any
  of the seven state names, so priority/state acceptance is unaffected:
  `sfr2-priority-upcase-sharp-s`, `sfr2-state-upcase-ligature`,
  `sfr2-state-cyrillic-1c89` all match.

## Note (not a finding)

`stateOrder` and the three scoped vocabularies in `States()`
(`filter.go:18,231-237`) are hand-copied literals of `Tasks::Check`'s
constants rather than a reference to a single ported `check` vocabulary. The
values are correct today. Whether the `check` slice should own them is a
Go-idiom/structure call for that reviewer, not a source-fidelity defect.
