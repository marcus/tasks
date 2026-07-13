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

  def test_short_frame_clips_footer_to_preserve_exact_height
    lines = build(width: 20, height: 6, footer: ["old response", :rule, "prompt"])
    assert_equal 6, lines.size
    lines.each { |line| assert_equal 20, A.vislen(line) }
    refute lines.any? { |line| A.strip(line).include?("old response") }
  end

  def test_selected_row_is_highlighted
    lines = build(selected: 2)
    assert_includes lines[5], "\e[7m"   # borders(1) + header(1) + rule(1) + 2 rows
    assert_includes A.strip(lines[5]), "❯ task number 3"
  end

  # The selected row composites the :selection SGR UNDER the row's own field
  # colors instead of stripping them: the line opens with the selection prefix,
  # the row's inner fg SGR survives, and the selection prefix is re-injected
  # after the field's reset so the background carries on across the row.
  def test_selected_row_composites_selection_under_field_colors
    Tui::Theme.configure!(overrides: { selection: "on-blue" })
    row = Row.new("\e[35mProject\e[0m task", Object.new)
    line = build(rows: [row], selected: 0)[3]
    assert_includes line, "\e[44m❯ ", "line opens with the selection SGR + cursor"
    assert_includes line, "\e[35mProject", "the field's own fg SGR is preserved, not stripped"
    assert_includes line, "\e[0m\e[44m", "selection SGR re-injected after the field reset"
    assert_includes A.strip(line), "❯ Project task"
    inner = line[/\A│ (.*) │\z/, 1]
    assert inner.end_with?("\e[0m"), "row closes with a reset so styling can't leak"
  ensure
    Tui::Theme.reset!
  end

  # The selection background spans the full inner width: the visible text is
  # padded and the padding sits under the selection SGR (before the final reset).
  def test_selected_row_pads_to_full_width_under_selection
    Tui::Theme.configure!(overrides: { selection: "on-blue" })
    line = build(rows: [Row.new("short", Object.new)], selected: 0)[3]
    inner = line[/\A│ (.*) │\z/, 1]
    refute_nil inner, "body cell parses out of the frame line"
    assert_equal 56, A.vislen(inner), "selected row fills the inner width (60 - borders/margins)"
    # the padding tail is styled (last visible run is under the selection SGR),
    # closed by exactly one trailing reset.
    assert inner.end_with?("\e[0m")
    assert_includes inner, "\e[44m", "selection background present across the row"
  ensure
    Tui::Theme.reset!
  end

  # Mono / NO_COLOR: :selection is reverse video (attribute-only, no bg color).
  # The reverse must still cover the padded width so the whole row inverts.
  def test_selected_row_reverse_video_covers_padding
    Tui::Theme.configure!(name: "mono")
    line = build(rows: [Row.new("plain row", Object.new)], selected: 0)[3]
    inner = line[/\A│ (.*) │\z/, 1]
    assert inner.start_with?("\e[7m"), "reverse video opens the row"
    assert_equal 56, A.vislen(inner)
    assert inner.end_with?("\e[0m")
    # no stray reset mid-row would drop the reverse before the pad
    assert_includes A.strip(inner), "❯ plain row"
  ensure
    Tui::Theme.reset!
  end

  # A selected row narrower than its content truncates cleanly: the pad+reset
  # tail is never clipped (truncation runs before compositing), the line ends in
  # a reset, and the visible width is exactly the inner width.
  def test_selected_row_truncates_and_stays_well_formed
    Tui::Theme.configure!(overrides: { selection: "on-blue" })
    row = Row.new("\e[35m#{"x" * 200}\e[0m", Object.new)
    line = build(rows: [row], selected: 0)[3]
    inner = line[/\A│ (.*) │\z/, 1]
    assert_equal 56, A.vislen(inner), "truncated selected row fills exactly the inner width"
    assert inner.end_with?("\e[0m"), "truncated row still closes with a reset"
    assert_includes A.strip(inner), "…", "ellipsis marks the truncation"
    assert_includes inner, "\e[44m", "selection SGR survives truncation"
  ensure
    Tui::Theme.reset!
  end

  # A plain-text row (no field ANSI at all) still gets the full-width selection.
  def test_selected_plain_row_gets_full_width_selection
    Tui::Theme.configure!(overrides: { selection: "on-blue" })
    line = build(rows: [Row.new("no ansi here", Object.new)], selected: 0)[3]
    inner = line[/\A│ (.*) │\z/, 1]
    assert inner.start_with?("\e[44m❯ "), "selection opens even with no field SGRs"
    assert_equal 56, A.vislen(inner)
    assert inner.end_with?("\e[0m")
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

  def test_right_panel_splits_body_at_fixed_layout_width
    layout = Tui::ScreenLayout.new(
      width: 60, height: 15, footer: ["prompt"], selected: 0, panel: true
    )
    panel = { title: "task", lines: ["details", "more"] }
    lines = build(rows: [Row.new("selected task", Object.new)], selected: 0,
                  footer: layout.footer, panel: panel, layout: layout)
    body = A.strip(lines[3])
    assert_includes body, "selected task"
    assert_includes body, "task"
    assert_includes A.strip(lines[6]), "more"
    assert lines.all? { |line| A.vislen(line) == 60 }
  end

  def test_panel_width_does_not_change_with_content
    short = build(panel: { title: "task", lines: ["x"] })
    long = build(panel: { title: "task", lines: ["x" * 200] })
    short_divider = A.strip(short[3]).index("│", 1)
    long_divider = A.strip(long[3]).index("│", 1)
    assert_equal short_divider, long_divider
  end

  def test_panel_remains_renderable_at_minimum_terminal_size
    lines = build(
      width: 8, height: 6, rows: [Row.new("task", Object.new)], footer: [], selected: 0,
      panel: { title: "task", lines: ["details"] }
    )
    assert_equal 6, lines.size
    assert lines.all? { |line| A.vislen(line) == 8 }
  end

  def test_popup_preserves_base_content_on_both_sides
    rows = [Row.new("left-side middle-part right-side-content", Object.new)]
    popup = { lines: ["[P]"], row: 0, col: 12 }
    body_row = A.strip(build(rows: rows, popup: popup)[3])
    assert_includes body_row, "left-side"
    assert_includes body_row, "[P]"
    assert_includes body_row, "right-side-content"
  end

  def test_popup_splices_at_terminal_cell_boundaries
    rows = [Row.new("a界bcdef", Object.new)]
    # Body coordinates include the two-cell row marker. Column 3 lands on the
    # first cell of 界; replacing only that cell must blank the other half while
    # leaving b at its original terminal column.
    line = A.strip(build(width: 20, height: 8, rows: rows, footer: [],
                         popup: { lines: ["X"], row: 0, col: 3 })[3])
    assert_includes line, "aX bcdef"
    assert_equal 20, A.vislen(line)
  end

  def test_popup_preserves_styles_around_wide_base_content
    rows = [Row.new(A.red("a界bcdef"), Object.new)]
    popup = { lines: [A.bold("X")], row: 0, col: 3 }
    line = build(width: 20, height: 8, rows: rows, footer: [], popup: popup)[3]
    assert_equal 20, A.vislen(line)
    assert_includes line, "\e[1mX\e[0m", "popup styling survives the splice"
    assert_includes line, "\e[31m bcdef", "base styling is restored on the suffix"
    assert_includes A.strip(line), "aX bcdef"
  end

  def test_wide_popup_is_clipped_without_overflow_at_right_edge
    rows = [Row.new("underlay", Object.new)]
    popup = { lines: ["界"], row: 0, col: 15 }
    line = build(width: 20, height: 8, rows: rows, footer: [], popup: popup)[3]
    assert_equal 20, A.vislen(line)
    refute_includes A.strip(line), "界", "a two-cell cluster cannot be half-rendered"
  end

  def test_popup_clips_cleanly_at_negative_left_edge
    rows = [Row.new("underlay", Object.new)]
    popup = { lines: ["[ABC]"], row: 0, col: -2 }
    line = build(width: 20, height: 8, rows: rows, footer: [], popup: popup)[3]
    assert_equal 20, A.vislen(line)
    assert_includes A.strip(line), "BC]nderlay"
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

  def test_modal_uses_compact_unboxed_view_when_body_is_one_row
    lines = build(width: 8, height: 6, rows: [], footer: [],
                  modal: { title: "task", lines: ["details"] })
    assert_equal 6, lines.size
    lines.each { |line| assert_equal 8, A.vislen(line) }
    body = A.strip(lines[3])
    assert_includes body, "tas", "compact modal remains identifiable"
    refute_match(/[┌┐└┘]/, body, "never show a clipped box fragment")
  end

  def test_popup_renders_on_top_of_modal
    # A popup always layers over a blocking modal.
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

  def test_mixed_unicode_frames_respect_narrow_width_boundaries
    rows = [Row.new(A.cyan("界👩‍💻 e\u0301 " + "x" * 40), Object.new)]
    modal = { title: "界 task", lines: [A.yellow("👩‍💻 details e\u0301")] }

    (8..24).each do |width|
      lines = build(width: width, height: 10, header: A.bold("界👩‍💻 header"),
                    rows: rows, footer: ["界 footer"], modal: modal,
                    popup: { lines: [A.red("界 edge")], row: 1, col: width - 8 })
      assert_equal 10, lines.size
      lines.each_with_index do |line, index|
        assert_equal width, A.vislen(line),
                     "width #{width}, line #{index}: #{line.inspect}"
      end
    end
  end
end
