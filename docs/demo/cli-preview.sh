#!/bin/sh
# Print a short CLI tour against whatever TASKS_DIR the parent exported.
# Used only by scripts/update-screenshots.sh.
set -eu

tasks_bin=${TASKS_BIN:-tasks}
printf '\033[1m$\033[0m tasks next\n'
"$tasks_bin" next
printf '\n\033[1m$\033[0m tasks agenda\n'
"$tasks_bin" agenda
# Keep the pane alive so betamax does not photograph tmux's "Pane is dead".
sleep 8
