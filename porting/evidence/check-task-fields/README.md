# Ruby oracle capture — check-task-fields

Captured on 2026-08-02 from Ruby at source revision `174165be85c9`. No Go
implementation exists for this slice yet and none was exercised; nothing here
is derived from Go output.

```console
$ porting/runners/ruby/run --out porting/evidence/check-task-fields/ruby \
    porting/runners/cases/check-task-fields.jsonl
```

Fourteen observations, seven stores observed twice each: once on the human
surface (summary line and exit status) and once with `--json`, which is the
`(line, message)` shape the later differential comparator pairs against. No case
mutated its copy.

| Case | Fixture | Exit | Owned outcome |
|---|---|---|---|
| `wrong-types` | `malformed/wrong-types` | 1 | the field-rule corpus: state, priority, title, dates, tags, recur, closed-on-open, update stamp |
| `recur-non-canonical` | `malformed/recur-non-canonical` | 1 | 27 rejected cookies, one message shape |
| `recur-calendar-grammar` | `valid/recur-calendar-grammar` | 0 | `ok — 20 tasks parsed, no structural errors` |
| `full-field-matrix` | `valid/full-field-matrix` | 0 | `ok — 32 tasks parsed, no structural errors` |
| `duplicate-open-titles` | `malformed/duplicate-open-titles` | 0 | one warning, exit 0, count in the summary line |
| `duplicate-closed-titles` | `valid/duplicate-closed-titles` | 0 | no warning: closed/proposed carriers do not participate |
| `unknown-keys` | `compat/forward-compat-unknown-keys` | 0 | 3 warnings: unknown top-level and unknown delegation key |

## Missing oracle coverage, filled

`test/test_check.rb#test_duplicate_done_titles_do_not_warn` is declared for this
slice and no fixture in the corpus carried repeated *closed* titles — every
duplicate-title fixture exercised only the positive half. `valid/duplicate-closed-titles`
was added for the negative half: two `DONE` and one `CANCELLED` task sharing a
title, two `PROPOSED` tasks sharing another, and a fourth open carrier of the
first title so a port that ignores `OPEN_STATES` warns on a group of four while
Ruby is silent. Ruby's recorded outcome, `ok — 6 tasks parsed, no structural
errors` with `{"ok":true,"errors":[],"warnings":[]}`, is captured here and in
the fixture README.

## What this slice owns, and what it does not

`Check#check_task` calls into rules other slices own, and those diagnostics
appear in the same reports. The comparator built in the translation step must
extract only this slice's entries, exactly as `check-meta-and-ids/compare.rb`
does, rather than claiming a neighbour's behavior:

- **Owned** — `invalid state`, `invalid priority`, `task has no title` /
  `title must be a string`, `<key> … is not a YYYY-MM-DD date` /
  `is not a real date` for `scheduled`/`deadline`/`closed`/`archived`,
  `invalid recur cookie`, `closed date on an open task` /
  `on a proposed task`, `tags must be an array` / `must all be strings`,
  `updated … is not an RFC3339 UTC timestamp with device slug`, the
  `unknown key` and `unknown delegation key` warnings, and the
  `duplicate open title` warning.
- **Not owned** — `check_lead` (`lead …`, `lead_skip …`) and
  `check_temporal_time` (`scheduled_time …`) are campaign 5 grammars;
  `malformed id` / `record missing id` / `duplicate id` / meta errors belong to
  check-meta-and-ids; `delegation.*` shape messages belong to
  delegation-record-shape; `section must not carry` / `unknown record type` /
  parent and DFS errors belong to check-tree-structure and
  check-report-and-cli. All of these are present in the captured stdout as
  context and must not be attributed here.

## Traps the capture pins

- **Errors and warnings differ in exit status.** `duplicate-open-titles` and
  `unknown-keys` carry diagnostics and still exit 0, with the warning count in
  parentheses on the `ok —` line. Conflating the two channels is the expected
  defect.
