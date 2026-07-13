# frozen_string_literal: true

require_relative "test_helper"
require "tui/screen_layout"
require "tui/frame"
require "tui/views"

class TestScreenLayout < Minitest::Test
  def test_owns_footer_body_viewport_and_selected_coordinates
    layout = Tui::ScreenLayout.new(
      width: 42, height: 12, footer: %w[a b c], selected: 9
    )
    assert_equal 3, layout.footer_size
    assert_equal 4, layout.body_height
    assert_equal 38, layout.body_width
    assert_equal 6, layout.viewport_offset
    assert_equal 3, layout.selected_screen_row
    assert_equal [6, 7, 8, 9], layout.visible_rows((0..12).to_a)
  end

  def test_short_terminal_preserves_actionable_footer_tail
    layout = Tui::ScreenLayout.new(
      width: 8, height: 7, footer: %w[old response flash prompt], selected: 0
    )
    assert_equal ["prompt"], layout.footer
    assert_equal 1, layout.body_height
    assert_equal 4, layout.body_width
  end

  def test_footer_is_a_deep_frozen_snapshot
    source_line = +"prompt"
    source = [source_line]
    layout = Tui::ScreenLayout.new(width: 20, height: 8, footer: source, selected: 5)

    source_line << " changed"
    source << "another"

    assert_equal ["prompt"], layout.footer
    assert_equal 1, layout.footer_size
    assert_equal 2, layout.body_height
    assert_raises(FrozenError) { layout.footer << "mutation" }
    assert_raises(FrozenError) { layout.footer.first << "mutation" }
  end

  def test_popup_placement_uses_viewport_selection_and_clamps
    popup = { lines: ["123456", "abcdef"], row: 99, col: 99 }
    below = Tui::ScreenLayout.new(width: 14, height: 11, footer: [], selected: 1)
                             .place_popup(popup, preferred_col: 8)
    assert_equal [2, 4], below.values_at(:row, :col)

    above = Tui::ScreenLayout.new(width: 14, height: 11, footer: [], selected: 5)
                             .place_popup(popup, preferred_col: 8)
    assert_equal [3, 4], above.values_at(:row, :col)
  end

  def test_modal_placement_is_frozen_to_sampled_frame
    modal = { title: "Details", lines: ["one", "two"], width: 20 }
    wide = Tui::ScreenLayout.new(width: 80, height: 24, footer: ["prompt"], selected: 0)
    narrow = Tui::ScreenLayout.new(width: 30, height: 10, footer: ["prompt"], selected: 0)

    assert_equal({ row: 7, col: 28 }, wide.place_modal(modal).slice(:row, :col))
    assert_equal({ row: 0, col: 3 }, narrow.place_modal(modal).slice(:row, :col))
    assert_equal [80, 24], [wide.width, wide.height], "later resizes cannot mutate an existing frame"
    assert wide.frozen?
  end

  def test_right_panel_uses_stable_percentage_and_preserves_list_space
    layout = Tui::ScreenLayout.new(
      width: 100, height: 24, footer: ["prompt"], selected: 0, panel: true
    )
    assert_equal 96, layout.body_width
    assert_equal 38, layout.panel_width
    assert_equal 36, layout.panel_content_width
    assert_equal 58, layout.list_width

    same_width = Tui::ScreenLayout.new(
      width: 100, height: 12, footer: [], selected: 0, panel: true
    )
    assert_equal layout.panel_width, same_width.panel_width,
                 "panel width depends on terminal width, not content or height"
  end

  def test_panel_content_widths_characterize_editor_breakpoint_boundaries
    expected = {
      87 => 31,
      89 => 32,
      126 => 47,
      129 => 48,
    }

    expected.each do |terminal_width, content_width|
      layout = Tui::ScreenLayout.new(
        width: terminal_width, height: 24, footer: [], selected: 0, panel: true
      )
      assert_equal content_width, layout.panel_content_width
      expected_breakpoint = content_width < 32 ? :below_minimum : (content_width < 48 ? :stacked : :inline)
      assert_equal expected_breakpoint, layout.content_breakpoint
      assert_operator layout.list_width, :>=, Tui::ScreenLayout::MIN_LIST_WIDTH
    end
  end


  def test_named_panel_modes_are_centrally_resolved
    expected = { compact: 32, standard: 36, wide: 54, focus: 86 }
    expected.each do |mode, content_width|
      layout = Tui::ScreenLayout.new(
        width: 100, height: 24, footer: [], panel: true, panel_mode: mode
      )
      assert_equal mode, layout.panel_mode
      assert_equal content_width, layout.panel_content_width
      assert_equal 96, layout.list_width + layout.panel_width
    end
  end

  def test_panel_offset_shifts_one_column_each_direction
    base = Tui::ScreenLayout.new(width: 100, height: 24, footer: [], panel: true).panel_width
    grow = Tui::ScreenLayout.new(
      width: 100, height: 24, footer: [], panel: true, panel_offset: 1
    )
    shrink = Tui::ScreenLayout.new(
      width: 100, height: 24, footer: [], panel: true, panel_offset: -1
    )
    assert_equal base + 1, grow.panel_width
    assert_equal base - 1, shrink.panel_width
    # The list absorbs the opposite move, so the body stays fully divided.
    assert_equal 96, grow.list_width + grow.panel_width
  end

  def test_panel_offset_clamps_to_list_and_body_invariants
    hi = Tui::ScreenLayout.new(
      width: 100, height: 24, footer: [], panel: true, panel_offset: 1000
    )
    lo = Tui::ScreenLayout.new(
      width: 100, height: 24, footer: [], panel: true, panel_offset: -1000
    )
    # Growth stops once the list is down to MIN_LIST_WIDTH.
    assert_equal 96 - Tui::ScreenLayout::MIN_LIST_WIDTH, hi.panel_width
    assert_operator hi.list_width, :>=, Tui::ScreenLayout::MIN_LIST_WIDTH
    # Shrink stops at the read-mode floor of 3 columns, never negative.
    assert_equal 3, lo.panel_width
  end

  def test_panel_offset_never_starves_the_editor_content_minimum
    layout = Tui::ScreenLayout.new(
      width: 100, height: 24, footer: [], panel: true,
      panel_offset: -1000, editing: true
    )
    assert_operator layout.panel_content_width, :>=, 32
    assert layout.editable_panel?
  end

  def test_panel_offset_rides_ratio_base_across_terminal_resize
    narrow = Tui::ScreenLayout.new(
      width: 100, height: 24, footer: [], panel: true, panel_offset: 4
    )
    wide = Tui::ScreenLayout.new(
      width: 120, height: 24, footer: [], panel: true, panel_offset: 4
    )
    narrow_base = Tui::ScreenLayout.new(width: 100, height: 24, footer: [], panel: true).panel_width
    wide_base = Tui::ScreenLayout.new(width: 120, height: 24, footer: [], panel: true).panel_width
    # The ratio-derived base adapts to each width; the offset is a constant tweak
    # layered on top, so it needs no re-clamping when the terminal resizes.
    refute_equal narrow_base, wide_base
    assert_equal narrow_base + 4, narrow.panel_width
    assert_equal wide_base + 4, wide.panel_width
  end

  def test_editing_promotes_without_overwriting_requested_read_preference
    layout = Tui::ScreenLayout.new(
      width: 87, height: 24, footer: [], panel: true,
      panel_mode: :standard, editing: true
    )
    assert_equal :standard, layout.requested_panel_mode
    assert_equal :wide, layout.panel_mode
    assert_operator layout.panel_content_width, :>=, 32
    assert layout.editable_panel?
  end

  def test_editing_admission_is_exact_at_minimum_terminal_width
    below = Tui::ScreenLayout.new(
      width: 45, height: 18, footer: [], panel: true,
      panel_mode: :compact, editing: true
    )
    exact = Tui::ScreenLayout.new(
      width: 46, height: 18, footer: [], panel: true,
      panel_mode: :compact, editing: true
    )
    assert_equal 31, below.panel_content_width
    refute below.editable_panel?
    assert_equal 32, exact.panel_content_width
    assert exact.editable_panel?
    assert_equal 46, Tui::ScreenLayout.minimum_edit_terminal_width
  end


  def test_editing_admission_is_exact_at_named_height_across_widths_and_modes
    Tui::ScreenLayout::PANEL_MODES.each do |mode|
      [45, 46, 80, 120].each do |width|
        [6, 7, 8, 9].each do |height|
          layout = Tui::ScreenLayout.new(
            width: width, height: height, footer: [], panel: true,
            panel_mode: mode, editing: true
          )
          expected = width >= 46 && height >= 8
          assert_equal expected, layout.editable_panel?,
                       "#{mode} at #{width}x#{height}"
          assert_equal [height - 7, 0].max, layout.edit_content_height
        end
      end
    end
    assert_equal [8, 46], Tui::ScreenLayout.minimum_edit_terminal_size
  end

  def test_each_footer_row_raises_edit_height_minimum
    below = Tui::ScreenLayout.new(
      width: 46, height: 8, footer: ["message"], panel: true,
      panel_mode: :focus, editing: true
    )
    exact = Tui::ScreenLayout.new(
      width: 46, height: 9, footer: ["message"], panel: true,
      panel_mode: :focus, editing: true
    )
    refute below.editable_panel?
    assert exact.editable_panel?
    assert_equal [9, 46], Tui::ScreenLayout.minimum_edit_terminal_size(footer_rows: 1)
  end

  def test_frame_consumes_layout_body_and_viewport_without_recomputing
    rows = (0..12).map { |i| Tui::Views::Row.new("row #{i}", Object.new) }
    layout = Tui::ScreenLayout.new(width: 42, height: 12, footer: %w[a b c], selected: 9)
    lines = Tui::Frame.build(
      width: 99, height: 99, header: "header", rows: rows,
      selected: 0, footer: Array.new(20, "wrong"), layout: layout
    )

    assert_equal 12, lines.size
    assert lines.all? { |line| Tui::Ansi.vislen(line) == 42 }
    assert_includes Tui::Ansi.strip(lines[3]), "row 6"
    assert_includes Tui::Ansi.strip(lines[6]), "row 9"
    assert_includes lines[6], "\e[7m"
    refute lines.any? { |line| Tui::Ansi.strip(line).include?("wrong") }
  end
end
