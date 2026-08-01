#!/usr/bin/env bash
#
# test-loop-limits.sh — proves loop.sh's usage-limit logic by construction.
#
# loop.sh has no full test suite and does not need one: it is a supervisor,
# and almost everything it does is observable by running it. The exception is
# the limit machinery, whose whole job is to fire on strings we hope never to
# see, at times we cannot schedule. So the two pure parts — pattern matching
# and reset-time parsing — are exercised here, with a pinned clock.
#
#   porting/test-loop-limits.sh          # exits 0 on success
#
# Sourcing loop.sh must not start the fleet; that guard is itself the first
# thing this file depends on, and the last assertion checks it explicitly.
set -uo pipefail

export TZ=UTC          # form 3 resolves a local wall clock; pin the zone
DIR="$(cd "$(dirname "$0")" && pwd)"

# Sourcing must define functions and run nothing. If the guard in loop.sh
# breaks, this line runs preflight and hangs or dies — a loud failure.
# shellcheck source=/dev/null
. "$DIR/loop.sh"

PASS=0; FAIL=0

ok()   { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad()  { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

assert_eq() { # assert_eq <label> <expected> <actual>
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (expected '$2', got '$3')"; fi
}

assert_match() { # assert_match <harness> <line>
  if detect_limit_text "$1" "$2"; then
    ok "$1 detects: $2"
  else
    bad "$1 MISSED: $2"
  fi
}

assert_no_match() { # assert_no_match <harness> <line>
  if detect_limit_text "$1" "$2"; then
    bad "$1 false positive on: $2 (pattern: $LIMIT_MATCH_PATTERN)"
  else
    ok "$1 ignores: $2"
  fi
}

# detect_limit reads files; give it one so the test exercises the same path
# the supervisor uses (tail + combined grep), not a simplified stand-in.
detect_limit_text() { # detect_limit_text <harness> <text>
  local f rc
  f="$(mktemp -t looplimit)"
  printf 'starting tick\nsome ordinary output\n%s\ntick over\n' "$2" >"$f"
  detect_limit "$1" "$f"; rc=$?
  rm -f "$f"
  return $rc
}

# ---------------------------------------------------------------- patterns
echo '--- detection: claude ---'
assert_match claude 'Claude AI usage limit reached|1785337200'
assert_match claude 'Claude AI usage limit reached. Your limit will reset at 3pm.'
assert_match claude 'Please upgrade to increase your usage limit.'
assert_match claude '{"error":{"type": "rate_limit_error","message":"..."}}'
assert_match claude 'API error 429: rate limit exceeded'
assert_match claude 'HTTP 429 Too Many Requests'

echo '--- detection: codex ---'
assert_match codex "You've hit your usage limit. Try again at 9:30am."
assert_match codex 'stream error: usage_limit_exceeded'
assert_match codex 'Goal hit usage limits; stopping.'
assert_match codex 'openai: 429 rate limit reached for gpt'

echo '--- detection: ordinary tick output must not trip it ---'
for h in claude codex; do
  assert_no_match "$h" 'raising the --tick-timeout limit to 7200s'
  assert_no_match "$h" 'the manifest limits this slice to one file'
  assert_no_match "$h" 'store: enforce the archive size limit'
  assert_no_match "$h" '429 files changed, 12 insertions'
  assert_no_match "$h" 'rate limit headroom looks fine'
  assert_no_match "$h" 'td list -s in_progress -n 20   # limit the page'
done

# ---------------------------------------------------------------- reset times
# NOW is pinned to 2026-07-29T08:00:00Z. Every expectation below is a literal
# epoch, computed once against that instant — no re-deriving it from the code
# under test.
NOW=1785312000
echo '--- reset parsing (NOW = 2026-07-29T08:00:00Z) ---'

# 1. epoch form
assert_eq 'epoch |form' 1785337200 \
  "$(parse_reset_at 'Claude AI usage limit reached|1785337200' "$NOW")"

# 2. ISO-8601, Z and offset
assert_eq 'iso Z' 1785594600 \
  "$(parse_reset_at 'usage limit reached; resets 2026-08-01T14:30:00Z' "$NOW")"
assert_eq 'iso +05:00' 1785576600 \
  "$(parse_reset_at 'usage limit reached; resets 2026-08-01T14:30:00+05:00' "$NOW")"

# 3. wall clock, today and rolled to tomorrow
assert_eq 'try again at 3pm' 1785337200 \
  "$(parse_reset_at "You've hit your usage limit. Try again at 3pm." "$NOW")"
assert_eq 'resets at 09:30' 1785317400 \
  "$(parse_reset_at 'usage limit reached; resets at 09:30' "$NOW")"
assert_eq 'try again at 7am rolls to tomorrow' 1785394800 \
  "$(parse_reset_at 'usage limit reached. try again at 7am' "$NOW")"

# 4. relative
assert_eq 'resets in 2h30m' $((NOW + 9000)) \
  "$(parse_reset_at 'usage limit reached; resets in 2h30m' "$NOW")"
assert_eq 'in 5 hours' $((NOW + 18000)) \
  "$(parse_reset_at 'usage limit reached; try again in 5 hours' "$NOW")"
assert_eq 'in 45 minutes' $((NOW + 2700)) \
  "$(parse_reset_at 'usage limit reached; try again in 45 minutes' "$NOW")"

# unparseable -> nothing, so the caller falls back
assert_eq 'unparseable yields nothing' '' \
  "$(parse_reset_at 'usage limit reached, sorry' "$NOW")"
assert_eq 'unparseable returns non-zero' 1 \
  "$(parse_reset_at 'usage limit reached, sorry' "$NOW" >/dev/null; echo $?)"

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

# ---------------------------------------------------------------- overrides
echo '--- pattern override file ---'
OVERRIDE_DIR="$(mktemp -d -t loopoverride)"
PORTING_DIR="$OVERRIDE_DIR"
printf '# a vendor changed its wording\nquota exhausted for this window\n\n' \
  >"$OVERRIDE_DIR/limit-patterns.claude"
assert_match claude 'error: quota exhausted for this window'
assert_no_match claude 'Claude AI usage limit reached|1785337200'
PORTING_DIR=""
rm -rf "$OVERRIDE_DIR"

# ---------------------------------------------------------------- source guard
echo '--- sourcing loop.sh does not run the supervisor ---'
# If main ran on source, it would have set REPO_ROOT and created log dirs.
assert_eq 'setup_paths did not run' '' "$REPO_ROOT"
assert_eq 'preflight did not run' '' "$TIMEOUT_BIN"

echo
echo "passed: $PASS   failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
