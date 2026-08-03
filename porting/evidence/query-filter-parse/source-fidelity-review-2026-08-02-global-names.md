# Source-fidelity review — query-filter-parse at c2dbd9e — **FAIL**

- **Slice:** query-filter-parse (td-8ebaa6), risk medium
- **Reviewed commit:** c2dbd9e "porting: repair query-filter-parse Symbol#inspect
  escaping and bare-name rules" on `port/query-filter-parse`
- **Ruby oracle:** ruby 4.0.6 (2026-07-14 revision 03b6d3f889) +PRISM, arm64-darwin23
- **Reviewer session:** ses_c25489. Independent of every session named in the
  previous handoff's exclusion list, and of the writer sessions.
- **Verdict:** FAIL. Two confirmed defects in `globalName`
  (`go/internal/query/coerce.go:189`). Everything else attacked in this pass
  matches the oracle byte for byte.
- **Evidence:** `review-2026-08-02-global-names/` beside this file — three
  differential corpora with their Ruby and Go captures, plus two Ruby
  characterisation probes and their recorded output.

The review took the direction the c2dbd9e handoff asked for: the previous
`Symbol#inspect` sweep was exhaustive over *single-codepoint* names for the four
sigils, so this pass attacked what a single codepoint cannot express —
multi-codepoint names behind a sigil — plus `ParseCLI`'s regex branches and
`filter.go`'s state-intersection vocabulary, which no review had re-read since
4df99f7.

Both defects live exactly in that gap: each needs **two or more** characters
after `$`, so the 4,448,256-name sweep could not have found either.

---

## Finding 1 — `$-x` globals print bare in Ruby; Go quotes them

- **File / function:** `go/internal/query/coerce.go:189`, `globalName`
- **Ruby source:** `is_special_global_name`'s `'-'` branch, reached from
  `rb_enc_symname_type`'s `case '$'`; observable through
  `TaskFilter#initialize`'s unknown-keyword `ArgumentError`.
- **Severity:** observable output divergence on a documented slice output
  ("argument rejection message").

Ruby treats `$-` followed by **exactly one** identifier character as a special
global name and prints it bare. Go's `globalName` has no `-` branch at all: the
identifier test fails, the all-digits test fails, and the final test requires a
single character present in `specialGlobals` — `-` is not in that set and the
name is two characters anyway — so every `$-x` name takes the quoted form.

Confirmed differentially (`review-2026-08-02-global-names/symbol-cases.jsonl`,
cases `sym-000`..`sym-003`, `sym-020`, `sym-022`, `sym-024`):

| name | Ruby | Go |
|---|---|---|
| `$-w` | `unknown keyword: :$-w` | `unknown keyword: :"$-w"` |
| `$-1` | `unknown keyword: :$-1` | `unknown keyword: :"$-1"` |
| `$-_` | `unknown keyword: :$-_` | `unknown keyword: :"$-_"` |
| `$-é` | `unknown keyword: :$-é` | `unknown keyword: :"$-é"` |
| `$-À` | `unknown keyword: :$-À` | `unknown keyword: :"$-À"` |
| `$-あ` | `unknown keyword: :$-あ` | `unknown keyword: :"$-あ"` |
| `$-😀` | `unknown keyword: :$-😀` | `unknown keyword: :"$-😀"` |

**The exact rule**, characterised against the oracle in
`review-2026-08-02-global-names/probe-globals.txt` and
`probe-globals-printability.txt`:

`$-X` prints bare iff `X` is exactly one character *and* `X` is an identifier
character *and* the whole name passes the printability predicate:

- ASCII: `X` is a letter, a digit, or `_`. `$-a`, `$-Z`, `$-0`, `$-9`, `$-_`
  bare; `$-!`, `$- `, `$-.`, `$--`, `$-$` quoted.
- Non-ASCII: any character, subject to printability — the *same* predicate
  `symbolPrintable` already implements, U+0085 exception included.
  `$-`, `$- `, `$-​`, `$-�`, `$-\u{E0001}` bare;
  `$-`, `$-`, `$-͸` quoted, matching those characters' solo
  bare/quoted answers one for one.
- Length is strict: `$-` alone and `$-ab` are quoted.

**Correction:** give `globalName` a `-` branch that reproduces that rule, reusing
`symbolPrintable` for the non-ASCII half rather than a second copy of the
predicate. `$-\x01` and `$-\x7F` need no special case — they fall out of the
ASCII identifier-character test.

---

## Finding 2 — `$00`, `$01`, `$0123` print quoted in Ruby; Go prints them bare

- **File / function:** `go/internal/query/coerce.go:196`, `globalName`'s
  all-digits test
- **Ruby source:** `is_special_global_name`. `'0'` is a member of Ruby's
  `SPECIAL_PUNCT` bitmap, so a leading `0` takes the **punct** branch, which
  demands the name end after that one character. The digit branch is reached
  only by `1`–`9`.
- **Severity:** same class as Finding 1, opposite direction — Go emits an
  unquoted form Ruby would never produce.

Go's `strings.TrimLeft(name, "0123456789") == ""` accepts any digit string,
including one with a leading zero.

Confirmed differentially (cases `sym-009`..`sym-011`):

| name | Ruby | Go |
|---|---|---|
| `$00` | `unknown keyword: :"$00"` | `unknown keyword: :$00` |
| `$01` | `unknown keyword: :"$01"` | `unknown keyword: :$01` |
| `$0123` | `unknown keyword: :"$0123"` | `unknown keyword: :$0123` |

