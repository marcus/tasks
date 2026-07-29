# frozen_string_literal: true

require_relative "test_helper"
require "tasks/recur"
require "tasks/temporal_value"
require "tasks/temporal_context"

# Calendar schedules: the second stored shape alongside interval cookies.
# test_recur.rb covers the interval cookies these must not disturb.
class TestRecurCalendar < Minitest::Test
  R = Tasks::Recur

  def assert_table(table)
    table.each { |input, expected| assert_equal expected, yield(input), input.inspect }
  end

  # -- grammar productions ---------------------------------------------------

  def test_parses_every_canonical_production
    assert_table(
      # weekly := [N] "w:" dayset
      "w:mon" => "w:mon",
      "w:mon,wed,fri" => "w:mon,wed,fri",
      "w:sat,sun" => "w:sat,sun",
      "2w:mon" => "2w:mon",
      "4w:tue,thu" => "4w:tue,thu",
      # monthly := [N] "m:" mspec ("," mspec)*
      "m:1" => "m:1",
      "m:31" => "m:31",
      "m:1,15" => "m:1,15",
      "m:last" => "m:last",
      "m:2tue" => "m:2tue",
      "m:5fri" => "m:5fri",
      "m:lastfri" => "m:lastfri",
      "m:15,lastfri" => "m:15,lastfri",
      "3m:last" => "3m:last",
      # yearly := [N] "y:" MM "-" DD | [N] "y:" MM ":" ordday
      "y:07-04" => "y:07-04",
      "y:02-29" => "y:02-29",
      "y:11:3thu" => "y:11:3thu",
      "y:05:lastmon" => "y:05:lastmon",
      "2y:07-04" => "2y:07-04",
      # prefix := "+"
      "+w:mon" => "+w:mon",
      "+m:15" => "+m:15",
      "+2w:mon,thu" => "+2w:mon,thu",
      "+y:07-04" => "+y:07-04"
    ) { |input| R.parse(input) }
  end

  def test_parses_natural_phrases
    assert_table(
      "every monday" => "w:mon",
      "every Monday" => "w:mon",
      "mondays" => "w:mon",
      "every mon wed fri" => "w:mon,wed,fri",
      "every mon, wed, fri" => "w:mon,wed,fri",
      "weekdays" => "w:mon,tue,wed,thu,fri",
      "every weekday" => "w:mon,tue,wed,thu,fri",
      "weekends" => "w:sat,sun",
      "every week on monday" => "w:mon",
      "every 2 weeks on monday" => "2w:mon",
      "every 2 weeks on mon and thu" => "2w:mon,thu",
      "monthly on the 2nd tuesday" => "m:2tue",
      "2nd tuesday of the month" => "m:2tue",
      "2 tuesdays of the month" => "m:2tue", # an explicit monthly scope disambiguates
      "monthly on the 15th" => "m:15",
      "1st of the month" => "m:1",
      "the 1st and 15th of the month" => "m:1,15",
      "last day of the month" => "m:last",
      "2nd tuesday" => "m:2tue",
      "second tuesday of the month" => "m:2tue",
      "last friday of the month" => "m:lastfri",
      "every 2 months on the 15th" => "2m:15",
      "every july 4" => "y:07-04",
      "every july 4th" => "y:07-04",
      "4 july" => "y:07-04",
      "3rd thursday of november" => "y:11:3thu",
      "every 2 years on july 4" => "2y:07-04"
    ) { |input| R.parse(input) }
  end

  def test_natural_interval_phrases_still_produce_cookies
    # The bare-interval path is untouched by the calendar grammar.
    assert_table(
      "weekly" => ".+1w",
      "daily" => ".+1d",
      "every day" => ".+1d",
      "every month" => ".+1m",
      "every 2 weeks" => ".+2w",
      "biweekly" => ".+2w",
      "quarterly" => ".+3m"
    ) { |input| R.parse(input) }
  end

  # -- normalization ---------------------------------------------------------

  def test_synonymous_inputs_share_one_canonical_spelling
    [
      ["w:mon", ["W:MON", "1w:mon", "w:monday", "w:mondays", "every monday", " every Monday "]],
      ["w:mon,wed,fri", ["w:fri,mon,wed", "w:wed,mon,fri,mon", "every fri wed mon"]],
      ["w:mon,tue,wed,thu,fri", ["weekdays", "every weekday", "w:tue,mon,fri,thu,wed"]],
      ["2w:mon", ["2w:monday", "every 2 weeks on monday", "2W:MON"]],
      ["m:1,15", ["m:15,1", "m:15,1,15", "the 1st and 15th of the month"]],
      ["m:2tue", ["m:2tue", "2nd tuesday", "second tuesday", "m:2tuesday"]],
      ["m:lastfri", ["m:lastfri", "last friday of the month", "m:lastfriday"]],
      ["y:07-04", ["y:7-4", "y:07-4", "every july 4", "every July 4th"]],
      ["m:15,lastfri", ["m:lastfri,15"]],
    ].each do |canonical, inputs|
      inputs.each { |input| assert_equal canonical, R.parse(input), input.inspect }
    end
  end

  def test_day_sets_sort_into_week_order_not_alphabetical
    assert_equal "w:mon,tue,wed,thu,fri,sat,sun",
                 R.parse("w:sun,sat,fri,thu,wed,tue,mon")
  end

  def test_monthly_specs_sort_numeric_then_last_then_ordinal_weekdays
    assert_equal "m:1,15,last,2tue,lastfri", R.parse("m:lastfri,2tue,last,15,1")
  end

  # -- rejection with reasons ------------------------------------------------

  def test_rejects_with_reasons
    {
      ".+w:mon" => /interval prefix/,
      ".+m:15" => /interval prefix/,
      "++w:mon" => /interval prefix/,
      "++y:07-04" => /interval prefix/,
      "0w:mon" => /interval must be at least 1/,
      "w:funday" => /unknown day of week/,
      "w:mon,funday" => /unknown day of week/,
      "w:mon," => /at least one day/,
      "m:0" => /day of month must be/,
      "m:32" => /day of month must be/,
      "m:6fri" => /ordinal weekdays run from 1 to 5/,
      "m:0fri" => /ordinal weekdays run from 1 to 5/,
      "m:bananas" => /unrecognized monthly rule/,
      "y:13-01" => /invalid yearly date/,
      "y:02-30" => /invalid yearly date/,
      "y:04-31" => /invalid yearly date/,
      "y:11:6thu" => /ordinal weekdays run from 1 to 5/,
      "y:bananas" => /unrecognized yearly rule/,
      "monthly on monday" => /weekdays needs a weekly schedule/,
      "every 2 weeks on the 15th" => /monthly schedule/,
      "every 2 years on monday" => /weekly schedule/,
      "every 3 days on monday" => /daily schedule/,
      "every 2 months on july 4" => /yearly schedule/,
      "every february 30" => /February has no day 30/,
      "" => /no schedule given/,
      "bananas" => /unrecognized schedule/,
    }.each do |input, pattern|
      result = R.parse_result(input)
      assert result[:error], "expected #{input.inspect} to be rejected"
      assert_match pattern, result[:error], input.inspect
      assert_nil R.parse(input), input.inspect
    end
  end

  def test_bare_cardinal_before_a_weekday_is_ambiguous_and_rejected
    # "every 2 tuesdays" reads as a cadence, not as "the 2nd Tuesday" — and the
    # ordinal already has its own spelling, so the input is declined.
    ["every 2 tuesdays", "2 tuesdays", "every 3 mondays", "3 mon"].each do |input|
      assert_nil R.parse(input), input.inspect
      reason = R.parse_result(input)[:error]
      assert_match(/is ambiguous/, reason, input.inspect)
      assert_match(/every \d+ weeks on \w+/, reason, input.inspect)
      assert_match(/\w+ of the month/, reason, input.inspect)
    end
  end

  def test_ordinal_marked_weekdays_still_parse
    ["2nd tuesday", "second tuesday", "2nd tuesday of the month",
     "monthly on the 2nd tuesday", "2 tuesdays of the month"].each do |input|
      assert_equal "m:2tue", R.parse(input), input.inspect
    end
  end

  def test_dot_plus_prefix_on_a_calendar_form_explains_the_default
    reason = R.parse_result(".+w:mon")[:error]
    assert_match(/already advances to the next occurrence after today/, reason)
    assert_match(/"\+"/, reason)
  end

  def test_parse_interval_still_declines_calendar_schedules
    # Surfaces that have not opted into the calendar grammar keep their old
    # behavior: `check` does not yet validate calendar cookies, so nothing may
    # write one through the interval-only entry point.
    ["every monday", "w:mon", "weekdays", "m:15", "+y:07-04"].each do |input|
      refute_nil R.parse(input), input.inspect
      assert_nil R.parse_interval(input), input.inspect
    end
    assert_equal ".+1w", R.parse_interval("weekly")
    assert_equal "+2w", R.parse_interval("2w", default_prefix: "+")
    assert_equal :off, R.parse_interval("off")
  end

  def test_calendar_input_ignores_the_interval_default_prefix
    # Bare calendar input is catch-up regardless of `recur --from`; the CLI
    # rejects the combination outright once it learns the grammar.
    assert_equal "w:mon", R.parse("every monday", default_prefix: "+")
    assert_equal "w:mon", R.parse("w:mon", default_prefix: ".+")
  end

  def test_interval_rejections_are_unchanged
    ["", "bananas", "1", "w", "2x", "+0d", ".+0w", "1.5w", "-2w"].each do |input|
      assert_nil R.parse(input), input.inspect
      assert_nil R.parse_interval(input), input.inspect
    end
  end

  # -- stored-form validation ------------------------------------------------

  def test_cookie_predicate_accepts_canonical_calendar_forms
    %w[w:mon w:mon,wed,fri 2w:mon m:15 m:last m:2tue m:lastfri y:07-04 y:11:3thu +w:mon 3m:1,15]
      .each { |stored| assert R.cookie?(stored), stored }
    assert R.cookie?(".+1w")
    assert R.cookie?("++2d")
  end

  def test_cookie_predicate_rejects_non_canonical_spellings
    # cookie? validates what is already on disk, so it is strict: anything the
    # parser would rewrite is not a stored form.
    %w[weekly 2w 1w:mon W:MON w:monday w:wed,mon m:15,1 y:7-4 .+w:mon w: m:32].each do |input|
      refute R.cookie?(input), input.inspect
    end
  end

  def test_parse_output_always_satisfies_cookie_predicate
    ["every monday", "weekdays", "every 2 weeks on mon and thu", "last day of the month",
     "2nd tuesday", "every july 4", "3rd thursday of november", "weekly", "2w",
     "w:sun,mon", "+m:31", "m:lastfri,15"].each do |input|
      canonical = R.parse(input)
      refute_nil canonical, input.inspect
      assert R.cookie?(canonical), "#{input.inspect} -> #{canonical.inspect}"
    end
  end

  def test_calendar_predicate_separates_the_two_shapes
    assert R.calendar?("w:mon")
    assert R.calendar?("+2m:1,15")
    refute R.calendar?(".+1w")
    refute R.calendar?("weekly")
  end

  # -- edge-date rules -------------------------------------------------------

  MAR = Date.new(2026, 3, 1)

  def test_numeric_day_a_month_lacks_clamps_to_the_month_end
    assert_equal [Date.new(2026, 3, 31), Date.new(2026, 4, 30), Date.new(2026, 5, 31),
                  Date.new(2026, 6, 30)],
                 R.occurrences("m:31", from: MAR, today: MAR, count: 4)
    # ...which makes m:31 a synonym for m:last in short months, by design.
    assert_equal Date.new(2026, 4, 30), R.next_date("m:31", from: Date.new(2026, 4, 1),
                                                            today: Date.new(2026, 4, 1))
  end

  def test_ordinal_weekday_a_month_lacks_skips_to_a_month_that_has_one
    jan = Date.new(2026, 1, 1)
    # Feb, Mar and Apr 2026 have only four Fridays; they are skipped, not clamped.
    assert_equal [Date.new(2026, 1, 30), Date.new(2026, 5, 29), Date.new(2026, 7, 31),
                  Date.new(2026, 10, 30)],
                 R.occurrences("m:5fri", from: jan, today: jan, count: 4)
  end

  def test_last_weekday_and_last_day_are_distinct_rules
    jan = Date.new(2026, 1, 1)
    assert_equal Date.new(2026, 1, 31), R.next_date("m:last", from: jan, today: jan)
    assert_equal Date.new(2026, 1, 30), R.next_date("m:lastfri", from: jan, today: jan)
  end

  def test_feb_29_yearly_clamps_in_non_leap_years
    from = Date.new(2024, 3, 1)
    assert_equal [Date.new(2025, 2, 28), Date.new(2026, 2, 28), Date.new(2027, 2, 28),
                  Date.new(2028, 2, 29)],
                 R.occurrences("y:02-29", from: from, today: from, count: 4)
  end

  def test_dst_gap_candidate_skips_to_the_next_occurrence
    # 2026-03-08 is the US spring-forward; 02:30 does not exist that morning.
    context = Tasks::TemporalContext.new(now: Time.utc(2026, 3, 2, 12), timezone: "Etc/UTC")
    value = Tasks::TemporalValue.new(date: "2026-03-01", local_time: "02:30",
                                     timezone: "America/Los_Angeles", validate: false)
    assert_equal Date.new(2026, 3, 15),
                 R.next_temporal_date("w:sun", value: value, kind: :scheduled, context: context)
    assert_equal Date.new(2026, 3, 15),
                 R.next_temporal_date("+w:sun", value: value, kind: :scheduled, context: context)
  end

  # A February with five Fridays needs 29 days starting on a Friday, so only a
  # leap year has one — an odd anchor year with a 2-year interval never fires.
  # Satisfiability is a property of the schedule *and* its anchor, so it cannot
  # be decided at parse time.
  UNSATISFIABLE = "2y:02:5fri"
  ODD_ANCHOR = Date.new(2027, 1, 1)

  def test_an_anchor_dependent_dead_schedule_parses_but_cannot_project
    assert_equal UNSATISFIABLE, R.parse("every 2 years on the 5th friday of february")
    error = assert_raises(ArgumentError) do
      R.next_date(UNSATISFIABLE, from: ODD_ANCHOR, today: ODD_ANCHOR)
    end
    assert_match(/no occurrence of "#{UNSATISFIABLE}"/, error.message)
    assert_match(/from 2027-01-01/, error.message)
    assert_match(/may never fire for this anchor/, error.message)
  end

  def test_the_same_schedule_projects_fine_from_a_leap_parity_anchor
    assert_equal [Date.new(2036, 2, 29), Date.new(2064, 2, 29)],
                 R.occurrences(UNSATISFIABLE, from: Date.new(2028, 1, 1),
                                              today: Date.new(2028, 1, 1), count: 2)
  end

  def test_explain_keeps_identifying_a_schedule_it_cannot_project
    # A projection failure must not masquerade as a parse error: the canonical
    # form and human rendering stay, `next` is empty, and the reason is distinct.
    result = R.explain("every 2 years on the 5th friday of february",
                       context: context_at(TODAY), from: ODD_ANCHOR)
    assert_equal UNSATISFIABLE, result[:canonical]
    assert_equal "every 2 years on the 5th Friday of February", result[:human]
    assert_empty result[:next]
    assert_match(/may never fire for this anchor/, result[:error])
    refute_match(/unrecognized/, result[:error])
  end

  def test_explain_rejects_an_unparsable_stamp_without_projecting
    result = R.explain("w:mon", context: context_at(TODAY), from: "not-a-date")
    assert_match(/must be a real YYYY-MM-DD date/, result[:error])
    refute result.key?(:canonical)
  end

  def test_validation_block_can_veto_a_calendar_candidate
    context = Tasks::TemporalContext.new(now: Time.utc(2026, 7, 28, 12), timezone: "Etc/UTC")
    value = Tasks::TemporalValue.new(date: "2026-07-28")
    seen = []
    result = R.next_temporal_date("w:mon", value: value, kind: :deadline, context: context) do |candidate|
      seen << candidate
      candidate > Date.new(2026, 8, 10)
    end
    assert_equal Date.new(2026, 8, 17), result
    assert_equal [Date.new(2026, 8, 3), Date.new(2026, 8, 10), Date.new(2026, 8, 17)], seen
  end

  # -- advance semantics -----------------------------------------------------

  TODAY = Date.new(2026, 7, 28) # a Tuesday; its ISO week starts Mon 2026-07-27

  def test_catch_up_lands_strictly_after_today
    assert_equal Date.new(2026, 8, 3), R.next_date("w:mon", from: Date.new(2026, 1, 5), today: TODAY)
    assert_equal Date.new(2026, 8, 1), R.next_date("m:1", from: Date.new(2025, 11, 1), today: TODAY)
    assert_equal Date.new(2027, 7, 4), R.next_date("y:07-04", from: Date.new(2020, 7, 4), today: TODAY)
  end

  def test_catch_up_still_advances_when_the_stamp_is_already_in_the_future
    # The stamp *is* the current occurrence, so a roll always moves past it.
    assert_equal Date.new(2026, 8, 17),
                 R.next_date("w:mon", from: Date.new(2026, 8, 10), today: TODAY)
  end

  def test_one_hop_advances_from_the_stored_date_and_may_stay_in_the_past
    result = R.next_date("+w:mon", from: Date.new(2026, 1, 5), today: TODAY)
    assert_equal Date.new(2026, 1, 12), result
    assert_operator result, :<, TODAY
    assert_equal Date.new(2025, 12, 1), R.next_date("+m:1", from: Date.new(2025, 11, 1), today: TODAY)
  end

  def test_every_nth_week_parity_is_anchored_on_the_stored_dates_iso_week
    # Two stamps one week apart produce opposite-parity Monday series.
    assert_equal [Date.new(2026, 8, 10), Date.new(2026, 8, 24), Date.new(2026, 9, 7)],
                 R.occurrences("2w:mon", from: TODAY, today: TODAY, count: 3)
    assert_equal [Date.new(2026, 8, 3), Date.new(2026, 8, 17), Date.new(2026, 8, 31)],
                 R.occurrences("2w:mon", from: Date.new(2026, 7, 21), today: TODAY, count: 3)
  end

  def test_parity_survives_a_catch_up_roll_from_a_stale_stamp
    # Stamp 2026-01-05 (a Monday) anchors the odd-week series; catching up in
    # July must land on that series, not on the nearest Monday.
    assert_equal Date.new(2026, 8, 3), R.next_date("2w:mon", from: Date.new(2026, 1, 5), today: TODAY)
    assert_equal 0, (Date.new(2026, 8, 3) - Date.new(2026, 1, 5)).to_i % 14
  end

  def test_every_nth_month_and_year_parity_anchor_on_the_stored_date
    assert_equal [Date.new(2026, 7, 15), Date.new(2026, 10, 15), Date.new(2027, 1, 15)],
                 R.occurrences("3m:15", from: Date.new(2026, 1, 15), today: Date.new(2026, 7, 1), count: 3)
    assert_equal [Date.new(2027, 7, 4), Date.new(2029, 7, 4)],
                 R.occurrences("2y:07-04", from: Date.new(2025, 7, 4), today: TODAY, count: 2)
  end

  def test_multi_day_weekly_walks_the_day_set_in_order
    assert_equal [Date.new(2026, 7, 29), Date.new(2026, 7, 31), Date.new(2026, 8, 3),
                  Date.new(2026, 8, 5)],
                 R.occurrences("w:mon,wed,fri", from: TODAY, today: TODAY, count: 4)
  end

  def test_next_temporal_date_catch_up_and_one_hop_for_all_day_stamps
    context = Tasks::TemporalContext.new(now: Time.utc(2026, 7, 28, 12), timezone: "Etc/UTC")
    value = Tasks::TemporalValue.new(date: "2026-01-05")
    assert_equal Date.new(2026, 8, 3),
                 R.next_temporal_date("w:mon", value: value, kind: :deadline, context: context)
    assert_equal Date.new(2026, 1, 12),
                 R.next_temporal_date("+w:mon", value: value, kind: :deadline, context: context)
  end

  def test_next_temporal_date_keeps_nw_parity_when_a_candidate_hits_a_dst_gap
    # Anchor Sun 2026-02-22 puts the every-other-Sunday series on 2026-03-08 —
    # the US spring-forward, where 02:30 does not exist. The skip must land on
    # the next *same-parity* Sunday (03-22), not the next Sunday (03-15).
    context = Tasks::TemporalContext.new(now: Time.utc(2026, 2, 23, 12), timezone: "Etc/UTC")
    value = Tasks::TemporalValue.new(date: "2026-02-22", local_time: "02:30",
                                     timezone: "America/Los_Angeles", validate: false)
    result = R.next_temporal_date("2w:sun", value: value, kind: :scheduled, context: context)
    assert_equal Date.new(2026, 3, 22), result
    assert_equal 0, (result - value.date).to_i % 14
  end

  def test_next_temporal_date_skips_months_without_an_ordinal_weekday
    # Feb, Mar and Apr 2026 have no 5th Friday; the roll skips them.
    context = Tasks::TemporalContext.new(now: Time.utc(2026, 2, 1, 12), timezone: "Etc/UTC")
    value = Tasks::TemporalValue.new(date: "2026-01-01")
    assert_equal Date.new(2026, 5, 29),
                 R.next_temporal_date("m:5fri", value: value, kind: :deadline, context: context)
    # One-hop walks the same series, just from the stored date.
    assert_equal Date.new(2026, 1, 30),
                 R.next_temporal_date("+m:5fri", value: value, kind: :deadline, context: context)
  end

  def test_next_temporal_date_rejects_a_non_stored_form
    context = Tasks::TemporalContext.new(now: Time.utc(2026, 7, 28, 12), timezone: "Etc/UTC")
    value = Tasks::TemporalValue.new(date: "2026-07-28")
    assert_raises(ArgumentError) do
      R.next_temporal_date("every monday", value: value, kind: :deadline, context: context)
    end
  end

  def test_next_date_rejects_a_non_stored_form
    assert_raises(ArgumentError) { R.next_date("every monday", from: TODAY) }
    assert_raises(ArgumentError) { R.next_date("w:funday", from: TODAY) }
  end

  # -- humanize --------------------------------------------------------------

  def test_humanize
    assert_table(
      # interval cookies
      ".+1w" => "every week from completion",
      ".+2w" => "every 2 weeks from completion",
      "+1m" => "every month from the scheduled date",
      "++3d" => "every 3 days from the scheduled date (catching up)",
      # calendar schedules
      "w:mon" => "every Monday",
      "w:mon,wed,fri" => "every Mon, Wed, Fri",
      "w:mon,tue,wed,thu,fri" => "every weekday",
      "w:sat,sun" => "every weekend",
      "2w:mon" => "every 2 weeks on Monday",
      "2w:mon,thu" => "every 2 weeks on Mon, Thu",
      "2w:mon,tue,wed,thu,fri" => "every 2 weeks on weekdays",
      "3w:sat,sun" => "every 3 weeks on weekends",
      "m:15" => "monthly on the 15th",
      "m:1,15" => "monthly on the 1st and 15th",
      "m:1,15,22" => "monthly on the 1st, 15th and 22nd",
      "m:last" => "monthly on the last day",
      "m:2tue" => "monthly on the 2nd Tuesday",
      "m:3wed" => "monthly on the 3rd Wednesday",
      "m:lastfri" => "monthly on the last Friday",
      "2m:15" => "every 2 months on the 15th",
      "y:07-04" => "yearly on July 4",
      "y:11:3thu" => "yearly on the 3rd Thursday of November",
      "2y:07-04" => "every 2 years on July 4",
      "+w:mon" => "every Monday (one hop)",
      "+m:15" => "monthly on the 15th (one hop)"
    ) { |cookie| R.humanize(cookie) }
  end

  def test_humanize_edges
    assert_nil R.humanize(nil)
    assert_nil R.humanize("")
    assert_equal "junk", R.humanize("junk") # unparsable stored values echo through
  end

  # -- explain ---------------------------------------------------------------

  def context_at(date) = Tasks::TemporalContext.new(now: Time.utc(date.year, date.month, date.day, 12),
                                                    timezone: "Etc/UTC")

  def test_explain_payload
    result = R.explain("every 2 weeks on monday", context: context_at(TODAY), count: 3)
    assert_equal "every 2 weeks on monday", result[:input]
    assert_equal "2w:mon", result[:canonical]
    assert_equal "every 2 weeks on Monday", result[:human]
    assert_equal [Date.new(2026, 8, 10), Date.new(2026, 8, 24), Date.new(2026, 9, 7)], result[:next]
    refute result.key?(:error)
  end

  def test_explain_projects_from_a_supplied_stamp
    result = R.explain("2w:mon", context: context_at(TODAY), count: 2, from: Date.new(2026, 7, 21))
    assert_equal [Date.new(2026, 8, 3), Date.new(2026, 8, 17)], result[:next]
    assert_equal [Date.new(2026, 8, 3), Date.new(2026, 8, 17)],
                 R.explain("2w:mon", context: context_at(TODAY), count: 2, from: "2026-07-21")[:next]
  end

  def test_explain_handles_interval_cookies_too
    result = R.explain("every 2 weeks", context: context_at(TODAY), count: 3)
    assert_equal ".+2w", result[:canonical]
    assert_equal "every 2 weeks from completion", result[:human]
    assert_equal [Date.new(2026, 8, 11), Date.new(2026, 8, 25), Date.new(2026, 9, 8)], result[:next]
  end

  def test_explain_returns_a_structured_error_with_the_reason
    result = R.explain(".+w:mon", context: context_at(TODAY))
    assert_equal ".+w:mon", result[:input]
    assert_match(/interval prefix/, result[:error])
    refute result.key?(:canonical)
    refute result.key?(:next)
  end

  def test_explain_of_off_reports_no_recurrence
    result = R.explain("off", context: context_at(TODAY))
    assert_nil result[:canonical]
    assert_equal "no recurrence", result[:human]
    assert_empty result[:next]
  end

  def test_explain_count_defaults_to_five_and_clamps
    assert_equal 5, R.explain("w:mon", context: context_at(TODAY))[:next].size
    assert_empty R.explain("w:mon", context: context_at(TODAY), count: 0)[:next]
    assert_equal 50, R.explain("w:mon", context: context_at(TODAY), count: 999)[:next].size
  end

  def test_explain_without_a_context_uses_the_local_clock
    result = R.explain("w:mon", count: 1)
    assert_operator result[:next].first, :>, Date.today
    assert_equal 1, result[:next].first.cwday
  end
end
