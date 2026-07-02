# Plan: LLM integration behind an adapter pattern

Status: proposed
Author: Marcus (drafted with Claude)
Date: 2026-07-01

## Why

The LLM integration is welded to one backend: the local `claude` CLI, invoked
headless with `-p`. There are exactly two call sites, and both hardcode the
binary, the flags, and the `sonnet/opus/haiku` model names:

- `lib/tui/claude.rb` — `Tui::Claude`, an async runner. Spawns `claude -p PROMPT
  --model MODEL --output-format text --dangerously-skip-permissions
  --append-system-prompt AGENTS.md`, exposes `#io`/`#pump`/`#cancel` so the
  event loop can multiplex it with `IO.select`.
- `bin/tasks` `cmd_prompt` — a synchronous `system("claude", "-p", ...)`, model
  pinned to `sonnet`, appends `AGENTS.md` + `PATHS.claude_context`.

The TUI model switcher (`App::MODELS = %w[sonnet opus haiku]`, `@model`,
`toggle_model`, footer readout) only cycles Claude model tiers.

We want to (a) swap in other backends without touching call sites, (b) support
local models via **Ollama** for the first new use case — adding tasks from
natural language — and (c) later support the **Claude Agent SDK** as an
alternative to shelling out to `claude -p`.

## The one insight that shapes the whole design

The two future backends are not the same *kind* of thing as each other, and
conflating them is the trap.

- **`claude -p` and the Claude Agent SDK are agentic.** We hand them a prompt +
  system context + a working directory and they act autonomously: they read
  `gtd.org`, run `bin/tasks`, and edit files themselves. Our code never parses
  their output for meaning — it just streams a transcript to the user and
  reloads the Store when the file changes. This is the "reschedule, capture,
  edit anything" box in the TUI.

- **Ollama (and any plain completion/chat API) is not agentic** in our setup. A
  local model behind Ollama's `/api/chat` returns *text*. It cannot run the CLI
  or edit files. To make it *do* something we must ask it for **structured
  output**, validate it, and apply the mutation ourselves via `Tasks::Store`.

So the adapter layer has **two protocols**, not one. Forcing Ollama into the
agentic shape (or pretending Claude returns structured data) would produce a
leaky abstraction. The registry/config/TUI plumbing is shared; the execution
contract is not.

```
                        ┌─────────────────────────────┐
                        │        LLM::Registry         │  built from config
                        │  provider → adapter + models │
                        └──────────────┬──────────────┘
                                       │ factory
                 ┌─────────────────────┴──────────────────────┐
                 ▼                                             ▼
      LLM::Agent (protocol)                        LLM::Completion (protocol)
      autonomous, mutates files                    returns text / structured data
      #start #io #pump #cancel #available?         #complete(messages, schema:)
      ────────────────────────────                 ──────────────────────────────
      ClaudeCli      (wraps today's Tui::Claude)   Ollama       (first target)
      ClaudeAgentSdk (later)                        OpenAiCompatible (later, ~free)
                                                    AnthropicApi     (later, ~free)
                             │                              │
                             └──── used raw by TUI ask ─────┘
                                    completion tier also feeds
                                    LLM::Capture → Store#capture!
```

## Goals

1. A stable adapter interface so call sites don't name a backend.
2. Both existing call sites refactored onto it with **zero behavior change**.
3. Provider + model selection driven by `~/.config/tasks/config`.
4. TUI model switcher becomes provider-aware.
5. Ollama-backed "add a task from a sentence" flow, structured + validated,
   applied through the Store — CLI first, TUI second.
6. Claude Agent SDK as a drop-in alternative agent backend.

## Non-goals

- No streaming token UI for completion providers beyond what the TUI already
  does (we can show a spinner; structured capture is fast and short).
- Not turning Ollama into a general file-editing agent. Its scope is the
  structured flows we explicitly build (capture first).
- No new runtime dependencies for the core CLI. The Agent SDK and any HTTP
  clients must be optional and lazily required, so `bin/tasks` stays
  stdlib-only for everyone who doesn't opt in.

## Architecture

New namespace `lib/llm/`, required lazily by the two call sites.

### 1. `LLM::Agent` — autonomous, file-mutating

This *is* today's `Tui::Claude`, generalized. Protocol:

```ruby
module LLM
  class Agent            # abstract
    def self.available? = raise NotImplementedError
    def start(prompt, system:, cwd:, model:) = raise NotImplementedError
    attr_reader :io, :output          # io for IO.select; output is the transcript
    def pump = raise NotImplementedError   # :running | :done
    def running? = raise NotImplementedError
    def cancel = raise NotImplementedError
  end
end
```

- `LLM::Agent::ClaudeCli` — the current spawn logic moved verbatim out of
  `Tui::Claude`. `system:` maps to `--append-system-prompt`, `cwd:` to `chdir:`.
- For `bin/tasks cmd_prompt` (synchronous), either add a `#run_sync` that blocks
  and returns the transcript, or let the CLI drive `start`/`pump` to completion.
  A small `#run_sync` is simpler and keeps `cmd_prompt` readable.

