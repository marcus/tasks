# frozen_string_literal: true

require_relative "test_helper"

class TestDates < Minitest::Test
  D = Tui::Dates
  TODAY = Date.new(2026, 7, 1) # a Wednesday

  def parse(s) = D.parse_when(s, today: TODAY)

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
end
