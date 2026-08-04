#!/usr/bin/env bash
set -euo pipefail

dist=${1:-dist}
[[ -d $dist ]] || {
  echo "release directory does not exist: $dist" >&2
  exit 1
}
[[ -f $dist/checksums.txt ]] || {
  echo "missing $dist/checksums.txt" >&2
  exit 1
}

temporary=$(mktemp -d)
cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT

host_os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64) host_arch=amd64 ;;
  arm64 | aarch64) host_arch=arm64 ;;
  *) host_arch=unsupported ;;
esac

find cmd -mindepth 1 -maxdepth 1 -type d -exec basename {} \; |
  LC_ALL=C sort >"$temporary/expected-commands"
if [[ ! -s $temporary/expected-commands ]]; then
  echo "cmd/ contains no commands" >&2
  exit 1
fi

find "$dist" -mindepth 1 -maxdepth 1 -type f -name '*.tar.gz' |
  LC_ALL=C sort >"$temporary/archives"
archive_count=$(wc -l <"$temporary/archives" | tr -d ' ')
[[ $archive_count -eq 4 ]] || {
  echo "expected 4 release archives, found $archive_count" >&2
  exit 1
}

archive_names=$(tr '[:upper:]' '[:lower:]' <"$temporary/archives")
for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
  grep -Eq "_${target}\.tar\.gz$" <<<"$archive_names" || {
    echo "missing archive for $target" >&2
    exit 1
  }
done

index=0
while IFS= read -r archive; do
  index=$((index + 1))
  unpack="$temporary/unpack-$index"
  mkdir "$unpack"
  tar -xzf "$archive" -C "$unpack"

  find "$unpack" -mindepth 1 -maxdepth 1 >"$temporary/top-level-$index"
  top_level_count=$(wc -l <"$temporary/top-level-$index" | tr -d ' ')
  [[ $top_level_count -eq 1 ]] || {
    echo "$archive must contain exactly one top-level entry" >&2
    exit 1
  }
  root=$(head -n 1 "$temporary/top-level-$index")
  [[ -d $root && ! -L $root ]] || {
    echo "$archive's only top-level entry must be a directory" >&2
    exit 1
  }

  find "$root" -mindepth 1 -maxdepth 1 -type f -perm -111 \
    -exec basename {} \; | LC_ALL=C sort >"$temporary/actual-commands-$index"
  if ! diff -u "$temporary/expected-commands" "$temporary/actual-commands-$index"; then
    echo "$archive command set does not match cmd/" >&2
    exit 1
  fi

  for required in README.md CHANGELOG.md LICENSE internal/agentcontext/TASK_AGENT.md docs/api/openapi.yaml; do
    [[ -f $root/$required ]] || {
      echo "$archive is missing $required" >&2
      exit 1
    }
  done
  archive_lower=$(printf '%s' "$archive" | tr '[:upper:]' '[:lower:]')
  if [[ $archive_lower == *"_${host_os}_${host_arch}.tar.gz" ]]; then
    version=$(basename "$archive" | sed -E 's/^tasks_([^_]+)_.*/v\1/')
    for command_name in tasks tasks-api tasks-tui; do
      output=$($root/$command_name --version)
      [[ $output == "$command_name $version ("* ]] || {
        echo "$archive has an unexpected $command_name version: $output" >&2
        exit 1
      }
    done
  fi
done <"$temporary/archives"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$dist" && sha256sum --check checksums.txt)
else
  (cd "$dist" && shasum -a 256 --check checksums.txt)
fi

echo "verified $archive_count archives against cmd/"
