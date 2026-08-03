# Field-patch differential harness

Runs the same store mutation through Ruby's `Tasks::Store` and the Go store over
identical fixtures, and compares three things: the typed outcome, the resulting
`tasks.jsonl` bytes, and the whole journal tree — `index.json` and every blob.

It exists because Wave 1's real defects were all found by running two
implementations over the same bytes, and none by reading code or by green unit
tests. The write path needed the same treatment before the CLI verbs that would
otherwise drive it existed, so both sides are driven at the STORE boundary
instead: `porting/patchdiff/ruby_driver.rb` and `go/cmd/patch-diff-probe`.

```sh
porting/patchdiff/run                  # every batch
porting/patchdiff/run recurrence.json  # one batch
```

Each case runs in its own `mkdtemp`. Nothing here reads or writes real task
data; fixtures come from `porting/fixtures/valid` or are written inline by the
case generators.

## The batches

| generator | cases | what it covers |
| --- | --- | --- |
| `cases_fields.py` | 377 | every field of the vocabulary against the fixture corpus, with the values each one accepts and refuses |
| `cases_recurrence.py` | 216 | completing a recurring task: twelve cookies × four clocks × three anchor shapes, timed anchors, DST gaps, and what the roll carries forward |
| `cases_delegation.py` | 312 | `undelegate`, `release`, `set_work_ref` across six marker shapes and four states |
| `cases_changeset.py` | 378 | multi-field changesets: every field over nine record shapes, plus twenty coupled pairs in both orders |

## Known divergences

1283 cases, 1280 byte-identical. The three that are not:

- **`location`** (1 case) — Go refuses the field by name; moves need placement,
  cycle and depth rules that are not ported.
- **an out-of-range recurrence roll** (2 cases) — Go refuses before writing
  where Ruby writes and rolls back. Recorded in
  [`intentional-differences.md`](../intentional-differences.md#recurrence-roll-out-of-range-refuses-before-writing--accepted-2026-08-03).

A new divergence is a defect until it is characterized and recorded there.

## Adding a case

A case is one JSON object:

```json
{"fixture": "full-field-matrix", "id": "f0000012",
 "field": "priority", "value": {"kind": "text", "text": "A"}}
```

`fixture` names a directory under `porting/fixtures/valid`; `records` may be
supplied instead as a literal list of JSONL records. `value` carries its kind —
`none`, `text`, `bool`, `list`, `tag_delta`, `temporal` — because Ruby's fields
assert on the SHAPE, and collapsing them all to strings is exactly the class of
bug this harness caught. `verb` drives a delegation verb instead of a patch, and
`changes` drives a multi-field changeset.
