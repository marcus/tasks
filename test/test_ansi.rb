# frozen_string_literal: true

require_relative "test_helper"

class TestAnsi < Minitest::Test
  A = Tui::Ansi

  def test_vislen_ignores_escape_codes
    assert_equal 5, A.vislen(A.bold(A.red("hello")))
  end

  def test_strip
    assert_equal "hi there", A.strip(A.dim("hi") + " " + A.cyan("there"))
  end

  def test_vpad_pads_to_visible_width
    padded = A.vpad(A.bold("ab"), 5)
    assert_equal 5, A.vislen(padded)
  end

  def test_vpad_leaves_wide_strings_alone
    assert_equal "abcdef", A.vpad("abcdef", 3)
  end

  def test_vtrunc_short_string_unchanged
    assert_equal "abc", A.vtrunc("abc", 10)
  end

  def test_vtrunc_truncates_to_width
    out = A.vtrunc("abcdefghij", 5)
    assert_equal 5, A.vislen(out)
    assert_includes A.strip(out), "…"
  end

  def test_vtrunc_preserves_codes_and_resets
    out = A.vtrunc(A.red("abcdefghij"), 5)
    assert_includes out, "\e[31m"
    assert_includes out, "\e[0m"
    assert_equal 5, A.vislen(out)
  end

  def test_wrap_wraps_words
    lines = A.wrap("one two three four five", 10)
    assert lines.all? { |l| l.length <= 10 }
    assert_equal "one two three four five", lines.join(" ").squeeze(" ")
  end

  def test_wrap_keeps_blank_lines
    assert_equal ["a", "", "b"], A.wrap("a\n\nb", 10)
  end

  def test_wrap_strips_ansi
    lines = A.wrap(Tui::Ansi.bold("styled text"), 20)
    assert_equal ["styled text"], lines
  end
end