**The exact rule** (`probe-globals.txt`, second block): a digit global prints
bare iff the name is the single character `0`, or is all digits with a nonzero
first digit. `$0`, `$1`, `$9`, `$10`, `$19`, `$90`, `$190` bare; `$00`, `$01`,
`$09` quoted. Mixed names `$0a` and `$1a` are quoted in both implementations
already.

**Correction:** require `name == "0" || (all digits && name[0] != '0')`.
Equivalently, add `0` to `specialGlobals` — where Ruby actually keeps it — and
restrict the digit branch to a nonzero leading digit. Note that `specialGlobals`
being twenty characters rather than Ruby's twenty-one is *not* itself a defect
today: `$0` is bare either way. It becomes one only if the digit branch is
narrowed without moving `0` across.

---

## What was attacked and holds

All three corpora were run through the committed probes unchanged —
`porting/runners/ruby/query-filter-parse-probe` and
`go/cmd/query-filter-parse-probe` — so every result below is a real
implementation-to-implementation diff, not a hand-read of source.

1. **Multi-codepoint symbol names** — 96 cases, 86 match, the 10 mismatches
   being Findings 1 and 2. Everything else in that corpus is clean: multi-rune
   identifiers (`αβ`, `éa`, `aé`, `a\u{1F600}b`), the trailing `?`/`!`/`=`
   suffix rules and their rejections (`a?=`, `a==`, `a!!`, `a?!`), `@`/`@@`
   names and their digit and empty rejections, the multi-character operator
   table (`<=>`, `<<`, `===`, `[]=`, `+@`, `-@`) together with the forms Ruby
   quotes (`~@`, `!@`, `=`, `**=`, `<=>=`, `[]==`), and the non-ASCII bare cases
   `ﬀ`, `ß`, `İ`, U+200B.
   Files: `symbol-cases.jsonl`, `symbol-ruby.jsonl`, `symbol-go.jsonl`.

2. **`ParseCLI`'s regex branches at c2dbd9e** — 68 argv cases, 110/110 with the
   constructor cases below. Covered: `/\A-([ABC])\z/` including the `\z`-not-`\Z`
   distinction (`-A\n` is an unknown flag, `\n-A` is text), `-ABC`, `-A=`;
   `/\A\+(.+)/`'s newline behaviour in every position (`+a\nb`, `+\nb`, `+\n`,
   `+ab\ncd\nef`, `++a`, `+a\rb`, bare `+`); `/\A@/` and `/\A\//` including the
   empty captures `@` and `/`; branch precedence (`/@a`, `/-A`, `/+t`); the
   unknown-flag branch (`-`, `--`, `---`, `--nope`) and its ordering against a
   later valid flag; last-wins scope with duplicate and conflicting scopes; and
   the post-loop mutually-exclusive-scopes check.
   Files: `cli-cases.jsonl`, `cli-ruby.jsonl`, `cli-go.jsonl`.

3. **`filter.go`'s state-intersection vocabulary** — 42 constructor cases, all
   matching. `stateOrder` and the three scoped vocabularies are hand-copied
   `Tasks::Check` literals; they are re-verified against `check.rb:23-26` by
   reading and against the oracle by running: every scope crossed with an
   in-scope state, an out-of-scope state (the empty intersection), and no state;
   scope and state and priority case folding; the three rejection messages.
   Same files as (2), cases `new-000`..`new-041`.

4. **`quoteRuby`'s `hexC0` parameter** — the restructure at c2dbd9e makes one
   boolean the only thing separating the two escape vocabularies, so both were
   re-run over the same characters in the same corpus: 206 cases, all matching.
   Every C0 code point, DEL, U+0085, U+00A0, U+00AD, U+200B, U+2028, U+FEFF,
   U+FFFD, U+0300, U+E000, U+1F600 and U+10FFFF, alone and embedded, each sent
   down the String path (a Hash key rendered by `Hash#to_s`) *and* the Symbol
   path (an unknown keyword name); plus the named escapes, the `#{`/`#$`/`#@`
   rule and its non-triggering neighbours, `"` and `\`.
   Files: `escape-cases.jsonl`, `escape-ruby.jsonl`, `escape-go.jsonl`.

5. **`String#upcase` versus `strings.ToUpper`** — checked, and deliberately
   *not* filed as a defect. `filter.go:77` and `filter.go:84` use Go's simple
   1:1 uppercase where Ruby applies full Unicode case mapping from newer tables,
   the mirror of the divergence `newerUnicodeLower` exists to fix on the
   downcase side. It is unobservable here: an upcased priority or state survives
   only by equalling an ASCII literal (`A`/`B`/`C`, the seven state names), and
   no divergent mapping produces one — probed with `ß`, `ﬀ`, `ı`, `İ`
   (cases `new-030`..`new-035`), all matching. Recording it so a later slice
   that renders an upcased value, rather than only testing it, knows the
   asymmetry with `TextQuery` is known and bounded, not overlooked.

## Not covered by this pass

- The Go-idiom review is still owed and still predates c2dbd9e; nothing here
  judges Go idiom, including whether `quoteRuby`'s boolean parameter should be
  two escapers.
- Both standing ownership questions are untouched and still Marcus's:
  whether `CoerceStrings`/`CoerceString`/`CoerceBool`/`InspectSymbol`/
  `DecodeValue` are exported `internal/query` surface or probe scaffolding, and
  the non-object-kwargs harness asymmetry that no conformance case can express.
- The `$-X` and leading-zero rules are characterised here by sample, not swept.
  The repair tick should fold these cases into
  `porting/runners/cases/query-filter-parse.jsonl` and extend
  `generate-symbol-sweep.rb` with a `$-` + single-codepoint sigil so the whole
  range is proved, the way the four existing sigils are.
