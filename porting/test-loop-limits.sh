#!/usr/bin/env bash
#
# test-loop-limits.sh — proves loop.sh's limit, worktree and claim machinery.
#
# The earlier version of this file tested three pure functions (detect_limit,
# parse_reset_at, fallback_wait) and left write_limit_state, limit_gate,
# park_until, preflight_limit_state, release_tick_claim, run_limit_probe,
# handle_limit and prepare_worktree with ZERO assertions — which is exactly
# where the defects that made the supervisor unsafe to run unattended lived.
# Every one of them was found by running the supervisor, not by this suite.
# Everything reachable without invoking a vendor is covered here now.
#
#   porting/test-loop-limits.sh          # exits 0 on success
#
# THE STANDARD FOR ADDING A TEST: break the behaviour it names, deliberately,
# and confirm this file goes red. A test that stays green when you revert the
# thing it claims to cover is not a test.
#
# Sourcing loop.sh must not start the fleet; that guard is itself the first
# thing this file depends on, and a late assertion checks it explicitly.
set -uo pipefail

export TZ=UTC          # form 3 resolves a wall clock; pin the zone
DIR="$(cd "$(dirname "$0")" && pwd)"

# Sourcing must define functions and run nothing. If the guard in loop.sh
# breaks, this line runs preflight and hangs or dies — a loud failure.
# shellcheck source=/dev/null
. "$DIR/loop.sh"

PASS=0; FAIL=0
TMPROOT="$(mktemp -d -t looptest)"
trap 'rm -rf "$TMPROOT"' EXIT

ok()   { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad()  { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

assert_eq() { # assert_eq <label> <expected> <actual>
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (expected '$2', got '$3')"; fi
}

assert_contains() { # assert_contains <label> <needle> <haystack>
  case "$3" in *"$2"*) ok "$1" ;; *) bad "$1 (no '$2' in: $3)" ;; esac
}

assert_not_contains() { # assert_not_contains <label> <needle> <haystack>
  case "$3" in *"$2"*) bad "$1 (unexpected '$2' in: $3)" ;; *) ok "$1" ;; esac
}

assert_before() { # assert_before <label> <first> <second> <haystack>
  case "$4" in
    *"$2"*"$3"*) ok "$1" ;;
    *) bad "$1 ('$2' does not precede '$3' in: $4)" ;;
  esac
}

exists() { [ -e "$1" ] && echo yes || echo no; }

# ---------------------------------------------------------------- harnesses
# Two ways a limit string can reach a transcript, and they must be treated
# differently. The first is the vendor's own typed error record; the second is
# the agent having merely READ a file — and this repo's own scripts are full of
# limit strings, which is what made unconditional grepping unsafe.

as_codex_error() { # as_codex_error <message> -> one JSONL error event
  jq -cn --arg m "$1" '{type:"turn.failed", error:{message:$m}}'
}

as_codex_agent_output() { # the SAME string, as agent-authored content
  jq -cn --arg m "$1" \
    '{type:"item.completed", item:{id:"item_0", type:"agent_message", text:$m}}'
}

as_claude_error() { # as_claude_error <message> [api_error_status]
  if [ -n "${2:-}" ]; then
    jq -cn --arg m "$1" --argjson s "$2" \
      '{type:"result", is_error:true, subtype:"success",
        terminal_reason:"api_error", api_error_status:$s, result:$m}'
  else
    jq -cn --arg m "$1" \
      '{type:"result", is_error:true, subtype:"error_during_execution", result:$m}'
  fi
}

as_claude_agent_output() {
  jq -cn --arg m "$1" \
    '{type:"assistant", message:{role:"assistant", content:[{type:"text", text:$m}]}}'
}

# A transcript with ordinary traffic around the interesting line, so the tail
# window and the combined grep are exercised the way run_tick exercises them.
write_transcript() { # write_transcript <file> <line>...
  local f="$1" l; shift
  {
    printf '%s\n' '{"type":"thread.started","thread_id":"t1"}'
    printf '%s\n' 'not json at all — harnesses emit stray stderr too'
    for l in "$@"; do printf '%s\n' "$l"; done
    printf '%s\n' '{"type":"turn.completed"}'
  } >"$f"
}

detect_in() { # detect_in <harness> <rc> <line>...
  local h="$1" rc="$2" f; shift 2
  f="$TMPROOT/t.log"
  write_transcript "$f" "$@"
  detect_limit "$h" "$rc" "$f"
}

# ---------------------------------------------------------------- L3: patterns
# The old claude list was anchored on "Claude AI usage limit reached", which
# appears ZERO times in claude 2.1.220. These are strings the binary actually
# carries; the first is verbatim the message that stopped an agent on this
# machine on 2026-08-01.
echo '--- L3 detection: claude, strings re-derived from the binary ---'
for msg in \
  "You've hit your session limit · resets 4pm (America/Los_Angeles)" \
  "You've hit your 5-hour limit · resets 3pm" \
  "You've reached your weekly limit · resets Mon" \
  "You're out of usage credits" \
  "You've used 100% of your 5-hour limit · resets 3pm" \
  "Your org is out of usage · add funds to continue" \
  "Your group's usage limit is set to \$0" \
  '{"error":{"type": "rate_limit_error","message":"..."}}' \
  'API error 429: rate limit exceeded' \
  'HTTP 429 Too Many Requests'
do
  if detect_in claude 1 "$(as_claude_error "$msg")"; then
    ok "claude detects: $msg"
  else
    bad "claude MISSED: $msg"
  fi
done

echo '--- L3 detection: codex ---'
for msg in \
  "You've hit your usage limit. Try again at 9:30am." \
  "You've reached your usage limit. Increase your limits to continue using codex." \
  'stream error: usage_limit_exceeded' \
  'Your workspace is out of credits. Add credits to continue.' \
  'openai: 429 rate limit reached for gpt'
do
  if detect_in codex 1 "$(as_codex_error "$msg")"; then
    ok "codex detects: $msg"
  else
    bad "codex MISSED: $msg"
  fi
done

# The phrase both vendors use lived in the codex list only, and limit_patterns
# never consulted the other harness's list — so claude had no coverage of it.
if detect_in claude 1 "$(as_claude_error "You've hit your usage limit")"; then
  ok "claude detects codex's phrasing too (the lists no longer disagree)"
else
  bad "claude MISSED \"You've hit your usage limit\""
fi

# Dropped on purpose: /goal is interactive-only, so `codex exec` cannot emit
# this, and in a tick transcript it could only be content the agent read.
if detect_in codex 1 "$(as_codex_error 'Goal hit usage limits; stopping.')"; then
  bad 'codex still matches the dropped /goal label'
else
  ok 'codex ignores the dropped /goal label'
fi