### 2. `LLM::Completion` — text / structured, in-process

```ruby
module LLM
  class Completion       # abstract
    def self.available? = raise NotImplementedError
    # messages: [{role:, content:}]; schema: optional JSON Schema.
    # With a schema, returns a parsed+validated Ruby Hash/Array.
    # Without, returns a String.
    def complete(messages, model:, schema: nil, timeout: 60) = raise NotImplementedError
  end
end
```

- `LLM::Completion::Ollama` — POST to `#{base_url}/api/chat` (default
  `http://localhost:11434`). Uses `net/http` (stdlib) + `json` (stdlib), so no
  new gems. Ollama supports a `format:` field that constrains output to a JSON
  schema — use it when `schema:` is given, and still validate on our side
  because small local models drift. `available?` = a cheap GET on `/api/tags`.
- Later, `OpenAiCompatible` and `AnthropicApi` are the same protocol with
  different endpoints/auth; deferred but the shape is fixed now.

### 3. `LLM::Registry` + factory + config

A registry maps a provider name to `{ kind:, adapter_class:, models: [...] }`
and is built from config with sane built-in defaults:

```ruby
LLM.registry #=> {
#   "claude-cli"  => { kind: :agent,      adapter: Agent::ClaudeCli,
#                      models: %w[sonnet opus haiku] },
#   "claude-sdk"  => { kind: :agent,      adapter: Agent::ClaudeAgentSdk,
#                      models: %w[sonnet opus haiku] },     # when enabled
#   "ollama"      => { kind: :completion, adapter: Completion::Ollama,
#                      models: %w[llama3.1 qwen2.5 ...] },  # from config / /api/tags
# }
```

- `LLM.build(provider:, model:)` returns a ready adapter instance.
- Default provider stays `claude-cli` so nothing changes for current users.

### 4. Config changes (`lib/tasks/config.rb`)

The config file today is a flat `key = value` list restricted to `dir/org/
archive`. Extend the whitelist (still flat — no need for INI sections yet):

```
# ~/.config/tasks/config
llm_provider = ollama          # default provider for -p and TUI
llm_model    = llama3.1        # default model within that provider
ollama_url   = http://localhost:11434
ollama_models = llama3.1,qwen2.5   # optional; else discovered via /api/tags
```

- Add the new keys to `parse_file`'s whitelist (the "unknown keys ignored"
  contract means old configs keep working and this is forward-compatible).
- Add an `LLM::Config` reader (or extend `Tasks::Config::Paths`) exposing
  `llm_provider`, `llm_model`, and provider-specific settings. Keep path
  resolution and LLM settings separable so a headless CLI that never touches
  the LLM doesn't pay for it.
- `Paths#claude_context` is provider-agnostic already (it just states file
  locations) — rename to `agent_context` since the Agent SDK will use it too;
  keep `claude_context` as an alias for one release if anything external calls
  it. Completion providers get a *different*, smaller system prompt (see
  Capture) — they don't need CLI paths because they don't run the CLI.

## TUI model switcher redesign

Today: `@model` is a bare string cycled through three Claude tiers.

New: the switcher cycles over `(provider, model)` entries flattened from the
registry, each carrying its `kind`:

```ruby
Entry = Struct.new(:provider, :model, :kind)  # kind: :agent | :completion
```

- `toggle_model` advances through the entry list; footer shows
  `provider:model` (e.g. `claude-cli:sonnet`, `ollama:llama3.1`).
- `submit_prompt` branches on the selected entry's `kind`:
  - `:agent` → today's path: `LLM.build(...).start(text, system:, cwd:,
    model:)`, stream transcript, reload Store. Unchanged UX.
  - `:completion` → the free-form "edit anything" box is **not** available;
    route the text through `LLM::Capture` (structured add-a-task) and show the
    created item(s) in the response pane. The prompt hint changes to reflect
    the narrower capability, e.g. `enter to capture a task with ollama…`.
- If a provider is unavailable (`claude` not on PATH, Ollama not running), the
  switcher still lists it but `submit_prompt` flashes the same
  "not found / not running" message pattern that exists today, so selection is
  never a dead end silently.

This keeps the switcher a single control while honoring the two tiers. A
completion provider simply exposes fewer actions in the TUI than an agent does —
which is the honest truth about what a local model can do here.

## Use case 1 (first): add tasks with Ollama — the completion tier

The target is `Tasks::Store#capture!`, whose keyword signature is a ready-made
schema:

```ruby
capture!(text, due:, scheduled:, priority:, tags:, state:, project:)
```

New module `LLM::Capture`:

1. Build messages: a compact system prompt describing the org conventions that
   matter for capture — the states (`INBOX/TODO/NEXT/WAITING`), priority
   `A|B|C`, contexts (`@computer`, `@email`, …), the Covey `important/urgent`
   tags, ISO dates, and today's date for relative-date resolution. (Source of
   truth is `docs/conventions.md`; keep this prompt short, not the whole doc.)
