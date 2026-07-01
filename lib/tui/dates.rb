# frozen_string_literal: true

require "date"

module Tui
  # Fuzzy date input for the reschedule popup. Accepts:
  #   today · tomorrow · +3 · fri/friday · 07-15 · 7/15 · 2026-07-15
  module Dates
    WDAYS = %w[sunday monday tuesday wednesday thursday friday saturday].freeze

    module_function

    # Returns a Date, or nil if the input can't be understood.
    def parse_when(str, today: Date.today)
      s = str.to_s.strip.downcase
      return nil if s.empty?
      return today     if s == "today"
      return today + 1 if s == "tomorrow"
      return today + Regexp.last_match(1).to_i if s =~ /\A\+(\d+)\z/

      if s.length >= 3 && (i = WDAYS.index { |d| d.start_with?(s) })
        delta = (i - today.wday) % 7
        delta = 7 if delta.zero? # "fri" on a Friday means next Friday
        return today + delta
      end

      case s
      when /\A(\d{4})-(\d{1,2})-(\d{1,2})\z/
        Date.new($1.to_i, $2.to_i, $3.to_i)
      when %r{\A(\d{1,2})[-/](\d{1,2})\z}
        d = Date.new(today.year, $1.to_i, $2.to_i)
        d < today ? d.next_year : d # bare month-day in the past rolls forward
      end
    rescue Date::Error
      nil
    end
  end
end
