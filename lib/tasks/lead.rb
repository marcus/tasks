# frozen_string_literal: true

require "date"
require_relative "recur"
require_relative "timezones"

module Tasks
  # A lead time: how long before its occurrence date a task becomes visible.
  #
  #   lead    3w  2d  1m         a positive count and a calendar unit
  #           5h                 or a clock duration in hours
  #   anchor  the task's deadline if it has one, else its available-from date
  #   gate    anchor - lead, released at local midnight of that date
  #           (a clock lead releases at anchor_instant - duration exactly)
  #
  # The stored spelling is exactly `<count><unit>` with no prefix — a lead has
  # no equivalent of a repeater cookie's `+`/`.+`/`++` axis, because it is
  # measured from one stamp in one direction. Parsing accepts the same friendly
  # phrasings `Recur` does ("2 weeks", "a week", "off"), and the stepping reuses
  # `Recur.step`, so a month lead clamps exactly the way a month interval does.
  #
  # `h` is the one CLOCK unit: `5h` measures a real duration back from the
  # anchor's instant, so it is arithmetic on instants rather than on dates and
  # #gate_date cannot express it (see #clock? and #gate_instant). `m` always
  # means months — never minutes — because a lead shares its unit letters with
  # the recurrence grammar, and overloading one of them would silently change
  # what an existing stored value means.
  module Lead
    # The canonical stored form. Zero is excluded: a `0d` lead is not a lead,
    # and would read as "no window" while looking like one.
    SPAN = /\A([1-9]\d*)([dwmyh])\z/

    UNITS = %w[d w m y h].freeze

    # The unit whose gate is an instant rather than a date.
    CLOCK_UNITS = %w[h].freeze

    OFF_WORDS = Recur::OFF_WORDS

    UNIT_NAMES = Recur::UNIT_NAMES.merge("h" => "hour").freeze

    # Clock spellings a human types. `m`/`min` are deliberately ABSENT: `m` is
    # months here and in the recurrence grammar, and a lead precise to the
    # minute would be a different feature, not a spelling.
    CLOCK_WORDS = { "h" => "h", "hr" => "h", "hrs" => "h", "hour" => "h", "hours" => "h" }.freeze

    # Friendly single words → count + unit, the lead-time subset of Recur::WORDS
    # (a lead is a span, so "daily"/"weekly" would be a category error).
    WORDS = {
      "day" => [1, "d"], "week" => [1, "w"], "fortnight" => [2, "w"],
      "month" => [1, "m"], "quarter" => [3, "m"], "year" => [1, "y"]
    }.freeze

    # Words that carry no span meaning; dropped before the phrase is read, so
    # "a week before" and "2 weeks ahead" both land on the same span.
    FILLER = %w[a an the in of before ahead early earlier prior advance].freeze

    module_function

    # True when `str` is already a stored lead span in its exact canonical
    # spelling. The guard every reader uses before deriving a gate, so a
    # hand-edited value can never crash a read — Check reports it instead.
    def span?(str) = str.is_a?(String) && SPAN.match?(str)

    # Normalize user input to a canonical stored span, `:off` to clear the lead,
    # or nil if it can't be understood.
    def parse(str)
      result = parse_result(str)
      result[:error] ? nil : result[:canonical]
    end

    # parse with the rejection reason kept:
    #   => { canonical: "3w" } | { canonical: :off } | { error: "reason" }
    def parse_result(str)
      raw = str.to_s.strip
      return { error: "no lead time given" } if raw.empty?

      s = raw.downcase
      return { canonical: :off } if OFF_WORDS.include?(s)
      return { canonical: s } if SPAN.match?(s)

      count, unit = parse_phrase(s)
      return { error: "unrecognized lead time: #{raw.inspect}" } if unit.nil?
      return { error: "a lead time must be at least 1 #{UNIT_NAMES[unit]}" } unless count.positive?

      { canonical: "#{count}#{unit}" }
    end

    # A one-line human rendering of a stored span: "3 weeks", "1 day". Returns
    # nil for a blank value and echoes anything unparsable, matching
    # Recur.humanize so the two fields read the same way on every surface.
    def humanize(span)
      s = span.to_s.strip
      return nil if s.empty?

      m = s.match(SPAN)
      return s unless m

      count = m[1].to_i
      "#{count} #{UNIT_NAMES[m[2]]}#{"s" unless count == 1}"
    end

    # The same rendering with the relationship spelled out, for a sentence that
    # has to say what the span is measured against.
    def describe(span)
      human = humanize(span)
      human && "#{human} before"
    end

    # "3 weeks before — opens 2026-10-11", or just "3 weeks before" when there
    # is no anchor to resolve against yet. One rendering of the field for every
    # surface that shows a span beside the date it derives. A CLOCK span needs a
    # context to resolve its instant into a wall time; without one it renders
    # the span alone rather than guessing a zone.
    def display(span, anchor = nil, context = nil)
      human = describe(span)
      return nil unless human

      if clock?(span)
        instant = context && anchor.respond_to?(:instant) && gate_instant(anchor, span, context)
        return human unless instant

        local = Timezones.local_time(instant, context.timezone)
        return format("%s — opens %s %02d:%02d", human, local.to_date.iso8601, local.hour, local.min)
      end

      date = anchor.respond_to?(:date) ? anchor.date : anchor
      gate = date && gate_date(date, span)
      gate ? "#{human} — opens #{gate.iso8601}" : human
    end

    # The date a lead's window opens: the anchor stepped back by the span.
    # Months and years step with Date#>>, so the clamp matches recurrence
    # intervals (1m before March 31 is February 28 in a common year). Returns
    # nil for a missing anchor or an uncanonical span — a reader derives no gate
    # rather than raising on data Check will report.
    def gate_date(anchor, span)
      return nil unless anchor.is_a?(Date)

      m = span.to_s.match(SPAN)
      return nil if m.nil? || CLOCK_UNITS.include?(m[2])

      Recur.step(anchor, -m[1].to_i, m[2])
    rescue Date::Error, RangeError
      nil
    end

    # The earliest calendar date a span's window could open on, for the storable-
    # range guard. Identical to #gate_date for a calendar span; for a clock span
    # it is the date the duration could reach at worst, which is all a range
    # check needs (and needs no zone, which the write path does not have).
    def date_bound(anchor, span)
      return nil unless anchor.is_a?(Date)

      seconds = duration(span)
      return gate_date(anchor, span) unless seconds

      anchor - ((seconds / 86_400.0).ceil + 1)
    rescue Date::Error, RangeError
      nil
    end

    # True when the span measures a clock duration, whose gate is an instant no
    # date can express — every caller that needs a date has to resolve it in a
    # zone first.
    def clock?(span)
      m = span.to_s.match(SPAN)
      !m.nil? && CLOCK_UNITS.include?(m[2])
    end

    # A clock span's duration in seconds, or nil for a calendar span.
    def duration(span)
      m = span.to_s.match(SPAN)
      return nil if m.nil? || !CLOCK_UNITS.include?(m[2])

      m[1].to_i * 3_600
    end

    # The instant a clock lead's window opens: the anchor's own instant minus
    # the duration. RAW — deliberately not rebuilt into a TemporalValue, which
    # would re-resolve an ambiguous local time and could move the gate by an
    # hour across a DST fall-back. An ALL-DAY anchor resolves to the first
    # instant of its date, so `5h` before June 1 is 19:00 on May 31 local.
    def gate_instant(anchor_value, span, context)
      seconds = duration(span)
      return nil unless seconds && anchor_value.respond_to?(:instant)

      anchor_value.instant(context) - seconds
    rescue Timezones::Error, ArgumentError
      nil
    end

    # The task's anchor date: deadline first, available-from second — the same
    # precedence recurrence completion rolls by, so a task never carries two
    # notions of "its date".
    def anchor_date(deadline, scheduled) = deadline || scheduled

    private_class_method def self.parse_phrase(s)
      tokens = s.tr(",-", "  ").split(/\s+/).reject { |word| word.empty? || FILLER.include?(word) }
      # A bare count and unit can arrive glued together ("2wks") or spaced.
      tokens = tokens.flat_map do |token|
        m = token.match(/\A(\d+)([a-z]+)\z/)
        m ? [m[1], m[2]] : [token]
      end
      return [nil, nil] if tokens.empty?

      if tokens.size == 1
        singular = singular(tokens[0])
        word = WORDS[singular]
        return [1, "h"] if word.nil? && CLOCK_WORDS[singular]
        return word ? [word[0], word[1]] : [nil, nil]
      end
      return [nil, nil] unless tokens.size == 2 && tokens[0].match?(/\A\d+\z/)

      unit = Recur::UNIT_WORDS[tokens[1]] || CLOCK_WORDS[singular(tokens[1])] ||
             abbreviated_unit(tokens[1])
      [tokens[0].to_i, unit]
    end

    # "wks"/"yrs"/"mos" and friends — spellings a human types that Recur's own
    # table (which reads schedule phrases, not spans) has no reason to carry.
    private_class_method def self.abbreviated_unit(word)
      case singular(word)
      when "dy" then "d"
      when "wk" then "w"
      when "mo", "mon", "mth" then "m"
      when "yr" then "y"
      end
    end

    private_class_method def self.singular(word)
      word.end_with?("s") ? word[0..-2] : word
    end

  end
end
