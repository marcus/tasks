#!/usr/bin/env bash
set -euo pipefail

# One-command release front-end: derive the version, stamp the changelog,
# make the prep commit, push main, then hand off to the existing fail-closed
# publisher (scripts/publish-release.sh) unchanged.
#
# The version is stated ONCE, in order of precedence:
#   RELEASE_VERSION=vX.Y.Z   explicit override (the old flow, still supported)
#   CHANGELOG.md             top heading already stamped `## [X.Y.Z] - date`
#   BUMP=major|minor|patch   top heading is `## [Unreleased]`; bump latest tag
#
# `--dry-run` prints the plan and exits before any mutation.

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

dry_run=false
[[ ${1:-} == --dry-run ]] && dry_run=true

die() {
  echo "Error: $*" >&2
  exit 1
}

valid_version() {
  [[ ${1:-} =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
}

top_heading=$(grep -m1 '^## \[' CHANGELOG.md) ||
  die "CHANGELOG.md has no '## [' heading"

# The section body between the top heading and the next one must have content:
# releasing an empty changelog entry is always a mistake.
section_body=$(awk '/^## \[/{n++; next} n==1' CHANGELOG.md)
[[ -n ${section_body//[[:space:]]/} ]] ||
  die "the top CHANGELOG.md section is empty — write the changelog first"

release_version=${RELEASE_VERSION:-}
needs_stamp=false

if [[ -n $release_version ]]; then
  valid_version "$release_version" ||
    die "RELEASE_VERSION must be strict SemVer vX.Y.Z"
  if [[ $top_heading == '## [Unreleased]' ]]; then
    needs_stamp=true
  elif [[ $top_heading != "## [${release_version#v}] - "* ]]; then
    die "RELEASE_VERSION=$release_version but the top CHANGELOG.md heading is '$top_heading'"
  fi
elif [[ $top_heading == '## [Unreleased]' ]]; then
  bump=${BUMP:-}
  case "$bump" in
    major | minor | patch) ;;
    *) die "top CHANGELOG.md heading is [Unreleased]: set BUMP=major|minor|patch (or RELEASE_VERSION=vX.Y.Z)" ;;
  esac
  latest=$(git tag --list 'v*' --sort=-v:refname | head -1)
  valid_version "$latest" || die "no SemVer tag found to bump from"
  IFS=. read -r major minor patch <<<"${latest#v}"
  case "$bump" in
    major) release_version="v$((major + 1)).0.0" ;;
    minor) release_version="v$major.$((minor + 1)).0" ;;
    patch) release_version="v$major.$minor.$((patch + 1))" ;;
  esac
  needs_stamp=true
elif [[ $top_heading =~ ^'## ['([0-9]+\.[0-9]+\.[0-9]+)']' ]]; then
  release_version="v${BASH_REMATCH[1]}"
  valid_version "$release_version" || die "cannot parse a version from '$top_heading'"
else
  die "cannot derive a version from the top CHANGELOG.md heading '$top_heading'"
fi

git rev-parse --verify --quiet "refs/tags/$release_version" >/dev/null &&
  die "tag $release_version already exists — nothing to release"

# The prep commit may only carry the changelog stamp. Any other dirt is work
# that has not been reviewed into main and must not ride along on a release.
dirty=$(git status --porcelain)
if [[ -n $dirty && $dirty != "M  CHANGELOG.md" && $dirty != " M CHANGELOG.md" ]]; then
  die "working tree has changes beyond CHANGELOG.md — commit or stash them first"
fi

echo "release plan: $release_version"
$needs_stamp && echo "  - stamp CHANGELOG.md [Unreleased] -> [${release_version#v}] - $(date +%Y-%m-%d)"
[[ -n $dirty || $needs_stamp ]] && echo "  - commit 'release: prepare $release_version'"
echo "  - push origin main"
echo "  - publish via scripts/publish-release.sh (tag, workflow, tap)"

if $dry_run; then
  echo "dry run: stopping before any mutation"
  exit 0
fi

# Fail early, before mutating anything, if this operator cannot finish the
# Homebrew publication. publish-release.sh repeats this check.
./scripts/publish-homebrew-tap.sh --check

if $needs_stamp; then
  today=$(date +%Y-%m-%d)
  perl -0pi -e "s/^## \[Unreleased\]/## [${release_version#v}] - $today/m" CHANGELOG.md
  grep -Fq "## [${release_version#v}] - $today" CHANGELOG.md ||
    die "failed to stamp CHANGELOG.md"
fi

if [[ -n $(git status --porcelain) ]]; then
  git add CHANGELOG.md
  git commit -m "release: prepare $release_version"
fi

git push origin main

RELEASE_VERSION=$release_version exec ./scripts/publish-release.sh
