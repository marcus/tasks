# Plan: LLM integration behind an adapter pattern

Status: proposed
Author: Marcus (drafted with Claude)
Date: 2026-07-01 (rev. 2026-07-02: agent-harness-only direction; Hermes first)

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

We want to (a) swap in other backends without touching call sites, (b) add the
**Hermes agent** (Nous Research) — already installed locally, driving a local
**Ollama** model (`gemma4:e4b`) — as the first non-Claude backend, and (c) later
support more harnesses: the **Claude Agent SDK** as an alternative to shelling
out to `claude -p`, and eventually others like **opencode** or **pi agent**.

## The one insight that shapes the whole design

**Every backend we support is an agentic harness — a program we hand a prompt +
system context + a working directory, that then acts autonomously.** It reads
`gtd.org`, runs `bin/tasks`, and edits files itself. Our code never parses its
output for meaning — it streams a transcript to the user and reloads the Store
when the file changes on disk. This is the "reschedule, capture, edit anything"
box in the TUI, and it is the *only* execution contract in the design.

We deliberately **do not** support harnessless models — a bare
completion/chat endpoint (Ollama's `/api/chat`, a raw OpenAI call) that returns
text we'd have to coerce into structured output and apply ourselves. That was an
earlier idea; it's dropped. The whole value of this tool's LLM integration is
autonomous mutation through the Store's guarantees, and a bare model can't do
that. When we want a local model, we put a **harness in front of it** (Hermes,
pointed at Ollama) rather than teaching our code to be the harness.

So the adapter layer has **exactly one protocol**. Backends differ only along
two axes that the protocol hides:

- **Transport** — how we drive the harness: shell out to its CLI
  (`claude -p`, `hermes -z`, an `opencode`/`pi` binary) versus call an in-process
  or subprocess **SDK** (the Claude Agent SDK). Both reduce to the same
  `#start`/`#io`/`#pump`/`#cancel` surface the TUI event loop already speaks.
- **Model** — which model the harness runs (Claude tiers; whatever Ollama model
  Hermes is pointed at, e.g. `gemma4:e4b`).

The registry/config/TUI plumbing is shared; there is no second "completion"
contract to keep in sync.

```
                        ┌─────────────────────────────┐
                        │        LLM::Registry         │  built from config
                        │  provider → adapter + models │
                        └──────────────┬──────────────┘
                                       │ factory
                                       ▼
                            LLM::Agent (one protocol)
                            autonomous, mutates files
                            #start #io #pump #cancel #available?
                    ┌───────────────────┼────────────────────┐
                    ▼                   ▼                     ▼
              transport: CLI      transport: CLI        transport: SDK
              ────────────        ────────────          ────────────
              ClaudeCli           HermesAgent            ClaudeAgentSdk (later)
              (wraps today's      (hermes CLI →          OpenCode / PiAgent
               Tui::Claude)        Ollama gemma4:e4b)    (later; CLI or SDK)
```

## Goals

1. A stable adapter interface (one agent protocol) so call sites don't name a
   backend.
2. Both existing call sites refactored onto it with **zero behavior change**.
3. Provider + model selection driven by `~/.config/tasks/config`.
4. TUI model switcher becomes provider-aware.
5. **Hermes agent** registered as the first non-Claude backend, driving the
   local Ollama model `gemma4:e4b`, reaching parity with the `claude-cli` path
   (streams a transcript, mutates the file, `esc` cancels) — CLI ask first,
   `tasks -p` second.
6. Claude Agent SDK as a drop-in alternative agent backend, proving the protocol
   spans both CLI-transport and SDK-transport harnesses.

## Non-goals

- **No harnessless backends.** We do not add a bare completion/chat adapter that
  returns text for us to parse or coerce into structured output. Local models
  are reached *through* a harness (Hermes → Ollama), never called raw. If we
  ever want raw structured output, that's a separate proposal, not this one.
- No new agentic UX beyond what the TUI already does: stream a transcript, show
  a spinner, reload the Store. Each harness gets the same treatment.
- No new *mandatory* runtime dependencies for the core CLI. The Claude Agent SDK
  (and any future SDK-transport harness) must be optional and lazily required,
  so `bin/tasks` stays stdlib-only for everyone who doesn't opt in. CLI-transport
  harnesses (Hermes, opencode, pi) add no Ruby dependency at all — they're just
  binaries we spawn.

## Architecture

New namespace `lib/llm/`, required lazily by the two call sites.

### 1. `LLM::Agent` — the one protocol

This *is* today's `Tui::Claude`, generalized. Every adapter implements it:

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

### 2. `LLM::Agent::HermesAgent` — the first non-Claude harness

Hermes (Nous Research) is an agentic CLI already installed at
`~/.local/bin/hermes` (v0.17.0). It runs tools and edits files autonomously, so
it fits the `Agent` protocol with **no second contract** — it's another spawn,
just a different binary and flags.

- **Invocation.** Two documented one-shot entry points:
  - `hermes -z "PROMPT"` — purest one-shot: single prompt in, *final response
    text only* on stdout. Good for the synchronous `tasks -p` when we only want
    the final answer.
  - `hermes chat -q "PROMPT"` — one-shot but streams the **transcript including
    tool output**. This is the analogue of `claude -p`'s streamed transcript and
    is what the TUI ask uses (multiplexed via `#io`/`#pump`/`IO.select`).

  Pick per call site during implementation; both act in the current working
  directory, so `cwd:` maps to `chdir:` exactly as with Claude. Confirm the
  exact flags against `hermes --help` at implementation time — the version can
  move under us.
- **Model / backend.** Hermes reads `~/.hermes/config.yaml`. Point it at the
  local Ollama model with a custom OpenAI-compatible endpoint:

  ```yaml
  # ~/.hermes/config.yaml
  model:
    provider: custom
    base_url: "http://localhost:11434/v1"   # Ollama's OpenAI-compatible API
    model: "gemma4:e4b"
  ```

  with `OPENAI_API_KEY=ollama` in `~/.hermes/.env` (Ollama ignores the value but
  the client requires a non-empty key). The `-m/--model` and `--provider` flags,
  and the `HERMES_INFERENCE_MODEL` env var, override per invocation — so our
  adapter can pass the switcher's selected model as `--model gemma4:e4b` rather
  than depending on ambient config. **Prefer passing the model per-invocation**
  so the tool is self-contained and doesn't mutate the user's global Hermes
  config.
- **System prompt.** Hermes has no `--append-system-prompt`. Prepend
  `agent_context` (the renamed `claude_context`) + `AGENTS.md` to the prompt
  text, or use whatever system-prompt mechanism `hermes --help` exposes. Keep the
  same context both harnesses receive so behavior is comparable.
- **`available?`** = `hermes` resolvable on `PATH` **and** the Ollama endpoint
  reachable (a cheap GET on `http://localhost:11434/api/tags`), since an
  installed Hermes with a dead Ollama is still a dead end.
- **Cancellation** is the same as `ClaudeCli`: kill the process group on `cancel`.

### 3. `LLM::Registry` + factory + config

A registry maps a provider name to `{ adapter_class:, models:, transport: }`
and is built from config with sane built-in defaults. Every entry is an agent —
there is no `kind` axis; `transport` (`:cli` | `:sdk`) is informational, used
only for optional-dependency handling, not for branching call sites:

```ruby
LLM.registry #=> {
#   "claude-cli" => { adapter: Agent::ClaudeCli,      transport: :cli,
#                     models: %w[sonnet opus haiku] },
#   "hermes"     => { adapter: Agent::HermesAgent,    transport: :cli,
#                     models: %w[gemma4:e4b] },              # from config
#   "claude-sdk" => { adapter: Agent::ClaudeAgentSdk,  transport: :sdk,
#                     models: %w[sonnet opus haiku] },       # when enabled
# }
```

- `LLM.build(provider:, model:)` returns a ready adapter instance.
- Default provider stays `claude-cli` so nothing changes for current users.

### 4. Config changes (`lib/tasks/config.rb`)

The config file today is a flat `key = value` list restricted to `dir/org/
archive`. Extend the whitelist (still flat — no need for INI sections yet):

```
# ~/.config/tasks/config
llm_provider  = hermes          # default provider for -p and TUI
llm_model     = gemma4:e4b      # default model within that provider
hermes_models = gemma4:e4b      # optional; else the single configured model
```

Note the Ollama endpoint/model live in **Hermes'** own config
(`~/.hermes/config.yaml`), not here — our tool talks to Hermes, and Hermes talks
to Ollama. We only name the provider and the model to pass through as
`--model`. (Keeping `llm_provider = claude-cli` preserves today's behavior.)

- Add the new keys to `parse_file`'s whitelist (the "unknown keys ignored"
  contract means old configs keep working and this is forward-compatible).
- Add an `LLM::Config` reader (or extend `Tasks::Config::Paths`) exposing
  `llm_provider`, `llm_model`, and provider-specific settings. Keep path
  resolution and LLM settings separable so a headless CLI that never touches
  the LLM doesn't pay for it.
- `Paths#claude_context` is provider-agnostic already (it just states file
  locations) — rename to `agent_context` since every harness (Hermes, the Agent
  SDK, …) uses the same context; keep `claude_context` as an alias for one
  release if anything external calls it.

## TUI model switcher redesign

Today: `@model` is a bare string cycled through three Claude tiers.

New: the switcher cycles over `(provider, model)` entries flattened from the
registry. Because every backend is an agent, there is **no `kind` branch** —
`submit_prompt` follows one path for all entries:

```ruby
Entry = Struct.new(:provider, :model)
```

- `toggle_model` advances through the entry list; footer shows `provider:model`
  (e.g. `claude-cli:sonnet`, `hermes:gemma4:e4b`).
- `submit_prompt` is unchanged in shape: `LLM.build(provider:, model:).start(
  text, system:, cwd:, model:)`, stream the transcript, reload the Store on file
  change. The only thing the selection changes is *which binary/SDK* runs and
  *which model* it uses. Identical UX across providers.
- If a provider is unavailable (`claude`/`hermes` not on PATH, Ollama not
  running behind Hermes), the switcher still lists it but `submit_prompt` flashes
  the same "not found / not running" message pattern that exists today, so
  selection is never a dead end silently. This is exactly what `Agent.available?`
  is for.

This keeps the switcher a single control with one behavior. Providers differ in
speed and quality, not in what actions they expose.

## Use case 1 (first): the Hermes agent, backed by Ollama

Register `LLM::Agent::HermesAgent` (see Architecture §2) and drive it exactly
like `claude -p`: hand it the same prompt + `agent_context` + `AGENTS.md` in the
project working directory and let it run `bin/tasks` and edit `gtd.org` itself.
Nothing about the mutation path changes — Hermes goes through the same
`Tasks::Store` commands every other flow uses, so `tasks check` guarantees and
file integrity are preserved without any new validation code on our side.

Concretely, "add a task from a sentence" is just: select `hermes:gemma4:e4b`,
type the sentence, and Hermes runs `bin/tasks capture …`. There is no structured
schema, no `LLM::Capture` module, no Ruby-side parsing of model output — the
harness does the work, which is the whole point of preferring a harness over a
bare model.

Surfaces:

- **CLI ask first** (easy to test, no event loop): `tasks -p "text"` with
  `llm_provider = hermes` (or a `--provider hermes` override) routes through
  `HermesAgent#run_sync`. No new subcommand needed — it's the existing prompt
  path pointed at a different backend.
- **TUI second**: selecting the `hermes` entry in the switcher; the unchanged
  `submit_prompt` path above.

Acceptance bar is **parity with `claude-cli`**: the transcript streams, the file
reloads, `esc` cancels. Quality of a small local model is a separate question
from whether the adapter works.

## Use case 2 (later): more harnesses — Claude Agent SDK, opencode, pi

The same `Agent` protocol absorbs any further harness; the registry/config just
point at a new adapter. Two transports to prove out:

**Claude Agent SDK instead of `claude -p`** (SDK transport):

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

**opencode / pi agent** (CLI transport): each is another `Agent::*` adapter that
spawns a binary, no different in shape from `HermesAgent` — figure out its
one-shot flag, its working-directory behavior, and how it takes a system prompt,
then map them onto `start`/`pump`/`cancel`. These are listed to confirm the
protocol generalizes; none is scheduled here. `pi`, like `hermes`, can itself be
pointed at a local model, giving a second harness-over-Ollama option.

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
3. **Hermes agent adapter.** Add `Agent::HermesAgent` (spawn `hermes`, map
   context + `cwd`, `available?` checks PATH + Ollama), register the `hermes`
   provider, and wire the CLI ask then the TUI switcher entry. This is the first
   user-visible new backend. Acceptance: parity with `claude-cli`.
4. **Claude Agent SDK adapter.** Add `Agent::ClaudeAgentSdk` (SDK transport,
   optional/lazy dependency), register it, document how to enable it in config.
   opencode / pi remain unscheduled follow-ons on the same protocol.

## Testing

Per the repo's mutation/testing rules, every mutation goes through the Store and
gets a test; the LLM boundary must be mockable so tests never spawn a subprocess
or hit a network.

- **Adapters are injectable.** Call sites take an adapter (or a factory) they can
  be handed a fake in tests. No test spawns `claude` or `hermes`.
- **`HermesAgent`:** unit-test *command construction* — that it builds the right
  `hermes` argv (one-shot flag, `--model`, `chdir` to the project dir) and
  routes the prepended system context correctly — without executing it. Cover
  `available?` returning false when the binary is absent or the Ollama probe
  fails. Optionally one integration test guarded by an env flag
  (`TASKS_HERMES_IT=1`) for local-only runs that actually spawns `hermes`.
- **Registry/config:** table-test that config keys select the right adapter +
  model and that defaults preserve today's `claude-cli:sonnet` behavior.
- **Regression:** confirm `bin/tasks -p` and the TUI ask are unchanged after
  phase 1 (transcript streams, file reloads, `esc` cancels), and that every
  registered agent is exercised through the same `submit_prompt` path (no
  per-provider branch to test).

## Risks & open questions

- **Small-model tool-use reliability.** A 4B-class model behind Hermes may run
  the wrong `bin/tasks` command or mangle a flag. But because Hermes mutates only
  through the CLI, every change still passes the Store's validation and is
  visible in the transcript and `git diff` — a bad edit is inspectable and
  revertible, not silent corruption. If it proves flaky, try a larger local model
  (`gemma4:12b-mlx`, `qwen3.6`) — a one-line config change, no code.
- **Hermes interface drift.** Hermes is at v0.17.0 and moving; the exact one-shot
  flags (`-z` vs `chat -q`), the system-prompt mechanism, and config schema may
  change. Confirm against `hermes --help` / the docs at implementation time and
  pin the observed version in a comment. Treat it as an external contract, not a
  stable API.
- **Config split.** LLM endpoint config lives in *two* places — our
  `~/.config/tasks/config` (provider/model) and Hermes' `~/.hermes/config.yaml`
  (Ollama endpoint). Passing `--model` per-invocation avoids depending on the
  latter, but the user still needs Hermes pointed at Ollama once. Document this
  in the README so it isn't a silent prerequisite.
- **Config surface creep.** Flat `key = value` is fine for now; if provider
  settings multiply, consider INI sections (`[hermes]`) later — out of scope
  here.
- **Dependency hygiene.** CLI-transport harnesses (Hermes, opencode, pi) add no
  Ruby dependency — they're spawned binaries. The Claude Agent SDK is the only
  backend that may add one, and it must stay optional and lazily loaded so the
  core CLI remains stdlib-only.
- **Model ID drift.** Don't hardcode assumptions about Claude/Anthropic model
  IDs; verify against current docs when touching the SDK adapter.
```
