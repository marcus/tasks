# frozen_string_literal: true

module Tui
  # ANSI color + width-aware string helpers. Everything that knows about
  # escape codes lives here so the rest of the code can treat styled
  # strings as opaque.
  module Ansi
    module_function

    def color(str, *codes) = "\e[#{codes.join(";")}m#{str}\e[0m"

    def bold(s)   = color(s, 1)
    def dim(s)    = color(s, 90)
    def red(s)    = color(s, 31)
    def yellow(s) = color(s, 33)
    def cyan(s)   = color(s, 36)
    def invert(s) = color(s, 7)

    def strip(s) = s.gsub(/\e\[[0-9;]*m/, "")

    # Visible length (ignores escape codes).
    def vislen(s) = strip(s).length

    # Pad to visible width w (no-op if already wider).
    def vpad(s, w)
      pad = w - vislen(s)
      pad.positive? ? s + " " * pad : s
    end

    # Truncate to visible width w, appending a dim ellipsis. Escape codes
    # are preserved; a reset is appended so styles can't leak.
    def vtrunc(s, w)
      return s if vislen(s) <= w
      out = +""
      count = 0
      s.scan(/\e\[[0-9;]*m|./m) do |tok|
        if tok.start_with?("\e[")
          out << tok
        else
          break if count >= w - 1
          out << tok
          count += 1
        end
      end
      out << "\e[0m" << dim("…")
    end

    # Word-wrap plain text to width w. Returns an array of lines.
    def wrap(text, w)
      strip(text).split("\n", -1).flat_map do |line|
        line = line.rstrip
        next [""] if line.empty?
        out = []
        cur = +""
        line.split(/(\s+)/).each do |tok|
          if cur.length + tok.length > w && !cur.strip.empty?
            out << cur.rstrip
            cur = tok.lstrip.dup
          else
            cur << tok
          end
        end
        out << cur.rstrip unless cur.strip.empty?
        out.empty? ? [""] : out
      end
    end
  end
end
