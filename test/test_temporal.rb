# frozen_string_literal: true

require_relative "test_helper"
require "tasks/temporal_context"
require "tasks/temporal_parser"

class TestTemporal < Minitest::Test
  TODAY = Date.new(2026, 7, 1)

  def context(zone = "America/Los_Angeles", now: Time.utc(2026, 7, 20, 16))
    Tasks::TemporalContext.new(now: now, timezone: zone)
  end

  def test_parser_preserves_all_day_and_accepts_bounded_times
    all_day = Tasks::TemporalParser.parse("tomorrow", today: TODAY)
    assert all_day.all_day?
    assert_equal Date.new(2026, 7, 2), all_day.date

    { "today 5pm" => "17:00", "tomorrow at 09:30" => "09:30",
      "fri noon" => "12:00", "2026-07-20T17:00" => "17:00",
      "2026-07-20 midnight" => "00:00" }.each do |input, expected|
      assert_equal expected, Tasks::TemporalParser.parse(input, today: TODAY).local_time
    end
  end

  def test_parser_rejects_a_bare_digit_as_a_time
    # "fri 5" must not silently become 05:00; minutes may be omitted only
    # when a meridiem disambiguates.
    assert_nil Tasks::TemporalParser.parse("tomorrow 5", today: TODAY)
    assert_nil Tasks::TemporalParser.parse("fri 17", today: TODAY)
    assert_equal "17:00", Tasks::TemporalParser.parse("fri 5pm", today: TODAY).local_time
    assert_equal "17:00", Tasks::TemporalParser.parse("fri 17:00", today: TODAY).local_time
  end

  def test_parser_requires_a_time_for_floating_flag
    assert_raises(ArgumentError) do
      Tasks::TemporalParser.parse("2026-07-20", today: TODAY, floating: true)
    end
  end

  def test_parser_rejects_bare_time_and_mutually_exclusive_modes
    assert_nil Tasks::TemporalParser.parse("9am", today: TODAY)
    assert_raises(ArgumentError) do
      Tasks::TemporalParser.parse("tomorrow 9am", today: TODAY,
                                  timezone: "Etc/UTC", floating: true)
    end
  end

  def test_floating_value_follows_evaluation_zone
    value = Tasks::TemporalParser.parse("2026-07-20 9am", today: TODAY)
    assert_equal Time.utc(2026, 7, 20, 16), value.instant(context("America/Los_Angeles"))
    assert_equal Time.utc(2026, 7, 20, 8), value.instant(context("Europe/London"))
  end

  def test_fixed_value_keeps_instant_when_display_zone_changes
    value = Tasks::TemporalParser.parse("2026-07-20 5pm", today: TODAY,
                                        timezone: "Europe/London")
    utc = value.instant(context("Etc/UTC"))
    assert_equal Time.utc(2026, 7, 20, 16), utc
    assert_equal utc, value.instant(context("America/Los_Angeles"))
    assert_equal({ date: Date.new(2026, 7, 20), local: "09:00",
                   timezone: "America/Los_Angeles" }, value.projected(context))
  end

  def test_all_day_due_boundary_is_next_local_date_not_stored_midnight
    value = Tasks::TemporalValue.new(date: "2026-07-20")
    before = context(now: Time.utc(2026, 7, 21, 6, 59))
    after = context(now: Time.utc(2026, 7, 21, 7, 1))
    refute value.overdue?(before)
    assert value.overdue?(after)
    assert_nil value.time_metadata
  end

  def test_timed_deadline_becomes_overdue_immediately_after_its_exact_minute
    value = Tasks::TemporalValue.new(date: "2026-07-20", local_time: "09:00",
                                     timezone: "America/Los_Angeles")

    refute value.overdue?(context(now: Time.utc(2026, 7, 20, 15, 59)))
    refute value.overdue?(context(now: Time.utc(2026, 7, 20, 16, 0)))
    assert value.overdue?(context(now: Time.utc(2026, 7, 20, 16, 0, 1)))
  end

  def test_gap_is_rejected_and_fold_round_trips_both_instants
    assert_raises(Tasks::Timezones::NonexistentLocalTime) do
      Tasks::TemporalValue.new(date: "2026-03-08", local_time: "02:30",
                               timezone: "America/Los_Angeles")
    end

    earlier = Tasks::TemporalValue.new(date: "2026-11-01", local_time: "01:30",
                                       timezone: "America/Los_Angeles")
    later = Tasks::TemporalValue.new(date: "2026-11-01", local_time: "01:30",
                                     timezone: "America/Los_Angeles", fold: 1)
    assert_equal 3_600, later.instant(context) - earlier.instant(context)
    assert_equal 1, later.time_metadata.fetch("fold")
  end

  def test_unknown_zone_and_abbreviation_are_rejected
    assert_raises(Tasks::Timezones::Error) { Tasks::Timezones.get("Mars/Olympus") }
    assert_raises(Tasks::Timezones::Error) { Tasks::Timezones.get("PST") }
  end

  def test_non_hour_offset_zone
    value = Tasks::TemporalValue.new(date: "2026-07-20", local_time: "09:00",
                                     timezone: "Asia/Kathmandu")
    assert_equal Time.utc(2026, 7, 20, 3, 15), value.instant(context)
  end
end
