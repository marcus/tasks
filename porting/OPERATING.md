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
`porting/logs/limit-<harness>.json`, releases the dead tick's td claim (one
`td unstart --session`), and by default parks until the window resets, then
resumes on its own.

Run it under `nohup`/tmux; it's a foreground supervisor. Mixing harnesses
is fine — run one invocation per harness; slots coordinate through td, and
slot worktrees are per-invocation directories under `../tasks-port-slots/`
(give a second invocation its own `SLOTS_DIR`).

**On `nohup` and stdin.** `codex exec` reads stdin whenever it is not a tty,
and `nohup` redirects stdout and stderr but *not* stdin. Under a piped or
`nohup`ed launch that used to make every codex tick print `Reading additional
input from stdin...` and block for the whole `--tick-timeout`, on every slot,
forever. `loop.sh` now runs both harnesses with `< /dev/null`, so `nohup
porting/loop.sh … &` is safe as written. If you ever invoke a harness by hand
outside the script, redirect stdin yourself.

## How a usage limit is detected

Both harnesses are invoked in JSONL mode (`codex exec --json`, `claude -p
--output-format stream-json --verbose`), which is what makes detection safe:
the harness's own typed error records are separable from the agent's output.

1. **Structured** (primary) — only typed error records are matched: codex's
   `error` / `turn.failed` / `item.completed(item.type=="error")` events,
   claude's `result` record with `is_error:true` (including the typed
   `api_error_status`, where 429 alone is enough) and any
   `is_api_error_message` turn. Agent-authored text can never reach these
   fields, so this stage runs at any exit status.
2. **Prose** (documented fallback) — a flat grep of the transcript, for a
   harness build that emits no JSONL at all. It is reached only when *no*
   structured records exist **and** the tick exited non-zero **and** the exit
   was not 124 (our own `timeout` kill).

Those gates are not decoration. This repo's own scripts contain every trigger
string by necessity, so before them a tick that merely *read* `porting/loop.sh`
parked the entire fleet — and one such false positive parsed a reset time out
of a test fixture and held every slot until 9:30am.

The `LIMIT` log line reports both: `stage=structured|prose` says which stage
fired, `source=` names the exact pattern that claimed the line — which is the
line of `porting/limit-patterns.<harness>` to fix if it was wrong.

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
  duration. Two grep-able tokens matter: `TIMEOUT` (the script killed a tick)
  and `LIMIT` (the vendor did). `grep LIMIT porting/logs/loop.log` is the whole
  usage-limit history.
- Transcripts: `porting/logs/YYYYMMDD/slotN-<ts>.log`, now **JSONL** — that is
  what structured limit detection reads. Codex also writes the final message
  as plain text to `.last` beside it, which is usually what you want first.
  To read a transcript as prose:

  ```sh
  T=porting/logs/20260801/slot1-20260801-120000.log
  cat "${T%.log}.last"                                    # codex: final message
  jq -r 'select(.type=="item.completed") | .item.text // empty' "$T"   # codex
  jq -r 'select(.type=="assistant") | .message.content[]?.text? // empty' "$T"  # claude
  jq -r 'select(.type=="result") | .result' "$T"          # claude: final message
  jq -rc 'select(.type=="error" or .type=="turn.failed")' "$T"         # what broke
  ```
- `td status` / `td ready` / `td in-review` / `td blocked` — the work
  itself. `git branch --list 'port/*'` — what the fleet is building.
- Progress of record is the manifest + evidence, not vibes:
  `jq -r '.status' porting/manifest.jsonl | sort | uniq -c`.

## Landing

