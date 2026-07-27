# frozen_string_literal: true

require "date"

module Tasks
  # Fuzzy date input for the reschedule popup, `due`/`schedule`/`defer`, and
  # task creation. Accepts:
  #   today · tomorrow · next week · next month · next year
  #   +3 · in 3 days · in 2 weeks · in 6 months · in a year
  #   fri/friday · next fri
  #   07-15 · 7/15 · 7/15/2026 · 2026-07-15 · 2026/07/15
  #   aug 1 · august 1st · 1 aug 2026 · aug 1, 2026
  #
  # Every numeric slash/dash form except the YYYY-first ISO one is ambiguous
  # between month-first and day-first (a year, even a 4-digit one, says
  # nothing about which number is which) — `date_order` says whether the
  # first number is the month (:mdy, the default) or the day (:dmy). Set the
  # process-wide default once via `configure!` (from Tasks::Config#date_order);
  # `parse_when`'s own `date_order:` kwarg exists so callers/tests can
  # override per call.
  module Dates
    WDAYS = %w[sunday monday tuesday wednesday thursday friday saturday].freeze
    MONTHS = %w[january february march april may june july
                august september october november december].freeze
    ORDER_VALUES = %i[mdy dmy].freeze
    DEFAULT_ORDER = :mdy

    # Trailing ordinal suffix on a day number: "1st", "22nd". Input reaching
    # this is already downcased, so no /i needed. Not validated against the
    # number itself (rendering "1nd" fine) — a fuzzy parser can afford to be
    # generous about a cosmetic mismatch.
    ORDINAL = /(?:st|nd|rd|th)/

    module_function

    # Install the process-wide bare-numeric-date interpretation. Anything but
    # :mdy/:dmy falls back to the default, so a bad config value degrades
    # rather than crashes (see Theme.configure! for the same shape).
    def configure!(date_order: DEFAULT_ORDER)
      @date_order = ORDER_VALUES.include?(date_order) ? date_order : DEFAULT_ORDER
    end

    # Clears the installed default back to unconfigured (see Theme#reset!) —
    # tests use this to guarantee isolation instead of hardcoding a value that
    # merely happens to match today's default.
    def reset!
      @date_order = nil
    end

    def date_order
      @date_order ||= DEFAULT_ORDER
    end

    # Returns a Date, or nil if the input can't be understood.
    def parse_when(str, today: Date.today, date_order: date_order())
      s = str.to_s.strip.downcase
      return nil if s.empty?

      parse_keyword(s, today) ||
        parse_relative(s, today) ||
        parse_weekday(s, today) ||
        parse_month_name(s, today) ||
        parse_numeric(s, today, date_order)
    rescue Date::Error
      nil
    end

    def parse_keyword(s, today)
      case s
      when "today" then today
      when "tomorrow" then today + 1
      when "next week" then today + 7
      when "next month" then today >> 1
      when "next year" then today.next_year
      end
    end

    # "+3" (days from today) and "in 3 days/weeks/months/years" (also "a"/"an"
    # for 1: "in a week"). Months/years use Date#>>, which clamps an
    # out-of-range day to the end of the target month (Jan 31 + 1mo -> Feb 28).
    def parse_relative(s, today)
      return today + Regexp.last_match(1).to_i if s =~ /\A\+(\d+)\z/

      m = s.match(/\Ain (\d+|an?) (day|week|month|year)s?\z/)
      return nil unless m

      n = %w[a an].include?(m[1]) ? 1 : m[1].to_i
      case m[2]
      when "day" then today + n
      when "week" then today + (n * 7)
      when "month" then today >> n
      when "year" then today >> (n * 12)
      end
    end

    # Bare weekday name ("fri", "friday") or "next <weekday>" (a convenience
    # alias, not an extra week's skip — both mean the same next occurrence).
    # Same weekday as today rolls to next week rather than returning today.
    def parse_weekday(s, today)
      token = s.start_with?("next ") ? s.delete_prefix("next ") : s
      return nil unless token.length >= 3 && (i = WDAYS.index { |d| d.start_with?(token) })

      delta = (i - today.wday) % 7
      delta = 7 if delta.zero?
      today + delta
    end

    MONTH_DAY_YEAR = /\A([a-z]+)\.?\s+(\d{1,2})#{ORDINAL}?(?:\s+(\d{4}))?\z/
    DAY_MONTH_YEAR = /\A(\d{1,2})#{ORDINAL}?\s+([a-z]+)\.?(?:\s+(\d{4}))?\z/

    # Month-name forms in either order: "aug 1", "august 1st", "1 aug 2026",
    # "aug 1, 2026". A trailing comma before the year is normalized to a space
    # so "aug 1,2026" (no space after the comma) still lines up the day/year.
    def parse_month_name(s, today)
      cleaned = s.tr(",", " ")

      if (m = cleaned.match(MONTH_DAY_YEAR)) && (month = month_index(m[1]))
        build_month_date(today, month, m[2].to_i, m[3])
      elsif (m = cleaned.match(DAY_MONTH_YEAR)) && (month = month_index(m[2]))
        build_month_date(today, month, m[1].to_i, m[3])
      end
    end

    # 1-based month for a full or (>=3 char) abbreviated prefix, or nil for
    # anything shorter or unrecognized. Not ambiguity *resolution* — it's a
    # plain first-match prefix lookup — but every standard three-letter
    # English month abbreviation happens to be a unique prefix (mar/may,
    # jun/jul don't collide), so a bare 3-letter abbreviation always resolves
    # to the month a human would mean.
    def month_index(token)
      return nil if token.length < 3

      idx = MONTHS.index { |name| name.start_with?(token) }
      idx && idx + 1
    end

    # A bare year-less month/day rolls to next year if already past — but
    # only when the rolled date actually exists (Date.new raises and
    # parse_when returns nil rather than silently landing on a different day,
    # e.g. "feb 29" asked for after this year's Feb 29 in a year whose
    # successor isn't a leap year).
    def build_month_date(today, month, day, year_str)
      return Date.new(year_str.to_i, month, day) if year_str

      d = Date.new(today.year, month, day)
      d < today ? Date.new(today.year + 1, month, day) : d
    end

    ISO_LIKE = %r{\A(\d{4})[-/](\d{1,2})[-/](\d{1,2})\z}
    NUMERIC_WITH_YEAR = %r{\A(\d{1,2})[-/](\d{1,2})[-/](\d{2}|\d{4})\z}
    BARE_MONTH_DAY = %r{\A(\d{1,2})[-/](\d{1,2})\z}

    # All-numeric forms: YYYY-MM-DD (or YYYY/MM/DD, unambiguous), MM/DD/YYYY
    # (or /YY), and bare MM/DD — the latter two ambiguous between
    # month-first and day-first regardless of year length, resolved by
    # `date_order`.
    def parse_numeric(s, today, date_order)
      case s
      when ISO_LIKE
        Date.new($1.to_i, $2.to_i, $3.to_i)
      when NUMERIC_WITH_YEAR
        month, day = order_parts($1.to_i, $2.to_i, date_order)
        Date.new(normalize_year($3), month, day)
      when BARE_MONTH_DAY
        month, day = order_parts($1.to_i, $2.to_i, date_order)
        d = Date.new(today.year, month, day)
        # Bare month-day in the past rolls forward a year — same nil-not-clamp
        # rule as build_month_date, so Feb 29 behaves identically either way.
        d < today ? Date.new(today.year + 1, month, day) : d
      end
    end

    def order_parts(first, second, date_order)
      date_order == :dmy ? [second, first] : [first, second]
    end

    # A 2-digit year is always in the 2000s — every caller here is scheduling
    # a task, so a past century is never the intended meaning.
    def normalize_year(str)
      n = str.to_i
      n < 100 ? 2000 + n : n
    end
  end
end
