# frozen_string_literal: true

require_relative "test_helper"
require "tasks/recur"
require "tasks/temporal_value"
require "tasks/temporal_context"

class TestRecur < Minitest::Test
  R = Tasks::Recur

  # -- parse (intervals) -----------------------------------------------------

  def test_parses_canonical_cookies
    assert_equal ".+1w", R.parse(".+1w")
    assert_equal "+2d",  R.parse("+2d")
    assert_equal "++1m", R.parse("++1m")
    assert_equal ".+3y", R.parse(".+3y")
  end

  def test_parses_friendly_words
    assert_equal ".+1d", R.parse("daily")
    assert_equal ".+1w", R.parse("weekly")
    assert_equal ".+1m", R.parse("monthly")
    assert_equal ".+1y", R.parse("yearly")
    assert_equal ".+1y", R.parse("annually")
  end

  def test_parses_bare_and_every_intervals
    assert_equal ".+2w", R.parse("2w")
    assert_equal ".+3d", R.parse("3 days")
    assert_equal ".+2w", R.parse("every 2 weeks")
    assert_equal ".+1m", R.parse("every 1 month")
  end

  def test_bare_interval_honors_default_prefix
    assert_equal "+2w", R.parse("2w", default_prefix: "+")
    assert_equal ".+2w", R.parse("2w") # default
    # an explicit cookie's own prefix wins over default_prefix
    assert_equal ".+2w", R.parse(".+2w", default_prefix: "+")
  end

  def test_off_synonyms
    %w[off none never clear no stop].each do |w|
      assert_equal :off, R.parse(w), w
    end
  end

  def test_case_and_whitespace_insensitive
    assert_equal ".+1w", R.parse("  Weekly ")
    assert_equal ".+2w", R.parse("2W")
  end

  def test_rejects_garbage
    ["", "bananas", "1", "w", "2x", "+0d", ".+0w", "1.5w", "-2w"].each do |s|
      assert_nil R.parse(s), s.inspect
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

  def test_catch_up_walks_the_series_up_to_today
    # ++: keep adding until the series reaches today
    d = R.next_date("++1w", from: Date.new(2026, 6, 1), today: TODAY)
    assert_operator d, :>=, TODAY
    assert_equal Date.new(2026, 7, 6), d
    # already-future stored date still advances at least once
    assert_equal Date.new(2026, 7, 20), R.next_date("++1w", from: Date.new(2026, 7, 13), today: TODAY)
  end

  # The date-only projection stops where the completion path's own fast-forward
  # stops — at today, not past it. An all-day stamp landing on the completion
  # day is still ahead by its end-of-day boundary, so `done` writes that day and
  # every preview built on next_date/occurrences has to name it.
  def test_catch_up_projection_can_land_on_today
    assert_equal TODAY, R.next_date("++1d", from: TODAY - 1, today: TODAY)
    assert_equal TODAY, R.next_date("++1w", from: TODAY - 7, today: TODAY)
    assert_equal TODAY, R.next_date("++1w", from: TODAY - 28, today: TODAY)
    assert_equal TODAY, R.next_date("++1m", from: TODAY << 1, today: TODAY)

    # Projections chain from each landing, so the series keeps stepping.
    assert_equal [TODAY, TODAY + 7, TODAY + 14],
                 R.occurrences("++1w", from: TODAY - 7, today: TODAY, count: 3)
  end

  # The date-only projection and the temporal write must agree for an all-day
  # stamp; the timed case below is the documented exception.
  def test_catch_up_projection_agrees_with_the_temporal_roll
    context = Tasks::TemporalContext.new(now: Time.utc(2026, 7, 4, 12), timezone: "Etc/UTC")
    %w[++1d ++1w ++1m].each do |cookie|
      value = Tasks::TemporalValue.new(date: (TODAY - 28).iso8601)
      assert_equal R.next_temporal_date(cookie, value: value, kind: :deadline, context: context),
                   R.next_date(cookie, from: TODAY - 28, today: TODAY),
                   "#{cookie}: preview and roll disagree"
    end
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

  # -- next_temporal_date ------------------------------------------------------

  # A catch-up roll walks its series to today with plain date math before the
  # civil-time loop runs, so a stamp thousands of hops stale still lands. The
  # loop's iterations are reserved for real skips (DST gaps, vetoes).
  def test_catch_up_rolls_from_a_stamp_thousands_of_hops_stale
    context = Tasks::TemporalContext.new(now: Time.utc(2030, 6, 1, 12), timezone: "Etc/UTC")
    value = Tasks::TemporalValue.new(date: "2026-01-31") # 1_582 daily hops behind
    assert_equal Date.new(2030, 6, 1),
                 R.next_temporal_date("++1d", value: value, kind: :deadline, context: context)
    # 2026-01-31 and 2030-06-01 are both Saturdays, so the weekly series lands
    # exactly on the completion day — still ahead by its end-of-day boundary.
    assert_equal Date.new(2030, 6, 1),
                 R.next_temporal_date("++1w", value: value, kind: :deadline, context: context)
    # Monthly hops clamp to the 28th at the first short month and stay there.
    assert_equal Date.new(2030, 6, 28),
                 R.next_temporal_date("++1m", value: value, kind: :deadline, context: context)
  end

  # The fast-forward stops *at* today, never past it: an all-day stamp on the
  # completion day is still ahead by its end-of-day boundary, and a timed one is
  # judged by its local time. Both stay the boundary comparison's call.
  def test_catch_up_still_offers_a_candidate_landing_on_today
    context = Tasks::TemporalContext.new(now: Time.utc(2026, 7, 20, 0, 30), timezone: "Etc/UTC")
    value = Tasks::TemporalValue.new(date: "2026-07-19", local_time: "23:00",
                                     timezone: "Etc/UTC")
    assert_equal Date.new(2026, 7, 20),
                 R.next_temporal_date("++1d", value: value, kind: :deadline, context: context)

    passed = Tasks::TemporalValue.new(date: "2026-07-19", local_time: "00:05",
                                      timezone: "Etc/UTC")
    assert_equal Date.new(2026, 7, 21),
                 R.next_temporal_date("++1d", value: passed, kind: :deadline, context: context),
                 "a candidate already past by local time advances again"
  end

  def test_catch_up_from_a_stale_stamp_still_honors_a_veto
    context = Tasks::TemporalContext.new(now: Time.utc(2030, 6, 1, 12), timezone: "Etc/UTC")
    value = Tasks::TemporalValue.new(date: "2026-01-31")
    result = R.next_temporal_date("++1d", value: value, kind: :deadline, context: context) do |date|
      date.day > 4
    end
    assert_equal Date.new(2030, 6, 5), result
  end
end
