# Intentional differences

Every place the Go implementation is allowed to behave differently from Ruby.
The list is empty, and it should stay short.

The porting rule is behavior preservation: Ruby is the oracle until cutover,
and a conformance mismatch is a defect until proven otherwise. Nothing lands on
the strength of "the Go behavior is better" alone — the bar is what a user of
the software would expect.

**Who decides (amended 2026-08-03).** This document used to reserve every
acceptance to Marcus, and required an agent that believed a difference was
right to stop and file a blocked issue. That rule cost more than it bought: it
blocked waves on edge cases nobody could observe. The agent or orchestrator now
**decides and records**, judging from the user's perspective and weighing how
much the difference actually matters. Escalate to Marcus only what is genuinely
major — data loss, cutover risk, product scope, or anything expensive to
reverse. Everything else lands here with its reasoning, where he can revisit it
after the fact.

That change does not loosen the standard of evidence. An entry still needs the
observable, the user-visible consequence, and the comparator disposition; a
difference nobody bothered to characterize is still a defect. And the rule that
binds humans too is unchanged: never resolve a mismatch by editing an expected
result to match Go output.

## The record

One `##` section per accepted difference, in the order they were accepted:

```markdown
## <short-name> — accepted YYYY-MM-DD

- **Slices:** manifest ids this applies to
- **Ruby behavior:** what the oracle does, with the fixture and the exact
  observable (exit status, stdout bytes, file bytes, journal entry)
- **Go behavior:** the same observable, as Go produces it
- **Who can see it:** the user-visible consequence, concretely. "Nobody can
  observe this" is a claim to prove, not to assert — if it is true, this is
  probably a normalization, not a difference.
- **Why accepted:** the reasoning, and who made the call
- **Evidence:** `porting/evidence/<slice-id>/…`
- **Conformance disposition:** how the comparator is told to expect it, so
  the difference stays a known exception rather than silencing a whole class
  of comparison
```

## update-stamp-real-instants — accepted 2026-08-03

- **Slices:** none (found in Wave 0 salvage of `port/check-task-fields`)
- **Ruby behavior:** `UpdateStamp.valid?` validates the timestamp half with
  `Time.iso8601`, which range-checks components without demanding a real
  calendar date. `2026-02-30T10:00:00Z#a`, `2026-06-31T00:00:00Z#a`,
  `2016-12-31T23:59:60Z#a` (leap second) and `2026-06-01T24:00:00Z#a` are all
  valid, so `tasks check` emits no diagnostic for them.
- **Go behavior:** `updatestamp.Valid` parses with `time.Parse`, which demands
  a real instant and refuses all four. `check` reports
  `updated "…" is not an RFC3339 UTC timestamp with device slug`.
- **Who can see it:** nobody, without a hand edit. Both implementations format
  stamps from a real clock reading (`Time#strftime` / `time.Time`), so no
  store either one writes can contain such a value. A hand-edited stamp would
  be flagged by Go's `check` and ignored by Ruby's, and — because the stamp
  drives last-write-wins ordering — would sort as unstamped in Go and as
  ordinary in Ruby.
- **Why accepted:** Marcus, asked before Wave 1: "leap seconds are a pretty big
  edge case so the go behavior is fine." A stamp naming an instant that never
  happened is bad data, and a linter that says so is more useful than one that
  stays quiet. The ordering divergence is unreachable in practice and would in
  any case only affect a record whose stamp is already meaningless.
- **Evidence:** `go/internal/check/validate_test.go`,
  `TestUpdateStampRejectsInstantsRubyAccepts` names all four shapes.
- **Conformance disposition:** no comparator exception needed — no fixture
  contains such a stamp, and none should be added. If a future case does, it
  belongs to this section rather than to a broadened comparison rule.

## custom-link-system-order — accepted 2026-08-03

- **Slices:** none (Wave 1, read model and queries — `links`, `open`, `show`)
- **Ruby behavior:** `Links.classify` walks the configured `system.<name>` rows
  with `systems.each`, which is the config file's INSERTION order. When one URL
  host matches two configured rows — `system.broad = acme.io` and
  `system.narrow = git.acme.io`, against `https://git.acme.io/x` — the row
  written first in the config file wins, so `tasks links --json` reports
  `"system":"broad"` for a config listing `broad` first and `"narrow"` for one
  listing it second.
- **Go behavior:** the rows are held in a `map[string]string`, which has no
  order at all, so classification tries the LONGEST configured host first and
  breaks a length tie by name. The same URL reports `"system":"narrow"`
  whichever order the config file used.
- **Who can see it:** only a user whose config declares two custom systems
  where one host is a suffix of the other, and who then classifies a URL under
  the narrower one. With a single custom row, or with unrelated hosts, the two
  implementations cannot disagree — a host matches at most one row. The
  observable is the `system` field of a link row and the `--system` filter that
  reads it.
- **Why accepted:** decided by the Wave 1 read-model agent. Preserving Ruby's
  answer would mean carrying config-file line order through `Config` into the
  read model purely to reproduce an ordering the user never stated. Specificity
  is also the better answer: a user who writes `system.narrow = git.acme.io`
  alongside a broader row is asking for the narrow one to win, and Ruby's
  behavior there is an artifact of hash iteration rather than a decision.
