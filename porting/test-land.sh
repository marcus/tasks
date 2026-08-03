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
advance_slice_branch() { # advance_slice_branch <path> <slice> [branch-status]
  local path="$1" slice="$2" branch_status="${3:-reviewing}" i
  ( cd "$path" && git switch -q -c "port/$slice" )
  : >"$path/porting/manifest.jsonl"
  for i in $(seq 1 43); do
    dummy_record "$(printf 'dummy-%04d' "$i")" "not_started" >>"$path/porting/manifest.jsonl"
  done
  dummy_record "$slice" "$branch_status" >>"$path/porting/manifest.jsonl"
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

# ================================================================== (f)
# Regression: the branch already advanced the manifest status to "ported"
# itself (the normal path — agents commit that as part of doing the work, as
# port/format-parse's real "porting: mark format parser ported" commit did).
# land must not try to write an identical status and choke on git refusing
# an empty commit; it must recognize the status is already right and land
# straight through to a clean success.
r="$(new_repo already-ported)"
seed_manifest "$r" 12 "preadvanced-slice" "not_started"
advance_slice_branch "$r" "preadvanced-slice" "ported"
issue="$(approve_slice "$r" "preadvanced-slice")"
before="$(sha "$r" main)"

out="$TMPROOT/preadvanced-land.out"
TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" preadvanced-slice >"$out" 2>&1
rc=$?
assert_status "(f) land exits 0 when the branch already set status to ported" 0 "$rc"
after="$(sha "$r" main)"
if [ "$before" != "$after" ]; then ok "(f) main advanced"; else bad "(f) main did not advance"; fi
merged="$(git -C "$r" log --merges -1 --format=%s main 2>/dev/null)"
case "$merged" in
  *"Land port/preadvanced-slice"*) ok "(f) an identifiable merge commit landed" ;;
  *) bad "(f) no merge commit found (got: $merged)" ;;
esac
status_now="$(git -C "$r" show main:porting/manifest.jsonl | jq -rc 'select(.id=="preadvanced-slice") | .status')"
assert_eq "(f) manifest still marks preadvanced-slice ported" "ported" "$status_now"
if git -C "$r" rev-parse --verify -q refs/heads/port/preadvanced-slice >/dev/null 2>&1; then
  bad "(f) port/preadvanced-slice branch was not deleted"
else
  ok "(f) port/preadvanced-slice branch deleted"
fi
[ -d "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock" ] \
  && bad "(f) lock directory left behind" || ok "(f) lock released"

# ================================================================== (g)
# Regression: invoking with the td issue id (what PORTING.md's iteration
# steps and `td` itself hand an agent, e.g. td-2f853a) must work identically
# to invoking with the manifest slice id.
r="$(new_repo by-td-id)"
seed_manifest "$r" 7 "tdid-slice" "not_started"
advance_slice_branch "$r" "tdid-slice"
issue="$(approve_slice "$r" "tdid-slice")"
before="$(sha "$r" main)"

out="$TMPROOT/by-td-id-land.out"
TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" "$issue" >"$out" 2>&1
rc=$?
assert_status "(g) land exits 0 when invoked by td id" 0 "$rc"
after="$(sha "$r" main)"
if [ "$before" != "$after" ]; then ok "(g) main advanced"; else bad "(g) main did not advance"; fi
merged="$(git -C "$r" log --merges -1 --format=%s main 2>/dev/null)"
case "$merged" in
  *"Land port/tdid-slice"*) ok "(g) td id resolved to the correct slice/branch" ;;
  *) bad "(g) no merge commit found for the resolved slice (got: $merged)" ;;
esac
status_now="$(git -C "$r" show main:porting/manifest.jsonl | jq -rc 'select(.id=="tdid-slice") | .status')"
assert_eq "(g) manifest marks tdid-slice ported" "ported" "$status_now"
git -C "$r" rev-parse --verify -q refs/heads/port/tdid-slice >/dev/null 2>&1 \
  && bad "(g) port/tdid-slice branch was not deleted" || ok "(g) port/tdid-slice branch deleted"

