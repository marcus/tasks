# frozen_string_literal: true

require_relative "ansi"
require_relative "theme"
require_relative "text_input"

module Tui
  # Searchable projection of context-available actions from Shortcuts. It owns
  # only query/selection state; App executes the selected registry entry.
  class ActionPalette
    A = Ansi
    T = Theme
    MAX_RESULTS = 8

    attr_reader :input, :entries, :return_mode, :target_id, :selected, :error

    def initialize(entries:, return_mode:, target_id: nil)
      @entries = entries.freeze
      @return_mode = return_mode
      @target_id = target_id
      @input = TextInput.new
      @selected = 0
      @error = nil
    end

    def results
      query = @input.strip.downcase
      return @entries if query.empty?

      @entries.select do |entry|
        [entry.description, entry.display_key, entry.handler.to_s]
          .any? { |value| value.downcase.include?(query) }
      end
    end

    def current = results[@selected]

    def handle_key(key)
      case key
      when "\e"           then :cancelled
      when "\r", "\n"     then current ? [:execute, current] : :handled
      when "\e[A", "\x10" then move(-1)
      when "\e[B", "\x0e" then move(1)
      else
        if @input.handle_key(key) == :changed
          @selected = 0
          @error = nil
          :changed
        else
          :handled
        end
      end
    end

    def paste(text)
      if @input.insert(text) == :changed
        @selected = 0
        @error = nil
        :changed
      else
        :handled
      end
    end

    def fail!(message)
      @error = message.to_s
    end

    def popup(row:, cols:, inline_input:)
      matches = results
      @selected = [[@selected, 0].max, [matches.size - 1, 0].max].min
      first = [[@selected - MAX_RESULTS + 1, 0].max, [matches.size - MAX_RESULTS, 0].max].min
      visible = matches.slice(first, MAX_RESULTS) || []
      content = visible.each_with_index.map do |entry, index|
        marker = first + index == @selected ? "❯" : " "
        key = entry.display_key
        " #{marker} #{entry.description}  #{T.paint(:muted, key)}"
      end
      content = [T.paint(:muted, "   no matching actions")] if content.empty?

      query = " search: #{inline_input.call(@input)}"
      hint = @error ? T.paint(:error, @error) : T.paint(:muted, "↑↓ choose · enter run · esc cancel")
      inner = [query, *content, " #{hint}"]
      width = [inner.map { |line| A.vislen(line) }.max + 2, 46].max
      width = [width, [cols - 6, 24].max].min
      title = " actions "
      lines = ["┌#{title}#{"─" * [width - A.vislen(title) - 2, 0].max}┐"]
      inner.each { |line| lines << "│#{A.vpad(A.vtrunc(line, width - 2), width - 2)}│" }
      lines << "└#{"─" * (width - 2)}┘"
      { lines: lines, row: row, col: 3 }
    end

    private

    def move(delta)
      count = results.size
      @selected = count.zero? ? 0 : (@selected + delta).clamp(0, count - 1)
      :handled
    end
  end
end
