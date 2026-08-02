# `query-filter-parse` independent source-fidelity review (parse_cli regexes)

Reviewed `port/query-filter-parse` at
`febbdf6` (the unknown-keyword repair) against pinned Ruby source
`87d8cc201410669e5b4ed1987eb44a01946ae92f:lib/tasks/task_queries.rb`.
Session `ses_01941e`; no prior review in this slice's history covered
`parse_cli`'s regex branches or `text_query`.

Ruby was captured before anything in Go was touched, and nothing in `go/` was
edited by this review.

**Verdict: source-fidelity review fails.** Three divergences, all in
`Tasks::TaskFilter.parse_cli` / `#text_query` versus
`go/internal/query/filter.go`. All are Go defects (the Ruby rule is the
specification), not intentional differences.

## Finding 1 — High: `+tag` capture ignores Ruby's newline boundary

- Ruby location: `Tasks::TaskFilter.parse_cli`, `when /\A\+(.+)/ then tags << Regexp.last_match(1)`
  (`lib/tasks/task_queries.rb:105`).
- Go location: `query.ParseCLI`, `case len(arg) > 1 && strings.HasPrefix(arg, "+"): options.Tags = append(options.Tags, arg[1:])`
  (`go/internal/query/filter.go:147`).
- Rule: Ruby's `.` does not match `"\n"`. `(.+)` therefore captures only the
  run of non-newline characters immediately after `+`, and the branch does not
  match at all when the character after `+` is a newline — in which case the
  argument falls through to `else text << arg` and keeps its leading `+`.
  Go takes the whole remainder unconditionally.
- Evidence (`source-fidelity-argv-{cases,ruby,go}-2026-08-02.jsonl`):

  | argv | Ruby | Go |
  |---|---|---|
  | `"+alpha\nbeta"` | `tags ["alpha"]` | `tags ["alpha\nbeta"]` |
  | `"+\nbeta"` | `tags []`, `text ["+\nbeta"]`, `text_query "+\nbeta"` | `tags ["\nbeta"]`, `text []` |
  | `"+alpha\n"` | `tags ["alpha"]` | `tags ["alpha\n"]` |

- Correction: in the `+` branch, let `rest := arg[1:]` truncated at the first
  `"\n"`; take the branch only when that truncation is non-empty, appending the
  truncated value; otherwise fall through to the remaining branches so the raw
  argument lands in `text`. Do not reuse `HasPrefix`+`len(arg) > 1` as the
  guard — `"+\nbeta"` satisfies it but must not become a tag.
  The `@`, `/` and `-` branches need no change: `/\A@/`, `/\A\//` and `/\A-/`
  have no capture group, and `/\A-([ABC])\z/`'s `\z` is an absolute end anchor
  that Go's `len(arg) == 2` already reproduces (`"-A\n"` is an unknown flag in
  both).

## Finding 2 — High: invalid UTF-8 arguments raise in Ruby and are accepted by Go

- Ruby location: the `case arg` regex branches in `parse_cli`
  (`lib/tasks/task_queries.rb:103-107`). Matching a `Regexp` against a String
  whose bytes are not valid in its encoding raises
  `ArgumentError: invalid byte sequence in UTF-8`. No literal `when` string can
  equal such an argument, so every invalid-UTF-8 argv element reaches a regex
  and raises — with the same exception class adapters already report for
  `unknown flag:` and the scope errors.
- Go location: `query.ParseCLI` (`go/internal/query/filter.go:104`) performs no
  encoding validation; the argument is classified bytewise and stored.
- Evidence (the JSONL corpus cannot carry invalid UTF-8, which is why no
  existing case caught this — reproduce directly):

  ```
  ruby -e 'require_relative "lib/tasks/task_queries"
  ["+\xC3", "\xC3"].each do |a|
    begin; p Tasks::TaskFilter.parse_cli([a.dup.force_encoding("UTF-8")]).filter.tags
    rescue => e; p [e.class, e.message]; end
  end'
  ```

  Both arguments raise `[ArgumentError, "invalid byte sequence in UTF-8"]`.
  `query.ParseCLI([]string{"+\xC3"})` returns a filter with
  `tags ["\xC3"]`.
- Correction: reject before classifying. In `ParseCLI`'s loop, when
  `!utf8.ValidString(arg)`, return `fmt.Errorf("invalid byte sequence in UTF-8")`
  at the point that argument is reached — position matters, because Ruby raises
  inside the loop, before the post-loop `lifecycle_scopes.uniq` check
  (`["--done", "--archived", "\xC3"]` raises the encoding error, not
  `task lifecycle scopes are mutually exclusive`).
- Oracle gap this exposes: the corpus format cannot express these arguments.
  The repair tick must add a byte-level argv encoding to
  `porting/runners/cases/query-filter-parse.jsonl` and both probes (e.g. an
  `argv_base64` alternative to `argv`) and record the gap in the manifest
  until it does.

## Finding 3 — Medium: `text_query` uses Go's simple case mapping

- Ruby location: `Tasks::TaskFilter#text_query = text.join(" ").downcase`
  (`lib/tasks/task_queries.rb:137`). `String#downcase` applies full Unicode
  case mapping, including the multi-character mapping of `U+0130`.
- Go location: `Filter.TextQuery` — `strings.ToLower(strings.Join(filter.text, " "))`
  (`go/internal/query/filter.go:225`). `strings.ToLower` applies only the
  simple (1:1) mapping.
- Evidence: argv `["İ"]` gives Ruby `text_query "i̇"` (U+0069 U+0307) and
  Go `text_query "i"`. `["ΑΣ"]` agrees (`"ασ"`): Ruby's `downcase` does not do
  contextual final-sigma either, so no correction is needed there.
- Correction: apply the special-casing entry before `strings.ToLower` —
  `U+0130 → U+0069 U+0307` is the only unconditional multi-character lowercase
  mapping, so a targeted replacement over the joined string is sufficient and
  keeps the rest of `strings.ToLower` intact. Add the Ruby case to the corpus.

## Checked and found faithful

- Scope vocabulary, flag aliases and `--json`; the mutual-exclusion checks and
  their message text; the post-loop `uniq` scope check (a Go set is equivalent).
- `stateOrder` and `States()`'s per-scope lists against
  `Tasks::Check::{STATES,PROPOSED_STATES,OPEN_STATES,CLOSED_STATES}`
  (`lib/tasks/check.rb:23-26`), including the empty result when an explicit
  state is outside the scope.
- `priority`/`state` upcase: Ruby's `String#upcase` full mapping (`"ß" → "SS"`)
  can differ from `strings.ToUpper`, but no such value is accepted by either
  language and neither message interpolates the value, so the difference is
  unobservable here. Not a finding.
- Empty-string and bare `"@"`, `"+"`, `"/"` arguments; explicit-null scope; the
  unknown-keyword ordering and `Symbol#inspect` rendering at `febbdf6`.

## Commands

- `porting/runners/ruby/query-filter-parse-probe porting/evidence/query-filter-parse/source-fidelity-argv-cases-2026-08-02.jsonl`
- `cd go && go run ./cmd/query-filter-parse-probe ../porting/evidence/query-filter-parse/source-fidelity-argv-cases-2026-08-02.jsonl`
- 4 of the 5 cases differ; only `sf-text-query-final-sigma` matches.
