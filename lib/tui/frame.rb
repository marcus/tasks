# frozen_string_literal: true

require_relative "ansi"

module Tui
  # Pure frame builder: given content, returns an array of strings, one per
  # terminal row, each exactly `width` visible characters. No IO — the app
  # decides how to paint, tests can assert on the result.
  module Frame
    A = Ansi

    module_function

    # rows:     array of Views::Row
    # selected: row index to highlight (or nil)
    # footer:   array of interior lines; the symbol :rule draws a divider
    # popup:    { lines: [...], row: Integer, col: Integer } overlaid on body
    def build(width:, height:, header:, rows:, selected: nil, footer: [], popup: nil)
      w = width - 2
      # top border + header + rule + body + rule + footer + bottom border
      body_h = [height - 5 - footer.size, 1].max

      # keep the selection in view
      offset = 0
      offset = selected - body_h + 1 if selected && selected >= body_h

      body = (rows[offset, body_h] || []).map.with_index do |row, vi|
        if offset + vi == selected
          A.invert(A.vpad("▸ " + A.strip(row.text), w - 2))
        else
          "  " + row.text
        end
      end
      body.map! { |l| A.vtrunc(l, w - 2) }
      body.fill("", body.size...body_h)

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

    # Paste popup lines over the body starting at popup[:row]/[:col].
    # Styling under the popup is dropped (plain-text base) — fine for a
    # transient overlay.
    def overlay!(body, popup, w)
      popup[:lines].each_with_index do |pl, k|
        r = popup[:row] + k
        next if r.negative? || r >= body.size
        base = A.strip(body[r]).ljust(w)
        body[r] = A.vtrunc(base[0, popup[:col]] + pl, w)
      end
    end
  end
end
