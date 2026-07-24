# frozen_string_literal: true

require_relative "ansi"

module Tui
  # The single owner of box-drawing chrome: rounded corners plus an angled
  # truecolor gradient swept across a container's border cells. Frame, modals,
  # and the palettes all route their boxes through here so every container gets
  # the same look, and so the box-drawing glyphs live in exactly one place.
  #
  # A gradient is { stops: [[r,g,b], …], angle: degrees }. For each border cell
  # the cell is projected onto the angle's direction vector (with a vertical
  # aspect correction, since terminal cells are ~2× taller than wide), the
  # projection is normalized across the box's four corners to t ∈ [0,1], and the
  # stops are interpolated at t. When no gradient is active (no truecolor, a
  # NO_COLOR/mono theme, or none configured) the border falls back to a single
  # solid SGR — same call sites, no branching at the caller.
  module Border
    A = Ansi

    # Rounded outer corners; the divider tees (├ ┤) are shared by both charsets
    # since the header/footer rules keep their square junctions.
    ROUND  = { tl: "╭", tr: "╮", bl: "╰", br: "╯", h: "─", v: "│", ml: "├", mr: "┤" }.freeze
    SQUARE = { tl: "┌", tr: "┐", bl: "└", br: "┘", h: "─", v: "│", ml: "├", mr: "┤" }.freeze

    # Distinct colors along the ring. Quantizing to a fixed palette both caps the
    # number of SGR sequences emitted and lets adjacent equal-color cells coalesce
    # into one run, so a ~200-cell perimeter costs a couple dozen escapes, not 200.
    DEFAULT_STEPS  = 24
    # Terminal cells are about twice as tall as wide; scaling y by this makes a
    # configured angle read on screen as that angle rather than vertically skewed.
    DEFAULT_ASPECT = 2.0

    module_function

    def chars(corners) = corners == :square ? SQUARE : ROUND

    # Truecolor gating. `nil` (the default) auto-detects; tests set it explicitly.
    # Absent truecolor → gradients drop to the solid fallback.
    def truecolor=(value)
      @truecolor = value
    end

    def truecolor?
      return @truecolor unless @truecolor.nil?
      return false if ENV["NO_COLOR"] && !ENV["NO_COLOR"].empty?
      return true if ENV["COLORTERM"].to_s.match?(/truecolor|24bit/i)

      term = ENV["TERM"].to_s
      !(term.empty? || term == "dumb")
    end

    # Parse a border_gradient spec: two-or-more hex stops then "@<angle>", e.g.
    # "#7aa2f7 #bb9af7 @60". Returns { stops:, angle: } or nil if malformed, so a
    # bad config value degrades to the solid border rather than raising.
    def parse_gradient(spec)
      tokens = spec.to_s.strip.downcase.split(/\s+/)
      return nil if tokens.empty?

      angle = 0.0
      stops = []
      tokens.each do |tok|
        if tok.start_with?("@")
          deg = tok[1..]
          return nil unless deg.match?(/\A-?\d+(?:\.\d+)?\z/)

          angle = deg.to_f
        elsif tok.match?(/\A#\h{6}\z/)
          stops << tok[1..].scan(/../).map { |h| h.to_i(16) }
        else
          return nil
        end
      end
      return nil if stops.length < 2

      { stops: stops, angle: angle }
    end

    # Build a Painter for a box of `width` × `height` cells. `gradient` is a
    # parsed spec or nil; `solid` is an SGR opener string (e.g. "\e[90m") or ""
    # used when the gradient is inactive.
    def painter(width:, height:, gradient: nil, solid: "", steps: DEFAULT_STEPS, aspect: DEFAULT_ASPECT)
      Painter.new(width: width, height: height, gradient: gradient, solid: solid, steps: steps, aspect: aspect)
    end

    # Render a complete box: rounded corners, gradient-swept edges, an optional
    # title strip on the top row. `inner_lines` are each exactly `inner_width`
    # visible cells (any inner margin is the caller's); the vertical edges are
    # added here. `title` (already styled by the caller, e.g. :modal_title) sits
    # after `title_lead` leading dashes; the border glyphs around it are painted
    # while the title text passes through untouched. Returns full rows, each
    # `inner_width + 2` cells wide.
    def box(inner_lines:, inner_width:, gradient: nil, solid: "", title: nil, title_lead: 0,
            corners: :round, steps: DEFAULT_STEPS, aspect: DEFAULT_ASPECT)
      width = inner_width + 2
      height = inner_lines.length + 2
      c = chars(corners)
      p = painter(width: width, height: height, gradient: gradient, solid: solid, steps: steps, aspect: aspect)

      rows = [top_row(p, c, inner_width, title, title_lead)]
      inner_lines.each_with_index do |line, i|
        y = i + 1
        rows << p.cell(0, y, c[:v]) + line + p.cell(width - 1, y, c[:v])
      end
      rows << p.run(height - 1, 0, [c[:bl], *Array.new(inner_width, c[:h]), c[:br]])
      rows
    end

    def top_row(painter, c, inner_width, title, title_lead)
      return painter.run(0, 0, [c[:tl], *Array.new(inner_width, c[:h]), c[:tr]]) if title.nil?

      lead = title_lead
      tw = A.vislen(title)
      fill = [inner_width - lead - tw, 0].max
      painter.run(0, 0, [c[:tl], *Array.new(lead, c[:h])]) +
        title +
        painter.run(0, 1 + lead + tw, [*Array.new(fill, c[:h]), c[:tr]])
    end

    # Precomputes the per-cell colorizer for one box size. `cell`/`run` return
    # glyphs wrapped in the appropriate SGR; the caller maps only border glyphs
    # through it, leaving interior content untouched.
    class Painter
      RESET = "\e[0m"

      def initialize(width:, height:, gradient:, solid:, steps:, aspect:)
        @solid = solid.to_s
        gradient = nil unless Border.truecolor?
        if gradient
          setup_gradient(width: width, height: height, gradient: gradient,
                         steps: [steps.to_i, 2].max, aspect: aspect.to_f)
        end
      end

      # SGR opener for the cell at (x, y): a truecolor fg in gradient mode, else
      # the solid fallback (possibly "").
      def sgr_at(x, y)
        return @solid unless @palette

        proj = (x * @cos) + (y * @aspect * @sin)
        t = @range.zero? ? 0.0 : (proj - @pmin) / @range
        @palette[(t * (@palette.length - 1)).round.clamp(0, @palette.length - 1)]
      end

      # A single colored glyph, self-closing.
      def cell(x, y, glyph)
        seq = sgr_at(x, y)
        seq.empty? ? glyph : "#{seq}#{glyph}#{RESET}"
      end

      # A run of consecutive cells on row `y` starting at column `x0`, with equal
      # adjacent colors coalesced to one SGR. Closes with a single reset iff any
      # color was emitted.
      def run(y, x0, glyphs)
        out = +""
        last = nil
        styled = false
        glyphs.each_with_index do |glyph, i|
          seq = sgr_at(x0 + i, y)
          if seq != last
            out << seq
            last = seq
            styled ||= !seq.empty?
          end
          out << glyph
        end
        out << RESET if styled
        out
      end

      private

      def setup_gradient(width:, height:, gradient:, steps:, aspect:)
        rad = gradient[:angle] * Math::PI / 180.0
        @cos = Math.cos(rad)
        @sin = Math.sin(rad)
        @aspect = aspect
        corners = [[0, 0], [width - 1, 0], [0, height - 1], [width - 1, height - 1]]
        projections = corners.map { |cx, cy| (cx * @cos) + (cy * aspect * @sin) }
        @pmin = projections.min
        @range = projections.max - @pmin
        stops = gradient[:stops]
        @palette = (0...steps).map do |i|
          r, g, b = lerp(stops, i / (steps - 1.0))
          "\e[38;2;#{r};#{g};#{b}m"
        end
      end

      # Interpolate the stop list at t ∈ [0,1] in plain sRGB. Predictable and
      # fine for the adjacent-hue sweeps borders use; isolated here so the color
      # space can be upgraded (linear-light / OkLab) without touching callers.
      def lerp(stops, t)
        return stops.first if stops.length == 1

        seg = (t * (stops.length - 1)).clamp(0, stops.length - 1)
        i = seg.floor.clamp(0, stops.length - 2)
        f = seg - i
        a = stops[i]
        b = stops[i + 1]
        [0, 1, 2].map { |k| (a[k] + ((b[k] - a[k]) * f)).round }
      end
    end
  end
end
