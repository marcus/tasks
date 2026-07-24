# frozen_string_literal: true

require_relative "choice_picker"

module Tui
  # Searchable projection of context-available actions from Shortcuts. It owns
  # only query/selection state; App executes the selected registry entry.
  class ActionPalette
    MAX_RESULTS = 8

    attr_reader :entries, :return_mode, :target_id

    def initialize(entries:, return_mode:, target_id: nil)
      @entries = entries.freeze
      @return_mode = return_mode
      @target_id = target_id
      options = @entries.map do |entry|
        ChoicePicker::Option.new(
          id: entry.handler,
          label: "#{entry.description}  #{Theme.paint(:muted, entry.display_key)}",
          search_text: [entry.description, entry.display_key, entry.handler.to_s],
          metadata: entry,
        )
      end
      @picker = ChoicePicker.new(
        title: "actions", options: options, selection_mode: :single,
        accept_label: "run", empty_label: "no matching actions",
        max_visible: MAX_RESULTS,
      )
    end

    def input = @picker.input
    def selected = @picker.cursor_index
    def error = @picker.error
    def results = @picker.results.map(&:metadata)
    def current = @picker.current&.metadata

    def handle_key(key)
      result = @picker.handle_key(key)
      return result unless result.is_a?(Array) && result.first == :accepted

      entry = @entries.find { |candidate| candidate.handler == result.last.first }
      entry ? [:execute, entry] : :handled
    end

    def paste(text) = @picker.paste(text)
    def fail!(message) = @picker.fail!(message)

    def popup(row:, col:, max_width:, max_height:, inline_input:)
      @picker.popup(
        row: row, col: col, max_width: max_width, max_height: max_height,
        inline_input: inline_input,
      )
    end
  end
end
