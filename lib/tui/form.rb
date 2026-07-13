# frozen_string_literal: true

require_relative "ansi"
require_relative "theme"
require_relative "form_renderer"
require_relative "../term_form"

module Tui
  # Reusable lifecycle for the TUI's small, single-field popup forms. The
  # caller supplies domain validation/mutation and owns post-submit effects;
  # Form owns input editing, paste, errors, rendering, and the return mode.
  class Form
    A = Ansi
    T = Theme

    attr_reader :kind, :input, :error, :return_mode, :target_id, :field, :engine

    def initialize(kind:, title:, prompt:, hint:, min_width:, return_mode:,
                   initial: +"", suffix: nil, target_id: nil, field: nil, renderer: FormRenderer.new, &submit)
      @kind = kind
      @title = title
      @prompt = prompt
      @hint = hint
      @min_width = min_width
      @return_mode = return_mode
      @target_id = target_id
      @suffix = suffix
      @submit = submit || raise(ArgumentError, "form submit callback required")
      @field = field || TermForm::Fields::Input.new(key: :value, value: initial, label: prompt)
      @engine = TermForm::Form.new(
        groups: [TermForm::Group.new(key: :quick, label: "", fields: [@field])],
        focus: @field.key,
      )
      @input = InputProxy.new(@engine, @field)
      @renderer = renderer
      @error = nil
    end

    def handle_key(key)
      case key
      when "\e"       then :cancelled
      when "\r", "\n" then submit
      else
        changed = @engine.handle(key).changed?
        @error = nil if changed
        changed ? :changed : :handled
      end
    end

    def paste(text)
      changed = @engine.handle(TermForm::Event.paste(text)).changed?
      @error = nil if changed
      changed ? :changed : :handled
    end

    def submit
      message = @submit.call(@input.to_s)
      if message
        @error = message.to_s
        :error
      else
        :submitted
      end
    rescue StandardError => e
      @error = e.message.empty? ? e.class.name : e.message
      :error
    end

    def popup(row:, col:, inline_input:, max_width: nil, max_height: nil)
      # inline_input remains accepted for compatibility with existing popup
      # hosts; cursor rendering now comes from TermForm's semantic cursor.
      inline_input
      natural_width = [A.vislen("#{@prompt} #{@input} #{@suffix}") + 10, @min_width].max
      width = [natural_width, max_width || natural_width].min
      height = [4, max_height || 4].min
      width = [width, 0].max
      height = [height, 0].max
      rendered = @renderer.render(
        model: @engine.render_model, width: width, height: height,
        title: @title, hint: @hint, error: @error, suffix: @suffix,
      )
      { lines: rendered.lines, focused_content_row: rendered.focused_content_row, row: row, col: col }
    end

    # Keeps the old single-input API available while TermForm owns the actual
    # editor and baseline. Direct test/host replacement therefore participates
    # in dirty-state rendering instead of mutating a second buffer.
    class InputProxy
      def initialize(engine, field)
        @engine = engine
        @field = field
      end

      def text = @field.text
      def to_s = text
      def to_str = text
      def cursor = @field.cursor
      def empty? = text.empty?
      def strip = text.strip
      def ==(other) = text == other.to_s

      def replace(value)
        @engine.set_value(@field.key, value)
        self
      end
    end
  end
end
