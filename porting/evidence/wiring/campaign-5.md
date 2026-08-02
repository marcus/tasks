# CAMPAIGN 5 — seeding temporal parsing, availability, recurrence, zones and calendar edges

Companion to the 28 records in `campaign-5-records.jsonl`. `campaign-10.md` holds
the exemplar and the quality bar; `campaigns-5-6-9-12-inventory.md` holds the
authoritative partition this pass was handed. This file holds the reasoning that
turned that partition into slices: where the cuts are, what is deliberately out
of scope, what the method cannot see, and the records I am asking to amend.

Result: **28 new slices, 374 Ruby tests claimed** (the 373 the inventory
allocated, plus one cross-file claim campaign 12 deliberately left for me — §2d).
Seven source files that were in nobody's `source_paths` now have owners; six more
gain a second, partial owner. `validate` is clean apart from the intended
`source_sha: "PENDING"`, and `reach` is back at the pre-existing baseline of
**21 reaches, 0 unexplained** — after it caught a real ordering bug in my first
draft (§2e).

---

## 1. The inventory, taken mechanically

The partition was given; the arithmetic was re-derived rather than trusted,
because the tree moved between the inventory's baseline (`9b9e6e9`) and this
pass (`6c97990`, with `9e05f7f` re-capturing the baseline at schema v2 and
`87d8cc2` adding `tasks repair` in between). Three things came out of that:

1. **The manifest is at 53 records, not 52.** `store-repair` (campaign 4) landed
   after the inventory was written. It claims nothing of mine and changes no
   boundary, but every count below is against 53.

2. **`test/test_create_task.rb` now has five unclaimed tests, not one.** The
   inventory's §2c allocates C5 exactly one test from that file. Re-running the
   enumeration finds five. The other four —
   `test_a_failed_write_records_the_write_stage_and_restores_the_bytes`,
   `test_a_failed_post_write_check_records_the_validation_stage`,
   `test_a_clean_mutation_clears_both_halves_of_the_rollback_record`,
   `test_a_rollback_stage_outside_the_vocabulary_is_refused` — were added by
   `b3d297c` ("name the stage that failed", 2026-08-01), which is **not** an
   ancestor of `9b9e6e9`. They are rollback-record tests and belong to campaign
   6 by the inventory's own §5b reasoning about `files.rolled_back`. **I did not
   claim them**, and I am flagging them because a mechanical re-derivation of
   "the unclaimed tests in C5's whole-file list" now returns four tests that are
   not C5's. Campaign 6's pass should confirm it took them; if it did not, they
   are a four-test hole in a file whose C5 line reads `(1)`.

3. **Everything else reconciles exactly.** All 173 Appendix A C5 refs resolve to
   a real `def test_`, none is claimed by any of the 53 existing records, and the
   whole-file totals come out at 200 once the four post-baseline tests above are
   set aside. `200 + 173 = 373`.

### 1a. The seven source files, and what is actually in them

| File | Lines | What it is |
|---|---|---|
| `lib/tasks/recur.rb` | 824 | two stored recurrence grammars, natural phrases, canonicalization, occurrence math, `explain` |
| `lib/tasks/lead.rb` | 236 | the lead-time span grammar and the derived gate, calendar and clock |
| `lib/tasks/dates.rb` | 184 | fuzzy date input and `date_order` |
| `lib/tasks/temporal_value.rb` | 107 | the stored value: all-day / floating / fixed, instants, boundaries, metadata |
| `lib/tasks/timezones.rb` | 90 | IANA resolution, DST gaps and folds, host detection |
| `lib/tasks/temporal_parser.rb` | 63 | the date-and-time expression grammar |
| `lib/tasks/temporal_context.rb` | 25 | the frozen (now, zone, format) triple every read takes |

`recur.rb` is over half the campaign's own code by line count and is named by five of my slices.
That is deliberate and is explained in §2b.

### 1b. Six files that gain a second, partial owner

Established practice (`bin/tasks` is named by five slices, `lib/tasks/store.rb`
by nine, and `campaign-10.md` §6 recorded the `config.rb` case explicitly). Each
is added rather than avoided, because a closure that does not reach a file does
not watch it for drift:

