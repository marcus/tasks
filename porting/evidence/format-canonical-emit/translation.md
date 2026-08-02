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

## Numbers: an open Go defect, not an accepted difference

Ruby's JSON generator does **not** use `Float#to_s` — `1e15` is `1e+15` to the
generator and `1.0e+15` to `Float#to_s`. The spelling rule was derived from the
oracle rather than from either assumption and captured by
[`porting/runners/ruby/format-emit-float-oracle`](../../runners/ruby/format-emit-float-oracle)
into [ruby/float-spellings.json](ruby/float-spellings.json) (467 literals,
ruby 4.0.6 / json 2.18.0):

- fixed notation while the decimal exponent is in `[-9, 14]`, exponential
  outside it;
- fixed integers always carry `.0`; exponential form has no `.0` for a
  single-digit mantissa (`1e+15`, not `1.0e+15`);
- integers print their digits, with `-0` normalized to `0`;
- `1e400` parses to `Float::INFINITY` and then *raises* `JSON::GeneratorError`,
  so `DumpRecord` returns a `*GeneratorError` rather than inventing a spelling.

Five of the 467 literals still diverge, recorded in
[float-divergences.json](float-divergences.json) and pinned by the test suite
so neither a new divergence nor a silent fix can pass unnoticed. Two causes:

1. **Non-shortest digits.** Ruby's vendored Grisu2 is not always optimal:
   `1e23` prints as `9.999999999999999e+22`. Go's `strconv` is always shortest.
2. **The decpt 16–17 boundary.** `1470498088023706.2` (17 digits, decimal point
   after 16) prints fixed in Ruby while `1e15` and `8.642975318642975e+15`
   print exponential at the same decimal exponent. The `[-9, 14]` window does
   not explain those cells; the remaining discriminator was not identified.

Both are one fix: port the digit generator and emit rule json 2.18 actually
uses instead of deriving the rule from samples. That is the next tick's work
and this slice cannot be approved before it lands. No float field exists in the
schema — floats reach the emitter only through forward-compatible unknown keys
— so the blast radius is small, but "small" is not "blessed".

## Not yet done for a high-risk slice

Fault injection at write boundaries, competing processes, and stress/budget
checks belong to `store-canonical-write`, which owns the write path; this slice
produces bytes in memory. That reading needs a reviewer's confirmation, not the
writer's. Two independent reviews are still outstanding.
