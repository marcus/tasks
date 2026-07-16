# Timed task values and time zones

Status: proposed implementation contract

Date: 2026-07-16

## Goal

Allow both task date fields to carry an optional time of day:

- `scheduled` remains the available-from/defer-until value;
- `deadline` remains the independent due value; and
- either field may remain an all-day date, gain a floating local time, or gain
  a fixed IANA time zone.

The feature must work through the CLI, TUI, and loopback HTTP API with the same
domain behavior. Existing date-only records and commands keep their meaning.
A task due on `2026-07-20` is still an all-day task; it must not quietly become
due at midnight. A task available from `2026-07-20 09:00` becomes available at
that exact time rather than at the start of the day.

These examples describe the intended result:

```sh
# All-day deadline: existing behavior.
tasks due "Submit report" 2026-07-20

# Floating time: 09:00 in whichever evaluation time zone is active.
tasks due "Take medication" "tomorrow 9am"

# Fixed time: the same instant worldwide, anchored to London civil time.
tasks due "Join customer call" "tomorrow 5pm" --timezone Europe/London

# Timed availability is independent of the deadline.
tasks defer "Publish announcement" "fri 8:30am" --timezone America/New_York
```

Notifications and reminders are not part of this feature. A deadline at 17:00
changes due/overdue calculations and presentation, but it does not cause the
process to send an alert.

## Current system and constraints

The implementation extends the existing application boundary:

- `lib/tasks/format.rb` owns the JSONL shape and canonical key order. The
  current schema version is 1.
- `lib/tasks/store.rb` turns stored `scheduled` and `deadline` strings into
  Ruby `Date` values and owns all checked, journaled writes.
- `lib/tasks/dates.rb` parses date-only expressions and returns `Date`.
- `Tasks::TaskView`, `Tasks::TaskQueries`, and `Tasks::Application` provide the
  shared read and command contracts used by all three adapters.
- `lib/tui/task_edit_form.rb` currently uses a date-only `DateInput` for both
  fields. TUI renderers calculate proximity from whole-day differences.
- `docs/api/openapi.yaml` exposes `scheduled` and `deadline` as nullable
  `YYYY-MM-DD` strings. POST and PATCH use the same date fields.
- recurrence advances calendar dates. When both fields exist, completion
  shifts `scheduled` by the deadline's calendar-day delta.
- availability currently captures one `Date.today` per operation. There are
  still many default `Date.today` calls across CLI, TUI, queries, recurrence,
  and Store methods; timed availability cannot be correct until those paths use
  one injected clock snapshot.
- the CLI and TUI are currently standard-library-only. Correct named-zone and
  DST handling requires either a maintained zone implementation or an unsafe
  process-global `ENV["TZ"]` mutation. This plan chooses a maintained runtime
  dependency and records that policy change explicitly.

All writes continue through `Tasks::Application` and `Tasks::Store`. The CLI,
TUI, API, migrations, and tests must never hand-edit task JSONL.

## Research findings

Task products do not agree that every date should secretly be a timestamp.
Their differences are useful here:

| System | Relevant behavior | Lesson for this project |
|---|---|---|
| Todoist | Supports full-day dates, floating timed dates, and fixed-zone timed dates. Its due object keeps the time zone separate from the instant. | Preserve all-day values and model floating/fixed time explicitly. |
| OmniFocus | Supports floating and fixed time zones for defer and due dates. Floating is the default; fixed values convert when the viewer changes zones. | Available-from and deadline both need the same temporal capability. |
| Things | Start dates and deadlines remain day-based; a separate reminder supplies a clock time and notification. | Do not equate “has a time” with “send a reminder.” |
| Microsoft To Do | Due is a calendar date while “Remind me” accepts a date and time. | Keep reminders outside the task deadline contract unless designed separately. |
| Taskwarrior | Normalizes date/time fields to UTC epoch seconds; date-only input defaults to midnight. Its docs warn that date filtering must account for the hidden time. | Do not collapse a date-only task into a midnight timestamp or discard its civil-time intent. |

The standards lead to the same model:

