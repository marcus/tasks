# Features

If it is on this page, you can invoke it. Nothing here is a roadmap item,
a slogan, or a thing the TUI does that the CLI forgot about.

The same list lives behind `tasks help`, `tasks help --json`, the TUI `?`
modal, and [`docs/api/openapi.yaml`](docs/api/openapi.yaml). This file is
the human-readable tour of that contract.

## Surfaces

- A CLI (`tasks`) that is the complete product, not a thin wrapper around a
  GUI. Every owned capability has a non-interactive path.
- A Bubble Tea TUI (`tasks-tui`) that is a client of the same application
  layer. It has no private writer and no secret store.
- A loopback HTTP API (`tasks-api`) on `127.0.0.1:4747` with the same
  outcomes, OpenAPI v1, ETags, and `If-Match`. Fuzzy title refs stay
  CLI-only; the wire speaks stable eight-hex ids.
- An embeddable TUI package (`pkg/tui`) so a host such as Sidecar can
  drop the same app into another frame. Hosts get command metadata, key
  routing, theme projection, and chrome they can suppress. They do not
  get to touch `tasks.jsonl`.
- Natural-language list management through `tasks -p "…"` and the TUI
  agent prompt. Both assemble the same embedded list-agent contract.
- Structured JSON on almost every command. `tasks help --json` is the
  registry: every spelling, every alias, and whether that command has a
  `--json` answer.
- Human output for humans. Colour when stdout is a TTY; identity
  functions when it is a pipe.
- Short aliases (`a`, `n`, `q`, `i`, `l`, `c`, `d`, `pj`, …) and the
  synonyms an agent actually types (`add`, `complete`, `close`,
  `deadline`, `snooze`, `repeat`).

## GTD vocabulary

- Seven states, and they mean what they say: `PROPOSED`, `INBOX`,
  `TODO`, `NEXT`, `WAITING`, `DONE`, `CANCELLED`.
- Dating an inbox item promotes it to `TODO`. Capture is not the same
  as processed.
- Contexts (`@computer`, `@calls`, `@home`, …) answer “where can I do
  this?”, not “what vibe is this?”
- Priorities `A` / `B` / `C` / none.
- Next actions grouped by context, because `@errands` is useless at a
  desk.
- Waiting For as a state, not a hope.
- Someday / Maybe as an indefinite hold (`tasks someday`) that does not
  move the deadline.
- Projects as rolled-up sections with open counts, NEXT counts, the
  soonest date, and a stuck flag when nothing is NEXT.
- Reserved GTD lists (Next Actions, Waiting For, Someday / Maybe) stay
  addressable and still render in the outline. They are not listed as
  areas, because membership is already derivable from the task.
- Covey quadrants computed on the way out, never stored. Important is
  `A`/`B` or the `important` tag. Urgent is a near deadline or the
  `urgent` tag. Empty Q1 still prints, which is the reassuring answer.

## Capture and filing

- `tasks capture "text"` with `--due`, `--scheduled`, `--priority`,
  `--tag`, `--context`, `--state`, `--project`, `--under`, `--recur`,
  `--lead`, `--note`.
- `--under <ref>` nests a new task under an existing one, capped at
  `max_depth`.
- `--no-host-context` skips the machine’s automatic `@home` / `@work`
  context for one capture.
- `tasks propose "text"` for inert follow-up the owner has not asked
  for. Same filing flags minus state and recurrence.
- Repeatable `--note` for rationale, evidence, or “why I cancelled this.”
- Title substring refs, exact 8-hex ids, and `L<line>` file-line refs.
  Ambiguous titles exit 2 and list the candidates instead of guessing.
- `tasks move` to another section, `--under` another task, `--top` back
  to the section, or `--before` a sibling.
- `tasks retitle`, `tasks tag +foo -bar @ctx -@old`, `tasks note`,
  `tasks priority A|B|C|none`.
- `tasks state` for any transition, including repairs on closed live
  tasks.
- Completing a parent cascades to open descendants as one undo.
  Completing a recurring task rolls the date and does not cascade.
- `tasks cancel` / `tasks drop` with an optional reason.
- `tasks delete` for genuine mistakes. Hard delete, undoable, cascade
  confirm when there are children. Prefer cancel/archive for ordinary
  life.

## Time, in more flavors than a todo list usually admits

- `scheduled` is available-from / start / defer-until.
- `deadline` is due. They are different fields. They stay different.
- Friendly input: `today`, `tomorrow`, `fri`, `+3`, `next friday`,
  `aug 1`, `in 2 weeks`, `today 3pm`.
- All-day values, floating wall times, and fixed IANA zones
  (`--timezone Europe/London`).
- DST fold choice (`--fold later`) for the second 1:30am.
- Minute precision. No reminders. A time changes availability and
  overdue state; it does not invent a notification daemon.
