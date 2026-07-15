# frozen_string_literal: true

# Subprocess-only clock used by CLI boundary tests. Each Date.today call takes
# the next comma-separated ISO date, then keeps returning the last one.
require "date"

if (raw = ENV["TASKS_TEST_TODAY_SEQUENCE"])
  sequence = raw.split(",").map { |value| Date.iso8601(value) }
  Date.define_singleton_method(:today) do
    @tasks_test_today_sequence ||= sequence.dup
    @tasks_test_today_last = @tasks_test_today_sequence.shift || @tasks_test_today_last
  end
end
