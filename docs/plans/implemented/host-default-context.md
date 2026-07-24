# Automatically add a context based on the current host

Status: implemented

## Context

Marcus normally keeps the TUI filtered to one location context: `@home` on
home machines and `@work` on work machines. Today that context is supplied by
agent instructions / task-set memory. Agents occasionally omit it, and the new
task then appears to vanish because it does not match the persistent filter.

This needs to be an application invariant, not prompt advice. Every normal task
creation path already converges on `Tasks::Application#create_task`:

- `tasks capture` (including list-agent captures)
- `POST /api/v1/tasks`
- the TUI's project capture form

The Store remains configuration-agnostic. Imports, restores, migrations,
projects/sections, edits, and recurrence advancement are not task creation and
must not acquire a context.

## Proposed contract

Configure host-to-context mappings in `~/.config/tasks/config`:

```ini
host_context.marcus-home.local = @home
host_context.work-mbp.local = @work
```

Matching is case-insensitive. The resolver tries the full value from
`Socket.gethostname` first, then its first DNS label, so this also works:

```ini
host_context.marcus-home = @home
host_context.work-mbp = @work
```

An unmatched host has no automatic context and retains today's behavior. Only
one automatic context is resolved for a host. Values may be written as `home`
or `@home` and resolve to the canonical `@home`; an empty value or bare `@` is
ignored with the same safe-fallback behavior as other optional config values.
If both the full and short hostname are configured, the full match wins.

`tasks config` must make the behavior debuggable:

```text
hostname:     marcus-home.local
host_context: @home  (host_context.marcus-home.local)
```

The JSON form adds `hostname`, `host_context`, and
`host_context_source`. It may also include the parsed `host_contexts` mapping,
consistent with the existing link and prompt namespaces.

### Creation semantics

When a host context resolves, add it to every newly created task:

- Preserve all explicitly supplied contexts and ordinary tags.
- Put contexts before ordinary tags, following the existing storage convention.
- Do not add a duplicate if the request already includes the host context.
- Apply the default to INBOX and TODO tasks, nested tasks, project-filed tasks,
  dated tasks, recurring tasks, and tasks with initial notes alike.
- Apply it in dry-run output as well as real writes.

The automatic context is deliberately additive. For example, capturing with
`--context @computer` on the home host produces both `@home` and `@computer`;
otherwise a well-intentioned functional context could still leave the task
invisible under the persistent `@home` filter.

Provide an explicit per-create escape hatch for tasks that should not inherit
the current host:

```sh
tasks capture "Prepare for office visit" --context @work --no-host-context
tasks capture "Context-free inbox note" --no-host-context
```

For HTTP, `POST /api/v1/tasks` accepts `apply_host_context: false`; omission or
`true` uses the configured host context. The TUI's compact project-capture form
always uses the default; users can edit contexts afterward. A future richer
capture form could expose the switch without changing the shared semantics.

## Design

Resolve host policy once with the rest of `Tasks::Config::Paths`, then enforce
it immediately before the Store transaction.

1. `Tasks::Config` parses `host_context.<hostname>` rows into an immutable map
   and resolves the current host context. Hostname lookup is injectable for
   deterministic tests; production defaults to `Socket.gethostname`.
2. `Tasks::CreateTask` carries an immutable `apply_host_context` boolean
   (default `true`). This records the caller's explicit opt-out without putting
   host configuration into the command.
3. A small shared creation-policy helper accepts a `CreateTask` and the resolved
   host context, returning an equivalent command with the context normalized,
   de-duplicated, and ordered. Both `Application#create_task` and CLI dry-run
   use this helper, avoiding adapter-specific behavior.
4. `Tasks::Application` is initialized with the resolved host context and
   applies the policy before `Store#create_task!`. The Store continues to
   validate and persist a complete, transport-neutral `CreateTask`; it never
   reads hostname or configuration.

The policy belongs at the Application boundary rather than in `bin/tasks`,
`lib/tasks/api/app.rb`, or `lib/tui/app.rb`: those adapters must not be able to
drift, and agent correctness must not depend on which surface the agent used.

## Changes

### Configuration

- `lib/tasks/config.rb`
  - Add `host_contexts`, `hostname`, `host_context`, and
    `host_context_source` to `Paths`.
  - Parse `host_context.<hostname>` keys. Preserve dots and hyphens in the
    selector; reject whitespace or an empty selector.
  - Resolve full-host before short-host matches, case-insensitively.
  - Keep `Config.for_dir` hermetic with an empty map and no resolved context.
- `bin/tasks`
  - Show the resolved policy in human and JSON `tasks config` output.
