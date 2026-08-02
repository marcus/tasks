# `config-resolution` Ruby characterization

This is the Ruby-oracle characterization for manifest slice `config-resolution`,
at source revision `e75019a3930f02635925ae16af9401bd0c0f0746`. No Go code was
written in this step.

The focused oracle selection passed on the repository's Ruby implementation:

```text
21 runs, 53 assertions, 0 failures, 0 errors, 0 skips
```

It covers the default directory, `TASKS_DIR`, the independent `TASKS_FILE` /
`TASKS_ARCHIVE` overrides, config-file `dir` / `file` / `archive` keys and
their precedence, empty environment values, the memory-sidecar precedence
chain, tilde expansion, unknown/comment config lines, `Config.for_dir`, and
the JSON CLI's resolved paths, source labels, and memory-exists field.

## Reproduction

```sh
ruby test/test_config.rb -n '/test_defaults_to_default_dir|test_tasks_dir_env_points_both_files|test_per_file_env_beats_tasks_dir|test_config_file_dir_key|test_config_file_per_file_keys_beat_dir_key|test_env_beats_config_file|test_empty_env_values_are_ignored|test_memory_defaults_beside_resolved_tasks_file|test_memory_follows_tasks_dir|test_memory_follows_the_final_tasks_file_override_not_the_base_dir|test_memory_config_key_beats_sibling_default|test_memory_config_key_expands_tilde|test_tasks_memory_env_beats_config_key_and_sibling|test_tasks_memory_empty_env_is_ignored|test_config_file_ignores_comments_blanks_and_unknown_keys|test_config_file_expands_tilde|test_missing_config_file_is_fine|test_for_dir_pins_both_files_ignoring_env_and_config|test_cli_config_reports_paths_and_sources|test_cli_config_reports_memory_from_tasks_file_sibling_and_existence|test_cli_reads_tasks_from_config_file_dir/'
```

## Differential-conformance prerequisite

The current generic fixture runner deliberately owns `TASKS_DIR`,
`TASKS_FILE`, `TASKS_ARCHIVE`, `TASKS_MEMORY`, and `XDG_CONFIG_HOME`, so its
case-list `env` cannot exercise the precedence rows this slice owns. It also
creates an empty XDG config directory after copying each fixture's `store/`.

Before translation, extend the language-neutral runner contract with a safe,
copy-root-contained way to stage a fixture-owned config file and express
copy-root-relative path overrides. Capture the CLI `config --json` cases with
that mechanism, validate the observations, and leave the Ruby test selection
above as the direct oracle for `Config.for_dir`. This is a harness coverage
requirement, not an intentional difference and not permission to relax the
runner's live-store protections.

## Runner prerequisite completed

The runner now accepts a fixture-owned `config_file` and a separately validated
`path_overrides` object. It copies only a regular file from the selected fixture
to the copy's isolated XDG configuration path; it accepts only relative
`TASKS_DIR`, `TASKS_FILE`, `TASKS_ARCHIVE`, and `TASKS_MEMORY` overrides, with
`null` meaning unset. The ordinary `env` path-variable guard remains unchanged.

`ruby test/test_porting_runner.rb` proves both the config-file and per-file-env
precedence paths through `tasks config --json`; `ruby test/all.rb` passed after
the extension.

## Named CLI observations

`porting/runners/cases/config-resolution.jsonl` provides five
fixture-contained `config --json` cases: runner default resolution, explicit
`TASKS_DIR`, fixture-owned config-file paths, per-file environment precedence,
and the memory sidecar following the final `TASKS_FILE` path. The captured Ruby
observations are the differential baseline for the Go implementation.

The `config-resolution-default` case is deliberately the Ruby CLI's actual
default: with `TASKS_DIR` unset, `bin/tasks` resolves its store paths from the
repository root, not the fixture copy. The fixture still pins `HOME` and XDG
roots, so its config-file path remains isolated. Its case note records this
distinction; a Go adapter must use the same repository-root default when the
runner unsets `TASKS_DIR`.

Reproduce and validate them with:

```sh
porting/runners/ruby/run --out porting/evidence/config-resolution/ruby \
  porting/runners/cases/config-resolution.jsonl
porting/compare/validate porting/evidence/config-resolution/ruby
```

## Go precedence characterization

