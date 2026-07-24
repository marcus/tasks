# frozen_string_literal: true

require_relative "choice_picker"

module Tui
  # Multiple-choice adapter for the TUI's global @context filters. ChoicePicker
  # owns interaction/rendering; App owns applying the accepted context set.
  class ContextPalette
    MAX_RESULTS = 8
    CLEAR_ID = :__clear_contexts__
    CLEAR_LABEL = "Clear all contexts"

    attr_reader :current_filters

    def initialize(contexts:, current: nil, current_filters: nil)
      @current_filters = normalize_many(current_filters || current)
      @picker = build_picker(contexts)
    end

    def input = @picker.input
    def options = @picker.options
    def selected = @picker.cursor_index
    def staged_selection = @picker.staged_selection
    def results = @picker.results
    def current = @picker.current
    def current_filter = @current_filters.first

    def handle_key(key)
      cursor = current
      query_present = !input.strip.empty?
      selection_changed = @picker.selection_changed?
      result = @picker.handle_key(key)
      return result unless result.is_a?(Array) && result.first == :accepted

      if query_present && !selection_changed && cursor&.kind == :choice
        return [:apply, [cursor.id]]
      end
      [:apply, result.last]
    end

    def paste(text) = @picker.paste(text)

    def refresh_options(contexts:, current: nil, current_filters: nil)
      @current_filters = normalize_many(current_filters || current || @current_filters)
      @picker.refresh_options(
        options: build_options(contexts),
        selection: @picker.staged_selection,
      )
      self
    end

    def popup(row:, col:, max_width:, max_height:, inline_input:)
      @picker.popup(
        row: row, col: col, max_width: max_width, max_height: max_height,
        inline_input: inline_input,
      )
    end

    def self.normalize(value)
      token = value.to_s.strip
      return nil if token.empty?

      token.start_with?("@") ? token : "@#{token}"
    end

    private

    def normalize(value) = self.class.normalize(value)

    def normalize_many(values)
      Array(values).filter_map { |value| normalize(value) }.uniq
    end

    def build_picker(contexts)
      ChoicePicker.new(
        title: "contexts",
        options: build_options(contexts),
        selection: @current_filters,
        selection_mode: :multiple,
        accept_label: "apply",
        empty_label: "no matching contexts",
        max_visible: MAX_RESULTS,
        preferred_id: @current_filters.first,
        search_normalizer: ->(value) { value.sub(/\A@/, "") },
        selected_style: :context_filter_active,
        toggle_command: ->(option, selection) {
          option.id == CLEAR_ID ? selection.clear : selection
        },
      )
    end

    def build_options(contexts)
      clear = ChoicePicker::Option.new(id: CLEAR_ID, label: CLEAR_LABEL, kind: :command)
      listed = Array(contexts).filter_map { |value| normalize(value) }.uniq.sort
      [clear, *listed.map { |ctx| ChoicePicker::Option.new(id: ctx, label: ctx) }]
    end
  end
end
