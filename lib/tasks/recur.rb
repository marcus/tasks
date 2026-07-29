# frozen_string_literal: true

require "date"
require_relative "timezones"

module Tasks
  # Recurrence for task timestamps. Two stored shapes share one field:
  #
  #   interval cookie    .+1w  +2d  ++1m       advance by a fixed span
  #   calendar schedule  w:mon  2w:mon  m:15   advance to the next matching date
  #                      m:last  m:2tue  y:07-04
  #
  # Interval cookies are org-mode repeater cookies and live inside a
  # SCHEDULED:/DEADLINE: bracket, after the date:
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
  # Calendar schedules need only two prefixes, because "from completion" and
  # "catch-up" coincide when the occurrences are calendar-fixed:
  #     (none) catch-up  next matching date strictly after today
  #     +      one-hop   next matching date strictly after the stored date
  # `.+`/`++` on a calendar schedule are rejected.
  #
  # Parsing (input → stored form), humanizing, and the next-date computation
  # live here; the Store writes the value into the file and rolls it forward on
  # `done`.
  module Recur
    UNITS = %w[d w m y].freeze

    # A canonical cookie: prefix (+, ++, .+) then a positive count then a unit.
    # The count excludes zero: ++0d would loop forever in a catch-up roll, and a
    # zero interval is meaningless — so it's rejected, not clamped.
    COOKIE = /\A(\.\+|\+\+|\+)([1-9]\d*)([dwmy])\z/

    # Anything shaped like a calendar schedule, so a malformed one reports its
    # own reason instead of falling through to natural-phrase parsing. Natural
    # phrases never contain a colon.
    CALENDAR_SHAPE = /\A(?:\.\+|\+\+|\+)?\d*[wmy]:/
    CALENDAR = /\A(\.\+|\+\+|\+)?(\d+)?([wmy]):(.+)\z/

    OFF_WORDS = %w[off none never clear no stop].freeze

    # Friendly single words → count + unit.
    WORDS = {
      "daily" => [1, "d"], "weekly" => [1, "w"], "monthly" => [1, "m"],
      "yearly" => [1, "y"], "annually" => [1, "y"],
      "biweekly" => [2, "w"], "fortnightly" => [2, "w"], "quarterly" => [3, "m"]
    }.freeze

    # Unit words (singular/plural) → canonical unit letter.
    UNIT_WORDS = {
      "day" => "d", "days" => "d", "week" => "w", "weeks" => "w",
      "month" => "m", "months" => "m", "year" => "y", "years" => "y",
      "d" => "d", "w" => "w", "m" => "m", "y" => "y"
    }.freeze

    # A bare unit word standing alone means one of it ("every week").
    BARE_UNITS = { "day" => "d", "week" => "w", "month" => "m", "year" => "y" }.freeze

    UNIT_NAMES = { "d" => "day", "w" => "week", "m" => "month", "y" => "year" }.freeze

    DAYS = %w[mon tue wed thu fri sat sun].freeze
    DAY_INDEX = DAYS.each_with_index.to_h.freeze
    WEEKDAY_SET = %w[mon tue wed thu fri].freeze
    WEEKEND_SET = %w[sat sun].freeze

    DAY_FULL = %w[Monday Tuesday Wednesday Thursday Friday Saturday Sunday].freeze
    DAY_SHORT = %w[Mon Tue Wed Thu Fri Sat Sun].freeze

    DAY_ALIASES = begin
      table = {}
      %w[monday tuesday wednesday thursday friday saturday sunday].each_with_index do |full, i|
        abbr = DAYS[i]
        [full, "#{full}s", abbr, "#{abbr}s"].each { |key| table[key] = abbr }
      end
      table.merge!("tues" => "tue", "thur" => "thu", "thurs" => "thu", "weds" => "wed")
      table.freeze
    end

    MONTH_FULL = %w[January February March April May June July August September
                    October November December].freeze

    MONTH_ALIASES = begin
      table = {}
      MONTH_FULL.each_with_index do |full, i|
        key = full.downcase
        table[key] = i + 1
        table[key[0, 3]] = i + 1
      end
      table["sept"] = 9
      table.freeze
    end

    # Words carrying no schedule meaning; dropped before the phrase is read.
    FILLER = %w[on of the each every a an in at and].freeze

    # A trailing scope word ("the 15th of the month") that qualifies the spec
    # rather than adding to it.
    QUALIFIERS = %w[week weeks month months year years].freeze

    WORD_ORDINALS = {
      "first" => 1, "second" => 2, "third" => 3, "fourth" => 4, "fifth" => 5, "last" => :last
    }.freeze

    # How many month/year cycles the occurrence search walks before declaring a
    # schedule unreachable from its anchor. Generous: a `m:5fri` skip needs a
    # handful, a `y:02:5fri` one can need ~30.
    CYCLE_LIMIT = 500

    module_function

    # True when `str` is already a stored recurrence value: an interval cookie
    # or a calendar schedule in its exact canonical spelling.
    def cookie?(str)
      s = str.to_s.strip
      s.match?(COOKIE) || !schedule(s).nil?
    end

    # True when `str` is a stored calendar schedule.
    def calendar?(str) = !schedule(str.to_s.strip).nil?

    # Normalize user input to a canonical stored value, `:off` to clear
    # recurrence, or nil if it can't be understood.
    #
    #   ".+1w" "+2d" "++1m"          -> passthrough (validated)
    #   "weekly" "2w" "every 3 days" -> ".+…" (bare interval takes default_prefix)
    #   "w:mon" "every monday"       -> "w:mon"
    #   "off" / "none" / "never"     -> :off
    #
    # default_prefix picks the semantics for a bare *interval* (no explicit
    # prefix): ".+" (from completion) by default, "+" when the caller wants the
    # date-anchored form (`recur --from schedule`). Calendar schedules carry
    # their own prefix and ignore it — bare calendar input is catch-up.
    def parse(str, default_prefix: ".+")
      result = parse_result(str, default_prefix: default_prefix)
      result[:error] ? nil : result[:canonical]
    end

    # The interval-only half of `parse`, for callers that cannot yet handle a
    # calendar schedule. Calendar input parses cleanly and is then declined, so
    # a surface that has not opted in behaves exactly as it did before.
    def parse_interval(str, default_prefix: ".+")
      canonical = parse(str, default_prefix: default_prefix)
      return canonical if canonical.nil? || canonical == :off || canonical.match?(COOKIE)
      nil
    end

    # parse with the rejection reason kept. Calendar results also carry the
    # parsed :schedule, which the occurrence math and humanizer reuse.
    #   => { canonical: "w:mon", schedule: {…} } | { canonical: :off } |
    #      { error: "reason" }
    def parse_result(str, default_prefix: ".+")
      raw = str.to_s.strip
      return { error: "no schedule given" } if raw.empty?

      s = raw.downcase
      return { canonical: :off } if OFF_WORDS.include?(s)

      # Already a cookie (COOKIE guarantees a positive count): its own prefix
      # wins over default_prefix.
      if (m = s.match(COOKIE))
        return { canonical: "#{m[1]}#{m[2].to_i}#{m[3]}" }
      end

      # Parsing reads the downcased form; rejections quote `raw`, so what the
      # caller sees echoed back is exactly what they typed.
      return canonical_calendar(s, echo: raw) if s.match?(CALENDAR_SHAPE)

      parse_natural(s, default_prefix: default_prefix, echo: raw)
    end

    # A one-line human rendering of a stored value: "every Mon, Wed, Fri",
    # "monthly on the 2nd Tuesday", "every 2 weeks from completion". Returns nil
    # for a blank value and echoes anything unparsable.
    def humanize(cookie)
      s = cookie.to_s.strip
      return nil if s.empty?

      if (m = s.match(COOKIE))
        n = m[2].to_i
        every = n == 1 ? "every #{UNIT_NAMES[m[3]]}" : "every #{n} #{UNIT_NAMES[m[3]]}s"
        return case m[1]
               when ".+" then "#{every} from completion"
               when "+"  then "#{every} from the scheduled date"
               else "#{every} from the scheduled date (catching up)"
               end
      end

      sched = schedule(s)
      return s unless sched

      body = case sched[:kind]
             when :weekly then humanize_weekly(sched)
             when :monthly then humanize_monthly(sched)
             else humanize_yearly(sched)
             end
      sched[:prefix] == "+" ? "#{body} (one hop)" : body
    end

    # Parse, normalize, and project — without touching the store. The
    # discoverability contract every surface renders.
    #
    #   { input:, canonical:, human:, next: [Date, …] }   understood and projected
    #   { input:, canonical:, human:, next: [], error: }  understood, but never
    #                                                     fires from this anchor
    #   { input:, error: "reason" }                       not understood
    #
    # The middle shape matters: whether a schedule ever fires depends on the
    # anchor, not on the schedule alone (`2y:02:5fri` needs a leap February on
    # the right parity), so a projection failure must still identify what was
    # typed rather than masquerading as a parse error.
    #
    # `from` is the stamp the projection is anchored on (parity anchor for
    # `Nw`/`Nm`/`Ny` forms); it defaults to today in `context`'s zone.
    def explain(input, context: nil, count: 5, from: nil)
      today = context ? context.local_date : Date.today
      wanted = count.to_i.clamp(0, 50)
      result = parse_result(input)
      return { input: input.to_s, error: result[:error] } if result[:error]

      canonical = result[:canonical]
      if canonical == :off
        return { input: input.to_s, canonical: nil, human: "no recurrence", next: [] }
      end

      begin
        anchor = from.is_a?(Date) ? from : (from ? Date.iso8601(from.to_s) : today)
      rescue Date::Error, ArgumentError
        return { input: input.to_s, error: "stamp must be a real YYYY-MM-DD date: #{from.inspect}" }
      end

      payload = { input: input.to_s, canonical: canonical, human: humanize(canonical) }
      begin
        payload.merge(next: occurrences(canonical, from: anchor, today: today, count: wanted))
      rescue ArgumentError => error
        payload.merge(next: [], error: error.message)
      end
    end

    # The next `count` dates a stored value would fire on, starting from the
    # stamp `from` and the clock date `today`.
    def occurrences(cookie, from:, today: Date.today, count: 5)
      dates = []
      cursor_from = from
      cursor_today = today
      count.times do
        date = next_date(cookie, from: cursor_from, today: cursor_today)
        dates << date
        cursor_from = date
        cursor_today = date
      end
      dates
    end

    # The next date for a stored value, given the stamp's current date (`from`)
    # and `today`. See the prefix tables above.
    def next_date(cookie, from:, today: Date.today)
      s = cookie.to_s.strip
      if (m = s.match(COOKIE))
        prefix, n, unit = m[1], m[2].to_i, m[3]
        return case prefix
               when ".+" then step(today, n, unit)
               when "+"  then step(from, n, unit)
               else
                 d = step(from, n, unit)
                 d = step(d, n, unit) while d <= today
                 d
               end
      end

      sched = schedule(s)
      raise ArgumentError, "not a repeater cookie: #{cookie.inspect}" unless sched

      after = sched[:prefix] == "+" ? from : [from, today].max
      occurrence_after(sched, anchor: from, after: after)
    end

    # Exact civil-time counterpart used by recurrence completion and previews.
    # The caller supplies whether the value is a deadline or available-from
    # stamp because all-day boundaries differ. A validation block can veto a
    # candidate when a paired field would land in a DST gap.
    def next_temporal_date(cookie, value:, kind:, context:)
      advance, candidate, require_future = temporal_plan(cookie.to_s.strip, value: value, context: context)

      1_000.times do
        begin
          candidate_value = value.with_date(candidate)
          boundary = kind.to_sym == :deadline ? candidate_value.due_boundary(context) :
                                                candidate_value.release_instant(context)
          valid = !block_given? || yield(candidate)
          return candidate if valid && (!require_future || boundary > context.now)
        rescue Timezones::Error, ArgumentError
          # A nonexistent civil candidate advances to the following occurrence.
        end
        candidate = advance.call(candidate)
      end
      raise ArgumentError, "recurrence could not find a valid local date/time"
    end

    # The first candidate, how to step past a rejected one, and whether the
    # result must land strictly in the future. Shared by both stored shapes so
    # the DST/validation loop above has one body.
    def temporal_plan(s, value:, context:)
      if (m = s.match(COOKIE))
        prefix, count, unit = m[1], m[2].to_i, m[3]
        advance = ->(date) { step(date, count, unit) }
        base = prefix == ".+" ? local_today(value, context) : value.date
        candidate = advance.call(base)
        if prefix == "++"
          # Walk a stale catch-up series up to the current day with plain date
          # math first. The loop above is bounded, and its iterations are for
          # genuine skips (DST gaps, vetoes) — a stamp years behind would
          # otherwise exhaust them on hops that only ever fail the future test.
          # Stopping *at* today rather than past it is deliberate: a candidate
          # landing on today can still be future by its local time, which is the
          # boundary comparison's call to make, not this one's.
          today = local_today(value, context)
          candidate = advance.call(candidate) while candidate < today
        end
        return [advance, candidate, prefix == "++"]
      end

      sched = schedule(s)
      raise ArgumentError, "not a repeater cookie: #{s.inspect}" unless sched

      catch_up = sched[:prefix] != "+"
      anchor = value.date
      after = catch_up ? [anchor, local_today(value, context)].max : anchor
      [->(date) { occurrence_after(sched, anchor: anchor, after: date) },
       occurrence_after(sched, anchor: anchor, after: after), catch_up]
    end

    def local_today(value, context) = Timezones.local_time(context.now, value.effective_zone(context)).to_date

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

    # -- stored calendar schedules --------------------------------------------

    # The parsed schedule for an exactly-canonical stored value, else nil.
    # Strict on purpose: this is what validates what is already on disk.
    def schedule(str)
      s = str.to_s
      return nil unless s.match?(CALENDAR_SHAPE)
      parsed = canonical_calendar(s)
      return nil if parsed[:error]
      parsed[:canonical] == s ? parsed[:schedule] : nil
    end

    # Validate and normalize a canonical-grammar calendar schedule. `echo` is
    # the spelling quoted in rejections (the caller's, not the downcased one).
    def canonical_calendar(s, echo: s)
      m = s.match(CALENDAR)
      return { error: "unrecognized schedule: #{echo.inspect}" } unless m

      prefix = m[1].to_s
      if prefix == ".+" || prefix == "++"
        return { error: "#{prefix.inspect} is an interval prefix; a calendar schedule already " \
                        "advances to the next occurrence after today — drop the prefix, or use " \
                        "\"+\" to advance one occurrence at a time" }
      end

      interval = (m[2] || "1").to_i
      return { error: "recurrence interval must be at least 1" } if interval < 1

      body = m[4]
      case m[3]
      when "w" then canonical_weekly(prefix, interval, body)
      when "m" then canonical_monthly(prefix, interval, body)
      else canonical_yearly(prefix, interval, body)
      end
    end

    def canonical_weekly(prefix, interval, body)
      parts = body.split(",", -1)
      return { error: "weekly schedules need at least one day, e.g. \"w:mon\"" } if parts.any?(&:empty?)

      days = parts.map do |part|
        DAY_ALIASES[part] || (return { error: "unknown day of week: #{part.inspect}" })
      end
      build(prefix: prefix, interval: interval, kind: :weekly, days: days)
    end

    def canonical_monthly(prefix, interval, body)
      parts = body.split(",", -1)
      return { error: "monthly schedules need at least one rule, e.g. \"m:15\"" } if parts.any?(&:empty?)

      specs = parts.map do |part|
        case part
        when /\A\d{1,2}\z/
          day = part.to_i
          return { error: "day of month must be 1–31: #{part.inspect}" } unless (1..31).cover?(day)
          day
        when "last" then :last
        when /\A(\d+|last)([a-z]+)\z/
          ordinal = Regexp.last_match(1)
          name = Regexp.last_match(2)
          weekday = DAY_ALIASES[name] || (return { error: "unknown day of week: #{name.inspect}" })
          ord = ordinal == "last" ? :last : ordinal.to_i
          unless ord == :last || (1..5).cover?(ord)
            return { error: "ordinal weekdays run from 1 to 5 or \"last\": #{part.inspect}" }
          end
          [ord, weekday]
        else
          return { error: "unrecognized monthly rule: #{part.inspect}" }
        end
      end
      build(prefix: prefix, interval: interval, kind: :monthly, specs: specs)
    end

    def canonical_yearly(prefix, interval, body)
      if (m = body.match(/\A(\d{1,2})-(\d{1,2})\z/))
        month = m[1].to_i
        day = m[2].to_i
        unless (1..12).cover?(month) && Date.valid_date?(2024, month, day)
          return { error: "invalid yearly date: #{body.inspect}" }
        end
        return build(prefix: prefix, interval: interval, kind: :yearly, month: month, day: day)
      end

      if (m = body.match(/\A(\d{1,2}):(\d+|last)([a-z]+)\z/))
        month = m[1].to_i
        return { error: "invalid month: #{m[1].inspect}" } unless (1..12).cover?(month)
        weekday = DAY_ALIASES[m[3]] || (return { error: "unknown day of week: #{m[3].inspect}" })
        ord = m[2] == "last" ? :last : m[2].to_i
        unless ord == :last || (1..5).cover?(ord)
          return { error: "ordinal weekdays run from 1 to 5 or \"last\": #{body.inspect}" }
        end
        return build(prefix: prefix, interval: interval, kind: :yearly, month: month, ord: [ord, weekday])
      end

      { error: "unrecognized yearly rule: #{body.inspect} (use \"y:07-04\" or \"y:11:3thu\")" }
    end

    # -- natural phrases -------------------------------------------------------

    def parse_natural(s, default_prefix:, echo: s)
      tokens = tokenize(s)
      return { error: "unrecognized schedule: #{echo.inspect}" } if tokens.empty?

      count, unit, spec = take_interval(tokens)
      monthly_hint = false
      if spec.size >= 2 && QUALIFIERS.include?(spec.last)
        monthly_hint = spec.pop.start_with?("month")
      end

      if spec.empty?
        return { error: "unrecognized schedule: #{echo.inspect}" } unless unit && count.positive?
        return { canonical: "#{default_prefix}#{count}#{unit}" }
      end

      return { error: "a daily schedule cannot also name calendar days" } if unit == "d"

      if (days = expand_days(spec))
        unless unit.nil? || unit == "w"
          return { error: "a list of weekdays needs a weekly schedule, e.g. \"every 2 weeks on monday\"" }
        end
        return build(prefix: "", interval: count || 1, kind: :weekly, days: days)
      end

      if spec.any? { |token| MONTH_ALIASES.key?(token) }
        unless unit.nil? || unit == "y"
          return { error: "a month name needs a yearly schedule, e.g. \"every 2 years on july 4\"" }
        end
        return natural_yearly(spec, interval: count || 1, source: echo)
      end

      unless unit.nil? || unit == "m"
        return { error: "day-of-month rules need a monthly schedule, e.g. \"every 2 months on the 15th\"" }
      end
      natural_monthly(spec, interval: count || 1, monthly_hint: monthly_hint || unit == "m", source: echo)
    end

    # Lowercase words, ordinals marked with a leading "#", filler dropped.
    # "every 2 weeks on the 1st and 15th" -> ["2", "weeks", "#1", "#15"]
    def tokenize(s)
      text = s.tr(",&/", "   ")
      text = text.gsub(/(\d+)(?:st|nd|rd|th)\b/) { "##{Regexp.last_match(1)}" }
      text = text.gsub(/(\d)([a-z])/) { "#{Regexp.last_match(1)} #{Regexp.last_match(2)}" }
      text = text.gsub(/([a-z])(\d)/) { "#{Regexp.last_match(1)} #{Regexp.last_match(2)}" }
      text.split(/\s+/).filter_map do |word|
        next nil if word.empty? || FILLER.include?(word)
        (ord = WORD_ORDINALS[word]) ? "##{ord == :last ? "last" : ord}" : word
      end
    end

    # Peel a leading interval ("2 weeks", "monthly", "week") off the tokens.
    # Returns [count, unit, remaining tokens]; count/unit are nil when absent.
    def take_interval(tokens)
      if tokens.size >= 2 && tokens[0].match?(/\A\d+\z/) && UNIT_WORDS[tokens[1]]
        [tokens[0].to_i, UNIT_WORDS[tokens[1]], tokens.drop(2)]
      elsif (word = WORDS[tokens[0]])
        [word[0], word[1], tokens.drop(1)]
      elsif (unit = BARE_UNITS[tokens[0]])
        [1, unit, tokens.drop(1)]
      else
        [nil, nil, tokens]
      end
    end

    # A day list if every token names weekdays, else nil.
    def expand_days(spec)
      spec.flat_map do |token|
        case token
        when "weekday", "weekdays" then WEEKDAY_SET
        when "weekend", "weekends" then WEEKEND_SET
        else DAY_ALIASES[token] || (return nil)
        end
      end
    end

    def natural_monthly(spec, interval:, monthly_hint:, source:)
      specs = []
      i = 0
      while i < spec.size
        token = spec[i]
        nxt = spec[i + 1]
        if (m = token.match(/\A#(\d+|last)\z/))
          ord = m[1] == "last" ? :last : m[1].to_i
          if nxt == "day"
            unless ord == :last || (1..31).cover?(ord)
              return { error: "day of month must be 1–31: #{ord}" }
            end
            specs << ord
            i += 2
          elsif nxt && (weekday = DAY_ALIASES[nxt])
            unless ord == :last || (1..5).cover?(ord)
              return { error: "ordinal weekdays run from 1 to 5 or \"last\"" }
            end
            specs << [ord, weekday]
            i += 2
          elsif ord == :last
            specs << :last
            i += 1
          else
            return { error: "day of month must be 1–31: #{ord}" } unless (1..31).cover?(ord)
            specs << ord
            i += 1
          end
        elsif token.match?(/\A\d+\z/)
          ord = token.to_i
          if nxt && (weekday = DAY_ALIASES[nxt])
            # A cardinal number before a weekday reads as a cadence ("every 2
            # tuesdays"), not as an ordinal — and the ordinal has its own
            # spelling. Only an ordinal marker or an explicit monthly scope
            # settles it.
            unless monthly_hint
              name = DAY_FULL[DAY_INDEX[weekday]].downcase
              return { error: "#{token} #{nxt} is ambiguous: write " \
                              "\"every #{token} weeks on #{name}\" for a cadence, or " \
                              "\"#{ordinal_word(ord)} #{name} of the month\" for the " \
                              "#{ordinal_word(ord)} #{name} of each month" }
            end
            return { error: "ordinal weekdays run from 1 to 5 or \"last\"" } unless (1..5).cover?(ord)
            specs << [ord, weekday]
            i += 2
          elsif monthly_hint
            return { error: "day of month must be 1–31: #{ord}" } unless (1..31).cover?(ord)
            specs << ord
            i += 1
          else
            return { error: "unrecognized schedule: #{source.inspect}" }
          end
        else
          return { error: "unrecognized schedule: #{source.inspect}" }
        end
      end
      return { error: "unrecognized schedule: #{source.inspect}" } if specs.empty?

      build(prefix: "", interval: interval, kind: :monthly, specs: specs)
    end

    def natural_yearly(spec, interval:, source:)
      positions = spec.each_index.select { |i| MONTH_ALIASES.key?(spec[i]) }
      return { error: "unrecognized schedule: #{source.inspect}" } unless positions.size == 1

      month = MONTH_ALIASES[spec[positions[0]]]
      rest = spec.reject.with_index { |_, i| i == positions[0] }

      if rest.size == 1 && (m = rest[0].match(/\A#?(\d{1,2})\z/))
        day = m[1].to_i
        unless Date.valid_date?(2024, month, day)
          return { error: "#{MONTH_FULL[month - 1]} has no day #{day}" }
        end
        return build(prefix: "", interval: interval, kind: :yearly, month: month, day: day)
      end

      if rest.size == 2 && (m = rest[0].match(/\A#?(\d+|last)\z/)) && (weekday = DAY_ALIASES[rest[1]])
        ord = m[1] == "last" ? :last : m[1].to_i
        unless ord == :last || (1..5).cover?(ord)
          return { error: "ordinal weekdays run from 1 to 5 or \"last\"" }
        end
        return build(prefix: "", interval: interval, kind: :yearly, month: month, ord: [ord, weekday])
      end

      { error: "unrecognized schedule: #{source.inspect}" }
    end

    # -- canonical form --------------------------------------------------------

    # Normalize a schedule and render its single canonical spelling.
    def build(prefix:, interval:, kind:, **rest)
      sched = { prefix: prefix.to_s, interval: interval, kind: kind }.merge(rest)
      case kind
      when :weekly
        sched[:days] = sched[:days].uniq.sort_by { |day| DAY_INDEX[day] }
      when :monthly
        sched[:specs] = sched[:specs].sort_by { |spec| spec_key(spec) }.uniq { |spec| spec_key(spec) }
      end
      { canonical: canonical_string(sched), schedule: sched }
    end

    def spec_key(spec)
      case spec
      when Integer then [0, spec, 0]
      when :last then [1, 0, 0]
      else [2, spec[0] == :last ? 6 : spec[0], DAY_INDEX[spec[1]]]
      end
    end

    def canonical_string(sched)
      count = sched[:interval] == 1 ? "" : sched[:interval].to_s
      case sched[:kind]
      when :weekly
        "#{sched[:prefix]}#{count}w:#{sched[:days].join(",")}"
      when :monthly
        "#{sched[:prefix]}#{count}m:#{sched[:specs].map { |spec| spec_string(spec) }.join(",")}"
      else
        body = if sched[:ord]
                 format("%02d:%s", sched[:month], spec_string(sched[:ord]))
               else
                 format("%02d-%02d", sched[:month], sched[:day])
               end
        "#{sched[:prefix]}#{count}y:#{body}"
      end
    end

    def spec_string(spec)
      case spec
      when Integer then spec.to_s
      when :last then "last"
      else "#{spec[0] == :last ? "last" : spec[0]}#{spec[1]}"
      end
    end

    # -- occurrence math -------------------------------------------------------

    # The first date matching `sched` strictly after `after`. `anchor` is the
    # stored stamp, which fixes the parity of every-Nth-week/month/year forms.
    def occurrence_after(sched, anchor:, after:)
      case sched[:kind]
      when :weekly then weekly_after(sched, anchor, after)
      when :monthly then monthly_after(sched, anchor, after)
      else yearly_after(sched, anchor, after)
      end
    end

    def weekly_after(sched, anchor, after)
      block = 7 * sched[:interval]
      anchor_monday = anchor - (anchor.cwday - 1)
      cycle = (after - anchor_monday).to_i.div(block)

      3.times do
        monday = anchor_monday + (block * cycle)
        sched[:days].each do |day|
          date = monday + DAY_INDEX[day]
          return date if date > after
        end
        cycle += 1
      end
      no_occurrence(sched, anchor, "#{3 * sched[:interval]} weeks")
    end

    def monthly_after(sched, anchor, after)
      interval = sched[:interval]
      anchor_month = (anchor.year * 12) + anchor.month - 1
      cycle = (((after.year * 12) + after.month - 1) - anchor_month).div(interval)

      CYCLE_LIMIT.times do
        months = anchor_month + (interval * cycle)
        year, index = months.divmod(12)
        hit = sched[:specs].filter_map { |spec| month_spec_date(spec, year, index + 1) }
                           .sort.find { |date| date > after }
        return hit if hit
        cycle += 1
      end
      no_occurrence(sched, anchor, "#{CYCLE_LIMIT * interval} months")
    end

    def yearly_after(sched, anchor, after)
      interval = sched[:interval]
      cycle = (after.year - anchor.year).div(interval)

      CYCLE_LIMIT.times do
        year = anchor.year + (interval * cycle)
        date = if sched[:ord]
                 nth_weekday(year, sched[:month], sched[:ord][0], sched[:ord][1])
               else
                 clamped(year, sched[:month], sched[:day])
               end
        return date if date && date > after
        cycle += 1
      end
      no_occurrence(sched, anchor, "#{CYCLE_LIMIT * interval} years")
    end

    # Some schedules are satisfiable only for some anchors — `2y:02:5fri` needs
    # a February with five Fridays, which only a leap year has, so an odd anchor
    # year never fires. That cannot be decided at parse time, so it surfaces
    # here, naming the schedule and the anchor that made it unreachable.
    def no_occurrence(sched, anchor, span)
      raise ArgumentError, "no occurrence of #{canonical_string(sched).inspect} within #{span} " \
                           "from #{anchor} — the schedule may never fire for this anchor"
    end

    # A monthly rule resolved inside one month, or nil when that month has no
    # such date (a 5th Friday that doesn't exist — skipped, not clamped).
    def month_spec_date(spec, year, month)
      case spec
      when Integer then clamped(year, month, spec)
      when :last then Date.new(year, month, -1)
      else nth_weekday(year, month, spec[0], spec[1])
      end
    end

    # A numeric day, clamped to the month's length (Apr 31 -> Apr 30, Feb 29 ->
    # Feb 28 in a common year), matching the Date#>> clamp intervals use.
    def clamped(year, month, day) = Date.new(year, month, [day, Date.new(year, month, -1).day].min)

    def nth_weekday(year, month, ordinal, day)
      if ordinal == :last
        last = Date.new(year, month, -1)
        return last - ((last.cwday - 1 - DAY_INDEX[day]) % 7)
      end

      first = Date.new(year, month, 1)
      date = first + ((DAY_INDEX[day] - (first.cwday - 1)) % 7) + (7 * (ordinal - 1))
      date.month == month ? date : nil
    end

    # -- humanizing ------------------------------------------------------------

    def humanize_weekly(sched)
      days = sched[:days]
      # "every weekday" reads as a set; "every 2 weeks on weekdays" reads as a
      # list, so the collective nouns pluralize when they follow "on".
      every = sched[:interval] == 1
      label =
        if days == WEEKDAY_SET then every ? "weekday" : "weekdays"
        elsif days == WEEKEND_SET then every ? "weekend" : "weekends"
        elsif days.size == 1 then DAY_FULL[DAY_INDEX[days[0]]]
        else days.map { |day| DAY_SHORT[DAY_INDEX[day]] }.join(", ")
        end
      every ? "every #{label}" : "every #{sched[:interval]} weeks on #{label}"
    end

    def humanize_monthly(sched)
      lead = sched[:interval] == 1 ? "monthly on" : "every #{sched[:interval]} months on"
      "#{lead} the #{join_words(sched[:specs].map { |spec| humanize_spec(spec) })}"
    end

    def humanize_spec(spec)
      case spec
      when Integer then ordinal_word(spec)
      when :last then "last day"
      else "#{ordinal_word(spec[0])} #{DAY_FULL[DAY_INDEX[spec[1]]]}"
      end
    end

    def humanize_yearly(sched)
      lead = sched[:interval] == 1 ? "yearly on" : "every #{sched[:interval]} years on"
      body = if sched[:ord]
               "the #{ordinal_word(sched[:ord][0])} #{DAY_FULL[DAY_INDEX[sched[:ord][1]]]} " \
                 "of #{MONTH_FULL[sched[:month] - 1]}"
             else
               "#{MONTH_FULL[sched[:month] - 1]} #{sched[:day]}"
             end
      "#{lead} #{body}"
    end

    def join_words(words)
      return words.first.to_s if words.size <= 1
      "#{words[0..-2].join(", ")} and #{words[-1]}"
    end

    def ordinal_word(n)
      return "last" if n == :last
      suffix = if (11..13).cover?(n % 100)
                 "th"
               else
                 { 1 => "st", 2 => "nd", 3 => "rd" }.fetch(n % 10, "th")
               end
      "#{n}#{suffix}"
    end
  end
end
