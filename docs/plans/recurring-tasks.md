# Plan: Recurring tasks with flexible intervals

Status: implemented (2026-07-04)
Author: Marcus (drafted with Claude)
Date: 2026-07-04

Implementation note (2026-07-04): shipped as designed. Parsing/next-date math
live in `lib/tasks/recur.rb`; the Store gained `set_recur!` plus recurrence
branches in `complete_impl`/`set_state_impl` (DONE rolls forward, CANCELLED
still closes) and cookie-preservation in `set_date_impl`. CLI: `recur`
command, `capture --recur`, recurrence-aware `done` output, `list --recurring`,
and a `recur` JSON field. TUI: the `r` popup (`:recur` mode), a `↻` badge, and
in-place roll-forward on complete. Covered by `test/test_recur.rb` plus new
cases in `test_store.rb`, `test_cli_mutations.rb`, `test_app.rb`, `test_views.rb`,
and `test_shortcuts.rb`. One deviation from the draft below: the `- Did [date]`
completion log is **on** (not left as a maybe) — it's the only record that an
occurrence happened, since the task never enters `DONE`.

Realizes `docs/ideas.md` §3 ("Recurring tasks"). The one-line idea there —
*"Org-mode's native repeaters (`+1w`, `.+1m`, `++1d`); on `done`, roll the date
forward instead of closing"* — is the whole design. This doc turns it into a
storage format, a Store contract, a CLI surface, a TUI affordance, and a test
plan, in the shape the rest of this tool already uses.

## Why

Bills, weekly reviews, standups, quarterly check-ins, "water the plants" — a
large fraction of a personal GTD system is things that come back. Today the only
way to model them is to re-capture the task by hand every time it's done, or to
never close it. Both are bad: re-capture is friction, never-closing loses the
"I did it this week" signal.

The fix is well-trodden: **org-mode timestamp repeaters**. A repeater cookie
lives inside the date brackets, and completing the task rolls the date forward
by the interval and keeps the task open instead of marking it `DONE`.

## The one insight that shapes the design

**A recurring task is an ordinary task whose date stamp carries a repeater
cookie. Recurrence is not a new state, a new file section, or a new record type
— it is a modifier on the existing `SCHEDULED:`/`DEADLINE:` timestamp, exactly
where org-mode puts it.** This mirrors how `defer` was added: a semantic marker
that rides alongside the task's real state rather than replacing it (see
`Store::DEFER_TAG` and `Item#deferred?`). We do the analogous thing on the
timestamp instead of the tag list.

Consequences that fall out of this single choice:

- **Parsing** is a small extension to the existing `STAMP` regex — one more
  optional capture group. No new line types to recognize.
- **Storage** stays a plain, org-compatible `gtd.org` — the file still opens
  correctly in Emacs org-mode, which understands these cookies natively.
- **Only one mutation genuinely changes behavior**: `done`. Completing a task
  that has a repeater rolls the stamp forward and leaves the task open; every
  other command is either unaffected or gains recurrence as a pass-through
  detail (`show`/`list` display it, `capture` can set it).
- **The Store's guarantees are unchanged.** Advancing a repeater is just another
  line rewrite wrapped in `with_history`, validated by `Check.check`, rolled
  back on corruption, and undoable — identical to `reschedule!`.

## Storage format — org-mode repeaters

A repeater cookie sits inside the timestamp brackets, after the date (and after
the optional day name / time org may write):

```
** NEXT Pay rent :@home:
   DEADLINE: <2026-08-01 Sat +1m>

** TODO Weekly review :@computer:
   SCHEDULED: <2026-07-06 Sun .+1w>
```

### Cookie grammar

```
<prefix><n><unit>
  prefix : +    ++    .+
  n      : one or more digits
  unit   : d (day)  w (week)  m (month)  y (year)
```

The prefix selects **what the interval is measured from** — the three org
semantics, preserved verbatim so the file stays interoperable:

| Cookie | Name | On completion, next date = | Use when |
|---|---|---|---|
| `+1w` | fixed | stored date **+ interval** (one hop; may still be in the past if you were late) | anchored cadence you don't want to drift |
| `++1w` | catch-up | stored date + interval, **repeated until strictly in the future** | anchored cadence, but skip missed occurrences |
| `.+1w` | from-completion | **today + interval** | "N after I last did it" (gym, oil change) |

