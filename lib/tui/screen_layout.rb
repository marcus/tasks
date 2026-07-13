# frozen_string_literal: true

require_relative "ansi"

module Tui
  # Pure geometry for one sampled terminal frame. Rendering, popup placement,
  # modal scrolling, and selection coordinates all consume this same value so
  # a resize cannot mix dimensions from different winsize reads.
  class ScreenLayout
    A = Ansi
    FIXED_ROWS = 5 # borders, header, and the two rules outside the body/footer

    PANEL_RATIO = 0.40
    MIN_PANEL_WIDTH = 28
    MIN_LIST_WIDTH = 8

    attr_reader :width, :height, :footer, :body_height, :body_width,
                :list_width, :panel_width, :panel_content_width,
                :viewport_offset, :selected_screen_row, :selected

    def initialize(width:, height:, footer:, selected: nil, panel: false)
      @width = width
      @height = height
      @footer = footer.last([height - 6, 0].max).map do |line|
        line.is_a?(String) ? line.dup.freeze : line
      end.freeze
      @body_height = [height - FIXED_ROWS - @footer.size, 1].max
      @body_width = [width - 4, 1].max
      @panel_width = panel ? calculate_panel_width : 0
      @panel_content_width = @panel_width.zero? ? 0 : [@panel_width - 2, 1].max
      @list_width = @body_width - @panel_width
      @selected = selected
      @viewport_offset = @selected && @selected >= @body_height ? @selected - @body_height + 1 : 0
      @selected_screen_row = @selected && @selected - @viewport_offset
      freeze
    end

    def footer_size = @footer.size
    def panel? = @panel_width.positive?

    def visible_rows(rows)
      rows[@viewport_offset, @body_height] || []
    end

    def place_popup(popup, preferred_col:)
      return unless popup

      popup_width = popup[:lines].map { |line| A.vislen(line) }.max || 0
      popup_height = popup[:lines].size
      col = preferred_col.clamp(0, [@body_width - popup_width, 0].max)
      selected_row = @selected_screen_row || 0
      below = selected_row + 1
      row = if popup_height <= @body_height - below
              below
            elsif popup_height <= selected_row
              selected_row - popup_height
            else
              [@body_height - popup_height, 0].max
            end
      popup.merge(row: row, col: col)
    end

    # Frame still draws the modal box; this value supplies its stable anchor.
    def place_modal(modal)
      return unless modal

      if @body_height < 3 || @body_width < 4
        return modal.merge(row: 0, col: 0)
      end

      box_width = modal[:width] ||
                  [(modal[:lines].map { |line| A.vislen(line) }.max || 0),
                   A.vislen(modal[:title]) + 6, 30].max + 4
      box_width = [[box_width, @body_width].min, 4].max
      box_height = [modal[:lines].size, @body_height - 2].min + 2
      modal.merge(
        row: [(@body_height - box_height) / 2, 0].max,
        col: [(@body_width - box_width) / 2, 0].max,
      )
    end

    private

    def calculate_panel_width
      return [@body_width - 1, 0].max if @body_width < MIN_LIST_WIDTH + 3

      desired = [(@body_width * PANEL_RATIO).round, MIN_PANEL_WIDTH].max
      desired.clamp(3, @body_width - MIN_LIST_WIDTH)
    end
  end
end
