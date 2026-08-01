#!/usr/bin/env bash
# Sandbox for the lead-time TUI proof. Writes only to a temporary store.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
sandbox="$(mktemp -d "${TMPDIR:-/tmp}/tasks-lead-time-proof.XXXXXX")"
cleanup() {
  rm -rf "$sandbox"
}
trap cleanup EXIT

export TASKS_FILE="$sandbox/tasks.jsonl"
export TASKS_ARCHIVE="$sandbox/archive.jsonl"
export XDG_STATE_HOME="$sandbox/state"
export TASKS_TIMEZONE="America/Los_Angeles"

mkdir -p "$XDG_STATE_HOME"
cp "$root/examples/tasks.jsonl" "$TASKS_FILE"

# Both motivating cases, far enough out that the windows stay closed whenever
# this proof is replayed.
"$root/bin/tasks" capture "Renew the passport" --state NEXT --project Tasks \
  --due 2036-11-01 --lead 3w >/dev/null
"$root/bin/tasks" capture "File quarterly sales tax" --state NEXT --project Tasks \
  --scheduled 2036-04-20 --recur "every 3 months on the 20th" --lead 1w >/dev/null
"$root/bin/tasks" capture "Review the vendor contract" --state NEXT --project Tasks >/dev/null

exec "$root/bin/tasks-tui"
