# Canonical emission — translation

Translated `Tasks::Format.dump` / `dump_record` into `go/internal/record/emit.go`
on 2026-08-02, against the oracle recorded in
[oracle-capture.md](oracle-capture.md). Ruby source pin
`68fdeea770a4afafde956594e2636c4dd46c11a8`; no drift.

## What was translated

| Ruby | Go |
| --- | --- |
| `KEY_ORDER` | `record.KeyOrder` |
| `LINE_KEY` | `record.LineKey` |
| `dump_record` | `record.DumpRecord` |
| `dump` | `record.Dump` |
| `omit?` | `omit` (nil / `""` / `[]` / `{}`) |
| `stringify` | not applicable: Go record keys are already strings |
| `nested_object` | **not translated** — owned by `format-nested-key-order` |

Nested objects re-serialize in source order. That is the honest boundary: this
slice owns top-level ordering only, and every in-scope fixture is already
canonical one level down, so the fixture dumps are byte-identical regardless.
The nested slice must revisit `encodeValue`'s object branch.

`encoding/json` is used to decode values, never to write them. Ruby's generator
leaves `<`, `>`, `&`, U+2028, and U+2029 verbatim while Go's encoder escapes
all five, so `encodeString` implements Ruby's escape set directly: the two
structural escapes, the five short control escapes, `\u00XX` below 0x20, and
every other byte verbatim (including DEL).

## Differential conformance

`go test ./internal/record/` dumps every fixture named in the manifest entry
and compares the SHA-256 against the hash Ruby produced in the oracle capture.
All eleven files match byte for byte, including `malformed/wrong-key-order`,
the one fixture Ruby rewrites (input `39f0fc8d…` → dump `9e94e743…`).

| Check | Result |
| --- | --- |
| gofmt / go vet | clean |
| `go test ./...` | pass |
| `go test -race ./internal/record/` | pass |
| Fixture dump bytes vs Ruby | 11/11 identical |
| Float spellings vs Ruby | 462/467 identical, 5 recorded divergences |

## Numbers: Ruby's generator, ported rather than approximated

Ruby's JSON generator does **not** use `Float#to_s` — `1e15` is `1e+15` to the
generator and `1.0e+15` to `Float#to_s`. The spelling rule was derived from the
oracle rather than from either assumption and captured by
[`porting/runners/ruby/format-emit-float-oracle`](../../runners/ruby/format-emit-float-oracle)
into [ruby/float-spellings.json](ruby/float-spellings.json) (467 literals,
ruby 4.0.6 / json 2.18.0):

- fixed integers always carry `.0`; exponential form has no `.0` for a
  single-digit mantissa (`1e+15`, not `1.0e+15`);
- integers print their digits, with `-0` normalized to `0`;
- `1e400` parses to `Float::INFINITY` and then *raises* `JSON::GeneratorError`,
  so `DumpRecord` returns a `*GeneratorError` rather than inventing a spelling.

A first pass derived the fixed-vs-exponential rule from the oracle samples — a
`[-9, 14]` decimal-exponent window over `strconv`'s shortest digits — and five
of the 467 literals diverged (kept in [float-divergences.json](float-divergences.json)
under `resolved.was`). Both causes were the same cause:

1. **Non-shortest digits.** Ruby's generator does not emit shortest digits:
   `1e23` prints as `9.999999999999999e+22`. Go's `strconv` is always shortest.
2. **No exponent window exists.** `1470498088023706.2` prints fixed while `1e15`
   prints exponential at the same decimal exponent, because the real rule is
   digit-count-dependent, not exponent-windowed.

The fix was to stop deriving the rule and port the generator. Ruby's JSON
extension calls `fpconv_dtoa` from its vendored `ext/json/ext/vendor/fpconv.c`
— Florian Loitsch's Grisu2 from <https://github.com/night-shift/fpconv>,
modified upstream to append `.0` to plain floats. It is transliterated into
[`go/internal/record/fpconv.go`](../../../go/internal/record/fpconv.go), C shape
and all, so a reviewer can diff it against the C by eye; the 87-entry
`powers_ten` table was extracted mechanically from that file, not retyped.
`rubyFloat` is now a call to it. Its emit rule, which the sampled window could
never have reproduced:

- `K >= 0 && exp < 15` → plain integer plus `.0`;
- `K < 0 && (K > -7 || exp < 10)` → fixed decimal;
- otherwise exponential,

where `K` is the decimal exponent of the generated digit string and
`exp = |K + ndigits - 1|`.

**Conformance.** All 467 captured literals now match Ruby byte for byte
(`TestFloatSpellingsMatchTheRubyOracle`, which also asserts the open divergence
list is empty so the file cannot quietly regain entries). Beyond the recorded
corpus, a randomized differential of **399,863** distinct doubles — half from
random 64-bit patterns, half from `mantissa × 10^e` for `e` in `[-320, 308]` —
was generated through ruby 4.0.6 / json 2.18.0 and compared with
`cmd/format-emit-float-diff`: **zero divergences**. That corpus is a
verification artifact, not a fixture, so it is not committed; the runner
`porting/runners/ruby/format-emit-float-oracle` reproduces the recorded 467.

One provenance caveat for the reviewer: the runtime oracle is json **2.18.0**
(a default gem inside ruby 4.0.6, shipped compiled, no C source on disk), while
the C that was read and ported came from json **2.21.1** vendored under
Homebrew. The 399,863-literal differential against the live 2.18.0 generator is
what establishes that the two agree for this code path; the version skew is
stated rather than assumed away.

## Not yet done for a high-risk slice

Fault injection at write boundaries, competing processes, and stress/budget
checks belong to `store-canonical-write`, which owns the write path; this slice
produces bytes in memory. That reading needs a reviewer's confirmation, not the
writer's. Two independent reviews are still outstanding.
