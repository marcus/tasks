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

`tasks config` prints the resolved paths, `urgent_days`, `max_depth`, `theme`
(+ any `color.*` and link overrides), and where each came from.

**TUI interaction.** With no task panel open, `Tab` focuses the agent prompt.
`:` opens the searchable, context-aware action palette; typing filters the
available actions, the arrow keys choose one, Return runs it, and Escape
cancels. Direct shortcuts and palette entries invoke the same registered
actions. Return opens the read-only task-detail panel on the right in every
view; list navigation stays active and refreshes the panel for each newly
selected task. Return or Escape closes it. The existing `d` date and `r`
recurrence quick actions remain available.

**Editable task-panel behavior.** With a read-only
task panel open, `Tab` enters editing at the first editable field and
`Shift-Tab` enters at the last. In edit mode, those keys traverse in their
respective directions. Leaving a changed field validates and immediately saves
that semantic field before focus moves; an unchanged field moves without IO.
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

The key reader treats `Shift-Tab` (`\e[Z`) and other CSI keys as complete
sequences, including when input arrives across reads, so a partial sequence
cannot become a destructive lone Escape. The editor is bound to the selected
task's stable ID. External changes to the same owned semantic slice conflict;
unrelated task or same-task field changes may be adopted without overwriting an
active buffer. Missing targets are never rebound to a neighboring row.