`.+` is the most intuitive default for personal habits and will be the default
prefix when the user gives a bare interval (see CLI/TUI below); `+`/`++` are
available for people who want calendar-anchored cadence.

### Interval math (unit semantics)

- `d` → `date + n`
- `w` → `date + 7*n`
- `m` → `date >> n` (Ruby `Date#>>`, calendar-month step)
- `y` → `date >> 12*n`

Month/year steps use `Date#>>`, which clamps overflowing days (e.g.
`2026-01-31 >> 1 => 2026-02-28`). This is org's behavior too and is the
least-surprising choice; note it in the code comment.

### What rolls

- If the stamp that carries the cookie is `DEADLINE`, the `DEADLINE` rolls; if
  `SCHEDULED`, the `SCHEDULED` rolls. A task may in principle carry a repeater on
  each; roll each independently (org does). In practice one is the norm.
- The **day-name and any time** org may have written inside the brackets are
  regenerated from the new date, not carried stale. We normalize on write:
  `<YYYY-MM-DD Dow COOKIE>`, dropping times (this tool has never stored times;
  don't start now).

## Parsing changes (`lib/tasks/store.rb`)

Two edits, both local:

1. **`STAMP` regex** gains an optional trailing repeater group. Today:

   ```ruby
   STAMP = /(SCHEDULED|DEADLINE):\s*<(\d{4}-\d{2}-\d{2})/
   ```

   becomes (capture the date, then anything up to a repeater, then the cookie):

   ```ruby
   STAMP = %r{
     (SCHEDULED|DEADLINE):\s*<
     (\d{4}-\d{2}-\d{2})           # date
     (?:[^>]*?\s([.+]{1,2}\d+[dwmy]))?   # optional repeater cookie
     [^>]*>
   }x
   ```

   The `[^>]*` swallows an intervening day name / time so the cookie is found
   regardless of what org wrote between the date and it.

2. **`Item`** gains a `recur` field (the raw cookie string, e.g. `".+1w"`, or
   `nil`), populated in `parse`. Add a predicate to match the `defer` pattern:

   ```ruby
   Item = Struct.new(..., :recur, keyword_init: true) do
     def recurring? = !recur.nil?
   end
   ```

   `parse` records which stamp the cookie was on if we need it (usually derivable:
   whichever of `scheduled`/`deadline` the cookie sits beside).

## New module: `lib/tasks/recur.rb`

Recurrence input parsing and next-date computation, kept separate from
`Tasks::Dates` (which parses one-shot dates) the way `Check`/`Quadrants` are
their own files. `module_function`, no state.

```ruby
module Tasks
  module Recur
    # Normalize a user interval string to a canonical org cookie, or nil.
    #   ".+1w" "+2d" "++1m"      -> passthrough (validated)
    #   "1w" "2d" "3 months"     -> ".+…"  (bare interval defaults to from-completion)
    #   "daily/weekly/monthly/yearly" -> ".+1d/.+1w/.+1m/.+1y"
    #   "every 2 weeks"          -> ".+2w"
    #   "off" / "none"           -> :off   (sentinel: remove recurrence)
    def parse_interval(str) ; end          # -> cookie String | :off | nil

    # Given a cookie and the stamp's current date, return the next date.
    #   next_date(".+1w", from: date, today: Date.today)
    def next_date(cookie, from:, today: Date.today) ; end   # -> Date

    def cookie?(str) ; end                 # -> Bool (is this already a cookie)
  end
end
```

`parse_interval` returning `nil` on garbage lets the CLI/TUI reuse the same
"can't parse" flash/exit-2 path `Dates.parse_when` already uses.

## Store mutations (`lib/tasks/store.rb`)

Follow the established `with_history` + stale-line-guard + `reload!` pattern
verbatim (see `reschedule_impl`, `set_deferred!`).

### 1. `set_recur!(item, cookie)` — attach / change / remove a repeater

```ruby
def set_recur!(item, cookie)   # cookie String, or :off to remove
  label = cookie == :off ? "recur off: #{item.title}" : "recur #{cookie}: #{item.title}"
  with_history(label) { set_recur_impl(item, cookie) }
end
```

`set_recur_impl`:

- Re-read the file; stale-guard the headline line
  (`lines[i].match?(HEADLINE) && lines[i].include?(item.title)`).
- Find the item's `SCHEDULED`/`DEADLINE` line **within its block** (reuse
  `block_end_index`). Prefer `DEADLINE` if both exist, matching
  `reschedule_impl`'s existing precedence.
