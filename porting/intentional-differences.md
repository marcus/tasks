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

## config-timezone-warning-is-returned-not-printed — accepted 2026-08-03

- **Slices:** none (Wave 1, record/config/check packet)
- **Ruby behavior:** `Config.pick_timezone` calls `warn` from inside
  `Config.resolve`, so a `TASKS_TIMEZONE` that names no loadable zone writes
  `tasks: ignoring invalid time zone "Bogus/NotAZone" from TASKS_TIMEZONE env`
  to stderr on **every** command, before the command's own output.
- **Go behavior:** `config.Resolve` returns the identical wording in
  `Paths.Warnings` and prints nothing. The resolved zone, its source, and the
  UTC-fallback flag are unchanged.
- **Who can see it:** anyone who has set an invalid `TASKS_TIMEZONE` sees the
  line under Ruby and, today, no line at all under Go — one stderr line per
  invocation. Nothing on stdout, no file bytes, and no exit status differs.
  The message is currently unwired: printing it needs `cmd/tasks/main.go`,
  which this packet does not own.
- **Why accepted:** the difference is the *seam*, not the behavior. A library
  that prints decides for every surface at once — the TUI would paint the line
  into its first frame and the API would put it in a log nobody reads — and a
  function that writes to a global stream cannot be tested without capturing
  one. Returning the warning keeps the wording identical and lets each adapter
  place it. Decided by the Wave 1 packet agent.
- **Evidence:** `go/internal/config/resolve_test.go`,
  `TestInvalidTimezoneEnvFallsThroughToTheConfigZoneWithAWarning` pins the
  exact wording and the fall-through it accompanies.
- **Conformance disposition:** no exception needed today — no fixture sets an
  invalid `TASKS_TIMEZONE`, so no paired case produces the line. **This entry
  expires when the CLI wires it up:** once `main.go` prints
  `Paths.Warnings` to stderr the two implementations agree byte for byte and
  this section should be deleted rather than kept as a standing exception.

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
