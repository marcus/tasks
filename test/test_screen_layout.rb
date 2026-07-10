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
