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
## recurrence-roll-out-of-range-refuses-before-writing — accepted 2026-08-03

- **Slices:** none (Wave 2, store field-patch vocabulary)
- **Ruby behavior:** `advance_recurrence_records` has no storable-range guard on
  the roll itself. Completing a task anchored at `9999-12-31` with `+1y` writes
  `"scheduled":"10000-12-31"`, the post-write `Check` refuses the bytes, and the
  whole file is restored: `status :store_invalid`, `rolled_back true`, error
  `scheduled "10000-12-31" is not a YYYY-MM-DD date`. `patch_recurrence` guards
  the same shape at SET time (`unreachable_recurrence`) and says so — the guard
  is simply missing on the completion side.
- **Go behavior:** the roll refuses before touching the file: `status invalid`,
  `rolled_back false`, error `recurrence left the storable year range at
  10000-12-31`. The stored bytes and the journal are identical to Ruby's, which
  rolled back to the same file.
- **Who can see it:** anyone completing a recurring task whose next occurrence
  lands past year 9999 — reachable only by a hand-written anchor in the last
  storable year, or a cookie like `+9999y` that Ruby's own set-time guard would
  have refused. They read a different sentence and a different status; the file
  is byte-identical either way, so nothing downstream of the store can tell.
- **Why accepted:** writing bytes you already know are invalid, to have a
  validator refuse them, is the failure mode `patch_recurrence`'s own comment
  describes as "rolling every completion back forever". Refusing up front is the
  behavior Ruby documents and only half-implements. Decided by the Wave 2 store
  packet agent.
- **Evidence:** `go/internal/store/patch_test.go`,
  `TestRecurrenceRollOutOfRangeRefusesWithoutWriting`; measured against Ruby by
  the differential harness described in the Wave 2 report (batch 2, `edge-0`
  and `edge-1`).
- **Conformance disposition:** no comparator exception needed — no fixture
  anchors a recurring task in year 9999, and none should be added.

## off-is-nil-not-the-word — accepted 2026-08-03

- **Slices:** none (Wave 2, store field-patch vocabulary)
- **Ruby behavior:** `patch_lead` and `patch_recurrence` accept `nil` or the
  SYMBOL `:off` as "clear this". The STRING `"off"` is neither, so it falls
  through to the value validators and is refused —
  `invalid lead time "off" (expected a span like 3w, 2d, 1m, 1y)` and
  `invalid recurrence cookie`. The CLI translates the typed word to the symbol
  before the store sees it.
- **Go behavior:** identical, and deliberately so. `PatchValue.off()` is true
  only for `NoValue()`. An earlier draft treated the literal string `"off"` as
  clearing, which the differential harness caught immediately: 14 cases where
  Ruby refused and Go cleared the field and wrote.
- **Who can see it:** nobody, now. This entry records a DECISION rather than a
  live difference — the friendly spelling belongs to the adapter that read the
  word, not to the store, and an adapter that forgets to translate gets a
  refusal rather than a silent write.
- **Why accepted:** recorded so the next agent building `tasks lead <ref> off`
  knows the translation is theirs to do. Decided by the Wave 2 store packet
  agent.
- **Evidence:** the differential harness's first batch (377 cases) agrees on
  every `lead`/`recurrence` `"off"` case.
- **Conformance disposition:** none needed; no observable differs.

## activate-has-a-baseline-where-ruby-raises — accepted 2026-08-03

- **Slices:** none (Wave 2, store field-patch vocabulary)
- **Ruby behavior:** `activate` is in `TaskChangeset::SPECIAL_FIELDS` but has no
  entry in `EditSnapshot#baselines`, so routing it through `TaskPatch` raises
  `KeyError: key not found: :activate` from `EditSnapshot#expected_for`. The
  path is unreachable in the product — `tasks activate` sends a changeset
  guarded by a whole-task revision — so the crash is latent, not live.
- **Go behavior:** `fieldBaseline` gives `activate` the pair activation actually
  reads: the defer marker and the available-from expectation. A single-field
  entry point needs a baseline, and a `KeyError` is not one.
- **Who can see it:** nobody through the Ruby CLI. A caller that reaches the Go
  store's single-field `Patch` with `activate` gets a narrow conflict check
  where Ruby would have crashed.
- **Why accepted:** the Ruby behavior is a defect, not a specification. It is
  left unfixed only because nothing reaches it; whoever ports `apply_changeset!`
  should fix `patch_expected_for` at the same time. Decided by the Wave 2 store
  packet agent.
