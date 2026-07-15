# frozen_string_literal: true

require_relative "ansi"
require_relative "theme"
require_relative "screen_layout"

module Tui
  # Pure frame builder: given content, returns an array of strings, one per
  # terminal row, each exactly `width` visible characters. No IO — the app
  # decides how to paint, tests can assert on the result.
  module Frame
    A = Ansi
    T = Theme

    module_function

    # rows:     array of Views::Row
    # selected: row index to highlight (or nil)
    # footer:   array of interior lines; the symbol :rule draws a divider
    # popup:    { lines: [...], row: Integer, col: Integer } overlaid on body
    # panel:    { title:, lines: [...] } drawn in a fixed-width right pane
    # modal:    { title:, lines: [...] } drawn as a centered box over the body
    def build(width:, height:, header:, rows:, selected: nil, footer: [], popup: nil, panel: nil, modal: nil,
              layout: nil, panel_offset: 0)
      layout ||= ScreenLayout.new(width: width, height: height, footer: footer, selected: selected,
                                  panel: !panel.nil?, panel_offset: panel_offset)
      width = layout.width
      height = layout.height
      w = width - 2
      footer = layout.footer
      body_h = layout.body_height
      offset = layout.viewport_offset

      list_w = layout.list_width
      body = (rows[offset, body_h] || []).map.with_index do |row, vi|
        if vi == layout.selected_screen_row
          selected_row(row, list_w)
        else
          "  " + row.text
        end
      end
      body.map! { |line| A.vpad(A.vtrunc(line, list_w), list_w) }
      # Empty filler rows are pre-padded so we don't pay a second full-body
      # vpad pass just to expand a short viewport.
      body.fill(" " * list_w, body.size...body_h)

      render_panel!(body, panel, layout) if panel

      # Modal first, then the popup on top. Archive confirmation and forms must
      # remain visible above a persistent task-detail panel.
      overlay_modal!(body, modal, w - 2) if modal
      overlay!(body, popup, w - 2) if popup

      lines = []
      lines << "┌#{"─" * w}┐"
      lines << "│#{A.vpad(A.vtrunc(header, w), w)}│"
      lines << "├#{"─" * w}┤"
      body.each { |b| lines << "│ #{A.vpad(b, w - 2)} │" }
      lines << "├#{"─" * w}┤"
      footer.each do |f|
        lines << (f == :rule ? "├#{"─" * w}┤" : "│#{A.vpad(A.vtrunc(f, w), w)}│")
      end
      lines << "└#{"─" * w}┘"
      lines
    end

    def render_panel!(body, panel, layout)
      content_width = layout.panel_content_width
      panel_lines = [T.paint(:panel_title, A.vtrunc(panel[:title], content_width))]
      panel_lines << T.paint(:muted, "─" * content_width)
      panel_lines.concat(panel[:lines])

      body.map!.with_index do |list_line, index|
        content = A.vpad(A.vtrunc(panel_lines[index].to_s, content_width), content_width)
        "#{A.vpad(A.vtrunc(list_line, layout.list_width), layout.list_width)}│ #{content}"
      end
    end

    # The selection cursor glyph. Deliberately distinct from Views::MARK_COLLAPSED
    # ("▸ ") so a selected collapsed row reads "❯ ▸ title", not a doubled marker.
    # One cell + a trailing space = two cells, matching the marker column width.
    CURSOR = "❯ "

    # Render the selected row with its own field colors intact ON TOP of the
    # :selection background, via SGR compositing (no stripping/repaint).
    #
    # Contract: the row text already carries its field SGRs, each closed with a
    # reset (\e[0m — the only reset form Ansi.color emits). We (1) open the line
    # with the :selection SGR, (2) re-open it immediately after every reset so a
    # field's own fg/attrs layer over the selection background instead of
    # clearing it, (3) pad the visible text to the full inner width so the
    # background spans the row, then (4) close with a single reset. Truncation
    # runs FIRST, on the composed cursor+text, so the pad+reset tail can never be
    # clipped. A :selection that resolves to nothing (an unstyled theme) skips
    # compositing and just pads the plain cursor+text.
    def selected_row(row, w)
      body = A.vtrunc(CURSOR + row.text, w)
      sel = T.sgr(:selection)
      return A.vpad(body, w) if sel.empty?

      composited = A.composite(sel, body)
      pad = w - A.vislen(body)
      composited += " " * pad if pad.positive?
      composited + "\e[0m"
    end

    # Paste popup lines over the body starting at terminal-cell coordinates,
    # preserving styled base content on either side. Cell slices replace any
    # partially covered wide grapheme with padding, so content after the popup
    # remains in its original column.
    def overlay!(body, popup, w)
      popup[:lines].each_with_index do |pl, k|
        r = popup[:row] + k
        next if r.negative? || r >= body.size

        col = popup[:col].to_i
        source_start = [-col, 0].max
        dest_start = [col, 0].max
        next if dest_start >= w

        base = A.vpad(A.vtrunc(body[r], w), w)
        replacement = A.cell_slice(pl, source_start, w - dest_start)
        replacement_width = A.vislen(replacement)
        prefix = A.vpad(A.cell_slice(base, 0, dest_start), dest_start)
        suffix_start = dest_start + replacement_width
        suffix = A.cell_slice(base, suffix_start, w - suffix_start)
        body[r] = A.vpad(A.vtrunc(prefix + replacement + suffix, w), w)
      end
    end

    # Box up modal content and center it over the body via overlay!.
    # modal[:width] pins the box width (Modal computes it from the full
    # content so scrolling can't resize the box); without it the width
    # fits the visible lines. The title strip is painted with the
    # :modal_title theme slot, so themes can give it a background.
    def overlay_modal!(body, modal, w)
      if body.size < 3 || w < 4
        compact = [T.paint(:modal_title, modal[:title]), *modal[:lines]].first(body.size)
        overlay!(body, { lines: compact, row: 0, col: 0 }, w)
        return
      end

      bw = modal[:width] ||
           [(modal[:lines].map { |l| A.vislen(l) }.max || 0), A.vislen(modal[:title]) + 6, 30].max + 4
      bw = [[bw, w].min, 4].max
      inner = modal[:lines].map { |l| A.vtrunc(l, bw - 4) }
      inner = inner.first(body.size - 2)
      title = A.vtrunc(" #{modal[:title]} ", bw - 4)
      box = ["┌─#{T.paint(:modal_title, title)}#{"─" * [bw - 4 - A.vislen(title), 0].max}─┐"]
      inner.each { |l| box << "│ #{A.vpad(l, bw - 4)} │" }
      box << "└#{"─" * (bw - 2)}┘"
      overlay!(body, {
        lines: box,
        row: modal.fetch(:row, [(body.size - box.size) / 2, 0].max),
        col: modal.fetch(:col, [(w - bw) / 2, 0].max),
      }, w)
    end
  end
end
