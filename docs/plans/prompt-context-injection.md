# Plan: configurable agent prompt context

Status: implemented (2026-07-15)
Date: 2026-07-15

## Goal

Inject a short, labeled "current environment" block into every agent system
prompt (`tasks -p` and the TUI queue), starting with **datetime** and
**hostname**. Users turn fields on/off with dotted config keys. Adding a future
fact (weather, etc.) is one registry entry + optional default-off.

## Why dotted on/off (not an ordered list)

Matches existing config namespaces (`link.*`, `system.*`, `color.*`).
Enable/disable is the common case; presentation order stays registry-fixed for
a small labeled fact list. Use the `prompt.` prefix (not `context.`) so it does
not collide with GTD `@contexts`.

```
# ~/.config/tasks/config — all optional; defaults below apply when a key is absent
prompt.datetime = on     # default: on
prompt.hostname = on     # default: on
# prompt.weather = on    # future provider; default: off until registered as default-on
```

Truthy values: `on` / `true` / `1` (case-insensitive). Falsy: `off` / `false` /
`0`. Unknown `prompt.*` names are ignored at resolve time (forward
compatibility). Invalid toggle values fall through to the registry default.

## Design

### Registry of providers

[`lib/tasks/prompt_facts.rb`](../../lib/tasks/prompt_facts.rb) — small,
testable registry:

- **datetime** (default on): local `2026-07-15 Wed 08:41 PDT`
  (`%Y-%m-%d %a %H:%M %Z`).
- **hostname** (default on): `Socket.gethostname` behind an injectable callable.
- **Failure policy**: any exception or blank/nil → omit that line silently and
  continue.

Rendering produces either `nil` or:

```text
Current environment:
- datetime: 2026-07-15 Wed 08:41 PDT
- hostname: marcus-mbp.local
```

### Config wiring

[`lib/tasks/config.rb`](../../lib/tasks/config.rb) parses `prompt.<name>` into
a raw map, then `PromptFacts.resolve` merges registry defaults onto
`Paths#prompt_facts`. `tasks config` / `--json` report the effective map.

### Injection point

[`lib/tasks/agent_context.rb`](../../lib/tasks/agent_context.rb) inserts the
facts block immediately after `TASK_AGENT.md` and before the file-locations
block. Both `tasks -p` and the TUI queue already build through
`AgentContext.build`. Coding-agent instructions stay in `AGENTS.md` and are not
injected here.

Order:

1. TASK_AGENT.md
2. Current environment (omitted entirely if empty)
3. File locations
4. Memory policy pointer
5. Memory sidecar (if present)

## Out of scope (v1)

- Custom format strings per fact
- Ordered-list config / reordering
- Remote/async providers (weather) — registry shape allows adding them later
- Env var overrides
- Surfacing unavailable facts in the prompt
