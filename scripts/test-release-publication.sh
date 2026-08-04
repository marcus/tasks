#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=publish-homebrew-tap.sh
source "$repo_root/scripts/publish-homebrew-tap.sh"

fail() {
  echo "release publication test failed: $*" >&2
  exit 1
}

for valid in v0.1.0 v1.0.0 v12.34.56; do
  validate_release_version "$valid" || fail "rejected valid version $valid"
done
for invalid in 1.2.3 v1.2 v01.2.3 v1.02.3 v1.2.03 'v1.2.3;false'; do
  if validate_release_version "$invalid"; then
    fail "accepted invalid version $invalid"
  fi
done

[[ $(compare_versions v1.2.3 v1.2.3) == 0 ]] ||
  fail "equal version comparison"
[[ $(compare_versions v1.10.0 v1.9.9) == 1 ]] ||
  fail "newer version comparison"
[[ $(compare_versions v0.9.9 v1.0.0) == -1 ]] ||
  fail "older version comparison"

temporary=$(mktemp -d)
cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT
mkdir "$temporary/current" "$temporary/expected"

sha=0000000000000000000000000000000000000000000000000000000000000000
"$repo_root/scripts/render-homebrew-formula.sh" \
  v1.2.3 "$sha" "$temporary/expected/tasks.rb" >/dev/null
check_formula_transition \
  "$temporary/current/missing.rb" "$temporary/expected/tasks.rb" v1.2.3 ||
  fail "rejected the first formula publication"
"$repo_root/scripts/render-homebrew-formula.sh" \
  v1.2.2 "$sha" "$temporary/current/tasks.rb" >/dev/null
check_formula_transition \
  "$temporary/current/tasks.rb" "$temporary/expected/tasks.rb" v1.2.3 ||
  fail "rejected an upgrade"

cp "$temporary/expected/tasks.rb" "$temporary/current/tasks.rb"
set +e
check_formula_transition \
  "$temporary/current/tasks.rb" "$temporary/expected/tasks.rb" v1.2.3
status=$?
set -e
[[ $status == 10 ]] || fail "exact formula was not idempotent"

sed -i.bak "s/$sha/1111111111111111111111111111111111111111111111111111111111111111/" \
  "$temporary/current/tasks.rb"
rm "$temporary/current/tasks.rb.bak"
if check_formula_transition \
  "$temporary/current/tasks.rb" "$temporary/expected/tasks.rb" v1.2.3 \
  >/dev/null 2>&1; then
  fail "accepted a divergent formula for an existing version"
fi

"$repo_root/scripts/render-homebrew-formula.sh" \
  v1.2.4 "$sha" "$temporary/current/tasks.rb" >/dev/null
if check_formula_transition \
  "$temporary/current/tasks.rb" "$temporary/expected/tasks.rb" v1.2.3 \
  >/dev/null 2>&1; then
  fail "accepted a tap downgrade"
fi

runs='[
  {
    "databaseId": 10,
    "event": "push",
    "headBranch": "v1.2.3",
    "headSha": "wrong",
    "status": "completed",
    "conclusion": "success"
  },
  {
    "databaseId": 11,
    "event": "push",
    "headBranch": "v1.2.3",
    "headSha": "exact",
    "status": "completed",
    "conclusion": "success"
  }
]'
selected=$(select_release_run "$runs" v1.2.3 exact)
[[ $(jq -r .databaseId <<<"$selected") == 11 ]] ||
  fail "did not select the exact tag commit workflow"
[[ -z $(select_release_run "$runs" v1.2.3 missing) ]] ||
  fail "selected a workflow for the wrong commit"
verify_release_run "$selected" v1.2.3 exact ||
  fail "rejected a successful exact workflow"
if verify_release_run "$selected" v1.2.3 wrong; then
  fail "accepted a workflow for the wrong commit"
fi
failed=$(jq '.conclusion = "failure"' <<<"$selected")
if verify_release_run "$failed" v1.2.3 exact; then
  fail "accepted a failed workflow"
fi

mkdir "$temporary/seed"
git -C "$temporary/seed" init --initial-branch=main >/dev/null
git -C "$temporary/seed" config user.name release-test
git -C "$temporary/seed" config user.email release-test@example.invalid
mkdir "$temporary/seed/Formula"
cp "$temporary/current/tasks.rb" "$temporary/seed/Formula/tasks.rb"
git -C "$temporary/seed" add Formula/tasks.rb
git -C "$temporary/seed" commit -m "initial formula" >/dev/null
git clone --bare "$temporary/seed" "$temporary/tap.git" >/dev/null 2>&1
git clone "$temporary/tap.git" "$temporary/publisher" >/dev/null 2>&1
git clone "$temporary/tap.git" "$temporary/racer" >/dev/null 2>&1
for checkout in "$temporary/publisher" "$temporary/racer"; do
  git -C "$checkout" config user.name release-test
  git -C "$checkout" config user.email release-test@example.invalid
done

cp "$temporary/expected/tasks.rb" "$temporary/publisher/Formula/tasks.rb"
git -C "$temporary/publisher" add Formula/tasks.rb
git -C "$temporary/publisher" commit -m "publish formula" >/dev/null
touch "$temporary/racer/unrelated"
git -C "$temporary/racer" add unrelated
git -C "$temporary/racer" commit -m "racing tap update" >/dev/null
git -C "$temporary/racer" push origin main >/dev/null
push_tap_commit "$temporary/publisher" "$temporary/expected/tasks.rb"
git --git-dir="$temporary/tap.git" show main:Formula/tasks.rb \
  >"$temporary/remote-formula.rb"
cmp -s "$temporary/remote-formula.rb" "$temporary/expected/tasks.rb" ||
  fail "race-safe rebase did not publish the exact formula"
[[ $(git --git-dir="$temporary/tap.git" rev-list --count main) == 3 ]] ||
  fail "race-safe rebase did not preserve both commits"

echo "release publication unit tests passed"
