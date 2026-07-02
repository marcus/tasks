# frozen_string_literal: true

require_relative "test_helper"

class TestFrame < Minitest::Test
  F = Tui::Frame
  A = Tui::Ansi
  Row = Tui::Views::Row

  def sample_rows(n = 5)
    (1..n).map { |i| Row.new("task number #{i}", Object.new) }
  end

  def build(**opts)
    defaults = { width: 60, height: 15, header: "header", rows: sample_rows,
                 footer: ["keybar", "prompt"] }
    F.build(**defaults.merge(opts))
  end

  def test_every_line_has_exact_visible_width
    build.each_with_index do |line, i|
      assert_equal 60, A.vislen(line), "line #{i} wrong width: #{line.inspect}"
    end
  end

  def test_frame_height_matches_terminal
    assert_equal 15, build.size
  end

  def test_selected_row_is_highlighted
    lines = build(selected: 2)
    assert_includes lines[5], "\e[7m"   # borders(1) + header(1) + rule(1) + 2 rows
    assert_includes A.strip(lines[5]), "▸ task number 3"
  end

  def test_footer_rule_sentinel_draws_divider
    lines = build(footer: ["response line", :rule, "keybar", "prompt"])
    rule = lines[-4]
    assert rule.start_with?("├")
    assert rule.end_with?("┤")
  end

  def test_long_rows_truncate_not_overflow
    long = [Row.new("x" * 200, Object.new)]
    lines = build(rows: long)
    lines.each { |l| assert_equal 60, A.vislen(l) }
    assert_includes A.strip(lines[3]), "…"
  end

  def test_scrolls_to_keep_selection_visible
    rows = sample_rows(50)
    lines = build(rows: rows, selected: 49)
    assert lines.any? { |l| A.strip(l).include?("task number 50") }
    refute lines.any? { |l| A.strip(l).include?("task number 1 ") }
  end

  def test_popup_overlays_body
    popup = { lines: ["[POPUP]"], row: 1, col: 4 }
    lines = build(popup: popup)
    row = A.strip(lines[4]) # body row 1
    assert_includes row, "[POPUP]"
    assert_equal 60, A.vislen(lines[4])
  end

  def test_popup_preserves_base_content_on_both_sides
    rows = [Row.new("left-side middle-part right-side-content", Object.new)]
    popup = { lines: ["[P]"], row: 0, col: 12 }
    body_row = A.strip(build(rows: rows, popup: popup)[3])
    assert_includes body_row, "left-side"
    assert_includes body_row, "[P]"
    assert_includes body_row, "right-side-content"
  end

  def test_modal_draws_centered_box_with_title
    modal = { title: "task", lines: ["alpha", "beta gamma"] }
    lines = build(modal: modal)
    joined = lines.map { |l| A.strip(l) }.join("\n")
    assert_includes joined, "┌─ task "
    assert_includes joined, "alpha"
    assert_includes joined, "beta gamma"
    assert_includes joined, "└"
    lines.each { |l| assert_equal 60, A.vislen(l) }
    # centered: box border starts past the left margin
    box_line = lines.find { |l| A.strip(l).include?("┌─ task") }
    assert A.strip(box_line).index("┌") > 5
  end

  def test_modal_wider_than_frame_is_clamped
    modal = { title: "wide", lines: ["z" * 200] }
    lines = build(modal: modal)
    lines.each { |l| assert_equal 60, A.vislen(l) }
  end

  def test_empty_rows_render_blank_body
    lines = build(rows: [])
    assert_equal 15, lines.size
    lines.each { |l| assert_equal 60, A.vislen(l) }
  end
end