echo '--- L3: which pattern matched is reported, and matching is case-insensitive ---'
# Nothing used to assert WHICH pattern claimed a line, so limit_match_line
# could be made case-sensitive undetected — and source= is the field an
# operator reads to know which line of an override file to fix.
detect_in codex 1 "$(as_codex_error 'stream error: usage_limit_exceeded')" >/dev/null
assert_eq 'source= names the exact pattern' 'usage_limit_exceeded' "$LIMIT_MATCH_PATTERN"
detect_in codex 1 "$(as_codex_error 'STREAM ERROR: USAGE_LIMIT_EXCEEDED')" >/dev/null
assert_eq 'matching is case-insensitive (upper)' 'usage_limit_exceeded' "$LIMIT_MATCH_PATTERN"
detect_in claude 1 "$(as_claude_error "YOU'VE HIT YOUR WEEKLY LIMIT")" >/dev/null
assert_contains 'a case-insensitive claude match still names its pattern' \
  "You've (hit|reached) your" "$LIMIT_MATCH_PATTERN"

# ---------------------------------------------------------------- L1/L2
echo '--- L1/L2: agent-authored text must not trip detection ---'
# THE reproducer. A tick that reads porting/loop.sh emits every trigger string
# in this repo as agent output. Structurally that is an agent/assistant message
# — never an error record — so it must not match at ANY exit status.
LOOP_TEXT="You've hit your usage limit. Try again at 9:30am."
for rc in 0 1 124 2; do
  if detect_in codex "$rc" "$(as_codex_agent_output "$LOOP_TEXT")"; then
    bad "codex false-positive on agent output at rc=$rc (pattern: $LIMIT_MATCH_PATTERN)"
  else
    ok "codex ignores agent output at rc=$rc"
  fi
  if detect_in claude "$rc" "$(as_claude_agent_output "$LOOP_TEXT")"; then
    bad "claude false-positive on agent output at rc=$rc (pattern: $LIMIT_MATCH_PATTERN)"
  else
    ok "claude ignores agent output at rc=$rc"
  fi
done

echo '--- L1/L2: the real files in this repo do not trip a clean tick ---'
# Not synthetic: the actual scripts, as a tick that read them would leave them
# in a transcript. rc 0 (ran to completion) and rc 124 (we killed it) must both
# stay silent; only a non-zero, non-124 exit opens prose matching at all.
for src in "$DIR/loop.sh" "$DIR/test-loop-limits.sh"; do
  for rc in 0 124; do
    for h in codex claude; do
      if detect_limit "$h" "$rc" "$src"; then
        bad "$(basename "$src") trips $h detection at rc=$rc (pattern: $LIMIT_MATCH_PATTERN)"
      else
        ok "$(basename "$src") is inert for $h at rc=$rc"
      fi
    done
  done
done

echo '--- L2: prose fallback still works when there is no typed record ---'
PROSE="$TMPROOT/prose.log"
printf 'ordinary output\nYou'\''ve hit your usage limit. Try again at 9:30am.\n' >"$PROSE"
if detect_limit codex 1 "$PROSE"; then
  assert_eq 'prose fallback reports stage=prose' 'prose' "$LIMIT_MATCH_SOURCE"
else
  bad 'prose fallback did not fire at rc=1'
fi
if detect_limit codex 0 "$PROSE"; then
  bad 'prose fallback fired at rc=0'
else
  ok 'prose fallback stays shut at rc=0'
fi
if detect_limit codex 124 "$PROSE"; then
  bad 'prose fallback fired at rc=124 (our own timeout kill)'
else
  ok 'prose fallback stays shut at rc=124'
fi

echo '--- L2: structured detection reports stage=structured, and a typed 429 ---'
detect_in claude 1 "$(as_claude_error 'request failed' 429)" >/dev/null
assert_eq 'typed api_error_status=429 is detected' 'structured' "$LIMIT_MATCH_SOURCE"
assert_eq 'typed 429 names its pattern' 'api_error_status=429' "$LIMIT_MATCH_PATTERN"
if detect_in claude 1 "$(as_claude_error 'request failed' 404)"; then
  bad 'a typed 404 was treated as a usage limit'
else
  ok 'a typed 404 is not a usage limit'
fi

# ---------------------------------------------------------------- reset times
# NOW is pinned to 2026-07-29T08:00:00Z. Every expectation below is a literal
# epoch, computed once against that instant — no re-deriving it from the code
# under test.
NOW=1785312000
echo '--- reset parsing (NOW = 2026-07-29T08:00:00Z, TZ=UTC) ---'

assert_eq 'epoch |form' 1785337200 \
  "$(parse_reset_at 'usage limit reached|1785337200' "$NOW")"

assert_eq 'iso Z' 1785594600 \
  "$(parse_reset_at 'usage limit reached; resets 2026-08-01T14:30:00Z' "$NOW")"
assert_eq 'iso +05:00' 1785576600 \
  "$(parse_reset_at 'usage limit reached; resets 2026-08-01T14:30:00+05:00' "$NOW")"

assert_eq 'try again at 3pm' 1785337200 \
  "$(parse_reset_at "You've hit your usage limit. Try again at 3pm." "$NOW")"
assert_eq 'resets at 09:30' 1785317400 \
  "$(parse_reset_at 'usage limit reached; resets at 09:30' "$NOW")"
assert_eq 'try again at 7am rolls to tomorrow' 1785394800 \
  "$(parse_reset_at 'usage limit reached. try again at 7am' "$NOW")"

assert_eq 'resets in 2h30m' $((NOW + 9000)) \
  "$(parse_reset_at 'usage limit reached; resets in 2h30m' "$NOW")"
assert_eq 'in 5 hours' $((NOW + 18000)) \
  "$(parse_reset_at 'usage limit reached; try again in 5 hours' "$NOW")"
assert_eq 'in 45 minutes' $((NOW + 2700)) \
  "$(parse_reset_at 'usage limit reached; try again in 45 minutes' "$NOW")"

assert_eq 'unparseable yields nothing' '' \
  "$(parse_reset_at 'usage limit reached, sorry' "$NOW")"
assert_eq 'unparseable returns non-zero' 1 \
  "$(parse_reset_at 'usage limit reached, sorry' "$NOW" >/dev/null; echo $?)"

echo '--- 12am/12pm (nothing covered these, so the conversion was deletable) ---'
assert_eq 'try again at 12pm is noon, not midnight' 1785326400 \
  "$(parse_reset_at 'usage limit reached; try again at 12pm' "$NOW")"
assert_eq 'try again at 12am is midnight tomorrow, not noon' 1785369600 \
  "$(parse_reset_at 'usage limit reached; try again at 12am' "$NOW")"

echo '--- L5: a naked ISO timestamp is not read as an hour-of-day ---'
# The bug: form 2 required Z or an offset, so this fell to form 3, which took
# the first two digits of the YEAR as an hour -> 20:00 on the CURRENT day,
# 18 hours EARLY. Early is the dangerous direction: the fleet resumes into a
# closed window, re-hits, and because a reset *was* parsed the streak was
# cleared and the backoff ladder never engaged.
NAKED="$(parse_reset_at 'usage limit reached; resets at 2026-08-02T14:00:00' "$NOW")"
assert_eq 'naked ISO parses to 2026-08-02 14:00 in the assumed zone (UTC here)' \
  1785679200 "$NAKED"
# The specific wrong answer the old code produced, named so a regression is
# unmistakable: 2026-07-29T20:00:00Z.
if [ "$NAKED" = 1785355200 ]; then
  bad 'naked ISO regressed to the year-as-hour reading (2026-07-29T20:00Z)'
else
  ok 'naked ISO is not the year-as-hour reading'
