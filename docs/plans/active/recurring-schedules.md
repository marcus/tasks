# Recurring schedules: calendar recurrence across CLI, TUI, and API

Status: accepted implementation contract (2026-07-28)

## Goal

Recurrence today is interval-only: an org-style cookie (`.+1w`, `+2d`, `++1m`)
that advances a date by N days/weeks/months/years. That covers "every two
weeks" but not the schedules people actually live by:

- **every Monday** / **every Mon, Wed, Fri** / **weekdays**
- **the 1st of the month** / **the 15th** / **the last day of the month**
- **the 2nd Tuesday of the month** / **the last Friday**
- **every July 4** (yearly on a calendar date)
- **every 2 weeks on Monday** (interval + day anchor)

This plan adds those as first-class schedules with one shared engine, exposed
with full parity on every surface: CLI, TUI, and HTTP API. Input accepts two
forms — natural phrases ("every monday") and a compact canonical grammar
(`w:mon`) — both normalizing to one stored representation.

Non-goals: no calendar-app sync, no RRULE storage, no per-occurrence exception
lists ("skip next one" is just editing the date), no sub-daily recurrence.

## Current code and constraints

- `lib/tasks/recur.rb` owns cookie parsing (`parse_interval`), validation
  (`cookie?`), and next-date math (`next_date`, `next_temporal_date` with
  DST/timezone handling via `TemporalValue` + `TemporalContext`).
- `lib/tasks/store.rb` — `Task#recurring?` gates on `Recur.cookie?`;
  `advance_recurrence_records` rolls the date on `done` (deadline preferred,
  scheduled shifted by the same offset when both exist, `- Did [date].`
  appended, delegation rolled forward); `patch_recurrence` and
  `normalize_create_recurrence` validate writes; PROPOSED tasks reject
  recurrence; removing the last date removes the cookie.
- `bin/tasks` — `recur <ref> <interval>` (`--from`, `--on`, `--dry-run`,
  `--json`), `capture --recur`, `list --recurring`.
- `lib/tui` — `r` opens the recurrence popup (`open_recur_popup`), `↻` badge,
  completion follows a rolled task.
- `lib/tasks/api/app.rb` — `recurrence` on create/patch via
  `normalize_recurrence`; `recurring` query filter; representation exposes the
  raw cookie. `docs/api/openapi.yaml` documents it.
- `lib/tasks/check.rb` reports invalid stored cookies without breaking reads:
  a task with a junk cookie is treated as non-recurring.

Constraints that carry forward unchanged:

- The stamp on the task **is** the next occurrence. Recurrence only defines
  how it advances on completion. No materialized future occurrences.
- `recur` stays a single scalar string field. No new record fields, no schema
  bump, no migration — every existing cookie remains valid as-is. Scalar LWW
  merge semantics (multi-device) keep working for free.
- All writes go through `Tasks::Application` / `Tasks::Store` — lock, journal,
  atomic write, post-write check. No surface grows a private mutation path.
- Recurrence preserves the stamp's local time and timezone metadata;
  nonexistent civil times (DST gaps) skip forward exactly as today via the
  `next_temporal_date` validation loop.

## Product decisions

### One stored grammar, two input syntaxes

The stored `recur` string gains a second shape alongside interval cookies:

| Shape | Examples | Meaning |
|---|---|---|
| Interval cookie (existing) | `.+1w`, `+2d`, `++1m` | Advance by a fixed span |
| Calendar schedule (new) | `w:mon,wed`, `2w:mon`, `m:15`, `m:last`, `m:2tue`, `y:07-04` | Advance to the next matching calendar date |

Users and agents never need to type the canonical form. `Recur.parse_interval`
(renamed responsibility: `Recur.parse`) accepts and normalizes:

1. **Natural phrases** — `every monday`, `every mon wed fri`, `weekdays`,
   `weekends`, `monthly on the 15th`, `1st of the month`, `last day of the
   month`, `2nd tuesday`, `last friday of the month`, `every 2 weeks on
   monday`, `every july 4`. Case-insensitive; filler words (`on`, `the`, `of`,
   `each`) ignored.
2. **Canonical grammar** — passthrough, validated.

Cron and RRULE input were considered and dropped (2026-07-28 review). Cron
cannot express core cases (every 2 weeks, 2nd Tuesday, from-completion
intervals), its dom+dow union rule is a footgun, and the natural-phrase form
covers the same ground more readably. RRULE is verbose for humans and only
earns its keep if calendar interop becomes a real requirement. The explain
surface (below) makes the supported grammar cheap for agents to discover;
revisit either syntax only under concrete demand.

