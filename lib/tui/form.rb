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

    def popup(row:, col:, inline_input:, max_width: nil, max_height: nil)
      suffix = @suffix.to_s
      suffix = "  #{T.paint(:muted, suffix)}" unless suffix.empty?
      hint = @error || @hint
      inner = [
        " #{@prompt}: #{inline_input.call(@input)}#{suffix}",
        " #{@error ? T.paint(:error, hint) : T.paint(:muted, hint)}",
      ]
      natural_width = [inner.map { |line| A.vislen(line) }.max + 2, @min_width].max
      width = [natural_width, max_width || natural_width].min
      height = [4, max_height || 4].min
      width = [width, 1].max
      height = [height, 1].max

      if width < 6 || height < 3
        label = @prompt.to_s.strip.empty? ? @title.to_s : @prompt.to_s
        compact = if @error
                    "#{label}: #{@error}"
                  elsif @input.to_s.empty?
                    rendered = inline_input.call(@input)
                    if A.vislen(label) + 1 + A.vislen(rendered) <= width
                      "#{label} #{rendered}"
                    else
                      A.cell_slice(label, 0, width)
                    end
                  else
                    inline_input.call(@input)
                  end
        lines = [A.vpad(A.vtrunc(compact, width), width)]
        return { lines: lines.first(height), row: row, col: col }
      end

      inner_width = width - 2
      title = A.vtrunc(" #{@title} ", inner_width)
      lines = ["┌#{title}#{"─" * (inner_width - A.vislen(title))}┐"]
      inner.first(height - 2).each do |line|
        lines << "│#{A.vpad(A.vtrunc(line, inner_width), inner_width)}│"
      end
      lines << "└#{"─" * (width - 2)}┘"
      { lines: lines, row: row, col: col }
    end
  end
end