fi

echo '--- L5: malformed date-shaped input is refused, not silently accepted ---'
assert_eq 'impossible components yield nothing' '' \
  "$(parse_reset_at 'usage limit reached; resets at 2026-13-45T99:99:99Z' "$NOW")"
assert_eq 'impossible components return non-zero' 1 \
  "$(parse_reset_at 'usage limit reached; resets at 2026-13-45T99:99:99Z' "$NOW" >/dev/null; echo $?)"
assert_eq 'a bare date with no time is not read as an hour' '' \
  "$(parse_reset_at 'usage limit reached; resets 2026-08-02' "$NOW")"
# Out-of-range components the regex rejects. Both BSD and GNU `date` also
# reject these, so the regex is belt-and-braces — this assertion pins the
# combination, so the day a platform's `date` starts rolling day 32 into
# September the regex is still what stops it and this test still passes.
assert_eq 'utc_to_epoch itself refuses day 32' '' "$(utc_to_epoch '2026-08-32T14:00:00')"
assert_eq 'utc_to_epoch itself refuses hour 25' '' "$(utc_to_epoch '2026-08-01T25:00:00')"
assert_eq 'day 32 yields nothing' '' \
  "$(parse_reset_at 'usage limit reached; resets at 2026-08-32T14:00:00Z' "$NOW")"
assert_eq 'hour 25 yields nothing' '' \
  "$(parse_reset_at 'usage limit reached; resets at 2026-08-01T25:00:00Z' "$NOW")"

echo '--- zone hint: a wall clock is resolved in the zone the vendor named ---'
# The live message. 4pm America/Los_Angeles on 2026-07-29 is 23:00 UTC.
assert_eq 'observed message: resets 4pm (America/Los_Angeles)' 1785366000 \
  "$(parse_reset_at "You've hit your session limit · resets 4pm (America/Los_Angeles)" "$NOW")"
# The same clock time with no zone named falls back to local (TZ=UTC here),
# which is a DIFFERENT instant — so this fails if the zone is silently ignored.
assert_eq 'no zone named falls back to local' 1785340800 \
  "$(parse_reset_at "You've hit your session limit · resets 4pm" "$NOW")"
assert_eq 'an unknown zone is ignored, not trusted' 1785340800 \
  "$(parse_reset_at "You've hit your session limit · resets 4pm (Mars/Olympus)" "$NOW")"
# A naked ISO timestamp uses the same zone rule as a wall clock. This is the
# assertion that distinguishes "assume the named zone / local" from "assume
# UTC" — under TZ=UTC the two are identical, so without a named zone the
# distinction is untestable and the choice could be silently reversed.
assert_eq 'a naked ISO honours the named zone (14:00 LA = 21:00 UTC)' 1785704400 \
  "$(parse_reset_at 'resets at 2026-08-02T14:00:00 (America/Los_Angeles)' "$NOW")"
assert_eq 'zone_hint accepts a known zone' 'America/Los_Angeles' \
  "$(zone_hint 'resets 4pm (America/Los_Angeles)')"
assert_eq 'zone_hint rejects an unknown zone' '' "$(zone_hint 'resets 4pm (Mars/Olympus)')"

# ---------------------------------------------------------------- fallback
echo '--- cooldown fallback and doubling ---'
LIMIT_COOLDOWN=1800
LIMIT_MAX_WAIT=21600
LIMIT_STREAK=1; assert_eq 'streak 1' 1800  "$(fallback_wait)"
LIMIT_STREAK=2; assert_eq 'streak 2' 3600  "$(fallback_wait)"
LIMIT_STREAK=3; assert_eq 'streak 3' 7200  "$(fallback_wait)"
LIMIT_STREAK=4; assert_eq 'streak 4' 14400 "$(fallback_wait)"
LIMIT_STREAK=5; assert_eq 'streak 5 caps' 21600 "$(fallback_wait)"
LIMIT_STREAK=9; assert_eq 'streak 9 still caps' 21600 "$(fallback_wait)"

# ---------------------------------------------------------------- L6: clamp
echo '--- L6: a parsed reset is clamped to LIMIT_MAX_WAIT ---'
# 'resets in 999h99m' parses to 41 days out. Unclamped, that parked the whole
# fleet for weeks behind a 15-minute heartbeat.
FAR="$(parse_reset_at 'usage limit reached; resets in 999h99m' "$NOW")"
assert_eq 'the vendor typo really does parse 41 days out' $((NOW + 3602340)) "$FAR"
assert_eq 'clamped to now + LIMIT_MAX_WAIT' $((NOW + 21600)) \
  "$(clamp_reset "$FAR" "$NOW" 2>/dev/null)"
assert_contains 'and the clamp is logged' 'clamping to' \
  "$(clamp_reset "$FAR" "$NOW" 2>&1 >/dev/null)"
assert_eq 'a reset inside the cap is returned untouched' $((NOW + 600)) \
  "$(clamp_reset $((NOW + 600)) "$NOW" 2>/dev/null)"
assert_eq 'a reset in the past is unusable' '' "$(clamp_reset $((NOW - 60)) "$NOW" 2>/dev/null)"
assert_eq 'a reset in the past returns non-zero' 1 \
  "$(clamp_reset $((NOW - 60)) "$NOW" >/dev/null 2>&1; echo $?)"
assert_eq 'non-numeric input is unusable' '' "$(clamp_reset 'soon' "$NOW" 2>/dev/null)"

# ---------------------------------------------------------------- limit state
# Everything below writes and reads real state files and, in the park tests,
# really sleeps — so the pinned clock has to go. Leaving NOW set here made
# "expired" records read as active (they are dated 2026-07-29) and quietly
# inverted three assertions. T0 is the wall clock; epochs are relative to it.
NOW=""
T0="$(date +%s)"
LIMIT_EPOCH=1785315600     # 2026-07-29T09:00:00Z, for pure formatting checks

echo '--- limit state: write / read / clear ---'
LOG_DIR="$TMPROOT/logs"; mkdir -p "$LOG_DIR"
HARNESS=codex
STOP_FILE="$TMPROOT/STOP"
ON_LIMIT=park
ONCE=0
STATE="$(limit_state_file)"
assert_eq 'the state file is per-harness' "$LOG_DIR/limit-codex.json" "$STATE"

LIMIT_MATCH_LINE="You've hit your usage limit"
write_limit_state "$((T0 + 3600))" "$LIMIT_MATCH_LINE" 2 7 'port-slot2-x' 'pattern'
assert_eq 'reset_at round-trips' $((T0 + 3600)) "$(limit_state_reset_at)"
assert_eq 'the record is valid json' 'codex' "$(jq -r .harness "$STATE")"
assert_eq 'slot is a number, not a string' 'number' "$(jq -r '.slot|type' "$STATE")"
assert_eq 'reset_at is a number, not a string' 'number' "$(jq -r '.reset_at|type' "$STATE")"
assert_eq 'the reason is carried for the operator' "$LIMIT_MATCH_LINE" \
  "$(jq -r .reason "$STATE")"
assert_eq 'no temp file is left behind' '' "$(ls "$LOG_DIR" | grep tmp)"

