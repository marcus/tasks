# frozen_string_literal: true

require_relative "test_helper"
require "tasks/patch_result"

class TestMutationResult < Minitest::Test
  def test_the_shared_vocabulary_is_complete_and_immutable
    assert_equal %i[
      ok no_change not_found stale invalid conflict cycle too_deep migration_required store_invalid unavailable
    ], Tasks::MutationResult::STATUSES

    result = Tasks::MutationResult.new(
      status: :ok,
      errors: ["unchanged"],
      field_errors: { title: ["unchanged"] },
      touched_ids: ["1234abcd"],
      summary: { nested: ["value"] },
      store_revision: "s1.abc"
    )

    assert result.frozen?
    assert result.errors.frozen?
    assert result.field_errors.frozen?
    assert result.touched_ids.frozen?
    assert result.summary.frozen?
    assert_equal "s1.abc", result.store_revision
    assert result.store_revision.frozen?
    refute result.rolled_back?

    rolled_back = Tasks::MutationResult.new(status: :store_invalid, rolled_back: true)
    assert rolled_back.rolled_back?
    assert rolled_back.frozen?
  end

  def test_patch_result_legacy_name_normalizes_missing_to_not_found
    result = Tasks::PatchResult.new(status: :missing)

    assert_same Tasks::MutationResult, Tasks::PatchResult
    assert_equal :not_found, result.status
    assert result.not_found?
    assert result.missing?
  end

  def test_cli_and_tui_adapters_preserve_their_distinct_protocols
    assert_equal 0, Tasks::MutationResult.new(status: :ok).cli_exit_code
    assert_equal 0, Tasks::MutationResult.new(status: :no_change).cli_exit_code
    assert_equal 2, Tasks::MutationResult.new(status: :not_found).cli_exit_code
    assert_equal 1, Tasks::MutationResult.new(status: :migration_required).cli_exit_code
    assert_equal 1, Tasks::MutationResult.new(status: :unavailable).cli_exit_code

    missing = Tasks::MutationResult.new(status: :not_found)
    conflict = Tasks::MutationResult.new(status: :stale)
    invalid = Tasks::MutationResult.new(status: :store_invalid)

    assert_equal :missing, missing.tui_status
    assert_equal "Task no longer exists", missing.tui_message
    assert_equal :conflict, conflict.tui_status
    assert_equal "Field changed externally", conflict.tui_message
    assert_equal :invalid, invalid.tui_status
    assert_equal "task list failed validation", invalid.tui_message
  end

  def test_unknown_status_is_rejected
    error = assert_raises(ArgumentError) { Tasks::MutationResult.new(status: :wat) }

    assert_match(/unknown mutation status/, error.message)
  end
end
