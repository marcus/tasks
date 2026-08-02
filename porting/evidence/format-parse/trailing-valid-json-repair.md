# Format-parse trailing valid JSON repair

Date: 2026-08-02

Independent review found that `decode` accepted a successfully decoded second
JSON value by returning its nil error. Ruby instead rejects the entire line.
This is a Go defect: Ruby's direct oracle reports the following result for the
new fixture text `{"a":1} []`:

```text
invalid JSON: unexpected token at end of stream '[]' at line 1 column 9
```

`decode` now treats a successful second decode as an error, allowing the
existing source-aware diagnostic adapter to retain the Ruby wording. The new
fixture is included in the direct Ruby-vs-Go conformance corpus; no expected
result was derived from Go output.

## Checks run

```console
$ (cd go && go test ./...)
ok   tasks-go/internal/record

$ (cd go && go test -race ./...)
ok   tasks-go/internal/record

$ (cd go && go test -fuzz=FuzzParseKeepsPhysicalLineBounds -fuzztime=3s ./internal/record)
PASS

$ (cd go && go vet ./...)

$ ruby test/test_format.rb -n '/test_parse_stamps_correct_line_numbers|test_lenient_parse_skips_bad_lines_and_reports_them|test_scalar_line_reports_its_type|test_parse_empty_string_yields_nothing|test_parse_lone_newline_is_one_blank_line_error|test_blank_line_between_records_reported_with_line_number|test_trailing_newline_does_not_create_a_phantom_record|test_leading_bom_is_stripped_and_line_one_parses/'
8 runs, 31 assertions, 0 failures, 0 errors, 0 skips

$ porting/evidence/format-parse/conformance
format-parse direct conformance: 12/12 cases matched

$ porting/compare/validate porting/evidence/format-parse/ruby
validate: 11/11 observations valid against observations.schema.json and internally coherent
```

## Remaining

This closes only the trailing-valid-value defect. The source-fidelity review's
remaining malformed-diagnostic taxonomy and ordered-record representation
findings still block medium-risk approval.
