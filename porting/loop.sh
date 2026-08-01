#!/usr/bin/env bash
#
# loop.sh — process manager for the Go-port agent fleet.
#
# This script is deliberately dumb. It owns:
#   - isolation:  one git worktree per slot, shared object store and refs
#   - scheduling: ticks, idle backoff, stagger, STOP file
#   - limits:     wall-clock timeout per tick, per-tick transcript logs, and
#                 subscription usage limits (park until the window resets)
# It never chooses slices or roles, and for Claude it never chooses the
# working model either — the Opus-class tick orchestrator delegates down.
# For Codex (flat, no per-subagent override) the script picks the tick
# model from one observable fact: review work waiting => top tier.
# Design rationale: docs/plans/active/tasks-go-port-fleet-ops.md.
#
# There is no dollar or quota accounting here on purpose: the harnesses run
# on subscriptions, so there is no per-tick price to cap and no reason to
# ration ticks per calendar day. What actually stops the fleet is a usage
# limit, which exhausts unpredictably and then resets at a known time — so it
# is handled the way STOP is: a clean halt at a tick boundary, with enough
# recorded state to resume when the window reopens.
#
# Usage:
#   loop.sh [--harness codex|claude] [-n SLOTS] [--once] [--dry-run]
#           [--max-ticks N] [--tick-timeout SECS] [--on-limit park|stop]
#
#   --harness       codex (default) or claude
#   -n              parallel slots (default 1); each gets its own worktree
#   --once          run a single tick on slot 1, then exit (debugging)
#   --dry-run       everything except invoking the harness
#   --max-ticks N   stop a slot after N ticks (default 0 = unlimited)
#   --tick-timeout SECS  kill a tick after this long (default 3600)
#   --on-limit      on a usage limit: park (default) sleeps until the window
#                   resets and resumes; stop exits the slot cleanly with
#                   status 0 — including at startup, when preflight finds a
#                   limit already in force. A usage limit is a wait, not a
#                   failure, and it reports the same status either way.
#                   --once never parks: it reports the limit and exits.
#
# Halt all slots cleanly:  touch porting/STOP   (remove it to allow restart)
#
# Env overrides:
#   TASKS_REPO      repo root (default: toplevel of this script's checkout)
#   SLOTS_DIR       where slot worktrees live (default: <repo>-port-slots)
#   DEFAULT_BRANCH  branch slices land on (default: main)
#   CLAUDE_MODEL    tick model for claude (default: opus)
#   CODEX_TOP / CODEX_MID   codex tier models (default: sol / terra)
#   CODEX_SANDBOX   codex sandbox mode (default: danger-full-access — td's
#                   database (.todos/) and its config live outside the slot
#                   worktree, so workspace-write would need --add-dir for the
#                   repo root and ~/.config/td; tighten once proven)
#   LIMIT_COOLDOWN  wait when no reset time is parseable (default 1800s)
#   LIMIT_MAX_WAIT  cap on the doubling fallback AND on any reset time parsed
#                   out of a vendor message (default 21600s = 6h)
#   LIMIT_GRACE     slack added to a parsed reset time (default 60s)
#   LIMIT_PROBE     optional pre-tick command; exit 3 = limited, and a bare
#                   epoch on its stdout is taken as the reset time
#
# Usage-limit detection is two-stage, and the order matters:
#
#   1. STRUCTURED (primary). Both harnesses are invoked in JSONL mode
#      (`codex exec --json`, `claude -p --output-format stream-json`), so the
#      harness's OWN error records are separable from the agent's output. Only
#      those records are matched. Agent-authored text — including this file,
#      which necessarily contains every trigger string — can never reach them.
#   2. PROSE (fallback), for a vendor that emits a limit with no typed error
#      record. This one greps the whole transcript, so it is gated on the tick
#      exiting non-zero and not on 124 (our own `timeout` kill): both
#      harnesses exit non-zero on a mid-run API error, and a tick that merely
#      *read* a file full of limit strings exits 0.
#
# Both stages share one pattern list per harness, overridable without touching
# this script: put one extended regex per line in
# porting/limit-patterns.<harness> and it replaces the built-in list for that
# harness. When a vendor changes its wording, the fix is a text file, not a
# script edit.
#
# Repo prerequisites (one-time): porting/PORTING.md exists; porting/logs/
# and porting/STOP are gitignored.
#
# Deliberately NOT set -e: this is a long-running supervisor, and a transient
# td/jq/git failure inside one tick must not take down the fleet. Errors are
# handled explicitly; preflight is the strict part.
set -uo pipefail

# ---------------------------------------------------------------- defaults
HARNESS="codex"
SLOTS=1
ONCE=0
DRY_RUN=0
MAX_TICKS=0
TICK_TIMEOUT=3600
ON_LIMIT="park"
PAUSE=15              # seconds between ticks when work is flowing
IDLE_THRESHOLD=120    # a tick shorter than this looks idle -> back off
MAX_PAUSE=900
STAGGER=10            # seconds between slot startups
PARK_POLL=30          # sleep granularity while parked (keeps STOP responsive)
PARK_HEARTBEAT=900    # log a still-parked line at least this often

DEFAULT_BRANCH="${DEFAULT_BRANCH:-main}"
CLAUDE_MODEL="${CLAUDE_MODEL:-opus}"
CODEX_TOP="${CODEX_TOP:-sol}"
CODEX_MID="${CODEX_MID:-terra}"
CODEX_SANDBOX="${CODEX_SANDBOX:-danger-full-access}"

LIMIT_COOLDOWN="${LIMIT_COOLDOWN:-1800}"
LIMIT_MAX_WAIT="${LIMIT_MAX_WAIT:-21600}"
LIMIT_GRACE="${LIMIT_GRACE:-60}"
LIMIT_PROBE="${LIMIT_PROBE:-}"

# Declared up front so `set -u` is safe when porting/test-loop-limits.sh
# sources this file and calls individual functions.
SCRIPT_DIR=""
REPO_ROOT=""
PORTING_DIR=""
LOG_DIR=""
STOP_FILE=""
SLOTS_DIR="${SLOTS_DIR:-}"
PROMPT_FILE=""
TIMEOUT_BIN=""
BOOTSTRAP=""
LAST_TICK_DUR=0
LIMIT_MATCH_LINE=""
LIMIT_MATCH_PATTERN=""
LIMIT_MATCH_SOURCE=""
LIMIT_PROBE_RESET=""
# Per-slot, deliberately; see the LIMIT_STREAK note in OPERATING.md.
LIMIT_STREAK=0

log() {
  local line
  line="$(printf '%s %s' "$(date '+%Y-%m-%dT%H:%M:%S')" "$*")"
  if [ -n "$LOG_DIR" ] && [ -d "$LOG_DIR" ]; then
    printf '%s\n' "$line" | tee -a "$LOG_DIR/loop.log" >&2
  else
    printf '%s\n' "$line" >&2
  fi
}

