# Operating the Go-port fleet

> The runbook for the human at the terminal, next to `loop.sh`. Agents read
> [PORTING.md](PORTING.md); design rationale is in
> [tasks-go-port-fleet-ops.md](../docs/plans/active/tasks-go-port-fleet-ops.md).
> All paths below are relative to the tasks repo root.

## One-time setup

1. Already done by the scaffold: `porting/loop.sh` (executable),
   `porting/PORTING.md`, plan + playbook under `docs/plans/active/`,
   `porting/logs/` and `porting/STOP` gitignored.
2. Build the control plane (Phase 1): `porting/manifest.jsonl`, fixtures,
   runners, comparators. Generate one td issue per slice, campaigns as
   epics. **The fleet has nothing to do until this exists.**
3. Smoke the machinery, cheapest first:

```sh
porting/loop.sh --once --dry-run              # preflight + worktree only
porting/loop.sh --once --harness claude       # one real tick, watch it live
tail -f porting/logs/loop.log
```

Don't scale past one slot until the Phase 1 gate passes (the comparator
catches seeded mismatches) and a few low-risk slices have flowed
claim → translate → review → land without hand-holding.

## Running

```sh
porting/loop.sh --harness codex -n 2 --tick-timeout 3600
porting/loop.sh --harness claude -n 1 --on-limit stop   # don't park; exit
```

There is nothing to spend here — both harnesses are on subscriptions, so the
script does no dollar or quota accounting. What it does handle is the usage
limit: when one is hit, the slot logs a `LIMIT` line, records
`porting/logs/limit-<harness>.json`, releases the dead tick's td claim, and by
default parks until the window resets, then resumes on its own.

Run it under `nohup`/tmux; it's a foreground supervisor. Mixing harnesses
is fine — run one invocation per harness; slots coordinate through td, and
slot worktrees are per-invocation directories under `../tasks-port-slots/`
(give a second invocation its own `SLOTS_DIR`).

## Stopping

| Intent | Do |
|---|---|
| Clean halt, let ticks finish | `touch porting/STOP` — every slot stops at its next tick boundary |
| Restart later | `rm porting/STOP` (the script refuses to start while it exists — deliberate) |
| Immediate stop | Ctrl-C / `kill` the supervisor; in-flight harness processes may linger briefly, and any uncommitted tick state is rescued on the next start |
| Stop one runaway tick | it dies on its own at `--tick-timeout`; don't hand-kill unless it's misbehaving *now* |
| Fleet stopped by a usage limit | `porting/logs/limit-<harness>.json` says when it resets; under the default `--on-limit park` it resumes itself, nothing to do |

## Watching

- `tail -f porting/logs/loop.log` — one line per tick: slot, model, exit,
  duration. Transcripts: `porting/logs/YYYYMMDD/slotN-<ts>.log` (codex also
  writes the final message to `.last` beside it). Two grep-able tokens matter:
  `TIMEOUT` (the script killed a tick) and `LIMIT` (the vendor did).
  `grep LIMIT porting/logs/loop.log` is the whole usage-limit history.
- `td status` / `td ready` / `td in-review` / `td blocked` — the work
  itself. `git branch --list 'port/*'` — what the fleet is building.
- Progress of record is the manifest + evidence, not vibes:
  `jq -r '.status' porting/manifest.jsonl | sort | uniq -c`.

**Healthy:** ticks run minutes not seconds, end in a td handoff or review,
the in-review queue drains, manifest statuses advance, `port/*` branches
merge and disappear.

**Read the log line, then the transcript.** `exit=0` only means the harness
exited cleanly — whether the tick *did* anything is in td and the manifest.

## Warning signs and what they mean

