# Source-fidelity review — query-filter-parse at 84df4c5

**Verdict: FAIL.** Three Go defects, all in `query.InspectSymbol`'s bare-vs-quoted
decision and its quoted-form escaping. One of the three is a regression
introduced by 84df4c5 itself. The three attacks the handoff directed at this
review — `rubyFloatToS`'s `point == 16` rule, the `rubyPrintable` table's
provenance, and `query.DecodeValue`'s repeated-key and non-object handling — all
pass, and are recorded below with the evidence that clears them.

- Reviewer session: `ses_187ec8`. Independent of `ses_529e17` (the repair) and of
  every session the handoff excluded.
- Subject: `go/internal/query/coerce.go`, `go/internal/query/inspect_printable.go`,
  `go/cmd/query-filter-parse-probe/main.go` at 84df4c5 on `port/query-filter-parse`.
- Oracle: Ruby 4.0.6, `lib/tasks/task_queries.rb` at `87d8cc201410`, driven
  through `porting/runners/ruby/query-filter-parse-probe`.
- Baseline reproduced before any new work: `porting/evidence/query-filter-parse/conformance`
  → 76/76.

Every differential below compares **parsed** probe output, never bytes. Ruby's
`JSON.generate` emits U+2028 and U+2029 raw where Go's encoder escapes them, so
a byte diff reports a divergence for a value the two probes agree on. This is a
property of the harness, not of the port; the committed `conformance` script
already compares structurally and must not be changed to a byte diff.

---

## Finding 1 — `Symbol#inspect` quotes C0 and DEL with `\xNN`, not `\uNNNN` (regression at 84df4c5)

**File:** `go/internal/query/coerce.go`, `InspectSymbol` → `inspectString`.

Ruby's `Symbol#inspect` and `String#inspect` do not share an escape vocabulary.
For the 25 C0/DEL codepoints that have no named escape, a String uses `\uNNNN`
and a Symbol uses `\xNN`:

```
U+0001   String#inspect "\u0001"   Symbol#inspect :"\x01"
U+007F   String#inspect "\u007F"   Symbol#inspect :"\x7F"
```

`InspectSymbol` delegates its quoted form to `inspectString`. Before 84df4c5,
`inspectString` emitted `\x%02X` for `character < 0x20 || character == 0x7f`,
which was wrong for `String#inspect` and right for `Symbol#inspect`. The
coercion review correctly required the String spelling; the repair changed the
shared helper, so it fixed String and broke Symbol in the same edit. No case in
the committed corpus covers a C0 keyword name, so 76/76 stayed green.

**Correction.** Symbol's quoted form needs its own escape step: named escapes and
the `#` rule as now, `\xNN` for `character < 0x20 || character == 0x7F`, and the
`\uNNNN` / `\u{NNNNN}` forms from `inspectString` for everything else. The two
callers must stop sharing one function, or the shared function must take the
C0 spelling as a parameter.

**Evidence.** `source-fidelity-symbol-{cases,ruby,go}-2026-08-02.jsonl`, cases
`sym-c0-*` (25 single-codepoint cases plus `sym-c0-embedded`). `sym-named-escapes`
passes, which pins that `\a \b \t \n \v \f \r \e` are shared and must stay shared.

## Finding 2 — a non-printable non-ASCII codepoint is not an identifier character

**File:** `go/internal/query/coerce.go`, `identifier`.

```go
case character == '_' || unicode.IsLetter(character) || character > unicode.MaxASCII:
```

The comment justifies the `> MaxASCII` arm with `:é?` and `:αβ`, which do print
bare. But it admits every non-ASCII codepoint, including the ones Ruby will not
put in a bare symbol:

```
U+0080   Ruby :"\u0080"   Go :<the raw U+0080 byte sequence, unquoted>
U+2028   Ruby :"\u2028"   Go :<the raw U+2028 byte sequence, unquoted>
U+FFFE   Ruby :"\uFFFE"   Go :<the raw U+FFFE byte sequence, unquoted>
```

An exhaustive sweep of all 1,112,064 non-surrogate codepoints as single-character
keyword names gives **814,763 divergent codepoints** in this class.

**Correction, stated exactly.** The divergent set is precisely the set of
non-ASCII codepoints that `String#inspect` escapes, **less U+0085**. U+0085 is
the single codepoint that `String#inspect` escapes and `Symbol#inspect` still
prints bare (`"\u0085"` as a String, `:<raw U+0085>` as a Symbol), so the rule is
`unicode.Is(rubyPrintable, character) || character == 0x85`, not
`unicode.Is(rubyPrintable, character)` alone. I verified this by set equality
against the fresh exhaustive capture, not by inference: the go-bare/Ruby-quoted
set and the non-ASCII escaped set differ by exactly `{0x85}` and nothing else.

Whether U+0085 deserves its own named constant or a comment pointing at this
review is the writer's call; the behavior is not negotiable.

**Evidence.** Cases `sym-nonprintable-*` and `sym-nonprintable-embedded` fail;
`sym-nel-bare` (U+0085 alone, leading, and trailing) and
`sym-printable-nonascii` (U+2192, U+2460, U+00E9, U+3042, U+1F600, U+00AD,
U+200B, U+FEFF, U+1D173) pass and must keep passing after the fix.

## Finding 3 — the single-character global fallback admits twelve names Ruby rejects

**File:** `go/internal/query/coerce.go`, `globalName`.

```go
return len([]rune(name)) == 1
```

