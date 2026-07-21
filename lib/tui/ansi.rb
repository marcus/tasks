# frozen_string_literal: true

require_relative "../char_width"

module Tui
  # ANSI color + width-aware string helpers. Everything that knows about
  # escape codes lives here so the rest of the code can treat styled
  # strings as opaque. Bare codepoint/grapheme widths come from the shared
  # CharWidth kernel; this module adds the SGR-aware layer on top.
  module Ansi
    module_function

    SGR = /\e\[[0-9;]*m/

    # cell_slice tokenizes into SGR escapes or single grapheme clusters. Built
    # once: interpolating SGR inline recompiles the regex on every call, and
    # cell_slice runs per box row of every modal frame.
    CELL_SCAN = /(#{SGR})|(\X)/

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

    # Bare codepoint/grapheme widths live in the shared CharWidth kernel; these
    # delegators keep Ansi.char_width / Ansi.cluster_width (and the bare internal
    # calls below) working unchanged.
    def char_width(ch) = CharWidth.char_width(ch)
    def cluster_width(gc) = CharWidth.cluster_width(gc)

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

      text.scan(CELL_SCAN) do |esc, gc|
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
