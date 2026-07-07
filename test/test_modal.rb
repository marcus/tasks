# frozen_string_literal: true

require_relative "test_helper"
require "tui/modal"

class TestModal < Minitest::Test
  A = Tui::Ansi

  BODY_H = 12 # borders take 2 → 10 inner rows; status line takes 1 when shown

  def modal(lines: (1..30).map { |i| "line #{i}" }, title: "t", kind: :help, filterable: true)
    Tui::Modal.new(title: title, lines: lines, kind: kind, filterable: filterable)
  end

  def texts(view) = view[:lines].map { |l| A.strip(l) }

  # -- width -----------------------------------------------------------------

  def test_width_comes_from_full_content_not_the_visible_window
    lines = ["short"] * 20 + ["a much much longer line below the fold"]
    m = modal(lines: lines)
    w = m.width
    assert_operator w, :>=, A.vislen(lines.last) + 4
    m.scroll_page(1, BODY_H)
    assert_equal w, m.view(BODY_H)[:width], "scrolling must not change the width"
    m.filter = "short"
    assert_equal w, m.view(BODY_H)[:width], "filtering must not change the width"
  end

  def test_width_floors_on_title_and_minimum
    assert_operator modal(lines: ["x"], title: "a much longer modal title").width,
                    :>=, A.vislen("a much longer modal title") + 6 + 4
    assert_equal 34, modal(lines: ["x"], title: "t").width # 30 min + box padding
  end

  # -- height budget ----------------------------------------------------------

  def test_view_never_exceeds_the_body
    (5..14).each do |body_h|
      view = modal.view(body_h)
      assert_operator view[:lines].size + 2, :<=, [body_h, Tui::Modal::MIN_INNER + 2].max,
                      "boxed modal must fit a #{body_h}-row body"
    end
  end

  def test_short_content_shows_fully_without_status_line
    view = modal(lines: %w[a b c]).view(BODY_H)
    assert_equal %w[a b c], texts(view)
  end

  def test_overflowing_content_gets_scroll_status_line
    view = modal.view(BODY_H)
    assert_equal 10, view[:lines].size # 9 content + status
    assert_match(%r{9/30 · ↑↓ scroll}, texts(view).last)
  end

  # -- scrolling ---------------------------------------------------------------

  def test_line_half_and_page_scroll_steps
    m = modal # viewport: 10 inner - 1 status = 9
    m.scroll_line(1, BODY_H)
    assert_equal 1, m.scroll
    m.scroll_half(1, BODY_H)
    assert_equal 5, m.scroll
    m.scroll_page(1, BODY_H)
    assert_equal 14, m.scroll
    m.scroll_page(-1, BODY_H)
    assert_equal 5, m.scroll
  end

  def test_scroll_clamps_at_both_ends
    m = modal
    m.scroll_line(-1, BODY_H)
    assert_equal 0, m.scroll, "can't scroll above the top"
    5.times { m.scroll_page(1, BODY_H) }
    assert_equal 21, m.scroll, "clamps to content size - viewport"
    assert_equal "line 30", texts(m.view(BODY_H))[-2], "last line visible at max scroll"
  end

  # -- filtering ---------------------------------------------------------------

  def test_filter_narrows_lines_case_insensitively_ignoring_ansi
    m = modal(lines: ["plain one", A.cyan("Styled TWO"), "three"])
    m.filter = "two"
    assert_equal 1, m.lines.size
    assert_includes A.strip(m.lines.first), "Styled TWO"
  end

  def test_filter_resets_scroll_and_shows_query_in_status
    m = modal
    m.scroll_page(1, BODY_H)
    m.filter = "line 1"
    assert_equal 0, m.scroll
    status = texts(m.view(BODY_H)).last
    assert_includes status, "/ line 1"
    assert_includes status, "/11" # line 1, 10–19
  end

  def test_no_match_filter_shows_placeholder_not_empty_box
    m = modal
    m.filter = "zzz-nope"
    lines = texts(m.view(BODY_H))
    assert_match(/no lines match/, lines.first)
    assert_includes lines.last, "0/0"
  end

  def test_clearing_filter_restores_all_lines
    m = modal
    m.filter = "line 3"
    m.filter = nil
    assert_equal 30, m.lines.size
    m.filter = "   "
    assert_nil m.filter, "blank filter means off"
  end

  def test_filterable_flag
    assert modal.filterable?
    refute modal(filterable: false).filterable?
  end
end
