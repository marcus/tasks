# frozen_string_literal: true

require_relative "test_helper"
require "tui/border"

# Tui::Border — gradient projection, corner charset, coalescing, degradation.
class TestBorder < Minitest::Test
  B = Tui::Border
  A = Tui::Ansi

  BLUE = [0x5a, 0xa2, 0xf7].freeze
  CYAN = [0x56, 0xd3, 0xc9].freeze
  GRAD = { stops: [BLUE, CYAN], angle: 0.0 }.freeze # angle 0 → sweep along x

  def setup
    B.truecolor = true
  end

  def teardown
    B.truecolor = false # restore the suite-wide default from test_helper
  end

  # -- spec parsing ------------------------------------------------------------

  def test_parses_two_stops_and_angle
    g = B.parse_gradient("#5aa2f7 #56d3c9 @60")
    assert_equal [BLUE, CYAN], g[:stops]
    assert_in_delta 60.0, g[:angle], 0.001
  end

  def test_parses_three_stops_and_defaults_angle_to_zero
    g = B.parse_gradient("#000000 #808080 #ffffff")
    assert_equal 3, g[:stops].length
    assert_equal 0.0, g[:angle]
  end

  def test_rejects_malformed_specs
    assert_nil B.parse_gradient("")
    assert_nil B.parse_gradient("#5aa2f7")            # one stop is not a gradient
    assert_nil B.parse_gradient("#5aa2f7 chartreuse") # bad token poisons the whole spec
    assert_nil B.parse_gradient("#5aa2f7 #56d3c9 @x") # non-numeric angle
    assert_nil B.parse_gradient("#12345 #56d3c9")     # short hex
  end

  # -- gradient projection -----------------------------------------------------

  def test_endpoints_hit_the_terminal_stops
    p = B.painter(width: 10, height: 4, gradient: GRAD)
    # angle 0 sweeps along x: the left corner is the first stop, the right the last.
    assert_includes p.cell(0, 0, "╭"), "\e[38;2;#{BLUE.join(";")}m"
    assert_includes p.cell(9, 0, "╮"), "\e[38;2;#{CYAN.join(";")}m"
  end

  def test_gradient_varies_across_the_span
    p = B.painter(width: 20, height: 4, gradient: GRAD)
    left = fg(p.cell(0, 0, "─"))
    mid = fg(p.cell(10, 0, "─"))
    right = fg(p.cell(19, 0, "─"))
    refute_equal left, mid
    refute_equal mid, right
    # Green channel climbs monotonically from blue (0xa2) toward cyan (0xd3).
    assert_operator left[1], :<, mid[1]
    assert_operator mid[1], :<, right[1]
  end

  def test_angle_changes_the_sweep_axis
    horizontal = B.painter(width: 10, height: 10, gradient: { stops: [BLUE, CYAN], angle: 0.0 })
    vertical = B.painter(width: 10, height: 10, gradient: { stops: [BLUE, CYAN], angle: 90.0 })
    # Horizontal: color tracks x, constant down a column. Vertical: the reverse.
    assert_equal fg(horizontal.cell(0, 0, "│")), fg(horizontal.cell(0, 9, "│"))
    refute_equal fg(vertical.cell(0, 0, "│")), fg(vertical.cell(0, 9, "│"))
  end

  # -- coalescing --------------------------------------------------------------

  def test_run_coalesces_adjacent_equal_colors
    p = B.painter(width: 40, height: 4, gradient: GRAD, steps: 4)
    row = p.run(0, 0, ["╭", *Array.new(38, "─"), "╮"])
    assert_equal 40, A.vislen(row)
    # 4 quantization steps → at most 4 color openers across the whole run.
    assert_operator row.scan(/\e\[38;2;/).length, :<=, 4
    assert row.end_with?("\e[0m")
  end

  # -- degradation -------------------------------------------------------------

  def test_solid_fallback_when_no_gradient
    p = B.painter(width: 10, height: 4, solid: "\e[90m")
    assert_equal "\e[90m│\e[0m", p.cell(0, 1, "│")
  end

  def test_no_gradient_and_no_solid_is_plain
    p = B.painter(width: 10, height: 4)
    assert_equal "│", p.cell(0, 1, "│")
    assert_equal "╭──╮", p.run(0, 0, ["╭", "─", "─", "╮"])
  end

  def test_no_truecolor_drops_gradient_to_solid
    B.truecolor = false
    p = B.painter(width: 10, height: 4, gradient: GRAD, solid: "\e[90m")
    assert_equal "\e[90m╭\e[0m", p.cell(0, 0, "╭")
  end

  # -- box ---------------------------------------------------------------------

  def test_box_has_rounded_corners_and_exact_width
    rows = B.box(inner_lines: ["ab", "cd"], inner_width: 2, gradient: GRAD)
    stripped = rows.map { |r| A.strip(r) }
    assert_equal "╭──╮", stripped.first
    assert_equal "│ab│", stripped[1]
    assert_equal "╰──╯", stripped.last
    rows.each { |r| assert_equal 4, A.vislen(r) }
  end

  def test_box_square_corners_opt_out
    rows = B.box(inner_lines: ["ab"], inner_width: 2, corners: :square)
    assert_equal "┌──┐", A.strip(rows.first)
    assert_equal "└──┘", A.strip(rows.last)
  end

  def test_box_oversized_title_never_widens_the_top_row
    # A title wider than the inner width must be truncated so every row of the
    # box stays the same width — the primitive enforces its own fit.
    rows = B.box(inner_lines: [], inner_width: 3, title: "abcdef", title_lead: 1)
    assert_equal [5], rows.map { |r| A.vislen(r) }.uniq, "every row stays inner_width + 2"
    assert_equal "╭─a…╮", A.strip(rows.first) # title truncated with an ellipsis, corners intact
  end

  def test_box_title_strip_keeps_title_unpainted
    title = "\e[1mHi\e[0m"
    rows = B.box(inner_lines: ["....."], inner_width: 5, gradient: GRAD, title: title, title_lead: 1)
    top = rows.first
    assert_includes top, title           # title passes through verbatim
    assert_equal "╭─Hi──╮", A.strip(top) # lead dash, title, fill dashes, corners (inner width 5)
  end

  private

  # Extract the [r,g,b] of the first truecolor fg SGR in a styled string.
  def fg(str)
    m = str.match(/\e\[38;2;(\d+);(\d+);(\d+)m/)
    [m[1].to_i, m[2].to_i, m[3].to_i]
  end
end