# ================================================================== (h)
# --auto with nothing eligible: several port/* branches exist but none are
# closed+approved (one not_started/no issue, one still in review). Must exit
# 0, touch nothing, and must NOT run the test suite — LAND_TEST_CMD is set to
# a sentinel that writes a marker file; if --auto's cheap eligibility scan
# ever fell through to landing, the marker would appear.
r="$(new_repo auto-none)"
seed_manifest "$r" 6 "auto-none-slice" "not_started"
advance_slice_branch "$r" "auto-none-untouched-1"   # no td issue at all
( cd "$r" && git branch port/auto-none-untouched-2 main )  # branch with no manifest entry either
marker="$TMPROOT/auto-none-marker"
rm -f "$marker"
before="$(sha "$r" main)"

out="$TMPROOT/auto-none.out"
TASKS_REPO="$r" LAND_TEST_CMD="touch '$marker' && true" "$LAND" --auto >"$out" 2>&1
rc=$?
assert_status "(h) --auto exits 0 when nothing is eligible" 0 "$rc"
after="$(sha "$r" main)"
assert_eq "(h) main untouched" "$before" "$after"
[ -f "$marker" ] && bad "(h) test suite ran even though nothing was eligible" \
  || ok "(h) test suite never ran (cheap checks only)"
git -C "$r" rev-parse --verify -q refs/heads/port/auto-none-untouched-1 >/dev/null 2>&1 \
  && ok "(h) ineligible branch (no td issue) retained" || bad "(h) ineligible branch was deleted"
git -C "$r" rev-parse --verify -q refs/heads/port/auto-none-untouched-2 >/dev/null 2>&1 \
  && ok "(h) ineligible branch (no manifest entry) retained" || bad "(h) ineligible branch was deleted"
[ -d "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock" ] \
  && bad "(h) lock directory left behind" || ok "(h) lock released (never acquired)"

# ================================================================== (i)
# --auto with exactly one eligible slice among several ineligible branches:
# only the eligible one lands; the rest are untouched.
r="$(new_repo auto-one)"
seed_manifest "$r" 6 "auto-one-eligible" "not_started"
advance_slice_branch "$r" "auto-one-eligible"
eligible_issue="$(approve_slice "$r" "auto-one-eligible")"

advance_slice_branch "$r" "auto-one-not-approved"
not_approved_issue="$(cd "$r" && td create "port auto-one-not-approved" --minor \
      --labels "porting,porting-slice,slice:auto-one-not-approved" --type task --json | jq -r '.id')"
( cd "$r" && td start "$not_approved_issue" >/dev/null && td review "$not_approved_issue" >/dev/null )

advance_slice_branch "$r" "auto-one-no-issue"   # branch exists, no td issue labeled for it

before="$(sha "$r" main)"
out="$TMPROOT/auto-one.out"
TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" --auto >"$out" 2>&1
rc=$?
assert_status "(i) --auto exits 0 when the eligible slice lands cleanly" 0 "$rc"
after="$(sha "$r" main)"
if [ "$before" != "$after" ]; then ok "(i) main advanced"; else bad "(i) main did not advance"; fi
merged="$(git -C "$r" log --merges -1 --format=%s main 2>/dev/null)"
case "$merged" in
  *"Land port/auto-one-eligible"*) ok "(i) the eligible slice landed" ;;
  *) bad "(i) wrong or no merge commit landed (got: $merged)" ;;
esac
git -C "$r" rev-parse --verify -q refs/heads/port/auto-one-eligible >/dev/null 2>&1 \
  && bad "(i) eligible branch was not deleted" || ok "(i) eligible branch deleted"
git -C "$r" rev-parse --verify -q refs/heads/port/auto-one-not-approved >/dev/null 2>&1 \
  && ok "(i) not-yet-approved branch retained" || bad "(i) not-yet-approved branch was touched"
git -C "$r" rev-parse --verify -q refs/heads/port/auto-one-no-issue >/dev/null 2>&1 \
  && ok "(i) no-issue branch retained" || bad "(i) no-issue branch was touched"
status_now="$(git -C "$r" show main:porting/manifest.jsonl | jq -rc 'select(.id=="auto-one-not-approved") | .status')"
assert_eq "(i) ineligible slice's branch was never merged into main" "" "$status_now"
[ -d "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock" ] \
  && bad "(i) lock directory left behind" || ok "(i) lock released"

