# `query-filter-parse` repair — the three `parse_cli` regex divergences

Repairs all three findings of
`source-fidelity-review-2026-08-02-parse-cli-regexes.md` (session `ses_01941e`,
reviewed at `febbdf6`). Session `ses_89cbf2`. Ruby was captured first, before
anything in `go/` was edited, against pinned source
`87d8cc201410669e5b4ed1987eb44a01946ae92f:lib/tasks/task_queries.rb`.

Every case below is now in the committed corpus
(`porting/runners/cases/query-filter-parse.jsonl`), so the divergences stay
proved rather than argued: **60/60 direct conformance**, up from 49/49.

## Oracle gap closed: `argv_base64`

Finding 2's arguments cannot be written in JSONL — the corpus format was the
reason no existing case caught the divergence. A `parse_cli` case may now give
`argv_base64` (one base64 string per argument, decoded to raw bytes and, in
Ruby, `force_encoding("UTF-8")`) instead of `argv`. Both probes accept it;
`argv` is unchanged and still the normal form.
`query-filter-argv-base64-equals-argv` pins that the two encodings agree on an
argument expressible in both.

## Finding 1 — `+tag` newline boundary

`go/internal/query/filter.go`: the `+` case now goes through `tagArgument`,
which truncates at the first `"\n"` and declines the branch when the truncation
is empty, so the raw argument falls through to `text` exactly as Ruby's
non-matching `/\A\+(.+)/` does. The old `len(arg) > 1` guard was the specific
thing the review warned against reusing.

| case | argv | Ruby = Go |
|---|---|---|
| `query-filter-tag-embedded-newline` | `"+alpha\nbeta"` | `tags ["alpha"]` |
| `query-filter-tag-leading-newline` | `"+\nbeta"` | `text ["+\nbeta"]`, `tags []` |
| `query-filter-tag-trailing-newline` | `"+alpha\n"` | `tags ["alpha"]` |
| `query-filter-tag-only-newline` | `"+\n"` | `text ["+\n"]`, `tags []` |

## Finding 2 — invalid UTF-8 arguments

`ParseCLI` now rejects with `invalid byte sequence in UTF-8` at the top of the
loop body, per argument, before classification. Position was the review's point
and both orderings are now pinned by a case rather than by reasoning:

| case | argv bytes | Ruby = Go |
|---|---|---|
| `query-filter-invalid-utf8-tag` | `"+\xC3"` | `ArgumentError: invalid byte sequence in UTF-8` |
| `query-filter-invalid-utf8-bare` | `"\xC3"` | same |
| `query-filter-invalid-utf8-precedes-scope-check` | `--done --archived "\xC3"` | the encoding error, **not** `task lifecycle scopes are mutually exclusive` |
| `query-filter-invalid-utf8-follows-unknown-flag` | `-z "\xC3"` | `unknown flag: -z` — the earlier argument still wins |

An invalid-UTF-8 argument can never equal one of the literal `when` strings, so
checking every argument is equivalent to Ruby reaching a regex with it; the
literal branches are unreachable for such an argument in both languages.

## Finding 3 — `text_query` full Unicode downcase

`Filter.TextQuery` replaces `U+0130` with `U+0069 U+0307` before
`strings.ToLower`, the review's targeted correction: it is the only character
with an unconditional multi-character lowercase mapping, so the rest of
`strings.ToLower` is left intact.

| case | argv | Ruby = Go |
|---|---|---|
| `query-filter-text-query-dotted-capital-i` | `"İ"` | `text_query "i̇"` |
| `query-filter-text-query-final-sigma` | `"ΑΣ"` | `text_query "ασ"` (unchanged; Ruby does no contextual final sigma) |

## Reproduction

- `porting/evidence/query-filter-parse/conformance` → `60/60 cases matched`
- The review's own five cases now agree:
  `porting/runners/ruby/query-filter-parse-probe porting/evidence/query-filter-parse/source-fidelity-argv-cases-2026-08-02.jsonl`
  vs `cd go && go run ./cmd/query-filter-parse-probe ../porting/evidence/query-filter-parse/source-fidelity-argv-cases-2026-08-02.jsonl`
- `cd go && gofmt -l . && go vet ./... && go test -race ./...` — clean.

## Not claimed here

The review's verdict is repaired, not re-passed: a fresh independent
source-fidelity review at this commit is still owed, as is the Go-idiom
re-confirmation that predates `f0f8dc1`/`46f58ca`/`febbdf6`. The three
pre-existing oracle gaps in the manifest (`String#inspect` escaping in coerced
collection elements, top-level-Hash `Kernel#Array`, float and large-integer
elements) are untouched by this repair.