write_limit_state "" 'no reset time in this one' 1 1 'ctx' 'pattern'
assert_eq 'an absent reset is null, not 0' 'null' "$(jq -r .reset_at "$STATE")"
assert_eq 'reset_at reads as empty when null' '' "$(limit_state_reset_at)"
assert_eq 'detected_at is always recorded' 'number' "$(jq -r '.detected_at|type' "$STATE")"

clear_limit_state
assert_eq 'clear removes the file' 'no' "$(exists "$STATE")"
assert_eq 'reading a missing state returns non-zero' 1 \
  "$(limit_state_reset_at >/dev/null 2>&1; echo $?)"

# ---------------------------------------------------------------- L11
echo '--- L11: resuming must not wipe a newer record another slot wrote ---'
write_limit_state "$((T0 + 3600))" 'first' 1 1 'ctx' 'pattern'
clear_limit_state_if "$((T0 + 3600))"
assert_eq 'the record we parked on is cleared' 'no' "$(exists "$STATE")"

write_limit_state "$((T0 + 9999))" 'a sibling slot hit a NEW limit' 2 1 'ctx' 'pattern'
out="$(clear_limit_state_if "$((T0 + 3600))" 2>&1)"
assert_eq 'a newer record survives' $((T0 + 9999)) "$(limit_state_reset_at)"
assert_contains 'and the skip is logged' 'limit state changed while parked' "$out"
clear_limit_state

# ---------------------------------------------------------------- limit_gate
echo '--- limit_gate ---'
assert_eq 'no record: proceed' 0 "$(limit_gate 1 >/dev/null 2>&1; echo $?)"

write_limit_state "$((T0 - 60))" 'expired' 1 1 'ctx' 'pattern'
assert_eq 'expired record: proceed' 0 "$(limit_gate 1 >/dev/null 2>&1; echo $?)"
limit_gate 1 >/dev/null 2>&1   # again, outside a subshell, so the rm sticks
assert_eq 'and the expired record is cleared on the way through' 'no' "$(exists "$STATE")"

write_limit_state "$((T0 + 3600))" 'active' 1 1 'ctx' 'pattern'
ON_LIMIT=stop
assert_eq '--on-limit stop: halt the slot' 1 "$(limit_gate 1 >/dev/null 2>&1; echo $?)"
assert_contains '--on-limit stop says why' 'on-limit stop, halting' \
  "$(limit_gate 1 2>&1 >/dev/null)"
assert_eq 'and it does NOT delete the still-binding record' $((T0 + 3600)) \
  "$(limit_state_reset_at)"

# ---------------------------------------------------------------- L8: --once
echo '--- L8: --once must not park ---'
# Reproduced as a 45s hang before the fix: limit_gate parked, and the
# `[ "$ONCE" = 1 ] && break` in slot_loop sat AFTER it, unreachable. Both
# assertions are bounded by `timeout` so a regression fails instead of hanging.
ON_LIMIT=park
GATE_BIN="$(command -v gtimeout || command -v timeout)"
gate_probe() { # gate_probe <once> <seconds>
  "$GATE_BIN" "$2" bash -c '
      . "'"$DIR"'/loop.sh"
      LOG_DIR="'"$LOG_DIR"'"; HARNESS=codex; ON_LIMIT=park; ONCE='"$1"'
      STOP_FILE="'"$TMPROOT"'/STOP"; PARK_POLL=1
      limit_gate 1; echo "RC=$?"' 2>&1
}
if [ -n "$GATE_BIN" ]; then
  out="$(gate_probe 1 10)"; rc=$?
  if [ "$rc" -eq 124 ]; then
    bad '--once still parks in limit_gate (timed out)'
  else
    ok '--once returns from limit_gate instead of parking'
    assert_contains '--once explains itself' 'exiting instead of parking' "$out"
    assert_contains '--once halts the slot (rc 1)' 'RC=1' "$out"
  fi
  # The control: the same gate MUST still park when --once is off. Without
  # this, the assertion above would pass even if limit_gate never parked.
  gate_probe 0 4 >/dev/null 2>&1
  assert_eq 'without --once the gate really does park' 124 $?
else
  bad 'no timeout binary; cannot bound the --once assertions'
fi
ON_LIMIT=park

# ---------------------------------------------------------------- park_until
echo '--- park_until: an elapsed park returns, STOP stays responsive ---'
PARK_POLL=1
assert_eq 'a park whose deadline has passed returns immediately' 0 \
  "$(park_until 1 "$((T0 - 5))" >/dev/null 2>&1; echo $?)"
# The clobber, through the function that actually does it. park_until resumes
# and clears; if it clears unconditionally it destroys the record a SIBLING
# slot wrote for a newer limit, and every slot walks back into a closed window.
write_limit_state "$((T0 + 9999))" 'a sibling slot hit a NEW limit' 2 1 'ctx' 'pattern'
park_until 1 "$((T0 - 5))" >/dev/null 2>&1
assert_eq 'park_until does not wipe a record it did not park on' $((T0 + 9999)) \
  "$(limit_state_reset_at)"
# The control: the record it DID park on is cleared, or nothing ever resumes.
write_limit_state "$((T0 - 5))" 'the one we parked on' 1 1 'ctx' 'pattern'
park_until 1 "$((T0 - 5))" >/dev/null 2>&1
assert_eq 'park_until does clear the record it parked on' 'no' "$(exists "$STATE")"

touch "$STOP_FILE"
assert_eq 'STOP while parked halts the slot' 1 \
  "$(park_until 1 "$((T0 + 300))" >/dev/null 2>&1; echo $?)"
assert_contains 'and says so' 'STOP file appeared while parked' \
  "$(park_until 1 "$((T0 + 300))" 2>&1 >/dev/null)"
rm -f "$STOP_FILE"
clear_limit_state

# ---------------------------------------------------------------- L9
echo '--- L9: --on-limit stop exits 0 at startup, matching a mid-run limit ---'
# preflight_limit_state used to `die` (exit 1) here while the docs and the
# mid-run path both said 0 — one condition reporting two different statuses
# depending only on when it happened to be noticed.
write_limit_state "$((T0 + 3600))" 'active' 1 1 'ctx' 'pattern'
out="$(bash -c '
    . "'"$DIR"'/loop.sh"
    LOG_DIR="'"$LOG_DIR"'"; HARNESS=codex; ON_LIMIT=stop
    preflight_limit_state; echo "REACHED_END"' 2>&1)"
rc=$?
assert_eq '--on-limit stop at startup exits 0' 0 "$rc"
assert_not_contains 'and it really did exit, not fall through' 'REACHED_END' "$out"
assert_contains 'and it names the reset time' 'active until' "$out"

# The control: --on-limit park must NOT exit; it logs and lets slots park.
out="$(bash -c '
    . "'"$DIR"'/loop.sh"
    LOG_DIR="'"$LOG_DIR"'"; HARNESS=codex; ON_LIMIT=park
    preflight_limit_state; echo "REACHED_END"' 2>&1)"
assert_contains '--on-limit park falls through to start the slots' 'REACHED_END' "$out"
assert_contains 'and says the slots will park' 'slots will park' "$out"

