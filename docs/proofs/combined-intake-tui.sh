#!/usr/bin/env bash
# Sandbox for the combined Inbox/Approvals TUI proof.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
sandbox="$(mktemp -d "${TMPDIR:-/tmp}/tasks-combined-intake-proof.XXXXXX")"
cleanup() {
  rm -rf -- "$sandbox"
}
trap cleanup EXIT

export TASKS_FILE="$sandbox/tasks.jsonl"
export TASKS_ARCHIVE="$sandbox/archive.jsonl"
export XDG_STATE_HOME="$sandbox/state"
export TASKS_TIMEZONE="America/Los_Angeles"
if [[ "${TASKS_PROOF_NO_COLOR:-0}" == "1" ]]; then
  unset TASKS_THEME || true
  export NO_COLOR=1
else
  export TASKS_THEME="${TASKS_THEME:-default}"
  unset NO_COLOR || true
fi

mkdir -p "$XDG_STATE_HOME"
cp "$root/examples/tasks.jsonl" "$TASKS_FILE"

"$root/bin/tasks" capture "Plan conference trip" \
  --context work --no-host-context >/dev/null
"$root/bin/tasks" capture "Reserve hotel" \
  --under "Plan conference trip" --no-host-context >/dev/null
"$root/bin/tasks" capture "Buy printer paper" \
  --context errands --no-host-context >/dev/null
"$root/bin/tasks" capture "Renew passport" \
  --context work --scheduled "+30" --no-host-context >/dev/null

# The @home proposal is first so one `a` moves it into Inbox while leaving the
# @work proposal available for the filtered proof.
"$root/bin/tasks" propose "Compare accounting tools" \
  --context home --no-host-context \
  --note "Compare migration cost and accountant access." >/dev/null
"$root/bin/tasks" propose "Research backup providers" \
  --context work --no-host-context \
  --note "Current setup has no offsite copy." >/dev/null

exec "$root/bin/tasks-tui"
