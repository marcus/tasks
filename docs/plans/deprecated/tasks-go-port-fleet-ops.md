# Fleet operations for the Go port loop

> **Superseded for execution on 2026-08-03.** The loop remains stopped and this
> file is historical design rationale. Do not restart or extend the fleet for
> the active port. Follow
> [tasks-go-port-velocity-plan.md](../active/tasks-go-port-velocity-plan.md).

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
killed tick left behind onto a `port/rescue/*` branch) → detach at main →
run the harness non-interactively with a one-line bootstrap ("read
porting/PORTING.md, do one iteration") under a wall-clock timeout, with
stdin on `/dev/null` and JSONL output → capture the transcript under
`porting/logs/` → pace and repeat. Short ticks read as "no claimable work"
and back off exponentially to 15 minutes.

Two details of that sentence are load-bearing and were each a real defect:

- **`< /dev/null`.** `codex exec` reads stdin whenever it is not a tty, and
  `nohup` — which this runbook recommends — does not redirect it. Without the
  redirect every codex tick on every slot blocked for the full
  `--tick-timeout` doing nothing, indefinitely.
- **"never discarded, never inherited" is now enforced, not asserted.** The
  rescue commit is made with an explicit identity and `--no-verify`, so
  neither a repo hook nor an unset `user.email` can fail it; its outcome is
  reported truthfully rather than logged as success from outside the `&&`
  chain; and a backstop hard-cleans the tree if a rescue fails anyway,
  because git carries the index across `checkout --detach` and the next tick
  would otherwise start on a dead tick's half-finished work.

- `porting/STOP` halts all slots at the next tick boundary and blocks
  restart until removed; `--max-ticks` caps a slot; `--tick-timeout` kills a
  runaway tick (its state is rescued on the next one).
- **No cost accounting, by design.** Both harnesses run on subscriptions, so
  there is no per-tick price to cap and no reason to ration ticks per
  calendar day. What actually stops the fleet is a usage limit — Claude's
  5-hour and 7-day windows, Codex's plan limits — which exhausts
  unpredictably and then resets at a *known time*. So the script treats it
  like `STOP`: after each tick it classifies the outcome, extracts a reset
  time (epoch, ISO-8601, wall clock, or relative), logs a grep-able `LIMIT`
  line, records `porting/logs/limit-<harness>.json`, releases the dead tick's
  td claim, and parks until the window reopens (`--on-limit stop` exits
  cleanly instead — with status 0, the same as a mid-run limit, whether the
  limit is found mid-run or by preflight). Slots check that record before
  ticking, so only the slot that discovered the limit pays for it.
- **Detection is structured first, prose second.** Both harnesses run in
  JSONL mode (`codex exec --json`, `claude -p --output-format stream-json`),
  and the primary matcher reads only the harness's own typed error records —
  codex's `turn.failed` / `error` events and its `usage_limit_exceeded` code,
  claude's `is_error` result record including the typed `api_error_status`.
  Prose matching survives as a documented fallback for a build that emits no
  JSONL, gated on the tick exiting non-zero and not on 124 (our own timeout
  kill). The gates are the whole point: this repo's scripts necessarily
  contain every trigger string, so an ungated grep parked the fleet whenever
  a tick happened to *read* `porting/loop.sh`. The override seam is
  unchanged — `porting/limit-patterns.<harness>` still replaces the built-in
  list, and the built-in lists are now derived from the installed binaries'
  string tables rather than guessed. (The previous claude list was anchored
  on `Claude AI usage limit reached`, a string that appears zero times in the
  binary; the real prose is `You've hit your …`.)
- **A parsed reset is a hint, not an instruction.** It is discarded if it
  lands in the past and clamped to `LIMIT_MAX_WAIT` if it lands beyond it, so
  a vendor typo (`resets in 999h99m` — 41 days) cannot park the fleet for
  weeks. A wall clock with no zone marker is resolved in the zone the vendor
  named, else local — never UTC, because on a machine west of UTC that errs
  *early*, and resuming early re-hits the limit while also clearing the
  backoff streak.
- The claim release is the subtle part. A limited tick is killed mid-work by
  the vendor and never hands off, so its td issue is stranded `in_progress`.
  td records its own session id rather than `TD_CONTEXT_ID`, but derives that
  id deterministically from it — so `TD_CONTEXT_ID=<ctx> td whoami --json`
  resolves the tick to its session, and a single `td unstart --session <ses>
  --force --json` releases exactly that session's claims and cannot touch a
  live sibling slot's work. `--force` is required (without it the sweep only
  previews), and the call exits 1 with `{"error":{"code":"not_found"}}` when
  the session holds nothing — the common case for a tick that handed off
  cleanly — so the outcome is read from `.error.code` / `.count`, never from
  the exit status. If the ctx cannot be mapped to a session at all, the
  script logs and releases nothing.
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
- **The supervisor is tested, and the test suite is held to mutation.**
  `porting/test-loop-limits.sh` covers the limit machinery, the worktree
  rescue path and the claim release (against a `td` stub and a throwaway git
  repo), not just the three pure functions it started with. Every fix above
  was verified by breaking it deliberately and confirming the suite goes red.
  That standard is written into the suite's header, because the first review
  of this script found every one of its defects by *running* it — the tests
  were all green throughout.

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