die() { echo "loop.sh: $*" >&2; exit 1; }

# ---------------------------------------------------------------- time helpers
# BSD (macOS) date takes -r <epoch> and -j -f <fmt>; GNU date takes -d. Try
# BSD first and fall back, so the same script works on a Linux runner. Never
# reach for `date -d` alone — that is the trap on macOS.
fmt_epoch() { # fmt_epoch <epoch> <strftime-format> [tz]
  local z="${3:-}"
  if [ -n "$z" ]; then
    TZ="$z" date -r "$1" "+$2" 2>/dev/null || TZ="$z" date -d "@$1" "+$2" 2>/dev/null
  else
    date -r "$1" "+$2" 2>/dev/null || date -d "@$1" "+$2" 2>/dev/null
  fi
}

iso_epoch() { # iso_epoch <epoch> -> 2026-08-01T14:30:00Z, or "unknown"
  local e="${1:-}" out
  # Never let a missing or junk epoch abort a caller under `set -u`; the whole
  # point of this function is to make a log line readable.
  case "$e" in ''|*[!0-9]*) printf 'unknown\n'; return 0 ;; esac
  out="$(date -u -r "$e" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null \
      || date -u -d "@$e" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null)"
  if [ -n "$out" ]; then printf '%s\n' "$out"; else printf 'unknown\n'; fi
}

utc_to_epoch() { # utc_to_epoch "YYYY-MM-DDTHH:MM:SS"
  date -j -u -f '%Y-%m-%dT%H:%M:%S' "$1" +%s 2>/dev/null \
    || date -u -d "$1" +%s 2>/dev/null
}

local_to_epoch() { # local_to_epoch "YYYY-MM-DD HH:MM:SS" [tz]
  local z="${2:-}"
  if [ -n "$z" ]; then
    TZ="$z" date -j -f '%Y-%m-%d %H:%M:%S' "$1" +%s 2>/dev/null \
      || TZ="$z" date -d "$1" +%s 2>/dev/null
  else
    date -j -f '%Y-%m-%d %H:%M:%S' "$1" +%s 2>/dev/null \
      || date -d "$1" +%s 2>/dev/null
  fi
}

# A vendor may name the zone its wall clock is in — the observed message was
# "You've hit your session limit · resets 4pm (America/Los_Angeles)". Honour
# it when the machine knows the zone; an unknown name is ignored rather than
# trusted, which falls back to local time.
zone_hint() { # zone_hint <text> -> IANA zone, or nothing
  local z
  z="$(printf '%s\n' "$1" | grep -oE '\([A-Za-z]+/[A-Za-z0-9_+-]+\)' | head -1 | tr -d '()')"
  [ -n "$z" ] || return 0
  [ -f "/usr/share/zoneinfo/$z" ] || return 0
  printf '%s\n' "$z"
}

# NOW exists so the tests can pin the clock; nothing else sets it.
now_epoch() { printf '%s\n' "${NOW:-$(date +%s)}"; }

# ---------------------------------------------------------------- limit patterns
# DERIVATION — read this before editing a pattern.
#
# These are not guesses and they are not the vendors' docs; they are lifted
# from the string tables of the binaries installed on this machine on
# 2026-08-01 (claude 2.1.220, codex-cli 0.146.0). To re-derive after an
# upgrade:
#
#   B=~/.local/share/claude/versions/<version>
#   strings -n 6 "$B" | grep -oE 'zUr=\[.{0,760}'
#     -> the BLOCKING list: ["You've hit your","You've reached your","You're
#        out of usage credits","Your org is out of usage · add funds to
#        continue","Your org is out of usage · contact your admin","Your seat
#        type doesn't include usage credits",…,"Your usage allocation has
#        been disabled by your admin","Your group's usage limit is set to
#        $0",…]. The arrays printed beside it are NOT blocking: YUr
#        ("You've used","You're close to") is the approaching-limit warning
#        and JUr ("You're now using usage credits",…) is a tier switch. Only
#        the 100%-consumed form of YUr is matched here.
#
#   C=~/.local/lib/node_modules/@openai/codex/node_modules/@openai/\
#     codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex
#   strings -n 6 "$C" | grep -oiE "You've (hit|reached) your[^\"]{0,70}"
#   strings -n 6 "$C" | grep -o  'usage_limit_exceeded'
#     -> usage_limit_exceeded is a variant of codex's CodexErrorInfo enum,
#        beside context_window_exceeded / server_overloaded / unauthorized.
#
# Three corrections this derivation forced, recorded so they are not
# reintroduced:
#   - "Claude AI usage limit reached" appears ZERO times in claude 2.1.220.
#     It was the old claude list's anchor and it could never have matched.
#     The real prose starts "You've hit your …" / "You've reached your …" —
#     as in the message that actually stopped an agent on this machine:
#     "You've hit your session limit · resets 4pm (America/Los_Angeles)".
#   - "You've hit your usage limit" was in the CODEX list only, and no code
#     path ever consulted the other harness's list, so the claude side had no
#     coverage of the one phrase both vendors use.
#   - "Goal hit usage limits" is dropped. It is a status label for codex's
#     interactive /goal feature ("Goal hit usage limits (/goal resume)"),
#     which `codex exec` never runs, so in a tick transcript it can only ever
#     appear as content the agent read or wrote — a pure false-positive
#     source with no true positive to justify it.
#
# These are defaults, not gospel: porting/limit-patterns.<harness> replaces
# the whole list for that harness.
limit_patterns() { # limit_patterns <harness>
  local override="$PORTING_DIR/limit-patterns.$1"
  if [ -n "$PORTING_DIR" ] && [ -f "$override" ]; then
    grep -v '^[[:space:]]*#' "$override" | grep -v '^[[:space:]]*$'
    return 0
  fi
  case "$1" in
    claude)
      echo "You've (hit|reached) your [^.]*limit"
      echo "You've (hit|reached) your (usage|extra usage)"
      echo "You've used 100% of your"
      echo "You're out of (usage credits|extra usage)"
      echo "Your org is out of usage"
      echo "Your (seat type doesn't include|usage allocation has been disabled)"
      echo "Your group's usage limit is set to"
      echo 'usage limit reached'
      echo '"type" *: *"rate_limit_error"'
      # Structured stage only: the result record's typed HTTP status, which
      # harness_error_text synthesises into this token.
      echo 'api_error_status=429'
      ;;
    codex)
      echo "You've (hit|reached) your (usage limit|workspace credit limit)"
      echo 'usage limit reached'
      echo 'workspace is out of credits'
      echo 'usage_limit_exceeded'
      ;;
  esac
  # Shared across harnesses.
  echo '429.*rate.?limit'
  echo 'rate.?limit.*429'
  echo 'too many requests'
}

