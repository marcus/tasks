#!/usr/bin/env bash
#
# test-land.sh — proves porting/land's preconditions, concurrency lock, and
# fail-closed behaviour against throwaway repos, never the real one.
#
#   porting/test-land.sh          # exits 0 on success, one line per assertion
#
# Every scratch repo is a local clone of THIS repo's main branch (read-only
# source; clone is a new directory, nothing here mutates the real checkout),
# with the real porting/merge driver installed and a synthetic manifest and
# td database seeded fresh per test — so land runs against the same shapes
# (merge driver, manifest schema, td review model) production does, without
# touching production state.
#
# THE STANDARD FOR ADDING A TEST HERE: break the behaviour it names,
# deliberately, and confirm this file goes red.
set -uo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
REAL_REPO="$(git -C "$DIR" rev-parse --show-toplevel)"
LAND="$REAL_REPO/porting/land"

PASS=0; FAIL=0
TMPROOT="$(mktemp -d -t landtest)"
cleanup() { rm -rf "$TMPROOT"; }
trap cleanup EXIT

ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

assert_eq() { # assert_eq <label> <expected> <actual>
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (expected '$2', got '$3')"; fi
}

assert_status() { # assert_status <label> <expected-rc> <actual-rc>
  assert_eq "$1" "$2" "$3"
}

# ------------------------------------------------------------- template repo
# One clone of the real repo (main only, so none of the real fleet's live
# port/* branches come along), reused (via cheap local clones) by every test.
TEMPLATE="$TMPROOT/template"
git clone --quiet --branch main --single-branch --no-tags "$REAL_REPO" "$TEMPLATE" \
  || { echo "FATAL: could not clone $REAL_REPO"; exit 1; }

# ---------------------------------------------------------------- fixtures
# One dummy manifest record.
dummy_record() { # dummy_record <id> <status>
  jq -nc --arg id "$1" --arg status "$2" '{
    id: $id, campaign: 1, behavior: ("dummy behavior " + $id),
    target_package: "internal/x", depends_on: [], risk: "low",
    source_paths: [], source_sha: "0000000000000000000000000000000000000000",
    ruby_tests: [], oracle_gaps: [], fixtures: [], fixtures_todo: null,
    observable_outputs: [], platforms: ["any"], perf_budget: null,
    status: $status, evidence: ("porting/evidence/" + $id + "/"),
    intentional_differences: [], notes: null
  }'
}

# A fresh scratch clone with the merge driver installed and its own isolated
# td database (.todos/ is gitignored, so a clone starts with none).
new_repo() { # new_repo <name>
  local path="$TMPROOT/$1"
  git clone --quiet --local "$TEMPLATE" "$path"
  git -C "$path" config user.email "land-test@localhost"
  git -C "$path" config user.name  "land test"
  ( cd "$path" && ruby porting/merge/install >/dev/null 2>&1 )
  ( cd "$path" && td init >/dev/null 2>&1 )
  printf '%s\n' "$path"
}

# Overwrite the manifest with N dummy records plus one named slice record,
# and commit. Mimics the real scale: 144 records on main.
seed_manifest() { # seed_manifest <path> <n-dummies> <slice-id> <slice-status>
  local path="$1" n="$2" slice="$3" status="$4" i
  : >"$path/porting/manifest.jsonl"
  for i in $(seq 1 "$n"); do
    dummy_record "$(printf 'dummy-%04d' "$i")" "not_started" >>"$path/porting/manifest.jsonl"
  done
  dummy_record "$slice" "$status" >>"$path/porting/manifest.jsonl"
  ( cd "$path" && git add porting/manifest.jsonl \
      && git commit -q -m "test: seed $((n + 1))-record manifest" )
}

# Create port/<slice>, mimicking a stale branch: a SMALLER (44-record) older
# manifest snapshot plus the slice's own record advanced, and a source file
# representing the ported code — so the merge exercises both the manifest
# merge driver and an ordinary file addition, same as a real slice.
advance_slice_branch() { # advance_slice_branch <path> <slice>
  local path="$1" slice="$2" i
  ( cd "$path" && git switch -q -c "port/$slice" )
  : >"$path/porting/manifest.jsonl"
  for i in $(seq 1 43); do
    dummy_record "$(printf 'dummy-%04d' "$i")" "not_started" >>"$path/porting/manifest.jsonl"
  done
  dummy_record "$slice" "reviewing" >>"$path/porting/manifest.jsonl"
  mkdir -p "$path/internal/record"
  printf 'package record\n\n// %s: pretend Go port of the slice.\n' "$slice" \
    >"$path/internal/record/$slice.go"
  ( cd "$path" && git add porting/manifest.jsonl "internal/record/$slice.go" \
      && git commit -q -m "test: advance $slice on its port branch (stale 44-record manifest)" )
  ( cd "$path" && git switch -q main )
}