2. Ask for structured output matching a JSON Schema mirroring `capture!`'s
   kwargs — e.g. `{ title, state, priority, due, scheduled, tags[], context[],
   project }`. One task to start; allow an array later.
3. `adapter.complete(messages, model:, schema: CAPTURE_SCHEMA)`.
4. Validate/normalize in Ruby: reject unknown states, coerce dates through
   `Tasks::Dates`, drop empties. Never trust the model's shape blindly.
5. Apply via `Store#capture!` — the single mutation path every other command
   already uses, so file integrity and `tasks check` guarantees are preserved.

Surfaces:

- **CLI first** (easy to test, no event loop): a subcommand, e.g.
  `tasks capture --ai "text"` or a distinct `tasks ai-capture "text"`. Prints
  the created item's headline like the normal `capture` does. Decide the exact
  spelling during implementation; update `docs/cli-spec.md`.
- **TUI second**: the `:completion` branch of `submit_prompt` above.

Why capture and not general editing: capture is the one flow where a small local
model is reliable (short, bounded, single structured object) and where a wrong
answer is cheap to inspect and undo. General editing needs an agent.

## Use case 2 (later): Claude Agent SDK instead of `claude -p`

`LLM::Agent::ClaudeAgentSdk` implements the same `Agent` protocol as
`ClaudeCli`, so no call site changes — only the registry/config point at it.

- Runs the SDK in a subprocess (keeps `#io`/`#pump`/`#cancel` working with the
  existing `IO.select` loop) rather than in-process, unless we later want
  richer structured events. A thin Ruby↔SDK shim script that reads a prompt and
  streams output is enough; it must be an **optional** dependency, lazily
  required, so core users aren't forced to install it.
- Reuses `agent_context` (the renamed `claude_context`) and `AGENTS.md`.
- Gains over `-p`: structured tool-use events, better cancellation, session
  continuity. None of that is required for parity — parity is the acceptance
  bar for this adapter.

Refer to the Claude Agent SDK docs when implementing; confirm current model IDs
and invocation via the `claude-code-guide` / `claude-api` references rather than
assuming.

## Phasing

Each phase ships independently and leaves the tool fully working.

1. **Extract, no behavior change.** Create `lib/llm/`, define `Agent` protocol,
   move `Tui::Claude`'s spawn logic into `LLM::Agent::ClaudeCli`. Point both
   `Tui::Claude` (now a thin wrapper or deleted) and `cmd_prompt` at it. Verify
   the TUI ask and `tasks -p` behave identically.
2. **Config + registry + provider-aware switcher.** Add LLM config keys, the
   registry/factory, and rework the TUI switcher to `(provider, model)` entries.
   Still only `claude-cli` registered → still no behavior change, but the
   plumbing is in place.
3. **Completion tier + Ollama capture.** Add `Completion` protocol,
   `Completion::Ollama`, `LLM::Capture`, the CLI subcommand, then the TUI
   `:completion` branch. This is the first user-visible new capability.
4. **Claude Agent SDK adapter.** Add `Agent::ClaudeAgentSdk`, register it,
   document how to enable it in config.

## Testing

Per the repo's mutation/testing rules, every mutation goes through the Store and
gets a test; the LLM boundary must be mockable so tests never hit a network or a
subprocess.

- **Adapters are injectable.** Call sites take an adapter (or a factory) they
  can be handed a fake in tests. No test spawns `claude` or calls Ollama.
- **Ollama adapter:** unit-test request building and response parsing against
  canned JSON (including malformed/partial output → validation rejects it
  cleanly). Optionally one integration test guarded by an env flag
  (`TASKS_OLLAMA_IT=1`) for local-only runs.
- **`LLM::Capture`:** feed a fake completion adapter a structured object; assert
  it lands as the right `Store#capture!` call and that bad shapes are rejected
  without mutating the file. Reuse the existing capture tests' fixtures.
- **Registry/config:** table-test that config keys select the right adapter +
  model and that defaults preserve today's `claude-cli:sonnet` behavior.
- **Regression:** confirm `bin/tasks -p` and the TUI ask are unchanged after
  phase 1 (transcript streams, file reloads, `esc` cancels).

## Risks & open questions

- **Small-model reliability.** Local models drift from schemas. Mitigation:
  Ollama's `format:` schema constraint + strict Ruby-side validation + showing
  the user the parsed result before it's final is optional but cheap. Keep the
  capture schema small.
- **Config surface creep.** Flat `key = value` is fine for now; if provider
  settings multiply, consider INI sections (`[ollama]`) later — out of scope
  here.
- **Naming.** `tasks capture --ai` vs a separate `ai-capture` command — pick
  during phase 3 and reflect it in `docs/cli-spec.md`.
- **Dependency hygiene.** Ollama uses stdlib (`net/http` + `json`). The Agent
  SDK is the only backend that may add a dependency, and it must stay optional
  and lazily loaded so the core CLI remains stdlib-only.
- **Model ID drift.** Don't hardcode assumptions about Claude/Anthropic model
  IDs; verify against current docs when touching the SDK adapter.
```