# Which single pattern claimed this line — that is what the log's source=
# field names, so an operator can fix the right line of the override file.
limit_match_line() { # limit_match_line <harness> <line>
  local harness="$1" line="$2" pat
  LIMIT_MATCH_PATTERN=""
  while IFS= read -r pat; do
    [ -n "$pat" ] || continue
    if printf '%s\n' "$line" | grep -qiE -- "$pat" 2>/dev/null; then
      LIMIT_MATCH_PATTERN="$pat"
      return 0
    fi
  done <<PATTERNS
$(limit_patterns "$harness")
PATTERNS
  return 1
}

# The harness's own error records, and nothing else. Both harnesses run in
# JSONL mode, so every transcript line is a typed event: either the harness
# speaking or the agent's output wrapped in a message envelope. Only the
# former can be an error record. That separation is what makes stage 1 safe to
# run unconditionally — a tick that reads loop.sh or test-loop-limits.sh emits
# those strings inside assistant/agent-message items, which this never reads.
#
# Emits one line of text per error record. `api_error_status=<n>` is
# synthesised so a typed 429 is matchable as an ordinary pattern.
harness_error_text() { # harness_error_text <harness> <file>
  local harness="$1" f="$2"
  { [ -n "$f" ] && [ -f "$f" ]; } || return 0
  command -v jq >/dev/null 2>&1 || return 0
  case "$harness" in
    codex)
      # {"type":"error","message":…}
      # {"type":"turn.failed","error":{"message":…}}
      # {"type":"item.completed","item":{"type":"error","message":…}}
      tail -n 400 "$f" 2>/dev/null | jq -rR '
        (fromjson? // empty)
        | select(type == "object")
        | if   .type == "error"       then (.message // empty)
          elif .type == "turn.failed" then (.error.message // empty)
          elif (.type == "item.completed" and (.item.type? == "error"))
                                      then (.item.message // empty)
          else empty end
        | tostring | gsub("\n"; " ")' 2>/dev/null
      ;;
    claude)
      # {"type":"result","is_error":true,"subtype":…,"terminal_reason":…,
      #  "api_error_status":429,"result":…}
      # {"type":"assistant","is_api_error_message":true,"error":"…",
      #  "message":{"content":[{"type":"text","text":…}]}}
      tail -n 400 "$f" 2>/dev/null | jq -rR '
        (fromjson? // empty)
        | select(type == "object")
        | if (.type == "result" and (.is_error == true)) then
            [ (.result // ""), (.subtype // ""), (.terminal_reason // ""),
              (if (.api_error_status | type) == "number"
               then "api_error_status=\(.api_error_status)" else "" end) ]
            | join(" ")
          elif (.is_api_error_message == true) then
            [ (.error // ""), ([.message.content[]?.text? // empty] | join(" ")) ]
            | join(" ")
          else empty end
        | gsub("\n"; " ")' 2>/dev/null
      ;;
  esac
  return 0
}

# Is this transcript in the JSONL format harness_error_text can read? One
# parseable typed event is enough. When it is, the structured view is
# AUTHORITATIVE and the prose fallback must not run: the harness told us in
# typed form what went wrong, and re-reading the same file as flat text can
# only add false positives — a tick that read this repo's scripts and then
# exited non-zero for an unrelated reason would otherwise park the fleet.
transcript_is_structured() { # transcript_is_structured <file>
  local f="$1" n
  { [ -n "$f" ] && [ -f "$f" ]; } || return 1
  command -v jq >/dev/null 2>&1 || return 1
  n="$(tail -n 400 "$f" 2>/dev/null \
       | jq -rR '(fromjson? // empty) | select(type=="object") | select(has("type")) | 1' 2>/dev/null \
       | head -1)"
  [ -n "$n" ]
}

# Two stages; see the header for why. Stage 1 reads only typed error records
# and needs no exit-status gate. Stage 2 greps the raw transcript, so it is
# reached only when there is no structured record to read AND the tick exited
# in a way that a limit could explain.
#
# Scan only the tail of each transcript — these files get large, and a limit
# message is always the last thing the harness manages to say.
detect_limit() { # detect_limit <harness> <rc> <file>...
  local harness="$1" rc="$2"; shift 2
  local combined f line
  combined="$(limit_patterns "$harness" | grep -v '^[[:space:]]*$' | paste -sd '|' -)"
  LIMIT_MATCH_LINE=""; LIMIT_MATCH_PATTERN=""; LIMIT_MATCH_SOURCE=""
  [ -n "$combined" ] || return 1

  for f in "$@"; do
    { [ -n "$f" ] && [ -f "$f" ]; } || continue
    line="$(harness_error_text "$harness" "$f" | grep -iE -m 1 -- "$combined" 2>/dev/null)"
    [ -n "$line" ] || continue
    LIMIT_MATCH_LINE="$line"; LIMIT_MATCH_SOURCE="structured"
    limit_match_line "$harness" "$line" || LIMIT_MATCH_PATTERN="$combined"
    return 0
  done

  # Prose fallback, for a harness that emitted no JSONL at all (an older
  # build, a --json we lost, a crash before the first event). Three gates,
  # each closing a reproduced false positive:
  #   - no structured records anywhere, else stage 1 already had the answer;
  #   - rc != 0, because a tick that ran to completion only READ those strings;
  #   - rc != 124, our own `timeout` kill — a tick killed a second after
  #     reading this very file would otherwise match this file's own patterns.
  for f in "$@"; do
    transcript_is_structured "$f" && return 1
  done
  case "$rc" in ''|*[!0-9]*) return 1 ;; esac
  [ "$rc" -ne 0 ] || return 1
  [ "$rc" -ne 124 ] || return 1
  for f in "$@"; do
    { [ -n "$f" ] && [ -f "$f" ]; } || continue
    line="$(tail -n 200 "$f" 2>/dev/null | grep -iE -m 1 -- "$combined" 2>/dev/null)"
    [ -n "$line" ] || continue
    LIMIT_MATCH_LINE="$line"; LIMIT_MATCH_SOURCE="prose"
    limit_match_line "$harness" "$line" || LIMIT_MATCH_PATTERN="$combined"
    return 0
  done
  return 1
}

# ---------------------------------------------------------------- reset time
# Every parse is guarded: an unreadable timestamp must fall back to the
# cooldown, never abort the supervisor. Prints an epoch, or nothing.
parse_reset_at() { # parse_reset_at <text> [now_epoch]
  local text="$1" now="${2:-$(now_epoch)}"
  local m epoch iso off sign oh om hh mm ampm today h n zone naked stripped

  zone="$(zone_hint "$text")"

  # 1. Claude's "…|1712345678" form.
  m="$(printf '%s\n' "$text" | grep -oE '\|1[0-9]{9}' | head -1)"
  if [ -n "$m" ]; then
    printf '%s\n' "${m#|}"; return 0
  fi

  # 2. ISO-8601: Z, numeric offset, or NAKED. The component ranges are
  #    validated in the regex on purpose — `date` happily rolls 2026-13-45
  #    over into a plausible-looking instant, and a silently accepted garbage
  #    timestamp is worse than no timestamp at all.
  iso="$(printf '%s\n' "$text" \
        | grep -oE '[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9](:[0-5][0-9])?(Z|[+-][0-9]{2}:?[0-9]{2})?' \
        | head -1)"
  if [ -n "$iso" ]; then
    off=""; naked=0
    case "$iso" in
      *Z) m="${iso%Z}" ;;
      *)  off="$(printf '%s\n' "$iso" | grep -oE '[+-][0-9]{2}:?[0-9]{2}$')"
          if [ -n "$off" ]; then m="${iso%$off}"; else m="$iso"; naked=1; fi ;;
    esac
    case "$m" in *T[0-9][0-9]:[0-9][0-9]) m="$m:00" ;; esac
    if [ "$naked" = 1 ]; then
      # ZONE ASSUMPTION, stated explicitly because it is unsafe in exactly one
      # direction: a naked timestamp is read as the zone the vendor named, and
      # failing that as LOCAL time. Reading it as UTC on a machine west of UTC
      # would place the reset EARLIER than the truth, and a fleet that resumes
      # early re-hits the limit, re-parks, and — because a reset *was* parsed
      # — never climbs the backoff ladder. Local errs late, which only wastes
      # time. (Before this, a naked timestamp fell through to form 3 entirely
      # and was read as an hour-of-day taken from the YEAR.)
      epoch="$(local_to_epoch "$(printf '%s' "$m" | tr 'T' ' ')" "$zone")"
      if [ -n "$epoch" ]; then printf '%s\n' "$epoch"; return 0; fi
    else
      epoch="$(utc_to_epoch "$m")"
      if [ -n "$epoch" ]; then
        if [ -n "$off" ]; then
          sign="$(printf '%s' "$off" | cut -c1)"
          off="$(printf '%s' "$off" | cut -c2- | tr -d ':')"
          oh="$(printf '%s' "$off" | cut -c1-2)"
          om="$(printf '%s' "$off" | cut -c3-4)"
          n=$(( 10#$oh * 3600 + 10#$om * 60 ))
          # +05:00 means the printed wall clock runs 5h ahead of UTC.
          if [ "$sign" = "+" ]; then epoch=$((epoch - n)); else epoch=$((epoch + n)); fi
        fi
        printf '%s\n' "$epoch"; return 0
      fi
    fi
  fi

  # 3. "try again at 3pm" / "resets 4pm (America/Los_Angeles)" / "resets at
  #    14:30" — a wall clock in the zone the vendor named, else the local
  #    zone, rolled to tomorrow if that instant has already passed.
  #
  #    Any date-shaped run is deleted first. Without that, "resets at
  #    2026-08-02T14:00:00" reached this form and `[0-9]{1,2}` bit off the
  #    first two digits of the YEAR — "20" — yielding 20:00 TODAY, hours
  #    early. Form 2 now takes well-formed naked timestamps; anything
  #    date-shaped that form 2 REJECTED is malformed and must fall through to
  #    the cooldown, not be re-read here as an hour.
  stripped="$(printf '%s\n' "$text" \
      | sed -E 's/[0-9]{4}-[0-9]{2}-[0-9]{2}([T ][0-9]{1,2}:[0-9]{2}(:[0-9]{2})?)?//g')"
  m="$(printf '%s\n' "$stripped" \
      | grep -oiE '(try again|resets?)( at)? [0-9]{1,2}(:[0-9]{2})?[[:space:]]*(am|pm)?' \
      | head -1)"
  if [ -n "$m" ]; then
    hh="$(printf '%s\n' "$m" | grep -oE '[0-9]{1,2}(:[0-9]{2})?' | head -1)"
    ampm="$(printf '%s\n' "$m" | grep -oiE '(am|pm)[[:space:]]*$' | tr 'A-Z' 'a-z')"
    if [ -n "$hh" ]; then
      case "$hh" in *:*) h="${hh%%:*}"; mm="${hh##*:}" ;; *) h="$hh"; mm="00" ;; esac
      h="$((10#$h))"; mm="$((10#$mm))"
      case "$ampm" in
        pm) [ "$h" -lt 12 ] && h=$((h + 12)) ;;
        am) [ "$h" -eq 12 ] && h=0 ;;
      esac
      if [ "$h" -le 23 ] && [ "$mm" -le 59 ]; then
        today="$(fmt_epoch "$now" '%Y-%m-%d' "$zone")"
        if [ -n "$today" ]; then
          epoch="$(local_to_epoch "$(printf '%s %02d:%02d:00' "$today" "$h" "$mm")" "$zone")"
          if [ -n "$epoch" ]; then
            [ "$epoch" -le "$now" ] && epoch=$((epoch + 86400))
            printf '%s\n' "$epoch"; return 0
          fi
        fi
      fi
    fi
  fi

  # 4. Relative: "resets in 2h30m", "in 45 minutes", "2 hours".
  m="$(printf '%s\n' "$text" | grep -oiE '[0-9]{1,3}h[0-9]{1,2}m' | head -1)"
  if [ -n "$m" ]; then
    h="$(printf '%s\n' "$m" | sed 's/[hH].*//')"
    mm="$(printf '%s\n' "$m" | sed 's/.*[hH]//; s/[mM]//')"
    printf '%s\n' "$(( now + 10#$h * 3600 + 10#$mm * 60 ))"; return 0
  fi
  m="$(printf '%s\n' "$text" | grep -oiE '[0-9]{1,3}[[:space:]]*hours?' | head -1)"
  if [ -n "$m" ]; then
    n="$(printf '%s\n' "$m" | grep -oE '[0-9]{1,3}')"
    printf '%s\n' "$(( now + 10#$n * 3600 ))"; return 0
  fi
  m="$(printf '%s\n' "$text" | grep -oiE '[0-9]{1,4}[[:space:]]*minutes?' | head -1)"
  if [ -n "$m" ]; then
    n="$(printf '%s\n' "$m" | grep -oE '[0-9]{1,4}')"
    printf '%s\n' "$(( now + 10#$n * 60 ))"; return 0
  fi

  return 1
}

