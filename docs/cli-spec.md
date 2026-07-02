# tasks CLI — agent interface specification

The `tasks` CLI is the API for `gtd.org`. Humans use it too, but the primary
audience is LLM agents: every mutation an agent would otherwise make by
editing the file should have a command, so direct file edits become the rare
exception rather than the norm. Commands go through the shared model layer
(`lib/tasks/store.rb`), which enforces conventions (e.g. dating an INBOX item
promotes it to TODO) and validates the file after every write.

Status legend: ✅ implemented · 🚧 planned (spec is authoritative for behavior
when it lands; agents should fall back to direct edits + `tasks check` for 🚧).

## Global conventions

**Invocation.** `bin/tasks <command> [args] [flags]` from the repo root (or the
`tasks` alias). Every command has a short alias. Synonyms are accepted where
an agent would plausibly reach for them (`done`/`complete`/`close` are the
same command); the canonical name is listed first. Unknown `--flags` are an
error (exit 1), never silently treated as positional args.

**File locations.** The task files don't have to live in this repo — the code
and your data are separable (so the project can be shared without the tasks).
Both the CLI and the TUI resolve `gtd.org`/`archive.org` through
`lib/tasks/config.rb`, highest precedence first:

1. `TASKS_ORG` / `TASKS_ARCHIVE` env vars (per-file; used by the test suite
   and for safe manual experiments).
2. `TASKS_DIR` env var — a directory containing `gtd.org` and `archive.org`.
3. Config file `~/.config/tasks/config` (or `$XDG_CONFIG_HOME/tasks/config`),
   `key = value` lines: `dir = ~/tasks`, or per-file `org = …` / `archive = …`.
   `~` expands; `#` comments and blank lines ignored.
4. Default: the repo root (current behavior).

`tasks config` prints the resolved paths and where each came from.

**Task refs.** Mutations take a `<ref>` — a case-insensitive substring of the
task title. Resolution rules:

- Exactly one open task matches → act on it.
- Zero matches → exit 2, message `no match: <ref>`.
- Multiple matches → exit 2, listing each candidate as `L<line>: <headline>`.
  The agent retries with a longer substring or an exact `L<line>` ref.
- `L<line>` (e.g. `L42`) targets the task whose headline is on that file line —
  precise, but only valid until the file changes. Prefer titles.
- By default refs match **open** tasks only; `--include-done` widens.

**Dates.** Anywhere a date is accepted: `2026-07-15`, `07-15`, `7/15`,
`fri`/`friday`, `today`, `tomorrow`, `+3` (days from today). Same parser as
the TUI (`lib/tasks/dates.rb`). Bare month-day in the past rolls forward a year.

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
| `list [filters]` | `l` | ✅ | All tasks grouped by state. Filters compose: `@context`, `+tag`, `/text` or bare word, `-A/-B/-C`, scope `--open/-o` (default) `--done/-d` `--archived/-x` `--all/-a`. `--json` |
| `agenda` | `a` | ✅ | Dated items, soonest first. `--json` |
| `next` | `n` | ✅ | NEXT actions by context. `--json` |
| `quadrants` | `q` | ✅ | Covey 2×2 by `important`/`urgent` tags. `--json` |
| `inbox` | `i` | ✅ | Unprocessed INBOX items. `--json` |
| `show <ref>` | `s` | ✅ | One task in full: headline fields + body/notes. `--json` shape: `{state, priority, title, tags, contexts, scheduled, deadline, closed, line, notes: [..]}` |
| `check [--json]` | `k` | ✅ | Validate gtd.org structure. Exit 1 if errors. Run after any direct file edit. |

JSON list shape (`--json` on list/agenda/next/quadrants/inbox) — a flat array,
already sorted the way the text view sorts:
`[{"state": "NEXT", "priority": "A", "title": "…", "tags": [..], "contexts": [..], "scheduled": null, "deadline": "2026-07-02", "line": 17, "source": "org", "headline": "** NEXT …"}]`
`quadrants --json` adds `"quadrant": "Q1".."Q4"` per item. Empty result → `[]`.