echo '--- preflight_limit_state: stale and unreadable records ---'
ON_LIMIT=park
write_limit_state "$((T0 - 3600))" 'expired' 1 1 'ctx' 'pattern'
out="$(preflight_limit_state 2>&1)"
assert_contains 'a passed reset is removed' 'already passed' "$out"
assert_eq 'and the file is gone' 'no' "$(exists "$STATE")"

printf 'not json\n' >"$STATE"
out="$(preflight_limit_state 2>&1)"
assert_contains 'an unreadable record is removed' 'unreadable' "$out"
assert_eq 'and that file is gone too' 'no' "$(exists "$STATE")"

write_limit_state "" 'no reset parsed' 1 1 'ctx' 'pattern'
out="$(preflight_limit_state 2>&1)"
assert_contains 'a recent record with no reset is kept and reported' \
  'no parseable reset time' "$out"
assert_eq 'and kept' 'yes' "$(exists "$STATE")"
clear_limit_state

# ---------------------------------------------------------------- probe
echo '--- run_limit_probe ---'
LIMIT_PROBE=""
assert_eq 'no probe configured: proceed' 1 "$(run_limit_probe >/dev/null 2>&1; echo $?)"

printf '#!/bin/sh\necho 1785337200\nexit 3\n'   >"$TMPROOT/probe-limited"
printf '#!/bin/sh\nexit 0\n'                    >"$TMPROOT/probe-ok"
printf '#!/bin/sh\necho "not an epoch"\nexit 7\n' >"$TMPROOT/probe-broken"
printf '#!/bin/sh\necho "resets soon-ish"\nexit 3\n' >"$TMPROOT/probe-limited-junk"
chmod +x "$TMPROOT"/probe-*

LIMIT_PROBE="$TMPROOT/probe-limited"
assert_eq 'exit 3 means limited' 0 "$(run_limit_probe >/dev/null 2>&1; echo $?)"
run_limit_probe >/dev/null 2>&1
assert_eq 'a bare epoch on stdout is the reset time' 1785337200 "$LIMIT_PROBE_RESET"
assert_eq 'the probe is named as the source' 'LIMIT_PROBE' "$LIMIT_MATCH_PATTERN"

LIMIT_PROBE="$TMPROOT/probe-ok"
assert_eq 'exit 0 means proceed' 1 "$(run_limit_probe >/dev/null 2>&1; echo $?)"

LIMIT_PROBE="$TMPROOT/probe-broken"
assert_eq 'any other exit is inconclusive: proceed' 1 \
  "$(run_limit_probe >/dev/null 2>&1; echo $?)"
assert_contains 'and an inconclusive probe is logged' 'inconclusive' \
  "$(run_limit_probe 2>&1 >/dev/null)"

# A LIMITED probe (exit 3) whose stdout is not an epoch: still limited, but the
# junk must not become a reset time — handle_limit would write it into the
# state file and limit_gate would then compare a string to a clock.
LIMIT_PROBE="$TMPROOT/probe-limited-junk"
assert_eq 'a junk-stdout probe is still limited' 0 \
  "$(run_limit_probe >/dev/null 2>&1; echo $?)"
run_limit_probe >/dev/null 2>&1
assert_eq 'but its junk stdout is not taken as an epoch' '' "$LIMIT_PROBE_RESET"
LIMIT_PROBE=""

# ---------------------------------------------------------------- L10
echo '--- L10: claim release collapses onto td unstart --session ---'
STUB="$TMPROOT/bin"; mkdir -p "$STUB"
REPO_ROOT="$TMPROOT"
OLDPATH="$PATH"
make_td_stub() { # make_td_stub <unstart-json> <unstart-exit>
  cat >"$STUB/td" <<EOS
#!/bin/sh
printf '%s\n' "\$*" >>"$TMPROOT/td-calls"
case "\$1" in
  whoami)  echo '{"action":"whoami","session":"ses_2a1c31"}' ;;
  unstart) printf '%s\n' '$1'; exit $2 ;;
esac
EOS
  chmod +x "$STUB/td"
}
PATH="$STUB:$PATH"

: >"$TMPROOT/td-calls"
make_td_stub '{"action":"released_session_claims","count":1,"claims":[{"id":"td-abc"}],"unresolved":[]}' 0
out="$(release_tick_claim 3 'port-slot3-x' 2>&1)"
assert_contains 'a released claim is reported' 'released 1 claim(s)' "$out"
assert_contains 'the session is named' 'ses_2a1c31' "$out"
calls="$(cat "$TMPROOT/td-calls")"
assert_contains '--session is used' 'unstart --session ses_2a1c31' "$calls"
assert_contains '--force is passed (without it --session only previews)' '--force' "$calls"
assert_contains 'whoami is asked for json, not scraped' 'whoami --json' "$calls"
assert_not_contains 'no list sweep, no per-id loop' 'list -s in_progress' "$calls"

# THE trap: a tick that finished cleanly holds nothing, and td reports that
# with exit 1. Discriminating on exit status turns the COMMON case into a
# spurious FAILED line on nearly every limit.
make_td_stub '{"error":{"code":"not_found","message":"no session ses_2a1c31 in this database, and no in_progress claim names it"}}' 1
out="$(release_tick_claim 3 'port-slot3-x' 2>&1)"
assert_contains 'holding nothing is reported as nothing to release' 'nothing to release' "$out"
assert_not_contains 'and NOT as a failure' 'FAILED' "$out"

make_td_stub '{"action":"released_session_claims","count":0,"claims":[],"unresolved":[]}' 0
out="$(release_tick_claim 3 'port-slot3-x' 2>&1)"
assert_contains 'count 0 is also nothing to release' 'nothing to release' "$out"
assert_not_contains 'count 0 is not a failure either' 'FAILED' "$out"

make_td_stub '{"error":{"code":"database_locked"}}' 1
out="$(release_tick_claim 3 'port-slot3-x' 2>&1)"
assert_contains 'a real error IS reported as a failure' 'FAILED to release' "$out"
assert_contains 'and tells the operator to act' 'by hand' "$out"

printf '#!/bin/sh\ncase "$1" in whoami) echo %s ;; esac\n' "'{\"action\":\"whoami\"}'" >"$STUB/td"
chmod +x "$STUB/td"
out="$(release_tick_claim 3 'port-slot3-x' 2>&1)"
assert_contains 'an unmappable ctx is skipped, loudly' 'claim-release skipped' "$out"

# ---------------------------------------------------------------- handle_limit
echo '--- handle_limit ties the three effects together ---'
make_td_stub '{"error":{"code":"not_found"}}' 1
LIMIT_MATCH_LINE="You've hit your session limit · resets 4pm (America/Los_Angeles)"
LIMIT_MATCH_PATTERN="You've (hit|reached) your [^.]*limit"
LIMIT_MATCH_SOURCE="structured"
out="$(handle_limit 2 5 'port-slot2-y' "$LIMIT_EPOCH" 'pattern' 2>&1)"
assert_contains 'the log line carries the grep-able LIMIT token' 'LIMIT harness=codex' "$out"
assert_contains 'and the reset time' '2026-07-29T09:00:00Z' "$out"
assert_contains 'and which stage detected it' 'stage=structured' "$out"
assert_contains 'and which pattern claimed it' "source=You've" "$out"
assert_eq 'state is recorded for the resume' "$LIMIT_EPOCH" "$(limit_state_reset_at)"
assert_eq 'with the slot that hit it' 2 "$(jq -r .slot "$STATE")"
assert_contains 'and the claim release is attempted' 'nothing to release' "$out"
out="$(handle_limit 2 5 'port-slot2-y' "$LIMIT_EPOCH" 'pattern' 0 2>&1)"
assert_contains 'an unsafe branch retains its tick claim' \
  'retaining tick claim because its worktree branch could not be released' "$out"