### Canonical calendar grammar

```
calendar   := prefix? body
prefix     := "+"                      # one-hop; default (absent) = catch-up
body       := weekly | monthly | yearly
weekly     := [N] "w:" dayset          # N ≥ 2 = every Nth week, anchored
dayset     := day ("," day)*           # mon,tue,wed,thu,fri,sat,sun
monthly    := [N] "m:" mspec ("," mspec)*
mspec      := 1..31 | "last" | ordday
ordday     := ("1".."5" | "last") day  # 2tue, last fri → "lastfri"
yearly     := [N] "y:" MM "-" DD | [N] "y:" MM ":" ordday
```

Normalization lowercases, sorts day sets into week order, and collapses
synonyms (`weekdays` → `w:mon,tue,wed,thu,fri`). `parse` output is the single
canonical spelling; two inputs meaning the same schedule store identically.

### Advance semantics for calendar schedules

Interval cookies keep their three prefixes untouched. Calendar schedules need
only two, because "from completion" and "catch-up" coincide when occurrences
are calendar-fixed — the next Monday after completion *is* the next future
Monday:

| Cookie | On completion, the stamp becomes |
|---|---|
| `w:mon` (default, catch-up) | the next matching date strictly after today |
| `+w:mon` (one-hop) | the next matching date strictly after the stored date — may stay in the past, for cadences where each missed occurrence must be processed (invoicing, log reviews) |

`.+` on a calendar schedule is rejected with a message explaining the default
already means that. Bare calendar input (no prefix) normalizes to the
prefixless catch-up form — a different default than bare intervals (`.+`),
but each matches its shape's natural expectation; the spec documents both.

`Nw` parity ("every 2 weeks on Monday") anchors on the ISO week of the stamp
at the moment the cookie is set, and stays anchored: rolls preserve parity by
stepping in whole weeks from the stored date.

### Edge-date rules

- **Numeric day that a month lacks** (`m:31` in April) — clamps to the last
  day of that month, consistent with the existing `Date#>>` clamp for `+1m`.
  `m:31` is therefore a synonym for `m:last` in short months, by design.
- **Ordinal weekday that a month lacks** (`m:5fri`) — skips to the next month
  that has one. A 5th Friday genuinely doesn't exist; clamping would silently
  mean "last Friday", which has its own spelling.
- **Feb 29 yearly** (`y:02-29`) — clamps to Feb 28 in non-leap years (same
  clamp rule, and matches how people treat leap-day birthdays).
- **DST gaps** — unchanged: a candidate whose local time doesn't exist skips
  to the next occurrence via the existing `next_temporal_date` loop.
- **Both dates present** — unchanged: the deadline carries the schedule;
  scheduled shifts by the same day-offset so lead time is preserved.

### Explain: the discoverability contract

Every surface can answer "what does this schedule mean and when does it fire
next" **without mutating anything**. One core function powers all of it:

```
Recur.explain(input, context:, count: 5)
→ { canonical:, human:, next: [dates] } | error with reason
```