# Real td approval flow: create, start, submit for review, approve — the
# actual representation porting/land inspects (status=closed plus a
# non-superseded review_history entry with decision=approved). --minor lets
# the same session self-review without a second td identity.
approve_slice() { # approve_slice <path> <slice>
  local path="$1" slice="$2" id
  id="$(cd "$path" && td create "port $slice" --minor \
        --labels "porting,porting-slice,slice:$slice" --type task --json \
        | jq -r '.id')"
  ( cd "$path" && td start "$id" >/dev/null \
      && td review "$id" >/dev/null \
      && td approve "$id" --self-review --reason "test approval" >/dev/null )
  printf '%s\n' "$id"
}

sha() { git -C "$1" rev-parse "$2"; }

# ================================================================== (a)
# A clean, approved slice lands: merge commit on main, branch gone, manifest
# says ported, td issue stays closed.
r="$(new_repo happy)"
seed_manifest "$r" 143 "target-slice" "not_started"
advance_slice_branch "$r" "target-slice"
issue="$(approve_slice "$r" "target-slice")"
before="$(sha "$r" main)"

out="$TMPROOT/happy-land.out"
TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" target-slice >"$out" 2>&1
rc=$?
assert_status "(a) land exits 0 on a clean approved slice" 0 "$rc"
after="$(sha "$r" main)"
if [ "$before" != "$after" ]; then ok "(a) main advanced"; else bad "(a) main did not advance"; fi
merged="$(git -C "$r" log --merges -1 --format=%s main 2>/dev/null)"
case "$merged" in
  *"Land port/target-slice"*) ok "(a) an identifiable merge commit landed" ;;
  *) bad "(a) no merge commit found (got: $merged)" ;;
esac
if git -C "$r" rev-parse --verify -q refs/heads/port/target-slice >/dev/null 2>&1; then
  bad "(a) port/target-slice branch was not deleted"
else
  ok "(a) port/target-slice branch deleted"
fi
status_now="$(git -C "$r" show main:porting/manifest.jsonl | jq -rc 'select(.id=="target-slice") | .status')"
assert_eq "(a) manifest marks target-slice ported" "ported" "$status_now"
issue_status="$(cd "$r" && td show "$issue" --json | jq -r .status)"
assert_eq "(a) td issue remains closed" "closed" "$issue_status"
[ -d "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock" ] && bad "(a) lock directory left behind" || ok "(a) lock released"

# ================================================================== (b)
# A genuinely conflicting slice refuses; main is untouched.
r="$(new_repo conflict)"
seed_manifest "$r" 10 "conflict-slice" "not_started"
( cd "$r" && git switch -q -c port/conflict-slice )
printf 'PORT-SIDE-CHANGE\n' >"$r/README.md"
( cd "$r" && git add README.md && git commit -q -m "test: port-side README change" )
( cd "$r" && git switch -q main )
printf 'MAIN-SIDE-CHANGE\n' >"$r/README.md"
( cd "$r" && git add README.md && git commit -q -m "test: main-side README change (diverges after branch)" )
issue="$(approve_slice "$r" "conflict-slice")"
before="$(sha "$r" main)"

out="$TMPROOT/conflict-land.out"
TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" conflict-slice >"$out" 2>&1
rc=$?
assert_status "(b) land refuses (non-zero) on a real conflict" 1 "$rc"
after="$(sha "$r" main)"
assert_eq "(b) main untouched" "$before" "$after"
git -C "$r" rev-parse --verify -q refs/heads/port/conflict-slice >/dev/null 2>&1 \
  && ok "(b) port/conflict-slice branch retained" || bad "(b) branch was deleted despite refusal"
grep -qi conflict "$out" && ok "(b) refusal message names the conflict" \
  || bad "(b) refusal message did not mention the conflict: $(cat "$out")"
[ -d "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock" ] && bad "(b) lock directory left behind" || ok "(b) lock released"

# ================================================================== (c)
# Merge is clean but the test suite fails: refuse, and main must be reset to
# exactly where it was (not left on the un-tested merge commit).
r="$(new_repo failing-tests)"
seed_manifest "$r" 5 "flaky-slice" "not_started"
advance_slice_branch "$r" "flaky-slice"
issue="$(approve_slice "$r" "flaky-slice")"
before="$(sha "$r" main)"

