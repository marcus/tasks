#!/usr/bin/env bash
# Sandbox for the delegation TUI proof. Writes only to a temporary store.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
sandbox="$(mktemp -d "${TMPDIR:-/tmp}/tasks-delegation-proof.XXXXXX")"
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

"$root/bin/tasks" capture "Compare CRDT libraries" --state NEXT --project Tasks >/dev/null
"$root/bin/tasks" capture "Renew office lease" --state NEXT --project Tasks >/dev/null
"$root/bin/tasks" capture "Audit subscriptions" --state NEXT --project Tasks >/dev/null

# Two markers are set up here so both render on screen. The human address is
# seeded through the CLI because a literal `@` is a directive in a .keys file,
# not because the TUI cannot take one — `D` accepts an email inline.
"$root/bin/tasks" delegate "Renew office lease" --to pat@example.com >/dev/null
"$root/bin/tasks" delegate "Audit subscriptions" implement >/dev/null
"$root/bin/tasks" claim "Audit subscriptions" \
  --worker claude-code/claude-fable-5/313cf82e >/dev/null
"$root/bin/tasks" workref "Audit subscriptions" https://example.com/audit-brief \
  --worker claude-code/claude-fable-5/313cf82e >/dev/null

exec "$root/bin/tasks-tui"
