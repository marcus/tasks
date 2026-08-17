# Conventions

This is a plain-text task system organized around **GTD** (Getting Things Done)
and **Covey's** Important/Urgent matrix. The data is a JSONL file that diffs one
task per line — greppable, git-committable, and parsed by the Go core in
`internal/`. The CLI (`tasks`), TUI (`tasks-tui`), and API (`tasks-api`) are thin
adapters over the same checked writer; the
file is **not meant for hand-editing** (see [The file](#the-file)).

## The file

Everything lives in `tasks.jsonl` (completed history sweeps into `archive.jsonl`).
Each line is one `JSON.generate`'d record — a `meta` header, a `section` (a GTD
list or a project heading), or a `task`. The tree is carried by `parent` pointers,
so there's no indentation to keep balanced and no block boundaries to infer.

Because ids, canonical key order, the `meta` line, and DFS pre-order file order
are all invariants the tooling relies on, **hand-editing is error-prone** — reach
for the CLI/TUI, which write the exact shape and validate every change. If
something ever edits the file out-of-band, `tasks check` reports any structural
breakage (see [`docs/cli-spec.md`](cli-spec.md)).

## TODO states

| State       | Meaning                                                              |
|-------------|---------------------------------------------------------------------|
| `PROPOSED`  | Inert suggestion pending explicit owner approval or rejection.       |
| `INBOX`     | Captured, not yet processed. Decide what it is and where it goes.    |
|             | Giving an item a date counts as processing: a `scheduled`/`deadline` on an `INBOX` item promotes it to `TODO` (the tooling does this automatically). |
| `TODO`      | Actionable, categorized, but not the immediate next physical action. |
| `NEXT`      | The next concrete physical action you can actually do right now.     |
| `WAITING`   | Delegated or blocked on someone/something else.                      |
| `DONE`      | Complete.                                                            |
| `CANCELLED` | Dropped, no longer relevant.                                         |

`PROPOSED` is its own lifecycle category. It is neither accepted open work nor
closed history. `INBOX`/`TODO`/`NEXT`/`WAITING` are the open states;
`DONE`/`CANCELLED` are the closed states (they carry a `closed` date).

## Record reference

One record per line. Records serialize with a fixed key order (nil/empty fields
are omitted), so a single field change is a one-line diff:

```
type id parent state priority title tags scheduled scheduled_time deadline deadline_time recur lead lead_skip delegation closed rejected archived body
```

```json
{"type":"meta","version":2}
{"type":"section","id":"a1b2c3d4","title":"Inbox"}
{"type":"task","id":"0f9e8d7c","parent":"a1b2c3d4","state":"INBOX","title":"Random thought","body":"Captured [2026-07-01]."}
{"type":"section","id":"b2c3d4e5","title":"Projects"}
{"type":"section","id":"c3d4e5f6","parent":"b2c3d4e5","title":"Launch the personal site","body":"Goal: site up by end of month."}
{"type":"task","id":"d4e5f6a7","parent":"c3d4e5f6","state":"NEXT","priority":"A","title":"Pick a static-site generator","tags":["@computer","important"],"deadline":"2026-07-20"}
{"type":"task","id":"e5f6a7b8","parent":"a1b2c3d4","state":"NEXT","title":"Water the plants","tags":["@home"],"scheduled":"2026-07-08","recur":".+1w","body":"- Did [2026-07-01]."}
{"type":"task","id":"f6a7b8c9","parent":"c3d4e5f6","state":"NEXT","title":"Join customer call","deadline":"2026-07-20","deadline_time":{"local":"17:00","timezone":"Europe/London"}}
```

### Record types

- **`meta`** — always line 1: `{"type":"meta","version":2}`. `version` is the
  on-disk schema version.
- **`section`** — a GTD list (`Inbox`, `Projects`, `Someday / Maybe`, …) or a
  project heading nested under one. Carries `title`, an optional `body`, and its
  `parent`. Sections never carry task fields (state, dates, priority, tags).
- **`task`** — an actionable item. Fields below.

### Fields

- **`id`** — a stable 8-hex handle (`[0-9a-f]{8}`) on **every** record. It's how
  a ref survives a retitle or a line reflow: the tooling locates a task by id
  before falling back to line + title. Preserved across edits; never reused.
- **`parent`** — the id of the containing section or task. Absent on top-level
  sections. Children are ordinary records that name their parent, so a project
  is a section (or task) with child records pointing at it.
- **`state`** — one of the seven states above (tasks only).
- **`priority`** — `"A"` / `"B"` / `"C"`, optional. Ranks within a list.
- **`title`** — short; starts with a verb for actions.
- **`tags`** — a JSON array including `@contexts` (e.g.
  `["@computer","important"]`). See [Tags](#tags).
- **`scheduled`** / **`deadline`** / **`closed`** / **`archived`** —
  `"YYYY-MM-DD"` strings (no day-of-week, no `< >`). `scheduled` is the single
  available-from/start/defer-until date. `deadline` is the separate due date;
  `closed` is stamped
  when a task enters DONE/CANCELLED; `archived` is stamped on a subtree root
  when it's swept to `archive.jsonl`.
- **`rejected`** — `"YYYY-MM-DD"`, the day a `PROPOSED` task was declined by
  `tasks reject`. It is what separates a declined proposal from an ordinary
  cancellation, which `CANCELLED` alone cannot express, and it is only ever
  valid on a `CANCELLED` task: any write that leaves that state drops it. The
  marker is what `tasks list --rejected` selects on and what `tasks unreject`
  requires; see `docs/cli-spec.md`.
- **`scheduled_time`** / **`deadline_time`** — optional time metadata owned by
  the matching date: `{ "local": "HH:MM", "timezone": "Area/Location", "fold": 1 }`.
  Omitting `timezone` makes the time floating in the configured evaluation zone.
  A fixed value stores a full IANA zone. `fold: 1` selects the later instant
  during a daylight-saving overlap and is omitted otherwise. These objects
  never appear without their matching date.
- **`recur`** — a recurrence schedule on a dated task, in one canonical
  spelling: an org-style interval cookie (`.+1w`, `++1m`, `+2d`) or a calendar
  schedule (`w:mon,wed`, `m:15`, `y:07-04`). See [Recurrence](#recurrence).
- **`lead`** — an optional lead-time window on a dated task: a positive count
  and a unit — calendar (`3w`, `2d`, `1m`, `10y`) or the clock unit `h` (`5h`). The task is hidden until that
  span before its anchor. See [Lead time](#lead-time).
- **`lead_skip`** — internal bookkeeping: the anchor date whose occurrence
  `activate` already released early. Written only by activation, retired by any
  anchor edit or recurrence roll, and never typed by a user or exposed on the
  HTTP wire.
- **`delegation`** — optional object naming who holds the next action; absent
  means not delegated. See [Delegation](#delegation).
- **`body`** — free-text notes as a single `\n`-joined string; omitted when
  empty. Notes, links, and context live here.

### Hierarchy and order

The tree lives in the `parent` pointers, and **file order is DFS pre-order** — a
record's whole subtree is the contiguous run of lines beneath it, and sibling
order is line order. The linter (`tasks check`) enforces this, so the store never
has to infer structure by scanning.

## Tags

### Contexts (GTD) — where/how you can do it
`@computer` `@email` `@calls` `@office` `@home` `@errands`
`@online` `@team` `@waiting`

Contexts (the `@`-prefixed tags) answer "what can I actually do given where I am
and what's in front of me?"

### Covey matrix — importance × urgency

The tooling computes each task's quadrant from two axes:

- **important** — priority `A` or `B`, **or** the `important` tag.
- **urgent** — a `deadline` within the next few days (default 3; overdue counts),
  **or** the `urgent` tag. A `scheduled` start date alone is *not* urgent.

|                    | urgent            | not urgent           |
|--------------------|-------------------|----------------------|
| **important**      | **Q1** — do now   | **Q2** — schedule/invest (the sweet spot) |
| **not important**  | **Q3** — delegate/minimize | **Q4** — eliminate |

So raising a task to `A`/`B` and giving it a near deadline moves it toward Q1
with no extra tagging. The `important`/`urgent` tags remain as explicit overrides
for what the derivation misses — e.g. `urgent` on something with no near deadline,
or `important` on a task you deliberately keep at low priority.

The urgency window is configurable: `urgent_days = N` in `~/.config/tasks/config`, or
the `TASKS_URGENT_DAYS` env var.

## Dates and times

- `scheduled` — when the task becomes available to *start* / work on.
- `deadline` — the separate value for when it's due.
- All dates are ISO `"YYYY-MM-DD"` strings. The CLI/TUI accept fuzzy input
  (`fri`, `+3`, `07-15`, `tomorrow`, `aug 1`, `next month`, `next year`, `in 2
  weeks`; see `docs/cli-spec.md` for the full grammar and the `date_order`
  config for month/day-first preference) and write the canonical form.

Without a time, `scheduled` releases at the start of its local calendar date
and `deadline` remains on time through its whole date. A time without a zone is
floating. A time with `--timezone Europe/London` is fixed to that IANA zone and
keeps the same instant when the display zone changes. Nonexistent local times
in daylight-saving gaps are rejected; a fold defaults to the earlier instant
unless `--fold later` is selected. Precision is one minute.

A future `scheduled` value removes the task from active views until its exact boundary;
it does not make the task urgent. The semantic `defer` tag means **On Hold
indefinitely**, not a dated deferral. Effective availability is ancestor-aware:
an On Hold or future-scheduled parent also hides its descendants. Use
`defer <ref> <date-or-date-time>` for a timed release, `someday <ref>` for an indefinite
hold, and `activate <ref>` to make the task available now. None of those moves a
`deadline`.

Timed deadlines affect overdue state and ordering. They do not create reminders
or notifications.

## Recurrence

A task *recurs* when it carries a `recur` schedule alongside a
`scheduled`/`deadline` date. The stamp on the task **is** its next occurrence;
the schedule only says how that stamp advances on completion, so nothing is
materialized ahead of time. One scalar field holds two stored shapes:

- **Interval cookies** — `.+1w`, `++1m`, `+2d`. The prefix sets what the interval
  is measured from on completion: `+` fixed (one hop from the stored date), `++`
  catch-up (repeated until strictly future), `.+` from-completion.
- **Calendar schedules** — `w:mon,wed`, `2w:mon`, `m:15`, `m:last`, `m:2tue`,
  `y:07-04`, `y:11:3thu`. These advance to the next matching calendar date and
  take only two prefixes: bare (the default) means the next match after today,
  `+` means the next match after the stored date.

Values are stored canonical, so two inputs meaning the same schedule store
identically; writes accept friendly intervals and natural calendar phrases too.
Completing a recurring task rolls its date forward and **leaves it open** (no
`closed`), appending a `- Did [date].` line to the body. See `docs/cli-spec.md`
for the full grammar, input forms, and edge-date rules.

## Lead time

A dated task can carry a `lead` span saying how long before its occurrence date
it should become visible. The **anchor** is the task's `deadline` if it has one,
otherwise its `scheduled` date — the same precedence a recurrence roll uses — and
the window opens at midnight of `anchor - lead` in the anchor's own effective
zone (the reader's, when the anchor carries none). A clock span (`5h`)
instead measures a real duration back from the anchor's own instant, so it opens
partway through a day; `m` always means months, never minutes.

A lead **replaces** the task's own available-from gate rather than joining it, so
a lead never sits beside a two-date window; that shape is refused at write time,
from either direction. Clearing the last date clears the lead with it, exactly as
it already retires a `recur` cookie — a lead is an intent about a date and cannot
outlive the last one. There is no state rule: a lead rides with the dates, so a
proposal takes one on the same terms it takes a deadline. (The `deadline` + `scheduled` pair, whose
offset a recurrence roll preserves, remains the way to express a runway that must
move with the occurrence *and* stay separately visible.)

Nothing else about availability changes: a lead-gated task is timed-unavailable
like any deferred task, reports `availability_reason: "scheduled"`, and an own or
inherited indefinite hold still outranks it. An ancestor's lead gates its whole
subtree. `activate` releases exactly one occurrence by stamping `lead_skip` with
the current anchor; the next roll or anchor edit retires the stamp and the window
comes back. For a task with no lead, including a recurring one, `activate` keeps
its long-standing meaning of clearing a future available-from date. See `docs/cli-spec.md` for the input forms and the refusal rules.

## Delegation

A task you have handed off carries one optional `delegation` object naming who
holds the next action. Absent means not delegated; there is no neutral value.

```json
{"type":"task","id":"e5f6a7b8","state":"WAITING","title":"Renew office lease","delegation":{"kind":"human","status":"delegated","assignee":"pat@example.com","at":"2026-07-27T18:04:11Z"}}
{"type":"task","id":"f6a7b8c9","state":"TODO","title":"Compare CRDT libraries","delegation":{"kind":"agent","mode":"research","status":"claimed","assignee":"claude-code/claude-fable-5/313cf82e","at":"2026-07-27T18:04:11Z","work_ref":"https://example.com/brief"}}
```

- **`kind`** — `human` (a person, identified by an email address) or `agent`
  (the agent pool).
- **`mode`** — agent authority: `refine` (improve the task), `research`
  (investigate and recommend), `implement` (do the work). Required for an
  agent, forbidden for a human. Only the owner sets or widens it.
- **`status`** — `delegated` (human), `ready` (agent, unclaimed), `claimed`
  (agent, held by one worker).
- **`assignee`** — the person's email (address-shaped: non-empty local part,
  one `@`, dotted domain), or the holding worker id
  (`<harness>/<model>/<session-id>`) while claimed. Both are bounded at 200
  characters and reject Unicode whitespace and control/escape characters.
- **`at`** — UTC timestamp of the last status transition.
- **`work_ref`** — optional single reference to where the work lives: a ticket,
  PR, research brief, or agent session; one line, at most 500 characters. More
  detail belongs in the body.

Nested keys this binary does not know are round-tripped rather than dropped and
reported by `check` as a warning, so a record written by a newer binary
survives a claim or release here.

`tasks delegate <ref> --to <email>` sets WAITING, because that is exactly what
WAITING means. Agent delegation and claims never change state, with one
exception: replacing a *person* with the agent pool on a task that is WAITING
*because* of that person returns it to TODO. `claim` is atomic, so exactly one
worker ever holds a task. Completing or cancelling a task clears an unclaimed
marker (nothing happened yet) and keeps a claimed or human one verbatim — that
is how "who did it, and where" survives into the archive. Completing a
*recurring* task instead rolls the standing intent forward onto the next
occurrence — mode or person retained, always unclaimed, fresh `at`, no
`work_ref`.

Across devices the object is merged atomically under one total order: a removal
(`undelegate`) absorbs everything, a `claimed` marker outranks a non-claimed
one, two claims resolve to the earlier `at`, and two non-claims to the later
`at`. See `docs/cli-spec.md` for the full rule and its consequences.

## Links

Tasks may store an ordered `links` array of formal `{url, label?}` objects.
Use `tasks link add <ref> <url> [--label TEXT]` and `tasks link rm <ref> <n|url>`
to edit it; the index is within the formal list only. Empty formal lists omit
the field, URLs must be HTTP(S), duplicates are refused, and tasks may carry at
most 50. Sections never carry links.

Body notes routinely reference other systems — a Slack thread, a Jira ticket,
a PR, a doc. Three derived forms, all recognized by the tooling (`tasks links`,
`show`, `open`, the TUI's `o`):

```
Context in [[https://acme.slack.com/archives/C042/p1719][the incident thread]].
Ticket: https://acme.atlassian.net/browse/OPS-1234
Or, with link.jira configured: jira:OPS-1234
```

- **Org-style links** `[[url][label]]` when a label helps.
- **Bare URLs** when it doesn't.
- **Shorthands** (`jira:OPS-1234`, `gh:acme/app/pull/412`) for systems you
  reference constantly — configure `link.<name> = <url template with %s>` in
  `~/.config/tasks/config` and descriptions stay terse; the tooling expands
  them everywhere.

Prefer a link over a prose description of where something lives — links are
listable, openable (`tasks open <ref>`, `o` in the TUI), and survive rewording.
The openable set is formal links first, then title and body links, deduplicated
by URL; a later body label upgrades an unlabelled formal row without moving it.

## Projects

Anything requiring more than one action is a **project**. Model it as a `section`
with sub-action children (child records naming the project as their `parent`):

```json
{"type":"section","id":"c3d4e5f6","parent":"b2c3d4e5","title":"Promotion recommendation","body":"Goal: line up a Sr. Director to recommend me for promotion."}
{"type":"task","id":"d4e5f6a7","parent":"c3d4e5f6","state":"NEXT","title":"Reach out to Derrick to feel out a recommendation","tags":["@calls","important"]}
```

GTD rule of thumb: every active project should have at least one `NEXT` action, or
it's stalled.

## Weekly review

The GTD habit that keeps this trustworthy: once a week, empty the inbox, mark done
items `DONE`, make sure every project has a `NEXT`, and scan `WAITING` / `Someday`.