- `date_order = mdy|dmy` for ambiguous numeric dates.
- `tasks due`, `tasks schedule`, `tasks undate --kind deadline|scheduled`.
- `tasks defer <ref> friday` writes a one-shot available-from and
  never touches the deadline.
- `tasks someday` for “not now, no date.”
- `tasks activate` to make it available now. On a lead or recurring
  task it releases only this occurrence.
- Ancestor-aware availability: an On Hold or future-scheduled parent
  hides its descendants too.
- `tasks list --unavailable` (and `--deferred`) to review what is
  hidden. `--someday` for your own indefinite holds.

## Recurrence and lead time

- Interval cookies: `.+1w`, `++1m`, `+2d`, or `weekly` / `every 3 days`.
- Calendar schedules: `every mon,wed`, `weekdays`, `m:15`,
  `2nd tuesday`, `last day of the month`, `every july 4`.
- The stamp on the task *is* the next occurrence. Nothing is
  materialized ahead of time.
- `tasks recur <ref>` previews the next dates. `tasks recur --explain
  "every mon"` parses a schedule with no task and no store.
- Completing a recurring task prints `↻ … → next <date>` and stays
  open.
- Lead time (`tasks lead <ref> 3w`) hides a task until a span before
  its deadline, else its start date. Units `d` / `w` / `m` / `y` / `h`.
  The window re-arms when a recurring task rolls.
- Three different ways to hide a task, and they refuse to pretend they
  are the same: lead, timed defer, someday.

## Reading the list

- `tasks agenda` — dated items, soonest first, DUE vs STRT.
- `tasks next` — NEXT actions grouped by context. A task with two
  contexts appears under both.
- `tasks quadrants` — the 2×2, empty quadrants included.
- `tasks inbox` — unprocessed INBOX.
- `tasks projects` — commitments, not the reserved GTD query-lists.
- `tasks list` with `@ctx`, `+tag`, `/text`, `-A|-B|-C`, `--open`,
  `--proposed`, `--done`, `--archived`, `--all`, `--unavailable`,
  `--someday`, `--recurring`, `--delegated`, `--agent-ready`,
  `--body` (search notes).
- `tasks show <ref>` — headline, notes, links, availability reason.
- `tasks id <ref>` — print the stable id, minting one if a fossil is
  missing it.
- `--json` on the reads, already sorted the way the text view sorts.

## The TUI

- Six views, number keys 1–6, arrows between them: Agenda, Next,
  Quadrants, Projects, Outline, Inbox.
- Agenda grouped as a calendar: OVERDUE / TODAY / TOMORROW / LATER.
- Next grouped by `@context`.
- Projects as foldable headings. Empty projects collapse onto one row.
  The badge names the live share (`1/4 open`).
- Outline with overdue / today / later bands inside plain lists.
- Inbox as one intake tab: Approvals first, then accepted Inbox.
  `a` approves, `r` rejects, and the selection walks the queue.
- Shared row vocabulary: cursor gutter, urgency band, priority letter
  beside the title, right-aligned `@` column, shared date/meta column.
- Selection is a dark band, not reverse video.
- Return opens a detail rail: state, labels, notes, links, actions,
  subtask progress. Click or drag the rail to resize; `ctrl-k` /
  `ctrl-l` do the same from the keyboard.
- `e` edits the selected task. Fields save on blur. `ctrl-s` saves in
  place, `ctrl-o` finishes, a dirty Escape asks before reverting.
- A calendar / time / zone / fold picker on the date fields, plus
  plain text if you would rather type `fri 4pm`.
- `/` text filter. `@` context picker with multi-select, search, and
  a clear-all row. Filters AND together; contexts OR within themselves.
- `:` searchable action palette. The palette and the keys are the same
  registry as `?`.
- `c` complete, `d` date, `r` recur (or reject, on a proposal), `z`
  defer, `D` delegate (person or agent, mode, note), `W` work-ref,
  `K`/`J` raise/lower priority,
  `x` archive with preview, `#` / Delete hard-delete with confirm.
- `h`/`l` fold, `H`/`L` fold/expand all, `>`/`<` indent/outdent,
  `alt-j`/`alt-k` reorder.
- `Z` show/hide unavailable tasks.
- `o` opens one link, or a searchable picker when there are several.
- `y` yanks the stable id, `Y` yanks markdown, `p` pastes the id into
  the agent prompt.
- Tab focuses the agent prompt from the list or the detail rail.
  Return queues the request. `M` cycles provider/model. `A` opens
  agent activity. Quit with a live request asks first.
- Mouse: click a row, click again for details, click a tab, click a
  fold marker, wheel the thing under the pointer, drag the detail
  rail.
