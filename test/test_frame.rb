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

  # The selected row paints through the :selection slot. Rows carry
  # pre-rendered themed text (Views::Row is text/item/node), so Frame reverses
  # the stripped text under :selection and pads the rest of the line in the
  # same slot — a custom selection background wraps the whole cursor line.
  def test_selected_row_uses_selection_slot
    Tui::Theme.configure!(overrides: { selection: "on-blue" })
    row = Row.new("\e[35mProject\e[0m task", Object.new)
    line = build(rows: [row], selected: 0)[3]
    assert_includes line, "\e[44m▸ Project task"   # stripped text, painted :selection
    assert_includes A.strip(line), "▸ Project task"
  ensure
    Tui::Theme.reset!
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

  def test_popup_renders_on_top_of_modal
    # rescheduling from an open detail modal layers the date popup over it
    modal = { title: "task", lines: %w[alpha beta gamma delta] }
    popup = { lines: ["X" * 50], row: 3, col: 0 }
    row = A.strip(build(modal: modal, popup: popup)[3 + 3]) # body row 3 (borders+header+rule)
    assert_includes row, "X" * 50, "popup must overlay the modal, not sit under it"
    refute_includes row, "│ alpha", "modal content at that row is covered by the popup"
  end

  def test_modal_wider_than_frame_is_clamped
    modal = { title: "wide", lines: ["z" * 200] }
    lines = build(modal: modal)
    lines.each { |l| assert_equal 60, A.vislen(l) }
  end

  def test_modal_explicit_width_pins_the_box
    modal = { title: "t", lines: ["short"], width: 44 }
    lines = build(modal: modal).map { |l| A.strip(l) }
    top = lines.find { |l| l.include?("┌─ t ") }
    bottom = lines.find { |l| l.include?("└") }
    assert_equal 44, top.rindex("┐") - top.index("┌") + 1, "top border pinned: #{top.inspect}"
    assert_equal 44, bottom.rindex("┘") - bottom.index("└") + 1, "bottom border pinned"
    content = lines.find { |l| l.include?("short") }
    assert_equal top.index("┌"), content.index("│", 1), "content row aligns with the border"
  end

  def test_modal_explicit_width_is_clamped_to_frame
    modal = { title: "t", lines: ["short"], width: 500 }
    build(modal: modal).each { |l| assert_equal 60, A.vislen(l) }
  end

  def test_modal_title_is_painted_with_theme_slot
    Tui::Theme.configure!(overrides: { modal_title: "on-blue" })
    top = build(modal: { title: "task", lines: ["x"] }).find { |l| A.strip(l).include?("┌─ task") }
    assert_includes top, "\e[44m task \e[0m", "title strip must carry the modal_title style"
  ensure
    Tui::Theme.reset!
  end

  def test_empty_rows_render_blank_body
    lines = build(rows: [])
    assert_equal 15, lines.size
    lines.each { |l| assert_equal 60, A.vislen(l) }
  end
end