- **Recur fidelity is `cookie?` fidelity only.** All 27 rows of
  `recur-non-canonical` produce one fixed message that varies only by the
  inspected value; `Recur.parse_result`'s specific reasons are discarded by
  `check`. Reproducing the richer reasons here would be a divergence, not an
  improvement (see the manifest's oracle gaps).
- **Whitespace padding.** `" w:mon"` and `"w:mon "` are accepted by
  `Recur.cookie?` (it strips) and rejected by `check_task`'s `rc == rc.strip`
  guard. A port that validates by calling its own parser alone accepts them and
  passes every other row in the file.
- **Parse-but-not-canonical.** `1w:mon`, `w:monday`, `w:wed,mon`, `m:01`,
  `m:15,1`, `y:7-04`, … parse and are still refused, because a stored value must
  be the exact canonical spelling.
- **Non-string values are reported, never raised.** `recur: 7` (line 29 of
  `recur-non-canonical`), `title: 42`, and `tags: "@home"` each produce a
  diagnostic; `Check` type-guards before every rule because `with_history` runs
  it after a write, where a raise would bypass the rollback.
- **Ruby `inspect` spelling.** Messages quote values with `Object#inspect`, so
  a non-string recur is `7`, a string is `"every week"`, and a nil is `nil`.
  `check-meta-and-ids` already needed three repairs on this rule alone; reuse
  its `rubyInspect`, do not re-derive it.

The focused Ruby suite passed at this revision: `ruby -Itest test/test_check.rb`
— 38 runs, 193 assertions, 0 failures.

## Translation and conformance

`go/internal/check/task.go` carries the field rules and both warning channels;
`recur_cookie.go` carries `Recur.cookie?` as *recognition only* — the grammar's
diagnostics stay in campaign 5, exactly as `check` itself discards them.
`UpdateStamp.valid?` is reimplemented at check fidelity for the same reason: it
is a delegated rule inside this slice's drift closure, not a slice of its own
that this one may claim.

```console
$ ruby porting/evidence/check-task-fields/compare.rb
check-task-fields: 14 Ruby/Go diagnostic comparisons passed

$ for seed in 1 7 99 4242 31337; do
    ROUNDS=15 PER_ROUND=80 SEED=$seed ruby porting/evidence/check-task-fields/property.rb
  done
check-task-fields property: 15 generated stores, 1200 records, Ruby and Go agreed
on every owned diagnostic (seed …)   # ×5 — 6,000 generated records

$ cd go && gofmt -l . && go vet ./... && go test -race ./...
ok  tasks-go/internal/check
```

`compare.rb` filters **both** sides through the same owned-message list: the
Ruby capture still carries lead, temporal, delegation, and tree messages this
slice does not port, and the Go package still carries check-meta-and-ids'
messages. It also asserts the channel split directly — nothing Ruby reported as
a warning may reach the Go error list, or the reverse. `property.rb` is a
Ruby/Go differential over generated stores, adversarial around the pinned
traps; Ruby's `Check` is the oracle in-process, and no expectation is derived
from Go output.

## One recorded deviation: tied entries on the same line

`Check` ends with `sort_by(&:first)`, and **Ruby's `sort_by` is not stable**, so
two diagnostics on the same line come back in an order set by quicksort pivots:

```console
$ ruby -e 'p ((1..80).map { [_1, "k#{_1}"] } + [[63,"dup"],[77,"dup"],[80,"dup"]])
             .sort_by(&:first).select { |l,| [63,77,80].include?(l) }'
[[63, "dup"], [63, "k63"], [77, "k77"], [77, "dup"], [80, "k80"], [80, "dup"]]
```

Line 63 is reordered while 77 and 80 are not. This is unspecified behavior of
the sort, not a rule of the linter, so it is classified as **nondeterminism
normalized**: Go sorts stably and keeps emission order, and both comparators
order tied entries by message on each side. Order *across* lines is still
compared exactly, and no fixture in this corpus ties — the captured Ruby output
above is reproduced exactly as captured. Flagged for the reviewer: if this
should instead be an intentional-difference record for Marcus, it is one line
to move.

## Next step

Split independent reviews — source fidelity (top tier) against `lib/tasks/check.rb`
and Go idiom (mid tier) — then independent approval. Two things worth a
reviewer's attention: the deviation above, and the deliberate absence of
`check_lead`, `check_temporal_time`, `check_parent`, `check_section`, and
`check_delegation` from the ported loop, each marked in place with the slice
that owns it.