- **`lib/tasks/config.rb`** — `date-fuzzy-parse` (the `date_order` key) and
  `timezone-resolution` (the `timezone` / `time_format` keys). This makes **four**
  slices naming that file: `config-resolution` (store paths), `prompt-facts`
  (the `prompt.<name>` parse), and these two. None ports all of it.
- **`lib/tasks/check.rb`** — `check-temporal-fields` takes `check_lead` and
  `check_temporal_time`, which `check-task-fields`' first gap reserved by name.
- **`lib/tasks/task_queries.rb`** — `availability-model` (`build_availability`,
  `effective_gate`) and `lead-availability-gate` (`lead_gate`). Two of my own
  slices split one file, which is why `availability-model`'s notes say so.
- **`lib/tasks/task_view.rb`** — the lead and availability fields
  `task-view-projection`'s gap excludes.
- **`lib/tasks/store.rb`** — eight of my slices, the same way nine existing ones
  already name it.
- **`bin/tasks` / `lib/tasks/cli_commands.rb`** — six CLI slices.

---

## 2. Where the cuts are

28 slices in dependency order. The graph, with campaign-crossing edges shown as
their target campaign:

```
[c2 config-resolution] ─┬─> date-fuzzy-parse ──────────────┐
                        └─> timezone-resolution ─> temporal-value-instants ─┐
                                                                   ├────────┴─> temporal-expression-parse
recur-interval-cookies ─┬─> recur-calendar-grammar ─> recur-occurrence-math ─> recur-explain-preview
                        └─> lead-span-grammar
temporal-expression-parse + [c4 changeset-apply-basic, update-stamp] ─> store-date-stamps
temporal-value-instants + [c3 query-list-filters, query-named-views, task-view-projection] ─> availability-model
lead-span-grammar + availability-model ─> lead-availability-gate
[c2 check-task-fields] + temporal-value-instants + lead-availability-gate + recur-calendar-grammar
                                                                          ─> check-temporal-fields
store-date-stamps ─┬─> cli-date-verbs
                   ├─> cli-temporal-flags
                   ├─> cli-defer-activate-someday   (+ availability-model)
                   ├─> lead-write-rules             (+ lead-availability-gate, [c4 create-basic])
                   └─> store-recurrence-attach      (+ recur-interval-cookies, [c4 create-basic])
store-recurrence-attach ─┬─> store-recurrence-advance  (+ recur-occurrence-math, availability-model,
                         │                                [c4 state-cascade-close, state-transitions,
                         │                                 delegation-assign, delegation-claim-release])
                         ├─> store-recur-calendar-guard (+ recur-calendar-grammar, recur-occurrence-math)
                         └─> temporal-patch-changeset   (+ store-date-stamps, availability-model,
                                                           [c4 changeset-apply-basic])
store-recur-calendar-guard + store-recurrence-advance ─> store-recur-calendar-roll
lead-write-rules + store-recurrence-advance + cli-defer-activate-someday ─> lead-skip-activation
                         ─> cli-recur-command / cli-recur-preview / cli-recurrence-verbs / cli-lead-command
                         ─> temporal-today-injection   (last, deliberately)
```

Twelve edges cross into campaigns 2, 3 and 4. That is correct and matches
campaign 7's and campaign 10's shape: campaign 5 is the first campaign that is
*downstream of almost everything*, because a temporal value is read through
campaign 3's projections and written through campaign 4's writer.

### 2a. The three organizing cuts

**Grammar / math / payload, for recurrence.** `recur.rb` could have been one
slice; it is five (`recur-interval-cookies`, `recur-calendar-grammar`,
`recur-occurrence-math`, `recur-explain-preview`, plus `store-recur-calendar-*`
downstream) because it fails in unrelated ways at unrelated depths. The
interval cookie is pure string plus `Date` arithmetic and needs neither a zone
nor a store — which is why it is the campaign's only zero-dependency slice. The
calendar grammar's defects are *normalization* (sort order, zero-padding, which
spellings round-trip). The occurrence math's defects are *calendar arithmetic and
parity* (the clamp/skip asymmetry, anchor-fixed parity, the 500-cycle bound).
`explain` is a *payload contract*. One reviewer cannot hold all four at once, and
the risk tier of a merged slice would have to be the maximum of the four.

