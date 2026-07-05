# frozen_string_literal: true

require "date"

module Tasks
  # Recurrence for task timestamps, using org-mode's native repeater cookies.
  # A cookie lives inside a SCHEDULED:/DEADLINE: bracket, after the date:
  #
  #   DEADLINE: <2026-08-01 Sat +1m>     # fixed: stored date + interval
  #   SCHEDULED: <2026-07-06 Sun .+1w>   # from-completion: today + interval
  #
  # Two axes:
  #   prefix — what the interval is measured from on completion:
  #     +   fixed        stored date + interval (one hop; may stay in the past)
  #     ++  catch-up     stored date + interval, repeated until strictly future
  #     .+  completion   today + interval
  #   unit — d(ay) / w(eek) / m(onth) / y(ear); months/years step by calendar
  #          (Date#>>), which clamps overflow (Jan 31 +1m => Feb 28), matching org.
  #
  # Parsing (input → cookie) and the next-date computation live here; the Store
  # writes the cookie into the file and rolls it forward on `done`.
  module Recur
    UNITS = %w[d w m y].freeze

    # A canonical cookie: prefix (+, ++, .+) then a positive count then a unit.
    # The count excludes zero: ++0d would loop forever in a catch-up roll, and a
    # zero interval is meaningless — so it's rejected, not clamped.
    COOKIE = /\A(\.\+|\+\+|\+)([1-9]\d*)([dwmy])\z/

    # Friendly single words → count + unit.
    WORDS = {
      "daily" => [1, "d"], "weekly" => [1, "w"], "monthly" => [1, "m"],
      "yearly" => [1, "y"], "annually" => [1, "y"]
    }.freeze

    # Unit words (singular/plural) → canonical unit letter.
    UNIT_WORDS = {
      "day" => "d", "days" => "d", "week" => "w", "weeks" => "w",
      "month" => "m", "months" => "m", "year" => "y", "years" => "y",
      "d" => "d", "w" => "w", "m" => "m", "y" => "y"
    }.freeze

    module_function

    # True when `str` is already a canonical repeater cookie.
    def cookie?(str) = str.to_s.strip.match?(COOKIE)

    # Normalize a user interval string to a canonical cookie, `:off` to clear
    # recurrence, or nil if it can't be understood.
    #
    #   ".+1w" "+2d" "++1m"        -> passthrough (validated)
    #   "weekly" "2w" "every 3 days" -> ".+…" (bare interval defaults to default_prefix)
    #   "off" / "none" / "never"   -> :off
    #
    # default_prefix picks the semantics for a bare interval (no explicit
    # prefix): ".+" (from completion) by default, "+" when the caller wants the
    # date-anchored form (`recur --from schedule`).
    def parse_interval(str, default_prefix: ".+")
      s = str.to_s.strip.downcase
      return nil if s.empty?
      return :off if %w[off none never clear no stop].include?(s)

      # Already a cookie (COOKIE guarantees a positive count): its own prefix
      # wins over default_prefix.
      if (m = s.match(COOKIE))
        return "#{m[1]}#{m[2].to_i}#{m[3]}"
      end

      count, unit =
        if (cu = WORDS[s])
          cu
        else
          parse_count_unit(s)
        end
      return nil unless count && unit && count.positive?

      "#{default_prefix}#{count}#{unit}"
    end

    # The next date for a cookie, given the stamp's current date (`from`) and
    # `today`. See the prefix table above.
    def next_date(cookie, from:, today: Date.today)
      m = cookie.to_s.strip.match(COOKIE)
      raise ArgumentError, "not a repeater cookie: #{cookie.inspect}" unless m
      prefix, n, unit = m[1], m[2].to_i, m[3]

      case prefix
      when ".+" then step(today, n, unit)
      when "+"  then step(from, n, unit)
      when "++"
        d = step(from, n, unit)
        d = step(d, n, unit) while d <= today
        d
      end
    end

    # Advance `date` by n units. Months/years use Date#>> (calendar step with
    # day-clamp); days/weeks are plain arithmetic.
    def step(date, n, unit)
      case unit
      when "d" then date + n
      when "w" then date + (7 * n)
      when "m" then date >> n
      when "y" then date >> (12 * n)
      end
    end

    # Pull a count and unit out of a bare interval like "2w", "2 weeks",
    # "every 3 days". Returns [count, unit] or [nil, nil].
    def parse_count_unit(s)
      s = s.sub(/\Aevery\s+/, "").strip
      m = s.match(/\A(\d+)\s*([a-z]+)\z/)
      return [nil, nil] unless m
      [m[1].to_i, UNIT_WORDS[m[2]]]
    end
  end
end
