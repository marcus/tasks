#!/usr/bin/env bash
# Refresh docs/assets/screenshot-{tui,cli}.png from a disposable demo store.
#
# Isolation is the whole point. The script never reads or writes the
# configured task directory: it builds a temp store, points TASKS_DIR /
# XDG_* / HOME at that sandbox, and pins the clock. Betamax uses tmux
# socket `-L betamax`, not the machine default server.

set -euo pipefail

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
demo_dir=$repo_root/docs/demo
assets_dir=$repo_root/docs/assets
real_home=$HOME
# Thursday, so Agenda has today / tomorrow / later-this-week without
# looking like a Monday fire drill. Override with TASKS_SCREENSHOT_NOW.
pin_now=${TASKS_SCREENSHOT_NOW:-2026-08-13T12:00:00-07:00}

need() {
	command -v "$1" >/dev/null 2>&1 || {
		printf 'update-screenshots: missing %s\n' "$1" >&2
		exit 1
	}
}

need betamax
need termshot
need python3

# Build against the real developer environment. Remapping HOME first would
# send Go's module cache into the sandbox and make cleanup fail.
cd "$repo_root"
make build

sandbox=$(mktemp -d "${TMPDIR:-/tmp}/tasks-screenshots.XXXXXX")
sandbox=$(CDPATH= cd -- "$sandbox" && pwd -P)
cleanup() { chmod -R u+w "$sandbox" 2>/dev/null || true; rm -rf "$sandbox"; }
trap cleanup EXIT

store_dir=$sandbox/store
home_dir=$sandbox/home
config_dir=$sandbox/config
state_dir=$sandbox/state
xdg_config=$config_dir
mkdir -p "$store_dir" "$home_dir" "$xdg_config/tasks" "$state_dir"

printf '%s\n' \
	'theme = default' \
	'timezone = America/Los_Angeles' \
	'time_format = 12' \
	'mouse = on' \
	> "$xdg_config/tasks/config"

# Isolate every resolution input the CLI and TUI honour. Time format and
# date order too — a leftover TASKS_TIME_FORMAT=24 would change the PNG.
unset TASKS_FILE TASKS_ARCHIVE TASKS_MEMORY TASKS_DIR
unset TASKS_THEME TASKS_TIMEZONE TASKS_URGENT_DAYS TASKS_MAX_DEPTH
unset TASKS_MOUSE TASKS_WORKER_ID TASKS_DEVICE
unset TASKS_TIME_FORMAT TASKS_DATE_ORDER
export HOME=$home_dir
export XDG_CONFIG_HOME=$xdg_config
export XDG_STATE_HOME=$state_dir
export TASKS_DIR=$store_dir
export TASKS_PIN_NOW=$pin_now
export TASKS_PIN_HOSTNAME=demo.tasks.local
export TASKS_DEVICE=demo
export TZ=America/Los_Angeles
export LANG=C.UTF-8
export LC_ALL=C.UTF-8
export PATH=$repo_root/bin:$PATH
export TASKS_BIN=$repo_root/bin/tasks

# Betamax starts the command in tmux -L betamax. A leftover server on that
# socket does not inherit this shell's env, so the capture wrappers re-apply
# the sandbox themselves.
write_capture_wrapper() {
	target=$1
	shift
	{
		printf '%s\n' '#!/bin/sh' 'set -eu'
		printf '%s\n' \
			'unset TASKS_FILE TASKS_ARCHIVE TASKS_MEMORY' \
			'unset TASKS_THEME TASKS_TIMEZONE TASKS_URGENT_DAYS TASKS_MAX_DEPTH' \
			'unset TASKS_MOUSE TASKS_WORKER_ID TASKS_TIME_FORMAT TASKS_DATE_ORDER'
		printf 'export HOME=%q\n' "$home_dir"
		printf 'export XDG_CONFIG_HOME=%q\n' "$xdg_config"
		printf 'export XDG_STATE_HOME=%q\n' "$state_dir"
		printf 'export TASKS_DIR=%q\n' "$store_dir"
		printf 'export TASKS_PIN_NOW=%q\n' "$pin_now"
		printf 'export TASKS_PIN_HOSTNAME=demo.tasks.local\n'
		printf 'export TASKS_DEVICE=demo\n'
		printf 'export TZ=America/Los_Angeles\n'
		printf 'export LANG=C.UTF-8\n'
		printf 'export LC_ALL=C.UTF-8\n'
		printf 'export PATH=%q\n' "$repo_root/bin:$PATH"
		printf 'export TASKS_BIN=%q\n' "$repo_root/bin/tasks"
		printf 'exec'
		for arg in "$@"; do
			printf ' %q' "$arg"
		done
		printf '\n'
	} > "$target"
	chmod +x "$target"
}

today=$(python3 - "$pin_now" <<'PY'
import sys
from datetime import datetime, timedelta

raw = sys.argv[1]
instant = datetime.fromisoformat(raw)
print(instant.date().isoformat())
PY
)

shift_date() {
	python3 - "$today" "$1" <<'PY'
import sys
from datetime import date, timedelta

day = date.fromisoformat(sys.argv[1])
print((day + timedelta(days=int(sys.argv[2]))).isoformat())
PY
}

