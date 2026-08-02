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
the extension. The next tick can add named config-resolution cases to a case
list, capture their Ruby observations under this directory, validate them, and
then hand translation to a separate mid-tier session.
