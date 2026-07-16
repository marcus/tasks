# frozen_string_literal: true

require_relative "dates"
require_relative "temporal_value"

module Tasks
  module TemporalParser
    # A bare digit is not a time: "fri 5" must be rejected, not stored as
    # 05:00. Minutes may be omitted only when a meridiem disambiguates (5pm).
    TIME_TOKEN = /(?:noon|midnight|(?:[01]?\d|2[0-3]):[0-5]\d(?:am|pm)?|(?:1[0-2]|0?[1-9])(?:am|pm))/i

    module_function

    def parse(expression, today:, timezone: nil, floating: false, fold: 0, context: nil)
      input = expression.to_s.strip
      return nil if input.empty?
      raise ArgumentError, "--timezone and --floating are mutually exclusive" if timezone && floating

      date_text, local = split(input)
      date = Dates.parse_when(date_text, today: today)
      return nil unless date
      if !local && (timezone || floating || fold.to_i == 1)
        raise ArgumentError, "a time is required with --timezone, --floating, or --fold"
      end

      value = TemporalValue.new(date: date, local_time: local,
                                timezone: (floating ? nil : timezone), fold: fold)
      value.instant(context) if local && context
      value
    end

    def split(input)
      normalized = input.sub(/\s+at\s+/i, " ")
      if (match = normalized.match(/\A(.+?)(?:[ T])(#{TIME_TOKEN})\z/i))
        [match[1], normalize_time(match[2])]
      else
        [normalized, nil]
      end
    end

    def normalize_time(token)
      value = token.downcase
      return "12:00" if value == "noon"
      return "00:00" if value == "midnight"
      match = value.match(/\A(\d{1,2})(?::(\d{2}))?(am|pm)?\z/) or raise ArgumentError, "invalid time #{token.inspect}"
      hour = match[1].to_i
      minute = (match[2] || "00").to_i
      meridiem = match[3]
      if meridiem
        raise ArgumentError, "invalid 12-hour time #{token.inspect}" unless hour.between?(1, 12)
        hour %= 12
        hour += 12 if meridiem == "pm"
      end
      raise ArgumentError, "invalid time #{token.inspect}" unless hour.between?(0, 23) && minute.between?(0, 59)
      format("%02d:%02d", hour, minute)
    end
  end
end
