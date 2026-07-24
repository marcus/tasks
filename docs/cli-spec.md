# tasks CLI — agent interface specification

The `tasks` CLI is the API for `tasks.jsonl`. Humans use it too, but the primary
audience is LLM agents. The CLI is the **only** writer: `tasks.jsonl` is a JSONL
store with per-record ids, a strict DFS pre-order, fixed key order, and a `meta`
line 1, so a hand-edit is error-prone by construction — every mutation has a
command. Commands go through the shared model layer (`lib/tasks/store.rb`), which
enforces conventions (e.g. dating an INBOX item promotes it to TODO) and validates
the file after every write.

Status legend: ✅ implemented · 🚧 planned (spec is authoritative for behavior
when it lands). `tasks check` is the escape hatch if the file is ever edited
out-of-band.

## Global conventions

**Invocation.** `bin/tasks <command> [args] [flags]` from the repo root (or the
`tasks` alias). Every command has a short alias. Synonyms are accepted where
an agent would plausibly reach for them (`done`/`complete`/`close` are the
same command); the canonical name is listed first. Unknown `--flags` are an
error (exit 1), never silently treated as positional args.

**File locations.** The task files don't have to live in this repo — the code
and your data are separable (so the project can be shared without the tasks).
Both the CLI and the TUI resolve `tasks.jsonl`/`archive.jsonl` through
`lib/tasks/config.rb`, highest precedence first:

1. `TASKS_FILE` / `TASKS_ARCHIVE` env vars (per-file; used by the test suite
   and for safe manual experiments).
2. `TASKS_DIR` env var — a directory containing `tasks.jsonl` and `archive.jsonl`.
3. Config file `~/.config/tasks/config` (or `$XDG_CONFIG_HOME/tasks/config`),
   `key = value` lines: `dir = ~/tasks`, or per-file `file = …` / `archive = …`.
   `~` expands; `#` comments (full-line, or inline after whitespace) and blank
   lines ignored — so a value can't contain ` #`; a bare `#` inside a value
   (e.g. a URL anchor) is fine.
4. Default: the repo root (current behavior).

The config file also carries non-path settings: `urgent_days = N` sets the
quadrants urgency window (see `quadrants`), overridable by the `TASKS_URGENT_DAYS`
env var, default 3. `max_depth = N` caps how deeply tasks may nest (integer ≥ 1),
overridable by the `TASKS_MAX_DEPTH` env var, default 4.

`timezone = Area/Location` sets the evaluation/display zone for floating times
and all-day boundaries. Resolution is `TASKS_TIMEZONE`, config, a valid IANA
`TZ`, the host `/etc/localtime` zoneinfo link, then `Etc/UTC` with a fallback
warning. `time_format = 12|24` controls human output; JSON always uses `HH:MM`
and RFC 3339. Full IANA identifiers are accepted for stored fixed values;
abbreviations such as `PST` are rejected.

A dotted `prompt.<name>` namespace toggles short facts injected into every agent
system prompt under a **Current environment** heading (see
[`prompt-context-injection.md`](plans/implemented/prompt-context-injection.md)):

```
prompt.datetime = on     # default: on — local `2026-07-15 Wed 08:41 PDT`
prompt.hostname = on     # default: on — Socket.gethostname
# prompt.weather = on    # future providers default off until registered as default-on
```

Truthy: `on` / `true` / `1` (case-insensitive). Falsy: `off` / `false` / `0`.
An invalid value is ignored (falls through to the registry default). Unknown
`prompt.*` names are ignored at resolve time (forward compatibility). A provider
that errors or returns blank is omitted silently; the rest of the block still
injects. Both `tasks -p` and the TUI queue assemble this through
`Tasks::AgentContext`.

Host-specific creation contexts use another dotted namespace:

```ini
host_context.marcus-home.local = @home
host_context.work-mbp = @work
```

Matching against `Socket.gethostname` is case-insensitive and tries the full
hostname before its first DNS label. Values without `@` are normalized. A
matched context is added to every task created through the CLI, TUI, or API,
alongside any explicit contexts. `capture --no-host-context` suppresses it for
one creation. `tasks config` reports the detected hostname, resolved context,
and matching config key.

Two dotted namespaces configure links (see `links`/`open`):

```
link.jira   = https://acme.atlassian.net/browse/%s   # shorthand: notes can say jira:OPS-1234
link.gh     = https://github.com/%s                  # gh:acme/app/pull/412
system.gitlab = gitlab.acme.io                       # classify this host as "gitlab"
```

`link.<name>` makes `<name>:<value>` in a task body expand through the URL
template (`%s`, or appended if the template has none) — descriptions stay
terse and one config edit re-points every link if a host changes. Names are
`[a-z][a-z0-9_-]*`; only configured names match, so ordinary prose ("note:
this") can't false-positive. `system.<name>` classifies a custom host (and its
subdomains) for self-hosted systems the built-in registry can't know; user
rows win over built-ins.

**TUI colors.** The TUI paints semantic *slots* (`lib/tui/theme.rb` lists them
all: `accent`, `selection`, per-view tabs like `tab_agenda` /
`tab_agenda_active`, task-row fields like `project`, `context`, `title`, the
`due_*` ladder plus selected-row variants such as `due_soon_selected`,
detail-panel slots like `panel_title`, `detail_label`, `description`, `link`, `link_system`,
`state_*`, …). Appearance keys in the same config file:

- `theme = <name>` — a named base theme: `default`, `mono` (attribute-only),
  or a generated popular scheme such as `dracula`, `nord`,
  `catppuccin-mocha`, `gruvbox-dark`, `tokyonight-night`, or
  `solarized-dark`. The generated names come from
  `scripts/generate-tui-themes`, which converts iTerm2-Color-Schemes
  Window Terminal JSON into tasks semantic slots. Overridable by `TASKS_THEME`;
  a non-empty `NO_COLOR` env var selects `mono` when nothing explicit is set.
- `color.<slot> = <spec>` — restyle one slot on top of the theme. A spec is
  space-separated tokens: attributes (`bold`, `dim`, `italic`, `underline`,
  `reverse`), a named color (`red`, `bright-red`, `gray`, …), a 256-color index
  (`208`), or hex (`#ff8800`); prefix a color with `on-` for the background
  (`on-blue`, `on-#1e2030`); `none` = unstyled. Example:
  `color.selection = black on-cyan`. Invalid values fall back to the theme
  default rather than erroring. Because a hex token follows a space, `color.*`
  lines are exempt from inline `#` comments.