**Accept / do, for every writer.** Four pairs, all cut on the same line:
`store-recurrence-attach` vs `store-recurrence-advance`;
`store-recur-calendar-guard` vs `store-recur-calendar-roll`; `lead-write-rules`
vs `lead-skip-activation`; `store-date-stamps` vs `cli-date-verbs`. What a write
*accepts* and what a completion *does* are different failure modes with different
evidence: the first is a refusal (exit status, stderr, unchanged bytes), the
second is a byte diff plus a history entry.

**Model / gate, for availability.** `availability-model` ports
`build_availability` and `effective_gate`; `lead-availability-gate` ports
`lead_gate`, which `effective_gate` calls. The second depends on the first, so a
reviewer of the hold walk and a reviewer of the lead window are never the same
sitting — and the lead's two units (calendar date vs clock duration) get their
own review, which they need, because they behave *oppositely* across a DST
boundary.

### 2b. `recur-interval-cookies` is first, and `lead-span-grammar` hangs off it

Not tidiness. `Lead` literally reuses `Recur`'s tables (`OFF_WORDS`,
`UNIT_NAMES`, `UNIT_WORDS`) and calls `Recur.step` with a **negative** count, so
that a `1m` lead before March 31 clamps to February 28 exactly the way a `+1m`
interval does. A port that reimplements the stepping inside the lead package
will disagree with recurrence at the end of a month — which is the entire reason
the Ruby shares it. The edge exists so that disagreement is impossible by
construction.

### 2c. `cli-defer-activate-someday` owns its store verbs; the date and recurrence pairs do not

The asymmetry is deliberate and is the one place I departed from the
accept/do rule. There is no store-level test block for `defer`/`activate` to
split off — every one of its 23 oracle tests is CLI-driven. Splitting a
`store-defer-verbs` slice out would produce a slice with no oracle, which
`manifest.md` permits only with an `oracle_gaps` sentence saying why, and
"because the taxonomy is prettier" is not a reason. So the verb and its CLI are
one slice, and `lead-skip-activation` depends on it because `activate` is the
verb that writes `lead_skip`.

### 2d. One cross-file claim, made loudly

`test/test_views.rb#test_agenda_sorts_timed_items_by_exact_boundary_not_file_order`
is claimed by `availability-model`, in a file that is otherwise campaign 12's
entirely (81 of 81). The inventory §6 listed this as one of the things it did
**not** decide, with the warning that two slices sharing one test is legal and
`validate` will not catch a double claim.

It is resolved, and from both sides. `query-named-views` reserved it by name
("lands with campaign 5"). Campaign 12's own seeding pass, running in parallel,
excluded it explicitly: `tui-view-row-projection`'s gap says *"agenda ordering by
exact instant is proved by campaign 5's slice and by nothing in campaign 12"*. I
checked all five sibling record files mechanically — **no other pass claims it**,
and campaign 12's three files total 831 of its 832 allocated tests, the missing
one being exactly this. So this is a **sole** claim, not a shared one, and
without it the test would have finished this round with no owner at all. The
behavior is `availability-model`'s: ordering by the exact release/due *instant*
rather than by date or file order is what `TemporalValue#due_boundary` exists
for, and the slice already claims `test_cli_agenda_json_sorted_by_date_then_priority`
beside it.

That is the only test outside the inventory's 373 that I took. It brings the pass
to 374.

### 2e. `reach` caught a genuine ordering bug in the first draft

Worth recording, because `campaign-10.md` §4g's finding was that `reach` is blind
to most of this class and the brief asked me to watch for it by hand. This time
the tool saw it:

```
store-recurrence-advance
  UNEXPLAINED test_completing_a_claimed_recurring_task_returns_the_next_occurrence_to_the_pool
              drives claim_task!, set_work_ref!, delegate_task! — owned by
              delegation-claim-release, delegation-assign, not upstream
  UNEXPLAINED test_completing_a_human_delegated_recurring_task_keeps_the_person_not_the_work_ref
store-recur-calendar-roll
  UNEXPLAINED test_rolling_a_calendar_schedule_returns_a_delegated_occurrence_to_the_pool
```

Three tests asserting what a recurrence roll does to a delegation marker, in two
slices that did not depend on the delegation slices. The fix is dependency edges,
not an explaining sentence: a recurring task cannot be *claimed* unless claiming
works, so `delegation-assign` and `delegation-claim-release` are real
prerequisites. Both records now carry them and say in `notes` that `reach` found
the omission.

