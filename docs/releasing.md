# Releasing tasks

`tasks` releases are annotated Git tags on `main`. GitHub Actions builds one
archive for each supported target (macOS and Linux on amd64 and arm64), creates
the GitHub release, and the local release publisher updates
`marcus/homebrew-tap` only after verifying the exact successful workflow run.

## Prepare

1. Add a dated entry to `CHANGELOG.md` for the unprefixed version.
2. Make sure `main` is clean, reviewed, tested, pushed, and identical to
   `origin/main`.
3. Confirm GitHub CLI authentication can push both `marcus/tasks` and
   `marcus/homebrew-tap`.
4. Install `brew`, `curl`, `gh`, `git`, `goreleaser`, `jq`, and Ruby.

Run the local release artifact and guard tests before publishing:

```sh
make release-snapshot
scripts/verify-release-archives.sh dist
scripts/test-release-guards.sh dist
scripts/test-release-publication.sh
```

## Publish

The normal flow is: write the changelog, run one command.

```sh
BUMP=minor make release      # or major / patch
```

With bullets sitting under `## [Unreleased]`, the command derives the next
version from the latest tag, stamps the heading to `## [X.Y.Z] - <today>`,
commits `release: prepare vX.Y.Z`, pushes `main`, and publishes. The version is
stated exactly once. Two equivalent spellings:

```sh
RELEASE_VERSION=v1.0.0 make release   # explicit version; stamps [Unreleased] if present
make release                          # heading already stamped `## [X.Y.Z] - date` by hand
```

`make release-dry-run` (with the same variables) prints the full plan —
derived version, stamp, commit, push — and exits before any mutation.

The prep step refuses an empty `[Unreleased]` section, a working tree dirty
beyond `CHANGELOG.md`, a version whose tag already exists, and a
`RELEASE_VERSION` that contradicts an already-stamped heading. Publication then
fails closed unless the version is strict SemVer, the working tree is clean,
`HEAD` is the live `origin/main`, the changelog entry exists, the tag does not
exist, and the operator can complete the Homebrew publication. It then:

1. creates and pushes the annotated tag;
2. waits for the exact tag workflow and GitHub release;
3. downloads and verifies GitHub's source archive;
4. renders, styles, and audits `Formula/tasks.rb`;
5. pushes the tap without force, retrying races by rebase;
6. verifies the formula committed to the remote tap.

If the tag exists but tap publication was interrupted, resume only the tap step:

```sh
RELEASE_VERSION=v1.0.0 make release-tap
```

## Verify the public install

```sh
RELEASE_VERSION=vX.Y.Z
export RELEASE_VERSION
proof_bin=$(mktemp -d)
GOBIN="$proof_bin" GOWORK=off go install "github.com/marcus/tasks/cmd/tasks@$RELEASE_VERSION"
GOBIN="$proof_bin" GOWORK=off go install "github.com/marcus/tasks/cmd/tasks-api@$RELEASE_VERSION"
GOBIN="$proof_bin" GOWORK=off go install "github.com/marcus/tasks/cmd/tasks-tui@$RELEASE_VERSION"
test "$("$proof_bin/tasks" --version)" = "tasks $RELEASE_VERSION"
test "$("$proof_bin/tasks-api" --version)" = "tasks-api $RELEASE_VERSION"
test "$("$proof_bin/tasks-tui" --version)" = "tasks-tui $RELEASE_VERSION"

make verify-homebrew

# Restore this development machine to the canonical main build afterward.
make install-local
make install-status
```

Release binaries do not guess a task-data directory. Configure each machine
with `TASKS_DIR` or `~/.config/tasks/config`, then run `tasks check` against the
machine's real data before removing any previous development install.

## Ruby archive

The final Ruby implementation is preserved by the annotated tag
`ruby-final-2026-08-04`. It is historical source, not a supported release line.