- `color.border = <spec>` — the container chrome (the window frame, modals, the
  form box, and the palettes all share it). This is the solid fallback used when
  the terminal lacks truecolor, `NO_COLOR` is set, or the gradient is disabled.
  `none` (the stock default) leaves the border the terminal's own foreground.
- `color.border_gradient = <stop> <stop> [<stop>…] @<angle>` — an angled
  truecolor gradient swept across the whole chrome, e.g.
  `color.border_gradient = #7aa2f7 #bb9af7 @60`. Two or more `#rrggbb` stops set
  the sweep; `@<angle>` is the direction in degrees (0 = left→right, 90 =
  top→bottom). The outer corners are drawn rounded (`╭ ╮ ╰ ╯`). Set it to `none`
  to disable the sweep and fall back to `color.border`. A malformed value
  degrades to the solid border rather than erroring. Only rendered on truecolor
  terminals; `mono`/`NO_COLOR` never sweep it.

`tasks config` prints the resolved paths, `urgent_days`, `max_depth`, `theme`,
the effective IANA `timezone`, `time_format` (12 or 24), and tzdb version
(+ any `color.*`, link, and `prompt.*` overrides), and where each came from.
`--json` includes `prompt_facts` (the effective name→boolean map).

**Multi-device Git merge plumbing.** Every Store write stamps only task records
whose semantic fields changed with `updated=<RFC3339 UTC second>#<device>`, for
example `2026-07-16T14:03:11Z#home`. The device is the first alphanumeric token
of `TASKS_DEVICE` or the hostname's first DNS label. Existing records without a
stamp remain valid and are treated as oldest during a merge. `updated` is not
part of task revision/ETag fingerprints, and undo/redo restores exact journal
bytes without re-stamping.

`tasks merge-driver <base> <ours> <theirs> <pathname>` is an internal,
Git-invoked CLI-only adapter. It performs a deterministic field-level 3-way
merge by stable id and writes valid canonical JSONL to `<ours>`; hard failure
leaves `<ours>` untouched and exits 1. `bin/install-merge-driver [data-repo]`
registers the absolute command in that repository's local Git config after
verifying `.gitattributes` selects `merge=tasksjsonl`. This is intentionally
not an HTTP capability: it is local Git transport plumbing, not user-visible
task behavior. See the root README for setup and audit-log details.

**TUI interaction.** `Tab` always focuses the agent prompt, including while a
read-only task panel is open. `p` inserts the selected task's stable id into
that prompt, and `y` copies the same stable id to the clipboard.
`:` opens the searchable, context-aware action palette; typing filters the
available actions, the arrow keys choose one, Return runs it, and Escape
cancels. `@` opens a searchable, fixed-size context selector for GTD `@` tags
(for example `@work` or `@home`). Typing filters and relevance-ranks the stable
list; arrow keys move the `❯` cursor without reordering choices. `Space` toggles
the cursor context (`●` marks every staged selection), Return applies the staged
set, and Escape cancels staged changes. Typing a context and pressing Return
still replaces the active set in one compact interaction. A leading **Clear all
contexts** row clears the staged set with Space or clears-and-applies with
Return. Multiple selected contexts match any selected context (OR within the
context facet); the `/` text filter composes with that group using AND.
Selected contexts are persisted in the TUI session and restored on the next
launch; contexts that no longer appear in the task set are pruned individually
on save. In list mode Escape clears `/` first, then the context group.
Direct shortcuts and palette entries invoke the same registered
actions. Return opens the read-only task-detail panel on the right in every
view; list navigation stays active and refreshes the panel for each newly
selected task. Return or Escape closes it. The existing `d` date and `r`
recurrence quick actions remain available.

**Editable task-panel behavior.** With a read-only task panel open, `e` enters
editing at the first editable field and `Shift-Tab` enters at the last. `Tab`
continues to focus the agent prompt. In edit mode, `Tab` and `Shift-Tab`
traverse in their respective directions. Leaving a changed field validates and
immediately saves that semantic field before focus moves; an unchanged field
moves without IO.
Validation errors and conflicts retain focus and the pending, copyable buffer.
Opening a picker, scrolling, resizing the terminal, or resizing the panel is
not blur and never saves.

Edit-mode keys are fixed as follows:

| Key | Contract |
|---|---|
| `Tab` / `Shift-Tab` | Validate and save on blur, then move forward/backward only after success. |
| `Ctrl-S` | Save the focused field in place. |
| `Ctrl-O` | Save the focused field if needed and finish editing, returning to the read panel. |
| `Ctrl-K` / `Ctrl-L` | Grow/shrink through compact → standard → wide → focus without blur; in task-edit text fields `Ctrl-K` intentionally shadows kill-to-end, while the agent prompt keeps its current `Ctrl-K`. |
| `Escape` | Close an inner picker first. A dirty field requires a confirming second Escape before only that buffer is reverted; a clean field leaves edit mode. |

Return on a scheduled or deadline row opens one structured temporal control.
It combines a calendar, 15-minute time adjustment, all-day/floating/fixed mode,
searchable full IANA zones, and an earlier/later fold choice that appears only
for an ambiguous civil time. Closing the control changes only the field buffer;
the complete temporal value is validated and saved atomically by the normal
save-on-blur rules. Direct text entry remains available.

The key reader treats `Shift-Tab` (`\e[Z`), CSI keys, and ESC-prefixed Alt
bindings as complete sequences, including when input arrives across reads, so
a partial sequence cannot become a destructive lone Escape. The editor is
bound to the selected task's stable ID. External changes to the same owned semantic slice conflict;
unrelated task or same-task field changes may be adopted without overwriting an
active buffer. Missing targets are never rebound to a neighboring row.

Quitting with `Ctrl-C`, or `q` while a resize-suspended editor exists, requires
an explicit visible confirmation before any unsaved field buffer is discarded.
Repeated quit keys do not confirm that prompt; `y`/Return confirms and
`n`/Escape cancels while retaining the draft.

