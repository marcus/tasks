# Intentional differences

Every place the Go implementation is allowed to behave differently from Ruby.
The list should stay short.

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

## year-less-feb-29-rolls-to-the-next-real-one — accepted 2026-08-03

- **Slices:** none (Wave 1, temporal and recurrence packet)
- **Ruby behavior:** `Dates.build_month_date` and the bare month-day branch of
  `Dates.parse_numeric` construct THIS year's date before deciding whether to
  roll forward. For a year-less February 29 typed in a non-leap year that
  construction raises `Date::Error`, which `parse_when` rescues to nil — so
  `tasks due "feb 29"` and `tasks due "2/29"` are refused outright in 2023,
  2025, 2026 and 2027, with the generic "could not understand that date".
- **Go behavior:** `temporal.rollForward` tries this year and then next year, so
  the same input resolves to 2024-02-29 when typed in 2023. Everything else is
  unchanged: the date is still never CLAMPED to the 28th, and the roll still
  looks at most one year ahead, so "feb 29" typed in June 2024 — where the next
  real one is four years out — is still refused, exactly as Ruby refuses it.
- **Who can see it:** anyone scheduling something for the next February 29 from
  a non-leap year. Under Ruby the input is rejected and the user has to type
  the full `2024-02-29`; under Go it is accepted. No stored value differs — the
  date written is the one the user asked for either way — and no existing store
  can contain a difference, because this is input parsing only.
- **Why accepted:** Ruby's refusal is incidental rather than designed. The
  documented rule in `dates.rb` is "reject, do not clamp", and rolling to the
  next real February 29 obeys that rule; refusing it obeys only the order in
  which the Ruby method happens to build its candidates. A person typing
  "feb 29" means the 29th, and the one-year horizon keeps the answer close
  enough to be what they meant. Decided by the Wave 1 packet agent.
- **Evidence:** `go/internal/temporal/dates_test.go`,
  `TestYearLessLeapDayRollsToTheNextRealFebruary29` tables both spellings, the
  cases that agree with Ruby, and the four-years-out case that still refuses.
- **Conformance disposition:** no comparator exception needed — date input is
  not a stored value, and no fixture drives the CLI with a bare "feb 29". If
  one is added, it belongs to this section.

## date-order-is-a-parameter-not-a-process-global — accepted 2026-08-03

- **Slices:** none (Wave 1, temporal and recurrence packet)
- **Ruby behavior:** `Tasks::Dates` keeps the month-first/day-first choice in a
  module-level `@date_order`, installed once by `Dates.configure!` from
  `Config#date_order` and readable as `Dates.date_order`. `parse_when` takes a
  `date_order:` keyword that defaults to that global. `Dates.reset!` exists
  only so tests can undo the install.
- **Go behavior:** `temporal.ParseWhen` takes the order as a required argument
  and `temporal` holds no mutable package state. `temporal.OrderNamed` converts
  the config string, degrading anything that is neither "mdy" nor "dmy" to MDY
  — the same degrade `configure!` performs.
- **Who can see it:** nobody. The resolved order is identical, and `Config`
  already computes it; the difference is only in who stores it between
  resolution and use. There is no Ruby surface that reads `Dates.date_order`
  other than `parse_when`'s own default.
- **Why accepted:** a process-wide mutable default is a shared-state hazard the
  Go build has no reason to inherit — it makes the parser untestable without a
  reset hook, and it would be a data race the moment the API surface serves two
  requests at once. Threading the value is the same design rule the plan
  applies elsewhere: the caller that resolved a setting passes it. Decided by
  the Wave 1 packet agent.
- **Evidence:** `go/internal/temporal/dates_test.go`,
  `TestOrderNamedFallsBackOnInvalidValue` covers the degrade `configure!` owned,
  and `TestDateOrderDMYFlipsBareAndYearForms` covers the threading.
- **Conformance disposition:** no exception needed; no observable differs.

## calendar-interval-beyond-int64 — accepted 2026-08-03

- **Slices:** none (Wave 1, temporal and recurrence packet)
- **Ruby behavior:** `Recur.cookie?` accepts an unbounded interval count,
  because Ruby integers are arbitrary precision. `"9999999999999999999w:mon"`
  is a valid cookie and `tasks check` reports nothing for it.
