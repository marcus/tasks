# frozen_string_literal: true

require "date"
require_relative "recur"

module Tasks
  # A lead time: how long before its occurrence date a task becomes visible.
  #
  #   lead    3w  2d  1m         a positive count and a calendar unit
  #   anchor  the task's deadline if it has one, else its available-from date
  #   gate    anchor - lead, released at local midnight of that date
  #
  # The stored spelling is exactly `<count><unit>` with no prefix — a lead has
  # no equivalent of a repeater cookie's `+`/`.+`/`++` axis, because it is
  # measured from one stamp in one direction. Parsing accepts the same friendly
  # phrasings `Recur` does ("2 weeks", "a week", "off"), and the stepping reuses
  # `Recur.step`, so a month lead clamps exactly the way a month interval does.
  #
  # Clock units (`5h`) are planned but not accepted yet — see docs/plans/active/
  # recurring-lead-time.md, "Planned clock units". The parser names `h` in its
  # rejection rather than lumping it in with "unrecognized" so the follow-up can
  # land without changing what a valid lead means, and #gate_date's caller keeps
  # the gate instant-shaped for the same reason.
  module Lead
    # The canonical stored form. Zero is excluded: a `0d` lead is not a lead,
    # and would read as "no window" while looking like one.
    SPAN = /\A([1-9]\d*)([dwmy])\z/

    UNITS = %w[d w m y].freeze

    OFF_WORDS = Recur::OFF_WORDS

    UNIT_NAMES = Recur::UNIT_NAMES

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
      return clock_rejection(s, raw) if unit.nil? && clock_shaped?(s)
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
    # surface that shows a span beside the date it derives.
    def display(span, anchor = nil)
      human = describe(span)
      return nil unless human

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
      return nil unless m

      Recur.step(anchor, -m[1].to_i, m[2])
    rescue Date::Error, RangeError
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
        word = WORDS[singular(tokens[0])]
        return word ? [word[0], word[1]] : [nil, nil]
      end
      return [nil, nil] unless tokens.size == 2 && tokens[0].match?(/\A\d+\z/)

      unit = Recur::UNIT_WORDS[tokens[1]] || abbreviated_unit(tokens[1])
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

    # `h` parses as a shape but is not a lead time yet. Naming it keeps the
    # rejection honest — "unrecognized" would read as "never coming".
    private_class_method def self.clock_shaped?(s) = s.match?(/\A\d+\s*(h|hr|hrs|hour|hours)\z/)

    private_class_method def self.clock_rejection(_s, raw)
      { error: "lead times are whole days, weeks, months, or years for now — " \
               "#{raw.inspect} names an hour lead, which isn't supported yet" }
    end
  end
end
