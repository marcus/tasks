# frozen_string_literal: true

require_relative "test_helper"

class TestDates < Minitest::Test
  D = Tui::Dates
  TODAY = Date.new(2026, 7, 1) # a Wednesday

  # Pinned to :mdy explicitly so these tests can't be affected by whatever
  # some other test file (or a future one) leaves installed via
  # Dates.configure! — that global is exercised deliberately below instead.
  def parse(s) = D.parse_when(s, today: TODAY, date_order: :mdy)

  def test_today_and_tomorrow
    assert_equal TODAY, parse("today")
    assert_equal TODAY + 1, parse("tomorrow")
  end

  def test_plus_days
    assert_equal TODAY + 3, parse("+3")
    assert_equal TODAY + 14, parse("+14")
  end

  def test_weekday_names
    assert_equal Date.new(2026, 7, 3), parse("fri")
    assert_equal Date.new(2026, 7, 3), parse("friday")
    assert_equal Date.new(2026, 7, 6), parse("mon")
  end

  def test_same_weekday_means_next_week
    assert_equal TODAY + 7, parse("wed")
  end

  def test_month_day
    assert_equal Date.new(2026, 7, 15), parse("07-15")
    assert_equal Date.new(2026, 7, 15), parse("7/15")
  end

  def test_past_month_day_rolls_to_next_year
    assert_equal Date.new(2027, 2, 1), parse("02-01")
  end

  def test_full_iso_date
    assert_equal Date.new(2026, 8, 1), parse("2026-08-01")
  end

  def test_garbage_returns_nil
    assert_nil parse("")
    assert_nil parse("someday")
    assert_nil parse("13-45")
    assert_nil parse("2026-99-99")
  end

  def test_two_letter_weekday_not_matched
    assert_nil parse("fr") # too short to be unambiguous
  end

  def test_next_week_month_year
    assert_equal TODAY + 7, parse("next week")
    assert_equal Date.new(2026, 8, 1), parse("next month")
    assert_equal Date.new(2027, 7, 1), parse("next year")
  end

  def test_next_month_clamps_short_month
    assert_equal Date.new(2026, 2, 28), parse2(Date.new(2026, 1, 31), "next month")
  end

  def test_next_year_clamps_leap_day
    assert_equal Date.new(2029, 2, 28), parse2(Date.new(2028, 2, 29), "next year")
  end

  def test_in_n_units
    assert_equal TODAY + 3, parse("in 3 days")
    assert_equal TODAY + 1, parse("in 1 day")
    assert_equal TODAY + 14, parse("in 2 weeks")
    assert_equal TODAY + 7, parse("in a week")
    assert_equal TODAY >> 6, parse("in 6 months")
    assert_equal TODAY >> 24, parse("in 2 years")
    assert_equal TODAY >> 1, parse("in a month")
    assert_equal TODAY >> 12, parse("in an year")
  end

  def test_in_n_units_rejects_garbage
    assert_nil parse("in days")
    assert_nil parse("in 3 fortnights")
    assert_nil parse("in -1 days")
  end

  def test_next_weekday_alias
    assert_equal Date.new(2026, 7, 3), parse("next fri")
    assert_equal Date.new(2026, 7, 3), parse("next friday")
  end

  def test_month_name_and_day
    assert_equal Date.new(2026, 8, 1), parse("aug 1")
    assert_equal Date.new(2026, 8, 1), parse("august 1")
    assert_equal Date.new(2026, 8, 1), parse("aug 1st")
    assert_equal Date.new(2026, 8, 22), parse("aug 22nd")
    assert_equal Date.new(2026, 8, 1), parse("aug. 1")
  end

  def test_month_name_day_first
    assert_equal Date.new(2026, 8, 1), parse("1 aug")
    assert_equal Date.new(2026, 8, 1), parse("1st august")
  end

  def test_month_name_explicit_year
    assert_equal Date.new(2026, 8, 1), parse("aug 1 2026")
    assert_equal Date.new(2026, 8, 1), parse("aug 1, 2026")
    assert_equal Date.new(2026, 8, 1), parse("aug 1,2026")
    assert_equal Date.new(2025, 8, 1), parse("aug 1 2025") # explicit past year respected
  end

  def test_month_name_bare_past_rolls_to_next_year
    assert_equal Date.new(2027, 1, 15), parse("jan 15")
  end

  def test_month_name_requires_unambiguous_abbreviation
    assert_nil parse("ju 1")   # june or july — ambiguous, rejected
    assert_nil parse("nonmonth 1")
  end

  def test_iso_date_accepts_slashes
    assert_equal Date.new(2026, 8, 1), parse("2026/08/01")
  end

  def test_numeric_date_with_year_mdy_default
    assert_equal Date.new(2026, 8, 1), parse("8/1/2026")
    assert_equal Date.new(2026, 8, 1), parse("08-01-2026")
  end

  def test_numeric_date_two_digit_year
    assert_equal Date.new(2026, 8, 1), parse("8/1/26")
  end

  def test_date_order_dmy_flips_bare_and_year_forms
    assert_equal Date.new(2026, 1, 8), D.parse_when("8/1/2026", today: TODAY, date_order: :dmy)
    assert_equal Date.new(2026, 7, 15), D.parse_when("15-07", today: TODAY, date_order: :dmy)
  end

  def test_configure_sets_process_default
    D.configure!(date_order: :dmy)
    assert_equal Date.new(2026, 1, 8), D.parse_when("8/1/2026", today: TODAY)
  ensure
    D.reset!
  end

  def test_configure_falls_back_on_invalid_value
    D.configure!(date_order: :nonsense)
    assert_equal :mdy, D.date_order
  ensure
    D.reset!
  end

  def test_garbage_month_name_returns_nil
    assert_nil parse("aug 45")     # no such day
    assert_nil parse("aug 1 abc")  # trailing junk isn't a year
  end

  def test_three_digit_year_is_rejected_not_truncated
    # A dropped digit ("2026" typo'd as "202") must not silently become
    # the year 202 — that's a real-data footgun, not a fuzzy convenience.
    assert_nil parse("8/1/202")
    assert_nil parse("8-1-202")
  end

  def test_bare_and_named_leap_day_rollover_agree
    today = Date.new(2024, 6, 1) # after this year's Feb 29, in a leap year
    # 2025 isn't a leap year, so rolling "the next Feb 29" forward doesn't
    # exist — both the numeric and month-name forms must reject it (nil),
    # neither may silently clamp to Feb 28 on a day the user didn't type.
    assert_nil D.parse_when("2/29", today: today, date_order: :mdy)
    assert_nil D.parse_when("feb 29", today: today)
  end

  def parse2(today, s) = D.parse_when(s, today: today, date_order: :mdy)
end
