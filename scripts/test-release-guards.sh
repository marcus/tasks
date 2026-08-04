#!/usr/bin/env bash
set -euo pipefail

dist=${1:-dist}
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
[[ -d $dist ]] || {
  echo "release directory does not exist: $dist" >&2
  exit 1
}

temporary=$(mktemp -d)
cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT

# Make must accept RELEASE_VERSION only as environment data. It must never
# interpolate an untrusted make command-line value into shell source.
sentinel="$temporary/version-injection-ran"
for malicious_version in \
  "v1.0.0\"; touch $sentinel; : #" \
  "v1.0.0'; touch $sentinel; : #" \
  "v1.0.0\$(touch $sentinel)" \
  "v1.0.0\$(shell touch $sentinel)"; do
  if env RELEASE_VERSION="$malicious_version" \
    make -s -C "$repo_root" check-release-state >/dev/null 2>&1; then
    echo "malicious release version unexpectedly passed" >&2
    exit 1
  fi
done
if [[ -e $sentinel ]]; then
  echo "release version was executed as shell source" >&2
  exit 1
fi

# Exercise both modes against a local bare remote: the annotated tag is valid
# only while it resolves to the live main commit.
guard_repo="$temporary/guard-repo"
remote="$temporary/origin.git"
git init --bare --quiet "$remote"
git init --quiet --initial-branch=main "$guard_repo"
mkdir "$guard_repo/scripts"
cp "$repo_root/scripts/check-release-state.sh" "$guard_repo/scripts/"
cp "$repo_root/CHANGELOG.md" "$guard_repo/"
(
  cd "$guard_repo"
  git add CHANGELOG.md scripts/check-release-state.sh
  git -c user.name=release-test -c user.email=release-test@example.invalid \
    commit --quiet -m initial
  git remote add origin "$remote"
  git push --quiet -u origin main
  RELEASE_VERSION=v1.0.0 ./scripts/check-release-state.sh pre-tag >/dev/null
  git -c user.name=release-test -c user.email=release-test@example.invalid \
    tag -a v1.0.0 -m "Release v1.0.0"
  git push --quiet origin refs/tags/v1.0.0
  tag_commit=$(git rev-parse "refs/tags/v1.0.0^{commit}")
  git checkout --quiet --detach "$tag_commit"
  git tag -d v1.0.0 >/dev/null
  RELEASE_VERSION=v1.0.0 ./scripts/check-release-state.sh tagged >/dev/null
  git checkout --quiet main
  git -c user.name=release-test -c user.email=release-test@example.invalid \
    commit --quiet --allow-empty -m drift
  git push --quiet origin main
  git checkout --quiet --detach v1.0.0
  if RELEASE_VERSION=v1.0.0 \
    ./scripts/check-release-state.sh tagged >/dev/null 2>&1; then
    echo "tagged state unexpectedly accepted a tag behind live main" >&2
    exit 1
  fi
)

# A wrapped archive may not smuggle an executable beside its wrapper
# directory. Checksums are intentionally left stale: shape validation must
# reject the archive before checksum validation.
probe_dist="$temporary/dist"
cp -R "$dist" "$probe_dist"
archive=$(find "$probe_dist" -mindepth 1 -maxdepth 1 \
  -type f -name '*darwin_amd64.tar.gz' -print -quit)
[[ -n $archive ]] || {
  echo "no darwin_amd64 archive found for negative test" >&2
  exit 1
}
unpack="$temporary/unpack"
mkdir "$unpack"
tar -xzf "$archive" -C "$unpack"
touch "$unpack/rogue"
chmod +x "$unpack/rogue"
tar -czf "$archive" -C "$unpack" .
if "$repo_root/scripts/verify-release-archives.sh" \
  "$probe_dist" >/dev/null 2>&1; then
  echo "archive verifier accepted an extra top-level executable" >&2
  exit 1
fi

echo "release guard negative tests passed"