- [RFC 5545](https://www.rfc-editor.org/rfc/rfc5545) distinguishes a calendar
  `DATE`, a floating `DATE-TIME`, a UTC time, and a local time qualified by
  `TZID`. Calendar recurrence operates in civil time rather than by repeatedly
  adding a fixed number of seconds.
- [RFC 9557](https://datatracker.ietf.org/doc/html/rfc9557) can attach an IANA
  zone name to a fixed RFC 3339 timestamp, but it explicitly does not model a
  future local wall time whose instant may change when time-zone law changes.
  It is therefore useful for derived API instants, not as the only persisted
  representation.
- The [IANA Time Zone Database](https://data.iana.org/time-zones/tzdb/theory.html)
  provides identifiers such as `America/Los_Angeles`. Abbreviations such as
  `PST`, `CST`, and `IST` are not stable identifiers and must not be accepted as
  stored zones.
- [TZInfo](https://www.rubydoc.info/gems/tzinfo/TZInfo%2FTimezone%3Alocal_to_utc)
  identifies both hard DST cases: a local time may not exist during a forward
  jump, or may map to two instants during a backward jump. The product contract
  must decide both cases rather than accepting whatever the host library picks.

Primary product references:

- [Todoist fixed and floating times](https://www.todoist.com/help/articles/set-a-fixed-time-or-floating-time-for-a-task-YUYVp27q)
- [Todoist due-date API model](https://developer.todoist.com/api/v1/#tag/Due-dates)
- [OmniFocus Time Zone inspector](https://support.omnigroup.com/documentation/omnifocus/universal/4.8.11/en/inspector/#time-zone-inspector)
- [Things scheduling and reminders](https://culturedcode.com/things/support/articles/2803579/)
- [Microsoft To Do due dates and reminders](https://support.microsoft.com/en-us/todo/add-due-dates-and-reminders-in-microsoft-to-do)
- [Taskwarrior date and time behavior](https://taskwarrior.org/docs/dates/)

## Product decisions

### Three temporal forms

Each of `scheduled` and `deadline` is one logical temporal value in exactly one
of these forms:

| Form | Example | Meaning |
|---|---|---|
| Date-only | `2026-07-20` | A local calendar day with no clock time. |
| Floating time | `2026-07-20 09:00`, no stored zone | 09:00 in the configured evaluation zone. If that setting changes, the represented instant changes. |
| Fixed zoned time | `2026-07-20 09:00 America/Los_Angeles` | 09:00 under that IANA zone's rules. Other zones display an equivalent local time for the same instant. |

Date-only remains the default when no time is supplied. A time without an
explicit zone is floating, matching the default in Todoist and OmniFocus. A
fixed time always stores a full IANA identifier. Numeric offsets and time-zone
abbreviations may be accepted only as part of an unambiguous import format; they
are never persisted as a substitute for an IANA name.

The two task fields own their temporal modes independently. A task may become
available at 09:00 in Tokyo and be due at 17:00 in Los Angeles. There is no
task-wide time-zone field that can make one value silently reinterpret the
other.

Time precision is one minute. Seconds and sub-seconds are rejected on user
input and omitted from storage. Derived RFC 3339 instants include `:00` seconds
for standard interoperability.

### A date is not midnight

Date-only behavior remains calendar based:

- `scheduled: 2026-07-20` becomes available when the evaluation zone's local
  date reaches July 20.
- `deadline: 2026-07-20` is due throughout July 20 and becomes overdue when the
  evaluation zone's local date reaches July 21.
- due-soon and `urgent_days` continue to use calendar-day buckets for date-only
  deadlines.

Timed behavior is instant based:

- timed `scheduled` becomes available at its resolved instant;
- timed `deadline` becomes overdue strictly after its resolved instant; and
- two timed tasks on the same displayed date sort by instant.

For same-day ordering, timed deadlines sort by their instant and an all-day
deadline sorts after timed deadlines because it remains on time through that
day. An all-day available-from value sorts at the beginning of the date because
it releases the task for the whole day.

### Floating and fixed display

`timezone` in the tasks config is the evaluation/display zone shared by the
CLI, TUI, and API server. Resolution order is:

1. `TASKS_TIMEZONE`;
2. `timezone = ...` in the tasks config file;
3. a valid IANA identifier from `ENV["TZ"]`;
4. the host's `/etc/localtime` zoneinfo link on systems that provide one; and
5. `Etc/UTC`, reported as a fallback warning by `tasks config`.

Date-only and floating values are interpreted in this configured zone. Fixed
values are resolved in their own stored zone, then converted to the configured
zone for chronological views. Compact rows show the configured local time. The
detail view and JSON also retain the original wall time and zone so travel does
not hide the task's intent.

Changing the configured zone is a presentation/evaluation change, not a JSONL
rewrite. It changes the instant represented by floating values, but never the
instant represented by fixed values.

### Daylight-saving gaps and folds

The parser validates the complete local date, time, and effective zone before a
write:

- A nonexistent local time, such as `02:30` during a spring-forward gap, is
  rejected. CLI/API errors identify the field and zone; the TUI keeps the
  editor open and offers the first valid time after the gap.
- An ambiguous local time during a backward fold defaults to the earlier
  instant (`fold: 0`). Users can select the later instant (`fold: 1`). Both
  choices are stored explicitly enough to round-trip.
- Outside an ambiguous interval, either fold preference resolves to the one
  valid instant. Keeping the preference is necessary for a recurring value
  that is ordinary this month but lands in a fold later.

Recurring advancement that lands in a nonexistent local time skips that
candidate and advances by the recurrence interval again. It does not move the
wall time permanently from 02:30 to 03:30. Ambiguous recurring occurrences use
the stored fold choice.

### Reminders remain separate

This feature does not add a scheduler, background daemon, desktop notification,
email, webhook, or reminder offset. Timed values make a later reminder feature
possible, but that feature needs its own delivery, deduplication, sleep/wake,
and missed-notification contract.

## Stored representation

### Schema version 2

Schema version 2 retains the existing `scheduled` and `deadline` date strings
and adds optional structured time metadata immediately after each date in
canonical key order:

```json
{"type":"task","id":"a1b2c3d4","state":"NEXT","title":"Take medication","deadline":"2026-07-20","deadline_time":{"local":"09:00"}}
{"type":"task","id":"b2c3d4e5","state":"NEXT","title":"Join customer call","deadline":"2026-07-20","deadline_time":{"local":"17:00","timezone":"Europe/London"}}
{"type":"task","id":"c3d4e5f6","state":"TODO","title":"Publish announcement","scheduled":"2026-11-01","scheduled_time":{"local":"01:30","timezone":"America/Los_Angeles","fold":1},"deadline":"2026-11-01","deadline_time":{"local":"12:00"}}
```

The canonical task key order becomes:

```text
type id parent state priority title tags
scheduled scheduled_time deadline deadline_time recur
closed archived body
```

Time metadata has this stored shape:

```json
{
  "local": "HH:MM",
  "timezone": "Area/Location",
  "fold": 1
}
```

- `local` is required and uses zero-padded 24-hour minute precision.
- an omitted `timezone` means floating; a present value must resolve through
  the IANA database;
- `fold` is omitted for the normal/earlier instant and stored as `1` only for
  the later ambiguous instant; and
- UTC instants, offsets, abbreviations, and derived display strings are not
  stored. They are recomputed from the civil value and current time-zone data.

Keeping the existing date and adding time metadata has four useful properties:

1. Date-only records stay legible and unchanged apart from the meta version.
2. Existing API date properties do not change type.
3. A date can be edited without manufacturing a clock time.
4. Future zone-rule updates can recompute a fixed task's intended local wall
   time rather than freezing the offset that happened to be known at creation.

The duplication is structural, not semantic: `deadline` owns the calendar
date, while `deadline_time` owns only the optional clock/zone part. Neither can
encode the other's information.

### Validation invariants

`Tasks::Check` enforces:

- `scheduled_time` requires `scheduled`; `deadline_time` requires `deadline`;
- each time object contains only `local`, `timezone`, and `fold`;
- `local` is a real `00:00` through `23:59` minute value;
- a zone is a canonical or accepted linked IANA identifier known to TZInfo;
- `fold` is either omitted or the integer `1`; it chooses the later period when
  two are valid and remains a harmless recurrence preference otherwise;
- the date/time/zone combination exists;
- section records carry none of the four task temporal fields; and
- recurrence still requires at least one complete scheduled/deadline temporal
  value.

Readers remain defensive: malformed time metadata yields a date-only fallback
for presentation while `check` reports the record. Mutations refuse to proceed
against an invalid store, preserving the current rollback contract.

### Domain value object

Add a small immutable value layer, tentatively:

```text
Tasks::TemporalValue
  date              Date
  local_time        HH:MM value or nil
  timezone          IANA id or nil
  fold              0 or 1
  all_day?
  floating?
  fixed?

Tasks::TemporalContext
  now               UTC Time captured once
  timezone          resolved evaluation TZInfo::Timezone
  local_date
```

`TemporalValue` owns parsing-independent temporal operations: resolving an
instant, projecting for display, comparing release/due boundaries, shifting a
calendar date while preserving time metadata, and serializing stored/API
forms. `TemporalContext` owns the single operation clock snapshot.

Do not make `TemporalValue` pretend to be `Date` through broad operator
delegation. Update call sites to ask explicitly for `date`, `release_instant`,
`due_boundary`, or `display_time`. This costs a larger but mechanical refactor
and prevents whole-day code from accidentally treating a timed value as a
date.

## Dependency decision

Use `tzinfo` 2.x for IANA lookup, UTC conversion, transitions, ambiguous times,
and nonexistent-time detection. Add `tzinfo-data` only on platforms without a
usable system zoneinfo database.

This changes the CLI/TUI's current “Ruby stdlib only” claim. Record the choice
in a new ADR before implementation and update installation docs. The rejected
options are:

1. mutate `ENV["TZ"]` around conversions, which is process-global and unsafe
   under Puma threads or TUI background work;
2. store numeric UTC offsets, which fail across DST and political zone-rule
   changes;
3. parse TZif files in this repository, which would create a security- and
   correctness-sensitive time-zone library to avoid one maintained dependency;
4. store UTC only, which loses floating behavior and recurring civil time; and
5. shell out to platform `date` utilities, which is slow, platform-specific,
   and hard to make atomic or testable.

Rack and Puma remain isolated to API boot paths. TZInfo is a shared domain
dependency because all surfaces need the same answer. Core tests still run
without loading Rack/Puma, but the documented install/test path becomes
Bundler-backed.

## One clock snapshot per operation

Replace the current `today:` plumbing with an injected `TemporalContext` at the
application boundary. Each adapter captures `Time.now.utc` once, pairs it with
the resolved evaluation zone, and passes the same frozen context through:

- fuzzy date/time parsing;
- availability and ancestor-blocker selection;
- due/overdue and urgency calculation;
- recurrence advancement and completion notes;
- dry-run preview and post-mutation resource reads;
- CLI/TUI rendering and sorting; and
- API list/resource representation.

No downstream domain method may call `Date.today` or `Time.now`. Presentation-
only elapsed timers such as the TUI's three-second flash (today wall-clock
`Time.now`, ideally monotonic) are not part of task semantics and may keep
their own clock.

`OperationContext` remains provenance (`cli`, `tui`, `api`, operation id,
actor). It should carry or reference the temporal snapshot rather than absorb
zone conversion methods itself.

Tests construct contexts from an explicit UTC instant and IANA zone. They must
not change `ENV["TZ"]` or depend on the machine running the suite.

## Availability and view semantics

### Exact release boundaries

For each own or ancestor `scheduled` value, derive a release boundary:

- date-only: the first valid instant of that calendar date in the evaluation
  zone;
- floating time: that local date/time in the evaluation zone;
- fixed time: that local date/time in the stored zone.

Some civil dates begin with a clock transition. The date-only helper must ask
the time-zone library for the earliest valid instant whose local date is the
target date, rather than assuming every date begins at `00:00:00`.

An open task is available when every scheduled boundary in its own/ancestor
chain is less than or equal to the operation's captured `now`, and no own or
ancestor On Hold marker wins. Existing On Hold precedence is unchanged. When
several timed blockers exist, the blocker with the latest release instant wins;
equal instants keep the existing self-then-nearest-ancestor tie break.

`Tasks::TaskQueries::Availability` adds the winning `available_at` UTC instant
and the winning temporal value. The reason enum remains `available`,
`scheduled`, `on_hold`, `ancestor_scheduled`, `ancestor_on_hold`, or `closed`.

`activate` clears an own scheduled value whenever its release boundary is in
the future. This includes a task scheduled for later today, which the current
date-only comparison cannot detect. A released time or past date remains as
history, matching the existing activate rule.

### Deadline boundaries

For deadline calculations:

- date-only becomes overdue when the evaluation local date is later than the
  stored date;
- floating time becomes overdue after its instant in the evaluation zone; and
- fixed time becomes overdue after its instant in the fixed zone.

The comparable due boundary for a date-only deadline is the first valid instant
of the following calendar date in the evaluation zone. This is derived only for
sorting/countdowns; it is not persisted as the task's due value.

Agenda grouping and compact display use the deadline projected into the
evaluation zone. Detail/JSON views also show the stored civil value. `urgent_days`
continues to select calendar-day buckets in the evaluation zone; a timed task
due within the selected final day is urgent. A timed task is immediately urgent
once overdue.

### Sorting and aggregates

Define shared sort keys in the temporal value layer. Do not rebuild them in the
CLI, TUI, and project queries.

- Agenda keeps keying each task by its deadline when present, otherwise its
  available-from date (the existing coalesced sort key), extended to the exact
  instant; all-day deadlines sort at the end of their date.
- Short list rows include a time when present: `7/20 9:00a`, with a compact
  zone indicator when the value is fixed outside the evaluation zone.
- Project `next_date` remains the compatibility calendar date. Add
  `next_time`/`next_at` to canonical and HTTP project resources so a timed
  aggregate does not lose precision.
- Quadrant urgency uses the deadline boundary. A future available-from time
  never makes a task urgent.
- timed unavailable rows show their time, not only `⏳ 7/20`.

The TUI must repaint at the next relevant minute/release/deadline boundary even
when no file mtime changes. A timed task cannot remain hidden or non-overdue
until the user presses a key. Compute the nearest boundary from the current
read snapshot and cap the existing poll timeout to it; do not add a background
writer.

## CLI contract

### Accepted expressions

Extend the date parser through a new `Tasks::TemporalParser`; keep
`Tasks::Dates.parse_when` as the date-only primitive. Commands accepting dates
also accept:

```text
today 5pm
tomorrow at 09:30
fri noon
2026-07-20 17:00
2026-07-20T17:00
```

Time tokens support `H:MMam`, `H:MMpm`, `HH:MM`, `noon`, and `midnight`.
Natural-language parsing remains intentionally bounded; it is not a general
English date library. A bare time without a date is rejected by commands that
replace a complete temporal value, preventing an implicit today/tomorrow guess.

For `due`, `schedule`, and timed `defer`:

```sh
tasks due <ref> <date-or-date-time> [--timezone ZONE | --floating]
  [--fold earlier|later] [--dry-run] [--json]

tasks schedule <ref> <date-or-date-time> [same temporal flags]
tasks defer <ref> <date-or-date-time> [same temporal flags]
```

- no time produces a date-only value and clears any existing time metadata;
- a time with neither flag is floating;
- `--timezone` fixes the value to that IANA zone;
- `--floating` explicitly removes a prior fixed zone while retaining the new
  local date/time;
- the two mode flags are mutually exclusive;
- `--fold later` stores fold 1 as the preference used whenever the local time
  is ambiguous; and
- command replacement is atomic and produces one undo entry.

`capture` accepts per-field settings because available-from and deadline may
use different zones:

```sh
tasks capture "..." \
  --scheduled "tomorrow 9am" --scheduled-timezone America/Los_Angeles \
  --due "tomorrow 5pm" --due-timezone Europe/London
```

Add `--scheduled-floating`, `--due-floating`, `--scheduled-fold`, and
`--due-fold` for complete parity. Reject a temporal modifier when its matching
date flag is absent.

`undate` clears the date and its time metadata together. `undate --kind
deadline|scheduled` (the existing flag) clears only that complete temporal
value. `defer` with no date continues to mean On Hold and takes no temporal
flags. `activate`, recurrence commands, dry-run, undo/redo, and mutation
output use the shared application command and temporal context.

### CLI JSON

Keep existing `scheduled` and `deadline` date strings. Add nullable
`scheduled_time` and `deadline_time` objects matching the HTTP representation
below. This is additive for date-only consumers and avoids changing a field
from a date string to a union type.

Human output uses the configured 12/24-hour preference. Add `time_format = 12`
or `24` to config, defaulting from the process locale when reliable and to 12
otherwise. JSON always uses `HH:MM` and RFC 3339.

`tasks config` prints the effective `timezone`, `time_format`, their sources,
and the TZInfo/tzdb version. It warns when UTC was a host-detection fallback.

## TUI contract

### Editing

Replace each date-only editor row with one temporal control that owns:

- date;
- optional time;
- mode (`All day`, `Floating`, `Fixed`);
- searchable IANA zone when fixed; and
- fold choice only when the selected local time is ambiguous.

The compact field value is one line, for example:

```text
Available from   Jul 20, 9:00 AM · floating
Deadline         Jul 20, 5:00 PM · Europe/London
```

Return opens the existing calendar picker extended with time/mode controls.
Typing a supported expression still works. Switching to All day removes time
metadata but preserves the date. Switching Floating/Fixed preserves the local
date and time. Escape discards the draft.

The editor sends the entire `TemporalValue` as one field-owned patch. Date,
time, zone, and fold are not independent save-on-blur writes; splitting them
would create transient invalid records and weaker conflict checks. `EditSnapshot`
baselines and semantic equality include the complete value.

The quick `d` date action and `z` Defer until action use the same temporal
editor. `z now`/Activate uses exact release semantics. The action palette and
keyboard shortcuts remain aliases for application commands, not separate
mutations.

### Rendering

- Agenda stamps show time when present and retain `AVL` versus `DUE`.
- List/Next rows show compact due time; fixed values display a zone abbreviation
  only as presentation, never as stored identity.
- Details show stored wall time, mode, IANA zone, configured-zone projection,
  and relative status (`in 2h`, `overdue by 14m`).
- A floating value is labeled `floating`; do not imply it is fixed to the
  current zone just because that zone was used for this render.
- timed own and inherited availability badges include the release time.
- project rows and counts use the same exact temporal query result as flat
  views.
- Markdown export writes an unambiguous civil form on the existing bullet
  lines, such as `- deadline: 2026-07-20 17:00 [Europe/London]` and
  `- available from: 2026-07-20 09:00`; date-only export stays
  `- deadline: 2026-07-20`.

The minute-boundary refresh must preserve selection, scroll position, open
editor drafts, reveal mode, and collapse state just like an external-file
refresh.

## HTTP API and OpenAPI contract

### Representation

Keep `scheduled` and `deadline` as required nullable ISO dates. Add required
nullable `scheduled_time` and `deadline_time` to task responses:

```json
{
  "scheduled": "2026-07-20",
  "scheduled_time": {
    "local": "09:00",
    "timezone": null,
    "fold": 0,
    "effective_timezone": "America/Los_Angeles",
    "instant": "2026-07-20T16:00:00Z"
  },
  "deadline": "2026-07-20",
  "deadline_time": {
    "local": "17:00",
    "timezone": "Europe/London",
    "fold": 0,
    "effective_timezone": "Europe/London",
    "instant": "2026-07-20T16:00:00Z"
  }
}
```

Response time objects contain:

- `local`: stored `HH:MM` wall time;
- `timezone`: stored fixed IANA zone or null for floating;
- `fold`: `0` or `1`;
- `effective_timezone`: zone used to resolve this response; equal to the stored
  fixed zone or the server's evaluation zone for floating values; and
- `instant`: RFC 3339 UTC timestamp derived using the request's single temporal
  context.

Date-only values return null time objects. Add `available_at` (the same name
the availability query exposes) as a nullable derived RFC 3339 instant for the
winning timed/date release boundary. Project
resources retain `next_date` and add nullable `next_time` plus `next_at`.

`GET /api/v1/meta` adds:

```json
{
  "timezone": "America/Los_Angeles",
  "time_format": 12,
  "tzdb_version": "...",
  "temporal_precision": "minute"
}
```

The API uses the server's configured evaluation zone. Client-selected per-
request display zones are out of scope for this slice; adding such a header
later would require cache/Vary rules and a clear interaction with floating
values.

### Create and patch

Create and patch accept a narrower `TaskTimeInput`:

```json
{
  "local": "17:00",
  "timezone": "Europe/London",
  "fold": 0
}
```

`timezone` and `fold` are optional; omitted/null timezone means floating and
omitted fold means 0. Derived fields are rejected in request bodies.

Request rules:

- `scheduled_time` requires a scheduled date either already on the resource or
  supplied in the same request; the deadline pair behaves identically.
- create with time but no matching date is `422 validation_failed`.
- PATCH date without a time field preserves existing time metadata, allowing a
  calendar move without changing 09:00/fixed/floating intent.
- PATCH time replaces the whole time object atomically.
- PATCH time `null` converts the existing value to date-only.
- PATCH date `null` clears both date and time, even when the time property is
  absent.
- a request combining date `null` with non-null time is rejected.
- nonexistent, ambiguous/fold, unknown-zone, and invalid-local errors appear in
  the existing structured field-error envelope.

`TaskChangeset::FIELD_ORDER` treats each date/time pair as one logical field.
The task revision's `own` component (the existing ETag structure is
`v1.<own>.<location>.<lifecycle>`) includes all stored time metadata; a stale
zone/time edit must fail exactly like a stale date edit (412 `stale_revision`).

This is an additive JSON expansion of `/api/v1`, but strict clients generated
from the old `additionalProperties: false` response schema may need
regeneration. Call that out in release notes. Do not create `/api/v2` solely to
add nullable companion fields while the existing date properties and meanings
remain intact.

### Contract validation

Update every embedded OpenAPI example, component, POST/PATCH schema, project
resource, meta resource, and error example. Route-produced traffic and all
examples must continue to validate through the `openapi_first`-backed test
gates (`test/api/test_app.rb` contract assertions and
`test/api/test_toolchain.rb`); the server keeps its own hand-rolled request
validation, as today. The Rack adapter does only transport shape validation
and mapping; zone resolution and temporal semantics stay in shared
domain/application code.

## Recurrence contract

Recurrence advances the civil date and preserves time metadata:

| Temporal form | Recurring result |
|---|---|
| Date-only | Existing calendar-date advancement. |
| Floating time | Advance the calendar date; preserve local time, fold choice, and floating mode. |
| Fixed time | Advance the calendar date in the fixed zone; preserve local time, zone, and fold choice. Recompute the UTC instant under the new date's zone rules. |

Prefix behavior remains:

- `+` steps once from the stored civil date;
- `++` steps by calendar intervals until the resulting occurrence boundary is
  strictly in the future; and
- `.+` projects the completion instant into the temporal value's effective
  zone, uses that local date as the base, then applies the calendar interval.

For floating `.+`, the effective zone is the operation's configured zone. For
fixed `.+`, it is the value's stored zone. Completion notes may remain
date-stamped in this feature; changing `closed`, `archived`, or `- Did [...]`
history to timestamps is a separate data-retention decision.

When both `scheduled` and `deadline` exist, compute the deadline's next civil
date with its own temporal mode. Shift the scheduled civil date by the same
calendar-day delta, preserving scheduled's independent local time, zone, and
fold. Do not preserve elapsed seconds across a DST change; the existing
availability-to-due calendar window is the invariant.

If a recurrence candidate is nonexistent in its effective zone, advance the
interval again until a valid candidate is found. Apply the same positive-count
termination protections as current `++` recurrence and cap pathological loops
with a typed invalid result rather than hanging.

Removing the last date still removes recurrence. Removing only time metadata
does not; the recurring task becomes all-day.

## Migration and compatibility

### Version migration

Add a checked v1-to-v2 migration owned by `Tasks::Store` and exposed through
`Tasks::Application`. The migration:

1. acquires the same sidecar lock as normal writes;
2. validates live and existing archive files as version 1;
3. changes only each meta record's version from 1 to 2;
4. writes through the atomic writer;
5. validates both outputs as version 2; and
6. rolls both files back if either installation or validation fails.

No task record rewrite is needed because v1 contains only date-only values.
The migration is idempotent. `tasks migrate` is the operator entry point and
prints a dry-run summary before writing. It has no fuzzy ref and no API parity
requirement because schema deployment is an operator action, not task behavior.

The new CLI/TUI/API may read v1 stores in a compatibility mode to show the
migration requirement, but ordinary mutations against v1 must refuse with a
typed `migration_required` result. The TUI offers a confirmation that invokes
the shared migration command; the API returns a safe
`409 schema_migration_required` with the required/current versions and never
auto-migrates an HTTP request.

An old binary sees version 2 and refuses before writing, which is safer than
letting `undate` orphan time metadata. This intentional one-way compatibility
boundary belongs in release notes and backup instructions.

### Undo, revisions, and archive

- The migration creates a recoverable pre-migration backup, rotates the normal
  undo/redo journal, and establishes a schema barrier. Ordinary undo cannot
  cross back to v1 after v2 time metadata exists; restoring the backup is an
  explicit operator recovery that first verifies no later changes would be
  discarded.
- Each task remains a single JSONL line (time metadata is inline, never a
  separate record) and each mutation remains one undo entry; the journal keeps
  its whole-file content-addressed snapshots as today.
- Unknown JSON keys retain the formatter's forward-compatible round-trip
  behavior, while known time objects use canonical nested key order.
- Archive sweep preserves time metadata byte-for-byte and version 2 archives
  validate it.
- task revisions include normalized temporal objects, not derived instants;
  updating tzdata alone must not invalidate every ETag or store revision.

## Implementation slices and expected files

### 1. Plan and architecture gate

- approve this implementation contract;
- add an ADR for the temporal model, TZInfo dependency, floating/fixed policy,
  and schema version boundary;
- run `git diff --check`; and
- do not begin adapter work until the value/storage model is accepted.

### 2. Temporal core and configuration

Expected files:

- `Gemfile`, `Gemfile.lock`
- `lib/tasks/config.rb`
- new `lib/tasks/temporal_value.rb`
- new `lib/tasks/temporal_context.rb`
- new `lib/tasks/timezones.rb`
- new `lib/tasks/temporal_parser.rb`
- `lib/tasks/dates.rb`
- focused parser, zone, DST, config, and value-object tests

Implement IANA discovery, one-minute parsing, fixed/floating resolution, fold
handling, boundary helpers, config precedence, and the one-clock snapshot.

### 3. Schema, migration, and Store model

Expected files:

- `lib/tasks/format.rb`
- `lib/tasks/check.rb`
- `lib/tasks/store.rb`
- `lib/tasks/journal.rb` if migration recovery needs an explicit guard
- migration/check/store tests and version-1 fixtures

Bump schema version, add canonical nested serialization, validate invariants,
build `Item` temporal values defensively, implement migration, and prove
rollback/file integrity.

### 4. Shared application/query semantics

Expected files:

- `lib/tasks/application.rb`
- `lib/tasks/operation_context.rb`
- `lib/tasks/task_queries.rb`
- `lib/tasks/task_view.rb`
- `lib/tasks/create_task.rb`
- `lib/tasks/edit_snapshot.rb`
- `lib/tasks/task_changeset.rb`
- `lib/tasks/task_patch.rb`
- `lib/tasks/quadrants.rb`
- `lib/tasks/recur.rb`
- `lib/tasks/store.rb` again — the activate patch (`patch_activate`) and
  recurrence advancement (`advance_recurrence_records`) semantics live here,
  not in `application.rb`
- shared command/query/resource/revision tests

Replace semantic `Date.today` defaults, group date/time patches, implement exact
availability and due boundaries, update sorting/aggregates, and preserve civil
recurrence.

### 5. CLI

Expected files:

- `bin/tasks`
- `test/test_cli_mutations.rb`
- `test/test_dates.rb` plus new temporal CLI tests

Add expression/zone/fold flags, capture variants, migration command, compact
rendering, JSON fields, config output, help text, dry-run, errors, and undo
coverage.

### 6. TUI and TermForm

Expected files:

- `lib/term_form/fields.rb`
- `lib/tui/app.rb`
- `lib/tui/views.rb`
- `lib/tui/task_edit_form.rb`
- `lib/tui/task_editor_session.rb`
- `lib/tui/task_details.rb`
- `lib/tui/project_details.rb`
- `lib/tui/export.rb`
- `lib/tui/shortcuts.rb`
- corresponding form/editor/app/view/modal/export tests

Build one atomic temporal form field, fixed-zone search, ambiguity choice,
minute-boundary refresh, rendering, migration prompt, and conflict-safe saves.
Keep `TermForm` generic: it may provide a composable date/time field, but task
zones and recurrence policy remain under `Tasks`/`Tui` rather than becoming
embedded domain rules in the form library.

### 7. HTTP API and OpenAPI

Expected files:

- `docs/api/openapi.yaml`
- `lib/tasks/api/app.rb`
- `lib/tasks/api/representation.rb`
- `lib/tasks/api/errors.rb`
- `test/api/test_app.rb`
- `test/api/test_toolchain.rb`
- `test/api/test_projects.rb`
- `test/api/test_black_box.rb`

Add input/output schemas, strict request validation, meta fields, exact
representation, typed errors, ETag coverage, and cross-process CLI/API parity.

### 8. Documentation and agent guidance

Expected files:

- `docs/cli-spec.md`
- `docs/conventions.md`
- `README.md`
- `TASK_AGENT.md`
- `.agents/skills/tasks-cli/SKILL.md`
- `.claude/skills/tasks-cli/SKILL.md`
- relevant examples and usage comments in `bin/tasks`

Document the dependency/install change, all-day versus floating/fixed meaning,
configuration, accepted expressions, API fields, migration, DST errors, and
the fact that a time does not create a reminder. Both task-agent skill copies
must stay identical in behavior.

### 9. Adversarial review and proof

Have an independent reviewer attempt to falsify:

- date-only compatibility;
- exact timed availability and overdue transitions;
- mixed own/ancestor blockers in different zones;
- floating versus fixed behavior after changing config zone;
- ambiguous/nonexistent DST handling;
- recurrence across both DST directions and month/year clamps;
- TUI refresh without file writes;
- API PATCH pair semantics and stale ETags;
- v1 migration rollback; and
- CLI/TUI/API parity from one clock snapshot.

Resolve every confirmed P0-P2 finding before release.

## Test matrix

### Temporal primitives

- valid/invalid 12-hour and 24-hour input;
- leap day, month boundary, year boundary, noon, and midnight;
- IANA canonical name, accepted link, unknown zone, abbreviation rejection;
- fixed and floating projection into at least three zones;
- spring gap rejection;
- fall fold earlier/later round-trip;
- a non-DST zone and a zone with non-hour offsets;
- a historical/political offset change from fixture tzdata behavior; and
- host-zone detection and UTC fallback warning.

### Availability and due behavior

- date-only boundary before/on/after local date;
- timed boundary one minute before/at/after instant;
- a later-today scheduled task hidden until its time;
- date-only and timed blockers mixed across three ancestors;
- fixed blockers in different zones with equal and unequal instants;
- On Hold still winning over every timed blocker;
- all-day deadline on time through the day;
- timed deadline overdue immediately after its instant;
- urgent-day classification at the final configured day;
- agenda ordering of timed and all-day values on one date; and
- project `next_date`/`next_time`/`next_at` parity.

### Mutation and recurrence

- date-only to floating to fixed to all-day transitions;
- date change preserving time on PATCH versus CLI full replacement clearing it;
- clearing date also clearing time;
- invalid orphan time rejected;
- exact undo/redo of time, zone, and fold;
- stale field and HTTP ETag conflicts;
- `activate` clearing later-today availability;
- fixed/floating `+`, `++`, and `.+` across DST;
- two-date recurrence with independent zones;
- nonexistent recurrence candidate skipping without wall-time drift;
- ambiguous recurrence preserving fold; and
- cascade completion/archive preserving or retiring recurrence as today.

### Cross-surface and migration

- one sandbox task created via CLI and read/edited via TUI application seam and
  HTTP with identical civil/instant values;
- HTTP edit visible to a fresh CLI process and undoable there;
- API response and every OpenAPI example validate;
- v1 live-only, live+archive, empty archive, already-v2, invalid-v1, and
  interrupted migration cases;
- old binary refusal against v2 documented/proved with a fixture where
  practical; and
- no test points at the user's real task files.

Use explicit instants and zones in every test. Do not let a test's expected
result depend on the developer machine's current date, zone, locale, or DST
state.

## Quality attributes and risks

| Attribute | Plan |
|---|---|
| Correctness | Civil value remains canonical; TZInfo resolves instants; gap/fold behavior is explicit. |
| Compatibility | Existing date fields remain ISO dates; new time fields are nullable companions; schema migration is one-way and guarded. |
| Maintainability | One temporal value/context layer replaces scattered date/time arithmetic. |
| Testability | Every operation receives an injected instant and zone; DST fixtures are deterministic. |
| Performance | Zone resolution is local and cached by immutable zone id; task-list scale does not justify persisted UTC indexes. |
| Reliability | No midnight/minute writer; reads derive state. TUI wakeups only trigger repaint. |
| Security | API rejects unknown zones/fields and bounded JSON rules remain. No shelling out or process-global TZ mutation. |
| Agent compatibility | CLI grammar, JSON, docs, and both skills expose one clear all-day/floating/fixed vocabulary. |

The largest risks are:

1. **Dependency policy change.** TZInfo ends the strict stdlib-only claim. The
   ADR and README must be candid, and startup errors must explain installation.
2. **Scattered `Date.today` calls.** Missing even one semantic call site can
   create a response that crosses a minute/day boundary internally. Add a test
   or static guard for prohibited domain clock calls.
3. **TUI partial writes.** Date/time/zone must remain one field patch; separate
   blur events can orphan metadata.
4. **API companion-field rules.** PATCH preservation versus clearing must be
   contract-tested because both are reasonable client assumptions.
5. **DST recurrence surprises.** The skip-invalid rule must be visible in docs
   and completion output when an occurrence is skipped.
6. **tzdata changes.** A fixed future civil value may resolve to a different UTC
   instant after a government rule update. That is intentional: the stored wall
   time and named zone are the user's commitment. Derived instants and ETags
   must not be persisted.

Architecture review outcome: **approved with conditions**. Implementation may
proceed after the temporal/dependency ADR is accepted, the schema migration is
proved reversible on fixtures, and the one-clock-context refactor lands before
adapter-specific timed behavior.

## Repository gates

After implementation:

```sh
bundle exec ruby test/all.rb
bundle exec ruby test/api/all.rb
bin/tasks check
git diff --check
```

Also record a sandbox transcript proving:

1. an all-day date remains all-day;
2. a floating task changes instant after changing evaluation zone;
3. a fixed task keeps its instant and changes displayed local time;
4. timed availability releases without a file write;
5. timed overdue status changes at the exact minute;
6. recurrence preserves wall time across a DST transition;
7. CLI/API writes are mutually visible and undoable; and
8. v1 migrates to v2 without changing task records.

Include a reproducible TUI capture showing an all-day task, a floating timed
task, and a fixed-zone timed task in Agenda and the editor.

## Acceptance criteria

- Both `scheduled` and `deadline` support all-day, floating timed, and fixed
  IANA-zoned values.
- CLI, TUI, and API create, read, update, clear, sort, and render those values
  through shared application/domain code.
- date-only behavior is unchanged and never normalized to midnight storage.
- availability and overdue status change at the exact resolved instant without
  background data mutations.
- fixed values preserve civil time and zone; floating values follow the
  configured evaluation zone.
- DST gaps/folds have deterministic validation and recurrence behavior.
- recurrence preserves local time/mode/zone and the two-date calendar window.
- HTTP date fields remain strings and nullable companion time objects expose
  unambiguous derived instants.
- ETags, undo/redo, archive, migration, and invalid-store rollback include all
  time metadata.
- the TUI refreshes at temporal boundaries without losing UI/editor state.
- OpenAPI examples and route traffic validate, both suites pass, task files
  check cleanly, and the implementation docs/agent prompts agree.

## Out of scope

- reminders, alerts, notification delivery, or background daemons;
- task duration, calendar blocking, or start/end event ranges;
- geolocation-triggered reminders;
- second/sub-second precision;
- client-selected API display zones;
- changing `closed`, `archived`, journal timestamps, or completion-note dates
  into zoned timestamps;
- importing/exporting full iCalendar `VTIMEZONE` components;
- remote API authentication or multi-user time-zone preferences; and
- silently guessing time zones from abbreviations, coordinates, task text, or
  project names.
