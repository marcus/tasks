# frozen_string_literal: true

require_relative "ansi"

module Tui
  # Pure geometry: map a screen (row, col) to a Hit zone using rectangles from
  # a ScreenLayout. Operates on integers and zone symbols only.
  class HitMap
    A = Ansi

    Hit = Data.define(:zone, :payload)

    OUTSIDE = Hit.new(zone: :outside, payload: nil).freeze

    attr_reader :layout, :tab_spans, :row_count, :modal, :popup, :panel,
                :marker_spans, :footer_roles

    def self.build(layout:, tab_spans: [], row_count: 0, modal: nil, popup: nil,
                   panel: false, marker_spans: {}, footer_roles: [])
      new(layout: layout, tab_spans: tab_spans, row_count: row_count,
          modal: modal, popup: popup, panel: panel,
          marker_spans: marker_spans, footer_roles: footer_roles)
    end

    def initialize(layout:, tab_spans:, row_count:, modal:, popup:, panel:,
                   marker_spans:, footer_roles:)
      @layout = layout
      @tab_spans = tab_spans
      @row_count = row_count
      @modal = modal
      @popup = popup
      @panel = panel
      @marker_spans = marker_spans
      @footer_roles = footer_roles
      freeze
    end

    # Never returns nil — unknown coordinates are :outside.
    def at(row, col)
      return OUTSIDE if row.negative? || col.negative?
      return OUTSIDE if row >= @layout.height || col >= @layout.width

      hit_popup(row, col) ||
        hit_modal(row, col) ||
        hit_body(row, col) ||
        hit_header(row, col) ||
        hit_footer(row, col) ||
        hit_border(row, col) ||
        OUTSIDE
    end

    private

    def hit_popup(row, col)
      return unless @popup

      origin_row, origin_col = @layout.body_origin
      pr = @popup[:row]
      pc = @popup[:col]
      lines = @popup[:lines] || []
      return if lines.empty?

      heights = lines.size
      widths = lines.map { |line| A.vislen(line) }
      max_w = widths.max || 0
      screen_row = origin_row + pr
      screen_col = origin_col + pc
      return unless row.between?(screen_row, screen_row + heights - 1)
      return unless col.between?(screen_col, screen_col + max_w - 1)

      local_row = row - screen_row
      line_w = widths[local_row] || 0
      return Hit.new(zone: :border, payload: nil) if col >= screen_col + line_w

      Hit.new(zone: :popup_row, payload: local_row)
    end

    def hit_modal(row, col)
      return unless @modal

      origin_row, origin_col = @layout.body_origin
      placed = @layout.place_modal(@modal)
      return unless placed

      mr = placed[:row]
      mc = placed[:col]
      lines = placed[:lines] || @modal[:lines] || []
      # Mirror Frame.overlay_modal!: box is lines + top/bottom borders, width
      # pinned by modal[:width] or derived the same way Frame does.
      box_h, box_w = modal_box_size(placed, lines)
      screen_row = origin_row + mr
      screen_col = origin_col + mc
      return unless row.between?(screen_row, screen_row + box_h - 1)
      return unless col.between?(screen_col, screen_col + box_w - 1)

      local_row = row - screen_row
      # Interior content rows are 1..(box_h-2); borders are chrome.
      if local_row.positive? && local_row < box_h - 1
        Hit.new(zone: :modal_row, payload: local_row - 1)
      else
        Hit.new(zone: :border, payload: nil)
      end
    end

    def modal_box_size(placed, lines)
      body_h = @layout.body_height
      body_w = @layout.body_width
      if body_h < 3 || body_w < 4
        content = [placed[:title], *lines].compact
        return [[content.size, body_h].min, body_w]
      end

      bw = placed[:width] ||
           [(lines.map { |l| A.vislen(l) }.max || 0),
            A.vislen(placed[:title].to_s) + 6, 30].max + 4
      bw = [[bw, body_w].min, 4].max
      bh = [lines.size, body_h - 2].min + 2
      [bh, bw]
    end

    def hit_body(row, col)
      return unless @layout.body_rows.cover?(row)

      if @panel && @layout.panel?
        divider = @layout.panel_divider_col
        if col == divider
          return Hit.new(zone: :panel_divider, payload: nil)
        end
        if @layout.panel_cols.cover?(col)
          return Hit.new(zone: :panel, payload: row - @layout.body_rows.begin)
        end
      end

      return unless @layout.list_cols.cover?(col)

      vis = row - @layout.body_rows.begin
      abs = @layout.viewport_offset + vis
      return Hit.new(zone: :border, payload: nil) if abs >= @row_count

      # Cursor/prefix columns occupy the first two list cells; marker spans are
      # measured inside row.text (after that prefix).
      text_col = col - @layout.list_cols.begin - 2
      if text_col >= 0 && (span = @marker_spans[abs])
        start_c, end_c = span
        if text_col >= start_c && text_col < end_c
          return Hit.new(zone: :collapse_marker, payload: abs)
        end
      end

      Hit.new(zone: :list_row, payload: abs)
    end

    def hit_header(row, col)
      return unless row == @layout.header_row

      @tab_spans.each do |key, start_col, end_col|
        return Hit.new(zone: :tab, payload: key) if col >= start_col && col < end_col
      end
      Hit.new(zone: :header, payload: nil)
    end

    def hit_footer(row, col)
      return unless @layout.footer_rows.cover?(row)
      return Hit.new(zone: :border, payload: nil) unless col.between?(1, @layout.width - 2)

      index = row - @layout.footer_rows.begin
      role = @footer_roles[index] || :chrome
      Hit.new(zone: :footer_row, payload: { index: index, role: role })
    end

    def hit_border(row, col)
      # Outer ring, header/footer divider rules, and anything else on-frame.
      Hit.new(zone: :border, payload: nil)
    end
  end
end
