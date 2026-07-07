# Conventions

This is a plain-text task system inspired by Emacs **org-mode**, organized around
**GTD** (Getting Things Done) and **Covey's** Important/Urgent matrix. It's just
text — readable and editable in any editor, greppable, and parseable by the Ruby
tooling in `bin/`.

## The file

Everything lives in `gtd.org`. Top-level headings (`*`) are GTD lists. You capture
into `* Inbox`, then process items out into the other lists.

## TODO states

Declared at the top of `gtd.org`:

```
#+TODO: INBOX(i) TODO(t) NEXT(n) WAITING(w) | DONE(d) CANCELLED(c)
```

| State       | Meaning                                                              |
|-------------|---------------------------------------------------------------------|
| `INBOX`     | Captured, not yet processed. Decide what it is and where it goes.    |
|             | Giving an item a date counts as processing: a `SCHEDULED`/`DEADLINE` on an `INBOX` item promotes it to `TODO` (the tooling does this automatically). |
| `TODO`      | Actionable, categorized, but not the immediate next physical action. |
| `NEXT`      | The next concrete physical action you can actually do right now.     |
| `WAITING`   | Delegated or blocked on someone/something else.                      |
| `DONE`      | Complete.                                                            |
| `CANCELLED` | Dropped, no longer relevant.                                         |

The `|` separates "in progress" states from "done" states.

## Headline anatomy

```
** NEXT [#A] Purchase ECR flight in Concur   :@computer:urgent:important:
   DEADLINE: <2026-07-15>
   :PROPERTIES:
   :ID: 7f3a9c2e
   :END:
   Denver. Fly in Jul 20, ECR Jul 21-22, leave Jul 23.
```

- **State keyword** — one of the states above.
- **Priority** `[#A]` / `[#B]` / `[#C]` — optional, ranks within a list.
- **Title** — short, starts with a verb for actions.
- **Tags** — `:tag:tag:` at end of line (see below).
- **Timestamps** — `DEADLINE:` / `SCHEDULED:` on the line below.
- **ID** — a stable handle in a `:PROPERTIES:` drawer (`:ID:`), sitting right
  after the timestamps (org's required order: planning lines, then the drawer).
  New captures get one automatically; existing tasks earn one when first touched
  or via `tasks id`. It's how a ref survives a retitle or a line reflow — the
  tooling locates a task by id before falling back to line + title. Leave it be.
- **Body** — indented free text for notes, links, context.

## Tags

### Contexts (GTD) — where/how you can do it
`:@computer:` `:@email:` `:@calls:` `:@office:` `:@home:` `:@errands:`
`:@online:` `:@team:` `:@waiting:`

Contexts answer "what can I actually do given where I am and what's in front of me?"

### Covey matrix — importance × urgency

The tooling computes each task's quadrant from two axes:

- **important** — priority `[#A]` or `[#B]`, **or** the `:important:` tag.
- **urgent** — a `DEADLINE` within the next few days (default 3; overdue counts),
  **or** the `:urgent:` tag. A `SCHEDULED` start date alone is *not* urgent.

|                    | urgent            | not urgent           |
|--------------------|-------------------|----------------------|
| **important**      | **Q1** — do now   | **Q2** — schedule/invest (the sweet spot) |
| **not important**  | **Q3** — delegate/minimize | **Q4** — eliminate |

So raising a task to `[#A]`/`[#B]` and giving it a near deadline moves it toward Q1
with no extra tagging. The `:important:`/`:urgent:` tags remain as explicit overrides
for what the derivation misses — e.g. `:urgent:` on something with no near deadline,
or `:important:` on a task you deliberately keep at low priority.

The urgency window is configurable: `urgent_days = N` in `~/.config/tasks/config`, or
the `TASKS_URGENT_DAYS` env var.

## Timestamps

- `SCHEDULED: <YYYY-MM-DD>` — the day you intend to *start* / work on it.
- `DEADLINE:  <YYYY-MM-DD>` — the day it's *due*.
- Dates are ISO. `<...>` is org's active-timestamp syntax (shows up in agenda).

## Links

Body notes routinely reference other systems — a Slack thread, a Jira ticket,
a PR, a doc. Write them as org links when a label helps, or bare URLs when it
doesn't; the tooling recognizes both and knows which system each points into
(`tasks links`, `show`):

```
   Context in [[https://acme.slack.com/archives/C042/p1719][the incident thread]].
   Ticket: https://acme.atlassian.net/browse/OPS-1234
```

Prefer the link over a prose description of where something lives — links are
listable, openable, and survive rewording.

## Projects

Anything requiring more than one action is a **project**. Model it as a heading with
sub-action children:

```
* Projects
** Promotion recommendation
   Goal: line up a Sr. Director to recommend me for promotion.
*** NEXT Reach out to Derrick to feel out a recommendation  :@calls:important:
```

GTD rule of thumb: every active project should have at least one `NEXT` action, or
it's stalled.

## Weekly review

The GTD habit that keeps this trustworthy: once a week, empty the inbox, mark done
items `DONE`, make sure every project has a `NEXT`, and scan `WAITING` / `Someday`.