out="$TMPROOT/failing-tests-land.out"
TASKS_REPO="$r" LAND_TEST_CMD="false" "$LAND" flaky-slice >"$out" 2>&1
rc=$?
assert_status "(c) land refuses (non-zero) when tests fail" 1 "$rc"
after="$(sha "$r" main)"
assert_eq "(c) main reset to exactly its pre-merge sha" "$before" "$after"
git -C "$r" rev-parse --verify -q refs/heads/port/flaky-slice >/dev/null 2>&1 \
  && ok "(c) port/flaky-slice branch retained after test failure" \
  || bad "(c) branch was deleted despite test failure"
status_now="$(git -C "$r" show main:porting/manifest.jsonl | jq -rc 'select(.id=="flaky-slice") | .status')"
assert_eq "(c) manifest NOT marked ported on main" "not_started" "$status_now"
grep -qi "test suite failed" "$out" && ok "(c) refusal names the test failure" \
  || bad "(c) refusal message unclear: $(cat "$out")"

# ================================================================== (d)
# Two concurrent invocations: one lands for real, the other either waits and
# then no-ops cleanly, or refuses cleanly — neither corrupts main, and main
# ends up advanced exactly once.
r="$(new_repo concurrent)"
seed_manifest "$r" 8 "race-slice" "not_started"
advance_slice_branch "$r" "race-slice"
issue="$(approve_slice "$r" "race-slice")"
before="$(sha "$r" main)"

# The first invocation holds the lock for a few seconds (via a slow "test
# suite") so the second is guaranteed to contend for it.
out1="$TMPROOT/concurrent-land1.out"; out2="$TMPROOT/concurrent-land2.out"
rc1f="$TMPROOT/concurrent-rc1"; rc2f="$TMPROOT/concurrent-rc2"
( TASKS_REPO="$r" LAND_TEST_CMD="sleep 3 && true" "$LAND" race-slice >"$out1" 2>&1; echo $? >"$rc1f" ) &
p1=$!
sleep 1
( TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" race-slice >"$out2" 2>&1; echo $? >"$rc2f" ) &
p2=$!
wait "$p1" "$p2"
rc1="$(cat "$rc1f")"; rc2="$(cat "$rc2f")"

if [ "$rc1" = 0 ] && [ "$rc2" = 0 ]; then
  ok "(d) both invocations exited 0 (one landed, one no-op/waited)"
else
  bad "(d) unexpected exit codes: rc1=$rc1 rc2=$rc2 (log1: $(cat "$out1") | log2: $(cat "$out2"))"
fi
after="$(sha "$r" main)"
if [ "$before" != "$after" ]; then ok "(d) main advanced exactly once (no corruption)"; else bad "(d) main never advanced"; fi
# The template's history already has its own real merge commits; count only
# merges of THIS slice's landing (its subject is distinctive).
merge_count="$(git -C "$r" log --merges --format=%s main | grep -c "^Land port/race-slice:" || true)"
assert_eq "(d) exactly one merge commit landed (no double-merge)" "1" "$merge_count"
git -C "$r" rev-parse --verify -q refs/heads/port/race-slice >/dev/null 2>&1 \
  && bad "(d) port/race-slice branch was not deleted" || ok "(d) port/race-slice branch deleted"
[ -d "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock" ] && bad "(d) lock directory left behind" || ok "(d) lock released after concurrency"

# ================================================================== (e)
# Re-running land on an already-landed slice is a clean no-op, not an error.
out="$TMPROOT/rerun-land.out"
TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" race-slice >"$out" 2>&1
rc=$?
assert_status "(e) re-running on a landed slice exits 0" 0 "$rc"
after2="$(sha "$r" main)"
assert_eq "(e) main unchanged by the redundant run" "$after" "$after2"
grep -qi "nothing to do\|already landed" "$out" \
  && ok "(e) message says it was a no-op" || bad "(e) unclear no-op message: $(cat "$out")"

# ================================================================== lock robustness
# A lock left behind by a dead process must be reclaimed, not deadlock.
r="$(new_repo stale-lock)"
seed_manifest "$r" 3 "stuck-slice" "not_started"
advance_slice_branch "$r" "stuck-slice"
issue="$(approve_slice "$r" "stuck-slice")"
mkdir -p "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock"
echo 999999 >"$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock/pid"   # a pid that (almost certainly) is not running
date +%s >"$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock/acquired_at"

out="$TMPROOT/stale-lock-land.out"
TASKS_REPO="$r" LAND_LOCK_TIMEOUT=15 LAND_TEST_CMD="true" "$LAND" stuck-slice >"$out" 2>&1
rc=$?
assert_status "(lock) reclaims a lock held by a dead pid instead of deadlocking" 0 "$rc"

# ================================================================== summary
echo "----------------------------------------"
echo "porting/test-land.sh: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
