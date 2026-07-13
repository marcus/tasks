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
    assert_raises(Tui::UiState::InvalidTransition) { ui.mode = :task_edit }
  end

  def test_task_editor_is_one_explicit_mode_owner
    ui = state
    editor = Object.new
    ui.task_editor = editor
    ui.mode = :task_edit
    assert_same editor, ui.task_editor
    assert_equal :task_edit, ui.mode

    ui.task_editor = nil
    assert_equal :list, ui.mode
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
    assert_nil ui.panel
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
      { "view" => "projects", "collapsed" => ["live0001"],
        "panel_mode" => "standard", "panel_offset" => 0 },
      ui.session_hash(live_ids: ["live0001", "other002"])
    )
  end

  def test_panel_offset_persists_and_coerces
    ui = Tui::UiState.new(view: :projects)
    ui.panel_offset = 5
    assert_equal 5, ui.session_hash(live_ids: [])["panel_offset"]

    restored = Tui::UiState.restore(
      saved: { view: "agenda", panel_offset: -3 },
      views: %i[agenda], default_view: :agenda
    )
    assert_equal(-3, restored.panel_offset)

    # A malformed persisted value falls back to a neutral offset.
    fallback = Tui::UiState.restore(
      saved: { view: "agenda", panel_offset: "wide" },
      views: %i[agenda], default_view: :agenda
    )
    assert_equal 0, fallback.panel_offset
  end


  def test_panel_mode_restore_and_validation
    restored = Tui::UiState.restore(
      saved: { view: "agenda", panel_mode: "wide" },
      views: %i[agenda], default_view: :agenda
    )
    assert_equal :wide, restored.panel_mode
    assert_raises(ArgumentError) { restored.panel_mode = :elastic }

    fallback = Tui::UiState.restore(
      saved: { view: "agenda", panel_mode: "elastic" },
      views: %i[agenda], default_view: :agenda
    )
    assert_equal :standard, fallback.panel_mode
  end
end
