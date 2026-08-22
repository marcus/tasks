# Plan: Upgrade to Go 1.27

## Goal

Move tasks from Go 1.26.0 to Go 1.27.0 with no behavioral regressions, leaning on the existing linux + macOS CI matrix (including the race detector) as the main verification surface.

## Current state

| Concern | Today |
|---|---|
| go.mod directive | `go 1.26.0`, no `toolchain` directive |
| CI | `ci.yml`: linux job runs `make fmt-check vet test build`; macos job runs `make test-race build`. All `setup-go` steps use `go-version-file: go.mod` — the directive is the single lever |
| Release | `release.yml`: goreleaser v2.16.0 builds from a tag after `check-release-state.sh` and changelog-driven release notes |
| Local | plain `go` toolchain; no lint runner in this repo |
| Plans convention | this directory (`docs/plans/active`) |

## What Go 1.27 changes that touch this repo

- **encoding/json/v2 is now the default implementation** of `encoding/json` (escape hatch: `GOEXPERIMENT=nojsonv2`). Error strings differ in places; state-file round-trips and any tests asserting exact JSON output or error text are the watch list.
- **stdversion vet** runs by default under `go test`; satisfied once the directive is current.
- Generic methods and embedded-field-selector struct literals are legal syntax (additive).
- `compress/flate` output bytes changed — only matters for byte-exact compressed-output assertions.
- darwin floor stays at macOS 13+ (unchanged from 1.26).

The macos race job is the most valuable check here: runtime and json/v2 changes surface data-race and ordering assumptions that the linux-only path can miss.

## Work sequence

1. **Directive**: `go.mod` → `go 1.27.0`.
2. **Tidy**: `go mod tidy`; inspect the diff for dependencies raising their own directives.
3. **Full local battery**: `make fmt-check vet test build` and `make test-race build`.
4. **JSON pass**: on any failure, bisect with `GOEXPERIMENT=nojsonv2` to confirm attribution before changing code.
5. **Release rehearsal**: `goreleaser release --snapshot --clean` locally; confirm the snapshot artifacts build and version metadata lands via ldflags as before.

## Coordination

sidecar pins released tasks versions (`tasks v1.12.0` today) for its release builds, and compiles this module from source through go.work for dev installs. This upgrade is safe to land independently — sidecar's workspace already declares ≥ this repo's current directive, and sidecar carries its own plan. Recommended trio order remains td → tasks → sidecar so sidecar's bump never trails a member requirement.

## Verification & acceptance evidence

- Both CI jobs green on the bumped branch (linux + macos/race).
- Snapshot release artifacts build locally.
- TUI smoke: launch tasks against a scratch store, create/close an item.

Out of scope: adding golangci-lint to this repo, dependency upgrades beyond tidy, json/v2-specific API adoption.
