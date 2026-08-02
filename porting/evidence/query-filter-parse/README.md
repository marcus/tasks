# Query-filter parse oracle capture

This medium-risk slice is a pure `argv`/constructor-to-`TaskFilter` boundary;
it has no task-store input or filesystem effect. The capture therefore uses the
reproducible direct probe instead of the fixture-copy runner.

```sh
porting/runners/ruby/query-filter-parse-probe \
  porting/runners/cases/query-filter-parse.jsonl \
  > porting/evidence/query-filter-parse/ruby.jsonl
ruby test/test_task_queries.rb
```

`ruby.jsonl` records every observable filter field, the derived `states` and
`text_query` values, `--json`, and exact `ArgumentError` messages for rejected
inputs. It is Ruby oracle evidence only; no Go output has been compared or
blessed.
