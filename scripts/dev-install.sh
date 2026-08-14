#!/bin/sh
set -eu

commands="tasks tasks-api tasks-tui"
action=${1:-status}
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
state_root=${TASKS_DEV_STATE:-"$HOME/.local/state/tasks/dev-installs"}

die() {
  printf 'tasks dev install: %s\n' "$*" >&2
  exit 1
}

brew_prefix() {
  if [ -n "${TASKS_BREW_PREFIX:-}" ]; then
    printf '%s\n' "$TASKS_BREW_PREFIX"
  else
    command -v brew >/dev/null 2>&1 || die "Homebrew is required to switch the machine-wide installation"
    brew --prefix
  fi
}

active_bin_dir() {
  printf '%s/bin\n' "$(brew_prefix)"
}

is_managed_link() {
  [ -L "$1" ] || return 1
  target=$(readlink "$1")
  case "$target" in
    "$state_root"/*) return 0 ;;
    *) return 1 ;;
  esac
}

is_homebrew_link() {
  [ -L "$1" ] || return 1
  case "$(readlink "$1")" in
    *Cellar/tasks/*) return 0 ;;
    *) return 1 ;;
  esac
}

preflight_switch() {
  bin_dir=$1
  for name in $commands; do
    path=$bin_dir/$name
    if [ -e "$path" ] || [ -L "$path" ]; then
      is_managed_link "$path" || is_homebrew_link "$path" ||
        die "$path is not managed by this repository or Homebrew; refusing to replace it"
    fi
  done
}

clear_managed_links() {
  bin_dir=$1
  for name in $commands; do
    path=$bin_dir/$name
    if [ -e "$path" ] || [ -L "$path" ]; then
      is_managed_link "$path" || die "$path is not managed by this repository; refusing to replace it"
      rm "$path"
    fi
  done
}

status() {
  bin_dir=$(active_bin_dir)
  printf 'active command directory: %s\n' "$bin_dir"
  for name in $commands; do
    path=$bin_dir/$name
    if is_managed_link "$path"; then
      printf '%-10s local    %s -> %s\n' "$name" "$path" "$(readlink "$path")"
      source_file=$(dirname "$(readlink "$path")")/source
      [ ! -r "$source_file" ] || printf '%-10s source   %s\n' "" "$(sed -n '1p' "$source_file")"
    elif is_homebrew_link "$path"; then
      printf '%-10s homebrew %s -> %s\n' "$name" "$path" "$(readlink "$path")"
    elif [ -e "$path" ] || [ -L "$path" ]; then
      printf '%-10s other    %s -> %s\n' "$name" "$path" "$(readlink "$path" 2>/dev/null || printf 'regular file')"
    else
      printf '%-10s missing  %s\n' "$name" "$path"
    fi
    if [ -x "$path" ]; then
      "$path" --version
    fi
  done
  resolved=$(command -v tasks 2>/dev/null || true)
  printf 'this shell resolves: %s\n' "${resolved:-not found}"
  if command -v zsh >/dev/null 2>&1; then
    interactive=$(zsh -lic 'whence -v tasks; tasks --version; whence -v tasks-api; tasks-api --version; whence -v tasks-tui; tasks-tui --version' 2>/dev/null || true)
    printf 'interactive login resolves:\n%s\n' "${interactive:-not found}"
  fi
}

install_local() {
  mode=$1
  branch=$(git -C "$repo_root" branch --show-current)
  [ -n "$branch" ] || branch=detached
  common_dir=$(git -C "$repo_root" rev-parse --path-format=absolute --git-common-dir)
  canonical=false
  [ "$common_dir" = "$repo_root/.git" ] && canonical=true

  if [ "$mode" = main ] && { [ "$canonical" != true ] || [ "$branch" != main ]; }; then
    die "install-local is only for the canonical main checkout; use 'make install-worktree' to activate this checkout deliberately"
  fi

  commit=$(git -C "$repo_root" rev-parse --short HEAD)
  dirty=
  [ -z "$(git -C "$repo_root" status --porcelain --untracked-files=normal)" ] || dirty=-dirty
  safe_branch=$(printf '%s' "$branch" | tr -cs 'A-Za-z0-9._-' '-')
  checkout_id=$(printf '%s' "$repo_root" | shasum -a 256 | cut -c1-12)
  build_id=$(date -u '+%Y%m%dT%H%M%SZ')-$$
  destination=$state_root/$safe_branch-$checkout_id-$commit$dirty-$build_id
  temporary=$state_root/.build-$checkout_id-$$
  version=dev-$safe_branch

  mkdir -p "$state_root" "$temporary"
  trap 'rm -rf "$temporary"' EXIT HUP INT TERM
  for name in $commands; do
    go build -ldflags "-s -w -X github.com/marcus/tasks/internal/buildinfo.Version=$version -X github.com/marcus/tasks/internal/buildinfo.Commit=$commit$dirty" -o "$temporary/$name" "./cmd/$name"
  done
  printf '%s\n' "$repo_root" > "$temporary/source"
  mv "$temporary" "$destination"
  trap - EXIT HUP INT TERM

  bin_dir=$(active_bin_dir)
  mkdir -p "$bin_dir"
  preflight_switch "$bin_dir"
  staged=
  for name in $commands; do
    staged_link=$bin_dir/.tasks-dev-$$-$name
    ln -s "$destination/$name" "$staged_link"
    staged="$staged $staged_link"
  done
  trap 'rm -f $staged' EXIT HUP INT TERM
  if command -v brew >/dev/null 2>&1 && brew list --versions tasks >/dev/null 2>&1; then
    brew unlink tasks >/dev/null
  fi
  clear_managed_links "$bin_dir"
  for name in $commands; do
    mv "$bin_dir/.tasks-dev-$$-$name" "$bin_dir/$name"
  done
  trap - EXIT HUP INT TERM
  printf 'activated local Tasks build from %s\n' "$repo_root"
  status
}

use_homebrew() {
  command -v brew >/dev/null 2>&1 || die "Homebrew is not installed"
  brew list --versions tasks >/dev/null 2>&1 || die "the tasks formula is not installed; run 'brew install marcus/tap/tasks'"
  bin_dir=$(active_bin_dir)
  preflight_switch "$bin_dir"
  previous=
  homebrew_count=0
  for name in $commands; do
    path=$bin_dir/$name
    if is_managed_link "$path"; then
      previous="$previous $name=$(readlink "$path")"
    elif is_homebrew_link "$path"; then
      homebrew_count=$((homebrew_count + 1))
    fi
  done
  if [ "$homebrew_count" -eq 3 ]; then
    printf 'Homebrew Tasks is already active\n'
    status
    return
  fi
  for name in $commands; do
    path=$bin_dir/$name
    if is_managed_link "$path"; then
      rm "$path"
    fi
  done
  if ! brew link --overwrite tasks >/dev/null; then
    brew unlink tasks >/dev/null 2>&1 || true
    for name in $commands; do
      path=$bin_dir/$name
      if is_homebrew_link "$path"; then
        rm "$path"
      fi
    done
    restore_failed=false
    for pair in $previous; do
      name=${pair%%=*}
      target=${pair#*=}
      ln -s "$target" "$bin_dir/$name" || restore_failed=true
    done
    [ "$restore_failed" = false ] || die "Homebrew relinking failed and the previous local installation could not be fully restored"
    die "Homebrew relinking failed; restored the previous local installation"
  fi
  printf 'activated Homebrew Tasks\n'
  status
}

verify_homebrew() {
  use_homebrew
  brew update
  brew upgrade --yes marcus/tap/tasks
  brew test marcus/tap/tasks
  status
}

case "$action" in
  install-local) install_local main ;;
  install-worktree) install_local worktree ;;
  use-homebrew) use_homebrew ;;
  verify-homebrew) verify_homebrew ;;
  status) status ;;
  *) die "usage: $0 {install-local|install-worktree|use-homebrew|verify-homebrew|status}" ;;
esac
