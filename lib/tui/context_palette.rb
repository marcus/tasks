# frozen_string_literal: true

require_relative "ansi"
require_relative "theme"
require_relative "text_input"

module Tui
  # Searchable picker of GTD @contexts for the global list filter. Mirrors
  # ActionPalette's type-to-filter + arrow selection contract; App owns apply
  # and clear. The first option is always "Clear context filter".
  class ContextPalette
    A = Ansi
    T = Theme
    MAX_RESULTS = 8
    CLEAR_ID = nil
    CLEAR_LABEL = "Clear context filter"

    Option = Struct.new(:id, :label, keyword_init: true)

    attr_reader :input, :options, :selected, :current_filter

    def initialize(contexts:, current: nil)
      @current_filter = normalize(current)
      @options = build_options(contexts).freeze
      @input = TextInput.new
      @selected = initial_selection
    end

    def results
      query = @input.strip.downcase
      return @options if query.empty?

      @options.select { |option| option.label.downcase.include?(query) }
    end

    def current = results[@selected]

    def handle_key(key)
      case key
      when "\e"           then :cancelled
      when "\r", "\n"     then current ? [:apply, current] : :handled
      when "\e[A", "\x10" then move(-1)
      when "\e[B", "\x0e" then move(1)
      else
        if @input.handle_key(key) == :changed
          @selected = 0
          :changed
        else
          :handled
        end
      end
    end

    def paste(text)
      if @input.insert(text) == :changed
        @selected = 0
        :changed
      else
        :handled
      end
    end

    def refresh_options(contexts:, current: @current_filter)
      query = @input.to_s
      # Hold the Option, not its id — the Clear row's id is nil, which a bare
      # id check can't tell apart from "nothing selected".
      selected_option = self.current
      @current_filter = normalize(current)
      @options = build_options(contexts).freeze
      @input.replace(query)
      matches = results
      idx = selected_option && matches.index { |option| option.id == selected_option.id }
      @selected = idx || [[initial_selection, matches.size - 1].min, 0].max
      self
    end

    def popup(row:, col:, max_width:, max_height:, inline_input:)
      matches = results
      @selected = [[@selected, 0].max, [matches.size - 1, 0].max].min
      first = [[@selected - MAX_RESULTS + 1, 0].max, [matches.size - MAX_RESULTS, 0].max].min
      visible = matches.slice(first, MAX_RESULTS) || []
      content = visible.each_with_index.map do |option, index|
        marker = first + index == @selected ? "❯" : " "
        label = if option.id && option.id == @current_filter
                  T.paint(:context_filter_active, "#{option.label} · active")
                else
                  option.label
                end
        " #{marker} #{label}"
      end
      content = [T.paint(:muted, "   no matching contexts")] if content.empty?
      selected_option = matches[@selected]
      selected_line = content.find { |line| line.include?("❯") } || content.first
      compact_selected = if selected_option
                           label = if selected_option.id && selected_option.id == @current_filter
                                     T.paint(:context_filter_active, selected_option.label)
                                   else
                                     selected_option.label
                                   end
                           "❯ #{label}"
                         else
                           T.paint(:muted, "no matches")
                         end

      query = " search: #{inline_input.call(@input)}"
      hint = T.paint(:muted, "↑↓ choose · enter apply · esc cancel")
      inner = [query, *content, " #{hint}"]
      natural_width = [inner.map { |line| A.vislen(line) }.max + 2, 40].max
      width = [[natural_width, max_width].min, 1].max
      height = [[inner.size + 2, max_height].min, 1].max

      if width < 6 || height < 3
        compact = [compact_selected, query, " #{hint}"].first(height)
        lines = compact.map { |line| A.vpad(A.vtrunc(line, width), width) }
        return { lines: lines, row: row, col: col }
      end

      slots = height - 2
      other = content.reject { |line| line.equal?(selected_line) }
      visible_inner = [selected_line, query, *other, " #{hint}"].first(slots)
      # Match Frame's shortcuts-modal chrome: ┌─ title ─────┐ with :modal_title.
      title = A.vtrunc(" context ", width - 4)
      lines = ["┌─#{T.paint(:modal_title, title)}#{"─" * [width - 4 - A.vislen(title), 0].max}─┐"]
      visible_inner.compact.each do |line|
        lines << "│ #{A.vpad(A.vtrunc(line, width - 4), width - 4)} │"
      end
      lines << "└#{"─" * (width - 2)}┘"
      { lines: lines, row: row, col: col }
    end

    def self.normalize(value)
      token = value.to_s.strip
      return nil if token.empty?

      token.start_with?("@") ? token : "@#{token}"
    end

    private

    def normalize(value) = self.class.normalize(value)

    def build_options(contexts)
      clear = Option.new(id: CLEAR_ID, label: CLEAR_LABEL)
      listed = Array(contexts).filter_map { |value| normalize(value) }.uniq.sort
      [clear, *listed.map { |ctx| Option.new(id: ctx, label: ctx) }]
    end

    def initial_selection
      return 0 unless @current_filter

      idx = @options.index { |option| option.id == @current_filter }
      idx || 0
    end

    def move(delta)
      count = results.size
      @selected = count.zero? ? 0 : (@selected + delta).clamp(0, count - 1)
      :handled
    end
  end
end
