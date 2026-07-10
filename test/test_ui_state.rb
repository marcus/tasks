# frozen_string_literal: true

require_relative "test_helper"
require "tui/ui_state"
require "tui/modal"

class TestUiState < Minitest::Test
  FakeOverlay = Struct.new(:return_mode)

  def state = Tui::UiState.new(view: :agenda)

  def test_requires_backing_objects_for_overlay_modes
    ui = state
    assert_raises(Tui::UiState::InvalidTransition) { ui.mode = :form }
    assert_raises(Tui::UiState::InvalidTransition) { ui.mode = :palette }
    assert_raises(Tui::UiState::InvalidTransition) { ui.mode = :modal }
  end

  def test_rejects_modal_dependent_overlays_without_retained_modal
    ui = state
    assert_raises(Tui::UiState::InvalidTransition) { ui.form = FakeOverlay.new(:modal) }
    assert_raises(Tui::UiState::InvalidTransition) { ui.action_palette = FakeOverlay.new(:modal) }
    assert_nil ui.form
    assert_nil ui.action_palette
    assert_equal :list, ui.mode
  end

  def test_rejects_illegal_mode_edges
    ui = state
    ui.mode = :prompt
    assert_raises(Tui::UiState::InvalidTransition) { ui.mode = :filter }
    assert_equal :prompt, ui.mode
  end

  def test_modal_filter_requires_filterable_modal
    ui = state
    ui.modal = Tui::Modal.new(title: "detail", lines: [], kind: :detail)
    ui.mode = :modal
    error = assert_raises(Tui::UiState::InvalidTransition) { ui.mode = :modal_filter }
    assert_match(/filterable/, error.message)
  end

  def test_non_filterable_modal_cannot_replace_active_filtered_modal
    ui = state
    original = Tui::Modal.new(title: "help", lines: [], kind: :help, filterable: true)
    ui.modal = original
    ui.mode = :modal
    ui.mode = :modal_filter

    replacement = Tui::Modal.new(title: "detail", lines: [], kind: :detail)
    assert_raises(Tui::UiState::InvalidTransition) { ui.modal = replacement }
    assert_same original, ui.modal
    assert_equal :modal_filter, ui.mode
  end

  def test_removing_active_overlay_recovers_to_list
    ui = state
    ui.form = FakeOverlay.new(:list)
    ui.mode = :form
    ui.form = nil
    assert_equal :list, ui.mode

    ui.action_palette = FakeOverlay.new(:list)
    ui.mode = :palette
    ui.action_palette = nil
    assert_equal :list, ui.mode

    ui.modal = Tui::Modal.new(title: "help", lines: [], kind: :help, filterable: true)
    ui.mode = :modal
    ui.modal = nil
    assert_equal :list, ui.mode
  end

  def test_removing_retained_modal_invalidates_dependent_overlay
    ui = state
    ui.modal = Tui::Modal.new(title: "detail", lines: [], kind: :detail)
    ui.mode = :modal
    ui.form = FakeOverlay.new(:modal)
    ui.form_success = -> { flunk "stale form callback must not survive" }
    ui.mode = :form

    ui.modal = nil

    assert_equal :list, ui.mode
    assert_nil ui.form
    assert_nil ui.form_success
    assert_nil ui.detail_item_id
  end

  def test_restore_validates_view_and_collapsed_shape
    restored = Tui::UiState.restore(
      saved: { view: "next", collapsed: %w[aaaa0001 bbbb0002] },
      views: %i[agenda next], default_view: :agenda
    )
    assert_equal :next, restored.view
    assert_equal Set["aaaa0001", "bbbb0002"], restored.collapsed

    fallback = Tui::UiState.restore(
      saved: { view: 123, collapsed: "not-an-array" },
      views: %i[agenda next], default_view: :agenda
    )
    assert_equal :agenda, fallback.view
    assert_empty fallback.collapsed
  end

  def test_session_hash_prunes_stale_collapsed_ids
    ui = Tui::UiState.new(view: :projects, collapsed: Set["live0001", "deadbeef"])
    assert_equal(
      { "view" => "projects", "collapsed" => ["live0001"] },
      ui.session_hash(live_ids: ["live0001", "other002"])
    )
  end
end