## Create

| Command | Alias | Status | Description |
|---|---|---|---|
| `capture "text"` | `add`, `c` | ✅ | New INBOX item. Flags: `--due <date>`, `--scheduled <date>`, `--priority A\|B\|C`, `--tag t` (repeatable), `--context @x` (repeatable), `--state STATE`, `--project "Heading"`, plus `--dry-run`/`--json`. A capture with a date lands already-processed as TODO (override with `--state`); `--project` files it under that top-level heading (default: Inbox). |

## Update (all take `<ref>`, all support `--dry-run`)

| Command | Alias/synonyms | Status | Description |
|---|---|---|---|
| `done <ref>` | `complete`, `close`, `d` | ✅ | Mark DONE + `CLOSED:` stamp. |
| `cancel <ref>` | `drop` | ✅ | Mark CANCELLED + `CLOSED:` stamp. |
| `state <ref> <STATE>` | `mv` | ✅ | Any state transition (INBOX/TODO/NEXT/WAITING/DONE/CANCELLED). Enforces: entering DONE/CANCELLED adds `CLOSED:`; leaving them removes it. Resolves refs across open *and* closed tasks so you can reopen a DONE item. |
| `due <ref> <date>` | `deadline`, `reschedule` | ✅ | Set/replace DEADLINE. INBOX items promote to TODO. |
| `schedule <ref> <date>` | | ✅ | Set/replace SCHEDULED. Same INBOX promotion. |
| `undate <ref>` | | ✅ | Remove SCHEDULED and/or DEADLINE (`--kind deadline\|scheduled` to pick one). |
| `priority <ref> <A\|B\|C\|none>` | `pri` | ✅ | Set or clear the priority cookie. |
| `retitle <ref> "new title"` | `rename` | ✅ | Replace the headline title; tags/priority/state untouched. |
| `tag <ref> +foo -bar @ctx -@old` | | ✅ | Add/remove tags and contexts in one call. `+t`/`@ctx` add, `-t`/`-@ctx` remove. |
| `note <ref> "text"` | | ✅ | Append a body line under the task. |
| `move <ref> "Section"` | | ✅ | Relocate the whole block under another top-level heading (e.g. out of `* Inbox` into `* Work`). Section matched case-insensitively (exact, then substring). |

## Lifecycle / meta

| Command | Alias | Status | Description |
|---|---|---|---|
| `archive` | `x` | ✅ | Sweep DONE/CANCELLED blocks to archive.org. |
| `delete <ref> --force` | `rm` | 🚧 | Hard-remove a block (no archive). Refuses without `--force`; suggest `cancel` instead. |
| `undo` | | 🚧 | Revert the last CLI mutation (file-backed journal, shared with the TUI's in-memory one is out of scope). Until then: `git diff` / `git checkout -- gtd.org`. |
| `-p "prompt"` | | ✅ | Natural-language request via headless Claude. |
| `config [--json]` | | ✅ | Print resolved file paths (org, archive, config file) and the source of each (`TASKS_ORG env`, `TASKS_DIR env`, `config file`, `default`). |
| `help` | `-h`, `--help` | ✅ | Grouped command reference. Also printed (to stderr, exit 1) on an unknown/absent command. |

Ideas beyond this spec live in `docs/ideas.md`.

## Design rules for new commands

1. **Spec first**: add/adjust the row here before implementing.
2. Thin dispatch in `bin/tasks`; logic in `lib/tasks/` (usually a `Store` method).
3. Mutations go through `Store#with_history` — never `File.write` directly.
   That buys the post-write `check` rollback and (future) undo journal.
4. Accept synonyms liberally, print the canonical name in output.
5. Every mutation's output includes the resulting headline(s).
6. Tests required: happy path, ref-not-found, ref-ambiguous, and
   `Tasks::Check.check` clean after every mutating test (the test helper's
   fixture makes this a one-liner).
