# frozen_string_literal: true

require_relative "test_helper"
require "tasks/recur"

class TestRecur < Minitest::Test
  R = Tasks::Recur

  # -- parse_interval --------------------------------------------------------

  def test_parses_canonical_cookies
    assert_equal ".+1w", R.parse_interval(".+1w")
    assert_equal "+2d",  R.parse_interval("+2d")
    assert_equal "++1m", R.parse_interval("++1m")
    assert_equal ".+3y", R.parse_interval(".+3y")
  end

  def test_parses_friendly_words
    assert_equal ".+1d", R.parse_interval("daily")
    assert_equal ".+1w", R.parse_interval("weekly")
    assert_equal ".+1m", R.parse_interval("monthly")
    assert_equal ".+1y", R.parse_interval("yearly")
    assert_equal ".+1y", R.parse_interval("annually")
  end

  def test_parses_bare_and_every_intervals
    assert_equal ".+2w", R.parse_interval("2w")
    assert_equal ".+3d", R.parse_interval("3 days")
    assert_equal ".+2w", R.parse_interval("every 2 weeks")
    assert_equal ".+1m", R.parse_interval("every 1 month")
  end

  def test_bare_interval_honors_default_prefix
    assert_equal "+2w", R.parse_interval("2w", default_prefix: "+")
    assert_equal ".+2w", R.parse_interval("2w") # default
    # an explicit cookie's own prefix wins over default_prefix
    assert_equal ".+2w", R.parse_interval(".+2w", default_prefix: "+")
  end

  def test_off_synonyms
    %w[off none never clear no stop].each do |w|
      assert_equal :off, R.parse_interval(w), w
    end
  end

  def test_case_and_whitespace_insensitive
    assert_equal ".+1w", R.parse_interval("  Weekly ")
    assert_equal ".+2w", R.parse_interval("2W")
  end

  def test_rejects_garbage
    ["", "bananas", "1", "w", "2x", "+0d", ".+0w", "1.5w", "-2w"].each do |s|
      assert_nil R.parse_interval(s), s.inspect
    end
  end

  def test_cookie_predicate
    assert R.cookie?(".+1w")
    assert R.cookie?("++2d")
    refute R.cookie?("weekly")
    refute R.cookie?("2w")
  end

  # -- next_date -------------------------------------------------------------

  TODAY = Date.new(2026, 7, 4)

  def test_from_completion_anchors_on_today
    assert_equal Date.new(2026, 7, 11), R.next_date(".+1w", from: Date.new(2020, 1, 1), today: TODAY)
    assert_equal Date.new(2026, 7, 6),  R.next_date(".+2d", from: Date.new(2026, 7, 1), today: TODAY)
  end

  def test_fixed_is_a_single_hop_from_stored_date
    # +: exactly one interval added to the stored date — may still be in the past
    assert_equal Date.new(2026, 7, 9), R.next_date("+1w", from: Date.new(2026, 7, 2), today: TODAY)
    assert_equal Date.new(2020, 1, 8), R.next_date("+1w", from: Date.new(2020, 1, 1), today: TODAY)
  end

  def test_catch_up_lands_strictly_in_the_future
    # ++: keep adding until strictly after today
    d = R.next_date("++1w", from: Date.new(2026, 6, 1), today: TODAY)
    assert_operator d, :>, TODAY
    assert_equal Date.new(2026, 7, 6), d
    # already-future stored date still advances at least once
    assert_equal Date.new(2026, 7, 20), R.next_date("++1w", from: Date.new(2026, 7, 13), today: TODAY)
  end

  def test_units_days_weeks_months_years
    from = Date.new(2026, 3, 10)
    assert_equal Date.new(2026, 3, 13), R.next_date("+3d", from: from, today: TODAY)
    assert_equal Date.new(2026, 3, 24), R.next_date("+2w", from: from, today: TODAY)
    assert_equal Date.new(2026, 5, 10), R.next_date("+2m", from: from, today: TODAY)
    assert_equal Date.new(2028, 3, 10), R.next_date("+2y", from: from, today: TODAY)
  end

  def test_month_step_clamps_overflowing_day
    # Jan 31 + 1 month -> Feb 28 (org's Date#>> behavior)
    assert_equal Date.new(2026, 2, 28), R.next_date("+1m", from: Date.new(2026, 1, 31), today: TODAY)
  end

  def test_year_step_from_leap_day_clamps
    assert_equal Date.new(2029, 2, 28), R.next_date("+1y", from: Date.new(2028, 2, 29), today: TODAY)
  end

  def test_next_date_rejects_non_cookie
    assert_raises(ArgumentError) { R.next_date("weekly", from: TODAY) }
  end
end
