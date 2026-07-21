# frozen_string_literal: true

module TermForm
  # UTF-8, grapheme, and terminal-cell helpers shared by the reusable text
  # fields. This module deliberately has no renderer or terminal dependency.
  module Text
    WIDE = [
      0x1100..0x115F, 0x2329..0x232A, 0x2E80..0x303E, 0x3041..0x33FF,
      0x3400..0x4DBF, 0x4E00..0x9FFF, 0xA000..0xA4CF, 0xA960..0xA97F,
      0xAC00..0xD7A3, 0xF900..0xFAFF, 0xFE10..0xFE19, 0xFE30..0xFE6F,
      0xFF00..0xFF60, 0xFFE0..0xFFE6,
      0x231A..0x231B, 0x23E9..0x23EC, 0x23F0..0x23F0, 0x23F3..0x23F3,
      0x25FD..0x25FE, 0x2614..0x2615, 0x2648..0x2653, 0x267F..0x267F,
      0x2693..0x2693, 0x26A1..0x26A1, 0x26AA..0x26AB, 0x26BD..0x26BE,
      0x26C4..0x26C5, 0x26CE..0x26CE, 0x26D4..0x26D4, 0x26EA..0x26EA,
      0x26F2..0x26F3, 0x26F5..0x26F5, 0x26FA..0x26FA, 0x26FD..0x26FD,
      0x2705..0x2705, 0x270A..0x270B, 0x2728..0x2728, 0x274C..0x274C,
      0x274E..0x274E, 0x2753..0x2755, 0x2757..0x2757, 0x2795..0x2797,
      0x27B0..0x27B0, 0x27BF..0x27BF, 0x2B1B..0x2B1C, 0x2B50..0x2B50,
      0x2B55..0x2B55, 0x1F004..0x1F004, 0x1F0CF..0x1F0CF,
      0x1F18E..0x1F18E, 0x1F191..0x1F19A, 0x1F1E6..0x1F1FF,
      0x1F200..0x1F2FF, 0x1F300..0x1F64F, 0x1F680..0x1F6FF,
      0x1F7E0..0x1F7EB, 0x1F900..0x1F9FF, 0x1FA70..0x1FAFF,
    ].freeze

    ZERO_WIDTH = [
      0x0300..0x036F, 0x0483..0x0489, 0x0591..0x05BD, 0x0610..0x061A,
      0x064B..0x065F, 0x0670..0x0670, 0x06D6..0x06DC, 0x0E31..0x0E31,
      0x0E34..0x0E3A, 0x1AB0..0x1AFF, 0x1DC0..0x1DFF, 0x200B..0x200F,
      0x202A..0x202E, 0x2060..0x2064, 0x20D0..0x20FF, 0xFE00..0xFE0F,
      0xFE20..0xFE2F, 0xFEFF..0xFEFF,
    ].freeze

    module_function

    def normalize(value)
      value.to_s.encode("UTF-8", invalid: :replace, undef: :replace, replace: "")
    end

    def graphemes(value) = normalize(value).each_grapheme_cluster.to_a

    def char_width(char)
      codepoint = char.ord
      # Printable ASCII is the overwhelmingly common input and every char of
      # every field render passes through here, so return before scanning the
      # ~75 WIDE/ZERO_WIDTH ranges. Mirrors Tui::Ansi.char_width.
      return 1 if codepoint >= 0x20 && codepoint < 0x7F
      return 0 if codepoint < 0x20
      return 0 if ZERO_WIDTH.any? { |range| range.cover?(codepoint) }
      return 2 if WIDE.any? { |range| range.cover?(codepoint) }

      1
    end

    def cluster_width(grapheme)
      return 2 if grapheme.each_char.any? { |char| char.ord == 0xFE0F }

      base = grapheme.each_char.find { |char| char_width(char).positive? }
      base ? char_width(base) : 0
    end

    def cell_width(value) = graphemes(value).sum { |grapheme| cluster_width(grapheme) }

    # Slice a plain string by terminal cells. Partial wide graphemes become
    # spaces, preserving the columns of content on either side.
    def cell_slice(value, start, width)
      start = [Integer(start), 0].max
      width = [Integer(width), 0].max
      return +"" if width.zero?

      finish = start + width
      cell = 0
      output = +""
      graphemes(value).each do |grapheme|
        grapheme_width = cluster_width(grapheme)
        cluster_end = cell + grapheme_width
        if grapheme_width.zero?
          output << grapheme if cell >= start && cell < finish
        elsif cluster_end > start && cell < finish
          overlap_start = [cell, start].max
          overlap_end = [cluster_end, finish].min
          if overlap_start == cell && overlap_end == cluster_end
            output << grapheme
          else
            output << " " * (overlap_end - overlap_start)
          end
        end
        cell = cluster_end
        break if cell >= finish
      end
      output
    end
  end

  # Mutable editing state used by TermForm fields and the existing TUI input.
  # Cursor offsets count grapheme clusters, never bytes or codepoints.
  class TextEditor
    CTRL_A = "\x01"
    CTRL_B = "\x02"
    CTRL_D = "\x04"
    CTRL_E = "\x05"
    CTRL_F = "\x06"
    CTRL_H = "\x08"
    CTRL_K = "\x0b"
    CTRL_U = "\x15"
    CTRL_W = "\x17"

    attr_reader :cursor

    def initialize(text = +"", multiline: false, kill_to_end: true)
      @multiline = multiline
      @kill_to_end = kill_to_end
      replace(text)
    end

    def text = @text
    def to_s = @text
    def to_str = @text
    def empty? = @text.empty?
    def strip = @text.strip
    def chars = @text.chars
    def end_with?(*suffixes) = @text.end_with?(*suffixes)
    def ==(other) = @text == other.to_s

    def cursor=(position)
      @cursor = [[Integer(position), 0].max, units.length].min
    end

    def <<(raw)
      insert(raw)
      self
    end

    def replace(raw)
      @text = sanitize(raw)
      @cursor = units.length
      self
    end

    def clear = replace(+"")

    def insert(raw)
      incoming = units_for(sanitize(raw))
      return nil if incoming.empty?

      current = units
      current.insert(@cursor, *incoming)
      @cursor += incoming.length
      @text = current.join
      :changed
    end

    def handle_key(key)
      case key
      when CTRL_A, "\e[H", "\e[1~", "\eOH", "\e[1;5H", "\e[1;3H" then move_start
      when CTRL_E, "\e[F", "\e[4~", "\eOF", "\e[1;5F", "\e[1;3F" then move_end
      when CTRL_B, "\e[D"                                          then move_left
      when CTRL_F, "\e[C"                                          then move_right
      when "\e[1;5D", "\e[1;3D", "\e[5D", "\e[3D"                 then word_left
      when "\e[1;5C", "\e[1;3C", "\e[5C", "\e[3C"                 then word_right
      when CTRL_D, "\e[3~"                                         then delete_forward
      when CTRL_H, "\b", "\x7f"                                   then backspace
      when CTRL_K
        @kill_to_end ? kill_to_end : nil
      when CTRL_U then kill_to_start
      when CTRL_W then kill_word_back
      when "\r", "\n"
        @multiline ? insert("\n") : nil
      else
        insert(key) if printable_key?(key)
      end
    end

    def printable_key?(key)
      return false if key.nil? || key.empty?

      key.each_grapheme_cluster.all? { |grapheme| grapheme.match?(/[[:print:]]/) }
    end

    private

    def units = units_for(@text)
    def units_for(text) = text.each_grapheme_cluster.to_a

    def sanitize(raw)
      text = Text.normalize(raw)
      text = if @multiline
               text.gsub("\r\n", "\n").tr("\r\t", "\n ")
             else
               text.tr("\r\n\t", "   ")
             end
      text.each_grapheme_cluster.select { |grapheme| grapheme == "\n" || printable_key?(grapheme) }.join
    end

    def move_start
      @cursor = 0
      :handled
    end

    def move_end
      @cursor = units.length
      :handled
    end

    def move_left
      @cursor = [@cursor - 1, 0].max
      :handled
    end

    def move_right
      @cursor = [@cursor + 1, units.length].min
      :handled
    end

    def backspace
      return :handled if @cursor.zero?

      current = units
      current.delete_at(@cursor - 1)
      @cursor -= 1
      @text = current.join
      :changed
    end

    def delete_forward
      current = units
      return :handled if @cursor >= current.length

      current.delete_at(@cursor)
      @text = current.join
      :changed
    end

    def kill_to_start
      return :handled if @cursor.zero?

      current = units
      current.slice!(0, @cursor)
      @cursor = 0
      @text = current.join
      :changed
    end

    def kill_to_end
      current = units
      return :handled if @cursor >= current.length

      current.slice!(@cursor, current.length - @cursor)
      @text = current.join
      :changed
    end

    def kill_word_back
      current = units
      return :handled if @cursor.zero?

      index = @cursor
      index -= 1 while index.positive? && current[index - 1].match?(/\s/)
      index -= 1 while index.positive? && current[index - 1].match?(/\S/)
      current.slice!(index, @cursor - index)
      @cursor = index
      @text = current.join
      :changed
    end

    def word_left
      current = units
      index = @cursor
      index -= 1 while index.positive? && current[index - 1].match?(/\s/)
      index -= 1 while index.positive? && current[index - 1].match?(/\S/)
      @cursor = index
      :handled
    end

    def word_right
      current = units
      index = @cursor
      index += 1 while index < current.length && current[index].match?(/\S/)
      index += 1 while index < current.length && current[index].match?(/\s/)
      @cursor = index
      :handled
    end
  end
end
