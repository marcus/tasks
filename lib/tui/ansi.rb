# frozen_string_literal: true

module Tui
  # ANSI color + width-aware string helpers. Everything that knows about
  # escape codes lives here so the rest of the code can treat styled
  # strings as opaque.
  module Ansi
    module_function

    SGR = /\e\[[0-9;]*m/

    def color(str, *codes) = "\e[#{codes.join(";")}m#{str}\e[0m"

    # Composite `sgr` (an opening SGR sequence, e.g. "\e[1m") over `str`, whose
    # embedded field styling already closes with a reset (\e[0m). Re-injects
    # `sgr` immediately after every true reset so the overlay survives each
    # field boundary instead of being cleared by it — same trick Frame uses to
    # lay :selection under a row's own colors. \e[0?m matches only true resets
    # (\e[0m / \e[m), never a field opener that merely carries a 0 param (e.g.
    # \e[38;2;0;0;0m). Does NOT append a trailing reset (the caller closes once,
    # after any padding). `sgr` empty → `str` unchanged.
    def composite(sgr, str)
      return str if sgr.empty?
      sgr + str.gsub(/\e\[0?m/) { |reset| reset + sgr }
    end

    def bold(s)   = color(s, 1)
    def dim(s)    = color(s, 90)
    def red(s)    = color(s, 31)
    def yellow(s) = color(s, 33)
    def cyan(s)   = color(s, 36)
    def invert(s) = color(s, 7)

    def normalize(s)
      return s if s.encoding == Encoding::UTF_8 && s.valid_encoding?

      s.dup.force_encoding(Encoding::UTF_8).scrub("�")
    end

    def strip(s) = normalize(s).gsub(SGR, "")

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

    # Display width of a single codepoint: 0 (combining/zero-width),
    # 2 (wide/emoji) or 1 (everything else).
    def char_width(ch)
      cp = ch.ord
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

    # Visible display width in terminal cells (ignores escape codes, counts
    # wide/emoji graphemes as two cells). Plain ASCII with no SGR is just
    # bytesize — the common case for muted chrome and unstyled fragments.
    def vislen(s)
      text = normalize(s)
      return text.bytesize if plain_ascii?(text)

      strip(text).each_grapheme_cluster.sum { |g| cluster_width(g) }
    end

    # Printable ASCII only (no ESC, no controls, no DEL). Those bytes are all
    # width-1, so grapheme/Unicode tables are pure overhead.
    def plain_ascii?(s)
      s.each_byte.all? { |b| b >= 0x20 && b <= 0x7E }
    end
    private_class_method :plain_ascii?

    # Return the visible cell window [start, start + width) without splitting
    # grapheme clusters. ANSI SGR styling is retained and closed at the slice
    # boundary. If a boundary crosses a wide cluster, spaces occupy the partial
    # cells so content after that cluster stays at its original terminal column.
    # With width omitted, return everything from start to the end.
    def cell_slice(s, start, width = nil)
      text = normalize(s)
      start = [Integer(start), 0].max
      width = [Integer(width), 0].max unless width.nil?
      return +"" if width == 0

      finish = width && start + width
      prefix = +""
      out = +""
      cell = 0
      started = false
      used_sgr = false

      text.scan(/(#{SGR})|(\X)/) do |esc, gc|
        if esc
          if !started && cell <= start
            prefix << esc
          elsif finish.nil? || cell < finish
            out << esc
            used_sgr = true
          end
          next
        end

        cw = cluster_width(gc)
        if cw.zero?
          if cell >= start && (finish.nil? || cell < finish)
            unless started
              out << prefix
              used_sgr ||= !prefix.empty?
              started = true
            end
            out << gc
          end
          next
        end

        cluster_end = cell + cw
        if cluster_end <= start
          cell = cluster_end
          next
        end
        break if finish && cell >= finish

        overlap_start = [cell, start].max
        overlap_end = finish ? [cluster_end, finish].min : cluster_end
        if overlap_end > overlap_start
          unless started
            out << prefix
            used_sgr ||= !prefix.empty?
            started = true
          end
          if overlap_start == cell && overlap_end == cluster_end
            out << gc
          else
            out << " " * (overlap_end - overlap_start)
          end
        end
        cell = cluster_end
      end

      out << "\e[0m" if used_sgr && !out.end_with?("\e[0m")
      out
    end

    # Pad to visible width w (no-op if already wider).
    def vpad(s, w)
      text = normalize(s)
      pad = w - vislen(text)
      pad.positive? ? text + " " * pad : text
    end

    # Truncate to visible width w, appending a dim ellipsis. Escape codes
    # are preserved; a reset is appended so styles can't leak.
    def vtrunc(s, w)
      w = Integer(w)
      return +"" unless w.positive?

      text = normalize(s)
      return text if vislen(text) <= w

      cell_slice(text, 0, w - 1) + dim("…")
    end

    # Word-wrap text to a terminal-cell width, preserving ANSI styling and
    # grapheme clusters. Subprocess output is normalized defensively because it
    # can arrive as BINARY or end on an incomplete UTF-8 sequence.
    def wrap(text, w)
      w = [Integer(w), 1].max
      styled_lines(normalize(text)).flat_map do |line|
        wrap_line(line, w)
      end
    end

    def styled_lines(text)
      state = +""
      text.split("\n", -1).map.with_index do |line, index|
        styled = index.zero? ? line : state + line
        line.scan(SGR) { |sgr| state << sgr }
        styled
      end
    end
    private_class_method :styled_lines

    def wrap_line(line, w)
      plain = strip(line)
      clusters = []
      cell = 0
      plain.each_grapheme_cluster do |gc|
        cw = cluster_width(gc)
        clusters << [gc, cell, cell + cw]
        cell += cw
      end

      words = []
      current = []
      clusters.each do |entry|
        if entry[0].match?(/\A\s+\z/)
          words << current unless current.empty?
          current = []
        else
          current << entry
        end
      end
      words << current unless current.empty?
      return [""] if words.empty?

      ranges = []
      line_start = nil
      line_end = nil
      words.each do |word|
        word_start = word.first[1]
        word_end = word.last[2]
        word_width = word_end - word_start
        if word_width > w
          ranges << [line_start, line_end] if line_start
          ranges.concat(hard_wrap_ranges(word, w))
          line_start = line_end = nil
        elsif line_start && word_end - line_start > w
          ranges << [line_start, line_end]
          line_start = word_start
          line_end = word_end
        else
          line_start ||= word_start
          line_end = word_end
        end
      end
      ranges << [line_start, line_end] if line_start
      ranges.map { |from, to| cell_slice(line, from, to - from) }
    end
    private_class_method :wrap_line

    def hard_wrap_ranges(word, w)
      ranges = []
      from = nil
      to = nil
      word.each do |_, cluster_start, cluster_end|
        cw = cluster_end - cluster_start
        if cw > w
          ranges << [from, to] if from
          ranges << [cluster_start, cluster_start + w]
          from = to = nil
        elsif from && cluster_end - from > w
          ranges << [from, to]
          from = cluster_start
          to = cluster_end
        else
          from ||= cluster_start
          to = cluster_end
        end
      end
      ranges << [from, to] if from
      ranges
    end
    private_class_method :hard_wrap_ranges
  end
end
