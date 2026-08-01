# Fleet operations for the Go port loop

- **Status:** landed — this is the design rationale for
  [`porting/loop.sh`](../../../porting/loop.sh); companion to the agent prompt
  [`porting/PORTING.md`](../../../porting/PORTING.md)
- **Related:** [tasks-go-port-plan.md](tasks-go-port-plan.md),
  [language-porting-playbook.md](language-porting-playbook.md); runbook in
  [`porting/OPERATING.md`](../../../porting/OPERATING.md)

How the many-agent loop is actually run: what the shell script owns, what the
agents own, and how td serves as the coordination bus. The prompt is policy;
this is mechanism. Agents do not read this file.

## Three layers, strict separation

1. **`porting/loop.sh`** — a dumb process manager. Spawns iterations,
   isolates them, enforces limits. Contains no porting knowledge.
2. **The per-tick agent** — reads `PORTING.md`, orients from td, does one
   step, records, exits. Decides *what* to do and (for Claude) which model
   tier its delegated subagents get.
3. **Subagents** — translation, review, conformance runs, spawned by the
   tick agent through whatever its harness natively supports.

State never lives in a running process. Every tick is stateless and
resumable; td plus the repo are the memory. This is what makes the fleet
harness-agnostic — Codex and Claude ticks can interleave freely because
nothing about the loop's state is harness-shaped.

## loop.sh

The script: [`porting/loop.sh`](../../../porting/loop.sh).

```sh
porting/loop.sh --harness codex -n 2        # n parallel slots
porting/loop.sh --once --dry-run            # single tick, no harness call
```

Per tick: reuse the slot's git worktree (rescuing any uncommitted state a
killed tick left behind onto a `port/rescue/*` branch — never discarded,
never inherited) → detach at main → run the harness non-interactively with
a one-line bootstrap ("read porting/PORTING.md, do one iteration") under a
wall-clock timeout → capture the transcript under `porting/logs/` → pace
and repeat. Short ticks read as "no claimable work" and back off
exponentially to 15 minutes.

- `porting/STOP` halts all slots at the next tick boundary and blocks
  restart until removed; `--max-ticks` caps a slot; `--tick-timeout` kills a
  runaway tick (its state is rescued on the next one).
- **No cost accounting, by design.** Both harnesses run on subscriptions, so
  there is no per-tick price to cap and no reason to ration ticks per
  calendar day. What actually stops the fleet is a usage limit — Claude's
  5-hour and 7-day windows, Codex's plan limits — which exhausts
  unpredictably and then resets at a *known time*. So the script treats it
  like `STOP`: after each tick it scans the transcript tail for the vendor's
  limit wording, extracts a reset time (epoch, ISO-8601, wall clock, or
  relative), logs a grep-able `LIMIT` line, records
  `porting/logs/limit-<harness>.json`, releases the dead tick's td claim, and
  parks until the window reopens (`--on-limit stop` exits cleanly instead).
  Slots check that record before ticking, so only the slot that discovered
  the limit pays for it. Detection patterns live in
  `porting/limit-patterns.<harness>` when a vendor's wording changes: the fix
  is a text file, not a script edit.
- The claim release is the subtle part. A limited tick is killed mid-work by
  the vendor and never hands off, so its td issue is stranded `in_progress`.
  td records its own session id rather than `TD_CONTEXT_ID`, but derives that
  id deterministically from it — so `TD_CONTEXT_ID=<ctx> td whoami` resolves
  the tick to its session, and only issues matching it exactly are unstarted.
  If the mapping ever fails, the script logs and releases nothing: unstarting
  by a looser match would steal a live sibling slot's work.
- Every tick exports a fresh `TD_CONTEXT_ID`, so each is a distinct td
  session — which is what makes "a different agent reviews" enforceable by
  td rather than aspirational.
- Verified before writing: a git worktree of the tasks repo resolves to the
  **same td database** (td keys off the common git dir; `.todos/` lives in
  the main repo root). This is what lets slots share one td while staying
  isolated on files. It is also why the codex sandbox defaults to
  `danger-full-access` — td's database and `~/.config/td` sit outside the
  worktree; tighten to `workspace-write` + `--add-dir` once proven.
- The script never chooses slices or roles. Its whole contract is
  isolation, scheduling, and limits.

## Model choice per harness

The policy (top/mid/cheap tier by role) lives in the prompt. Codex's tiers
mirror Claude's: **sol / terra / luna ↔ opus / sonnet / haiku**. The
plumbing differs:

- **Claude:** run the tick on Opus; the orchestrating agent delegates
  translation/repair/conformance to Sonnet subagents via native subagent
  model override. This is the fully-delegated shape and needs nothing from
  the script.
- **Codex:** flatter — no clean per-subagent model override, so the script
  applies one observable proxy per tick: anything `in_review` waiting →
  **sol** (the tick will judge), otherwise **terra**. That implements the
  prompt's ordering (reviews first) without the script knowing anything
  about slices. If it proves too coarse, the richer mechanism is reading
  the tier named in the latest `td handoff` — model choice as data, script
  as mechanism — but start with the two-state heuristic.

## td as the coordination bus

Right call, with one boundary: **td carries work state; git carries proof.**

- One td issue per manifest slice; campaigns (the plan's 12) as epics.
  Generate the issues from `porting/manifest.jsonl` in a one-time inventory
  step so "next ready work" is just a td query on unblocked issues.
- `td start` is the atomic claim — the whole multi-agent collision problem
  is already solved by the tool. Failed claim = pick other work.
- Handoffs via `td handoff` with what's-proven / what's-next / next tier.
  Asynchronous is fine: nothing in the playbook needs realtime coordination,
  and handoff-through-logs is exactly what td was built for.
- Reviews map onto td's review flow: a *later tick, different context*
  claims the in-review issue and `td approve`s with its own session
  identity. Writers self-approve only low-risk slices. This gives the
  playbook's writer/reviewer separation an enforcement surface for free.
- Evidence (oracle captures, conformance reports, review findings) lives in
  `porting/evidence/` in git, referenced from td logs. The cutover decision
  reads the manifest and evidence — reproducible, CI-checkable, diffable —
  never a td query. No second hand-written progress number anywhere.

## Branch strategy

Land slices on **main**, not a long-lived port branch. The plan already
requires every landed slice to compile and pass conformance beside the
untouched Ruby tree, so main stays shippable — and a port branch would
recreate exactly the drift problem Bun needed a whole parity campaign to
fix. Per-tick work happens on the worktree's branch; merge to main is the
landing step after approval. The standing parity sweep then only has to
watch Ruby-side commits, not reconcile two long-running lines.

## Scaling discipline

Start with **one loop, Claude, interactive-adjacent** until the Phase 1 gate
passes (the comparator provably catches seeded mismatches) and a few
low-risk slices have flowed claim → translate → review → land without
manual repair. Then two parallel loops; mix in Codex. Bun-scale parallelism
was earned by a working factory, not assumed. The failure smell to watch
for: ticks spending most of their tokens re-orienting — that means the
prompt or the handoffs are carrying too little, and the fix is in those,
not in more agents.