# Nothing parsed: wait LIMIT_COOLDOWN, doubling per consecutive unresolved
# hit, capped at LIMIT_MAX_WAIT. LIMIT_STREAK is reset by any tick that
# finishes without hitting a limit.
fallback_wait() {
  local w="$LIMIT_COOLDOWN" i=1
  while [ "$i" -lt "${LIMIT_STREAK:-1}" ]; do
    w=$((w * 2)); i=$((i + 1))
    [ "$w" -ge "$LIMIT_MAX_WAIT" ] && break
  done
  [ "$w" -gt "$LIMIT_MAX_WAIT" ] && w="$LIMIT_MAX_WAIT"
  printf '%s\n' "$w"
}

# LIMIT_MAX_WAIT bounds the fallback ladder; it did NOT bound a reset time we
# parsed out of a message. "resets in 999h99m" is 41 days out, and a vendor
# typo or a lucky garbage match would park the whole fleet for weeks behind a
# 15-minute heartbeat. A parsed reset is a hint, not an instruction.
#
# Prints the clamped epoch, or nothing when the input is unusable: non-numeric,
# or already in the past. A reset that has already happened tells us nothing,
# so the caller must treat it as unparseable and fall back to the ladder —
# which also keeps LIMIT_STREAK climbing instead of being reset by garbage.
clamp_reset() { # clamp_reset <epoch> <now> -> epoch | ""
  local reset="$1" now="$2" ceiling
  case "$reset" in ''|*[!0-9]*) return 1 ;; esac
  case "$now"   in ''|*[!0-9]*) return 1 ;; esac
  [ "$reset" -gt "$now" ] || return 1
  ceiling=$(( now + LIMIT_MAX_WAIT ))
  if [ "$reset" -gt "$ceiling" ]; then
    log "LIMIT parsed reset $(iso_epoch "$reset") exceeds LIMIT_MAX_WAIT (${LIMIT_MAX_WAIT}s); clamping to $(iso_epoch "$ceiling")"
    reset="$ceiling"
  fi
  printf '%s\n' "$reset"
}