`go/internal/config/config_test.go` now has a table-driven characterization of
the path-resolution boundary: config-file path keys outrank `TASKS_DIR`,
per-file environment overrides outrank both, empty overrides are ignored, and
the fallback memory path follows the final `TASKS_FILE`. This distinction is
deliberate: `TASKS_DIR` chooses the fallback directory, while a config-file
`file` or `archive` key is a more-specific path selection.

Reproduced in this partial tick with:

```sh
(cd go && go test ./... && go vet ./...)
ruby test/test_config.rb -n '/test_defaults_to_default_dir|test_tasks_dir_env_points_both_files|test_per_file_env_beats_tasks_dir|test_config_file_dir_key|test_config_file_per_file_keys_beat_dir_key|test_env_beats_config_file|test_empty_env_values_are_ignored|test_memory_defaults_beside_resolved_tasks_file|test_memory_follows_tasks_dir|test_memory_follows_the_final_tasks_file_override_not_the_base_dir|test_memory_config_key_beats_sibling_default|test_memory_config_key_expands_tilde|test_tasks_memory_env_beats_config_key_and_sibling|test_tasks_memory_empty_env_is_ignored|test_config_file_ignores_comments_blanks_and_unknown_keys|test_config_file_expands_tilde|test_missing_config_file_is_fine|test_for_dir_pins_both_files_ignoring_env_and_config|test_cli_config_reports_paths_and_sources|test_cli_config_reports_memory_from_tasks_file_sibling_and_existence|test_cli_reads_tasks_from_config_file_dir/'
```

## Translation handoff (partial)

The Go resolver now lives at `go/internal/config`. It ports only this slice's
store-path concerns: the `TASKS_FILE` / `TASKS_ARCHIVE` / `TASKS_MEMORY` and
`TASKS_DIR` precedence chain, config-file `dir` / `file` / `archive` / `memory`
keys, tilde expansion, empty-value fallthrough, provenance labels, and pinned
`ForDir` paths. Its focused Go tests pass with `go test ./...` and `go vet ./...`.

It is not yet a completed medium-risk slice: no Go `config --json` adapter or
runner exists, so the five captured Ruby observations cannot yet be compared.
The Go resolver now also exposes `config.ConfigReport`, the narrow shared
projection a later CLI adapter or probe will use for its resolved `org`,
`archive`, `memory`, `sources`, `memory_exists`, `config_file`, and
`config_file_exists` fields. It does not invent the unrelated settings fields
of Ruby's full `config --json` report; those remain owned by their slices.

## Go CLI adapter (partial)

`go/internal/cli.WriteConfigJSON` now serializes that resolver-owned projection
through one thin adapter. Its test verifies the final `TASKS_FILE` path and
provenance flow unchanged into the JSON document. This is deliberately not yet
the public `tasks config --json` command: Ruby's envelope contains settings
owned by other slices, and emitting an incomplete envelope as though it were
compatible would be a false conformance claim.

Verified in this tick:

```sh
(cd go && go test ./... && go vet ./...)
ruby test/test_config.rb -n '/test_defaults_to_default_dir|test_tasks_dir_env_points_both_files|test_per_file_env_beats_tasks_dir|test_config_file_dir_key|test_config_file_per_file_keys_beat_dir_key|test_env_beats_config_file|test_empty_env_values_are_ignored|test_memory_defaults_beside_resolved_tasks_file|test_memory_follows_tasks_dir|test_memory_follows_the_final_tasks_file_override_not_the_base_dir|test_memory_config_key_beats_sibling_default|test_tasks_memory_env_beats_config_key_and_sibling|test_tasks_memory_empty_env_is_ignored|test_config_file_ignores_comments_blanks_and_unknown_keys|test_config_file_expands_tilde|test_missing_config_file_is_fine|test_for_dir_pins_both_files_ignoring_env_and_config|test_cli_config_reports_paths_and_sources|test_cli_config_reports_memory_from_tasks_file_sibling_and_existence|test_cli_reads_tasks_from_config_file_dir/'
```

The next tick should provide the protocol-conforming Go runner/probe and a
complete composed public config envelope before running the named Ruby-vs-Go
differential cases.

The next tick should add the protocol-conforming runner/probe around this
projection, compose the remaining public settings, run the named differential
cases, and then arrange separate source-fidelity and Go-idiom reviews.
