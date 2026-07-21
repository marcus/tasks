# frozen_string_literal: true

# Terminal display width of Unicode codepoints and grapheme clusters. This is
# the single source of truth shared by Tui::Ansi (ANSI/SGR-aware) and
# TermForm::Text (plain reusable fields) — both used to carry verbatim copies
# of these tables and functions, which is how one perf regression could live in
# two places at once.
#
# Deliberately dependency-free: no terminal, renderer, or SGR knowledge. Given
# a bare character or grapheme it answers 0, 1, or 2 cells.
module CharWidth
  # Codepoint ranges that occupy two terminal cells: East Asian Wide/
  # Fullwidth plus default-emoji-presentation symbols and the emoji planes.
  # Text-presentation symbols (e.g. ⚠ U+26A0, ✓ U+2713) stay one cell, which
  # is how terminals actually render them without a U+FE0F selector.
  WIDE = [
    0x1100..0x115F, 0x2329..0x232A, 0x2E80..0x303E, 0x3041..0x33FF,
    0x3400..0x4DBF, 0x4E00..0x9FFF, 0xA000..0xA4CF, 0xA960..0xA97F,
    0xAC00..0xD7A3, 0xF900..0xFAFF, 0xFE10..0xFE19, 0xFE30..0xFE6F,
    0xFF00..0xFF60, 0xFFE0..0xFFE6,
    # BMP emoji with default emoji presentation (Emoji_Presentation=Yes)
    0x231A..0x231B, 0x23E9..0x23EC, 0x23F0..0x23F0, 0x23F3..0x23F3,
    0x25FD..0x25FE, 0x2614..0x2615, 0x2648..0x2653, 0x267F..0x267F,
    0x2693..0x2693, 0x26A1..0x26A1, 0x26AA..0x26AB, 0x26BD..0x26BE,
    0x26C4..0x26C5, 0x26CE..0x26CE, 0x26D4..0x26D4, 0x26EA..0x26EA,
    0x26F2..0x26F3, 0x26F5..0x26F5, 0x26FA..0x26FA, 0x26FD..0x26FD,
    0x2705..0x2705, 0x270A..0x270B, 0x2728..0x2728, 0x274C..0x274C,
    0x274E..0x274E, 0x2753..0x2755, 0x2757..0x2757, 0x2795..0x2797,
    0x27B0..0x27B0, 0x27BF..0x27BF, 0x2B1B..0x2B1C, 0x2B50..0x2B50,
    0x2B55..0x2B55,
    # emoji planes (symbols, pictographs, transport, supplemental, flags)
    0x1F004..0x1F004, 0x1F0CF..0x1F0CF, 0x1F18E..0x1F18E, 0x1F191..0x1F19A,
    0x1F1E6..0x1F1FF, 0x1F200..0x1F2FF, 0x1F300..0x1F64F, 0x1F680..0x1F6FF,
    0x1F7E0..0x1F7EB, 0x1F900..0x1F9FF, 0x1FA70..0x1FAFF,
  ].freeze

  # Codepoint ranges that occupy zero cells: combining marks and the
  # zero-width/format controls. U+FE0F (emoji variation selector) lands here
  # too; cluster_width promotes the whole grapheme to width 2 when present.
  ZERO_WIDTH = [
    0x0300..0x036F, 0x0483..0x0489, 0x0591..0x05BD, 0x0610..0x061A,
    0x064B..0x065F, 0x0670..0x0670, 0x06D6..0x06DC, 0x0E31..0x0E31,
    0x0E34..0x0E3A, 0x1AB0..0x1AFF, 0x1DC0..0x1DFF, 0x200B..0x200F,
    0x202A..0x202E, 0x2060..0x2064, 0x20D0..0x20FF, 0xFE00..0xFE0F,
    0xFE20..0xFE2F, 0xFEFF..0xFEFF,
  ].freeze

  module_function

  # Display width of a single codepoint: 0 (combining/zero-width),
  # 2 (wide/emoji) or 1 (everything else). Printable ASCII is by far the
  # hottest input (every char of every styled row runs through here), so it
  # returns before touching the ~75 WIDE/ZERO_WIDTH ranges — those linear
  # `cover?` scans dominated modal compositing and field rendering otherwise.
  def char_width(ch)
    cp = ch.ord
    return 1 if cp >= 0x20 && cp < 0x7F
    return 0 if cp < 0x20
    return 0 if ZERO_WIDTH.any? { |r| r.cover?(cp) }
    return 2 if WIDE.any? { |r| r.cover?(cp) }

    1
  end

  # Display width of one grapheme cluster. A U+FE0F selector forces emoji
  # (wide) presentation; otherwise the visible base character sets the width
  # and trailing combining marks add nothing.
  def cluster_width(gc)
    return 2 if gc.each_char.any? { |c| c.ord == 0xFE0F }

    base = gc.each_char.find { |c| char_width(c).positive? }
    base ? char_width(base) : 0
  end
end