# ---------------------------------------------------------------- limit state
limit_state_file() { printf '%s\n' "$LOG_DIR/limit-$HARNESS.json"; }

# Atomic, so a sibling slot reading the file mid-write never sees half a
# record.
write_limit_state() { # write_limit_state <reset|""> <reason> <slot> <tick> <ctx> <source>
  local f tmp
  f="$(limit_state_file)"; tmp="$f.$$.tmp"
  jq -n \
    --arg h "$HARNESS" \
    --argjson d "$(now_epoch)" \
    --arg r "$1" \
    --arg reason "$(printf '%.200s' "$2")" \
    --argjson slot "$3" --argjson tick "$4" \
    --arg ctx "$5" --arg src "$6" \
    '{harness:$h, detected_at:$d,
      reset_at: (if $r == "" then null else ($r|tonumber) end),
      reason:$reason, slot:$slot, tick:$tick, ctx:$ctx, source:$src}' \
    >"$tmp" 2>/dev/null \
    && mv -f "$tmp" "$f" \
    || { rm -f "$tmp"; log "WARNING could not write $f"; return 1; }
}

limit_state_reset_at() {
  local f; f="$(limit_state_file)"
  [ -f "$f" ] || return 1
  jq -r '.reset_at // empty' "$f" 2>/dev/null
}

limit_state_detected_at() {
  local f; f="$(limit_state_file)"
  [ -f "$f" ] || return 1
  jq -r '.detected_at // empty' "$f" 2>/dev/null
}

clear_limit_state() { rm -f "$(limit_state_file)" 2>/dev/null; return 0; }

# Slots share the state file, so "the limit I parked on has expired" is not
# the same claim as "the record on disk is expired". A sibling slot can hit a
# NEW limit while this one is parked; clearing unconditionally on resume would
# erase that newer record and send every slot back into the closed window.
# Only clear a record that is still the one we parked on.
clear_limit_state_if() { # clear_limit_state_if <reset_epoch_we_parked_on>
  local on_disk
  on_disk="$(limit_state_reset_at)" || return 0
  if [ "$on_disk" = "$1" ]; then
    clear_limit_state
  else
    log "limit state changed while parked (now resets $(iso_epoch "$on_disk")); leaving it in place"
  fi
  return 0
}

# A tick killed by the vendor never handed off, so its claim is stranded. td
# stores its own session id, not TD_CONTEXT_ID — but it derives that id
# deterministically from TD_CONTEXT_ID, so `td whoami` under the tick's ctx
# resolves it exactly (verified against td v0.54.0-11-g7e312e6, see td-f4e1fd).
#
# `td unstart --session` does the whole sweep in one call, exactly: it releases
# every claim held by ONE named session and cannot touch another slot's live
# issue. Three properties of that call are load-bearing:
#   - --force is REQUIRED. Without it --session only previews and releases
#     nothing, silently.
#   - It exits 1 with {"error":{"code":"not_found"}} when the session holds
#     nothing — which is the COMMON case, because a tick that finished cleanly
#     already handed off. Discriminating on exit status would turn "nothing to
#     release" into a spurious FAILED line on nearly every limit.
#   - It runs as loop.sh's own identity, not the tick's, so there is no
#     question of td's lineage protection declining to release.
release_tick_claim() { # release_tick_claim <slot> <ctx>
  local slot="$1" ctx="$2" ses out code count
  ses="$(TD_CONTEXT_ID="$ctx" td whoami --json 2>/dev/null | jq -r '.session // empty' 2>/dev/null)"
  case "$ses" in
    ses_*) ;;
    *) log "slot=$slot LIMIT claim-release skipped: cannot map ctx to implementer_session"
       return 0 ;;
  esac
  out="$( cd "$REPO_ROOT" && td unstart --session "$ses" --force --json 2>/dev/null )"
  code="$(printf '%s' "$out"  | jq -r '.error.code // empty' 2>/dev/null)"
  count="$(printf '%s' "$out" | jq -r '.count // empty' 2>/dev/null)"
  if [ "$code" = "not_found" ] || [ "$count" = "0" ]; then
    log "slot=$slot LIMIT no claim held by $ses; nothing to release"
    return 0
  fi
  case "$count" in
    ''|*[!0-9]*)
      log "slot=$slot LIMIT FAILED to release claims for session=$ses (td said: $(printf '%.200s' "${out:-no output}")); release them by hand"
      return 0 ;;
  esac
  log "slot=$slot LIMIT released $count claim(s) held by $ses"
  return 0
}

# Sleep in small steps so `touch porting/STOP` and Ctrl-C stay responsive
# while parked. Returns 1 if the slot should halt instead of resuming.
park_until() { # park_until <slot> <until_epoch>
  local slot="$1" until_at="$2" now beat=0 mins
  now="$(now_epoch)"
  mins=$(( (until_at - now + 59) / 60 )); [ "$mins" -lt 0 ] && mins=0
  log "slot=$slot LIMIT parked until $(iso_epoch "$until_at") (${mins}m from now)"
  while :; do
    if [ -f "$STOP_FILE" ]; then
      log "slot=$slot STOP file appeared while parked; halting"
      return 1
    fi
    now="$(date +%s)"
    [ "$now" -ge "$until_at" ] && break
    sleep "$PARK_POLL"
    beat=$((beat + PARK_POLL))
    if [ "$beat" -ge "$PARK_HEARTBEAT" ]; then
      beat=0
      log "slot=$slot LIMIT still parked until $(iso_epoch "$until_at")"
    fi
  done
  log "slot=$slot LIMIT resuming"
  clear_limit_state_if "$until_at"
  return 0
}

