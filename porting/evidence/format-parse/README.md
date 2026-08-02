# `format-parse` oracle capture

This directory holds the Ruby oracle for manifest slice `format-parse`, captured
from source revision `68fdeea770a4afafde956594e2636c4dd46c11a8`.

`ruby/` is produced by the language-neutral runner protocol from
[`porting/runners/cases/format-parse.jsonl`](../../runners/cases/format-parse.jsonl).
It covers every manifest fixture and pins the CLI-visible parse outcomes. The
focused Ruby test run covers direct `Tasks::Format.parse` boundaries that no CLI
surface exposes independently: physical line stamps, empty string, lone newline,
and a trailing newline that adds no phantom record.

## Verification

At capture time the focused Ruby selection passed: 8 runs, 31 assertions, 0
failures, 0 errors, 0 skips.

```sh
ruby test/test_format.rb -n '/test_parse_stamps_correct_line_numbers|test_lenient_parse_skips_bad_lines_and_reports_them|test_scalar_line_reports_its_type|test_parse_empty_string_yields_nothing|test_parse_lone_newline_is_one_blank_line_error|test_blank_line_between_records_reported_with_line_number|test_trailing_newline_does_not_create_a_phantom_record|test_leading_bom_is_stripped_and_line_one_parses/'
porting/evidence/capture --out porting/evidence/format-parse/ruby --cases porting/runners/cases/format-parse.jsonl --work /tmp/tasks-format-parse
porting/compare/validate porting/evidence/format-parse/ruby
```

The next step is **medium-risk translation** in a different session: implement
only `internal/record` parsing against these observations, then compile and run
differential conformance. It must preserve Ruby's total parsing behavior and
error wording; it must not treat the successful `wrong-key-order` check as a
reason to canonicalize or reject input on read.
