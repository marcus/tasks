#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# Do not create a tag that this operator cannot carry through to the supported
# Homebrew install path. This is repeated by the publisher after the release
# workflow, but the pre-tag check makes missing local authorization fail early.
"$repo_root/scripts/publish-homebrew-tap.sh" --check
"$repo_root/scripts/publish-release-tag.sh"
"$repo_root/scripts/publish-homebrew-tap.sh"