# Called before every tick. All slots of an invocation share one limit, so
# every slot but the one that discovered it learns about it here and never
# pays a harness invocation to rediscover it. It is also what makes a restart
# after `--on-limit stop` behave sensibly.
# 0 = proceed with the tick, 1 = halt this slot.
limit_gate() { # limit_gate <slot>
  local slot="$1" reset now
  reset="$(limit_state_reset_at)" || return 0
  case "$reset" in ''|*[!0-9]*) return 0 ;; esac
  now="$(now_epoch)"
  [ "$reset" -le "$now" ] && { clear_limit_state; return 0; }
  if [ "$ON_LIMIT" = "stop" ]; then
    log "slot=$slot LIMIT active until $(iso_epoch "$reset"); --on-limit stop, halting"
    return 1
  fi
  # --once is documented as "run a single tick, then exit (debugging)". Parking
  # here would block for hours on a debugging invocation — and it did: the
  # `--once` break in slot_loop sits AFTER this call, so it was unreachable.
  # Halt instead, and say why.
  if [ "$ONCE" = 1 ]; then
    log "slot=$slot LIMIT active until $(iso_epoch "$reset"); --once, exiting instead of parking"
    return 1
  fi
  park_until "$slot" "$reset" || return 1
  return 0
}

# LIMIT_PROBE is the adapter seam for a future "how much headroom is left"
# check; nothing ships behind it. exit 3 = limited, and a bare epoch on
# stdout is the reset time. Any other exit is inconclusive: run the tick.
# 0 = limited (LIMIT_PROBE_RESET may hold an epoch), 1 = proceed.
run_limit_probe() {
  local out rc
  LIMIT_PROBE_RESET=""
  [ -n "$LIMIT_PROBE" ] || return 1
  out="$( $LIMIT_PROBE 2>/dev/null )"; rc=$?
  if [ "$rc" -eq 3 ]; then
    LIMIT_MATCH_LINE="LIMIT_PROBE reported limited: $out"
    LIMIT_MATCH_PATTERN="LIMIT_PROBE"
    case "$out" in 1[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]) LIMIT_PROBE_RESET="$out" ;; esac
    return 0
  fi
  [ "$rc" -eq 0 ] || log "LIMIT_PROBE exited $rc (inconclusive); proceeding"
  return 1
}

# Everything a limit hit does, in one place: log with a grep-able LIMIT
# token, record resumable state, release the stranded claim. Parking or
# stopping is the caller's decision.
handle_limit() { # handle_limit <slot> <tick> <ctx> <reset|""> <source>
  local slot="$1" tick="$2" ctx="$3" reset="$4" src="$5" when="unknown"
  [ -n "$reset" ] && when="$(iso_epoch "$reset")"
  log "slot=$slot tick=$tick LIMIT harness=$HARNESS reset_at=$when" \
      "stage=${LIMIT_MATCH_SOURCE:-$src} source=${LIMIT_MATCH_PATTERN:-$src}"
  write_limit_state "$reset" "$LIMIT_MATCH_LINE" "$slot" "$tick" "$ctx" "$src"
  release_tick_claim "$slot" "$ctx"
}

# ---------------------------------------------------------------- arguments
# Print the header comment block. Derived, not hard-coded: a line range drifts
# silently every time the header grows, and a --help that quietly truncates is
# a documentation bug you only notice when you needed the docs.
usage() {
  sed -n '2,/^set -uo pipefail$/p' "$0" \
    | sed '$d' | sed 's/^# \{0,1\}//' | sed 's/[[:space:]]*$//'
  exit "${1:-0}"
}

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --harness)      HARNESS="$2"; shift 2 ;;
      -n|--slots)     SLOTS="$2"; shift 2 ;;
      --once)         ONCE=1; shift ;;
      --dry-run)      DRY_RUN=1; shift ;;
      --max-ticks)    MAX_TICKS="$2"; shift 2 ;;
      --tick-timeout) TICK_TIMEOUT="$2"; shift 2 ;;
      --on-limit)     ON_LIMIT="$2"; shift 2 ;;
      -h|--help)      usage 0 ;;
      *) echo "loop.sh: unknown argument: $1" >&2; usage 1 ;;
    esac
  done
  [ "$ONCE" = 1 ] && SLOTS=1
  case "$ON_LIMIT" in park|stop) ;; *) die "--on-limit must be park or stop" ;; esac
  return 0
}

# ---------------------------------------------------------------- paths
setup_paths() {
  SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
  REPO_ROOT="${TASKS_REPO:-$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null)}"
  PORTING_DIR="$REPO_ROOT/porting"
  LOG_DIR="$PORTING_DIR/logs"
  STOP_FILE="$PORTING_DIR/STOP"
  [ -n "$SLOTS_DIR" ] || SLOTS_DIR="${REPO_ROOT}-port-slots"
  PROMPT_FILE="$PORTING_DIR/PORTING.md"

  BOOTSTRAP="Read porting/PORTING.md and perform exactly one iteration of the porting loop described there, then exit."
  [ -f "$PORTING_DIR/BOOTSTRAP" ] && BOOTSTRAP="$(cat "$PORTING_DIR/BOOTSTRAP")"
  return 0
}

# ---------------------------------------------------------------- preflight
preflight() {
  [ -n "$REPO_ROOT" ] || die "cannot determine repo root (set TASKS_REPO)"
  git -C "$REPO_ROOT" rev-parse --verify -q "$DEFAULT_BRANCH" >/dev/null \
    || die "branch '$DEFAULT_BRANCH' not found in $REPO_ROOT"
  [ -f "$PROMPT_FILE" ] || die "missing $PROMPT_FILE — the fleet has no prompt to read"
  command -v "$HARNESS" >/dev/null || die "harness '$HARNESS' not on PATH"
  case "$HARNESS" in codex|claude) ;; *) die "unsupported harness '$HARNESS'" ;; esac
  command -v td  >/dev/null || die "td not on PATH"
  command -v jq  >/dev/null || die "jq not on PATH"
  TIMEOUT_BIN="$(command -v gtimeout || command -v timeout)" \
    || die "need gtimeout or timeout (brew install coreutils)"
  ( cd "$REPO_ROOT" && td list -n 1 --format json >/dev/null 2>&1 ) \
    || die "td does not answer in $REPO_ROOT"
  [ -f "$STOP_FILE" ] && die "porting/STOP exists; remove it to start the fleet"
  mkdir -p "$LOG_DIR" "$SLOTS_DIR"
  preflight_limit_state
}

