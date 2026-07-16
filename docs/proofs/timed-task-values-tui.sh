#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
sandbox="$(mktemp -d "${TMPDIR:-/tmp}/tasks-timed-values-proof.XXXXXX")"
cleanup() {
  rm -rf "$sandbox"
}
trap cleanup EXIT

export TASKS_FILE="$sandbox/tasks.jsonl"
export TASKS_ARCHIVE="$sandbox/archive.jsonl"
export XDG_STATE_HOME="$sandbox/state"
export TASKS_TIMEZONE="America/Los_Angeles"
export TASKS_TIME_FORMAT="24"

mkdir -p "$XDG_STATE_HOME"
cp "$root/examples/tasks.jsonl" "$TASKS_FILE"

"$root/bin/tasks" capture "Floating design review" --state NEXT --project Tasks \
  --due "tomorrow 11am" --due-floating >/dev/null
"$root/bin/tasks" capture "London coordination call" --state NEXT --project Tasks \
  --due "tomorrow 5pm" --due-timezone Europe/London >/dev/null
"$root/bin/tasks" capture "New York launch handoff" --state NEXT --project Tasks \
  --scheduled "+2 9am" --scheduled-timezone America/New_York \
  --due "+2 5pm" --due-timezone America/New_York >/dev/null

exec "$root/bin/tasks-tui"
