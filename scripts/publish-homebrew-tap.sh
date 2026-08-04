#!/usr/bin/env bash
set -euo pipefail

source_repo=${TASKS_SOURCE_REPO:-marcus/tasks}
tap_repo=${TASKS_TAP_REPO:-marcus/homebrew-tap}
release_workflow=${TASKS_RELEASE_WORKFLOW:-release.yml}
discovery_timeout=${TASKS_RELEASE_DISCOVERY_TIMEOUT:-600}
poll_interval=${TASKS_RELEASE_POLL_INTERVAL:-5}

die() {
  echo "Error: $*" >&2
  exit 1
}

validate_release_version() {
  [[ ${1:-} =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
}

compare_versions() {
  local left=${1#v}
  local right=${2#v}
  local left_major left_minor left_patch
  local right_major right_minor right_patch

  IFS=. read -r left_major left_minor left_patch <<<"$left"
  IFS=. read -r right_major right_minor right_patch <<<"$right"

  if ((10#$left_major != 10#$right_major)); then
    ((10#$left_major > 10#$right_major)) && echo 1 || echo -1
  elif ((10#$left_minor != 10#$right_minor)); then
    ((10#$left_minor > 10#$right_minor)) && echo 1 || echo -1
  elif ((10#$left_patch != 10#$right_patch)); then
    ((10#$left_patch > 10#$right_patch)) && echo 1 || echo -1
  else
    echo 0
  fi
}

formula_version() {
  local formula=$1
  local line

  while IFS= read -r line; do
    if [[ $line =~ /archive/refs/tags/(v[0-9]+\.[0-9]+\.[0-9]+)\.tar\.gz ]]; then
      echo "${BASH_REMATCH[1]}"
      return 0
    fi
  done <"$formula"
  return 1
}

formula_sha256() {
  local formula=$1
  local line

  while IFS= read -r line; do
    if [[ $line =~ ^[[:space:]]*sha256[[:space:]]+\"([0-9a-f]{64})\" ]]; then
      echo "${BASH_REMATCH[1]}"
      return 0
    fi
  done <"$formula"
  return 1
}

# Return 10 when the exact formula is already published. This is deliberately
# distinct from an older formula, which should be replaced, and from a same-
# version mismatch, which needs operator investigation rather than overwriting.
check_formula_transition() {
  local current_formula=$1
  local expected_formula=$2
  local release_version=$3
  local current_version comparison

  # The first tasks release creates Formula/tasks.rb. Subsequent releases use
  # the same downgrade, idempotency, and same-version-divergence guards.
  [[ -f $current_formula ]] || return 0

  current_version=$(formula_version "$current_formula") || {
    echo "tap formula has no recognizable release URL: $current_formula" >&2
    return 1
  }
  comparison=$(compare_versions "$current_version" "$release_version")

  if [[ $comparison == 1 ]]; then
    echo "tap formula $current_version is newer than requested $release_version" >&2
    return 1
  fi
  if [[ $comparison == 0 ]]; then
    if cmp -s "$current_formula" "$expected_formula"; then
      return 10
    fi
    echo "tap formula already names $release_version but differs from the exact rendered formula" >&2
    return 1
  fi
}

select_release_run() {
  local runs_json=$1
  local release_version=$2
  local tag_commit=$3

  jq -c \
    --arg version "$release_version" \
    --arg sha "$tag_commit" \
    '[.[] | select(
      .event == "push" and
      .headBranch == $version and
      .headSha == $sha
    )] | sort_by(.databaseId) | last // empty' <<<"$runs_json"
}

verify_release_run() {
  local run_json=$1
  local release_version=$2
  local tag_commit=$3

  jq -e \
    --arg version "$release_version" \
    --arg sha "$tag_commit" \
    '.event == "push" and
     .headBranch == $version and
     .headSha == $sha and
     .status == "completed" and
     .conclusion == "success"' <<<"$run_json" >/dev/null
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

tap_repo_url() {
  if [[ -n ${TASKS_TAP_REPO_URL:-} ]]; then
    echo "$TASKS_TAP_REPO_URL"
    return
  fi

  if [[ $(gh config get git_protocol --host github.com 2>/dev/null || true) == ssh ]]; then
    echo "git@github.com:${tap_repo}.git"
  else
    echo "https://github.com/${tap_repo}.git"
  fi
}

remote_tag_commit() {
  local release_version=$1
  local refs tag_object tag_commit

  refs=$(git ls-remote --tags "https://github.com/${source_repo}.git" \
    "refs/tags/$release_version" "refs/tags/$release_version^{}")
  tag_object=$(awk -v ref="refs/tags/$release_version" '$2 == ref {print $1}' <<<"$refs")
  tag_commit=$(awk -v ref="refs/tags/$release_version^{}" '$2 == ref {print $1}' <<<"$refs")
  [[ $tag_object =~ ^[0-9a-f]{40}$ ]] ||
    die "public tag $release_version does not exist"
  [[ $tag_commit =~ ^[0-9a-f]{40}$ ]] ||
    die "public tag $release_version is not annotated"
  echo "$tag_commit"
}

check_prerequisites() {
  local command_name
  for command_name in brew curl gh git jq ruby tar; do
    command -v "$command_name" >/dev/null 2>&1 ||
      die "required command is not installed: $command_name"
  done
  if ! command -v shasum >/dev/null 2>&1 &&
    ! command -v sha256sum >/dev/null 2>&1; then
    die "required checksum command is not installed: shasum or sha256sum"
  fi

  [[ $discovery_timeout =~ ^[1-9][0-9]*$ ]] ||
    die "TASKS_RELEASE_DISCOVERY_TIMEOUT must be a positive integer"
  [[ $poll_interval =~ ^[1-9][0-9]*$ ]] ||
    die "TASKS_RELEASE_POLL_INTERVAL must be a positive integer"

  gh auth status --hostname github.com >/dev/null
  [[ $(gh api "repos/$source_repo" --jq .full_name) == "$source_repo" ]] ||
    die "GitHub authentication cannot read $source_repo"
  [[ $(gh api "repos/$tap_repo" --jq .permissions.push) == true ]] ||
    die "GitHub authentication does not have push access to $tap_repo"

  git ls-remote "$(tap_repo_url)" refs/heads/main >/dev/null ||
    die "Git authentication cannot read $tap_repo main"
  git config --get user.name >/dev/null ||
    die "git user.name is required for the tap commit"
  git config --get user.email >/dev/null ||
    die "git user.email is required for the tap commit"
}

wait_for_release_run() {
  local release_version=$1
  local tag_commit=$2
  local deadline=$((SECONDS + discovery_timeout))
  local runs_json selected

  while ((SECONDS < deadline)); do
    runs_json=$(gh run list \
      --repo "$source_repo" \
      --workflow "$release_workflow" \
      --event push \
      --branch "$release_version" \
      --limit 20 \
      --json databaseId,headBranch,headSha,status,conclusion,url,workflowName,event)
    selected=$(select_release_run "$runs_json" "$release_version" "$tag_commit")
    if [[ -n $selected ]]; then
      echo "$selected"
      return 0
    fi
    sleep "$poll_interval"
  done

  echo "timed out waiting for $release_workflow at $release_version ($tag_commit)" >&2
  return 1
}

wait_for_public_release() {
  local release_version=$1
  local deadline=$((SECONDS + discovery_timeout))
  local release_json

  while ((SECONDS < deadline)); do
    if release_json=$(gh release view "$release_version" \
      --repo "$source_repo" \
      --json tagName,isDraft,isPrerelease,url,publishedAt 2>/dev/null) &&
      jq -e --arg version "$release_version" \
        '.tagName == $version and .isDraft == false' \
        <<<"$release_json" >/dev/null; then
      echo "$release_json"
      return 0
    fi
    sleep "$poll_interval"
  done

  echo "timed out waiting for the public GitHub release $release_version" >&2
  return 1
}

verify_archive_shape() {
  local archive=$1
  local release_version=$2
  local listing=$3
  local prefix="${source_repo##*/}-${release_version#v}/"
  local path

  tar -tzf "$archive" >"$listing"
  [[ -s $listing ]] || die "public tag archive is empty"
  while IFS= read -r path; do
    [[ $path == "$prefix"* ]] ||
      die "public tag archive contains an unexpected path: $path"
  done <"$listing"
}

validate_formula() {
  local formula=$1
  local release_version=$2
  local expected_sha=$3
  local validation_tap="tasks-release/validation-$$-$RANDOM"

  ruby -c "$formula" >/dev/null
  [[ $(formula_version "$formula") == "$release_version" ]] ||
    die "rendered formula does not name $release_version"
  [[ $(formula_sha256 "$formula") == "$expected_sha" ]] ||
    die "rendered formula does not contain the public archive checksum"

  # Homebrew intentionally rejects path-based formula audits outside its tap
  # tree. Use a disposable local tap so the candidate is checked as the formula
  # Homebrew will actually load, then remove it even when validation fails.
  (
    local validation_repo
    trap 'brew untap --force "$validation_tap" >/dev/null 2>&1 || true' EXIT
    brew tap-new --no-git "$validation_tap" >/dev/null
    validation_repo=$(brew --repository "$validation_tap")
    mkdir -p "$validation_repo/Formula"
    cp "$formula" "$validation_repo/Formula/tasks.rb"
    brew style --formula "$validation_tap/tasks"
    brew audit --strict --online --formula "$validation_tap/tasks"
  )
}

push_tap_commit() {
  local tap_dir=$1
  local expected_formula=$2
  local attempt

  for attempt in 1 2 3; do
    if git -C "$tap_dir" push origin HEAD:main; then
      return 0
    fi
    if [[ $attempt == 3 ]]; then
      break
    fi

    echo "tap push raced or failed; rebasing onto the latest origin/main (attempt $((attempt + 1))/3)" >&2
    git -C "$tap_dir" fetch origin main
    if ! git -C "$tap_dir" rebase origin/main; then
      git -C "$tap_dir" rebase --abort >/dev/null 2>&1 || true
      die "tap update conflicts with origin/main; no force-push was attempted"
    fi
    if ! cmp -s "$tap_dir/Formula/tasks.rb" "$expected_formula"; then
      die "tap formula changed during rebase; refusing to overwrite the racing update"
    fi
  done

  die "could not push the tap update after 3 race-safe attempts"
}

publish_tap() (
  local release_version=$1
  local tag_commit run_json run_id verified_run release_json
  local temporary archive archive_listing archive_sha source_tree formula_dir expected_formula
  local tap_dir tap_formula transition_status remote_formula remote_version remote_sha remote_commit

  tag_commit=$(remote_tag_commit "$release_version")

  echo "waiting for the exact $release_workflow run for $release_version ($tag_commit)"
  run_json=$(wait_for_release_run "$release_version" "$tag_commit")
  run_id=$(jq -r .databaseId <<<"$run_json")
  gh run watch "$run_id" --repo "$source_repo" --exit-status
  verified_run=$(gh run view "$run_id" \
    --repo "$source_repo" \
    --json databaseId,headBranch,headSha,status,conclusion,url,workflowName,event)
  verify_release_run "$verified_run" "$release_version" "$tag_commit" ||
    die "GitHub Actions run $run_id is not a successful exact-tag release run"

  release_json=$(wait_for_public_release "$release_version")
  echo "verified release $(jq -r .url <<<"$release_json") via workflow $(jq -r .url <<<"$verified_run")"

  temporary=$(mktemp -d)
  cleanup() {
    rm -rf "$temporary"
  }
  trap cleanup EXIT

  archive="$temporary/${release_version}.tar.gz"
  archive_listing="$temporary/archive-contents.txt"
  curl --fail --location --retry 3 --output "$archive" \
    "https://github.com/${source_repo}/archive/refs/tags/${release_version}.tar.gz"
  verify_archive_shape "$archive" "$release_version" "$archive_listing"
  archive_sha=$(sha256_file "$archive")
  tar -xzf "$archive" -C "$temporary"
  source_tree="$temporary/${source_repo##*/}-${release_version#v}"
  [[ -x $source_tree/scripts/render-homebrew-formula.sh ]] ||
    die "public tag archive does not contain the executable formula renderer"

  formula_dir="$temporary/rendered"
  mkdir "$formula_dir"
  expected_formula="$formula_dir/tasks.rb"
  "$source_tree/scripts/render-homebrew-formula.sh" \
    "$release_version" "$archive_sha" "$expected_formula"
  validate_formula "$expected_formula" "$release_version" "$archive_sha"

  tap_dir="$temporary/homebrew-tap"
  git clone --branch main --single-branch "$(tap_repo_url)" "$tap_dir"
  tap_formula="$tap_dir/Formula/tasks.rb"
  mkdir -p "$(dirname "$tap_formula")"

  set +e
  check_formula_transition "$tap_formula" "$expected_formula" "$release_version"
  transition_status=$?
  set -e
  if [[ $transition_status == 10 ]]; then
    echo "$tap_repo already contains the exact $release_version formula"
  elif [[ $transition_status != 0 ]]; then
    die "unsafe tap formula transition"
  else
    cp "$expected_formula" "$tap_formula"
    git -C "$tap_dir" add Formula/tasks.rb
    git -C "$tap_dir" diff --cached --check
    git -C "$tap_dir" commit -m "tasks $release_version"
    push_tap_commit "$tap_dir" "$expected_formula"
  fi

  git -C "$tap_dir" fetch origin main
  remote_commit=$(git -C "$tap_dir" rev-parse FETCH_HEAD)
  remote_formula="$temporary/remote-tasks.rb"
  git -C "$tap_dir" show "$remote_commit:Formula/tasks.rb" >"$remote_formula"
  cmp -s "$remote_formula" "$expected_formula" ||
    die "remote tap formula does not match the exact rendered $release_version formula"
  remote_version=$(formula_version "$remote_formula")
  remote_sha=$(formula_sha256 "$remote_formula")
  [[ $remote_version == "$release_version" && $remote_sha == "$archive_sha" ]] ||
    die "remote tap formula version or checksum is stale"

  echo "published and verified $tap_repo@$remote_commit: tasks $remote_version ($remote_sha)"
)

main() {
  local mode=${1:-publish}
  local release_version=${RELEASE_VERSION:-}

  if [[ $mode != publish && $mode != --check ]]; then
    echo "usage: RELEASE_VERSION=vX.Y.Z $0 [--check]" >&2
    exit 2
  fi
  validate_release_version "$release_version" ||
    die "RELEASE_VERSION must be strict SemVer vX.Y.Z"

  check_prerequisites
  if [[ $mode == --check ]]; then
    echo "release publication prerequisites verified for $source_repo -> $tap_repo"
    return
  fi

  publish_tap "$release_version"
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
  main "$@"
fi
