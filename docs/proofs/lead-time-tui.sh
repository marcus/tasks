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

# The contract's two motivating cases, anchored far enough out that the windows
# stay closed whenever this proof is replayed.
"$root/bin/tasks" capture "clean gutters" --state NEXT --project Tasks \
  --due 2036-06-01 --lead 17d >/dev/null
"$root/bin/tasks" capture "water succulents" --state NEXT --project Tasks \
  --scheduled 2036-06-07 --recur 2w:sun --lead 1d >/dev/null
"$root/bin/tasks" capture "Review the vendor contract" --state NEXT --project Tasks >/dev/null

exec "$root/bin/tasks-tui"