`human` is a one-line rendering ("every Mon, Wed, Fri", "monthly on the 2nd
Tuesday", "every 2 weeks from completion"). `next` projects occurrences from
today (or from a supplied stamp). This is the agent-facing contract: an agent
proposes a schedule, explains it, verifies the projected dates, then commits.

## Surfaces

### CLI (`bin/tasks`)

- `recur <ref> <schedule>` — accepts both input syntaxes. `--from
  schedule|completion` keeps its meaning for bare intervals; it is rejected
  with a pointer to `+`-prefix semantics when combined with a calendar form.
  `--on <date>` unchanged. Success output shows the humanized schedule and
  the (possibly re-anchored) next date.
- `recur <ref>` with no schedule — read-only: prints the task's current
  schedule humanized plus its next `--count N` (default 5) occurrences.
  `--json` for the structured form.
- `recur --explain "<schedule>"` — taskless parse/preview: canonical form,
  human text, next N dates, or the rejection reason. `--json` emits the
  `Recur.explain` payload verbatim. Deterministic, non-interactive.
- `capture --recur` / `add --recur` — same parser, all forms accepted.
- `list --recurring` — row output gains the humanized schedule; JSON keeps
  `recur` (canonical) and adds `recur_human`.
- `done` output for a rolled task is unchanged (`↻ <title> → next <date>`).

### TUI (`lib/tui`)

- The `r` popup accepts both input forms. As the user types, a footer line live-
  previews `Recur.explain`: the human rendering plus the next 2–3 dates, or
  the parse error. Committing behaves exactly as today (same `patch_task`
  path).
- The task detail panel shows the humanized schedule next to the `↻` badge
  instead of the raw cookie.
- The shortcut hint text updates: `recur — weekly · every mon · m:15 · off`.
- No new keybindings; no behavior change to completion-follow or undo.

### API (`lib/tasks/api`)

- `recurrence` on create/patch accepts both input forms via the shared
  parser; stored and echoed canonical. Validation errors carry the parser's
  reason string.
- Representation adds `recurrence_human` alongside `recurrence` on single-task
  and list payloads (cheap string render, no occurrence math in lists — the
  stamp already is the next occurrence).
- New endpoint `GET /recurrence/explain?input=…&count=…` returning the
  `Recur.explain` payload — the API twin of `recur --explain`. No auth or
  store access beyond read-only clock/timezone context.
- `recurring` filter unchanged. `docs/api/openapi.yaml` updated for all of the
  above.

### Validation and repair

- `check` validates stored cookies against the full grammar and reports (not
  repairs) anything unparsable; unparsable cookies still degrade to
  non-recurring on read, exactly as today.
- `patch_recurrence`, `normalize_create_recurrence`, and the API normalizer
  all route through the one parser — no surface-specific grammar drift.

## Implementation map

| Piece | Where | Nature of change |
|---|---|---|
| Schedule model + parser + humanizer + explain | `lib/tasks/recur.rb` (grow into `Recur::Schedule` if it wants a class; module API stays `Recur.parse` / `Recur.cookie?` / `Recur.next_temporal_date` / `Recur.explain` / `Recur.humanize`) | Core; everything else consumes this |
| Roll-forward | `lib/tasks/store.rb` `advance_recurrence_records` | Minimal — already delegates date math to Recur; calendar cookies just flow through `next_temporal_date` |
| Write validation | `store.rb` (`patch_recurrence`, `normalize_create_recurrence`), `check.rb` | Swap `cookie?` for full-grammar validation |
| CLI | `bin/tasks` | Extend `recur`, add `--explain` / read-only mode, list output |
| TUI | `lib/tui/app.rb` (`open_recur_popup`), `task_details.rb`, `shortcuts.rb` | Live preview footer, humanized detail line |
| API | `api/app.rb`, `api/representation.rb`, `docs/api/openapi.yaml` | Normalizer passthrough, `recurrence_human`, explain endpoint |
| Docs | `docs/cli-spec.md`, `.claude/skills/tasks-cli`, this plan → `implemented/` | Spec the grammar and semantics |

## Phasing

Sized to be staged through delegated implementation agents with adversarial
review between phases; each phase lands green on main.

1. **Core engine** — grammar, parser (both input syntaxes), normalizer,
   humanizer, `next_temporal_date` support for calendar schedules, `explain`.
   Pure library + exhaustive unit tests; no surface changes. Table-driven
   tests for every grammar production, rejection message, and edge-date rule;
   DST-gap and timezone cases mirroring the existing `test_recur.rb`
   patterns.
2. **Store + validation** — write paths accept calendar cookies; roll-forward
   covered by store-level completion tests (catch-up vs one-hop, parity
   anchoring, both-dates offset, cascade/retire rules unchanged).
3. **CLI** — command surface + `--explain`; cli-spec.md updated in the same
   change (spec and behavior move together).
4. **API** — normalizer, representation, explain endpoint, openapi.yaml,
   request tests.
5. **TUI** — popup live preview + detail rendering; view/shortcut tests.
6. **Docs sweep** — tasks-cli skill, README/AGENTS touchpoints, move this
   plan to `implemented/` rewritten as system description (house convention).

## Resolved decisions (2026-07-28 review)

- **Cron input: dropped.** Natural phrases plus the canonical grammar cover
  the same ground with less surface; this also moots the question of cron
  time fields seeding the stamp's local time.
- **`m:31` clamps** to the last day of short months (RRULE-style skipping
  rejected): matches the existing `Date#>>` clamp behavior and what a person
  typing `31` means.
- **`recur <ref>` with no schedule stays** as the read-only preview —
  symmetric with `show`, and the cheapest way to ask "when does this fire
  next" from the CLI.