It saw these three and would have seen none of the others, for the reason
campaign 10 recorded: `VERB_OWNERS` maps store **mutation verb methods** only.
These happened to reach `claim_task!`. My slices that reach downstream through a
CLI verb, a renderer or `Journal#undo` are invisible to it, and I checked those
by reading test bodies (§4e).

---

## 3. What is deliberately out of scope

**The nine registry-wide tests in `test/test_cli_json_coverage.rb`.**
`cli-mutation-json-envelopes`' gap enumerates them and says no slice can claim
them until `recur`/`lead` (campaign 5) and `undo`/`redo` (campaign 6) land. Both
now have slices, which is what *unblocks* the final parity slice — and that slice
is campaign 8's. The inventory §7.5 says so explicitly and I have not seeded it.

**The six delegation-scope tests in `test/test_task_queries.rb`.** They sit
directly beside the eight availability tests I claim, in the same file, and read
like filter work. They are campaign 3/4 delegation behavior with no temporal
content. Claiming them because they share a file with availability is the
misattribution td-940935 removed; the inventory §3c makes the same call.

**`test/test_lead_matrix.rb`'s three renderer-agreement tests.** The file is
named for a campaign 5 feature and is not wholly campaign 5's: three of its eight
tests assert that the *renderer's* date-grained fallback agrees with the query
about which rows are hidden, which is campaign 12's. I take four; campaign 6
takes the concurrency one. The inventory §3j is the authority and I did not
re-litigate it.

**`test/test_views.rb`'s other 80 tests, `test/test_cli_mutations.rb`'s 113
campaign 8 tests, and the 24 PRIOR store-method tests in the same file.** The
rule applied throughout — the inventory's §3h rule, which is the rule
`campaign-10.md` used for `agent-diff-report` — is that a CLI test belongs to the
campaign owning the *semantics* it asserts, unless what it asserts is only the
rendering or the registry-wide grammar. So `test_cli_recur_*` is mine and
`test_cli_show_human_readable` is campaign 8's; `test_cli_show_human_availability_covers_own_inherited_hold_timed_and_closed_states`
is mine because it asserts which availability *states* are reported, not how the
line is painted.

**The four post-baseline rollback tests in `test/test_create_task.rb`** (§1
item 2). Campaign 6's, by the inventory's own reasoning, arriving after its
arithmetic was fixed.

**`lib/tui/dates.rb`.** A four-line compat shim (`Tui::Dates = Tasks::Dates`)
with no Go analogue. It is campaign 12's per the inventory §3m; `lib/tasks/dates.rb`
is mine.

---

## 4. What the conformance method cannot observe here, precisely

The inventory §7.4 says campaign 5 is the one campaign of the four that is
genuinely observable today, and that it should not inherit the others' caveats by
copy-paste. That is right, and this section says exactly where it stops being
right. **No schema extension is proposed.**

### 4a. What IS observable, which is most of it

Campaign 5's behavior really is a pure function from (fixture bytes, argv, pinned
env) to (fixture bytes, stdout, exit status). There is no child process, no
socket, no terminal, no interleaving, and no second invocation. Concretely, and
with no new fixture at all:

- `tasks check` against `valid/recur-calendar-grammar` (20 canonical schedules,
  one grammar axis each) and `malformed/recur-non-canonical` (27 rejected values,
  three distinct rejection mechanisms) exercises the whole accept/reject boundary
  of the recurrence grammar today.
- `tasks check` against `malformed/temporal-unknown-nested-key` pins both
  unknown-key messages for `check_temporal_time`; `valid/temporal-both-times`
  pins the interleaved key order and the nested key order on two differently
  zoned time objects in one record.
- `valid/full-field-matrix` carries the three temporal-value shapes on three
  records (floating, `Europe/London` fixed, and a `fold: 1` on the 2026-11-01
  fall-back date) and both lead unit families with a matching `lead_skip`.
- `valid/deferred-tags` carries the entire hold matrix including three records no
  mutation path can produce.
- `tasks recur <ref>` (preview) and `tasks recur --explain --json` are read-only
  commands over that corpus.