overdue=$(shift_date -4)
plus2=$(shift_date 2)
plus5=$(shift_date 5)
plus10=$(shift_date 10)

tasks() {
	"$repo_root/bin/tasks" "$@"
}

# Bootstrap + seed. Every mutation goes through the CLI so the store stays
# canonical; titles are fictional on purpose.
tasks capture "The espresso machine hissed at me again" \
	--state INBOX --context @studio \
	--note "Captured during the afternoon rush. Probably the gasket."
tasks capture "Reprint the shop cards with Sunday hours" \
	--state INBOX --context @computer --priority B

tasks propose "Add a standing Thursday close-out" \
	--context @computer \
	--note "Suggested after three weeks of forgetting to count the till."

tasks project create "Launch the listening-room nights"
tasks project create "Rebuild the shop site"
tasks project create "Catalog the back room"

tasks capture "Pay the studio insurance" \
	--state NEXT --priority A --context @computer --tag important \
	--due "$overdue" \
	--note "Policy ND-4418. They cancel on the 10th if this is late."
tasks link add "Pay the studio insurance" \
	https://example.test/policy/nd-4418 --label "policy portal"

tasks capture "Write Friday's member letter" \
	--state NEXT --priority A --context @computer --tag important \
	--due today --project "Launch the listening-room nights" \
	--note "Thank the July volunteers. Mention the first Friday lineup."

tasks capture "Meet the electrician" \
	--state NEXT --context @studio \
	--due "today 3pm"

tasks capture "Book the first three musicians" \
	--state NEXT --priority A --context @calls --tag important \
	--due "$plus2" --project "Launch the listening-room nights"

tasks capture "Print the door posters" \
	--state TODO --context @errands \
	--project "Launch the listening-room nights"

tasks capture "Finish the events calendar page" \
	--state NEXT --context @computer \
	--project "Rebuild the shop site"

tasks capture "Move the old photos off the homepage" \
	--state TODO --context @computer \
	--due "$plus10" --project "Rebuild the shop site"

tasks capture "Hear back from the registrar on the SSL cert" \
	--state WAITING --context @waiting \
	--project "Rebuild the shop site"
tasks delegate "Hear back from the registrar on the SSL cert" \
	--to sam@example.test

tasks capture "Call the plumber about the slow drain" \
	--state NEXT --context @calls --due tomorrow

tasks capture "Review the summer exhibit labels" \
	--state NEXT --priority B --context @computer --tag important

tasks capture "Reply to the city about the sidewalk permit" \
	--state TODO --priority B --context @email --due "$plus5"

tasks capture "Photograph the new arrivals wall" \
	--state NEXT --context @studio

tasks capture "Order beans from the usual roaster" \
	--state NEXT --context @errands

tasks capture "Restock the darkroom paper" \
	--state TODO --priority B --context @errands

tasks capture "Water the plants" \
	--state NEXT --context @home --scheduled today --recur weekly

tasks capture "Learn enough Italian to order without pointing" \
	--state TODO --context @home
tasks someday "Learn enough Italian to order without pointing"

if ! tasks check --all-files >/dev/null; then
	printf 'update-screenshots: demo store failed check\n' >&2
	tasks check --all-files >&2 || true
	exit 1
fi

resolved=$(tasks config)
org_line=$(printf '%s\n' "$resolved" | awk '/^org:/{print $2; exit}')
case "$org_line" in
	"$store_dir"/tasks.jsonl) ;;
	*)
		printf 'update-screenshots: TASKS_DIR did not win; refusing to capture\n%s\n' "$resolved" >&2
		exit 1
		;;
esac
# TMPDIR may live under $HOME, so do not treat every home-relative path as a
# leak. Refuse only the real config file and a list that resolved through it.
case "$resolved" in
	*"$real_home/.config/tasks/config"*)
		printf 'update-screenshots: sandbox leaked into the real config file\n%s\n' "$resolved" >&2
		exit 1
		;;
esac

capture_dir=$sandbox/captures
mkdir -p "$capture_dir"
write_capture_wrapper "$sandbox/run-tui.sh" "$repo_root/bin/tasks-tui"
write_capture_wrapper "$sandbox/run-cli.sh" "$demo_dir/cli-preview.sh"

betamax --session tasks-docs-tui \
	--output-dir "$capture_dir" \
	-f "$demo_dir/tui.keys" \
	"$sandbox/run-tui.sh"

betamax --session tasks-docs-cli \
	--output-dir "$capture_dir" \
	-f "$demo_dir/cli.keys" \
	"$sandbox/run-cli.sh"

for name in screenshot-tui.png screenshot-cli.png; do
	if [ ! -f "$capture_dir/$name" ]; then
		printf 'update-screenshots: missing %s\n' "$name" >&2
		exit 1
	fi
	cp "$capture_dir/$name" "$assets_dir/$name"
done

printf 'updated %s and %s from isolated demo store\n' \
	"$assets_dir/screenshot-tui.png" "$assets_dir/screenshot-cli.png"
