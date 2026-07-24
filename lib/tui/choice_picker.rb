# frozen_string_literal: true

require "set"
require_relative "ansi"
require_relative "border"
require_relative "theme"
require_relative "text_input"

module Tui
  # Stable, searchable single- or multiple-choice overlay. It owns only picker
  # interaction state; callers provide domain options and apply accepted ids.
  class ChoicePicker
    A = Ansi
    T = Theme

    Option = Struct.new(:id, :label, :search_text, :kind, :metadata, keyword_init: true) do
      def initialize(**values)
        super
        self.search_text ||= label
        self.kind ||= :choice
        freeze
      end
    end

    attr_reader :title, :input, :all_options, :selection_mode, :cursor_index,
                :viewport_start, :staged_selection, :error

    def initialize(title:, options:, selection: [], selection_mode: :single,
                   accept_label: nil, empty_label: "no matching choices",
                   max_visible: 8, toggle_command: nil, preferred_id: nil,
                   search_normalizer: nil, selected_style: nil)
      raise ArgumentError, "selection_mode must be :single or :multiple" unless %i[single multiple].include?(selection_mode)

      @title = title.to_s
      @all_options = normalize_options(options)
      @selection_mode = selection_mode
      @accept_label = accept_label || (selection_mode == :multiple ? "apply" : "choose")
      @empty_label = empty_label.to_s
      @max_visible = [max_visible.to_i, 1].max
      @toggle_command = toggle_command
      @search_normalizer = search_normalizer || ->(value) { value }
      @selected_style = selected_style
      @input = TextInput.new
      @staged_selection = normalize_selection(selection)
      @initial_selection = @staged_selection.dup.freeze
      @cursor_index = initial_cursor(preferred_id)
      @viewport_start = 0
      @error = nil
      @natural_width = natural_width
      @result_capacity = result_capacity
    end

    def options = @all_options

    def results
      query = normalized_query
      return @all_options if query.empty?

      @all_options.each_with_index.filter_map do |option, index|
        rank = option_rank(option, query)
        [rank, index, option] if rank
      end.sort_by { |rank, index, _option| [rank, index] }.map(&:last)
    end

    def current = results[@cursor_index]
    def cursor_id = current&.id
    def selected?(id) = @staged_selection.include?(id)
    def selection_changed? = @staged_selection != @initial_selection

    def handle_key(key)
      case key
      when "\e"           then :cancelled
      when "\r", "\n"     then accept_current
      when "\e[A", "\x10" then move(-1)
      when "\e[B", "\x0e" then move(1)
      when " "
        return toggle_current if @selection_mode == :multiple

        edit_input(key)
      else
        edit_input(key)
      end
    end

    def paste(text) = edit_input(text, paste: true)

    def fail!(message)
      @error = message.to_s
      @natural_width = [@natural_width, natural_width].max
      self
    end

    def refresh_options(options:, selection: @staged_selection)
      query = @input.to_s
      previous_cursor = cursor_id
      @all_options = normalize_options(options)
      @staged_selection = normalize_selection(selection)
      @initial_selection = normalize_selection(@initial_selection).freeze
      @input.replace(query)
      matches = results
      @cursor_index = matches.index { |option| option.id == previous_cursor } ||
                      initial_cursor(previous_cursor, matches: matches)
      @viewport_start = clamp_viewport(@viewport_start, matches.size)
      @natural_width = [@natural_width, natural_width].max
      @result_capacity = [@result_capacity, result_capacity].max
      self
    end

    def popup(row:, col:, max_width:, max_height:, inline_input:)
      matches = results
      clamp_cursor!(matches.size)
      ensure_cursor_visible(matches.size)

      width = [[@natural_width, max_width].min, 1].max
      preferred_height = @result_capacity + 5
      height = [[preferred_height, max_height].min, 1].max
      query = " search: #{inline_input.call(@input)}"
      hint = status_hint

      if width < 6 || height < 3
        compact_selected = current ? compact_line(current) : T.paint(:muted, @empty_label)
        compact = [compact_selected, query, " #{hint}"].first(height)
        lines = compact.map { |line| A.vpad(A.vtrunc(line, width), width) }
        return { lines: lines, row: row, col: col }
      end

      inner_width = width - 2
      if height < 6
        selected_line = current ? option_line(current, cursor: true) : T.paint(:muted, "   #{@empty_label}")
        inner = [selected_line, query, " #{hint}"].first([height - 2, 0].max)
        title = T.paint(:modal_title, A.vtrunc(" #{@title} ", width - 4))
        lines = Border.box(
          inner_lines: inner.map { |line| " #{A.vpad(A.vtrunc(line, width - 4), width - 4)} " },
          inner_width: inner_width,
          gradient: T.gradient(:border), solid: T.sgr(:border),
          title: title, title_lead: 1,
        )
        return { lines: lines, row: row, col: col }
      end

      result_slots = [height - 5, 0].max
      ensure_cursor_visible(matches.size, capacity: result_slots)
      visible = matches.slice(@viewport_start, result_slots) || []
      option_lines = visible.each_with_index.map do |option, offset|
        option_line(option, cursor: @viewport_start + offset == @cursor_index)
      end
      option_lines << T.paint(:muted, "   #{@empty_label}") if matches.empty? && result_slots.positive?
      option_lines.fill("", option_lines.length...result_slots)

      inner = [query, "", *option_lines, " #{hint}"]
      title = T.paint(:modal_title, A.vtrunc(" #{@title} ", width - 4))
      lines = Border.box(
        inner_lines: inner.map { |line| " #{A.vpad(A.vtrunc(line, width - 4), width - 4)} " },
        inner_width: inner_width,
        gradient: T.gradient(:border), solid: T.sgr(:border),
        title: title, title_lead: 1,
      )
      { lines: lines, row: row, col: col }
    end

    private

    def normalize_options(options)
      seen = Set.new
      Array(options).each_with_object([]) do |option, normalized|
        option = Option.new(**option) if option.is_a?(Hash)
        raise ArgumentError, "choice options need stable ids" if option.id.nil?
        next unless seen.add?(option.id)

        normalized << option
      end.freeze
    end

    def normalize_selection(selection)
      valid = @all_options.select { |option| option.kind == :choice }.map(&:id).to_set
      Set.new(Array(selection)) & valid
    end

    def initial_cursor(preferred_id = nil, matches: results)
      [preferred_id, *@staged_selection.to_a].compact.each do |id|
        index = matches.index { |option| option.id == id }
        return index if index
      end
      0
    end

    def normalized_query
      normalize_search(@input.strip.downcase)
    end

    def searchable_values(option)
      Array(option.search_text).map { |value| normalize_search(value.to_s.downcase) }
    end

    def normalize_search(value)
      @search_normalizer.call(value)
    end

    def option_rank(option, query)
      searchable_values(option).filter_map do |value|
        if value == query
          0
        elsif value.start_with?(query)
          1
        elsif value.split(/[^[:alnum:]@_-]+/).any? { |token| token.start_with?(query) }
          2
        elsif value.include?(query)
          3
        end
      end.min
    end

    def edit_input(value, paste: false)
      result = paste ? @input.insert(value) : @input.handle_key(value)
      if result == :changed
        @cursor_index = 0
        @viewport_start = 0
        @error = nil
        :changed
      else
        :handled
      end
    end

    def accept_current
      option = current
      return :handled unless option

      if option.kind == :command
        apply_command(option)
        [:accepted, @staged_selection.to_a]
      elsif @selection_mode == :single
        [:accepted, [option.id]]
      else
        [:accepted, @staged_selection.to_a]
      end
    end

    def toggle_current
      option = current
      return :handled unless option

      if option.kind == :command
        apply_command(option)
      elsif @staged_selection.delete?(option.id)
        # deleted
      else
        @staged_selection.add(option.id)
      end
      @error = nil
      :changed
    end

    def apply_command(option)
      replacement = @toggle_command&.call(option, @staged_selection.dup)
      @staged_selection = normalize_selection(replacement) if replacement
    end

    def move(delta)
      count = results.size
      @cursor_index = count.zero? ? 0 : (@cursor_index + delta).clamp(0, count - 1)
      ensure_cursor_visible(count)
      :changed
    end

    def clamp_cursor!(count)
      @cursor_index = count.zero? ? 0 : @cursor_index.clamp(0, count - 1)
    end

    def clamp_viewport(start, count, capacity: @result_capacity)
      max_start = [count - capacity, 0].max
      start.clamp(0, max_start)
    end

    def ensure_cursor_visible(count, capacity: @result_capacity)
      return @viewport_start = 0 if count.zero?

      @viewport_start = @cursor_index if @cursor_index < @viewport_start
      bottom = @viewport_start + capacity - 1
      @viewport_start = @cursor_index - capacity + 1 if @cursor_index > bottom
      @viewport_start = clamp_viewport(@viewport_start, count, capacity: capacity)
    end

    def option_line(option, cursor:)
      marker = cursor ? T.paint(:selection, "❯") : " "
      check = option.kind == :choice && selected?(option.id) ? "●" : " "
      label = if option.kind == :choice && selected?(option.id) && @selected_style
                T.paint(@selected_style, option.label)
              else
                option.label
              end
      " #{marker} #{check} #{label}"
    end

    def compact_line(option)
      check = option.kind == :choice && selected?(option.id) ? "● " : ""
      "❯ #{check}#{option.label}"
    end

    def result_capacity
      [[@all_options.size, 1].max, @max_visible].min
    end

    def natural_width
      option_width = @all_options.map { |option| A.vislen(option.label) + 8 }.max || 0
      hint_width = A.vislen(status_hint) + 4
      [[option_width, hint_width, A.vislen(@title) + 8].max + 2, 40].max
    end

    def status_hint
      return T.paint(:error, @error) if @error

      instructions = if @selection_mode == :multiple
                       "↑↓ move · space toggle · enter #{@accept_label} · esc cancel"
                     else
                       "↑↓ choose · enter #{@accept_label} · esc cancel"
                     end
      prefix = @selection_mode == :multiple ? "#{@staged_selection.size} selected · " : ""
      T.paint(:muted, "#{prefix}#{instructions}")
    end
  end
end