`check-temporal-fields` and `cli-recur-preview` are, on that basis, wired to real
fixtures and could be started today with no fixture work at all. **All 28 slices
carry real fixture paths** — no slice in this campaign is in the fixture-less
position `agent-request-queue` or campaign 12's renderer slices are in. Five of
them carry `fixtures` and a `null` `fixtures_todo`, because the committed corpus
already covers them: `temporal-value-instants`, `recur-interval-cookies`,
`recur-calendar-grammar`, `recur-explain-preview`, `lead-span-grammar`. The other
23 carry both, and every `fixtures_todo` names the specific missing *records*
rather than asking for a store in the abstract.

### 4b. The pinned zone is UTC, and that is the campaign's real ceiling

`porting/runners/ruby/run` pins `TZ=UTC` **and** `TASKS_TIMEZONE=UTC` (the latter
because it out-ranks the former). So under the default pin set nothing ever
leaves UTC, and UTC has no DST. Every gap, fold, ambiguous-hour, anchor's-zone
and duration-vs-wall-time behavior in this campaign is therefore **unreachable
from fixture bytes alone**.

It is not unreachable, and the distinction matters: `porting/runners/README.md`
says a case's `env` may override any pin and `invocation.env` records the
override. So the fix is a **case-list obligation**, not a harness change — every
zone-dependent slice's cases must set `TASKS_TIMEZONE` (and usually
`TASKS_PIN_NOW`) explicitly. I have written that requirement into the
`oracle_gaps` of all seven affected slices rather than into this file only,
because `slicing.md` §1 item 2 records what happens to an exclusion stated once
and never picked up.

The failure mode this guards against is specific and quiet: a corpus assembled by
copying another slice's cases inherits UTC, runs green, and proves nothing about
the behavior the slice exists for.

### 4c. `tzdb_version` is recorded-never-compared — and the product prints it anyway

This is the inventory §5a gap, and it is worse than §5a states.

determinism.md exempts the **observation field** `environment.tzdb_version`:
"Recorded, never compared for equality — but a comparison whose two sides
disagree here is re-run before any difference is classified." That is a sound
rule and `porting/compare` implements it.

The exemption does not reach the product's own stdout. `tasks config --json`
prints a `tzdb_version` key, and its value is `TZInfo::DataSource.get.to_s`. On
this host that is the literal string:

```
"Zoneinfo DataSource: /usr/share/zoneinfo"
```

and on a host carrying the `tzinfo-data` gem it is `"Ruby DataSource"`. It is
**not a version**. It names a Ruby object and embeds an absolute host path, it
varies with how Ruby was installed rather than with the tzdb release, and no Go
port can emit it. It arrives in `process.stdout`, which the comparator compares
byte-for-byte.

Consequence: **every `tasks config --json` case fails on this one key, for every
implementation, forever** — including the cases `config-resolution` already
wires. That record lists "`tasks config` output" in `observable_outputs` with
`fixtures_todo: null` and no gap recorded against it, which is the sentence this
pass falsifies most concretely (§6, item 1).

I am not deciding it. The options are an intentional difference (normalize the
key in the comparator), or emitting a real tzdb release identifier on both sides
— and the second is a product change. Both are Marcus's. It is recorded on
`timezone-resolution`'s `oracle_gaps` in full, and `intentional_differences`
stays empty, per PORTING.md.

### 4d. The pin-interception defence is Ruby-only

The second genuine §5a gap, stated at its real size and no larger.

The pins themselves are adequate: every clock read in campaign 5 is wall-clock
and persisted, so `TASKS_PIN_NOW` covers them, and there is no monotonic-clock
problem here (that is campaign 6's and 12's). determinism.md's own warning is
that `invocation.pins[].applied` records whether a pin was **resolved**, not
whether every consumer **used** it — and it names the shipped defect class:
`Tasks::Application` has some thirty methods with a `today: Date.today` default
parameter, so one adapter call site that forgets to pass `today:` produces
wall-clock output with `applied: true` recorded beside it.

Ruby's answer is `test/test_porting_determinism_seams.rb`, which intercepts
`Date.today`, `Time.now`, `Socket.gethostname` and `SecureRandom` **at the
source** via `RUBYOPT=-r`, runs the fully-pinned commands the corpus runs, and
fails on any read the pins were supposed to have removed. It is a monkeypatch of
the language's own clock API.

