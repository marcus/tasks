# ADR 0010: Civil temporal values and named time zones

- Status: Accepted
- Date: 2026-07-16

## Context

Tasks currently store `scheduled` and `deadline` as calendar dates. Adding a
clock time cannot turn those dates into midnight timestamps without changing
their meaning: an all-day deadline remains on time throughout its calendar day,
while a timed deadline becomes overdue immediately after its resolved instant.

The application also needs floating local times, fixed named-zone times,
daylight-saving gap and fold handling, civil-time recurrence, and identical
answers from the CLI, TUI, and HTTP API. Ruby's process-global `ENV["TZ"]`
cannot safely provide those semantics under a threaded API server.

## Decision

Each `scheduled` and `deadline` value is represented independently as one of:

- an all-day calendar date;
- a date plus floating `HH:MM` local time; or
- a date plus `HH:MM`, an IANA time-zone identifier, and an optional later-fold
  preference.

The persisted date remains an ISO `YYYY-MM-DD` string. Optional companion
`scheduled_time` and `deadline_time` objects store civil time metadata. UTC
instants and numeric offsets are derived and are never persisted.

The shared domain layer uses TZInfo 2.x for named-zone lookup and local/UTC
conversion. `tzinfo-data` is installed only where the host lacks system zoneinfo
data. This intentionally replaces the CLI/TUI's strict standard-library-only
runtime claim; Rack and Puma remain isolated to API boot paths.

Every application operation captures one UTC instant and one configured
evaluation zone in a `TemporalContext`. Domain parsing, availability,
due/overdue calculations, recurrence, sorting, rendering, and response
serialization use that snapshot. Domain code does not consult `Date.today`,
`Time.now`, or mutate `ENV["TZ"]`.

Date-only boundaries remain calendar based. Timed boundaries are instant based.
Floating values resolve in the operation's evaluation zone. Fixed values resolve
in their stored zone and may be projected into the evaluation zone for display.

Nonexistent local times are rejected. An ambiguous local time defaults to its
earlier instant; a stored `fold: 1` chooses the later instant. Recurrence advances
civil dates while preserving local time, zone, and fold preference. A candidate
that lands in a daylight-saving gap is skipped by advancing the recurrence again.

The JSONL schema advances from version 1 to version 2. A checked, locked,
rollback-safe operator migration changes only live/archive meta versions. Version
1 data remains readable for migration guidance, but ordinary writes require
version 2. Old binaries safely reject version 2. Undo cannot cross the schema
barrier; recovery uses the explicit pre-migration backup.

## Consequences

- All-day values preserve their existing storage and behavior.
- Fixed future values retain the user's named-zone civil intent even when tzdata
  updates change the derived UTC instant.
- Changing the configured zone changes floating instants but not fixed instants.
- Response ETags and store revisions cover stored civil metadata, not derived
  instants, so tzdata upgrades do not invalidate every task.
- The CLI and TUI now require the shared bundle/runtime dependency installation.
- Reminders and notification delivery remain separate product concerns.

## Rejected alternatives

- Mutating `ENV["TZ"]`: process-global and unsafe under concurrent work.
- Persisting numeric offsets or UTC only: loses future civil-time and floating
  semantics across rule changes.
- Parsing TZif in this repository: recreates security-sensitive time-zone code.
- Shelling out to platform date tools: non-portable, slow, and difficult to
  compose atomically.