- `docs/cli-spec.md` and `README.md`
  - Document syntax, matching, additive behavior, opt-out, and diagnostics.

No environment-variable override is proposed initially. Host mappings are
already explicit, and adding a second identity/precedence mechanism would make
it harder to answer why a context was selected. If an override is later needed
for containers or ephemeral CI hosts, it can be added with a documented
precedence rule.

### Shared creation policy

- `lib/tasks/create_task.rb`
  - Add `apply_host_context:` (boolean, default `true`) to the immutable command.
  - Reject non-boolean values through the existing create validation path.
- Add a focused helper under `lib/tasks/` for applying the configured host
  context without mutating the caller's command or tag array.
- `lib/tasks/application.rb`
  - Accept `host_context:` at initialization.
  - Apply the helper inside `create_task`, before calling the Store.
  - Expose the same prepared command to the CLI preview path (either a narrow
    `prepare_create_task` method or the helper directly; prefer the smaller
    public surface during implementation).

### Adapters

- `bin/tasks`
  - Add `--no-host-context` to capture parsing and usage.
  - Pass `apply_host_context: false` in the typed command when present.
  - Use the shared policy for `--dry-run`, so preview and persisted output are
    identical.
  - Initialize `Application` with `PATHS.host_context`.
- `lib/tasks/api/app.rb`
  - Add `apply_host_context` to the create-only allowlist and validate it as a
    boolean.
  - Pass it into `CreateTask`; do not add contexts in the Rack adapter.
  - Initialize `Application` with the resolved host context in `.build`.
- `lib/tui/app.rb`
  - Initialize `Application` with the resolved host context. The existing
    project capture call requires no surface-specific context logic.
- `docs/api/openapi.yaml`
  - Add the create-only `apply_host_context` boolean, default `true`, with an
    example showing the explicit opt-out.

### Agent-facing guidance

- `.agents/skills/tasks-cli/SKILL.md`, `TASK_AGENT.md`, and the usage block in
  `bin/tasks`
  - Teach `--no-host-context`.
  - Replace the old “omit/pass a different context to override the default”
    wording for host defaults: another context is additive; explicit
    `--no-host-context` is required to suppress the host context.
  - Keep task-set memory rules for narrower semantic defaults (garden tasks,
    filing rules, and so on). Host context is mechanical configuration and no
    longer depends on the agent remembering it.

## Verification

### Config tests (`test/test_config.rb`)

- Parses several `host_context.*` rows.
- Full hostname match and case-insensitive match.
- Short first-label fallback.
- Full match wins when full and short rows both exist.
- Unmatched host, malformed selector, blank value, and bare `@` resolve to nil.
- Context normalization accepts `home` and emits `@home`.
- `Config.for_dir` does not inspect the real hostname or config.
- `tasks config` human and JSON output report the detected host, resolved
  context, and source.

### Application / Store boundary tests

- Application creation with `@home` configured adds `@home`.
- Explicit `@computer` becomes `@home` + `@computer`.
- Existing `@home` is not duplicated.
- `apply_host_context: false` preserves explicit contexts and permits none.
- No configured host context is byte-for-byte equivalent to current behavior.
- Store-level `create_task!` remains configuration-free.
- Validation, undo round-trip, stale placement, and `Tasks::Check` integrity
  continue to pass.

### CLI tests (`test/test_cli_mutations.rb`)

- Capture inherits the resolved host context.
- Explicit contexts are additive.
- `--no-host-context` suppresses it.
- `--dry-run` shows the same effective tags and writes nothing.
- `--json` returns the effective context.
- Unknown/missing flags retain current exit behavior.

### API tests (`test/api/test_app.rb` and OpenAPI parity)

- Omitted / true `apply_host_context` inherits the configured context.
- False suppresses it.
- Non-boolean input and unknown fields return 422.
- Explicit contexts remain additive and de-duplicated.
- Response, ETag, revisions, undo/history behavior, and OpenAPI examples agree.

### TUI regression

- Project capture on a configured host creates a task carrying the host context
  and it remains visible under that context filter after the write refresh.

Run:

```sh
ruby test/all.rb
bundle exec ruby test/api/all.rb
```

Then smoke-test two hostname mappings with `tasks config`, `tasks capture
--dry-run`, CLI capture, TUI project capture, and API create.

## Confirmed product decision

This plan makes the host context additive and requires an explicit opt-out.
That is the safest behavior for the stated problem: an agent adding a secondary
context such as `@computer` cannot accidentally make the task invisible.

Marcus confirmed that multiple contexts are expected and the host context
should be additive.