- **If the task has no date stamp**, recurrence is meaningless — return `false`
  so the caller can tell the user to schedule it first. (Alternatively the CLI
  can seed one; keep the Store honest and let the CLI decide — see below.)
- Rewrite the bracket: insert/replace/remove the cookie, regenerating the day
  name from the existing date. Never touch the date itself here.
- `File.write`, `reload!`, `true`.

### 2. `complete!` branches on recurrence

This is the only behavioral change. `complete_impl` today sets `DONE`, strips
the `defer` tag, and stamps `CLOSED:`. Add a branch at the top:

```ruby
def complete_impl(item)
  return advance_recurrence_impl(item) if item.recurring?
  # …unchanged non-recurring path…
end
```

`advance_recurrence_impl`:

- Re-read + stale-guard as usual.
- For each stamp in the block carrying a cookie, compute
  `Recur.next_date(cookie, from: current_date, today: Date.today)` and rewrite
  that stamp's date (keeping the cookie, regenerating the day name).
- **Leave the state as-is** (a recurring `NEXT` stays `NEXT`; a `TODO` stays
  `TODO`). Do **not** add `CLOSED:`. Do **not** strip anything.
- Optionally append a lightweight completion log line under the block so history
  isn't lost — a single `   - Did [YYYY-MM-DD]` note (cheap, greppable,
  org-comment-ish). Keep it opt-out-able; default on. (Org itself uses
  `:LAST_REPEAT:` / a `- State "DONE"` log; a plain note is simpler and matches
  how `note` already appends. Decide during implementation; the note is not
  load-bearing.)
- `reload!`, `true`.

Because the task never enters `DONE`, it is never swept by `archive` — correct:
a recurring task has no terminal state. To actually stop it, remove the repeater
(`recur off`) and then `done`, or `cancel`.

Undo already works for free: `with_history` snapshots the whole file before the
roll-forward, so `undo` (TUI) restores the pre-completion date and state.

## CLI surface (`bin/tasks` + `docs/cli-spec.md`)

Spec-first: add these rows to `docs/cli-spec.md` before coding, and flip
🚧→✅ as each lands. Reuse the shared helpers (`resolve_ref`, `take_flags`,
`report_touched`, `item_json`) — no bespoke arg handling.

### New command

| Command | Alias/synonyms | Description |
|---|---|---|
| `recur <ref> <interval>` | `repeat`, `every` | Attach/replace a repeater on the task's date stamp. `<interval>`: `.+1w`/`+2d`/`++1m` cookies, or friendly `weekly`/`daily`/`monthly`/`yearly`/`2w`/`every 3 days`. `recur <ref> off` removes it. `--from schedule\|completion` picks `+`/`.+` semantics for a bare interval (default `completion` → `.+`). If the task has no date, error unless `--on <date>` is given to seed one. `--dry-run`/`--json`. |

### Extended commands

- **`capture`** gains `--recur <interval>` (requires a `--scheduled`/`--due`,
  or seeds `--scheduled today` if only `--recur` is given). A captured recurring
  task lands as `TODO` (dated ⇒ processed, existing rule) with the cookie set.
- **`done`/`complete`** needs **no new flag**: it already resolves the ref and
  calls `complete!`, which now auto-rolls a recurring task. Its output must
  reflect the branch — instead of "✓ completed", print
  `↻ <title> → next <date> (<Dow>)` so the agent/human sees it recurred rather
  than closed. `report_touched` prints the resulting headline; add the
  next-date line for the recurrence case.
- **`show`** JSON gains `"recur": ".+1w"` (or `null`); the human view shows a
  `↻ every 1w (from completion)` line when present.
- **List-family `--json`** (`list`/`agenda`/`next`) gains `"recur"` per item.
- **`list --recurring/-R`** (optional, cheap): filter to tasks with a repeater —
  a "what's on a cadence" review, symmetric with `--deferred`.