- **Evidence:** `go/internal/links/links_test.go`,
  `TestCustomSystemsPreferTheMoreSpecificHost` names the case; the
  documented-behavior cases (`TestCustomSystemRowsClassifySelfHosted`) pin the
  agreeing majority.
- **Conformance disposition:** no comparator exception needed — no fixture
  configures overlapping custom system hosts, and the corpus does not generate
  config files. A future fixture that did would belong to this section.

## list-priority-tie-order — accepted 2026-08-03

- **Slices:** none (found by the Wave 1 integration review's differential sweep;
  the behavior predates that packet)
- **Ruby behavior:** `bin/tasks` `cmd_list` orders each state group with
  `list.sort_by { |i| i.priority || "Z" }`. MRI's `sort_by` is **not stable**,
  so rows that tie on priority come out in whatever order introsort's
  partitioning left them in. On
  `porting/fixtures/valid/scale-ordering/store` the INBOX `[B]` rows print as
  Review (2.3.4), Measure (4.3.2), Plan (3.3.3), Check (1.3.5), Book (5.3.1) —
  file lines 71, 165, 118, 23, 206. On `link-corpus` and
  `recur-calendar-grammar`, where every row ties, median-of-three pivot
  selection swaps the first and last rows of the group.
- **Go behavior:** `sort.SliceStable` on the same key, so a priority tie keeps
  ascending file order: Check (1.3.5), Review (2.3.4), Plan (3.3.3), Measure
  (4.3.2), Book (5.3.1).
- **Who can see it:** anyone running `tasks list` on a store with two or more
  same-priority tasks in one state group — which is most stores. The observable
  is stdout row order. **Only the human form**: `list --json` emits the
  selection in file order without the adapter's sort, so both implementations
  already agree there, and no row is ever added, dropped, or altered — a sweep
  of 4 clock pins × 18 valid fixtures × 6 filter scopes found 52 differing
  invocations and confirmed all 52 print the identical multiset of lines.
- **Why accepted:** decided by the Wave 1 read-model agent. Ruby's order is not
  a rule to port. It is reproducible for a fixed input array and arbitrary as a
  user-facing order: appending one unrelated unprioritized INBOX row to
  `scale-ordering` — a row that is not in the `[B]` group and does not move any
  row that is — reshuffles those five rows from
  `[Review, Measure, Plan, Check, Book]` to `[Plan, Review, Check, Book, Measure]`.
  The same instability is why several fixtures diverge at one clock pin and
  agree at another: the availability filter changes the array length, and the
  permutation follows. Reproducing it would mean porting MRI's `ruby_qsort`
  and pinning the port to a Ruby version, to preserve an ordering that carries
  no information and that Ruby's own read model already rejects —
  `lib/tasks/task_queries.rb:453-469`'s `stable_sort` carries the source index
  for exactly this reason, and its comment names the symptom ("visible as
  `tasks next` shuffling same-priority tasks, and as a nondeterministic
  canonical order for the future HTTP API"). `cmd_list` is the single place in
  the read path that never adopted it.
- **Evidence:** the arbitrariness is reproducible in two commands. Copy
  `porting/fixtures/valid/scale-ordering/store/tasks.jsonl`, run
  `tasks list` and note the INBOX `[B]` run, then append

  ```
  {"type":"task","id":"5cffff01","parent":"5c000001","state":"INBOX","title":"Zzz unrelated extra capture"}
  ```

  and run it again. Ruby goes from `Review Measure Plan Check Book` to
  `Plan Review Check Book Measure`; Go prints `Check Review Plan Measure Book`
  both times. The added row is unprioritized, is not in the `[B]` group, and
  changes no `[B]` row's line number.

  In Go: `go/cmd/tasks/listorder_test.go`.
  `TestListOrdersByPriorityThenFileOrderWithinEveryStateGroup` checks the rule
  as a property against every valid fixture;
  `TestListTieOrderDivergesFromRubyOnTheseFixtures` pins the three concrete
  sequences and fails if Ruby ever stops diverging;
  `TestListJSONIsFileOrderAndUnaffectedByTheTieBreak` pins that the structured
  form is unaffected.
- **Conformance disposition:** `porting/conform`'s curated cases do not include
  a `list` invocation over a tie-bearing fixture, so the gate is unaffected and
  needs no exception today. A future `list` case must either use a fixture with
  no priority tie in any state group, or compare `--json` rather than the human
  form. **Preferred resolution:** fix `cmd_list` in Ruby to use the same stable
  idiom `TaskQueries#stable_sort` already uses, which retires this entry
  entirely. That is a one-line change to the oracle and was deliberately left
  to the integration owner rather than made from inside a port packet.

## Notes on the record

The last field is the one that rots. A difference recorded here but not
reflected in the comparator is a difference the harness will keep
re-reporting; a comparator exception not recorded here is a difference-hiding
machine. Both directions are review failures.

## Related

- Manifest entries carry an `intentional_differences` array pointing at the
  section names here: `porting/manifest.jsonl`.
- Method and classification rules:
  [`docs/plans/deprecated/language-porting-playbook.md`](../docs/plans/deprecated/language-porting-playbook.md).
- The agent-facing rule: [`PORTING.md`](PORTING.md), "Never bless Go output".