clear_limit_state
PATH="$OLDPATH"

# ---------------------------------------------------------------- L7
echo '--- L7: a failed rescue is reported as one, and no tick starts dirty ---'
REPO_ROOT="$TMPROOT/repo"
SLOTS_DIR="$TMPROOT/slots"
DEFAULT_BRANCH=main
mkdir -p "$REPO_ROOT"
( cd "$REPO_ROOT"
  git init -q -b main .
  git config user.email t@t; git config user.name t
  echo seed >seed.txt; git add -A; git commit -qm seed ) >/dev/null 2>&1

prepare_worktree 1 >/dev/null 2>&1
assert_eq 'the worktree is created' 'yes' "$(exists "$SLOTS_DIR/slot-1")"

out="$(release_worktree_branch 9 "$SLOTS_DIR/missing-slot" 2>&1)"; rc=$?
assert_eq 'a missing worktree fails branch release closed' 1 "$rc"
assert_contains 'and explains that its claim must be retained' \
  'retaining any tick claim' "$out"

# A clean commit made on detached HEAD is invisible to status --porcelain.
# Before the fix, prepare_worktree reset it away without creating any ref.
( cd "$SLOTS_DIR/slot-1"
  echo 'completed tick' >committed.txt
  git add committed.txt
  git commit -qm 'detached tick result' )
DETACHED_HEAD="$(git -C "$SLOTS_DIR/slot-1" rev-parse HEAD)"
out="$(release_worktree_branch 1 "$SLOTS_DIR/slot-1" 2>&1)"
assert_contains 'a clean detached commit is protected when its tick exits' \
  'protected detached commit' "$out"
DETACHED_RESCUE="$(git -C "$REPO_ROOT" branch --contains "$DETACHED_HEAD" \
  --list 'port/rescue/*-committed*' --format '%(refname:short)' | head -1)"
assert_eq 'the detached rescue branch exists' 1 \
  "$(printf '%s' "$DETACHED_RESCUE" | grep -c 'port/rescue/')"
assert_eq 'and it contains the committed work' 'completed tick' \
  "$(git -C "$REPO_ROOT" show "$DETACHED_RESCUE:committed.txt" 2>/dev/null)"
out="$(prepare_worktree 1 2>&1)"
assert_eq 'the slot resets to the default branch afterwards' \
  "$(git -C "$REPO_ROOT" rev-parse main)" \
  "$(git -C "$SLOTS_DIR/slot-1" rev-parse HEAD)"

# Handoffs become claimable before the next prepare_worktree call. A clean
# branch therefore has to be released immediately when its tick exits.
( cd "$SLOTS_DIR/slot-1"
  git switch -qc port/releasable
  echo 'slice result' >slice.txt
  git add slice.txt
  git commit -qm 'slice result' )
SLICE_HEAD="$(git -C "$SLOTS_DIR/slot-1" rev-parse HEAD)"
out="$(release_worktree_branch 1 "$SLOTS_DIR/slot-1" 2>&1)"
assert_contains 'a clean slice branch is released after the tick' \
  'released worktree branch port/releasable' "$out"
assert_eq 'the slot is detached after releasing the slice branch' '' \
  "$(git -C "$SLOTS_DIR/slot-1" symbolic-ref -q --short HEAD 2>/dev/null)"
assert_eq 'and the slice commit remains on its durable branch' "$SLICE_HEAD" \
  "$(git -C "$REPO_ROOT" rev-parse port/releasable)"

# An interrupted dirty slice must also be freed before a usage-limit handler
# can expose its claim to another slot. Its partial state remains inspectable.
git -C "$SLOTS_DIR/slot-1" switch -qc port/dirty-slice
echo 'interrupted slice' >"$SLOTS_DIR/slot-1/dirty.txt"
out="$(release_worktree_branch 1 "$SLOTS_DIR/slot-1" 2>&1)"
assert_contains 'a dirty slice is rescued immediately after the tick' \
  'rescued dirty worktree' "$out"
assert_contains 'and its original branch is released' \
  'released worktree branch port/dirty-slice' "$out"
assert_eq 'the dirty slice slot is detached' '' \
  "$(git -C "$SLOTS_DIR/slot-1" symbolic-ref -q --short HEAD 2>/dev/null)"
DIRTY_RESCUE="$(printf '%s\n' "$out" | sed -n \
  's/.*rescued dirty worktree to \([^ ]*\).*/\1/p' | tail -1)"
assert_eq 'the dirty rescue contains the interrupted state' 'interrupted slice' \
  "$(git -C "$REPO_ROOT" show "$DIRTY_RESCUE:dirty.txt" 2>/dev/null)"

echo 'half-finished work' >"$SLOTS_DIR/slot-1/wip.txt"
out="$(prepare_worktree 1 2>&1)"
assert_contains 'a successful rescue says so' 'rescued dirty worktree' "$out"
RESCUE="$(printf '%s\n' "$out" | sed -n \
  's/.*rescued dirty worktree to \([^ ]*\).*/\1/p' | tail -1)"
assert_eq 'the rescue branch exists' 1 "$(printf '%s' "$RESCUE" | grep -c 'port/rescue/')"
assert_eq 'and it actually contains the work' 'half-finished work' \
  "$(git -C "$REPO_ROOT" show "$RESCUE:wip.txt" 2>/dev/null)"
assert_eq 'the tree is clean afterwards' '' "$(git -C "$SLOTS_DIR/slot-1" status --porcelain)"

# A pre-commit hook was one of the two ways the rescue commit failed in
# practice (an unset user.email was the other). Both are now impossible.
HOOKS="$(git -C "$SLOTS_DIR/slot-1" rev-parse --git-common-dir)/hooks"
mkdir -p "$HOOKS"
printf '#!/bin/sh\nexit 1\n' >"$HOOKS/pre-commit"; chmod +x "$HOOKS/pre-commit"
echo 'more work' >"$SLOTS_DIR/slot-1/wip2.txt"
out="$(prepare_worktree 1 2>&1)"
assert_contains 'a pre-commit hook cannot block the salvage commit' \
  'rescued dirty worktree' "$out"
assert_eq 'and the tree is still clean for the next tick' '' \
  "$(git -C "$SLOTS_DIR/slot-1" status --porcelain)"
rm -f "$HOOKS/pre-commit"

# Now force the rescue to fail outright and assert the two things that matter:
# the log tells the truth, and the next tick does not start dirty. The old code
# logged "rescued …" from OUTSIDE the && chain — it claimed success, left an
# empty branch, and because git carries the index across `checkout --detach`
# the next tick began on someone else's half-finished work.
mkdir -p "$TMPROOT/nogit"
printf '#!/bin/sh\nfor a in "$@"; do [ "$a" = "-b" ] && exit 1; done\nexec %s "$@"\n' \
  "$(command -v git)" >"$TMPROOT/nogit/git"