### Interaction with dating commands

`due`/`schedule`/`reschedule`/`undate` operate on the date and must **preserve
an existing cookie** (re-attach it after rewriting the date). `undate` removing
the only stamp also removes recurrence (no date, no repeater) — mention in its
spec row. These are small edits to `set_date_impl`/`undate_impl` to carry the
cookie through; add a test for each.

## TUI (`lib/tui/`)

Mirror the `defer` wiring (`z` key, `⏸` badge) and the reschedule popup
(`:date` mode). Recurrence gets an input popup because it takes an interval
string, so it follows the `:date` popup pattern more than the one-key `z` toggle.

### New mode + key

- **`r`** — open a small "recur" input popup over the selected task (a `:recur`
  mode alongside `:date`), pre-filled with the current cookie if any. Type an
  interval (`weekly`, `.+1w`, `2d`, `off`), `enter` applies via
  `store.set_recur!`, `esc` cancels. Verify `r` is unbound at implementation
  time against `lib/tui/shortcuts.rb`; fall back to another free key if not.
  Add the `Shortcuts::LIST` entry (`desc: "set / clear recurrence"`) so it shows
  in the `?` help modal, exactly like the `z`/`Z` entries.
- Reuse the `date_popup`/`date_key` machinery: a near-copy `recur_popup` +
  `recur_key` (or generalize the existing input popup to take a label +
  submit-handler; the two are structurally identical — prefer generalizing if
  it's a clean extraction, else copy, matching how the codebase already has a
  focused `date_popup`). Parse with `Recur.parse_interval`; on `nil`, set the
  same inline `@…_error` the date popup uses.

### Completion behavior in the TUI

The complete key (`d`) already calls `store.complete!`. With the Store branch in
place it auto-rolls; update the flash to the recurrence case:
`↻ deferred to <date>` vs the current "✓ completed", chosen on
`item.recurring?`. Keep the cursor on the task (it stays in the list) via the
existing `reselect(item.line)` — unlike a normal complete which removes it.

### Badge

Add a `↻` trailing badge in the list views next to the `⏸` defer badge
(`lib/tui/views.rb#badge`) so recurring tasks are visible at a glance:

```ruby
def badge(item)
  m = +""
  m << A.dim(" ↻") if item.recurring?
  m << A.dim(" ⏸") if item.deferred?
  m
end
```

## Phasing

Each phase ships independently and leaves the tool fully working.

1. **Parse + model.** Extend `STAMP`, add `Item#recur`/`recurring?`, add
   `lib/tasks/recur.rb` with `parse_interval`/`next_date`. Pure parsing; no
   mutations yet. Tests for the module and the parse. **No user-visible change**
   beyond `show --json` now surfacing `recur`.
2. **`done` rolls forward.** Branch `complete_impl` → `advance_recurrence_impl`.
   Now recurring tasks recur when completed (CLI `done` and TUI `d` both, since
   both call `complete!`). Update `done` output + TUI flash. This is the
   headline feature.
3. **Set/clear recurrence.** `Store#set_recur!`, the `recur` CLI command,
   `capture --recur`, and cookie-preservation in `due`/`schedule`/`reschedule`/
   `undate`. Now recurrence is fully manageable from the CLI.
4. **TUI affordance.** `r` popup + `:recur` mode, the `↻` badge, help entry, and
   `list --recurring`. Now it's fully manageable without touching the file.

Docs propagate with each phase per the `tasks-cli-dev` skill: `docs/cli-spec.md`
rows, `.claude/skills/tasks-cli/SKILL.md` (add `recur`, note `done`'s new
behavior), `AGENTS.md` CLI bullets, and the `bin/tasks` usage banner.

## Testing (non-negotiable — per repo rules)

New fixture rows in `test/test_helper.rb` (`FIXTURE_ORG`): at least one task with
a `DEADLINE … +1m`, one with `SCHEDULED … .+1w`, and one plain dated task (to
prove non-recurring `done` is unchanged). Every mutating test asserts
`Tasks::Check.check(org).ok?` afterward.

**`Recur` unit tests** (no file):
- `parse_interval`: every cookie form, every friendly form (`weekly`, `2w`,
  `every 3 days`), `off` → `:off`, garbage → `nil`, bare interval defaults to
  `.+`.