- **Evidence:** the differential harness drives Ruby's `activate` through
  `apply_changeset!` instead, and the two agree on all 18 cases.
- **Conformance disposition:** none needed; the CLI path is unchanged.

## archive-interruption-is-a-typed-failure-not-a-crash — accepted 2026-08-03

- **Slices:** none (Wave 3, store history and lifecycle)
- **Ruby behavior:** `archive_swept_impl` writes the archive first, then
  re-reads it to prove every moved id is durable, then deletes the live
  records. Both the durability assertion and the live write can raise, and
  nothing rescues them: `with_history` has no `rescue`, `cmd_archive` has none,
  so `tasks archive` exits with a `RuntimeError`/`Errno` backtrace on stderr
  and status 1. `test_archive_interruption_after_archive_write_is_retry_safe_and_idempotent`
  asserts the raise directly.
- **Go behavior:** the same two conditions return `ArchiveResult{Failed: true}`
  with the reason recorded in `LastRollback`, and `tasks archive` prints
  `archive failed; live tasks were preserved` and exits 1. Under `--json` it
  emits the `conflict` / `write_failed` envelope the CLI already had for the
  `result == false || store.last_rollback` branch — a branch Ruby's own adapter
  wrote and cannot currently reach for this cause.
- **Who can see it:** anyone whose disk fills or whose store goes read-only
  mid-sweep. They see a sentence instead of a backtrace with absolute paths in
  it. The FILES are identical in both implementations: neither rolls back, both
  leave the durable archive copy alongside the live one, and both converge on
  the next `tasks archive`.
- **Why accepted:** the retry-safety guarantee is preserved exactly — that is
  the part that protects data. What differs is only how the failure is
  reported, and a backtrace is not a report. `cmd_archive` already contains the
  message for this case, which is the strongest evidence that raising was an
  oversight rather than a decision. Decided by the Wave 3 lifecycle packet
  agent.
- **Evidence:** `go/internal/store/lifecycle_test.go`
  `TestArchiveInterruptionAfterArchiveWriteIsRetrySafeAndIdempotent` denies the
  live write and asserts both copies survive and the retry converges;
  `porting/compare/lifecycle-diff` agrees on every reachable `archive` case.
- **Conformance disposition:** none needed. The corpus cannot arrange a failing
  write mid-sweep, so no case observes it.

## repair-reports-a-failed-write-instead-of-a-bare-refusal — accepted 2026-08-03

- **Slices:** none (Wave 3, store history and lifecycle)
- **Ruby behavior:** `repair!`'s write goes through `with_history`, whose only
  failure path is post-write validation; a raising `Atomic.write` propagates out
  of `cmd_repair`.
- **Go behavior:** a failing write is caught, the files are restored, and the
  pass reports `unrepairable` with the write error as its single blocker —
  the same shape `with_history`'s validation rollback already produces.
- **Who can see it:** the same population as the entry above, in the same way.
  Nothing was written in either implementation.
- **Why accepted:** identical reasoning; the failure classification Ruby
  already has for the neighbouring cause is simply extended to this one.
  Decided by the Wave 3 lifecycle packet agent.
- **Evidence:** `go/internal/store/repair.go`; every reachable `repair` case in
  `porting/compare/lifecycle-diff` matches byte for byte.
- **Conformance disposition:** none needed.
## go-api-refuses-unbuilt-writes-with-501 — accepted 2026-08-03

- **Slices:** none (Wave 3, HTTP API packet)
- **Ruby behavior:** `bin/tasks-api` performs every route in
  `docs/api/openapi.yaml`. `DELETE /tasks/{id}` answers 204,
  `POST /tasks/{id}/approve|reject` answer 200, the five delegation routes
  answer 200, the four project-mutation routes answer 200/201, a `placement` or
  `parent_id` PATCH moves the subtree and answers 200, and a create carrying
  `scheduled`, `deadline`, `recurrence` or `lead` persists them and answers 201.
- **Go behavior:** each of those answers **501 `not_implemented`** with a
  message naming the missing capability, and writes nothing. Everything else on
  the route — Host, Origin, media type, body limit, query validation, the
  If-Match precondition — is enforced FIRST, so a malformed request still gets
  its own refusal.