| Sign | Likely cause | Response |
|---|---|---|
| Every tick short, backoff at 15m | No claimable work | `td ready` empty? Dependency stall — check `td blocked` and `td critical-path`; you may owe a decision |
| Repeated `rescued dirty worktree` on one slot | Ticks timing out mid-work | Read the transcript tail; raise `--tick-timeout` or shrink the slice; salvage or delete the `port/rescue/*` branches |
| Every codex tick on sol | Review pileup | Reviews failing or being rejected repeatedly — read the review findings; the writer and reviewer may be deadlocked on a real question for you |
| Same issue claimed, handed off, claimed… | Slice too big or handoffs too thin | Fix the handoff quality or split the slice; more ticks won't help |
| Ticks burn most tokens re-orienting | Prompt/handoffs carrying too little | Fix `PORTING.md` or the handoff contract — never answer this with more agents |
| Issue `in_progress`, no recent activity | Claim leaked by a killed tick (every tick is a fresh session; a dead one can't hand off) | After any `TIMEOUT` line: `td list -s in_progress`, then `td unstart <id>` to release. After a `LIMIT` line the script already did this — unless it logged `claim-release skipped`, which means it could not map the tick to a td session and left it to you |
| Nothing running, log ends in `parked until …` | A usage limit; the window hasn't reset | Nothing. It resumes itself and logs a still-parked line every 15m. `porting/logs/limit-<harness>.json` has the reset time |
| Repeated `LIMIT no reset time in the message` | The vendor changed its wording, so the reset time no longer parses | The fallback wait doubles from 30m to 6h, so the fleet still recovers — but fix the pattern: add the new wording to `porting/limit-patterns.<harness>` |
| `LIMIT` lines but no limit is actually in force | A detection pattern is too broad | Narrow it in `porting/limit-patterns.<harness>` (that file replaces the built-in list) and rerun `porting/test-loop-limits.sh` |

## Routine maintenance

- **Rescue branches** (`port/rescue/*`): inspect with `git log -1 -p`;
  salvage into the slice branch or delete. Don't let them accumulate —
  each one is a tick whose work nobody adopted.
- **Weekly:** `git worktree prune`; delete merged `port/*` branches; clear
  old `porting/logs/YYYYMMDD/` dirs; skim
  `porting/intentional-differences.md` for anything agents parked.
- **Usage-limit record:** `porting/logs/limit-<harness>.json`. The script
  clears it when the window reopens and clears a stale one at startup; delete
  it by hand only if you're certain the limit has lifted early.
- **Worktree repair** (corrupt/confused slot): stop the fleet,
  `git worktree remove --force ../tasks-port-slots/slot-N`, restart — the
  slot recreates itself. Committed work is safe; it lives in shared refs.

## The queue only you can drain

The fleet escalates by blocking an issue and naming you. Check
`td blocked` regularly — a blocked slice blocks everything downstream of
it. Only you decide:

- **Intentional differences** — any behavior change from Ruby, however
  reasonable it looks.
- **Stop conditions** — a data-format break, unmatchable temporal
  behavior, or permanent dual-domain implementation means stop and
  reconsider, not push through.
- **Tier and scale changes, phase gates, and cutover** — the promotion
  checklist is the plan's Phase 8; nothing in the fleet automates it.

Never resolve an escalation by editing an expected result to match Go
output — that rule binds you too.

## Tuning knobs

| Knob | Default | Notes |
|---|---|---|
| `-n` slots | 1 | Scale only after the loop demonstrably works; 2–3 is plenty for a long time |
| `--tick-timeout` | 3600s | High-risk slices with fault-injection suites may need more |
| `--max-ticks` | off | Per-slot cap; good for supervised sessions. A supervision cap, not a cost cap — there is no cost cap |
| `--on-limit` | `park` | `park` sleeps until the window resets and resumes; `stop` exits the slot cleanly (exit 0 — a usage limit is not a failure) |
| `LIMIT_COOLDOWN` | 1800s | Wait when the limit message carries no parseable reset time; doubles per consecutive unresolved hit |
| `LIMIT_MAX_WAIT` | 21600s | Cap on that doubling (6h, comfortably past a 5-hour window) |
| `LIMIT_GRACE` | 60s | Slack added to a parsed reset time |
| `LIMIT_PROBE` | off | Optional pre-tick command; exit 3 means limited, and a bare epoch on its stdout is the reset time. The seam for a real headroom check; nothing ships behind it |
| `porting/limit-patterns.<harness>` | off | One extended regex per line, `#` comments ignored; replaces the built-in detection patterns for that harness. When a vendor changes its wording, the fix is this file, not the script |
| `CLAUDE_MODEL`, `CODEX_TOP/MID` | opus, sol/terra | Tier policy itself lives in `PORTING.md` |
| `CODEX_SANDBOX` | danger-full-access | td's DB lives outside the worktree; tighten to `workspace-write` + `--add-dir` once proven |
| `PAUSE`/`STAGGER`/backoff | constants in script | Edit in place if they chafe |

## Preflight failures

The script refuses to start rather than limping: missing `PORTING.md`,
harness/td/jq not on PATH, no `gtimeout` (`brew install coreutils`), td not
answering in the repo (`td init`?), or `porting/STOP` present. Each prints
exactly what's wrong; fix and rerun.

Preflight also reads any leftover `porting/logs/limit-<harness>.json`. If the
reset time has passed it deletes the record and says so; if it is still in the
future, `--on-limit park` logs it and lets the slots park, while
`--on-limit stop` refuses to start and names the reset time. Either way you
can see from the log why nothing is running.

## Testing the limit logic

`porting/test-loop-limits.sh` sources `loop.sh` and asserts the detection
patterns and all four reset-time forms against a pinned clock. Run it after
editing either the script or a `limit-patterns.*` override; it exits non-zero
on failure and prints one line per assertion.
