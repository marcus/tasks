# frozen_string_literal: true

module Tui
  # Small single-line editor used by every focused text field in the TUI.
  # It keeps cursor/editing behavior out of App so paste/link handling can grow
  # without spreading terminal details through the mode dispatcher.
  class TextInput
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

    def initialize(text = +"")
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

    def <<(raw)
      insert(raw)
      self
    end

    def replace(raw)
      @text = sanitize(raw)
      @cursor = units.length
      self
    end

    def clear
      replace(+"")
    end

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
      when CTRL_H, "\b", ""                                       then backspace
      when CTRL_K                                                   then kill_to_end
      when CTRL_U                                                   then kill_to_start
      when CTRL_W                                                   then kill_word_back
      else
        insert(key) if printable_key?(key)
      end
    end

    private

    def units
      units_for(@text)
    end

    def units_for(text)
      text.each_grapheme_cluster.to_a
    end

    def sanitize(raw)
      raw.to_s.encode("UTF-8", invalid: :replace, undef: :replace, replace: "")
         .tr("\r\n\t", "   ")
         .each_grapheme_cluster
         .select { |g| printable_key?(g) }
         .join
    end

    def printable_key?(key)
      return false if key.nil? || key.empty?

      key.each_grapheme_cluster.all? { |g| g.match?(/[[:print:]]/) }
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

      i = @cursor
      i -= 1 while i.positive? && current[i - 1].match?(/\s/)
      i -= 1 while i.positive? && current[i - 1].match?(/\S/)
      current.slice!(i, @cursor - i)
      @cursor = i
      @text = current.join
      :changed
    end

    def word_left
      current = units
      i = @cursor
      i -= 1 while i.positive? && current[i - 1].match?(/\s/)
      i -= 1 while i.positive? && current[i - 1].match?(/\S/)
      @cursor = i
      :handled
    end

    def word_right
      current = units
      i = @cursor
      i += 1 while i < current.length && current[i].match?(/\S/)
      i += 1 while i < current.length && current[i].match?(/\s/)
      @cursor = i
      :handled
    end
  end
end
