#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."

ruby test/test_temporal.rb \
  --name '/test_(parser_preserves_all_day|floating_value_follows|fixed_value_keeps|timed_deadline_becomes)/'
ruby test/test_temporal_queries.rb \
  --name '/test_(timed_availability_releases|recurrence_skips)/'
bundle exec ruby test/api/test_black_box.rb \
  --name test_temporal_cli_and_api_writes_are_mutually_visible_and_cli_undoable
ruby test/test_schema_v2.rb \
  --name test_v1_migration_changes_only_meta_and_establishes_backups