- **Go behavior:** the count is parsed into an `int`, so a value past int64
  overflows and the cookie is refused: `check` reports
  `invalid recur cookie "9999999999999999999w:mon" (expected e.g. .+1w, …)`.
  The boundary is exactly int64 — `"99999999999w:mon"` is accepted by both.
- **Who can see it:** nobody in practice. The cookie means "every ten
  quintillion weeks", roughly 10^17 times the age of the universe, and it can
  only reach a store by hand edit. Someone who hand-wrote one would get a
  diagnostic from Go and silence from Ruby.
- **Why accepted:** arbitrary-precision interval counts are not a feature, they
  are a consequence of Ruby's numeric tower. A schedule that can never produce
  a second occurrence is bad data, and the same argument that accepted
  [update-stamp-real-instants](#update-stamp-real-instants--accepted-2026-08-03)
  applies: a linter that says so beats one that stays quiet. Recorded by the
  integrating reviewer rather than left as a known-but-unwritten difference.
- **Evidence:** verified directly against both implementations; the accepted
  and refused boundary values are named above.
- **Conformance disposition:** no exception needed — no fixture contains such a
  cookie, and none should be added.
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

## application-refuses-where-ruby-raises — accepted 2026-08-03

- **Slices:** none (Wave 2, application layer)
- **Ruby behavior:** `Tasks::Application` raises `ArgumentError` for a malformed
  request rather than returning a result. `list_tasks(:open)` raises
  "filter must be a Tasks::TaskFilter"; a `context:` that is not an
  `OperationContext` raises; `get_task_result(id, source: :other)` raises;
  `delegate_task(command, mode: "implement")` — a prebuilt command plus loose
  keywords — raises; `DelegationCommand.new(action: :promote)` raises. Each
  aborts the caller with a backtrace.
- **Go behavior:** the same requests are typed refusals. An unknown delegation
  action, an unknown proposal action, a blank task id and an unknown read source
  return `invalid` / `not_found` results carrying the same message text. The
  cases that were Ruby type checks — the filter class, the context class, the
  prebuilt-command-plus-keywords mix — are unrepresentable in Go's signatures
  and cannot be reached at all. Construction-time errors that Ruby raises from
  `Application.new` (a missing factory, a host context that is not an
  `@context`) are returned as an `error` from `application.New`.
- **Who can see it:** an adapter author, and eventually an API client. Through
  the CLI, nobody: no CLI path constructs a malformed command. Through the
  future HTTP surface the difference is the whole point — a request body naming
  `"source": "other"` becomes a 404 with a message instead of a 500 with a
  backtrace.
- **Why accepted:** decided by the Wave 2 application-packet agent. The
  application layer is explicitly the seam a transport sits on, and the plan
  gives it to the CLI, the API and the TUI equally. A layer that panics on a
  query parameter cannot serve a transport, and Ruby itself already treats the
  distinction as incidental — `decide_proposal` rescues its own `ArgumentError`
  into an `:invalid` result, which is exactly the behavior generalized here.
- **Evidence:** `go/internal/application/delegate_test.go`,
  `TestAnUnknownDelegationActionIsRefused`;
  `go/internal/application/project_test.go`,
  `TestAMalformedProposalDecisionIsARefusalNotAPanic`;
  `go/internal/application/read_test.go`,
  `TestCheckedTaskLookupIsExactToTheRequestedSource` (the unknown-source case);
  `go/internal/application/create_test.go`, `TestHostContextMustBeAnAtContext`
  and `TestNewRefusesAMissingStoreFactory`.
- **Conformance disposition:** no comparator exception needed — no fixture and
  no CLI invocation reaches a malformed application command. If the API surface
  lands a fixture that does, its expected output belongs to this section.

## empty-work-ref-clears-like-off — accepted 2026-08-03

- **Slices:** none (Wave 2, application layer)
- **Ruby behavior:** `DelegationCommand#normalize_work_ref` treats `nil`, `off`
  and `none` as "clear this reference", and everything else as a reference the
  store validates. The empty string is *everything else*: it survives
  normalization as `""` and reaches `Store#set_work_ref!`, which refuses it as
  blank. So `tasks workref <ref> ""` is an error while `tasks workref <ref> off`
  clears.
- **Go behavior:** `DelegationCommand.WorkRef` is a plain string in which `""`
  IS the clear instruction, alongside `off` and `none`. There is deliberately no
  spelling that asks to store a blank reference.
- **Who can see it:** a caller that passes an empty string where it means "no
  value" — an HTTP body with `"work_ref": ""`, a TUI field cleared to empty, a
  shell variable that expanded to nothing. Under Ruby that is a refusal the user
  has to re-type as `off`; under Go it clears. No stored value differs: neither
  implementation can persist a blank reference, so the file is identical either
  way and only the outcome of the request differs.
- **Why accepted:** decided by the Wave 2 application-packet agent. Ruby's
  `nil`-versus-`""` distinction is a Ruby fact, not a product decision — the
  comment in `delegation_command.rb` says clearing "is spelled `off` or `none`
  at every surface … and reaches Store as nil", and an empty field is the fourth
  surface spelling of the same intent. Refusing it buys nothing: the only other
  possible answer for a blank reference is the refusal the store already gives,
  so no information is lost by treating the two as one.
- **Evidence:** `go/internal/application/delegate_test.go`,
  `TestWorkRefClearWordsNormalizeAtEverySurface` tables every clearing spelling
  including the empty one, and the references that are NOT clear instructions
  (`offline`, `https://example.com/off`).
- **Conformance disposition:** no comparator exception needed — the corpus does
  not invoke `workref` with an empty argument, and `workref … off` behaves
  identically on both sides. A fixture that added the empty spelling would
  belong to this section.
## merge-conflict-marker-size-is-bounded — accepted 2026-08-03

- **Slices:** none (Wave 2, JSONL merge packet)
- **Ruby behavior:** `MergeDriverCommand.resolved_marker_size` accepts any
  digits-only argument and passes it to `Integer#to_i`, which is unbounded, so
  `tasks merge-driver … 99999999999999` asks Ruby to build a fourteen-digit run
  of `<` characters.
- **Go behavior:** the same argument clamps to 2^20. Every value Git can supply
  — and every value below the clamp — resolves identically, including the
  minimum-width rule that widens anything under 7.
- **Who can see it:** nobody through Git. `%L` is Git's `conflict-marker-size`,
  which Git itself validates as a small integer; reaching the clamp requires
  invoking the plumbing command by hand with an absurd width. Such a caller gets
  a one-megabyte marker line from Go and an out-of-memory attempt from Ruby.
- **Why accepted:** allocating gigabytes because a hand invocation had a typo is
  not behavior worth preserving, and the conflicted file is unreadable either
  way. Decided by the Wave 2 merge packet agent.
- **Evidence:** `go/internal/merge/driver_test.go`,
  `TestMarkerSizeIsClampedAtBothEnds` tables the fallback, the minimum, an
  honored width, and the clamp.
- **Conformance disposition:** no comparator exception needed.

## merge-driver-io-errors-carry-the-go-runtime-wording — accepted 2026-08-03

- **Slices:** none (Wave 2, JSONL merge packet)
- **Ruby behavior:** a merge-stage file the driver cannot read raises
  `Errno::ENOENT`, whose message is
  `No such file or directory @ rb_sysopen - /path/to/base.jsonl`. That string
  reaches stderr as `tasks JSONL merge failed: …` and is appended to
  `.tasks-merge.log`.
- **Go behavior:** the same failure reports Go's wording,
  `open /path/to/base.jsonl: no such file or directory`. Exit status (1), the
  log line's shape, and the decision not to touch `%A` are identical.
- **Who can see it:** only a hand invocation naming a path that does not exist,
  or a merge stage the process cannot read. Git creates all three temp files
  before it calls the driver, so the path is unreachable through a real merge.
- **Why accepted:** reproducing `rb_sysopen` diagnostics would mean carrying a
  table of Ruby's errno spellings into the Go port to describe failures Git
  cannot produce. The path, the cause, and the exit status all still reach the
  user. Decided by the Wave 2 merge packet agent.
- **Evidence:** the wording is named above; the driver comparison
  (`TestTheTwoDriversLeaveIdenticalBytes`) covers every reachable case, all of
  which agree byte for byte.
- **Conformance disposition:** no comparator exception needed — no fixture
  invokes the driver with a missing stage file.

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