Ruby's special globals are a fixed vocabulary, not "any one character". Sweeping
`$` + every codepoint shows Go prints bare, and Ruby quotes, for exactly these
twelve ASCII characters:

```
(space) # % ( ) - [ ] ^ { | }
```

and Ruby accepts exactly these twenty:

```
! " $ & ' * + , . / : ; < = > ? @ \ ` ~
```

**Correction.** Replace the length test with that twenty-character set. The
digit-global branch (`$1`, `$99`) and the identifier branch (`$foo`, `$_`) are
correct and must not change; `$` alone stays quoted. Non-ASCII after `$` is
governed by Finding 2's rule, not by this one.

**Evidence.** Cases `sym-global-reject-*` (12, all failing) and
`sym-global-accept-*` (20, all passing), plus `sym-global-digits` and
`sym-global-nonprintable`.

---

## The three directed attacks — all pass

### (a) `rubyFloatToS`'s `point == 16` rule and the exponent/fraction layout

Confirmed correct, and confirmed load-bearing.

The rule is not an artifact. Ruby:

```
1234567890123456.7  →  1234567890123456.8      point 16, a fractional digit remains → fixed
1234567890123456.0  →  1.234567890123456e+15   point 16, no fractional digit        → exponent
12345678901234567.0 →  1.2345678901234568e+16  point 17                             → exponent
```

The coercion review's proposed correction (`exp >= 16` → exponent unconditionally)
would have misrendered the first of those, so the handoff was right to treat the
capture as the oracle over the review text.

A 41,786-literal adversarial corpus — targeted layout boundaries at points 15,
16, and 17, the `0.000ddd`/exponent cut at points 0 through −5, fixed and
exponent spellings of the same value, integers past int64, `-0`, the saturating
`1.0e400`/`1e-400` literals, subnormals, and 40,000 deterministic random doubles
— runs **0 mismatches**. Mutation check: deleting the `point == 16` clause kills
34 cases, so the corpus would catch its removal.

Generator: `review-2026-08-02-symbol-inspect/generate-float-sweep.rb`.

### (b) The `rubyPrintable` table's provenance

Regenerated rather than trusted. A fresh exhaustive `String#inspect` capture
(814,799 escaped codepoints) fed through the committed
`review-2026-08-02-coercion/generate-printable-table.rb` produces a file
**byte-identical** to the committed `go/internal/query/inspect_printable.go`.

Independently of the table, an exhaustive `String#inspect` differential over all
1,112,064 non-surrogate codepoints — each nested one Array deep inside `text`,
which is the only path that reaches `inspectString` — runs **0 mismatches**, and
is byte-identical besides. Generator:
`review-2026-08-02-symbol-inspect/generate-inspect-sweep.rb`.

Note for the writer: the table is correct for `String#inspect` and is *not* the
right predicate for `Symbol#inspect` on its own — see Finding 2.

### (c) `query.DecodeValue`'s repeated-key and non-object handling

Repeated keys are correct. Ruby's `Hash#[]=` keeps a repeated key in its first
position while taking the later value, and `Object.index` implements exactly
that. Fourteen cases covering a repeated scalar, collection, boolean, unknown
keyword (both alone and mixed with a known one), a repeated key inside a nested
Hash, a repeated key at depth, and empty/`null`/boolean nesting all match. The
oracle is unambiguous: `{"b":1,"a":2,"b":3}` renders `["b", 3], ["a", 2]`.

Non-object kwargs is a **harness asymmetry, not a Go defect**. For a String,
Array, Integer, `null`, or `true`, Go answers `ArgumentError: kwargs is not an
object`, while the Ruby probe dies with `NoMethodError` from its own
`transform_keys` before it reaches `TaskFilter.new`. Ruby's subject cannot be
called with a non-Hash — `**` requires one — so there is no ported behavior here
to diverge from. It is worth recording only because the differential harness
cannot express this case class at all: no conformance case may be written for
it, and a future reviewer should not read its absence as coverage.

## One more sweep, run unprompted

`priority` and `state` go through `strings.ToUpper` against Ruby's full-Unicode
`String#upcase`. An exhaustive comparison finds **157 divergent codepoints**
(86 where Ruby expands one codepoint to two, 16 to three, 55 simple mappings
Go's Unicode 15.0.0 tables lack). None is observable: no divergent codepoint's
Ruby image or Go image is a substring of `A`, `B`, `C`, or of any name in
`STATE_ORDER`, so no string can be accepted by one runtime and rejected by the
other, and an invalid value raises before it can be read back. This independently
confirms the claim the coercion review recorded. The coupling itself is real and
belongs to any later slice that upcases — the same standing note the
`newerUnicodeLower` repair already carries for downcasing.

The raw (un-inspected) `text_query` downcase path was also swept over every
codepoint as a bare `text` element: **0 mismatches**, confirming the
`newerUnicodeLower` repair at this commit.

## What the next tick owes

1. Repair Findings 1, 2, and 3 in `coerce.go`. All three are in
   `InspectSymbol`'s neighbourhood and should land together; mid tier.
2. Fold `source-fidelity-symbol-cases-2026-08-02.jsonl` into
   `porting/runners/cases/query-filter-parse.jsonl` so the corpus stops being
   blind to symbol rendering.
3. Record in the manifest that the symbol oracle was 73 hand-written name shapes
   and is now swept exhaustively; that gap is what hid three defects through four
   reviews.
4. A fresh independent source-fidelity review at the repaired commit, plus the
   Go-idiom re-confirmation that still predates every commit since f0f8dc1.
