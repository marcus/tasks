# frozen_string_literal: true

module Tui
  # Pure SGR mouse-report decoder. Terminal bytes in, Event out. Legacy X10
  # encoding is deliberately unsupported (see docs/plans/active/tui-mouse-support.md).
  module Mouse
    ENABLE  = "\e[?1000h\e[?1006h"
    DISABLE = "\e[?1006l\e[?1000l"
    SEQUENCE = /\A\e\[<[0-9;]*[Mm]/
    WHEEL_DELTA = 3

    BUTTONS = {
      0 => :left,
      1 => :middle,
      2 => :right,
    }.freeze

    WHEEL_BUTTONS = {
      0 => :wheel_up,
      1 => :wheel_down,
      2 => :wheel_left,
      3 => :wheel_right,
    }.freeze

    Event = Data.define(:button, :action, :col, :row, :shift, :alt, :ctrl) do
      def wheel? = button.to_s.start_with?("wheel_")
      def press? = action == :press
      def release? = action == :release
      def motion? = action == :motion
    end

    module_function

    # Decode one complete SGR report. Returns nil for malformed input rather
    # than raising — a garbled report is discarded, never a crash.
    def decode(seq)
      return nil unless seq.is_a?(String)
      return nil unless (m = seq.match(/\A\e\[<(\d+);(\d+);(\d+)([Mm])\z/))

      cb = Integer(m[1], 10)
      # Terminal reports are 1-based; screen coordinates are 0-based.
      col = Integer(m[2], 10) - 1
      row = Integer(m[3], 10) - 1
      return nil if col.negative? || row.negative?

      action = if (cb & 32) != 0
                 :motion
               elsif m[4] == "M"
                 :press
               else
                 :release
               end

      shift = (cb & 4) != 0
      alt   = (cb & 8) != 0
      ctrl  = (cb & 16) != 0
      code  = cb & ~(4 | 8 | 16 | 32)

      button = if (code & 64) != 0
                 WHEEL_BUTTONS[code & 3]
               elsif (code & 128) != 0
                 :"button#{8 + (code & 3)}"
               else
                 BUTTONS[code & 3]
               end
      return nil unless button

      Event.new(button: button, action: action, col: col, row: row,
                shift: shift, alt: alt, ctrl: ctrl)
    rescue ArgumentError, TypeError
      nil
    end
  end
end