# A leftover limit record is either still binding or stale. Either way the
# operator should be able to read the log and know exactly why nothing is
# running — a silently ignored record is the worst of both.
preflight_limit_state() {
  local f reset detected now
  f="$(limit_state_file)"
  [ -f "$f" ] || return 0
  now="$(now_epoch)"
  reset="$(limit_state_reset_at)"
  detected="$(limit_state_detected_at)"
  case "$reset" in
    ''|*[!0-9]*)
      case "$detected" in
        ''|*[!0-9]*)
          log "limit state $f is unreadable; removing"; clear_limit_state; return 0 ;;
      esac
      if [ $((now - detected)) -ge "$LIMIT_MAX_WAIT" ]; then
        log "stale limit state from $(iso_epoch "$detected") with no reset time; removing"
        clear_limit_state
      else
        log "limit state present with no parseable reset time (detected $(iso_epoch "$detected"))"
      fi
      return 0 ;;
  esac
  if [ "$reset" -le "$now" ]; then
    log "stale limit state (reset $(iso_epoch "$reset") already passed); removing"
    clear_limit_state
    return 0
  fi
  if [ "$ON_LIMIT" = "stop" ]; then
    # Exit 0, matching the documented contract for --on-limit stop and — more
    # importantly — matching what a MID-RUN limit already did. This is the
    # same condition discovered a moment earlier; returning a different exit
    # status for it is exactly what makes a wrapper script wrong. It was
    # `die` (exit 1) before, which contradicted both.
    log "usage limit for $HARNESS is active until $(iso_epoch "$reset") (see $f);" \
        "--on-limit stop, exiting 0 without starting. Rerun after that, or use --on-limit park."
    exit 0
  fi
  log "usage limit for $HARNESS active until $(iso_epoch "$reset"); slots will park"
  return 0
}

# ---------------------------------------------------------------- worktrees
# Fresh, deterministic start every tick. A dirty tree means a tick was killed
# mid-work: its uncommitted state is rescued to a branch, never discarded —
# the next agent (or Marcus) can inspect or delete it. A tick cut off by a
# usage limit lands here too, which is why handle_limit does not duplicate it.
prepare_worktree() {
  local slot="$1" wt="$SLOTS_DIR/slot-$1" rescue
  if [ ! -d "$wt" ]; then
    git -C "$REPO_ROOT" worktree prune
    git -C "$REPO_ROOT" worktree add --detach "$wt" "$DEFAULT_BRANCH" >/dev/null 2>&1 \
      || { log "slot=$slot FAILED to create worktree $wt"; return 1; }
    log "slot=$slot created worktree $wt"
  fi
  rescue_dirty_worktree "$slot" "$wt"
  git -C "$wt" checkout -q --detach "$DEFAULT_BRANCH" \
    || { log "slot=$slot FAILED to reset worktree to $DEFAULT_BRANCH"; return 1; }
  # Backstop. git carries the index across `checkout --detach`, so a failed
  # rescue used to leave the tree dirty AND the checkout succeeding — the next
  # tick then started on someone else's half-finished work, which is precisely
  # what the comment above promises never happens. Guarantee it instead of
  # asserting it. This only ever runs when the rescue above already failed and
  # said so loudly, so nothing silently discards work.
  if [ -n "$(git -C "$wt" status --porcelain 2>/dev/null)" ]; then
    log "slot=$slot worktree still dirty after rescue; discarding to guarantee a clean tick"
    git -C "$wt" reset -q --hard HEAD >/dev/null 2>&1
    git -C "$wt" clean -qfd >/dev/null 2>&1
    if [ -n "$(git -C "$wt" status --porcelain 2>/dev/null)" ]; then
      log "slot=$slot FAILED to clean worktree $wt; skipping this tick"
      return 1
    fi
  fi
  return 0
}

# Returns 0 if the tree was clean or the rescue committed, 1 if it could not.
# The log line must report which — a "rescued" line that fires when the commit
# failed is worse than no line at all, because it sends the operator looking
# for work on a branch that is empty.
#
# The commit is made with an explicit identity and --no-verify: the two ways
# this fails in practice are an unset user.email and a repo pre-commit hook,
# and neither has any business blocking a supervisor's salvage commit.
rescue_dirty_worktree() { # rescue_dirty_worktree <slot> <wt>
  local slot="$1" wt="$2" rescue base i=1
  [ -n "$(git -C "$wt" status --porcelain 2>/dev/null)" ] || return 0
  # The stamp has one-second resolution, so two rescues in the same second
  # would collide and the second `checkout -b` would fail — reporting a
  # rescue failure that is really a naming failure.
  base="port/rescue/slot$slot-$(date +%Y%m%d-%H%M%S)"
  rescue="$base"
  while git -C "$wt" rev-parse --verify -q "refs/heads/$rescue" >/dev/null 2>&1; do
    rescue="$base-$i"; i=$((i + 1))
  done
  if git -C "$wt" checkout -q -b "$rescue" 2>/dev/null \
     && git -C "$wt" add -A 2>/dev/null \
     && git -C "$wt" -c user.name='loop.sh' -c user.email='loop@localhost' \
          commit -q --no-verify \
          -m "loop.sh: rescue uncommitted state from interrupted tick" 2>/dev/null
  then
    log "slot=$slot rescued dirty worktree to $rescue"
    return 0
  fi
  log "slot=$slot FAILED to rescue dirty worktree (branch $rescue holds nothing usable);" \
      "the uncommitted state is about to be discarded — investigate before the next tick"
  return 1
}

# ---------------------------------------------------------------- models
# Codex only. Policy lives in PORTING.md; the script applies its one
# observable proxy: review work waiting => the tick should judge => top tier.
pick_codex_model() {
  local n
  n="$(cd "$REPO_ROOT" && td list -s in_review --format json 2>/dev/null | jq 'length' 2>/dev/null)"
  case "$n" in ''|*[!0-9]*) n=0 ;; esac
  if [ "$n" -gt 0 ]; then echo "$CODEX_TOP"; else echo "$CODEX_MID"; fi
}