A compiled Go binary has no analogue and the schema records nothing that would
substitute. So on the Go side, **"every temporal call site received the injected
today" is unprovable by conformance alone.** What remains provable is narrower
and genuinely useful: that one invocation's outputs are mutually consistent — the
parsed date, the written stamp, the history label and the JSON all agree — which
is exactly what the four `*_uses_one_today_*` tests assert, and which a
wall-clock leak breaks whenever an invocation straddles midnight.

Two things follow, both recorded on `temporal-today-injection`:

- The port needs a Go-side equivalent of the seams test (a build-tagged clock
  interface with a fake that records every read). That is new harness work, not
  translation, and it should be budgeted as such.
- Under the default pin (`2026-03-14T15:09:26Z`, mid-afternoon) a double clock
  read is invisible, because both reads land on the same date. The cases that
  would catch it must pin `TASKS_PIN_NOW` a few seconds either side of local
  midnight in a zone the case sets. That is a case-shape requirement, not a
  fixture.

### 4e. Six behaviors that are observable in principle and unproved by the corpus

These are not method limitations — they are holes a fixture would close, listed
because each is a place a port passes everything and is wrong:

1. **The prose guard.** `test_undate_never_deletes_prose_mentioning_a_stamp_keyword`
   is the campaign's only data-loss guard. No fixture's body text contains a line
   mentioning a stamp keyword, so a port that removes stamps by line-matching
   rather than by key deletes user prose, silently, on stores nobody in the
   corpus has. `store-date-stamps` names the fixture that fixes it.
2. **The clamp/skip asymmetry.** A numeric day of month clamps (`m:31` fires
   Feb 28); an ordinal weekday a month lacks is *skipped* (`m:5fri` produces
   nothing that month). Adjacent lines, opposite policies, and invisible in any
   single-occurrence output.
3. **Parity anchored on the stamp, not the clock.** Every `Nw`/`Nm`/`Ny` form
   counts blocks from the stored anchor, which is why `2y:02:5fri` fires from one
   anchor and never fires from another. A port that anchors on today passes every
   single-occurrence assertion and drifts on the second.
4. **The two `overdue?` operators.** `>=` for all-day, `>` for timed. One
   character, no JSON projection shows it, and no fixture's `TASKS_PIN_NOW` lands
   exactly on a stored boundary — so both implementations agree everywhere the
   corpus looks.
5. **A stale `lead_skip`.** `valid/full-field-matrix` carries a *matching* one,
   which is the case where the stamp works. Nothing carries a stale one or one
   without a lead, so three of `lead-skip-activation`'s seven tests have no
   differential path.
6. **Rule five's deliberate looseness.** The lead storable-range guard uses
   `Lead.date_bound`, a worst-case date, because the write path has no zone. A
   port that resolves the real instant is *more correct* and diverges — exactly
   the improvement PORTING.md forbids landing without a decision.

### 4f. Two slices that reach into campaign 6 and cannot name it

Eleven of my slices claim tests asserting undo (`*_is_undoable`,
`*_undoes_byte_exactly`, `*_restores_byte_identical_*`). `Tasks::Journal` is
campaign 6's, seeded **in parallel with this pass**, so its slice ids did not
exist when these records were written and no `depends_on` edge could name them.

I kept the tests. The property they assert is mine — one semantic change is one
history entry, and the restored bytes are my slice's bytes — and dropping them
would leave that property owned by nobody. But it means those eleven slices
cannot go green before a slice they cannot name. Each carries the same
`oracle_gaps` sentence saying so, with the same mitigation
`state-cascade-close`'s gap already prescribes for the same reason:
**characterization must count writes at `Store#save` rather than count journal
entries.** Adding the edges is an amendment owed once campaign 6's records land,
and it is the largest single piece of follow-up this pass generates.

The same shape, once, in the other direction: `temporal-patch-changeset` claims
two revision-staleness tests that five existing records reserve for campaign 6.
They are claimed because what makes them mine is the *field* — a change to a
`*_time` object or its zone must count as a semantic change for the revision,
which is a temporal rule about what the revision covers, not a rule about how
staleness is detected. The detection half is campaign 6's.

---

## 5. What I recommend, and what I did not defer

**Nothing in campaign 5 was deferred.** All 373 allocated tests and all seven
source files are claimed, plus the one cross-file test §2d explains.

Four recommendations, none of them a slice:

1. **Build `valid/recur-anchors` first.** Five slices' fixture gaps are the same
   store: a record whose schedule can never fire from its own anchor, the same
   schedule from a reachable anchor, a stamp ~2000 days stale, a `m:5fri`
   anchored in a month without one, a `+0d` hand edit, and a stamp near the
   storable year boundary. Eight records. It converts `recur-occurrence-math`,
   `store-recur-calendar-guard`, `store-recur-calendar-roll`,
   `store-recurrence-advance` and `cli-recur-preview` from "mostly unit tests" to
   "mostly cases", and every one of its behaviors is reachable through
   `tasks recur <ref>` with no writes at all.
2. **Build the availability corpus second, and know that it discharges someone
   else's obligation.** `cli-mutation-json-envelopes`' `fixtures_todo` says its
   envelope's availability triple can only prove the default "because that corpus
   is campaign 5's to build". It is `availability-model`'s. `valid/deferred-tags`
   already carries the full *tag* hold matrix; what is missing is a **timed**
   hold — a `scheduled_time` in the future, and one exactly at the pinned now.
3. **Decide the `.config/tasks/config` fixture question once.** `date-fuzzy-parse`
   and `timezone-resolution` both need one, and `campaign-10.md` §4b needed the
   same thing for the LLM keys and it was never built. No fixture in the corpus
   ships a config file, even though the runner defines a `config` role for that
   path. One fixture with three keys unblocks three slices across two campaigns.
4. **Port `cli-recur-preview` or `check-temporal-fields` first.** They are the two
   slices whose case lists can be written against the committed corpus today,
   with no fixture work, no zone override and no writes. They are the cheapest
   available end-to-end validation that the differential method works for
   temporal behavior at all.

---

## 6. Records this pass asks to amend

Nine. The first is the only one whose sentence this pass **falsifies**; the rest
are true sentences that acquire an id they should name. The inventory §4 predicted
five of these; three more surfaced while slicing, and one (§6.1) is new.

### Falsified — the sentence is now wrong

1. **`config-resolution`**, `oracle_gaps[0]`, final sentence:
   > "The rest — the timezone, date-order, theme, mouse and host-context keys —
   > belong to campaign 8's CLI and TUI work and to create-basic, and are
   > unclaimed on purpose."

   Timezone and date-order are now campaign 5's (`timezone-resolution`,
   `date-fuzzy-parse`, 10 tests between them); theme and mouse are campaign 12's
   and were never campaign 8's. Only host-context stays as written. This is the
   same amendment `campaign-10.md` §6 had to make to the same sentence for the
   seven `prompt.*` tests, and it is the third time that one sentence has been
   wrong.

   **And a second, separate correction to the same record** — this one is not in
   the inventory and is the sharpest thing this pass found. Its
   `observable_outputs` lists "`tasks config` output" with `fixtures_todo: null`
   and no gap against it. That output contains `tzdb_version`, whose value is
   `TZInfo::DataSource.get.to_s` — `"Zoneinfo DataSource: /usr/share/zoneinfo"`
   on this host — which no Go port can emit and which the comparator compares
   byte-for-byte (§4c). The record needs a gap sentence saying that its headline
   observable carries one key that is not portable and is an
   intentional-difference decision for Marcus.

### True, but needs an id

2. **`check-task-fields`**, `oracle_gaps[0]`: *"Check#check_lead (lead-span
   grammar) and Check#check_temporal_time are excluded here: their grammars are
   campaign 5. This slice must not claim their tests."* → name
   `check-temporal-fields`, and note that `lib/tasks/check.rb` now has two owners
   in the `config.rb` pattern.
3. **`query-list-filters`**, `oracle_gaps[0]`: *"Availability gating
   (`TaskQueries#build_availability`, `effective_gate`, `lead_gate`) is excluded:
   it is campaign 5 temporal work."* → name `availability-model` (the first two)
   and `lead-availability-gate` (the third).
4. **`task-view-projection`**, `oracle_gaps[0]`: *"TaskView's lead and
   availability fields are covered by test/test_lead.rb, which is campaign 5."*
   → name both of the same two slices.
5. **`query-named-views`**, `oracle_gaps[0]`: agenda's timed-boundary sort *"needs
   temporal values and lands with campaign 5"* → name `availability-model`, and
   record that the test lives in `test_views.rb`, which campaign 12 otherwise
   owns entirely, and that campaign 12's `tui-view-row-projection` excluded it
   explicitly so this is a sole claim rather than a shared one (§2d). The
   inventory §4 item 13 asked for the id; the second half is what makes the claim
   auditable later.
