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

  def test_wrap_hard_breaks_overlong_words
    lines = A.wrap("x" * 25, 10)
    assert_equal ["x" * 10, "x" * 10, "x" * 5], lines
  end

  def test_wrap_handles_binary_encoded_utf8
    # subprocess reads arrive as ASCII-8BIT; multibyte content must not raise
    binary = "moved “Book flight” → 07-03 ✓".b
    lines = A.wrap(binary, 20)
    assert lines.all? { |l| l.encoding == Encoding::UTF_8 }
    assert_includes lines.join(" "), "→ 07-03 ✓"
  end

  def test_wrap_scrubs_invalid_utf8
    # a multibyte char split across a read boundary leaves invalid bytes
    truncated = "task done ✓".b[0..-2] # chop one byte off the ✓
    lines = A.wrap(truncated, 20)
    assert lines.all?(&:valid_encoding?)
    assert_includes lines.first, "task done"
  end

  def test_vislen_counts_emoji_as_two_cells
    assert_equal 2, A.vislen("✨")
    assert_equal 15, A.vislen("Inbox empty. ✨")
  end

  def test_vislen_text_presentation_symbols_stay_one_cell
    # ⚠ and ✓ render text-presentation (one cell) without a U+FE0F selector
    assert_equal 1, A.vislen("⚠")
    assert_equal 1, A.vislen("✓")
    assert_equal 1, A.vislen("▸")
  end

  def test_vislen_emoji_variation_selector_forces_wide
    assert_equal 2, A.vislen("⚠️")
  end

  def test_vpad_accounts_for_emoji_width
    # the empty-inbox bug: padding must reserve two cells for the emoji so the
    # line does not overflow its box and wrap to a new terminal row
    padded = A.vpad(A.dim("Inbox empty. ✨"), 20)
    assert_equal 20, A.vislen(padded)
  end

  def test_vtrunc_does_not_split_wide_char
    out = A.vtrunc("ab✨cd", 4)
    # never exceeds the budget, and never emits a half of the 2-cell ✨
    assert_operator A.vislen(out), :<=, 4
    refute_includes A.strip(out), "✨"
  end

  def test_vtrunc_binary_wrapped_line_roundtrip
    # the exact crash path: wrap output fed to vtrunc alongside UTF-8 borders
    line = A.wrap("é" * 50, 40).first
    out = A.vtrunc(line, 10)
    assert_equal 10, A.vislen(out)
  end
end
