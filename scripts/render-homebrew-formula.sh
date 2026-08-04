#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 vX.Y.Z SHA256 OUTPUT/tasks.rb" >&2
  exit 2
}

[[ $# -eq 3 ]] || usage

version=$1
sha256=$2
output=$3

[[ $version =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || {
  echo "invalid release version: $version" >&2
  exit 2
}
[[ $sha256 =~ ^[0-9a-f]{64}$ ]] || {
  echo "invalid SHA-256: $sha256" >&2
  exit 2
}
[[ $(basename "$output") == tasks.rb ]] || {
  echo "formula output must be named tasks.rb" >&2
  exit 2
}
[[ -d $(dirname "$output") ]] || {
  echo "formula output directory does not exist: $(dirname "$output")" >&2
  exit 2
}

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
template="$repo_root/packaging/homebrew/tasks.rb.tmpl"
[[ -f $template ]] || {
  echo "formula template does not exist: $template" >&2
  exit 1
}

temporary=$(mktemp "$(dirname "$output")/.tasks.rb.XXXXXX")
cleanup() {
  rm -f "$temporary"
}
trap cleanup EXIT

sed \
  -e "s/@VERSION@/$version/g" \
  -e "s/@SHA256@/$sha256/g" \
  "$template" >"$temporary"

if grep -Eq '@(VERSION|SHA256)@' "$temporary"; then
  echo "formula still contains an unresolved placeholder" >&2
  exit 1
fi

ruby -c "$temporary" >/dev/null
chmod 0644 "$temporary"
mv "$temporary" "$output"
trap - EXIT

echo "rendered $output for $version"