- `next_date` for each prefix × each unit, including:
  - `+` = single hop (may land in the past);
  - `++` = loops to strictly-future;
  - `.+` = today-anchored;
  - month/year clamp: `2026-01-31 +1m => 2026-02-28`;
  - leap-year `+1y` from `2028-02-29`.

**Store tests** (`with_store`, file content asserted):
- `set_recur!` attaches a cookie, preserves the date, regenerates the day name;
  `:off` removes it; both `SCHEDULED`- and `DEADLINE`-anchored.
- `set_recur!` on an undated task returns `false` (no crash, no write).
- Stale-line guard (mirror `test_complete_rejects_stale_line_numbers`).
- **`complete!` on a recurring task**: date rolls forward by the cookie, state
  unchanged, **no `CLOSED:` added**, task still `open?`, file check clean —
  cases for `+`, `++` (overdue → future), `.+`.
- `complete!` on a **non-recurring** task: unchanged (regression).
- `due`/`schedule`/`reschedule`/`undate` preserve (and `undate`-to-empty
  removes) the cookie.
- Undo round-trip: `complete!` then `undo!` restores the pre-roll date + state.

**CLI tests** (`run_cli`, sandboxed via `TASKS_ORG`/`TASKS_ARCHIVE`):
- `recur <ref> weekly` → cookie set, headline echoed, `--json` includes `recur`.
- `recur <ref> off` → cookie removed.
- `recur` on undated task without `--on` → exit 1 with a clear message;
  with `--on <date>` → seeds date + cookie.
- `done <recurring-ref>` prints the `↻ … → next <date>` line and leaves the task
  open (assert it's still listed).
- `capture --recur weekly --scheduled today` → `TODO` with the cookie.
- ref-not-found → exit 2, ref-ambiguous → exit 2 (candidate list).
- `--dry-run` on `recur` writes nothing.

**TUI**: exercise the parse+apply path through the Store headlessly (as the
existing TUI-adjacent tests do — no event loop): `Recur.parse_interval` +
`set_recur!`, and the `complete!` roll-forward, asserting the resulting file.
The popup key-handling (`recur_key` buffer edits) can be unit-tested on the
buffer if the existing suite tests `date_key`; otherwise keep it thin and lean
on the Store-level coverage.

## Risks & open questions

- **`+` landing in the past.** The fixed `+` prefix does exactly one hop, so
  completing a long-overdue `+1w` task yields a still-past date (this is org's
  documented behavior). Mitigated by defaulting bare intervals to `.+`
  (from-completion, always future) and offering `++` for anchored-but-catch-up.
  Document the three prefixes wherever the user picks one (`recur --help`, the
  TUI popup hint).
- **Completion history is softer.** A recurring task never enters `DONE`, so it
  won't appear in `list --done` or get archived. The optional `- Did [date]`
  log line is the compromise; if a user wants full per-occurrence history,
  that's a larger feature (a completions log) and out of scope here. Flag it in
  `docs/ideas.md` as a follow-on.
- **Both stamps repeating.** Supported (roll each independently) but rare and a
  little surprising; the common case is one repeater. Keep the code general but
  don't add UI to manage two cookies — the CLI/TUI set the cookie on the
  precedence stamp (`DEADLINE` first), matching `reschedule_impl`.
- **Time-of-day in timestamps.** This tool has never stored times; we normalize
  them away on any rewrite. If org-written times ever need preserving, that's a
  separate change to the whole timestamp model, not this feature.
- **Key binding for the TUI popup.** `r` is the intended key but must be
  confirmed free against `lib/tui/shortcuts.rb` at implementation time; the `z`
  (defer) precedent shows the wiring, and any free key works.
- **`Date#>>` clamping vs "same day next month".** `Jan 31 >> 1 = Feb 28` means
  a monthly task seeded on the 31st drifts to the 28th and stays there. This is
  org's behavior and the sanest default; noted so it's a decision, not a bug.

Follow-on ideas this unlocks (park in `docs/ideas.md`): a completions log for
full recurrence history, and `WAITING` aging (§4) which wants the same
"last-touched" signal the optional `- Did [date]` note provides.
