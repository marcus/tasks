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
env var, default 3.

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

`tasks config` prints the resolved paths, `urgent_days`, and where each came from.

### LLM agent settings

`-p` and the TUI hand your request to an **agent** — an autonomous harness
(the local `claude` CLI, the Hermes agent, …) that acts on `tasks.jsonl` itself
through this CLI. Which harness and model are chosen from the same config file;
all keys optional, unknown keys ignored:

```
llm_provider = hermes            # default harness (default: claude-cli)
llm_model    = gemma4:e4b        # default model within that provider
claude-cli_models = sonnet,opus,haiku   # override a provider's model list
hermes_models     = gemma4:e4b,gemma4:12b-mlx
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

**Local models:** an eval of local models behind Hermes
(`eval/llm/results-2026-07-02.md`) found `qwen3.6:35b-a3b` the only one that
reliably drives the CLI (all task types, zero corruption) — hence it's the
default Hermes model. It is not the overall default because it's slow (~2–4 min
per request vs seconds for Sonnet). Use it for offline/private work via
`llm_provider = hermes`, accepting the latency; re-run the harness
(`ruby eval/llm/harness.rb`) when a faster capable local model appears.

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

**Output.** Human-readable by default. Read commands and mutations accept
`--json`; shapes below. Mutations always print (or return in JSON) the full
new headline of every task they touched, so the agent can verify the result
without a follow-up read.

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
| `show <ref>` | `s` | ✅ | One task in full: rendered headline + body/notes + links. `--json` shape: `{id, state, priority, title, tags, contexts, scheduled, deadline, recur, closed, line, notes: [..], project, links: [{url, label, system}]}`. `notes` is the task's `body` split to lines (a child's body never leaks in — children are separate records); `project` is the nearest ancestor section. |
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
| `capture "text"` | `add`, `c` | ✅ | New INBOX item. Flags: `--due <date>`, `--scheduled <date>`, `--priority A\|B\|C`, `--tag t` (repeatable), `--context @x` (repeatable), `--state STATE`, `--project "Heading"`, `--recur <interval>`, plus `--dry-run`/`--json`. A capture with a date lands already-processed as TODO (override with `--state`); `--recur` implies a date (defaults to scheduling it today) and lands it repeating; `--project` files it under that top-level heading (default: Inbox). |

## Update (all take `<ref>`, all support `--dry-run`)

| Command | Alias/synonyms | Status | Description |
|---|---|---|---|
| `done <ref>` | `complete`, `close`, `d` | ✅ | Mark DONE + `closed` date. A recurring task (recur cookie on its date) rolls forward and stays open instead — output shows `↻ <title> → next <date>`. |
| `cancel <ref>` | `drop` | ✅ | Mark CANCELLED + `closed` date. |
| `state <ref> <STATE>` | `mv` | ✅ | Any state transition (INBOX/TODO/NEXT/WAITING/DONE/CANCELLED). Enforces: entering DONE/CANCELLED sets `closed`; leaving them clears it. Resolves refs across open *and* closed tasks so you can reopen a DONE item. |
| `due <ref> <date>` | `deadline`, `reschedule` | ✅ | Set/replace `deadline`. INBOX items promote to TODO. |
| `schedule <ref> <date>` | | ✅ | Set/replace `scheduled`. Same INBOX promotion. |
| `undate <ref>` | | ✅ | Remove `scheduled` and/or `deadline` (`--kind deadline\|scheduled` to pick one). |
| `priority <ref> <A\|B\|C\|none>` | `pri` | ✅ | Set or clear the `priority` field. |
| `retitle <ref> "new title"` | `rename` | ✅ | Replace the `title`; tags/priority/state untouched. |
| `tag <ref> +foo -bar @ctx -@old` | | ✅ | Add/remove tags and contexts in one call. `+t`/`@ctx` add, `-t`/`-@ctx` remove. |
| `note <ref> "text"` | | ✅ | Append a line to the task's `body`. |
| `move <ref> "Section"` | | ✅ | Relocate the whole subtree under another top-level section (e.g. out of `Inbox` into `Work`) by re-pointing its `parent`. Section matched case-insensitively (exact, then substring). |
| `recur <ref> <interval>` | `repeat`, `every` | ✅ | Attach/replace the `recur` cookie on the task's date. `<interval>`: a cookie (`.+1w`/`+2d`/`++1m`) or friendly form (`weekly`/`daily`/`monthly`/`yearly`/`2w`/`every 3 days`); `off`/`none` clears it. `--from schedule\|completion` picks `+`/`.+` for a bare interval (default `completion` → `.+`). `--on <date>` seeds a `deadline` when the task has no date yet (else it errors). `--dry-run`/`--json`. |
| `defer <ref>` | `snooze` | ✅ | Mark a task deferred (someday/maybe) by adding a semantic `defer` tag. Deferred tasks keep their state but drop out of `agenda`/`next`/`quadrants`/`inbox` and the default `list` until reactivated. Idempotent. |
| `activate <ref>` | `undefer`, `resume` | ✅ | Clear the `defer` tag, returning the task to the active views. Resolves deferred (open) tasks. |

## Lifecycle / meta

| Command | Alias | Status | Description |
|---|---|---|---|
| `archive` | `x` | ✅ | Sweep each DONE/CANCELLED subtree to `archive.jsonl` (root drops `parent`, gains `archived`). |
| `delete <ref> --force` | `rm` | 🚧 | Hard-remove a record (no archive). Refuses without `--force`; suggest `cancel` instead. |
| `undo` | | ✅ | Revert the last mutation via the on-disk journal (`Tasks::Journal`, under `$XDG_STATE_HOME/tasks/journal/`), shared with the TUI and across CLI runs. Refuses (exit 1) if `tasks.jsonl` changed out-of-band since that edit — resolve with `git diff` / `git checkout -- tasks.jsonl`. |
| `redo` | | ✅ | Replay the last undone mutation; same shared journal and conflict guard as `undo`. |
| `-p [--provider N] [--model N] "prompt"` | | ✅ | Natural-language request via a headless LLM agent (Claude CLI by default, or any configured harness). Leading `--provider`/`--model` override the config default for one run; see [LLM agent settings](#llm-agent-settings). |
| `config [--json]` | | ✅ | Print resolved file paths (tasks file, archive, config file), `urgent_days`, and the source of each (`TASKS_FILE env`, `TASKS_DIR env`, `TASKS_URGENT_DAYS env`, `config file`, `default`). |
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