# ---------------------------------------------------------------- ticks
# Each tick gets its own TD_CONTEXT_ID, so every tick is a distinct td
# session — which is what makes "a different agent claims the review"
# enforceable rather than aspirational, and what lets a usage-limited tick's
# stranded claim be found and released.
# Returns 2 when the tick ended in a usage limit.
run_tick() {
  local slot="$1" tick="$2" wt="$SLOTS_DIR/slot-$1"
  local ts day tlog last model ctx rc start dur reset wait
  ts="$(date +%Y%m%d-%H%M%S)"; day="$(date +%Y%m%d)"
  mkdir -p "$LOG_DIR/$day"
  tlog="$LOG_DIR/$day/slot$slot-$ts.log"
  last="$LOG_DIR/$day/slot$slot-$ts.last"
  ctx="port-slot$slot-$ts"

  if run_limit_probe; then
    # A probe's epoch is no more trustworthy than a vendor's; clamp it too.
    [ -n "$LIMIT_PROBE_RESET" ] \
      && LIMIT_PROBE_RESET="$(clamp_reset "$LIMIT_PROBE_RESET" "$(now_epoch)")"
    handle_limit "$slot" "$tick" "$ctx" "$LIMIT_PROBE_RESET" "probe"
    return 2
  fi

  prepare_worktree "$slot" || return 0

  case "$HARNESS" in
    codex)  model="$(pick_codex_model)" ;;
    claude) model="$CLAUDE_MODEL" ;;
  esac

  log "slot=$slot tick=$tick harness=$HARNESS model=$model ctx=$ctx log=$tlog"
  if [ "$DRY_RUN" = 1 ]; then
    log "slot=$slot tick=$tick DRY RUN — harness not invoked"
    return 0
  fi

  # Two things about these invocations are not incidental:
  #   </dev/null — `codex exec` reads stdin whenever it is not a tty. Under a
  #     piped launch (and OPERATING.md recommends nohup, which does NOT
  #     redirect stdin) it prints "Reading additional input from stdin..." and
  #     blocks until killed, so every tick on every slot burned the full
  #     --tick-timeout doing nothing, forever. Redirect both harnesses.
  #   JSONL output — this is what lets detect_limit read the harness's own
  #     typed error records instead of grepping text the agent may have merely
  #     read. See the header.
  start=$SECONDS
  if [ "$HARNESS" = codex ]; then
    ( cd "$wt" && TD_CONTEXT_ID="$ctx" "$TIMEOUT_BIN" -k 30 "$TICK_TIMEOUT" \
        codex exec --cd "$wt" -m "$model" --sandbox "$CODEX_SANDBOX" \
          --color never \
          --json \
          -o "$last" \
          "$BOOTSTRAP" </dev/null ) >"$tlog" 2>&1
    rc=$?
  else
    ( cd "$wt" && TD_CONTEXT_ID="$ctx" "$TIMEOUT_BIN" -k 30 "$TICK_TIMEOUT" \
        claude -p --output-format stream-json --verbose \
          --dangerously-skip-permissions --model "$model" \
          "$BOOTSTRAP" </dev/null ) >"$tlog" 2>&1
    rc=$?
  fi
  dur=$((SECONDS - start))
  LAST_TICK_DUR=$dur

  # Classify the outcome before reporting it: a tick the vendor cut off is
  # not a tick that failed, and it must not be logged as one.
  if detect_limit "$HARNESS" "$rc" "$tlog" "$last"; then
    local now
    now="$(now_epoch)"
    reset="$(parse_reset_at "$LIMIT_MATCH_LINE" "$now")"
    # A parsed reset is only usable once it survives the clamp: in the future,
    # and no further out than LIMIT_MAX_WAIT. Anything else is treated exactly
    # like an unparseable message — which matters, because the old code reset
    # LIMIT_STREAK to 0 whenever *anything* parsed, so a garbage reset also
    # disabled the backoff ladder that was supposed to catch it.
    [ -n "$reset" ] && reset="$(clamp_reset "$((reset + LIMIT_GRACE))" "$now")"
    if [ -z "$reset" ]; then
      LIMIT_STREAK=$((LIMIT_STREAK + 1))
      wait="$(fallback_wait)"
      reset=$(( now + wait ))
      log "slot=$slot tick=$tick LIMIT no usable reset time in the message; waiting ${wait}s (streak=$LIMIT_STREAK)"
    else
      LIMIT_STREAK=0
    fi
    handle_limit "$slot" "$tick" "$ctx" "$reset" "pattern"
    return 2
  fi

  LIMIT_STREAK=0
  if [ "$rc" -eq 124 ]; then
    log "slot=$slot tick=$tick TIMEOUT after ${TICK_TIMEOUT}s (state rescued next tick)"
  else
    log "slot=$slot tick=$tick done exit=$rc dur=${dur}s"
  fi
  return 0
}

# ---------------------------------------------------------------- slot loop
slot_loop() {
  local slot="$1" tick=0 pause="$PAUSE" rc
  LAST_TICK_DUR=0
  LIMIT_STREAK=0
  while :; do
    if [ -f "$STOP_FILE" ]; then
      log "slot=$slot STOP file present; halting"
      break
    fi
    if [ "$MAX_TICKS" -gt 0 ] && [ "$tick" -ge "$MAX_TICKS" ]; then
      log "slot=$slot reached max ticks ($MAX_TICKS); halting"
      break
    fi
    limit_gate "$slot" || break
    tick=$((tick + 1))
    run_tick "$slot" "$tick"; rc=$?
    if [ "$rc" -eq 2 ]; then
      # A usage limit is not a failure; it is a wait. Park (and come back) or
      # exit cleanly, but never burn the remaining ticks against a closed door.
      if [ "$ON_LIMIT" = "stop" ]; then
        log "slot=$slot halting on usage limit (--on-limit stop)"
        break
      fi
      # Check --once BEFORE the gate, not after: limit_gate parks, and a
      # `--once` run that parks for hours has stopped being a single tick.
      # (limit_gate now refuses to park under --once too; this is the
      # explicit, readable half of the same fix.)
      if [ "$ONCE" = 1 ]; then
        log "slot=$slot --once: tick ended in a usage limit; exiting without parking"
        break
      fi
      limit_gate "$slot" || break
      continue
    fi
    [ "$ONCE" = 1 ] && break
    # A very short tick means "no claimable work" far more often than "fast
    # success" — agent startup alone eats most of the threshold. Back off.
    if [ "${LAST_TICK_DUR:-0}" -lt "$IDLE_THRESHOLD" ]; then
      pause=$((pause * 2)); [ "$pause" -gt "$MAX_PAUSE" ] && pause="$MAX_PAUSE"
    else
      pause="$PAUSE"
    fi
    sleep "$pause"
  done
}

# ---------------------------------------------------------------- main
main() {
  parse_args "$@"
  setup_paths
  preflight
  log "fleet start harness=$HARNESS slots=$SLOTS repo=$REPO_ROOT dry_run=$DRY_RUN" \
      "max_ticks=$MAX_TICKS on_limit=$ON_LIMIT timeout=${TICK_TIMEOUT}s"

  trap 'log "fleet interrupted; killing slot loops"; kill $(jobs -p) 2>/dev/null; exit 130' INT TERM

  local slot=1
  while [ "$slot" -le "$SLOTS" ]; do
    slot_loop "$slot" &
    slot=$((slot + 1))
    [ "$slot" -le "$SLOTS" ] && sleep "$STAGGER"
  done
  wait
  log "fleet stopped"
}

# Sourcing this file (porting/test-loop-limits.sh does) must define the
# functions and nothing else — no preflight, no worktrees, no slots.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
