#!/usr/bin/env bash
# Sandbox transcript for the recurring lead-time proof (td-526a45). Writes only
# to a temporary store; `today` is pinned per command through the same
# subprocess clock the CLI boundary tests use.
set -e
BIN=/Users/marcus/code/tasks/bin/tasks
SUPPORT=/Users/marcus/code/tasks/test/support/sequenced_today.rb
ROOT=/tmp/tasks-lead-time-proof-run
rm -rf "$ROOT"; mkdir -p "$ROOT/state"
export TASKS_FILE=$ROOT/tasks.jsonl TASKS_ARCHIVE=$ROOT/archive.jsonl XDG_STATE_HOME=$ROOT/state
export TZ=America/Denver RUBYOPT=-r$SUPPORT

on() { TASKS_TEST_TODAY_SEQUENCE="$1" "$BIN" "${@:2}"; }

echo "== motivating case 1: water succulents — 2w:sun, 1d lead"
on 2026-06-01 capture "water succulents" --recur 2w:sun --lead 1d --state NEXT --scheduled 2026-06-07
on 2026-06-01 lead "water succulents"
echo "-- 2026-06-05, two days out: hidden"
on 2026-06-05 list
echo "-- 2026-06-06, the Saturday before: visible"
on 2026-06-06 list
echo "-- do it on the Sunday; the anchor rolls two weeks"
on 2026-06-07 done "water succulents"
echo "-- 2026-06-08, the rest of the cycle: hidden again, window re-armed"
on 2026-06-08 list
on 2026-06-08 list --unavailable
echo "-- 2026-06-20, the Saturday before the next occurrence: visible"
on 2026-06-20 list

echo
echo "== motivating case 2: clean gutters — y:06-01, 17d lead"
on 2026-01-05 capture "clean gutters" --recur y:06-01 --lead 17d --state NEXT --due 2026-06-01
on 2026-01-05 lead "clean gutters"
echo "-- 2026-05-14: hidden"
on 2026-05-14 list
echo "-- 2026-05-15, seventeen days before June 1: visible"
on 2026-05-15 list
echo "-- do it; the anchor rolls a year and the window follows"
on 2026-06-01 done "clean gutters"
on 2026-06-02 list --unavailable

echo
echo "== activate releases exactly one occurrence"
on 2026-06-08 activate "water succulents"
on 2026-06-08 list
on 2026-06-08 lead "water succulents" --json
echo "-- the next roll retires the release"
on 2026-06-21 done "water succulents"
on 2026-06-22 list
on 2026-06-22 list --unavailable

echo
echo "== refusals (rules 1-5)"
on 2026-06-22 defer "water succulents" 2026-07-01 || true
on 2026-06-22 lead "water succulents" soonish || true
on 2026-06-22 capture "no date at all" --lead 3w || true
on 2026-06-22 schedule "clean gutters" 2026-05-01 || true
echo "-- rule 5: clearing the last date clears the lead with it"
on 2026-06-22 undate "clean gutters"
on 2026-06-22 lead "clean gutters"

echo
echo "== a proposal takes a lead on the same terms it takes a deadline"
on 2026-06-22 propose "maybe reseal the deck" --due 2026-09-01 --lead 2w
on 2026-06-22 lead "reseal the deck"

echo
echo "== clock lead: 5h before an all-day date is 19:00 the evening before"
on 2026-05-01 capture "board the flight" --due 2026-06-01 --lead 5h --state NEXT
on 2026-05-01 lead "board the flight"

echo
echo "== check"
"$BIN" check
