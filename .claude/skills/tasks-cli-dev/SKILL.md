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
3. **Ref resolution** in bin/tasks: match `<ref>` case-insensitively against
   open item titles; `L<line>` targets an exact headline line. 0 matches or
   >1 matches → print candidates as `L<line>: <headline>`, exit 2.
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

For CLI-level behavior (arg parsing, exit codes, output), shell out in the
test: `system("ruby", "bin/tasks", "check", ...)` against a sandbox copy, or
better, extract the logic so it is unit-testable without a subprocess.

Manual verification: copy `gtd.org` + `AGENTS.md` to a scratch dir, run the
command there, run `bin/tasks check`, and `diff` against the original.

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
