#!/usr/bin/env bash
set -euo pipefail

release_version=${RELEASE_VERSION:-}

# Repeat the live-state check after the potentially long preflight so a remote
# main update or tag race fails closed at the final mutation boundary.
./scripts/check-release-state.sh pre-tag

git tag -a "$release_version" -m "Release $release_version"
git push origin "refs/tags/$release_version"
