#!/bin/sh
# probe-parity.sh — does the Go probe agree with the Ruby oracle's probe?
#
#   go/testdata/probe-parity.sh              # the phase1 cases (default)
#   go/testdata/probe-parity.sh --fixtures   # every fixture in the corpus
#
# The two probes print the same object from the same inputs (see
# porting/runners/README.md § "The probe"), so this script builds the Go probe,
# materializes stores, runs BOTH probes against each one under the protocol's
# pinned environment, and diffs the JSON.
#
# `environment` is excluded and nothing else is. Its four fields —
# tzdb_version, platform, locale, runtime — are per-implementation by
# construction and advisory-only; `revisions`, `paths` and `pins` are compared
# FIELD FOR FIELD, which is what porting/compare does and what makes a
# divergence in the revision algorithm or in the pin resolution a Go defect
# rather than an invisible difference.
#
# Two modes, because they answer different questions:
#
#   default     the phase1 case list, run through the real runner (--keep), so
#               each store is in the state an INVOCATION left it in. This is
#               the acceptance test.
#   --fixtures  every fixture's pristine store, copied straight out of the
#               corpus. Slower, no runner, and no invocation — it is the wider
#               net, and it is where the remaining internal/check gaps show up.
#
# The work directory is NOT the runner's default /tmp/tasks-conformance, and
# that is safe here in a way it would not be for observations: the protocol's
# same-absolute-path requirement exists because the journal key is a digest of
# the store's canonical path, and here BOTH probes read the same copy at the
# same path. Keeping this run out of the runner's tree stops it from clobbering
# a corpus run in progress.
#
# Exit status: 0 when every store compared identical, 1 otherwise.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
mode=${1:-cases}
# The two modes get separate trees on purpose: they materialize different
# stores under the same names, and a shared tree would let one run read the
# leftovers of the other.
work=${WORK:-/tmp/tasks-probe-parity}/${mode#--}

ruby_probe="$root/porting/runners/ruby/probe"
go_probe="$root/go/bin/tasks-probe"
ruby_bin=$(command -v ruby)

"$root/porting/runners/go/build"

# The protocol's pinned environment, and the whole of it. `env -i` is the half
# that makes "unset" a pin rather than a wish: the child receives this map and
# nothing else, so an operator with TERM exported cannot reach either probe.
run_probe() {
  env -i \
    PATH=/usr/bin:/bin:/usr/sbin:/sbin \
    HOME="$1" TASKS_DIR="$1" \
    XDG_CONFIG_HOME="$1/.config" XDG_STATE_HOME="$1/.state" \
    TZ=UTC TASKS_TIMEZONE=UTC \
    LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 \
    TASKS_DEVICE=fixture \
    TASKS_PIN_NOW=2026-03-14T15:09:26Z \
    TASKS_PIN_IDS=bbbb0001 \
    TASKS_PIN_COALESCE_SCOPE=pinned-scope \
    TASKS_PIN_DELEGATION_KEYS=cccc000000000001 \
    TASKS_PIN_HOSTNAME=fixture-host \
    LINES=40 COLUMNS=100 \
    "$2" ${3:+"$3"} "$1"
}

compare() {
  name="$1"
  root_dir="$2"
  # A case may have left the copy root unwritable (copy_root_mode); the probe
  # needs to create its lock sidecar, so restore a writable mode first. That is
  # what the runner itself does after its last observation.
  chmod u+rwx "$root_dir" 2>/dev/null || true
  run_probe "$root_dir" "$ruby_bin" "$ruby_probe" >"$work/out/$name.ruby.json" 2>/dev/null || true
  run_probe "$root_dir" "$go_probe" >"$work/out/$name.go.json" 2>/dev/null || true
  python3 "$root/go/testdata/probe_diff.py" \
    "$work/out/$name.ruby.json" "$work/out/$name.go.json" "$name"
}

# A case may declare copy_root_mode 0555, and files cannot be unlinked from a
# directory that is not writable — so a plain `rm -rf` of a previous run leaves
# part of the tree behind. Widen first, then remove.
chmod -R u+rwx "$work" 2>/dev/null || true
rm -rf "$work"
mkdir -p "$work/out"
failed=0

if [ "$mode" = "--fixtures" ]; then
  for store in "$root"/porting/fixtures/*/*/store; do
    fixture=${store%/store}
    name=$(basename "$(dirname "$fixture")")-$(basename "$fixture")
    mkdir -p "$work/$name/.config/tasks" "$work/$name/.state"
    cp -a "$store/." "$work/$name/"
    compare "$name" "$work/$name" || failed=1
  done
else
  cases="$root/porting/runners/cases/phase1.jsonl"
  "$root/porting/runners/ruby/run" --keep --quiet --pin-identity --work "$work/cases" "$cases" >/dev/null
  for copy in "$work"/cases/*; do
    case "$copy" in *.before) continue ;; esac
    [ -d "$copy" ] || continue
    compare "$(basename "$copy")" "$copy" || failed=1
  done
fi

if [ "$failed" -eq 0 ]; then
  echo "probe parity: identical"
else
  echo "probe parity: MISMATCHES above"
fi
exit "$failed"
