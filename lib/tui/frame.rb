# frozen_string_literal: true

require_relative "ansi"
require_relative "theme"

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
    # modal:    { title:, lines: [...] } drawn as a centered box over the body
    def build(width:, height:, header:, rows:, selected: nil, footer: [], popup: nil, modal: nil)
      w = width - 2
      # top border + header + rule + body + rule + footer + bottom border
      body_h = [height - 5 - footer.size, 1].max

      # keep the selection in view
      offset = 0
      offset = selected - body_h + 1 if selected && selected >= body_h

      body = (rows[offset, body_h] || []).map.with_index do |row, vi|
        if offset + vi == selected
          selected_row(row, w - 2)
        else
          "  " + row.text
        end
      end
      body.map! { |l| A.vtrunc(l, w - 2) }
      body.fill("", body.size...body_h)

      # modal first, then the popup on top: rescheduling can be launched from an
      # open detail modal, and the date popup must sit above it.
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

      # \e[0?m matches only the true resets (\e[0m / \e[m), never a field opener
      # that merely contains a 0 param (e.g. \e[38;2;0;0;0m), so we re-inject the
      # selection SGR after resets only.
      composited = sel + body.gsub(/\e\[0?m/) { |reset| reset + sel }
      pad = w - A.vislen(body)
      composited += " " * pad if pad.positive?
      composited + "\e[0m"
    end

    # Paste popup lines over the body starting at popup[:row]/[:col],
    # preserving the (plain-text) base content on either side. Styling
    # under the popup is dropped — fine for a transient overlay.
    def overlay!(body, popup, w)
      popup[:lines].each_with_index do |pl, k|
        r = popup[:row] + k
        next if r.negative? || r >= body.size
        base = A.strip(body[r]).ljust(w)
        rest = base[popup[:col] + A.vislen(pl)..] || ""
        body[r] = A.vtrunc(base[0, popup[:col]] + pl + rest, w)
      end
    end

    # Box up modal content and center it over the body via overlay!.
    # modal[:width] pins the box width (Modal computes it from the full
    # content so scrolling can't resize the box); without it the width
    # fits the visible lines. The title strip is painted with the
    # :modal_title theme slot, so themes can give it a background.
    def overlay_modal!(body, modal, w)
      bw = modal[:width] ||
           [(modal[:lines].map { |l| A.vislen(l) }.max || 0), A.vislen(modal[:title]) + 6, 30].max + 4
      bw = [bw, w - 2].min
      inner = modal[:lines].map { |l| A.vtrunc(l, bw - 4) }
      title = A.vtrunc(" #{modal[:title]} ", bw - 4)
      box = ["┌─#{T.paint(:modal_title, title)}#{"─" * [bw - 4 - A.vislen(title), 0].max}─┐"]
      inner.each { |l| box << "│ #{A.vpad(l, bw - 4)} │" }
      box << "└#{"─" * (bw - 2)}┘"
      overlay!(body, {
        lines: box,
        row: [(body.size - box.size) / 2, 0].max,
        col: [(w - bw) / 2, 0].max,
      }, w)
    end
  end
end
