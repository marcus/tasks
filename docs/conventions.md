# Conventions

This is a plain-text task system organized around **GTD** (Getting Things Done)
and **Covey's** Important/Urgent matrix. The data is a JSONL file that diffs one
task per line — greppable, git-committable, and parseable by the Ruby tooling in
`bin/`. The CLI (`bin/tasks`) and the TUI (`bin/tasks-tui`) are the writers; the
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
| `INBOX`     | Captured, not yet processed. Decide what it is and where it goes.    |
|             | Giving an item a date counts as processing: a `scheduled`/`deadline` on an `INBOX` item promotes it to `TODO` (the tooling does this automatically). |
| `TODO`      | Actionable, categorized, but not the immediate next physical action. |
| `NEXT`      | The next concrete physical action you can actually do right now.     |
| `WAITING`   | Delegated or blocked on someone/something else.                      |
| `DONE`      | Complete.                                                            |
| `CANCELLED` | Dropped, no longer relevant.                                         |

`INBOX`/`TODO`/`NEXT`/`WAITING` are the open states; `DONE`/`CANCELLED` are the
closed states (they carry a `closed` date).

## Record reference

One record per line. Records serialize with a fixed key order (nil/empty fields
are omitted), so a single field change is a one-line diff:

```
type id parent state priority title tags scheduled deadline recur closed archived body
```

```json
{"type":"meta","version":1}
{"type":"section","id":"a1b2c3d4","title":"Inbox"}
{"type":"task","id":"0f9e8d7c","parent":"a1b2c3d4","state":"INBOX","title":"Random thought","body":"Captured [2026-07-01]."}
{"type":"section","id":"b2c3d4e5","title":"Projects"}
{"type":"section","id":"c3d4e5f6","parent":"b2c3d4e5","title":"Launch the personal site","body":"Goal: site up by end of month."}
{"type":"task","id":"d4e5f6a7","parent":"c3d4e5f6","state":"NEXT","priority":"A","title":"Pick a static-site generator","tags":["@computer","important"],"deadline":"2026-07-20"}
{"type":"task","id":"e5f6a7b8","parent":"a1b2c3d4","state":"NEXT","title":"Water the plants","tags":["@home"],"scheduled":"2026-07-08","recur":".+1w","body":"- Did [2026-07-01]."}
```

### Record types

- **`meta`** — always line 1: `{"type":"meta","version":1}`. `version` is the
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
- **`state`** — one of the six states above (tasks only).
- **`priority`** — `"A"` / `"B"` / `"C"`, optional. Ranks within a list.
- **`title`** — short; starts with a verb for actions.
- **`tags`** — a JSON array including `@contexts` (e.g.
  `["@computer","important"]`). See [Tags](#tags).
- **`scheduled`** / **`deadline`** / **`closed`** / **`archived`** —
  `"YYYY-MM-DD"` strings (no day-of-week, no `< >`). `scheduled` is the single
  available-from/start/defer-until date: an open task is unavailable before it
  and available on it. `deadline` is the separate due date; `closed` is stamped
  when a task enters DONE/CANCELLED; `archived` is stamped on a subtree root
  when it's swept to `archive.jsonl`.
- **`recur`** — an org-style repeater cookie (`.+1w`, `++1m`, `+2d`) on a dated
  task. See [Recurrence](#recurrence).
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

## Dates

- `scheduled` — the first day the task is available to *start* / work on.
- `deadline` — the separate day it's *due*.
- All dates are ISO `"YYYY-MM-DD"` strings. The CLI/TUI accept fuzzy input
  (`fri`, `+3`, `07-15`, `tomorrow`) and write the canonical form.

A future `scheduled` date removes the task from active views until that date;
it does not make the task urgent. The semantic `defer` tag means **On Hold
indefinitely**, not a dated deferral. Effective availability is ancestor-aware:
an On Hold or future-scheduled parent also hides its descendants. Use
`defer <ref> <date>` for a timed release, `someday <ref>` for an indefinite
hold, and `activate <ref>` to make the task available now. None of those moves a
`deadline`.

## Recurrence

A task *recurs* when it carries a `recur` cookie alongside a `scheduled`/`deadline`
date: `.+1w`, `++1m`, `+2d`. The prefix sets what the interval is measured from on
completion — `+` fixed, `++` catch-up, `.+` from-completion. Completing a recurring
task rolls its date forward and **leaves it open** (no `closed`), appending a
`- Did [date].` line to the body. See `docs/cli-spec.md` for the full grammar.

## Links

Body notes routinely reference other systems — a Slack thread, a Jira ticket,
a PR, a doc. Three forms, all recognized by the tooling (`tasks links`,
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
