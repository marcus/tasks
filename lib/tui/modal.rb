# frozen_string_literal: true

require_relative "ansi"
require_relative "theme"

module Tui
  # State and geometry for one open modal overlay: content, scroll position,
  # and an optional live line filter. Pure — App owns input dispatch, Frame
  # owns drawing. Three invariants the box keeps for itself:
  #
  #   * width comes from the FULL content (title included), so scrolling or
  #     filtering never resizes the box
  #   * view(body_h) never yields more rows than fit: borders take 2 rows of
  #     the body, the status line one more when shown
  #   * while a filter is active the box keeps its full unfiltered height, so
  #     neither the box nor its centered position jumps as matches come and go;
  #     the `/` input renders on a filter line pinned inside the box, not in the
  #     app's prompt area
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
      @filtered = nil # memoized matches for @filter
      @haystack = nil # lazy stripped/downcased @all for fast matching
    end

    def filterable? = @filterable

    def filter=(query)
      query = query.to_s
      normalized = query.strip.empty? ? nil : query
      return if normalized == @filter # an unchanged query keeps the memo + scroll

      @filter = normalized
      @filtered = nil
      @scroll = 0
    end

    # Lines after the filter (matched case-insensitively on the visible text);
    # the full content when no filter is active. The stripped/downcased haystack
    # is built once from the immutable content and the match set is memoized per
    # query, so a keystroke costs one substring scan of pre-stripped text — not a
    # fresh ANSI-strip of every line on each of view's internal calls.
    def lines
      return @all unless @filter

      @filtered ||= begin
        q = @filter.downcase
        @all.each_index.select { |i| haystack[i].include?(q) }.map { |i| @all[i] }
      end
    end

    # Stripped, downcased view of the immutable content, built once and reused
    # for every keystroke's match scan.
    def haystack
      @haystack ||= @all.map { |line| A.strip(line).downcase }
    end

    # Box width from the full, unfiltered content. Frame clamps to the frame.
    def width
      @width ||= [(@all.map { |l| A.vislen(l) }.max || 0),
                  A.vislen(@title) + 6, 30].max + 4
    end

    # Content rows visible at once inside a body of body_h rows. A filter pins
    # the query line to the top and the count status to the bottom, so two fewer
    # content rows are available while one is active.
    def viewport(body_h)
      return [locked_rows(body_h) - 2, 0].max if @filter

      inner = inner_budget(body_h)
      status?(inner) ? inner - 1 : inner
    end

    def scroll_line(delta, body_h) = scroll_by(delta, body_h)
    def scroll_half(dir, body_h)   = scroll_by(dir * [viewport(body_h) / 2, 1].max, body_h)
    def scroll_page(dir, body_h)   = scroll_by(dir * viewport(body_h), body_h)

    # The window Frame draws: { title:, lines:, width: }, at most body_h - 2
    # lines so the boxed result fits the body. `filter_line` is the rendered
    # filter input (App owns the cursor styling); when a filter is active — being
    # typed or committed — it pins to the top of the box and the box keeps its
    # full unfiltered height.
    def view(body_h, filter_line: nil)
      return filtered_view(body_h, filter_line) if filter_line || @filter

      ls = lines
      vp = viewport(body_h)
      @scroll = @scroll.clamp(0, [ls.size - vp, 0].max)
      shown = ls[@scroll, vp] || []
      out = shown.dup
      out << status_line(ls, shown.size) if status?(inner_budget(body_h))
      { title: @title, lines: out, width: width }
    end

    private

    # A fixed-height view for an active filter: the query line on top, matched
    # (scrolled) content padded to the retained height, and the count status at
    # the bottom. The row count equals what the unfiltered box shows, so the box
    # size and centered position stay put across keystrokes.
    def filtered_view(body_h, filter_line)
      ls = lines
      rows = locked_rows(body_h)
      # In a degenerate short modal the status row would crowd out the content
      # itself (and overflow the locked height) — drop it before dropping matches.
      status = rows > 2
      content_vp = [rows - 1 - (status ? 1 : 0), 0].max
      @scroll = @scroll.clamp(0, [ls.size - content_vp, 0].max)
      shown = ls[@scroll, content_vp] || []
      placeholder = shown.empty? && @filter && content_vp.positive?
      content = placeholder ? [T.paint(:muted, "no lines match “#{@filter}”")] : shown.dup
      content << "" while content.size < content_vp
      out = [filter_line || T.paint(:prompt, "/ #{@filter}"), *content]
      out << status_line(ls, shown.size) if status
      { title: @title, lines: out, width: width }
    end

    def inner_budget(body_h) = [body_h - 2, MIN_INNER].max

    # Inner rows the box occupies with no filter applied — also the height it
    # retains while filtering.
    def locked_rows(body_h) = [@all.size, inner_budget(body_h)].min

    def scroll_by(delta, body_h)
      @scroll = (@scroll + delta).clamp(0, [lines.size - viewport(body_h), 0].max)
    end

    # A status line appears whenever the unfiltered content overflows the body;
    # the filtered view always shows it for the match count.
    def status?(inner) = @all.size > inner

    def status_line(ls, shown)
      parts = ["#{[@scroll + shown, ls.size].min}/#{ls.size}"]
      parts << "↑↓ scroll" if ls.size > shown
      T.paint(:muted, "── #{parts.join(" · ")} ──")
    end
  end
end
