# frozen_string_literal: true

require_relative "../char_width"

module TermForm
  # UTF-8, grapheme, and terminal-cell helpers shared by the reusable text
  # fields. This module deliberately has no renderer or terminal dependency.
  # Bare codepoint/grapheme widths come from the equally dependency-free
  # CharWidth kernel (also used by Tui::Ansi).
  module Text
    module_function

    def normalize(value)
      value.to_s.encode("UTF-8", invalid: :replace, undef: :replace, replace: "")
    end

    def graphemes(value) = normalize(value).each_grapheme_cluster.to_a

    def char_width(char) = CharWidth.char_width(char)
    def cluster_width(grapheme) = CharWidth.cluster_width(grapheme)

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
