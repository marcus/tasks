# frozen_string_literal: true

require_relative "test_helper"

class TestAnsi < Minitest::Test
  A = Tui::Ansi

  def test_vislen_plain_ascii_matches_bytesize
    assert_equal 5, A.vislen("hello")
    assert_equal 0, A.vislen("")
    assert_equal "hello world!".bytesize, A.vislen("hello world!")
  end

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
    assert lines.all? { |l| A.vislen(l) <= 10 }
    assert_equal "one two three four five", lines.join(" ").squeeze(" ")
  end

  def test_wrap_keeps_blank_lines
    assert_equal ["a", "", "b"], A.wrap("a\n\nb", 10)
  end

  def test_wrap_preserves_ansi
    lines = A.wrap(Tui::Ansi.bold("styled text"), 20)
    assert_equal ["styled text"], lines.map { |line| A.strip(line) }
    assert_includes lines.first, "\e[1m"
    assert lines.first.end_with?("\e[0m")
  end

  def test_wrap_carries_active_style_across_explicit_newlines
    lines = A.wrap(A.bold("first\nsecond"), 20)
    assert_equal %w[first second], lines.map { |line| A.strip(line) }
    assert lines.all? { |line| line.include?("\e[1m") && line.end_with?("\e[0m") }
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

  def test_wrap_uses_terminal_cells_for_wide_and_combining_graphemes
    samples = [
      "界界界界界",
      "👩‍💻👩‍💻 task",
      "e\u0301e\u0301e\u0301e\u0301",
      A.cyan("界界 styled text"),
    ]

    samples.each do |sample|
      (2..7).each do |width|
        lines = A.wrap(sample, width)
        assert lines.all? { |line| A.vislen(line) <= width },
               "#{sample.inspect} exceeded #{width}: #{lines.inspect}"
        assert lines.all?(&:valid_encoding?)
      end
    end
  end

  def test_wrap_never_splits_grapheme_clusters
    text = "界👩‍💻e\u0301界"
    lines = A.wrap(text, 3)
    assert_equal text, lines.map { |line| A.strip(line) }.join
    assert_equal [2, 3, 2], lines.map { |line| A.vislen(line) }
  end

  def test_wrap_replaces_a_cluster_that_cannot_fit_the_budget
    assert_equal [" "], A.wrap("界", 1)
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

  def test_vtrunc_zero_width_is_empty
    assert_equal "", A.vtrunc("界", 0)
  end

  def test_cell_slice_uses_cell_offsets_and_pads_partial_wide_clusters
    assert_equal "a ", A.cell_slice("a界b", 0, 2)
    assert_equal "界", A.cell_slice("a界b", 1, 2)
    assert_equal " b", A.cell_slice("a界b", 2, 2)
  end

  def test_cell_slice_preserves_styles_and_closes_them
    sliced = A.cell_slice(A.red("a界b"), 2, 2)
    assert_equal " b", A.strip(sliced)
    assert_includes sliced, "\e[31m"
    assert sliced.end_with?("\e[0m")
  end

  def test_cell_slice_normalizes_invalid_binary_utf8
    sliced = A.cell_slice("ok \xE2\x9C".b, 0, 10)
    assert sliced.valid_encoding?
    assert_equal Encoding::UTF_8, sliced.encoding
    assert_includes sliced, "ok "
  end

  def test_composite_empty_sgr_is_noop
    assert_equal "hello", A.composite("", "hello")
    styled = A.bold("hi")
    assert_equal styled, A.composite("", styled)
  end

  def test_composite_reinjects_after_embedded_reset
    # a field that closes with \e[0m mid-string: the overlay must re-open after
    # it so styling survives the field boundary instead of being cleared.
    body = "a" + A.red("b") + "c" # "a\e[31mb\e[0mc"
    out = A.composite("\e[1m", body)
    assert_equal "\e[1ma\e[31mb\e[0m\e[1mc", out
  end

  def test_composite_does_not_append_trailing_reset
    # the caller closes once, after any padding — composite must not add its own
    out = A.composite("\e[1m", "plain")
    refute out.end_with?("\e[0m")
    assert_equal "\e[1mplain", out
  end

  def test_composite_ignores_field_opener_with_zero_param
    # \e[38;2;0;0;0m carries a 0 but is NOT a reset — no re-injection there
    body = "\e[38;2;0;0;0mx\e[0m"
    out = A.composite("\e[1m", body)
    assert_equal "\e[1m\e[38;2;0;0;0mx\e[0m\e[1m", out
  end
end
