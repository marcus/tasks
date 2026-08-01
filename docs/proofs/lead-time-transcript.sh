set -e
BIN=/Users/marcus/code/tasks/bin/tasks
SUPPORT=/Users/marcus/code/tasks/test/support/sequenced_today.rb
ROOT=/tmp/tasks-lead-time-proof-run
rm -rf "$ROOT"; mkdir -p "$ROOT/state"
export TASKS_FILE=$ROOT/tasks.jsonl TASKS_ARCHIVE=$ROOT/archive.jsonl XDG_STATE_HOME=$ROOT/state
export TZ=America/Denver RUBYOPT=-r$SUPPORT

on() { TASKS_TEST_TODAY_SEQUENCE="$1" "$BIN" "${@:2}"; }

echo "== case 1: deadline-anchored, hidden until 3 weeks before"
on 2026-07-01 capture "Renew the passport" --due 2026-11-01 --lead 3w --state NEXT
on 2026-07-01 lead "Renew the passport"
echo "-- 2026-10-10, the day before the window opens:"
on 2026-10-10 list
on 2026-10-10 list --unavailable
echo "-- 2026-10-11, the day it opens:"
on 2026-10-11 list

echo
echo "== case 2: scheduled-anchored + recurring, window survives the roll"
on 2026-04-13 capture "File quarterly sales tax" --scheduled 2026-04-20 --recur "every 3 months on the 20th" --lead 1w --state NEXT
echo "-- 2026-04-12, still hidden:"
on 2026-04-12 list --unavailable
echo "-- 2026-04-13, inside the window:"
on 2026-04-13 list
echo "-- complete it: the anchor rolls and the window re-arms"
on 2026-04-20 done "quarterly sales tax"
on 2026-04-21 list
on 2026-04-21 list --unavailable
echo "-- 2026-07-13, one week before the next occurrence:"
on 2026-07-13 list

echo
echo "== activate releases exactly one occurrence"
on 2026-04-21 activate "quarterly sales tax"
on 2026-04-21 list
on 2026-04-21 lead "quarterly sales tax" --json
echo "-- the next roll retires the release:"
on 2026-07-20 done "quarterly sales tax"
on 2026-07-21 list
on 2026-07-21 list --unavailable

echo
echo "== refusals"
on 2026-04-21 defer "quarterly sales tax" 2026-05-01 || true
on 2026-04-21 lead "Renew the passport" soonish || true
on 2026-04-21 capture "No date" --lead 3w || true

echo
echo "== clock lead: 5h before an all-day date is 19:00 the evening before"
on 2026-05-01 capture "Board the flight" --due 2026-06-01 --lead 5h --state NEXT
on 2026-05-01 lead "Board the flight"

echo
echo "== check"
"$BIN" check