chmod +x "$TMPROOT/nogit/git"
echo 'doomed work' >"$SLOTS_DIR/slot-1/wip3.txt"
out="$(PATH="$TMPROOT/nogit:$PATH" prepare_worktree 1 2>&1)"
assert_contains 'a failed rescue is reported as FAILED' 'FAILED to rescue' "$out"
assert_not_contains 'and NOT as a success' 'rescued dirty worktree' "$out"
assert_contains 'and the backstop says what it did' 'still dirty' "$out"
assert_eq 'and the next tick starts on a clean tree regardless' '' \
  "$(git -C "$SLOTS_DIR/slot-1" status --porcelain)"

# ---------------------------------------------------------------- L12: land after tick
echo '--- L12: run_land_auto runs porting/land --auto from THIS checkout, never signals it ---'
LOG_DIR="$TMPROOT/land-logs"; mkdir -p "$LOG_DIR"
LAND_STUB_DIR="$TMPROOT/land-stub"; mkdir -p "$LAND_STUB_DIR"
LAND_AFTER_TICK=1
LAND_AUTO_TIMEOUT=2

# A stub `land` that mimics the real one just enough for this wrapper: records
# what it was invoked with, then does whatever the test body (appended below)
# asks.
write_land_stub() { # write_land_stub <name> <body>
  local f="$LAND_STUB_DIR/$1"
  { printf '#!/usr/bin/env bash\n'
    printf 'printf "%%s|%%s|%%s\\n" "$1" "$TASKS_REPO" "$(pwd)" >>"%s/calls.log"\n' "$LAND_STUB_DIR"
    printf '%s\n' "$2"
  } >"$f"
  chmod +x "$f"
}

reset_land_test() { rm -f "$LOG_DIR/loop.log" "$LAND_STUB_DIR/calls.log"; }

# (a) nothing eligible ("no port/* branches" quiet exit): no log line at all.
write_land_stub none 'echo "auto: no port/* branches exist; nothing to land"; exit 0'
LAND_BIN="$LAND_STUB_DIR/none"
reset_land_test
run_land_auto 1 1
[ -s "$LOG_DIR/loop.log" ] \
  && bad "L12(a) nothing-eligible logged something: $(cat "$LOG_DIR/loop.log")" \
  || ok "L12(a) nothing-eligible produced no log line"

# (b) everything skipped (landed=0 failed=0 skipped=N): still quiet — same
# common case, just discovered a different way.
write_land_stub skipped 'echo "auto: done — landed=0 failed=0 skipped=3"; exit 0'
LAND_BIN="$LAND_STUB_DIR/skipped"
reset_land_test
run_land_auto 1 1
[ -s "$LOG_DIR/loop.log" ] \
  && bad "L12(b) all-skipped logged something: $(cat "$LOG_DIR/loop.log")" \
  || ok "L12(b) all-skipped produced no log line"

# (c) something actually landed: one grep-able line, naming slot/tick/summary.
write_land_stub landed 'echo "auto: done — landed=2 failed=0 skipped=1"; exit 0'
LAND_BIN="$LAND_STUB_DIR/landed"
reset_land_test
run_land_auto 3 7
assert_contains 'L12(c) a landed summary is logged' \
  'landed=2 failed=0 skipped=1' "$(cat "$LOG_DIR/loop.log" 2>/dev/null)"
assert_contains 'L12(c) the log line carries slot and tick' \
  'slot=3 tick=7' "$(cat "$LOG_DIR/loop.log" 2>/dev/null)"

# (d) a failure must be logged even though nothing landed.
write_land_stub failed 'echo "auto: done — landed=0 failed=1 skipped=0"; exit 1'
LAND_BIN="$LAND_STUB_DIR/failed"
reset_land_test
run_land_auto 2 2
assert_contains 'L12(d) a failed summary is logged even with landed=0' \
  'landed=0 failed=1 skipped=0' "$(cat "$LOG_DIR/loop.log" 2>/dev/null)"

# (e) missing/non-executable LAND_BIN: one clear line, never a crash.
LAND_BIN="$LAND_STUB_DIR/does-not-exist"
reset_land_test
run_land_auto 1 1
assert_contains 'L12(e) a missing land binary is logged' \
  'missing or not executable' "$(cat "$LOG_DIR/loop.log" 2>/dev/null)"

# (f) LAND_AFTER_TICK=0 disables it entirely: never invoked, never logged.
write_land_stub should-not-run 'exit 0'
LAND_BIN="$LAND_STUB_DIR/should-not-run"
LAND_AFTER_TICK=0
reset_land_test
run_land_auto 1 1
[ -f "$LAND_STUB_DIR/calls.log" ] \
  && bad "L12(f) LAND_AFTER_TICK=0 still invoked land" \
  || ok "L12(f) LAND_AFTER_TICK=0 never invokes land"
[ -s "$LOG_DIR/loop.log" ] \
  && bad "L12(f) LAND_AFTER_TICK=0 logged something" \
  || ok "L12(f) LAND_AFTER_TICK=0 produced no log line"
LAND_AFTER_TICK=1

# (g) invocation shape: --auto as $1, TASKS_REPO forced to REPO_ROOT, and it
# runs from REPO_ROOT's own cwd — never a worktree's copy of the script.
LAND_BIN="$LAND_STUB_DIR/none"
reset_land_test
run_land_auto 1 1
call_line="$(cat "$LAND_STUB_DIR/calls.log" 2>/dev/null)"
assert_eq 'L12(g) invoked with --auto' '--auto' "$(printf '%s' "$call_line" | cut -d'|' -f1)"
assert_eq 'L12(g) TASKS_REPO forced to REPO_ROOT' "$REPO_ROOT" "$(printf '%s' "$call_line" | cut -d'|' -f2)"
assert_eq 'L12(g) runs with REPO_ROOT as cwd' "$REPO_ROOT" "$(printf '%s' "$call_line" | cut -d'|' -f3)"

# (h) running past LAND_AUTO_TIMEOUT: run_land_auto must return promptly
# (bounded by the timeout, not by how long the stub actually takes), must NOT
# kill the stub (it keeps running and finishes on its own), and must log that
# it is moving on without signaling it.
write_land_stub slow 'sleep 6; touch "'"$LAND_STUB_DIR"'/slow-finished"'
LAND_BIN="$LAND_STUB_DIR/slow"
reset_land_test; rm -f "$LAND_STUB_DIR/slow-finished"
t0=$SECONDS
run_land_auto 1 1
t1=$SECONDS
if [ $((t1 - t0)) -lt 5 ]; then
  ok "L12(h) returns promptly, bounded by LAND_AUTO_TIMEOUT (${LAND_AUTO_TIMEOUT}s), not the stub's own runtime"
else
  bad "L12(h) took $((t1 - t0))s — did not return at LAND_AUTO_TIMEOUT"
fi
assert_contains 'L12(h) logs that it is moving on without signaling it' \
  'not signaling it' "$(cat "$LOG_DIR/loop.log" 2>/dev/null)"