Field ownership and order are contractual: Title owns `title`; Priority owns
`priority`; Available from owns `scheduled`; On hold owns only the indefinite
`defer` marker; Deadline owns `deadline`; each date owns its documented
INBOX/recurrence side effects; Recurrence owns
`recur`; Contexts owns `@` tags; Tags owns other non-`defer` tags; Notes owns
exact raw `body`; and State owns `state`, `closed`, recurrence completion, and
documented lifecycle effects. State is last, keeping high-impact changes out of
ordinary traversal. Parent/subtree placement is not an editor field: nesting is
handled at the store/move level. Manual ordering will use dedicated structure
actions in the unfiltered Outline tab described under [Nesting](#nesting), not
an editable form field.

Panel sizing uses named modes with content-cell breakpoints: 48 or more cells
may render short labels and controls inline; 32–47 cells stack them; 32 cells is
the editable minimum. Below that, the layout promotes to focus mode when it can
supply the minimum, otherwise it stays read-only and reports the required
width. Resize preserves task identity, focus, buffer, cursor, errors, picker
state, scroll, and edit-session identity.

Every successful blur is durable immediately. Consecutive writes in one edit
session may coalesce into one undo entry only when their non-nil session key
matches and the new exact `before` bytes equal the journal tip's exact `after`
bytes. CLI/external writes, undo/redo, reopening the editor, or any byte mismatch
breaks the group. If a successful Location or State patch removes the task from
the current view, the app immediately exits editing, selects a deterministic
nearby row, returns to the read panel or list, and explains where the task went.

The generated `?` help comes from `lib/tui/shortcuts.rb` and includes the
detail-panel entry keys, every editor-owned key, and the panel resize actions.
The embedded `TermForm` boundary can be exercised independently with
`ruby examples/term_form_demo.rb`; that plain renderer is extraction proof, not
a stable public or gem API.

`x` previews the number of completed roots and descendants that would move to
`archive.jsonl`; `y` confirms, while `n` or Escape cancels without writing.

**Queued TUI agent requests.** Return in the agent prompt accepts the request
even while another request is running. The TUI executes accepted requests one
at a time in FIFO order; at most one autonomous harness may mutate the task
files at once. Each request snapshots the selected `provider:model` at submit
time, so `M` affects only subsequently submitted requests. The waiting queue is
capped at 100; a full queue or an unavailable selected harness leaves the
prompt intact and focused.

The footer streams the active request and reports the pending count. `A` opens
a scrollable, filterable Agent activity modal containing every retained prompt,
status, provider/model, and transcript. Results are session-only: the latest 50
finished requests plus every active/pending request remain available until the
TUI exits. Escape cancels only the active request and continues with the next
queued request. The action palette can cancel all waiting requests without
touching the active one. Quit with active or pending work requires explicit
confirmation, then cancels the live process group and discards the queue.

### LLM agent settings

`-p` and the TUI hand your request to an **agent** — an autonomous harness
(the local `claude` CLI, the Hermes agent, …) that acts on `tasks.jsonl` itself
through this CLI. Which harness and model are chosen from the same config file;
all keys optional, unknown keys ignored:

```
llm_provider = hermes            # selected harness (default: claude-cli)
llm_model    = qwen3.6:35b-a3b   # selected model (default: provider's first model)
claude-cli_models = sonnet,opus,haiku   # override a provider's model list
hermes_models     = qwen3.6:35b-a3b      # override Hermes' model list
cursor-cli_models = composer-2.5-fast    # override Cursor CLI's model list
hermes_command    = hermes       # override the binary a provider spawns
cursor-cli_command = agent       # override the Cursor CLI binary
hermes_provider   = ollama-launch # Hermes inference provider (passed as --provider)
ollama_url        = http://127.0.0.1:11434  # endpoint Hermes' availability probe hits
```

Built-in providers are `claude-cli` (models `sonnet/opus/haiku`), `hermes`
(default model `qwen3.6:35b-a3b`, driving a local Ollama model via Hermes' own
config), and `cursor-cli` (default model `composer-2.5-fast`). Cursor CLI uses
the local `agent` binary in non-interactive force mode; authenticate first with
`agent login` or `CURSOR_API_KEY`, and run `agent --list-models` to discover
model ids available to the current account. Its text output contains the final
assistant message rather than structured tool progress. The overall default
stays `claude-cli:sonnet`. The TUI's `M` key cycles the flattened
`(provider, model)` list. The header and agent activity use concise display
aliases for known entries (for example `claude:sonnet`, `cursor:grok`,
`cursor:composer`, and `hermes:qwen`) while configuration and CLI flags retain
the exact provider/model ids; unknown ids fall back to their full names. Adding
a new harness is one adapter class in `lib/llm/` plus a `Registry::DEFAULTS`
entry — see `docs/plans/llm-adapter-pattern.md`.

**Local models:** a pre-JSONL eval of models behind Hermes
(`eval/llm/results-2026-07-02.md`) selected `qwen3.6:35b-a3b` as the default
Hermes model. It was the only candidate that handled every tested task type
without corrupting the Org store, but it took roughly 2–4 minutes per request.
The overall default remains `claude-cli:sonnet`. Treat those scores as
historical until the harness is rerun against `tasks.jsonl`.

**Task refs.** Mutations take a `<ref>` — a case-insensitive substring of the
task title. Resolution rules:

- Exactly one open task matches → act on it.
- Zero matches → exit 2, message `no match: <ref>`.
- Multiple matches → exit 2, listing each candidate as `L<line>: <headline>`.
  The agent retries with a longer substring or an exact `L<line>` ref.
- `L<line>` (e.g. `L42`) targets the record on that 1-based file line — precise,
  but only valid until the file changes. Prefer titles.
- An exact `id` (e.g. `7f3a9c2e`) resolves unambiguously and is stable across
  edits — it wins over fuzzy title matching. Get one with `tasks id <ref>`.
- By default refs match **open** tasks only; `--include-done` widens.

**Task IDs.** Every record carries a stable 8-hex `id` field — the durable handle
for that task no matter how lines shift or the title changes. Migration and
`capture` mint them; `tasks id <ref>` is the repair path for a record somehow
missing one. Mutations locate their target by id (falling back to line + title
otherwise), so an out-of-band reflow or retitle can't misfire an edit onto the
wrong task. IDs must be unique — `check` reports a collision as an error.

**Dates and times.** Anywhere a date is accepted: `2026-07-15`, `07-15`, `7/15`,
`fri`/`friday`, `today`, `tomorrow`, `+3` (days from today). Same parser as
the TUI (`lib/tasks/dates.rb`). Bare month-day in the past rolls forward a year.

`due`, `schedule`, and timed `defer` also accept `today 5pm`, `tomorrow at
09:30`, `fri noon`, `2026-07-20 17:00`, and `2026-07-20T17:00`. A time without
a zone is floating in the configured evaluation zone. `--timezone
Europe/London` makes it fixed; `--floating` explicitly selects floating mode;
`--fold later` selects the later instant during an ambiguous DST fold. A bare
time is rejected, as are seconds, abbreviations, numeric offsets, unknown IANA
zones, and nonexistent local times. `TASKS_TIMEZONE` overrides the config's
`timezone`; `time_format = 12|24` affects human output only.
If a later configuration-zone change makes a stored floating civil time
nonexistent, CLI/API reads fail safely with a corrective error instead of a
trace or partial result. The TUI reports the same error and temporarily
projects in UTC so the value can be edited.

**Availability and deferral.** `scheduled` is the task's single
available-from/start/defer-until value; `deadline` is its independent due value.
An open task is available on and after the exact `scheduled` boundary, and a future value filters it
out of `agenda`, `next`, `quadrants`, `inbox`, and the default `list`. The
semantic `defer` marker now means an indefinite **On Hold** state
(Someday/Maybe), not another date. A task retains its lifecycle state while
unavailable.

Availability is ancestor-aware. A task is available only when neither it nor
any task ancestor has a future available-from date or an On Hold marker. Closed
ancestor rows are skipped for lifecycle rendering and their open descendants
are hoisted, but those ancestors remain in the ancestry chain for availability:
their timed and On Hold constraints still propagate. When several timed
ancestors block a task, the latest boundary wins; an On Hold marker wins over every
date or time. `defer <ref> <date-or-date-time>` sets availability without moving `deadline`;
`someday <ref>` holds indefinitely; `activate <ref>` clears the task's own hold
and any own future available-from date. `list --unavailable` (`--deferred/-D`
compatibility alias) reviews all effective blockers, while
`list --someday`/`--on-hold` matches only an own indefinite marker. In the TUI,
`Z` reveals unavailable rows and `z` accepts a date/time, `someday`, or `now`.

Date-only deadlines remain on time for their whole calendar date. Timed
deadlines become overdue strictly after their resolved instant and sort by that
instant; same-day all-day deadlines sort after timed ones. Times affect task
semantics only. They do not schedule reminders or notifications.

**Recurrence.** A task *recurs* when it carries a `recur` cookie alongside a
`scheduled`/`deadline` date: `.+1w`, `++1m`, `+2d`. The prefix sets what the
interval is measured from on completion — `+` fixed (stored date + interval, one
hop), `++` catch-up (repeated until strictly future), `.+` from-completion (today
+ interval) — and the suffix is a count plus a unit (`d`/`w`/`m`/`y`; months/years
step by calendar with day-clamp, so Jan 31 `+1m` → Feb 28). Timed `++` catch-up
compares the exact release/due boundary, and `.+` uses the completion date in
the value's effective zone. A nonexistent recurring wall time is skipped by
another whole interval without changing its clock time or fixed zone.
Completing a recurring
task (`done`, or `state … DONE`) rolls its date forward and **leaves it open**
instead of setting `closed`; it logs a `- Did [date]` line to the body since the
task never closes. `cancel` still truly closes it
(stopping the recurrence). `recur <ref> <interval>` sets/replaces the cookie
(bare intervals like `weekly`/`2w`/`every 3 days` default to `.+`; `--from
schedule` uses `+`); `recur <ref> off` clears it; `list --recurring` reviews
them. Dating commands (`due`/`schedule`/`reschedule`) preserve an existing
cookie. In the TUI, `r` opens a recurrence popup on the selected task, a `↻`
badge marks recurring tasks, and completing one rolls it forward in place.

**Cascading completion.** Completing a parent completes its whole open subtree.
`done` (or `state … DONE`) on a task closes every open descendant
(INBOX/TODO/NEXT/WAITING) with the same `closed` date and drops any `defer`
tag. A recurring descendant closes **outright** — its `recur` cookie is retired,
not rolled forward: finishing the project finishes the sub-item (no date hop, no
`- Did` log). Already-closed descendants (DONE/CANCELLED) keep their existing
`closed`. The whole cascade is a single journal entry, so one `undo` restores
the subtree exactly. Completing a **recurring parent** is the exception: it rolls
its own date forward, stays open, and does **not** cascade (an occurrence, not
the project). `cancel` (and `state … CANCELLED`) never cascades — it closes only
the target. Reopening a cascaded parent (e.g. `state … TODO`) does **not**
reopen its descendants; reopen those individually. (Pre-existing caveat:
`archive` refuses the whole sweep when a DONE/CANCELLED root still has an open
descendant. Complete, cancel, move, or unnest the open descendant first; a
closed subtree only moves as one unit once every descendant is closed.)

**Nesting.** Tasks form a tree via their `parent` ids; the CLI both reads that
hierarchy (`list` groups it, `show` reports each task's `project` — unchanged
here) and edits it. Two depth terms govern the mutations:

- **task-depth** — the number of TASK records on a task's parent chain,
  counting itself. A task filed directly under a section is depth 1; sections
  don't count.
- **subtree height** — over the span `records[ri...subtree_end)` of a subtree,
  `max(task_depth) − task_depth(root) + 1` (a lone task has height 1).

The `max_depth` config (default 4; see [Global conventions](#global-conventions),
env `TASKS_MAX_DEPTH`) caps how deep tasks may nest, enforced only at these mutation
points (never in `check`, so a deeper legacy file still validates and rolls
back cleanly):

- `capture --under P` requires `task_depth(P) + 1 ≤ max_depth`.
- `move <ref> --under P` of subtree S requires `task_depth(P) + height(S) ≤
  max_depth`.
- A move to a section (positional `move <ref> "Section"`) or `move <ref> --top`
  is **never** depth-checked — it can only reduce depth, so it's the escape
  hatch for a legacy file already deeper than the cap.

`capture --under <ref>` files the new task as the last child of an existing task
(mutually exclusive with `--project`, which files under a section). `move`'s
destination is exactly one of a positional section, `--under <ref>` (nest the
whole subtree below another task), or `--top` (unnest to the section level).
Over-cap moves/captures exit 1 with a depth message and write nothing; nesting a
task under itself or a descendant exits 1 (a cycle); `move --top` on an
already-top-level task is a no-op (prints "already at top level", exit 0, burns
no undo slot). Completion still cascades over the whole subtree regardless of
depth (see Cascading completion).

In the TUI tree views, an open task under a *closed* (DONE/CANCELLED) ancestor
is **hoisted** to top level rather than dropped with its pruned parent — so a
reopened child, or a task captured under a since-completed project, still shows.
An *unavailable* ancestor is different: it still hides its whole subtree (unless
`Z` reveals unavailable tasks), and availability hiding wins over hoisting — a
closed node below an unavailable parent stays hidden with it. Conversely, a
closed ancestor that itself owns a future date or On Hold marker remains an
availability blocker even though its row is transparent and its open descendants
would otherwise be hoisted.

`h`/`l` collapse/expand the selected subtree (a collapsed node shows `▸` and a
dim count of hidden descendants; a second `h` on a leaf or already-collapsed
node climbs to the parent), and `H`/`L` collapse/expand every subtree at once.
The collapsed set persists across restarts alongside the active view (pruned to
tasks that still exist), in `$XDG_STATE_HOME/tasks/tui.json`.

**Manual sibling placement.** The exact CLI forms are:

```text
tasks move <ref> --before <anchor-ref>
tasks move <ref> --under <parent-ref> --before <anchor-ref>
tasks move <ref> "Section" --before <anchor-ref>
```

`--before` alone infers the anchor's current direct parent. With `--under` or a
positional section, the anchor must be a direct child of that explicit
destination. `--before` cannot be combined with `--top`; at most one explicit
destination is allowed. Existing `move <ref> --under <ref>`, `move <ref>
--top`, and positional section moves remain append operations.

Source, parent, and anchor task refs use normal exact-id/line/fuzzy resolution:
no match or ambiguity exits 2. Missing flag values, contradictory destinations,
a missing section, a self-anchor, an anchor outside the requested parent,
cycles, and excessive depth exit 1 and write nothing. A placement that already
describes the exact slot succeeds with exit 0, writes nothing, and creates no
undo entry.

Every new `--before` form has a non-null anchor. Its successful human output
prints a summary followed by the moved task's standard post-write headline; the
summary names the task and destination and ends with `before "<anchor>"`.
`--dry-run` prefixes that summary with `would`, prints the current headline,
and writes nothing; it takes precedence over `--json` and remains
human-readable. Non-dry-run `--json` keeps the standard `touched` array and adds
`placement` with `parent_id`, `parent_type` (`task`/`section`), `parent_title`,
and non-null `before_id` and `before_title`.

The legacy positional section, `--under`, and `--top` forms continue to build
their existing append/unnest location values and keep their current human,
JSON, and dry-run output. They do not emit the new placement summary or
`placement` JSON member. Appending through `TaskPlacement` remains available to
the API/TUI via an omitted/null `before_id`; no CLI grammar for that conversion
is added in this slice.

Agenda, Next, Quadrants, Inbox, and Projects are not eligible for ordering:
they filter, regroup, or sort away live siblings. The sixth **Outline** tab
renders every live section and task in canonical DFS order, including closed
and unavailable tasks. Only collapse may hide
descendants. `Alt+↑`/`Alt+k`, `Alt+↓`/`Alt+j`, `>`, and `<` reorder, indent,
and outdent in that unfiltered tab. In another tab, or while `/` text or `@`
context filtering is active, those keys are consumed and the footer directs
the user to the unfiltered Outline tab. Up/down stay within the current direct
sibling list; indent appends under the preceding sibling; outdent places the
subtree immediately after its old parent. Each action is one checked placement
changeset and one undo entry, while boundary/refusal cases write nothing.

**Output.** Human-readable by default. Read commands and mutations accept
`--json`; shapes below. Mutations always print (or return in JSON) the full
new headline of every task they touched — a single mutation may touch several
(a completion cascade closes the whole open subtree; every touched headline
prints, in file order) — so the agent can verify the result without a
follow-up read.

**Exit codes.** `0` success · `1` error (bad args, validation failure,
file corrupt) · `2` ref resolution failure (no match / ambiguous). Code 2 is
distinct so agents can branch: refine the ref rather than abort.

**Safety.** Every mutation validates the file afterward and rolls back if it
would introduce a structural error. `--dry-run` on any mutation prints what
would change and writes nothing.

### Task-set agent memory

A task set may carry an optional Markdown sidecar, `agent-memory.md`, holding
durable, user-approved defaults for managing that list ("garden tasks use
`@home`"). It travels with the task data, not this checkout, so a private task
repo can commit `tasks.jsonl`, `archive.jsonl`, and `agent-memory.md` together
and cloning brings the right defaults along. The file is human-authored
Markdown — read and diffable in Git; it is *not* a structured store and does not
relax the CLI-only rule for `tasks.jsonl`/`archive.jsonl`.

When present and non-empty, its contents are appended to the agent's system
context (below `TASK_AGENT.md` and the Current environment / file-locations
blocks, inside a delimited block) on **every** `-p` run and
every TUI request — read fresh each time, never cached, so a default saved by
one request is visible to the next and an out-of-band edit or `git pull` is
picked up without a restart. An absent file simply means "no saved defaults";
the CLI never creates it as a side effect of running an agent. Agents create or
edit it only when a user explicitly asks to remember, change, or forget a
default (the policy lives in [`TASK_AGENT.md`](../TASK_AGENT.md)).

Resolution adds `memory` to the config paths, highest precedence first:

| Precedence | Location | Purpose |
| --- | --- | --- |
| 1 | `TASKS_MEMORY` env var | One-run or test override. |
| 2 | `memory = /path/to/agent-memory.md` in the config file | Intentional nonstandard location (`~`/relative expanded). |
| 3 | `agent-memory.md` beside the resolved `tasks.jsonl` | Normal per-task-set default. |

The default is derived from the **final** `tasks.jsonl` path, so a `TASKS_FILE`
override selects its sibling `agent-memory.md` even when the base dir or archive
resolve elsewhere. An empty `TASKS_MEMORY` is ignored (falls through to the next
level).

**Size budget and errors.** Before injection the sidecar is capped at 16 KiB
UTF-8. If it exists but is oversize, unreadable, invalid UTF-8, or contains one
of the reserved `----- BEGIN/END AGENT MEMORY -----` delimiter lines (which
could escape the fence that marks the block as data), the run fails loudly with
the path and reason rather than silently dropping the user's defaults: `-p`
aborts before starting the agent (exit 1), and in the TUI the request surfaces
as a failed queue event carrying the same message — never a crash and never a
run without the defaults.

**Diff.** After a `-p` run in a git-backed task set, the change summary includes
`agent-memory.md` alongside the task files, so a saved default shows up in the
same diff as the captured task. A sidecar relocated by `TASKS_MEMORY`/`memory`
to *outside* the task-data repo can't be diffed there; instead a one-line notice
points at its path.

**`tasks config`.** Reports the resolved memory path, its source, and whether
the file exists. `--json` adds `memory` (the path), `memory_exists` (boolean),
and `sources.memory` (`"TASKS_MEMORY env"` / `"config file"` /
`"beside tasks.jsonl"`, or `"pinned"` under a hermetic sandbox).

## Read commands

| Command | Alias | Status | Description |
|---|---|---|---|
| `list [filters]` | `l` | ✅ | All tasks grouped by state. Filters compose: `@context`, `+tag`, `/text` or bare word, `-A/-B/-C`, scope `--open/-o` (default) `--done/-d` `--archived/-x` `--all/-a`. Effectively unavailable tasks are hidden from the default open scope; `--unavailable` (compatibility alias `--deferred/-D`) lists timed, inherited, and indefinite blockers; `--someday/--on-hold` selects tasks carrying their own indefinite marker. Those two filters are mutually exclusive. With a closed/archive scope, legacy `--deferred` and `--someday` filter the own marker; explicit `--unavailable` is rejected because every closed task is unavailable for lifecycle reasons. `--recurring/-R` lists tasks with a repeater. `--body/-b` widens text matching into notes. `--json` |
| `agenda` | `a` | ✅ | Available dated items, soonest first. `--json` |
| `next` | `n` | ✅ | NEXT actions by context. `--json` |
| `quadrants` | `q` | ✅ | Covey 2×2 from priority (A/B ⇒ important) + a `DEADLINE` within `urgent_days` (default 3, overdue counts) ⇒ urgent, with `important`/`urgent` tags as overrides. `--json` adds `quadrant`. |
| `inbox` | `i` | ✅ | Unprocessed INBOX items. `--json` |
| `projects` | `pj` | ✅ | Projects and areas rolled up over their open, non-deferred tasks (at any depth). Projects are the section children of the top-level "Projects" heading (listed even when empty); areas are the other top-level sections that currently hold open work (Inbox excluded). Each carries an open count, a NEXT count, the soonest deadline-or-scheduled value, and a `stuck` flag (no open NEXT — including an empty project). Ordered projects-before-areas, then by soonest boundary (nil last), then title. `--json` adds `next_time` and `next_at` beside the compatibility `next_date`. |
| `show <ref>` | `s` | ✅ | One task in full: rendered headline + body/notes + links. Human output labels `scheduled` as `available from` and reports exact effective availability. `--json` keeps nullable ISO `scheduled`/`deadline` and adds nullable `scheduled_time`/`deadline_time` plus `available_at`; time objects carry `local`, stored `timezone`, `fold`, `effective_timezone`, and derived UTC `instant`. Reasons remain `available`, `scheduled`, `on_hold`, `ancestor_scheduled`, `ancestor_on_hold`, or `closed`. |
| `id <ref> [--json]` | | ✅ | Print a task's stable `id`, minting one if absent (post-migration every record already has one — this is the repair path). Idempotent. Resolves refs regardless of state. |
| `links [<ref>]` | `urls` | ✅ | Links found in task titles/notes, classified by system (`slack`, `jira`, `github`, …; unknown hosts fall back to the host name; Confluence-on-Atlassian is told apart from Jira by its `/wiki` path). One task's links with `<ref>`; every open task's otherwise. `--system <name>` filters (case-insensitive), `--all` widens the listing to done + archived (`<ref>` resolution itself stays live-file only), `--json` emits `{links: [{url, label, system, task, id, line, source}]}`. Recognizes org links `[[url][label]]`, bare URLs, and configured shorthands (below), in file order; org-internal targets (`[[id:…]]`, `[[file:…]]`, headline links) are org navigation, not links. |
| `open <ref> [n]` | `o` | ✅ | Open a task's link in the browser (macOS `open` / `xdg-open`; `TASKS_OPENER` overrides). One link opens directly; several are listed numbered (exit 1) unless picked by 1-based `n` or `--system <name>`. `--print` prints the URL instead of launching. Resolves refs regardless of state (live file). |
| `check [--json] [--all-files]` | `k` | ✅ | Validate `tasks.jsonl` structure (records, ids, DFS order, dates). `--all-files` also validates `archive.jsonl` and rejects any stable id present in both files; sync automation uses this after a merge. Exit 1 if errors. The escape hatch after any out-of-band edit — and see Repairing an invalid record below for how a mutation can fix the broken record it names. |

JSON list shape (`--json` on list/agenda/next/quadrants/inbox) — a flat array,
already sorted the way the text view sorts:
`[{"state": "NEXT", "priority": "A", "title": "…", "tags": [..], "contexts": [..], "deferred": false, "scheduled": null, "scheduled_time": null, "deadline": "2026-07-02", "deadline_time": null, "available": true, "available_at": null, "availability_reason": "available", "availability_blocker_id": null, "recur": null, "line": 17, "source": "live", "headline": "NEXT [#A] …"}]`
(`headline` is the star-less summary rendered from the record's fields; `source`
is `"live"` or `"archive"`; `recur` is the cookie string, e.g. `".+1w"`, or `null`.)
`quadrants --json` adds `"quadrant": "Q1".."Q4"` per item. Empty result → `[]`.

## Create

| Command | Alias | Status | Description |
|---|---|---|---|
| `capture "text"` | `add`, `c` | ✅ | New INBOX item. `--due` and `--scheduled` accept complete date/time expressions. Each has independent `--due-timezone`/`--scheduled-timezone`, `--due-floating`/`--scheduled-floating`, and `--due-fold`/`--scheduled-fold` modifiers; a modifier without its matching value is rejected. Other flags remain `--priority`, repeatable tags/contexts, `--no-host-context`, state, project/under, recurrence, dry-run, and JSON. A configured host context is additive with explicit contexts unless suppressed. A capture with either temporal value lands as TODO unless state is explicit. |

## Update (all take `<ref>`, all support `--dry-run`)

| Command | Alias/synonyms | Status | Description |
|---|---|---|---|
| `done <ref>` | `complete`, `close`, `d` | ✅ | Mark DONE + `closed` date, cascading to every open descendant (see Cascading completion); recurring descendants close outright and their recur cookie is retired. A recurring task (recur cookie on its date) rolls forward and stays open instead — output shows `↻ <title> → next <date>` — and does **not** cascade. `--dry-run` also previews how many open descendants would close. |
| `cancel <ref>` | `drop` | ✅ | Mark CANCELLED + `closed` date. |
| `state <ref> <STATE>` | `mv` | ✅ | Any state transition (INBOX/TODO/NEXT/WAITING/DONE/CANCELLED). Enforces: entering DONE/CANCELLED sets `closed`; leaving them clears it. Entering DONE cascades to open descendants (see Cascading completion); entering CANCELLED does not. Resolves refs across open *and* closed tasks so you can reopen a DONE item (reopening does not reopen cascaded descendants). |
| `due <ref> <date-or-date-time>` | `deadline`, `reschedule` | ✅ | Atomically replace `deadline`; accepts `--timezone ZONE` or `--floating`, plus `--fold earlier\|later`. Omitting time creates an all-day value and clears old time metadata. INBOX items promote to TODO. |
| `schedule <ref> <date-or-date-time>` | | ✅ | Atomically replace `scheduled` with the same temporal flags. A future exact boundary hides the task, but this command does not clear an On Hold marker; callers that mean deferral use `defer`. Same INBOX promotion. |
| `undate <ref>` | | ✅ | Remove `scheduled` and/or `deadline` (`--kind deadline\|scheduled` to pick one). |
| `priority <ref> <A\|B\|C\|none>` | `pri` | ✅ | Set or clear the `priority` field. |
| `retitle <ref> "new title"` | `rename` | ✅ | Replace the `title`; tags/priority/state untouched. |
| `tag <ref> +foo -bar @ctx -@old` | | ✅ | Add/remove tags and contexts in one call. `+t`/`@ctx` add, `-t`/`-@ctx` remove. |
| `note <ref> "text"` | | ✅ | Append a line to the task's `body`. |
| `move <ref> ("Section" \| --under <ref> \| --top)` | | ✅ | Relocate a task's whole subtree by re-pointing its `parent`. Exactly one destination: a positional **section** name (out of `Inbox` into `Work`), `--under <ref>` to **nest** below another task, or `--top` to **unnest** to the section level. A section name resolves in the same widening tiers as `capture --project` (exact top-level, exact any-level, substring top-level, substring any-level; case-insensitive), so a **nested project sub-section** — e.g. a project under the "Projects" root — is a valid destination, not just a top-level heading. Section and `--top` moves are never depth-checked; `--under` is capped at `max_depth` (over-cap exits 1 with a depth message). Nesting under itself or a descendant exits 1 (cycle). `--top` on an already-top-level task prints "already at top level" (exit 0, no-op). See Nesting. |
| `move <ref> ["Section" \| --under <ref>] --before <ref>` | | ✅ | Place the whole subtree before a stable sibling. Without an explicit destination, infer the anchor's current parent; otherwise require the anchor to be a direct child of the named task/section. Not combinable with `--top`. Exact errors and human/JSON/dry-run output are frozen under Manual sibling placement above. |
| `recur <ref> <interval>` | `repeat`, `every` | ✅ | Attach/replace the `recur` cookie on the task's date. `<interval>`: a cookie (`.+1w`/`+2d`/`++1m`) or friendly form (`weekly`/`daily`/`monthly`/`yearly`/`2w`/`every 3 days`); `off`/`none` clears it. `--from schedule\|completion` picks `+`/`.+` for a bare interval (default `completion` → `.+`). `--on <date>` seeds a `deadline` when the task has no date yet (else it errors). `--dry-run`/`--json`. |
| `defer <ref> [date-or-date-time]` | `snooze` | ✅ | With a value, atomically set `scheduled` and clear the task's own indefinite marker, preserving `deadline`; accepts the same temporal flags as `schedule`. Without a value, put it On Hold indefinitely. Output and `--dry-run` report exact ancestor-aware availability. |
| `someday <ref>` | | ✅ | Canonical spelling for an indefinite Someday/Maybe / On Hold task. Adds the own `defer` marker without changing either date. Idempotent. |
| `activate <ref>` | `undefer`, `resume` | ✅ | Make the task available now: clear its own indefinite marker and clear its own `scheduled` only when that date is in the future. A blocker inherited from an ancestor remains effective and is reported. Resolves unavailable open tasks. |

### Repairing an invalid record

Every mutation preflights the whole file and normally refuses with a "task file
is already invalid — run `tasks check` (nothing was written)" hint when it finds
breakage, since editing on top of a broken file isn't trustworthy. The one
exception is a **targeted repair**: an update command (`schedule`, `undate`,
`due`, `retitle`, …) whose `<ref>` resolves to the *only* broken record may fix
it. Because hand-editing is forbidden and `check` only reports, this is the
supported way to clear a malformed field (e.g. `schedule <ref> 2026-08-01` over
a record with `"scheduled":"not-a-date"`, or `undate <ref>` to drop a bad
stamp).

The contract is narrow:

- Repair engages only when **every** `check` error is on the record the command
  targets. If any other record is also broken, the command still refuses with
  the "already invalid" hint and writes nothing — fix the others first (each via
  its own targeted mutation).
- Raw-safety comes first: a file that isn't valid UTF-8, or that has a line which
  isn't parseable JSON, always refuses — even when that line is the target.
- After the write the file must validate **completely**, or the change rolls back
  (exit 1). A repair can't leave the file partially broken.
- `undo` of a repair faithfully restores the prior (invalid) bytes, so you can
  retry a different fix.

## Projects

`projects` (alias `pj`) lists projects and areas; the `project <verb>` command
group reads and mutates a single project or area. A fixed verb set avoids the
title ambiguity a bare `project "<ref>"` would create.

| Command | Synonyms | Status | Description |
|---|---|---|---|
| `project create <title> [--json] [--dry-run]` | `project new` | ✅ | Create a new empty project — a section filed under the top-level "Projects" root (created first if the store has none yet, so an empty/rootless file still works). A blank title, or one that duplicates an existing project or area (case-insensitive; the project-ref candidate set, so a duplicate would make later refs ambiguous), exits 1 with the reason. Success prints the new project row (`--json` emits the project object). `--dry-run` writes nothing. Then `move <ref> "<title>"` files tasks into it. |
| `project show <ref> [--json]` | | ✅ | One project/area in full: title, kind, rolled-up open/NEXT counts, soonest date, and body note. `--json` is the project object (same shape as a `projects` element). |
| `project complete <ref>` | `project done` | ✅ | Close every open descendant task of the project — the same cascade as `done`: DONE + today's `closed` date, `defer` dropped, and a recurring descendant retired (its cookie removed, no roll-forward). Prints every touched task's new headline (identified by line). |
| `project archive <ref> [--force]` | | ✅ | Sweep the project's whole section subtree to `archive.jsonl` (the root section drops its `parent` and gains today's `archived` stamp). Refuses with exit 1 while the project still has open work unless `--force`; deferred/held tasks (`held_count`) count as open work too, so a parked-but-open project also refuses (parity with `project complete`, which closes them). |
| `project rename <ref> <new title>` | | ✅ | Replace the section title (leading/trailing space trimmed). |

**Project refs.** A `<ref>` resolves against the `projects` listing: an exact
8-hex section id (case-insensitive) wins, then an `L<line>` section line, then a
case-insensitive title substring across both projects and areas. Zero matches or
an ambiguous substring exits 2, listing candidates as `L<line>: <title>` — the
same contract as task refs. All four commands accept `--json`; the three
mutations accept `--dry-run` and write nothing in that mode.

Over the HTTP API the same capability is `GET /api/v1/projects`,
`POST /api/v1/projects` (create — `{"title": …}` → 201 with the project; 422 on
a blank/duplicate title), `GET/PATCH /api/v1/projects/{id}`, and
`POST /api/v1/projects/{id}/complete` and `…/archive` — strict 8-hex ids only,
no fuzzy refs (a transport difference per design rule 7). See
`docs/api/openapi.yaml`.

## Lifecycle / meta

| Command | Alias | Status | Description |
|---|---|---|---|
| `archive` | `x` | ✅ | Sweep each DONE/CANCELLED subtree to `archive.jsonl` (root drops `parent`, gains `archived`). Refuses with exit 1 when any candidate root has an open descendant and explains how to resolve it. Persistence is retry-safe across interruption: the archive is installed first, and live records are removed only when the archive contains exactly one canonical copy of every moved ID; partial or conflicting overlap refuses without deleting live data. In the TUI, `x` previews root and descendant counts and requires `y` confirmation; the Store validates that exact candidate-ID/content fingerprint under the sweep lock, while `n`/`esc` cancels without writing. |
| `delete <ref>` | | ✅ | Undoable **hard delete** of a task's subtree from the live file — not an alias for `CANCELLED`, and it never touches `archive.jsonl`. A leaf deletes directly; a task that still has descendants is refused (exit 1) unless `--cascade` removes the whole contiguous subtree as one journal entry. Deleting never hoists or reparents children. Archived-only ids are not found (exit 2 via ref resolution / `not_found`); a section id is rejected (delete targets tasks). Resolves open tasks by default; `--include-done` widens to closed live tasks (they are still live records). Reports every removed task's pre-delete headline (`--json` → `{deleted: [..]}`); `--dry-run` prints what would be deleted, including the descendant count when cascading, and writes nothing. Undoable via `tasks undo` (restores the exact prior bytes). Cancellation/archival is usually the right call — `delete` is for genuine mistakes. |
| `undo` | | ✅ | Revert the last mutation via the on-disk journal (`Tasks::Journal`, under `$XDG_STATE_HOME/tasks/journal/`), shared with the TUI and across CLI runs. Refuses (exit 1) if `tasks.jsonl` changed out-of-band since that edit — resolve with `git diff` / `git checkout -- tasks.jsonl`. |
| `redo` | | ✅ | Replay the last undone mutation; same shared journal and conflict guard as `undo`. |
| `migrate [--dry-run] [--json]` | | ✅ | Check and migrate schema-v1 live/archive files to v2 under the Store lock. The migration changes only each meta version, writes `.v1.bak` backups for every existing source (including a zero-byte backup for an empty archive), validates both outputs, rolls both back on failure, and establishes an undo-journal schema barrier. It is idempotent. Preview with `--dry-run` before upgrading every machine; old binaries refuse v2. Before any v2 edits, explicit recovery may restore live and archive backups together; after v2 edits, reconcile them first because backup restoration discards later work. |
| `-p [--provider N] [--model N] "prompt"` | | ✅ | Natural-language request via a headless LLM agent (Claude CLI by default, or any configured harness). Leading `--provider`/`--model` override the config default for one run; see [LLM agent settings](#llm-agent-settings). |
| `config [--json]` | | ✅ | Print resolved file paths, `urgent_days`, `max_depth`, theme/colors, effective `timezone`, `time_format`, tzdb version, fallback warning, prompt facts, and each setting's source. |
| `help` | `-h`, `--help` | ✅ | Grouped command reference. Also printed (to stderr, exit 1) on an unknown/absent command. |

Ideas beyond this spec live in `docs/ideas.md`.

## Design rules for new commands

1. **Spec first**: add/adjust the row here before implementing.
2. Thin dispatch in `bin/tasks`; logic in `lib/tasks/` (usually a `Store` method).
3. Mutations go through `Store#with_history` — never `File.write` directly.
   That buys the file lock, the post-write `check` rollback, the persistent
   undo journal, and crash-safe atomic writes (`Tasks::Atomic.write`).
4. Accept synonyms liberally, print the canonical name in output.
5. Every mutation's output includes the resulting headline(s).
6. Tests required: happy path, ref-not-found, ref-ambiguous, and
   `Tasks::Check.check` clean after every mutating test (the test helper's
   fixture makes this a one-liner).
7. **CLI/API parity by default**: user-visible task semantics belong behind
   `Tasks::Application` and should be exposed consistently by `bin/tasks` and
   the loopback HTTP API. Keep this spec and `docs/api/openapi.yaml` synchronized
   whenever both adapters expose the capability. CLI-only or API-only behavior
   requires an explicitly discussed product/security reason documented in the
   relevant spec (and an ADR or plan when architectural). Adapter mechanics may
   differ — fuzzy refs and friendly input on CLI, stable ids/JSON/ETags over
   HTTP — but the resulting domain behavior must not drift.