An approved, closed slice lands on its own: any tick's step 6 runs
`porting/land <slice>`, which merges to main (no-ff), runs the test suite,
marks the manifest `ported`, and deletes `port/<slice>` — or refuses and
leaves main untouched, logging why on the td issue (`td log <id>`). Both
harnesses serialize through a lock in `.git/porting-land.lock`; a dead
holder's lock is reclaimed automatically. Nothing to run by hand; watch for
it in `td log` on a slice's issue, and in `git branch --list 'port/*'`
shrinking as `porting/manifest.jsonl` statuses reach `ported`.
`porting/test-land.sh` proves the mechanism against scratch repos. To pause
just landing without stopping the fleet, there is no separate switch —
`touch porting/STOP` stops everything at the next tick boundary.

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
| Issue `in_progress`, no recent activity | Claim leaked by a killed tick (every tick is a fresh session; a dead one can't hand off) | After any `TIMEOUT` line: `td list -s in_progress`, then `td unstart --session <ses> --force` to release. After a `LIMIT` line the script already did this — unless it logged `claim-release skipped`, which means it could not map the tick to a td session and left it to you. `LIMIT no claim held by …` is the normal, common case: the tick had already handed off |
| Nothing running, log ends in `parked until …` | A usage limit; the window hasn't reset | Nothing. It resumes itself and logs a still-parked line every 15m. `porting/logs/limit-<harness>.json` has the reset time |
| Repeated `LIMIT no usable reset time in the message` | The vendor changed its wording, so the reset time no longer parses — or it parsed to something unusable (in the past, or beyond `LIMIT_MAX_WAIT`) | The fallback wait doubles from 30m to 6h, so the fleet still recovers — but fix the pattern: add the new wording to `porting/limit-patterns.<harness>` |
| `LIMIT parsed reset … exceeds LIMIT_MAX_WAIT; clamping` | A vendor typo or a bad match produced an absurd reset time | Nothing urgent — the clamp is the safety net. Read the `reason` field of `limit-<harness>.json` to see what was matched, and narrow the pattern if it was wrong |
| `LIMIT` lines but no limit is actually in force | A detection pattern is too broad | Read `stage=` on the LIMIT line first. `stage=structured` means the harness really did report an error, so the pattern matched a genuine failure that isn't a limit. `stage=prose` means the harness emitted no JSONL — check the transcript is really JSONL. Either way, narrow it in `porting/limit-patterns.<harness>` (that file replaces the built-in list) and rerun `porting/test-loop-limits.sh` |
| `FAILED to rescue dirty worktree` | The salvage commit could not be made | The tick's uncommitted work was discarded so the next tick starts clean — that is deliberate, and the line is your only notice. Read the transcript for what was in flight. `--no-verify` and an explicit commit identity mean a hook or unset `user.email` can no longer cause this, so treat it as a real repo problem |
| `porting/land` `REFUSED` in `td log` on a slice | A precondition failed: not approved, branch missing, real conflict, or failing tests | The log line names the exact check. Main is guaranteed untouched — read the tail, fix the cause (re-review, rebase the slice, fix the break), and the next tick's step 6 retries on its own |
| `port/*` branches accumulate despite closed, approved issues | `porting/land` isn't being reached, or the merge genuinely conflicts every time | Check a recent transcript for step 6; if it never runs, fix `PORTING.md`'s step 6 or the harness's context budget. If it runs and refuses, the branch is stale against main — the next tick's translator should rebase or a human should look |

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
| `--on-limit` | `park` | `park` sleeps until the window resets and resumes; `stop` exits the slot cleanly. **Exit 0 either way** — mid-run, or when preflight finds a limit already in force. A usage limit is a wait, not a failure, and a wrapper script must not have to care when it was noticed |
| `--once` | off | Never parks. If a limit is in force at the gate, or the single tick ends in one, it reports and exits — `--once` is for debugging, and a debugging run that sleeps until 4pm is not one |
| `LIMIT_COOLDOWN` | 1800s | Wait when the limit message carries no usable reset time; doubles per consecutive unresolved hit |
| `LIMIT_MAX_WAIT` | 21600s | Cap on that doubling (6h, comfortably past a 5-hour window) **and on any reset time parsed out of a vendor message**. `resets in 999h99m` is 41 days; without the clamp one typo parks the fleet for weeks |
| `LIMIT_GRACE` | 60s | Slack added to a parsed reset time (applied before the clamp) |
| `LIMIT_PROBE` | off | Optional pre-tick command; exit 3 means limited, and a bare epoch on its stdout is the reset time (clamped like any other). The seam for a real headroom check; nothing ships behind it |
| `porting/limit-patterns.<harness>` | off | One extended regex per line, `#` comments ignored; replaces the built-in detection patterns for that harness. When a vendor changes its wording, the fix is this file, not the script. The built-in lists are derived from the installed binaries' string tables — `loop.sh`'s `limit_patterns` header carries the exact `strings`/`grep` commands to re-derive them after an upgrade |

### Reset times, zones, and what is assumed

A reset time is read from the limit message in four forms: a bare epoch, an
ISO-8601 timestamp, a wall clock (`resets 4pm`), or a relative interval
(`resets in 2h30m`). Two assumptions are worth knowing because they are
deliberate:

- A **wall clock or a naked ISO timestamp with no zone marker** is resolved in
  the zone the vendor named in parentheses — the real message is `You've hit
  your session limit · resets 4pm (America/Los_Angeles)` — and failing that in
  the machine's local zone. Not UTC: on a machine west of UTC, reading it as
  UTC would place the reset *earlier* than the truth, and resuming early
  re-hits the limit, re-parks, and (because a reset *was* parsed) never climbs
  the backoff ladder. Erring late only costs time.
- A parsed reset that lands **in the past** or **beyond `LIMIT_MAX_WAIT`** is
  discarded, not used, and the fleet falls back to the doubling ladder.

**`LIMIT_STREAK` is per-slot, on purpose.** The doubling ladder counts
consecutive unresolved hits within one slot, while the limit record is shared
by every slot of an invocation. In practice only the slot that discovers a
limit climbs the ladder — the others learn about it at the gate and park
without paying a harness call, which is the behaviour you want. Making the
streak shared would need a read-modify-write lock on the state file for a case
that costs, at worst, one extra cooldown. If you ever see the ladder failing to
engage across slots, that is why, and it is documented rather than fixed.
| `CLAUDE_MODEL`, `CODEX_TOP/MID` | opus, gpt-5.6-sol/gpt-5.6-terra | Tier policy itself lives in `PORTING.md`; use full Codex model identifiers because the ChatGPT-account CLI rejects the short aliases |
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
`--on-limit stop` names the reset time and **exits 0** without starting — the
same status a mid-run limit produces. Either way you can see from the log why
nothing is running.

## Testing the limit logic

```sh
porting/test-loop-limits.sh    # exits 0 on success, one line per assertion
```

It sources `loop.sh` and covers the detection patterns, all four reset-time
forms against a pinned clock, and — the part that used to be missing —
`write_limit_state`, `limit_gate`, `park_until`, `preflight_limit_state`,
`release_tick_claim` (against a `td` stub), `run_limit_probe`, `handle_limit`
and `prepare_worktree` against a real throwaway git repo. Run it after editing
the script or a `limit-patterns.*` override.

**The bar for adding a test here: break the behaviour it names, deliberately,
and confirm the suite goes red.** Every defect this suite now covers was found
by running the supervisor rather than by testing it, and a test that stays
green when you revert the thing it claims to cover is worse than no test —
it is a false assurance sitting exactly where you will stop looking.