[ -f "$LAND_STUB_DIR/slow-finished" ] \
  && bad "L12(h) the stub had already finished — the test's own timing is too tight, not a real result" \
  || ok "L12(h) the stub was left running (not signaled) at the moment run_land_auto returned"
sleep 5
[ -f "$LAND_STUB_DIR/slow-finished" ] \
  && ok "L12(h) the stub went on to finish undisturbed in the background" \
  || bad "L12(h) the stub never finished — something killed it after all"
LAND_AUTO_TIMEOUT=2

# ---------------------------------------------------------------- overrides
echo '--- pattern override file ---'
OVERRIDE_DIR="$TMPROOT/override"; mkdir -p "$OVERRIDE_DIR"
PORTING_DIR="$OVERRIDE_DIR"
printf '# a vendor changed its wording\nquota exhausted for this window\n\n' \
  >"$OVERRIDE_DIR/limit-patterns.claude"
if detect_in claude 1 "$(as_claude_error 'error: quota exhausted for this window')"; then
  ok 'the override file is used'
else
  bad 'the override file was ignored'
fi
if detect_in claude 1 "$(as_claude_error "You've hit your usage limit")"; then
  bad 'the override did not REPLACE the built-in list'
else
  ok 'the override replaces the built-in list'
fi
assert_eq 'comments and blank lines are stripped' 'quota exhausted for this window' \
  "$(limit_patterns claude)"
PORTING_DIR=""

# ---------------------------------------------------------------- source guard
echo '--- sourcing loop.sh does not run the supervisor ---'
# REPO_ROOT and friends are reassigned above, so re-check in a fresh shell.
out="$(bash -c '. "'"$DIR"'/loop.sh"; printf "[%s][%s]" "$REPO_ROOT" "$TIMEOUT_BIN"' 2>&1)"
assert_eq 'setup_paths and preflight did not run on source' '[][]' "$out"

# ---------------------------------------------------------------- prompt claim lifecycle
echo '--- prompt preserves saved handoffs and rolls back failed branch setup ---'
PROMPT="$DIR/PORTING.md"
ORIENT_BLOCK="$(sed -n '/1\. \*\*Orient/,/2\. \*\*Claim/p' "$PROMPT")"
CLAIM_BLOCK="$(sed -n '/2\. \*\*Claim/,/3\. \*\*Work/p' "$PROMPT")"
assert_contains 'saved handoff query requires open porting slices with handoffs' \
  "td query 'status = open AND handoff.remaining ~ \"\" AND labels ~ \"porting-slice\"' --json" \
  "$ORIENT_BLOCK"
assert_before 'saved partial handoffs precede generic ready work' \
  'handoff.remaining ~ ""' 'the next ready slice' "$ORIENT_BLOCK"
assert_contains 'the prompt names the real open state of a partial handoff' \
  'handoff is open after' "$ORIENT_BLOCK"
assert_contains 'rollback covers branch switch and resumed commit verification' \
  'If any branch switch or resumed' "$CLAIM_BLOCK"
assert_contains 'rollback applies only after a successful claim' \
  'commit verification fails after `td start`' "$CLAIM_BLOCK"
assert_before 'branch setup rollback detaches before releasing the claim' \
  'git switch --detach' 'td unstart' "$CLAIM_BLOCK"
assert_contains 'claim release is conditional on successful detachment' \
  'Only after detachment succeeds' "$CLAIM_BLOCK"
assert_contains 'branch setup failure releases the exact claimed issue' \
  'td unstart' "$CLAIM_BLOCK"
assert_contains 'claim rollback targets id and records its reason' \
  '<id> --reason "Branch' "$CLAIM_BLOCK"
assert_contains 'claim rollback records the branch setup failure reason' \
  'setup failed after claim"' "$CLAIM_BLOCK"
assert_contains 'claim rollback verifies open before another choice' \
  'issue is open before choosing' "$CLAIM_BLOCK"
assert_contains 'failed detachment retains the claim' \
  'If detachment fails, retain the claim' "$CLAIM_BLOCK"
assert_contains 'failed detachment exits instead of exposing the branch' \
  'never expose a claimable handoff whose branch is' "$CLAIM_BLOCK"

# The query is a cross-entity contract, not just prompt spelling. `has()` only
# checks issue fields and silently returned no handoffs, so exercise a real td
# database with both a saved open handoff and an otherwise-ready porting slice.
HANDOFF_REPO="$TMPROOT/handoff-query-repo"
mkdir -p "$HANDOFF_REPO"
git -C "$HANDOFF_REPO" init -q -b main
git -C "$HANDOFF_REPO" config user.name test
git -C "$HANDOFF_REPO" config user.email test@example.invalid
touch "$HANDOFF_REPO/seed"
git -C "$HANDOFF_REPO" add seed
git -C "$HANDOFF_REPO" commit -qm seed
td -w "$HANDOFF_REPO" init >/dev/null 2>&1
SAVED_ID="$(TD_CONTEXT_ID=prompt-query-test td -w "$HANDOFF_REPO" \
  create 'saved partial slice' --type task --labels porting-slice --json \
  | jq -r '.issue.id')"
TD_CONTEXT_ID=prompt-query-test td -w "$HANDOFF_REPO" \
  create 'generic ready slice' --type task --labels porting-slice >/dev/null 2>&1
DONE_ONLY_ID="$(TD_CONTEXT_ID=prompt-query-test td -w "$HANDOFF_REPO" \
  create 'done-only partial slice' --type task --labels porting-slice --json \
  | jq -r '.issue.id')"
TD_CONTEXT_ID=prompt-query-test td -w "$HANDOFF_REPO" start "$SAVED_ID" >/dev/null 2>&1
TD_CONTEXT_ID=prompt-query-test td -w "$HANDOFF_REPO" \
  handoff "$SAVED_ID" --remaining 'translate' >/dev/null 2>&1
TD_CONTEXT_ID=prompt-query-test td -w "$HANDOFF_REPO" \
  unstart "$SAVED_ID" --reason 'handoff saved' >/dev/null 2>&1
TD_CONTEXT_ID=prompt-query-test td -w "$HANDOFF_REPO" start "$DONE_ONLY_ID" >/dev/null 2>&1
TD_CONTEXT_ID=prompt-query-test td -w "$HANDOFF_REPO" \
  handoff "$DONE_ONLY_ID" --done 'oracle captured' >/dev/null 2>&1
TD_CONTEXT_ID=prompt-query-test td -w "$HANDOFF_REPO" \
  unstart "$DONE_ONLY_ID" --reason 'handoff saved' >/dev/null 2>&1
HANDOFF_QUERY='status = open AND handoff.remaining ~ "" AND labels ~ "porting-slice"'
QUERY_IDS="$(td -w "$HANDOFF_REPO" query "$HANDOFF_QUERY" --json \
  | jq -r 'map(.id) | sort | join(",")')"
EXPECTED_IDS="$(printf '%s\n%s\n' "$SAVED_ID" "$DONE_ONLY_ID" | sort | paste -sd, -)"
assert_eq 'the prompt query finds both handoff shapes and excludes generic ready work' \
  "$EXPECTED_IDS" "$QUERY_IDS"

echo
echo "passed: $PASS   failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