# ================================================================== (j)
# --auto respects the landing lock: a lock held by a live, competing --auto
# (or single-slice) invocation must make this one wait, not race main.
r="$(new_repo auto-lock)"
seed_manifest "$r" 4 "auto-lock-slice" "not_started"
advance_slice_branch "$r" "auto-lock-slice"
approve_slice "$r" "auto-lock-slice" >/dev/null
before="$(sha "$r" main)"

out1="$TMPROOT/auto-lock1.out"; out2="$TMPROOT/auto-lock2.out"
rc1f="$TMPROOT/auto-lock-rc1"; rc2f="$TMPROOT/auto-lock-rc2"
( TASKS_REPO="$r" LAND_TEST_CMD="sleep 3 && true" "$LAND" auto-lock-slice >"$out1" 2>&1; echo $? >"$rc1f" ) &
p1=$!
sleep 1
( TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" --auto >"$out2" 2>&1; echo $? >"$rc2f" ) &
p2=$!
wait "$p1" "$p2"
rc1="$(cat "$rc1f")"; rc2="$(cat "$rc2f")"

if [ "$rc1" = 0 ] && [ "$rc2" = 0 ]; then
  ok "(j) both the single-slice landing and the --auto scan exited 0"
else
  bad "(j) unexpected exit codes: rc1=$rc1 rc2=$rc2 (log1: $(cat "$out1") | log2: $(cat "$out2"))"
fi
after="$(sha "$r" main)"
merge_count="$(git -C "$r" log --merges --format=%s main | grep -c "^Land port/auto-lock-slice:" || true)"
assert_eq "(j) exactly one merge landed (auto waited on the lock, did not double-land)" "1" "$merge_count"
[ -d "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock" ] \
  && bad "(j) lock directory left behind" || ok "(j) lock released after contention"

# ================================================================== (k)
# Regression: a slice that already landed (main's manifest already says
# ported) but whose branch delete failed last time — recreated here to
# simulate exactly that state — is cleaned up on the next run without
# re-merging or re-running the test suite.
r="$(new_repo linger)"
seed_manifest "$r" 9 "linger-slice" "not_started"
advance_slice_branch "$r" "linger-slice"
issue="$(approve_slice "$r" "linger-slice")"
branch_sha="$(sha "$r" port/linger-slice)"
TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" linger-slice >/dev/null 2>&1
if git -C "$r" rev-parse --verify -q refs/heads/port/linger-slice >/dev/null 2>&1; then
  echo "FATAL: (k) setup expected the first land to delete the branch"; exit 1
fi
git -C "$r" branch port/linger-slice "$branch_sha"   # simulate a delete that failed last time
before="$(sha "$r" main)"

marker="$TMPROOT/linger-marker"; rm -f "$marker"
out="$TMPROOT/linger-land.out"
TASKS_REPO="$r" LAND_TEST_CMD="touch '$marker' && true" "$LAND" linger-slice >"$out" 2>&1
rc=$?
assert_status "(k) cleanup of a lingering landed branch exits 0" 0 "$rc"
after="$(sha "$r" main)"
assert_eq "(k) main untouched (no re-merge)" "$before" "$after"
[ -f "$marker" ] && bad "(k) test suite ran again for an already-landed slice" \
  || ok "(k) test suite did not run again"
git -C "$r" rev-parse --verify -q refs/heads/port/linger-slice >/dev/null 2>&1 \
  && bad "(k) lingering branch was not deleted" || ok "(k) lingering branch deleted"
grep -qi "already landed" "$out" && ok "(k) message says already landed" \
  || bad "(k) unclear message: $(cat "$out")"
[ -d "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock" ] \
  && bad "(k) lock directory left behind" || ok "(k) lock released"

# ================================================================== (l)
# Same lingering-branch state, but the branch is checked out in another
# worktree at the moment of the retry: land must defer the deletion (not
# error, not force it) and leave the branch alone for a later run — then
# actually delete it once that worktree lets go.
r="$(new_repo linger-checked-out)"
seed_manifest "$r" 5 "linger-wt-slice" "not_started"
advance_slice_branch "$r" "linger-wt-slice"
issue="$(approve_slice "$r" "linger-wt-slice")"
branch_sha="$(sha "$r" port/linger-wt-slice)"
TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" linger-wt-slice >/dev/null 2>&1
git -C "$r" branch port/linger-wt-slice "$branch_sha"
wt="$TMPROOT/linger-wt-checkout"
git -C "$r" worktree add --quiet "$wt" port/linger-wt-slice >/dev/null 2>&1
before="$(sha "$r" main)"

out="$TMPROOT/linger-wt-land.out"
TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" linger-wt-slice >"$out" 2>&1
rc=$?
assert_status "(l) deferring a branch checked out elsewhere exits 0" 0 "$rc"
after="$(sha "$r" main)"
assert_eq "(l) main untouched" "$before" "$after"
git -C "$r" rev-parse --verify -q refs/heads/port/linger-wt-slice >/dev/null 2>&1 \
  && ok "(l) checked-out branch retained (deferred)" || bad "(l) branch was deleted while checked out elsewhere"
grep -qi "checked out at" "$out" && ok "(l) message names the deferral" \
  || bad "(l) unclear message: $(cat "$out")"

git -C "$r" worktree remove --force "$wt" >/dev/null 2>&1
out2="$TMPROOT/linger-wt-land2.out"
TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" linger-wt-slice >"$out2" 2>&1
rc2=$?
assert_status "(l) a later run deletes it once released" 0 "$rc2"
git -C "$r" rev-parse --verify -q refs/heads/port/linger-wt-slice >/dev/null 2>&1 \
  && bad "(l) branch was not deleted after the worktree released it" || ok "(l) branch deleted after release"
[ -d "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock" ] \
  && bad "(l) lock directory left behind" || ok "(l) lock released"

# ================================================================== (m)
# Regression: an external SIGTERM delivered to `porting/land` itself while it
# is blocked inside the test suite. This is NOT loop.sh's own timeout (loop.sh
# deliberately never signals `land`, see run_land_auto's header comment) —
# it is the scenario Marcus reproduced by hand: something outside this
# script (an operator's Ctrl-C, a process manager, a machine restart) kills
# `land` mid-run_tests. On this fleet's bash (Apple's bash 3.2), a process
# blocked on a command-substitution `wait()` can die by the signal's default
# action without running its own EXIT/INT/TERM trap — so `cleanup` may never
# fire. Two things must still be true after that:
#   1. the landing lock is left with a dead pid inside it, not held forever;
#   2. the NEXT invocation that actually needs the lock (not short-circuited
#      by the already-landed fast path) detects the dead pid and reclaims it,
#      and lands cleanly.
# What this does NOT claim: that main is left "clean" in the sense of
# untouched. It is not — the merge and manifest-status commit happen BEFORE
# run_tests, so a kill during the test run leaves main holding a merge whose
# tests never ran, marked ported anyway, with its branch undeleted. That is
# the concrete reason run_land_auto refuses to signal `land`: doing so from
# loop.sh would be able to trigger exactly this. Recovery here is about the
# LOCK, not about retroactively validating a merge that a kill interrupted.
r="$(new_repo sigterm)"
seed_manifest "$r" 3 "sigterm-slice" "not_started"
advance_slice_branch "$r" "sigterm-slice"
approve_slice "$r" "sigterm-slice" >/dev/null

out="$TMPROOT/sigterm-land.out"
TASKS_REPO="$r" LAND_TEST_CMD="sleep 20" LAND_LOCK_STALE=5 "$LAND" sigterm-slice >"$out" 2>&1 &
land_pid=$!

waited=0
while [ ! -d "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock" ] && [ "$waited" -lt 50 ]; do
  sleep 0.2; waited=$((waited + 1))
done
if [ ! -d "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock" ]; then
  bad "(m) setup: land never acquired the lock before the kill"
else
  ok "(m) setup: land acquired the lock and is inside the test suite"
fi
kill -TERM "$land_pid" 2>/dev/null
wait "$land_pid" 2>/dev/null
sleep 1
kill -0 "$land_pid" 2>/dev/null \
  && bad "(m) land process survived SIGTERM (test setup invalid)" \
  || ok "(m) the killed land process is gone"

# A second, unrelated slice forces a REAL lock acquisition attempt (the first
# slice's own idempotent fast path would short-circuit before ever touching
# the lock, proving nothing about reclaim).
seed_manifest2_append() { # append one more manifest record + commit, on main
  dummy_record "$2" "not_started" >> "$1/porting/manifest.jsonl"
  ( cd "$1" && git switch -q main >/dev/null 2>&1 || true
    git add porting/manifest.jsonl && git commit -q -m "test: seed $2" )
}
( cd "$r" && git switch -q main )
seed_manifest2_append "$r" "sigterm-other-slice"
advance_slice_branch "$r" "sigterm-other-slice"
approve_slice "$r" "sigterm-other-slice" >/dev/null

out2="$TMPROOT/sigterm-land-recover.out"
TASKS_REPO="$r" LAND_TEST_CMD="true" LAND_LOCK_TIMEOUT=20 LAND_LOCK_STALE=1 \
  "$LAND" sigterm-other-slice >"$out2" 2>&1
rc2=$?
assert_status "(m) a later run reclaims the dead-pid lock and lands cleanly" 0 "$rc2"
grep -qi "dead pid" "$out2" && ok "(m) the reclaim is logged" \
  || bad "(m) no dead-pid reclaim message: $(cat "$out2")"
[ -d "$(git -C "$r" rev-parse --absolute-git-dir)/porting-land.lock" ] \
  && bad "(m) lock directory left behind after recovery" || ok "(m) lock released after recovery"
git -C "$r" show main:porting/manifest.jsonl | jq -rc 'select(.id=="sigterm-other-slice") | .status' \
  | grep -q ported && ok "(m) the second slice landed despite the stranded lock" \
  || bad "(m) the second slice did not land"

# ================================================================== (n)
# The exact production bug: `--auto`'s branch scan (not a direct single-slice
# call) rescanning a landed-but-undeleted branch must be a clean pass, never
# a spurious FAILED from re-attempting a merge of an already-merged branch.
# `td close` already ran on the first landing, so the issue is closed+
# approved and `is_eligible` legitimately re-selects it — the fix has to be
# in land_one's idempotency check (proven above by (k)/(l)), not in auto's
# discovery. This proves the two compose correctly end to end.
r="$(new_repo autolinger)"
seed_manifest "$r" 4 "autolinger-slice" "not_started"
advance_slice_branch "$r" "autolinger-slice"
approve_slice "$r" "autolinger-slice" >/dev/null
branch_sha="$(sha "$r" port/autolinger-slice)"
TASKS_REPO="$r" LAND_TEST_CMD="true" "$LAND" autolinger-slice >/dev/null 2>&1
git -C "$r" branch port/autolinger-slice "$branch_sha"   # simulate last time's failed delete
before="$(sha "$r" main)"

marker="$TMPROOT/autolinger-marker"; rm -f "$marker"
out="$TMPROOT/autolinger-auto.out"
TASKS_REPO="$r" LAND_TEST_CMD="touch '$marker' && true" "$LAND" --auto >"$out" 2>&1
rc=$?
assert_status "(n) --auto exits 0 rescanning a landed-but-lingering branch" 0 "$rc"
after="$(sha "$r" main)"
assert_eq "(n) main untouched (no re-merge via --auto)" "$before" "$after"
[ -f "$marker" ] && bad "(n) --auto re-ran the test suite for an already-landed slice" \
  || ok "(n) --auto did not re-run the test suite"
git -C "$r" rev-parse --verify -q refs/heads/port/autolinger-slice >/dev/null 2>&1 \
  && bad "(n) --auto left the lingering branch in place" || ok "(n) --auto deleted the lingering branch"
grep -q "FAILED landing" "$out" && bad "(n) --auto reported a spurious FAILED: $(cat "$out")" \
  || ok "(n) --auto reported no FAILED"
grep -q "failed=0" "$out" && ok "(n) auto summary reports zero failed" \
  || bad "(n) auto summary did not report failed=0: $(cat "$out")"

# ================================================================== summary
echo "----------------------------------------"
echo "porting/test-land.sh: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
