# frozen_string_literal: true

require_relative "ansi"
require_relative "theme"
require_relative "text_input"

module Tui
  # Reusable lifecycle for the TUI's small, single-field popup forms. The
  # caller supplies domain validation/mutation and owns post-submit effects;
  # Form owns input editing, paste, errors, rendering, and the return mode.
  class Form
    A = Ansi
    T = Theme

    attr_reader :kind, :input, :error, :return_mode, :target_id

    def initialize(kind:, title:, prompt:, hint:, min_width:, return_mode:,
                   initial: +"", suffix: nil, target_id: nil, &submit)
      @kind = kind
      @title = title
      @prompt = prompt
      @hint = hint
      @min_width = min_width
      @return_mode = return_mode
      @target_id = target_id
      @suffix = suffix
      @submit = submit || raise(ArgumentError, "form submit callback required")
      @input = TextInput.new(initial)
      @error = nil
    end

    def handle_key(key)
      case key
      when "\e"       then :cancelled
      when "\r", "\n" then submit
      else
        changed = @input.handle_key(key) == :changed
        @error = nil if changed
        changed ? :changed : :handled
      end
    end

    def paste(text)
      changed = @input.insert(text) == :changed
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

    def popup(row:, col:, inline_input:)
      suffix = @suffix.to_s
      suffix = "  #{T.paint(:muted, suffix)}" unless suffix.empty?
      hint = @error || @hint
      inner = [
        " #{@prompt}: #{inline_input.call(@input)}#{suffix}",
        " #{@error ? T.paint(:error, hint) : T.paint(:muted, hint)}",
      ]
      width = [inner.map { |line| A.vislen(line) }.max + 2, @min_width].max
      title_width = A.vislen(@title) + 4
      lines = ["┌ #{@title} #{"─" * [width - title_width, 0].max}┐"]
      inner.each { |line| lines << "│#{A.vpad(line, width - 2)}│" }
      lines << "└#{"─" * (width - 2)}┘"
      { lines: lines, row: row, col: col }
    end
  end
end