- Word-navigation and word-deletion in every text field.
- `?` help, filterable. Only matching bindings stay on screen.
- Session restore: last view, folds, panel size, context filters.
  An old session saved on the retired Approvals tab lands on Inbox.
- Themes: `default`, `mono`, and the usual suspects (dracula, nord,
  catppuccin-mocha, gruvbox-dark, tokyonight-night, solarized-dark, …).
  Per-slot `color.*` overrides, optional truecolor border gradient,
  `NO_COLOR` → mono.
- `mouse = on|off`. The terminal’s own selection still works with the
  bypass modifier.

## Projects

- `tasks project create "title"` bootstraps the Projects root if needed.
- `project show`, `project rename`, `project complete` (closes the
  open subtree), `project archive` (`--force` past leftover open
  work).
- The reserved GTD lists still resolve to `project show|rename|…`
  even though `projects` does not list them.

## Proposals

- `PROPOSED` is its own lifecycle, not a cute spelling of INBOX.
- Hidden from agenda, next, quadrants, inbox, project rollups, and
  the default open list.
- Cannot recur. Cannot be completed. Archive leaves them live until
  someone decides.
- `tasks approve` → INBOX. `tasks reject` → CANCELLED, with an
  optional note.
- `priority` / `retitle` / `tag` / `note` can still edit a proposal
  without accepting it.
- The TUI Inbox tab badges pending approvals as `inbox 1!`. When the
  approval queue is empty it badges the inbox count instead, and a
  quiet zero is omitted.

## Delegation and agent work

- Hand a task to a person: `tasks delegate <ref> --to pat@example.com`
  (moves it to WAITING unless `--keep-state`).
- Offer it to the agent pool at `refine`, `research`, or `implement` —
  or at whatever words you set `delegation_modes` to. A mode is
  optional on a person too: there it says what was asked for.
- Brief the receiver: `--note "…"`, `--note-file <path>`, or
  `--note-file -` for stdin, so a long briefing need not fight shell
  quoting. Who, mode, and note are one write and one undo step.
  `--note off` clears it; omitting the flag keeps what is there.
  The briefing prints in `show`, previews under both delegation list
  scopes, and is carried whole by every `--json`, including
  `list --agent-ready --json`.
- `tasks undelegate` clears the marker and any live claim.
- `tasks workref` records the single place the work actually happened.
  Survives completion and archival.
- `tasks claim --worker <id>` is compare-and-set. A lost race names
  the holder and exits 1. `--json` returns the full task so the worker
  reads its authority in one step.
- `tasks release --worker <id>` hands a claim back. `--note` records
  the blocker in the same undo. `--force` is the owner override.
- `list --agent-ready` is the heartbeat queue, ranked. `list --delegated`
  is everything handed off.
- Completing a delegated recurring task keeps the standing intent
  (mode or person) and returns the next occurrence to the queue,
  unclaimed, without the finished cycle’s work-ref.
- Authority modes are real limits: refine does not implement, research
  does not ship, implement still does not deploy, message, or buy
  things. The task text and the repo’s own instructions remain the
  only permission.

## Links

- Formal, ordered, labelled links on the task (`link add` / `rm` /
  `set`).
- Links in titles and notes too: bare URLs, `[[url][label]]`, and
  configured shorthands (`jira:OPS-1234`).
- `tasks links` lists the union, classified by system. `--system`,
  `--all`, `--json`.
- `tasks open <ref>` launches one, or lists them numbered. `--print`
  if you only wanted the URL. `TASKS_OPENER` overrides the browser.
- `link.<name>` URL templates and `system.<name>` custom hosts in
  config.

## Agents, on purpose

- `tasks -p [--provider N] [--model N] "…"` runs a harness against
  the list. The agent’s job is the list, not the work the tasks
  describe, unless you unmistakably say “do it now.”
- Built-in adapters: `claude-cli`, `hermes`, `cursor-cli`. Adding
  another is an adapter, not a rewrite.
- The list-agent contract is embedded in the installed binary from
  `internal/agentcontext/TASK_AGENT.md`. A source checkout is not
  required at runtime.
- Current-environment facts (`prompt.datetime`, `prompt.hostname`)
  inject into the system prompt so “tomorrow” means the machine’s
  tomorrow.
- Optional `agent-memory.md` beside the list: durable, user-approved
  filing defaults. Created only when you say “remember.” Never
  inferred from three garden tasks happening to carry `@home`.
- TUI queue: FIFO, one live harness at a time, cap 100 waiting,
  session-only activity log of the last 50 finished plus everything
  still in flight.
- `TASKS_WORKER_ID` supplies a claim identity when the flag is
  omitted. The `workref --worker` flag is deliberately *not*
  defaulted from it.

## The file, and the things that refuse to let you ruin it

