# frozen_string_literal: true

require_relative "ansi"
require_relative "theme"

module Tui
  # Reusable state for the persistent panel on the right side of the TUI.
  # Content builders own what the panel says; Frame owns the split-pane drawing.
  # Replacing content for the same identity preserves the reader's scroll, while
  # moving to another task starts the new content at the top.
  class RightPanel
    A = Ansi
    T = Theme

    attr_reader :title, :kind, :identity, :lines, :scroll, :focused_row

    def initialize(title:, lines:, kind:, identity: nil, focused_row: nil)
      @title = title
      @lines = lines
      @kind = kind
      @identity = identity
      @scroll = 0
      @focused_row = focused_row
    end

    def replace(title:, lines:, identity: @identity, focused_row: nil)
      @scroll = 0 if identity != @identity
      @title = title
      @lines = lines
      @identity = identity
      @focused_row = focused_row
      self
    end

    # The title and divider consume two body rows. A status row appears only
    # when the content overflows, and is included in the returned line budget.
    def view(height:, width:)
      budget = [height - 2, 0].max
      viewport = content_viewport(budget)
      reveal_focused_row(viewport) if @focused_row
      @scroll = @scroll.clamp(0, [@lines.size - viewport, 0].max)
      shown = @lines[@scroll, viewport] || []
      shown = shown.map { |line| A.vtrunc(line, width) }
      shown << A.vtrunc(status_line(shown.size), width) if status?(budget)
      { title: @title, lines: shown, width: width }
    end

    def scroll_line(delta, height) = scroll_by(delta, height)
    def scroll_half(dir, height) = scroll_by(dir * [viewport(height) / 2, 1].max, height)
    def scroll_page(dir, height) = scroll_by(dir * viewport(height), height)

    def viewport(height)
      content_viewport([height - 2, 0].max)
    end

    private

    def reveal_focused_row(viewport)
      return if viewport <= 0

      row = @focused_row.clamp(0, [@lines.size - 1, 0].max)
      @scroll = row if row < @scroll
      @scroll = row - viewport + 1 if row >= @scroll + viewport
    end

    def content_viewport(budget)
      [budget - (status?(budget) ? 1 : 0), 0].max
    end

    def status?(budget)
      budget.positive? && @lines.size > budget
    end

    def scroll_by(delta, height)
      vp = viewport(height)
      @scroll = (@scroll + delta).clamp(0, [@lines.size - vp, 0].max)
    end

    def status_line(shown)
      last = [@scroll + shown, @lines.size].min
      T.paint(:muted, "#{last}/#{@lines.size} · ctrl-u/d scroll")
    end
  end
end