- **Who can see it:** every API client, immediately and unambiguously. The
  cause in each case is the Go store, not the adapter: it has no `DeleteTask`,
  no `DecideProposal`, no project lifecycle writer, `applyFieldPatch` refuses
  `location` outright, `store.CreateCommand` carries no temporal or recurrence
  fields, and `application.runDelegation` refuses a non-empty
  `expected_revision` — which the HTTP contract makes mandatory on the
  delegation routes. Wiring a delegation route that silently dropped the
  client's If-Match would be the one outcome worse than refusing.
- **Why accepted:** decided by the Wave 3 API packet agent, under the plan's
  rule that an unfinished capability refuses rather than approximates. 501 is
  chosen over 503 deliberately: 503 invites a retry, and no number of retries
  will build the store operation. The refusals disappear one at a time as the
  store grows each capability; nothing in `internal/api` has to change except
  deleting a `notImplemented` call.
- **Evidence:** `go/internal/api/write_test.go`,
  `TestRoutesThisBuildRefusesSayWhy`,
  `TestRefusedRoutesStillEnforceTheirPreconditions` and
  `TestCreateRefusesFieldsThisBuildCannotPersist`; the live two-server
  differential run agrees on every other route.
- **Contract note:** `docs/api/openapi.yaml` documents no 501 and its
  `ErrorCode` enum (line 3572) has no `not_implemented` member, so these
  responses are deliberately OUTSIDE the written contract. That is the honest
  place for them: the contract describes the finished product, and a build that
  cannot yet perform a route should fail contract validation for that route
  rather than pass it by answering something plausible. The spec is left
  unchanged so the gap stays visible.
- **Conformance disposition:** `porting/conform` does not drive the API, so no
  comparator exception is needed. `porting/api-differential` lists these routes
  in `EXPECTED_REFUSALS`; a route that stops diverging means the capability
  landed and this entry should lose a line.

## go-api-honours-the-determinism-pins — accepted 2026-08-03

- **Slices:** none (Wave 3, HTTP API packet)
- **Ruby behavior:** `bin/tasks-api` reads none of `TASKS_PIN_NOW`,
  `TASKS_PIN_IDS`, `TASKS_PIN_COALESCE_SCOPE` or `TASKS_PIN_DELEGATION_KEYS`.
  It serializes a fixed set of resolved paths into
  `TASKS_API_RESOLVED_CONFIG`, and `config.ru` rebuilds `Config::Paths` from
  that JSON and injects no clock and no id mint. So a pinned harness gets real
  ids and the real clock from the Ruby server.
- **Go behavior:** `cmd/tasks-api` reads the pins the same way `cmd/tasks`
  does, so a pinned harness gets the pinned ids and clock.
- **Who can see it:** only a harness that sets a pin. In ordinary use no pin is
  set and the two behave identically — which the differential run confirms.
- **Why accepted:** decided by the Wave 3 API packet agent.
  `porting/specs/determinism.md` says the pins are an adapter-boundary concern
  honoured by `bin/tasks`, `bin/tasks-api` and `bin/tasks-tui`; the Ruby server
  simply does not implement what that document claims. Making the Go server
  match the document rather than the omission is the cheaper of the two fixes,
  and it is what lets an API conformance harness exist at all. If Ruby is to be
  the oracle for a byte-level API comparison, `config.ru` should carry the pins
  too — that is a one-line change nobody has needed yet.
- **Evidence:** `porting/specs/determinism.md`; the write-sequence differential
  run compares with normalization rather than with pins for exactly this reason.
- **Conformance disposition:** none; `porting/conform` drives the CLI, which
  honours the pins on both sides.
## recur-count-preview-clock-pinned — a Ruby TEST fixed, not a behavior difference — 2026-08-03

- **Slices:** none (Wave 3, CLI mutation packet)
- **Ruby behavior:** unchanged. `tasks recur <ref> --count N` projects a
  calendar schedule forward from the stamp, and a calendar schedule catches up
  to the next match after TODAY — so the projection legitimately depends on the
  wall clock.
- **What changed:** `test/test_cli_mutations.rb#test_cli_recur_preview_honors_count_and_json`
  asserted `["2026-08-01", "2026-08-03"]` against the real clock. That is only
  the right answer while today is on or before 2026-08-02; the test passed in
  July and failed in August. Wave 1 recorded it as a known pre-existing failure
  and left it to this packet. It now pins `TASKS_PIN_NOW=2026-08-01T12:00:00Z`
  and `TZ=UTC`, which states the anchor the expectation was always written
  against.