- One inspectable JSON record per line. Schema v2. Canonical key
  order, so a field change is usually a one-line diff.
- Stable 8-hex ids on every record. Never reused.
- Tree via `parent` pointers. File order is DFS pre-order and
  `tasks check` enforces it.
- `tasks.jsonl` for the live list, `archive.jsonl` for swept
  history. `tasks archive` is the sweep.
- An unconfigured binary refuses to read or write. No implicit
  “current directory” fallback, no writing into the install prefix.
- Resolution: `TASKS_FILE` / `TASKS_ARCHIVE` / `TASKS_MEMORY`, then
  `TASKS_DIR`, then `~/.config/tasks/config`.
- Exclusive flock on writes, shared lock on snapshot reads, 5s
  timeout, then `lock timeout after 5s: held by pid … since …`
  instead of hanging forever.
- Atomic replace, then validate, then rollback if the result is
  not a legal store.
- `tasks check` and `tasks check --all-files` (cross-file duplicate
  ids included).
- `tasks repair` for the defects no single mutation can converge:
  id-less records, unknown keys inside time objects. `--dry-run`
  first.
- Schema version gate: this build reads v2 and nothing else, on
  every surface. `check` / `config` / `help` / `version` still run
  so they can tell you why.
- Content-addressed undo journal shared by CLI, TUI, API, and the
  agent. `tasks undo` / `tasks redo`. TUI `u` / `ctrl-r`. Consecutive
  field saves in one edit session may coalesce; a CLI write in
  between will not.
- `updated=<RFC3339>#<device>` stamps on records that actually
  changed, for merge. Not part of the ETag.

## Multi-device Git

- Task files can live in a private repo.
- `tasks install-merge-driver` registers a record-aware
  `merge=tasksjsonl` driver.
- Field-level 3-way merge by stable id. Independent edits combine.
  Same-field conflicts use the `updated` stamp.
- Delegation merges as one atomic value, never a spliced two-owner
  claim.
- A refused merge writes both sides inside ordinary conflict
  markers and logs the reason. Git cannot stage a clean-looking
  partial result.
- `docs/multi-device-sync.md` is the recovery guide.

## HTTP, still local

- `GET /api/v1/meta`, `/sections`, `/tasks`, `/tasks/{id}`,
  `/projects`, `/views/{name}`, `/recurrence/explain`.
- Writes: create, patch, delete, approve, reject, delegate,
  undelegate, claim, release, work_ref, delegation_note,
  project complete/archive. The delegation writes honour the same
  mandatory `If-Match` as every other task write.
- `GET /history`, `POST /history/undo`, `POST /history/redo`.
- Archive preview and sweep.
- `/events` for store-change invalidation.
- `/healthz` and `/readyz` outside `/api/v1`.
- JSON only, 64 KiB bodies, unknown fields rejected, revision
  ETags, loopback bind, Host/Origin checks. No remote auth mode
  because there is no remote mode.

## Config knobs

- `urgent_days` / `TASKS_URGENT_DAYS` (default 3).
- `max_depth` / `TASKS_MAX_DEPTH` (default 4).
- `timezone`, `time_format` (12|24), `date_order`.
- `delegation_modes` / `TASKS_DELEGATION_MODES` — the delegation
  vocabulary (default `refine, research, implement`). Comma-separated
  lowercase words; `off`, `none`, `clear`, and `release` are reserved
  because a delegation is written with them: `release` is a verb and the
  others clear a work reference or a note. A list that cannot
  be honoured is ignored whole, with a warning, and never blocks a write.
- `theme`, `mouse`, `color.<slot>`, `color.border`,
  `color.border_gradient`.
- `host_context.<hostname> = @wherever`.
- `prompt.<name> = on|off`.
- `link.<name>` and `system.<name>`.
- `llm_provider`, `llm_model`, per-provider model lists and
  command overrides.
- `tasks config` prints every resolved path and where it came from.
  `--json` includes the prompt-fact map.

## Install and development

- Homebrew: `brew install marcus/tap/tasks` → `tasks`, `tasks-tui`,
  `tasks-api`.
- Darwin and Linux, arm64 and amd64, from GitHub release archives.
- `make build` / `make install` from source.
- `make install-local` and `make install-worktree` switch the
  machine-wide Homebrew links to a local build without uninstalling
  the formula. `make use-homebrew` switches back. `make
  install-status` says which one is live. Local `--version` includes
  branch, commit, and `-dirty`.
- `make screenshots` rebuilds the README images from a disposable
  demo store. It does not read or write the configured task list.

## What this is not

- A reminder app. There are no notifications.
- A calendar. Recurrence advances one stamp.
- A multi-tenant service. The API binds to loopback on purpose.
- A store you should hand-edit. `tasks check` will tell you, after
  the fact, how that went.