Field ownership and order are contractual: Title owns `title`; Priority owns
`priority`; Deferred owns only the `defer` tag; Scheduled and Deadline each own
their date plus documented INBOX/recurrence side effects; Recurrence owns
`recur`; Contexts owns `@` tags; Tags owns other non-`defer` tags; Notes owns
exact raw `body`; Location owns parent/subtree placement; and State owns
`state`, `closed`, recurrence completion, and documented lifecycle effects.
Location is the late Placement field after Notes and State is last, keeping
high-impact changes out of ordinary traversal.

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
hermes_command    = hermes       # override the binary a provider spawns
hermes_provider   = ollama-launch # Hermes inference provider (passed as --provider)
ollama_url        = http://127.0.0.1:11434  # endpoint Hermes' availability probe hits
```

Built-in providers are `claude-cli` (models `sonnet/opus/haiku`) and `hermes`
(default model `qwen3.6:35b-a3b`, driving a local Ollama model via Hermes' own
config). The overall default stays `claude-cli:sonnet`. The TUI's `M` key cycles
the flattened `(provider, model)` list; the header shows `provider:model`.
Adding a new harness is one adapter class in `lib/llm/` plus a
`Registry::DEFAULTS` entry — see `docs/plans/llm-adapter-pattern.md`.

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

**Dates.** Anywhere a date is accepted: `2026-07-15`, `07-15`, `7/15`,
`fri`/`friday`, `today`, `tomorrow`, `+3` (days from today). Same parser as
the TUI (`lib/tasks/dates.rb`). Bare month-day in the past rolls forward a year.

**Deferral.** A task is *deferred* (someday/maybe) when it carries the semantic
`defer` tag — the same mechanism by which `important`/`urgent` tags drive the
quadrants view. Deferred tasks retain their state (a deferred `NEXT` is still a
`NEXT`) but are filtered out of the active views (`agenda`, `next`, `quadrants`,
`inbox`, and the default `list` scope) so they stop competing for attention.
`defer`/`activate` toggle the tag; `list --deferred` reviews them. The TUI hides
them too, with `Z` to show/hide and `z` to defer/activate the selected task.

**Recurrence.** A task *recurs* when it carries a `recur` cookie alongside a
`scheduled`/`deadline` date: `.+1w`, `++1m`, `+2d`. The prefix sets what the
interval is measured from on completion — `+` fixed (stored date + interval, one
hop), `++` catch-up (repeated until strictly future), `.+` from-completion (today
+ interval) — and the suffix is a count plus a unit (`d`/`w`/`m`/`y`; months/years
step by calendar with day-clamp, so Jan 31 `+1m` → Feb 28). Completing a recurring
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
A *deferred* ancestor is different: it still hides its whole subtree (unless `Z`
reveals deferred tasks), and defer-hiding wins over hoisting — a closed node
under a hidden deferred parent stays hidden with it.

`h`/`l` collapse/expand the selected subtree (a collapsed node shows `▸` and a
dim count of hidden descendants; a second `h` on a leaf or already-collapsed
node climbs to the parent), and `H`/`L` collapse/expand every subtree at once.
The collapsed set persists across restarts alongside the active view (pruned to
tasks that still exist), in `$XDG_STATE_HOME/tasks/tui.json`.

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

## Read commands

| Command | Alias | Status | Description |
|---|---|---|---|
| `list [filters]` | `l` | ✅ | All tasks grouped by state. Filters compose: `@context`, `+tag`, `/text` or bare word, `-A/-B/-C`, scope `--open/-o` (default) `--done/-d` `--archived/-x` `--all/-a`. Deferred tasks are hidden from the default open scope; `--deferred/-D` lists only them (a someday/maybe review); `--recurring/-R` lists only tasks with a repeater. `--body/-b` widens the text match into task notes (title-only otherwise, keeping refs predictable). `--json` |
| `agenda` | `a` | ✅ | Dated items, soonest first. `--json` |
| `next` | `n` | ✅ | NEXT actions by context. `--json` |
| `quadrants` | `q` | ✅ | Covey 2×2 from priority (A/B ⇒ important) + a `DEADLINE` within `urgent_days` (default 3, overdue counts) ⇒ urgent, with `important`/`urgent` tags as overrides. `--json` adds `quadrant`. |
| `inbox` | `i` | ✅ | Unprocessed INBOX items. `--json` |
| `show <ref>` | `s` | ✅ | One task in full: rendered headline + body/notes + links. `--json` shape: `{id, state, priority, title, tags, contexts, scheduled, deadline, recur, closed, line, notes: [..], project, links: [{url, label, system}]}`. `notes` is the task's `body` split to lines (a child's body never leaks in — children are separate records); `project` is the nearest OPEN ancestor — a live parent task, else the enclosing section; closed ancestors are skipped (same rule as the TUI's Projects view and detail panel). |
| `id <ref> [--json]` | | ✅ | Print a task's stable `id`, minting one if absent (post-migration every record already has one — this is the repair path). Idempotent. Resolves refs regardless of state. |
| `links [<ref>]` | `urls` | ✅ | Links found in task titles/notes, classified by system (`slack`, `jira`, `github`, …; unknown hosts fall back to the host name; Confluence-on-Atlassian is told apart from Jira by its `/wiki` path). One task's links with `<ref>`; every open task's otherwise. `--system <name>` filters (case-insensitive), `--all` widens the listing to done + archived (`<ref>` resolution itself stays live-file only), `--json` emits `{links: [{url, label, system, task, id, line, source}]}`. Recognizes org links `[[url][label]]`, bare URLs, and configured shorthands (below), in file order; org-internal targets (`[[id:…]]`, `[[file:…]]`, headline links) are org navigation, not links. |
| `open <ref> [n]` | `o` | ✅ | Open a task's link in the browser (macOS `open` / `xdg-open`; `TASKS_OPENER` overrides). One link opens directly; several are listed numbered (exit 1) unless picked by 1-based `n` or `--system <name>`. `--print` prints the URL instead of launching. Resolves refs regardless of state (live file). |
| `check [--json]` | `k` | ✅ | Validate `tasks.jsonl` structure (records, ids, DFS order, dates). Exit 1 if errors. The escape hatch after any out-of-band edit. |

JSON list shape (`--json` on list/agenda/next/quadrants/inbox) — a flat array,
already sorted the way the text view sorts:
`[{"state": "NEXT", "priority": "A", "title": "…", "tags": [..], "contexts": [..], "scheduled": null, "deadline": "2026-07-02", "recur": null, "line": 17, "source": "live", "headline": "NEXT [#A] …"}]`
(`headline` is the star-less summary rendered from the record's fields; `source`
is `"live"` or `"archive"`; `recur` is the cookie string, e.g. `".+1w"`, or `null`.)
`quadrants --json` adds `"quadrant": "Q1".."Q4"` per item. Empty result → `[]`.

## Create

| Command | Alias | Status | Description |
|---|---|---|---|
| `capture "text"` | `add`, `c` | ✅ | New INBOX item. Flags: `--due <date>`, `--scheduled <date>`, `--priority A\|B\|C`, `--tag t` (repeatable), `--context @x` (repeatable), `--state STATE`, `--project "Heading"`, `--under <ref>`, `--recur <interval>`, plus `--dry-run`/`--json`. A capture with a date lands already-processed as TODO (override with `--state`); `--recur` implies a date (defaults to scheduling it today) and lands it repeating; `--project` files it under that top-level heading (default: Inbox). `--under <ref>` instead nests it as the last child of an existing task (mutually exclusive with `--project`; exit 1 if both) — capped at `max_depth` (over-cap exits 1 with a depth message, writes nothing; see Nesting). |

## Update (all take `<ref>`, all support `--dry-run`)

| Command | Alias/synonyms | Status | Description |
|---|---|---|---|
| `done <ref>` | `complete`, `close`, `d` | ✅ | Mark DONE + `closed` date, cascading to every open descendant (see Cascading completion); recurring descendants close outright and their recur cookie is retired. A recurring task (recur cookie on its date) rolls forward and stays open instead — output shows `↻ <title> → next <date>` — and does **not** cascade. `--dry-run` also previews how many open descendants would close. |
| `cancel <ref>` | `drop` | ✅ | Mark CANCELLED + `closed` date. |
| `state <ref> <STATE>` | `mv` | ✅ | Any state transition (INBOX/TODO/NEXT/WAITING/DONE/CANCELLED). Enforces: entering DONE/CANCELLED sets `closed`; leaving them clears it. Entering DONE cascades to open descendants (see Cascading completion); entering CANCELLED does not. Resolves refs across open *and* closed tasks so you can reopen a DONE item (reopening does not reopen cascaded descendants). |
| `due <ref> <date>` | `deadline`, `reschedule` | ✅ | Set/replace `deadline`. INBOX items promote to TODO. |
| `schedule <ref> <date>` | | ✅ | Set/replace `scheduled`. Same INBOX promotion. |
| `undate <ref>` | | ✅ | Remove `scheduled` and/or `deadline` (`--kind deadline\|scheduled` to pick one). |
| `priority <ref> <A\|B\|C\|none>` | `pri` | ✅ | Set or clear the `priority` field. |
| `retitle <ref> "new title"` | `rename` | ✅ | Replace the `title`; tags/priority/state untouched. |
| `tag <ref> +foo -bar @ctx -@old` | | ✅ | Add/remove tags and contexts in one call. `+t`/`@ctx` add, `-t`/`-@ctx` remove. |
| `note <ref> "text"` | | ✅ | Append a line to the task's `body`. |
| `move <ref> ("Section" \| --under <ref> \| --top)` | | ✅ | Relocate a task's whole subtree by re-pointing its `parent`. Exactly one destination: a positional **section** name (out of `Inbox` into `Work`; matched case-insensitively, exact then substring), `--under <ref>` to **nest** below another task, or `--top` to **unnest** to the section level. Section and `--top` moves are never depth-checked; `--under` is capped at `max_depth` (over-cap exits 1 with a depth message). Nesting under itself or a descendant exits 1 (cycle). `--top` on an already-top-level task prints "already at top level" (exit 0, no-op). See Nesting. |
| `recur <ref> <interval>` | `repeat`, `every` | ✅ | Attach/replace the `recur` cookie on the task's date. `<interval>`: a cookie (`.+1w`/`+2d`/`++1m`) or friendly form (`weekly`/`daily`/`monthly`/`yearly`/`2w`/`every 3 days`); `off`/`none` clears it. `--from schedule\|completion` picks `+`/`.+` for a bare interval (default `completion` → `.+`). `--on <date>` seeds a `deadline` when the task has no date yet (else it errors). `--dry-run`/`--json`. |
| `defer <ref>` | `snooze` | ✅ | Mark a task deferred (someday/maybe) by adding a semantic `defer` tag. Deferred tasks keep their state but drop out of `agenda`/`next`/`quadrants`/`inbox` and the default `list` until reactivated. Idempotent. |
| `activate <ref>` | `undefer`, `resume` | ✅ | Clear the `defer` tag, returning the task to the active views. Resolves deferred (open) tasks. |

## Lifecycle / meta

| Command | Alias | Status | Description |
|---|---|---|---|
| `archive` | `x` | ✅ | Sweep each DONE/CANCELLED subtree to `archive.jsonl` (root drops `parent`, gains `archived`). Refuses with exit 1 when any candidate root has an open descendant and explains how to resolve it. Persistence is retry-safe across interruption: the archive is installed first, and live records are removed only when the archive contains exactly one canonical copy of every moved ID; partial or conflicting overlap refuses without deleting live data. In the TUI, `x` previews root and descendant counts and requires `y` confirmation; the Store validates that exact candidate-ID/content fingerprint under the sweep lock, while `n`/`esc` cancels without writing. |
| `delete <ref> --force` | `rm` | 🚧 | Hard-remove a record (no archive). Refuses without `--force`; suggest `cancel` instead. |
| `undo` | | ✅ | Revert the last mutation via the on-disk journal (`Tasks::Journal`, under `$XDG_STATE_HOME/tasks/journal/`), shared with the TUI and across CLI runs. Refuses (exit 1) if `tasks.jsonl` changed out-of-band since that edit — resolve with `git diff` / `git checkout -- tasks.jsonl`. |
| `redo` | | ✅ | Replay the last undone mutation; same shared journal and conflict guard as `undo`. |
| `-p [--provider N] [--model N] "prompt"` | | ✅ | Natural-language request via a headless LLM agent (Claude CLI by default, or any configured harness). Leading `--provider`/`--model` override the config default for one run; see [LLM agent settings](#llm-agent-settings). |
| `config [--json]` | | ✅ | Print resolved file paths (tasks file, archive, config file), `urgent_days`, `max_depth`, `theme` (+ any `color.*` overrides), and the source of each (`TASKS_FILE env`, `TASKS_DIR env`, `TASKS_URGENT_DAYS env`, `TASKS_MAX_DEPTH env`, `TASKS_THEME env`, `NO_COLOR env`, `config file`, `default`). |
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
