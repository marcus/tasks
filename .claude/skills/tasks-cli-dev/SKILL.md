---
name: tasks-cli-dev
description: How to add or change commands in the tasks CLI (bin/tasks + lib/tasks). Use when implementing a command from docs/cli-spec.md, adding flags, or changing mutation behavior. Covers the architecture, the mutation pattern, testing requirements, and file-integrity rules.
---

# Building tasks CLI commands

## Architecture

```
bin/tasks           thin dispatch + human output. No business logic.
lib/tasks/store.rb  Tasks::Store — the model layer. ALL writes go through it.
lib/tasks/dates.rb  Tasks::Dates.parse_when — fuzzy date parsing.
lib/tasks/check.rb  Tasks::Check.check — structural linter, the safety net.
lib/tui/            the TUI; consumes the same Store via compat shims.
docs/cli-spec.md    the interface contract. SPEC FIRST — update it before code.
test/               minitest; run with: ruby test/all.rb
```

`gtd.org` is an unstructured text file. The whole design exists to keep it
unmangled: `Store#with_history` snapshots before/after every mutation, runs
`Check.check` after the write, and **rolls back automatically** if the file
would no longer parse. Never bypass it with a raw `File.write`.

## Adding a command — the pattern

1. **Spec**: update the command's row in `docs/cli-spec.md` (flip 🚧→✅,
   adjust flags/behavior). The spec defines ref resolution, exit codes
   (0 ok / 1 error / 2 ref failure), `--json`, `--dry-run`, synonyms.
2. **Model**: add/extend a method on `Tasks::Store`. Mutations:
   - take an `Item` (from `store.items`) plus new values
   - re-read the file, guard against stale line numbers
     (`lines[i].match?(HEADLINE) && lines[i].include?(item.title)` → `false` if stale)
   - wrap the write in `with_history("label: #{item.title}")`
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

## Testing requirements (non-negotiable)

Tests live in `test/test_*.rb`, auto-loaded by `test/all.rb`. The shared
fixture is `FIXTURE_ORG` in `test/test_helper.rb`; `with_store` yields a
`Store` on a tempdir copy — never test against the real `gtd.org`.

Every mutating command needs at minimum:

- happy path (file content asserted, not just return value)
- ref-not-found and ref-ambiguous → exit code 2 behavior
- stale-line guard (see `test_complete_rejects_stale_line_numbers`)
- undo round-trip if the mutation records history
- **file integrity**: `assert Tasks::Check.check(org).ok?` after the mutation

For CLI-level behavior (arg parsing, exit codes, output), shell out via the
`run_cli` helper in `test/test_cli_mutations.rb`: it sandboxes with the
`TASKS_ORG`/`TASKS_ARCHIVE` env overrides (which `bin/tasks` honors precisely
for this), captures stdout/stderr/status with Open3, and takes a `content:`
kwarg when the fixture needs special shape (e.g. duplicate titles).

Manual verification: `TASKS_ORG=/tmp/sandbox.org bin/tasks <cmd> …`, then
`TASKS_ORG=/tmp/sandbox.org bin/tasks check`, and `diff` against the original.

## Gotchas learned the hard way

- `# frozen_string_literal: true` is on everywhere — use `+""` for buffers.
- The org file is UTF-8 with multibyte chars (·, ✨, —). Read/write with
  `encoding: "UTF-8"`; never assume ASCII.
- Line numbers from `store.items` go stale the moment the file changes;
  always re-verify before acting on them.
- `archive.org` may not exist yet — guard reads, and note that undoing an
  archive sweep may need to delete it.
- The TUI polls mtime every 250ms; CLI writes show up there automatically.
  Don't add locking — last-writer-wins plus the check rollback is the model.
- Don't leave scratch reasoning in comments ("use X? No — try Y…"). Comments
  state what the code does and why, in final form.
- If a mutation's logic overlaps an existing `_impl`, delegate rather than
  copy (see reschedule_impl → set_date_impl).