6. **`create-basic`**, `oracle_gaps[0]`: *"test/test_create_task.rb#test_create_recurring_task_defaults_to_today_and_uses_the_processed_state
   needs recurrence, which is campaign 5. Excluded from this slice."* → name
   `store-recurrence-attach`.
7. **`changeset-apply-basic`**, `oracle_gaps[0]`: *"Date, lead, and recurrence
   patches (patch_date, patch_lead, patch_recurrence) are campaign 5."* → three
   different slices: `store-date-stamps`, `lead-write-rules`,
   `store-recurrence-attach`, with `temporal-patch-changeset` owning the
   changeset-level coupling. Its `oracle_gaps[1]` also says two of its own
   claimed tests express field-application order through `recurrence:` and
   `scheduled:` values and "cannot go green until campaign 5" — that is now a
   nameable dependency in the other direction.
8. **`state-cascade-close`**, `oracle_gaps[0]`: `test_complete_cascade_retires_recurring_descendant`
   *"lands with campaign 5"* → name `store-recurrence-advance`. The consequence
   sentence beside it ("nothing here proves what the cascade does to a repeating
   child") stays true and is now discharged by a named slice.
9. **`cli-mutation-json-envelopes`**, two places. `oracle_gaps[0]` names `recur`
   and `lead` (campaign 5) among the blockers for the nine registry-wide tests →
   `cli-recur-command` and `cli-lead-command` remove that half of the blockage;
   `undo`/`redo` (campaign 6) remain, so the final parity slice is still campaign
   8's and is still owed. `oracle_gaps[4]` names the eight temporal verbs whose
   `--json` tests "belong with campaign 5" → they land in `cli-date-verbs`,
   `cli-defer-activate-someday`, `cli-recur-command` and `cli-lead-command`. And
   its `fixtures_todo` says the availability corpus "is campaign 5's to build" →
   it is `availability-model`'s, which now says so.

### `source_paths` and `VERB_OWNERS`

No existing record's `source_paths` becomes wrong. Six files gain co-owners
(§1b), all already reserved by the incumbent's own `oracle_gaps`, and every new
record says in `notes` that it ports only part of the file, as `campaign-10.md`
§6 required.

`VERB_OWNERS` needs no change: no campaign 5 slice introduces a store mutation
verb that is not already mapped, and `reach` returns to the pre-existing 21/0
once the delegation edges of §2e are added.

---

## 7. Verification

Run against a scratch merge of `HEAD`'s `manifest.jsonl` with these 28 records
(`MANIFEST_ISSUES_MANIFEST` / `MANIFEST_ISSUES_CAMPAIGNS`), because this pass
does not touch the merged files:

```
porting/manifest-issues validate → clean apart from 28 × "source_sha is not a
                                   40-char sha" — intended; the orchestrator
                                   computes the real pins centrally
porting/manifest-issues reach    → 21 reach(es), 0 unexplained     (exit 0)
                                   — the pre-existing baseline, restored after
                                     the three found in §2e were fixed
porting/manifest-issues progress → campaign 5: 0/28
```

Checked mechanically, additionally:

- **Every one of the 374 claimed refs resolves** to a real `def test_` in a real
  file. Zero dangling; `validate` confirms it independently.
- **No test is claimed twice within these records**, and none collides with any
  of the 53 records in `manifest.jsonl`.
- **No test collides with any of the five sibling passes** running in parallel
  (campaigns 6, 9, and 12 a/b/c — 1042 tests between them). Checked by set
  intersection against their record files: zero overlap in every direction.
- **Every `source_paths` entry exists and every `fixtures` entry is a real
  directory.** No fixture path was invented; 20 of the 28 slices carry real
  fixture paths and the other 8 carry an honest `fixtures_todo` naming the
  specific records missing. Five carry a `null` `fixtures_todo` because the corpus
  already covers them; none carries an empty `fixtures` list.
- **Every `depends_on` names a real slice** — an existing manifest id or one of
  these 28 — and every intra-campaign edge points at a slice declared **earlier**
  in the file, so the campaign can be worked top to bottom.
- **Key set and ordering are byte-identical to `manifest.jsonl`'s** 19 keys, in
  the same order, asserted per record.
