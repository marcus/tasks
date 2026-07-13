# frozen_string_literal: true

require_relative "ansi"

module Tui
  # Pure geometry for one sampled terminal frame. Rendering, popup placement,
  # modal scrolling, and selection coordinates all consume this same value so
  # a resize cannot mix dimensions from different winsize reads.
  class ScreenLayout
    A = Ansi
    FIXED_ROWS = 5 # borders, header, and the two rules outside the body/footer

    PANEL_MODES = %i[compact standard wide focus].freeze
    PANEL_RATIO = 0.40
    WIDE_PANEL_RATIO = 0.58
    MIN_PANEL_WIDTH = 28
    MIN_LIST_WIDTH = 8
    EDIT_MIN_CONTENT_WIDTH = 32
    INLINE_CONTENT_WIDTH = 48
    EDIT_PANEL_CHROME_ROWS = 2 # RightPanel title and divider
    EDIT_MIN_VISIBLE_ROWS = 1  # compact focused-field fallback

    attr_reader :width, :height, :footer, :body_height, :body_width,
                :list_width, :panel_width, :panel_content_width,
                :viewport_offset, :selected_screen_row, :selected,
                :panel_mode, :requested_panel_mode, :edit_content_height,
                :panel_offset

    def initialize(width:, height:, footer:, selected: nil, panel: false,
                   panel_mode: :standard, panel_offset: 0, editing: false)
      @width = width
      @height = height
      @footer = footer.last([height - 6, 0].max).map do |line|
        line.is_a?(String) ? line.dup.freeze : line
      end.freeze
      @body_height = [height - FIXED_ROWS - @footer.size, 1].max
      @body_width = [width - 4, 1].max
      @requested_panel_mode = normalize_panel_mode(panel_mode)
      @panel_offset = panel_offset.to_i
      @editing = !!editing
      @panel_mode, @panel_width = panel ? calculate_panel : [@requested_panel_mode, 0]
      @panel_content_width = @panel_width.zero? ? 0 : [@panel_width - 2, 1].max
      @edit_content_height = [@body_height - EDIT_PANEL_CHROME_ROWS, 0].max
      @list_width = @body_width - @panel_width
      @selected = selected
      @viewport_offset = @selected && @selected >= @body_height ? @selected - @body_height + 1 : 0
      @selected_screen_row = @selected && @selected - @viewport_offset
      freeze
    end

    def footer_size = @footer.size
    def panel? = @panel_width.positive?
    def editing? = @editing
    def editable_panel?
      panel? && @panel_content_width >= EDIT_MIN_CONTENT_WIDTH &&
        @edit_content_height >= EDIT_MIN_VISIBLE_ROWS
    end
    def content_breakpoint
      return :below_minimum if @panel_content_width < EDIT_MIN_CONTENT_WIDTH
      return :stacked if @panel_content_width < INLINE_CONTENT_WIDTH

      :inline
    end

    def self.minimum_edit_terminal_width
      4 + MIN_LIST_WIDTH + 2 + EDIT_MIN_CONTENT_WIDTH
    end

    # Named zero-footer minimum. Every footer/help row consumes another
    # terminal row before the panel title, divider, and focused fallback.
    def self.minimum_edit_terminal_height(footer_rows: 0)
      FIXED_ROWS + Integer(footer_rows) + EDIT_PANEL_CHROME_ROWS + EDIT_MIN_VISIBLE_ROWS
    end

    def self.minimum_edit_terminal_size(footer_rows: 0)
      [minimum_edit_terminal_height(footer_rows: footer_rows), minimum_edit_terminal_width].freeze
    end

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

    # Mode selection stays keyed off each mode's natural (unoffset) width so the
    # editing promotion ladder is unchanged; the column offset is a user tweak
    # layered on top of whichever mode wins.
    def calculate_panel
      candidates = if @editing
                     PANEL_MODES.drop(PANEL_MODES.index(@requested_panel_mode))
                   else
                     [@requested_panel_mode]
                   end
      candidates.each do |mode|
        width = panel_width_for(mode)
        return [mode, apply_panel_offset(width)] if !@editing || width - 2 >= EDIT_MIN_CONTENT_WIDTH
      end

      mode = candidates.last || :focus
      [mode, apply_panel_offset(panel_width_for(mode))]
    end

    def panel_width_for(mode)
      return [@body_width - 1, 0].max if @body_width < MIN_LIST_WIDTH + 3

      desired = case mode
                when :compact then EDIT_MIN_CONTENT_WIDTH + 2
                when :standard then [(@body_width * PANEL_RATIO).round, MIN_PANEL_WIDTH].max
                when :wide then [(@body_width * WIDE_PANEL_RATIO).round, MIN_PANEL_WIDTH].max
                when :focus then @body_width - MIN_LIST_WIDTH
                end
      desired.clamp(3, @body_width - MIN_LIST_WIDTH)
    end

    # ctrl+k/ctrl+l shift the panel by a signed column count on top of the mode
    # width. The same invariants that bound panel_width_for bound the result:
    # the list keeps MIN_LIST_WIDTH, editing keeps EDIT_MIN_CONTENT_WIDTH of
    # content, and the panel never goes negative or wider than the body. Ratio
    # bases re-derive on resize, so the offset rides along without re-clamping.
    def apply_panel_offset(width)
      return width if @panel_offset.zero? || @body_width < MIN_LIST_WIDTH + 3

      hi = @body_width - MIN_LIST_WIDTH
      floor = @editing ? EDIT_MIN_CONTENT_WIDTH + 2 : 3
      (width + @panel_offset).clamp([floor, hi].min, hi)
    end

    def normalize_panel_mode(value)
      value = value.to_sym if value.respond_to?(:to_sym)
      PANEL_MODES.include?(value) ? value : :standard
    end
  end
end
