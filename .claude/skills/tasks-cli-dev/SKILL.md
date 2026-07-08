---
name: tasks-cli-dev
description: How to add or change commands in the tasks CLI (bin/tasks + lib/tasks). Use when implementing a command from docs/cli-spec.md, adding flags, or changing mutation behavior. Covers the architecture, the mutation pattern, testing requirements, and file-integrity rules.
---

# Building tasks CLI commands

## Architecture

```
bin/tasks           thin dispatch + human output. No business logic.
lib/tasks/format.rb Tasks::Format — SOLE owner of the on-disk schema: KEY_ORDER,
                    VERSION, dump/parse. Shape only (no meaning); lenient parse.
lib/tasks/store.rb  Tasks::Store — the model layer. ALL writes go through it.
                    Loads records → locates by id → mutates hash fields → Format.dump.
lib/tasks/check.rb  Tasks::Check.check — structural linter (ids, DFS pre-order,
                    parents, states/dates/recur). The safety net.
lib/tasks/tree.rb   Tasks::Tree — nodes built from `parent` pointers (not indentation).
lib/tasks/config.rb Tasks::Config.resolve — where tasks.jsonl/archive.jsonl live
                    (TASKS_FILE/TASKS_ARCHIVE > TASKS_DIR > ~/.config/tasks/config
                    > repo root). CLI and TUI both resolve through it; tests
                    pin sandboxes with Config.for_dir.
lib/tasks/dates.rb  Tasks::Dates.parse_when — fuzzy date parsing.
lib/tui/            the TUI; consumes the same Store via compat shims.
docs/cli-spec.md    the interface contract. SPEC FIRST — update it before code.
test/               minitest; run with: ruby test/all.rb
```

`tasks.jsonl` is a JSONL store: one explicit JSON record per line, tree carried by
`parent` ids in DFS pre-order (no block-boundary inference — the old org
line-walker and its bug class are gone). Format owns the schema; Store owns
meaning; Check owns validation. The whole design keeps the file unmangled:
`Store#with_history` snapshots before/after every mutation, runs `Check.check`
after the write, and **rolls back automatically** if it would break an invariant.
Never bypass it with a raw `File.write` — write through `Format.dump`.

## Adding a command — the pattern

1. **Spec**: update the command's row in `docs/cli-spec.md` (flip 🚧→✅,
   adjust flags/behavior). The spec defines ref resolution, exit codes
   (0 ok / 1 error / 2 ref failure), `--json`, `--dry-run`, synonyms.
2. **Model**: add/extend a method on `Tasks::Store`. Mutations:
   - take an `Item` (from `store.items`) plus new values
   - re-read fresh records under the lock (`fresh_records`), then
     `locate(records, item)` — by id, falling back to line + title; `→ false` if
     the record is gone (the staleness guard)
   - mutate the record's hash fields, then `write_records` (`Format.dump`)
   - wrap the whole thing in `with_history("label: #{item.title}")`
   - `reload!` before returning
3. **Reuse the shared CLI helpers** in bin/tasks — do not reinvent them:
   - `resolve_ref(ref, include_done:)` — title-substring or `L<line>` → one
     item, exit 2 on no-match/ambiguous
   - `take_flags(args, "--dry-run", ...)` — flag extraction; unknown `--*`
     flags abort (add new flags to the known list, never let them fall
     through as positionals)
   - `report_touched(item.line, json:)` — post-mutation output. Identify
     tasks by **line**, never by title (duplicate titles are legal)
   - `item_json(item)` — the standard JSON shape
4. **Dispatch**: add the `when` clause + alias, update the usage banner.
5. **Output**: print the resulting headline(s) of every touched task.
   `--json` via `require "json"` at use site (keep startup fast).
6. **Propagate the docs — a command isn't done until agents can find it:**
   - `docs/cli-spec.md`: flip the row 🚧→✅, adjust flags/synonyms to match
     what you actually built
   - `.claude/skills/tasks-cli/SKILL.md`: add the command to the right
     section (Read/Mutate) with a one-line example — future agents only use
     what that skill teaches, and the CLI is their only writer
   - `AGENTS.md` (the `tasks -p` system prompt): add the command to the CLI
     bullet list
   - usage comment block at the top of `bin/tasks`

## Testing requirements (non-negotiable)

Tests live in `test/test_*.rb`, auto-loaded by `test/all.rb`. The shared fixture
is `FIXTURE_RECORDS` in `test/test_helper.rb` (ids exposed via the `FIX` map);
`FIXTURE` is its `Format.dump`. `with_store` yields a `Store` on a tempdir copy —
never test against the real `tasks.jsonl`. Assert on fields with the
`record_for(path, title:)` helper rather than matching file text with regexes.

Every mutating command needs at minimum:

- happy path (resulting record fields asserted, not just return value)
- ref-not-found and ref-ambiguous → exit code 2 behavior
- stale-line guard (see `test_complete_rejects_stale_line_numbers`)
- undo round-trip if the mutation records history
- **file integrity**: `assert Tasks::Check.check(org).ok?` after the mutation

For CLI-level behavior (arg parsing, exit codes, output), shell out via the
`run_cli` helper in `test/test_cli_mutations.rb`: it sandboxes with the
`TASKS_FILE`/`TASKS_ARCHIVE` env overrides (which `bin/tasks` honors precisely
for this), captures stdout/stderr/status with Open3, and takes a `content:`
kwarg when the fixture needs a special shape (e.g. duplicate titles).

Manual verification: `TASKS_FILE=/tmp/sandbox.jsonl bin/tasks <cmd> …`, then
`TASKS_FILE=/tmp/sandbox.jsonl bin/tasks check`, and `diff` against the original.

## Gotchas learned the hard way

- `# frozen_string_literal: true` is on everywhere — use `+""` for buffers.
- Records are UTF-8 with multibyte chars (·, ✨, —); Format writes non-ASCII
  unescaped so diffs stay readable. Read/write with `encoding: "UTF-8"`.
- A record's `id` is the durable handle; locate by id first. Line numbers from
  `store.items` go stale the moment the file changes — they're only a fallback.
- Subtrees are contiguous by the DFS pre-order invariant, so move/capture/sweep
  splice a record range rather than walk — don't re-derive structure by scanning.
- `archive.jsonl` may not exist yet — guard reads, and note that undoing an
  archive sweep may need to delete it.
- The TUI polls mtime every 250ms; CLI writes show up there automatically.
  Don't add locking — last-writer-wins plus the check rollback is the model.
- Don't leave scratch reasoning in comments ("use X? No — try Y…"). Comments
  state what the code does and why, in final form.
- If a mutation's logic overlaps an existing `_impl`, delegate rather than
  copy (see reschedule_impl → set_date_impl).
