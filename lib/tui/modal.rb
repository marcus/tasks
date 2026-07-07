# frozen_string_literal: true

require_relative "ansi"
require_relative "theme"

module Tui
  # State and geometry for one open modal overlay: content, scroll position,
  # and an optional live line filter. Pure — App owns input dispatch, Frame
  # owns drawing. Two invariants the box keeps for itself:
  #
  #   * width comes from the FULL content (title included), so scrolling or
  #     filtering never resizes the box
  #   * view(body_h) never yields more rows than fit: borders take 2 rows of
  #     the body, the status line one more when shown
  class Modal
    A = Ansi
    T = Theme

    MIN_INNER = 3 # inner rows kept even in degenerate bodies

    attr_reader :title, :kind, :scroll, :filter

    def initialize(title:, lines:, kind:, filterable: false)
      @title = title
      @all = lines
      @kind = kind
      @filterable = filterable
      @scroll = 0
      @filter = nil
    end

    def filterable? = @filterable

    def filter=(query)
      query = query.to_s
      @filter = query.strip.empty? ? nil : query
      @scroll = 0
    end

    # Lines after the filter (matched case-insensitively on the visible
    # text); the full content when no filter is active.
    def lines
      return @all unless @filter
      q = @filter.downcase
      @all.select { |l| A.strip(l).downcase.include?(q) }
    end

    # Box width from the full, unfiltered content. Frame clamps to the frame.
    def width
      @width ||= [(@all.map { |l| A.vislen(l) }.max || 0),
                  A.vislen(@title) + 6, 30].max + 4
    end

    # Content rows visible at once inside a body of body_h rows.
    def viewport(body_h)
      inner = inner_budget(body_h)
      status?(inner) ? inner - 1 : inner
    end

    def scroll_line(delta, body_h) = scroll_by(delta, body_h)
    def scroll_half(dir, body_h)   = scroll_by(dir * [viewport(body_h) / 2, 1].max, body_h)
    def scroll_page(dir, body_h)   = scroll_by(dir * viewport(body_h), body_h)

    # The window Frame draws: { title:, lines:, width: }, at most
    # body_h - 2 lines so the boxed result fits the body.
    def view(body_h)
      ls = lines
      vp = viewport(body_h)
      @scroll = @scroll.clamp(0, [ls.size - vp, 0].max)
      shown = ls[@scroll, vp] || []
      out = shown.empty? && @filter ? [T.paint(:muted, "no lines match “#{@filter}”")] : shown.dup
      out << status_line(ls, shown.size) if status?(inner_budget(body_h))
      { title: @title, lines: out, width: width }
    end

    private

    def inner_budget(body_h) = [body_h - 2, MIN_INNER].max

    def scroll_by(delta, body_h)
      @scroll = (@scroll + delta).clamp(0, [lines.size - viewport(body_h), 0].max)
    end

    # A status line appears while filtering or when the content overflows.
    def status?(inner)
      !@filter.nil? || lines.size > inner
    end

    def status_line(ls, shown)
      parts = []
      parts << "/ #{@filter}" if @filter
      parts << "#{[@scroll + shown, ls.size].min}/#{ls.size}"
      parts << "↑↓ scroll" if ls.size > shown
      T.paint(:muted, "── #{parts.join(" · ")} ──")
    end
  end
end