- **Who can see it:** nobody. No product code changed; only the test's clock is
  now stated rather than assumed.
- **Why accepted:** a test that reports the date instead of the behavior is a
  defect in the test. Decided by the Wave 3 CLI mutation packet agent, under the
  velocity plan's explicit invitation to pin its clock.
- **Evidence:** `ruby test/test_cli_mutations.rb` — 293 runs, 0 failures.
- **Conformance disposition:** none needed; the corpus pins its own clock.

## project-archive-stamp-honours-the-injected-clock — a Ruby DEFECT fixed, not a behavior difference — 2026-08-03

- **Slices:** none (store-completion packet)
- **Ruby behavior, before:** `Store#archive_project_impl` stamped the swept
  section root with `Date.today.iso8601`. Every other date this store writes —
  the sweep's own `archived`, a close's `closed`, a capture's
  `Captured [...]` — comes from the injected `@now` clock, so a harness pin
  reaches them. This one read the process wall clock and ignored the pin.
- **Who could see it:** anyone pinning a clock — every conformance run and every
  test — and anyone whose configured time zone is not the machine's. `tasks project archive X` under `TASKS_PIN_NOW=2026-03-14`
  wrote `"archived":"2026-08-03"` into `archive.jsonl` — the real date. It was
  the single nondeterministic byte a project archive produced, and it made the
  Go port's only honest choices "reproduce the nondeterminism" or "diverge".
- **What changed:** `archive_project!` now takes `today:` and stamps it, exactly
  as `archive_swept!` already did, and `Application#archive_project` passes the
  reader's resolved day. The default is still `Date.today`, so a programmatic
  caller that supplies nothing behaves as before. The Go side threads the same
  value through `ArchiveProject(id, today)`.
- **Why not the store's own clock:** the first attempt read `@now.call.to_date`,
  which IS pin-honouring but is a UTC instant — so a reader west of UTC got
  tomorrow's date on a project archive after 17:00, while `closed` and the sweep
  gave them today's. A stamp must come from the same day the rest of the
  product means by "today", and that is the reader's, not the process's.
- **Why fixed rather than ported:** porting it would have meant making the Go
  binary deliberately ignore its own injected clock for one field, to preserve a
  value carrying no information. It is also a latent bug for Ruby users of the
  test suite regardless of the port. Decided by the store-completion packet
  agent, under the velocity plan's standing preference for fixing a Ruby defect
  over reproducing it.
- **Evidence:** `ruby test/test_projects.rb` — 29 runs, 0 failures;
  `porting/compare/store-completion-diff` — `project-archive` and
  `project-archive-blocked` byte-identical across `tasks.jsonl`,
  `archive.jsonl` and the journal.
- **Conformance disposition:** none. Both sides now produce the pinned date, so
  `gen.project-archive.*` matches without an exception.

## undelegate-and-workref-do-not-coalesce — 2026-08-03

- **Slices:** none (store-completion packet)
- **Ruby behavior:** `Store#undelegate_task!` and `Store#set_work_ref!` accept
  no `coalesce_key:` at all — only `delegate_task!` and `release_task!` do,
  because only those two compose a SECOND write that has to share one undo step.
  `Application#invoke_delegation` mints a key for every action and passes it to
  the two that take one.
- **Go behavior:** `Store.Undelegate` and `Store.SetWorkRef` accept the key —
  the application layer's optional interfaces declare it — and deliberately
  ignore it.
- **Who can see it:** nobody, now. Before the fix, everyone: `tasks workref X url`
  followed by `tasks workref X off` produced ONE journal step in Go and two in
  Ruby, so one undo reverted both writes and the reference could not be restored
  without editing the file. The pinned delegation-key sequence restarts per
  process, so consecutive invocations drew the SAME key and coalesced into each
  other.
- **Why accepted:** the shape is a difference (a parameter that exists and is
  ignored), the behavior is not — this is the port matching Ruby. The parameter
  stays because `application.Undelegator` and `application.WorkRefWriter` declare
  it, and dropping it would make the store stop satisfying interfaces it should
  satisfy.
- **Evidence:** found by `porting/compare/store-completion-diff`, scenario
  `workref`, step 4 — `journal.labels` had three entries on the Ruby side and two
  on the Go side. Unit tests did not find it, and could not have: a single
  invocation coalesces with nothing.
- **Conformance disposition:** none needed; the two now agree.

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
