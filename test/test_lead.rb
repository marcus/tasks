# frozen_string_literal: true

require_relative "test_helper"
require "tasks/lead"
require "tasks/operation_context"
require "tasks/store"
require "tasks/task_queries"

# The lead-time model: the span grammar, the derived gate, the store's five
# validation rules, and the per-occurrence release stamp. Surface behavior
# (CLI/API/TUI) lives with those surfaces; this file pins the canonical answers
# every one of them projects.
class TestLead < Minitest::Test
  ZONE = "America/Denver"

  # -- the span grammar ------------------------------------------------------

  def test_canonical_spans_pass_through_and_phrases_normalize
    {
      "3w" => "3w", "2d" => "2d", "1m" => "1m", "10y" => "10y",
      "3 weeks" => "3w", "a week" => "1w", "the week before" => "1w",
      "2 wks" => "2w", "1 month" => "1m", "6 months" => "6m",
      "a quarter" => "3m", "fortnight" => "2w", "2 yrs" => "2y",
      "10 days ahead" => "10d", "4 days early" => "4d",
    }.each do |input, canonical|
      assert_equal({ canonical: canonical }, Tasks::Lead.parse_result(input),
                   "#{input.inspect} should normalize to #{canonical.inspect}")
    end
  end

  def test_off_words_clear_the_lead
    %w[off none never clear no stop].each do |word|
      assert_equal({ canonical: :off }, Tasks::Lead.parse_result(word))
    end
  end

  def test_zero_and_junk_are_refused_with_a_reason
    assert_match(/at least 1 day/, Tasks::Lead.parse_result("0 days")[:error])
    assert_match(/unrecognized lead time/, Tasks::Lead.parse_result("soonish")[:error])
    assert_match(/no lead time given/, Tasks::Lead.parse_result("  ")[:error])
    refute Tasks::Lead.span?("0w"), "zero is not a canonical span"
    refute Tasks::Lead.span?("+2w"), "a lead carries no repeater prefix"
    refute Tasks::Lead.span?("w:mon"), "a lead is a span, not a calendar schedule"
  end

  # `h` is the one clock unit; `m` must keep meaning months, in the lead grammar
  # exactly as in the recurrence grammar it shares its letters with.
  def test_hour_leads_parse_and_m_still_means_months
    { "5h" => "5h", "5 hr" => "5h", "12hours" => "12h", "an hour" => "1h" }.each do |input, canonical|
      assert_equal({ canonical: canonical }, Tasks::Lead.parse_result(input))
    end
    assert Tasks::Lead.clock?("5h")
    refute Tasks::Lead.clock?("5m")
    assert_equal({ canonical: "1m" }, Tasks::Lead.parse_result("1m"))
    assert_equal({ canonical: "6m" }, Tasks::Lead.parse_result("6 months"))
    assert_nil Tasks::Lead.gate_date(Date.new(2026, 6, 1), "5h"),
               "a clock gate is an instant no date can express"
  end

  def test_humanize_reads_as_a_span
    assert_equal "3 weeks", Tasks::Lead.humanize("3w")
    assert_equal "1 day", Tasks::Lead.humanize("1d")
    assert_equal "3 weeks before", Tasks::Lead.describe("3w")
    assert_nil Tasks::Lead.humanize("")
    assert_equal "nonsense", Tasks::Lead.humanize("nonsense"), "unparsable values echo"
  end

  def test_month_and_year_leads_clamp_like_recurrence_intervals
    assert_equal Date.new(2026, 2, 28), Tasks::Lead.gate_date(Date.new(2026, 3, 31), "1m")
    assert_equal Date.new(2024, 2, 29), Tasks::Lead.gate_date(Date.new(2024, 3, 31), "1m")
    assert_equal Date.new(2025, 11, 1), Tasks::Lead.gate_date(Date.new(2026, 11, 1), "1y")
    assert_equal Date.new(2026, 10, 11), Tasks::Lead.gate_date(Date.new(2026, 11, 1), "3w")
    assert_nil Tasks::Lead.gate_date(nil, "3w")
    assert_nil Tasks::Lead.gate_date(Date.new(2026, 11, 1), "0w")
  end

  def test_anchor_prefers_the_deadline
    deadline = Date.new(2026, 11, 1)
    scheduled = Date.new(2026, 10, 1)
    assert_equal deadline, Tasks::Lead.anchor_date(deadline, scheduled)
    assert_equal scheduled, Tasks::Lead.anchor_date(nil, scheduled)
    assert_nil Tasks::Lead.anchor_date(nil, nil)
  end

  # -- the derived gate ------------------------------------------------------

  def test_deadline_anchored_lead_hides_until_the_derived_date
    records = lead_records(deadline: "2026-11-01", lead: "3w")

    assert_unavailable(records, on: "2026-10-10", until_date: "2026-10-11")
    assert_available(records, on: "2026-10-11")
    assert_available(records, on: "2026-10-31")
  end

  def test_scheduled_anchored_lead_releases_before_the_available_from_date
    records = lead_records(scheduled: "2026-04-20", lead: "1w")

    assert_unavailable(records, on: "2026-04-12", until_date: "2026-04-13")
    assert_available(records, on: "2026-04-13")
    assert_available(records, on: "2026-04-20")
  end

  def test_lead_gate_uses_the_existing_scheduled_reason_and_available_at
    availability = availability_on(lead_records(deadline: "2026-11-01", lead: "3w"), "2026-10-01")

    assert_equal :scheduled, availability.reason
    assert_equal Date.new(2026, 10, 11), availability.scheduled
    # Local midnight in the reader's zone (Denver is UTC-6 in October).
    assert_equal "2026-10-11T06:00:00Z", availability.available_at.iso8601
  end

  def test_a_timezone_carrying_anchor_still_opens_at_the_readers_local_midnight
    records = lead_records(deadline: "2026-11-01", lead: "3w")
    task = records.last
    task["deadline_time"] = { "local" => "09:00", "timezone" => "Asia/Tokyo" }

    availability = availability_on(records, "2026-10-01")
    assert_equal Date.new(2026, 10, 11), availability.scheduled
    assert_equal "2026-10-11T06:00:00Z", availability.available_at.iso8601
  end

  # US DST ends 2026-11-01; a calendar lead spanning it keeps its wall date and
  # simply releases an hour later in UTC.
  def test_a_calendar_lead_holds_its_wall_date_across_a_dst_change
    availability = availability_on(lead_records(deadline: "2026-11-10", lead: "2w"), "2026-10-01")

    assert_equal Date.new(2026, 10, 27), availability.scheduled
    assert_equal "2026-10-27T06:00:00Z", availability.available_at.iso8601

    spring = availability_on(lead_records(deadline: "2026-03-20", lead: "2w"), "2026-03-01")
    assert_equal Date.new(2026, 3, 6), spring.scheduled
    assert_equal "2026-03-06T07:00:00Z", spring.available_at.iso8601
  end

  def test_an_ancestor_lead_gates_its_subtree
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "bbbb0001", "title" => "Work" },
      { "type" => "task", "id" => "bbbb0002", "parent" => "bbbb0001", "state" => "TODO",
        "title" => "Quarter close", "deadline" => "2026-11-01", "lead" => "3w" },
      { "type" => "task", "id" => "bbbb0003", "parent" => "bbbb0002", "state" => "NEXT",
        "title" => "Reconcile the ledger" },
    ]

    with_lead_store(records, on: "2026-10-01") do |query|
      child = query.snapshot.items.find { |item| item.id == "bbbb0003" }
      availability = query.availability(child)
      assert_equal :ancestor_scheduled, availability.reason
      assert_equal "bbbb0002", availability.blocker_id
      assert_equal Date.new(2026, 10, 11), availability.scheduled
    end

    with_lead_store(records, on: "2026-10-11") do |query|
      child = query.snapshot.items.find { |item| item.id == "bbbb0003" }
      assert query.availability(child).available?
    end
  end

  # The furthest gate wins regardless of which candidate derived it, so a lead
  # and a plain available-from date compare on the same axis.
  def test_mixed_own_and_ancestor_gates_report_the_later_one
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "bbbb0001", "title" => "Work" },
      { "type" => "task", "id" => "bbbb0002", "parent" => "bbbb0001", "state" => "TODO",
        "title" => "Parent", "scheduled" => "2026-10-20" },
      { "type" => "task", "id" => "bbbb0003", "parent" => "bbbb0002", "state" => "NEXT",
        "title" => "Child", "deadline" => "2026-11-01", "lead" => "3w" },
    ]

    with_lead_store(records, on: "2026-10-01") do |query|
      child = query.snapshot.items.find { |item| item.id == "bbbb0003" }
      availability = query.availability(child)
      assert_equal :ancestor_scheduled, availability.reason, "the ancestor's later gate wins"
      assert_equal Date.new(2026, 10, 20), availability.scheduled
    end
  end

  def test_an_own_hold_still_outranks_a_lead_gate
    records = lead_records(deadline: "2026-11-01", lead: "3w")
    records.last["tags"] = %w[defer]

    assert_equal :on_hold, availability_on(records, "2026-10-01").reason
    assert_equal :on_hold, availability_on(records, "2026-10-20").reason
  end

  def test_a_malformed_lead_gates_nothing_and_check_reports_it
    records = lead_records(deadline: "2026-11-01", lead: "0w")

    assert availability_on(records, "2026-10-01").available?,
           "a reader must not crash or hide work over a value Check will report"
    with_lead_files(records) do |org, _archive|
      refute Tasks::Check.check(org).ok?
      assert_match(/invalid lead "0w"/, Tasks::Check.check(org).errors.first.last)
    end
  end

  def test_check_rejects_a_lead_skip_with_no_anchor_and_a_lead_on_a_section
    records = lead_records(deadline: "2026-11-01", lead: "3w")
    records.last.delete("deadline")
    records.last["lead_skip"] = "2026-11-01"
    with_lead_files(records) do |org, _archive|
      messages = Tasks::Check.check(org).errors.map(&:last)
      assert(messages.any? { |message| message.include?("lead_skip without a scheduled date or deadline") })
    end

    section_records = lead_records(deadline: "2026-11-01", lead: "3w")
    section_records[1]["lead"] = "3w"
    with_lead_files(section_records) do |org, _archive|
      messages = Tasks::Check.check(org).errors.map(&:last)
      assert(messages.any? { |message| message.include?(%(section must not carry "lead")) })
    end
  end

  # -- clock leads (td-556c53) ----------------------------------------------

  # An all-day anchor resolves to the first instant of its date, so a clock lead
  # opens partway through the previous evening.
  def test_a_clock_lead_on_an_all_day_anchor_opens_the_evening_before
    records = lead_records(deadline: "2026-06-01", lead: "5h")

    availability = availability_on(records, "2026-05-31")
    refute availability.available?
    assert_equal "2026-06-01T01:00:00Z", availability.available_at.iso8601,
                 "19:00 on May 31 in Denver (MDT, UTC-6)"
    assert_equal Date.new(2026, 5, 31), availability.scheduled

    assert_available(records, on: "2026-06-01")
  end

  def test_a_clock_lead_measures_from_a_timed_anchors_own_instant
    records = lead_records(deadline: "2026-06-01", lead: "3h")
    records.last["deadline_time"] = { "local" => "09:00", "timezone" => "America/Denver" }

    availability = availability_on(records, "2026-05-31")
    assert_equal "2026-06-01T12:00:00Z", availability.available_at.iso8601,
                 "06:00 local, three hours before a 09:00 deadline"
  end

  # Either side of local midnight: the window opens on the previous day when the
  # duration reaches back past 00:00, and on the anchor's own day when it does not.
  def test_a_clock_lead_lands_on_the_right_side_of_local_midnight
    late = lead_records(deadline: "2026-06-01", lead: "1h")
    late.last["deadline_time"] = { "local" => "00:30", "timezone" => "America/Denver" }
    assert_equal Date.new(2026, 5, 31), availability_on(late, "2026-05-30").scheduled

    early = lead_records(deadline: "2026-06-01", lead: "1h")
    early.last["deadline_time"] = { "local" => "01:30", "timezone" => "America/Denver" }
    assert_equal Date.new(2026, 6, 1), availability_on(early, "2026-05-30").scheduled
  end

  # A clock lead is a real duration, so a DST change inside the window MOVES the
  # wall time — the opposite of a calendar lead, which holds its wall date. US
  # DST ends 2026-11-01 (02:00 → 01:00) and begins 2026-03-08 (02:00 → 03:00).
  def test_a_clock_lead_crossing_dst_keeps_its_duration_not_its_wall_time
    fall = lead_records(deadline: "2026-11-01", lead: "5h")
    fall.last["deadline_time"] = { "local" => "12:00", "timezone" => "America/Denver" }
    # 12:00 MST is 19:00Z; five hours earlier is 14:00Z = 08:00 MDT, an hour
    # further back in wall-clock terms than a no-DST day would give.
    assert_equal "2026-11-01T14:00:00Z", availability_on(fall, "2026-10-31").available_at.iso8601

    # Spring forward: 06:00 MDT is 12:00Z, and five real hours earlier is 07:00Z
    # — 00:00 MST, a six-hour wall-clock step back across the lost hour.
    spring = lead_records(deadline: "2026-03-08", lead: "5h")
    spring.last["deadline_time"] = { "local" => "06:00", "timezone" => "America/Denver" }
    assert_equal "2026-03-08T07:00:00Z", availability_on(spring, "2026-03-07").available_at.iso8601

    # The calendar counterpart, for contrast: same span in days, same wall date.
    calendar = lead_records(deadline: "2026-11-10", lead: "2w")
    assert_equal Date.new(2026, 10, 27), availability_on(calendar, "2026-10-01").scheduled
  end

  def test_a_clock_lead_carries_its_own_window_onto_the_task_view
    with_lead_store(lead_records(deadline: "2026-06-01", lead: "5h"), on: "2026-05-01") do |query|
      item = query.snapshot.items.find { |candidate| candidate.id == "bbbb0002" }
      task = query.task(item)
      assert_equal "5h", task.lead
      assert_equal "5 hours", task.lead_human
      assert_equal Date.new(2026, 5, 31), task.lead_opens
      assert_equal "2026-06-01T01:00:00Z", task.lead_opens_at.iso8601
      assert_equal "19:00", task.lead_opens_value.local_time
    end
  end

  # -- store rules -----------------------------------------------------------

  def test_rule_one_refuses_a_lead_with_no_anchor
    result = patch_lead(FIXTURE_RECORDS, "aaaa000a", "3w")
    refute result.ok?
    assert_match(/needs a date to hide before/, result.errors.first)
  end

  # A lead is an intent ABOUT a date, so it is accepted wherever the date fields
  # themselves are — including on a proposal, which the contract requires — and
  # unlike recurrence, which `done` must be able to roll.
  def test_a_lead_is_accepted_on_any_state_the_dates_are
    records = lead_records(deadline: "2026-11-01", lead: nil)
    records.last["state"] = "PROPOSED"
    proposed = patch_lead(records, "bbbb0002", "3w")
    assert proposed.ok?, proposed.errors.join(", ")

    records.last["state"] = "DONE"
    records.last["closed"] = "2026-07-01"
    done = patch_lead(records, "bbbb0002", "3w")
    assert done.ok?, done.errors.join(", ")
  end

  # Rule 5: a lead is an intent about a date and cannot outlive the last one.
  def test_clearing_the_final_anchor_clears_the_lead_in_the_same_write
    with_lead_store_files(lead_records(deadline: "2026-11-01", lead: "3w")) do |store, org|
      snapshot = store.edit_snapshot("bbbb0002")
      result = store.apply_changeset!(
        Tasks::TaskChangeset.from(snapshot, changes: { date_clear: nil })
      )
      assert result.ok?, result.errors.join(", ")
      record = record_for(org, title: "Renew the passport")
      refute record.key?("lead"), "the lead goes with its anchor"
      refute record.key?("deadline")
      assert Tasks::Check.check(org).ok?
    end

    # Clearing ONE date leaves a lead that still has an anchor alone. (A lead
    # beside BOTH dates is unreachable by any write and reported by Check, so
    # the only way to reach a one-date clear is from a one-date task.)
    with_lead_store_files(lead_records(scheduled: "2026-10-01", lead: "3w")) do |store, org|
      snapshot = store.edit_snapshot("bbbb0002")
      result = store.patch_task!(
        Tasks::TaskPatch.from(snapshot, field: :scheduled, value: Date.new(2026, 10, 15))
      )
      assert result.ok?, result.errors.join(", ")
      record = record_for(org, title: "Renew the passport")
      assert_equal "3w", record["lead"], "moving the anchor keeps the window"
      assert_equal "2026-10-15", record["scheduled"]
    end
  end

  # The linter reports the two shapes a write refuses, because a hand edit or a
  # foreign writer can still produce them.
  def test_check_reports_a_lead_with_no_anchor_and_a_lead_beside_both_dates
    dateless = lead_records(deadline: "2026-11-01", lead: "3w")
    dateless.last.delete("deadline")
    with_lead_files(dateless) do |org, _archive|
      messages = Tasks::Check.check(org).errors.map(&:last)
      assert(messages.any? { |message| message.include?("no scheduled date or deadline to hide before") })
    end

    both = lead_records(deadline: "2026-11-01", lead: "3w")
    both.last["scheduled"] = "2026-10-01"
    with_lead_files(both) do |org, _archive|
      messages = Tasks::Check.check(org).errors.map(&:last)
      assert(messages.any? { |message| message.include?("beside both a scheduled date and a deadline") })
    end
  end

  def test_rule_three_refuses_a_second_timed_gate_from_either_side
    both = lead_records(deadline: "2026-11-01", lead: nil)
    both.last["scheduled"] = "2026-10-01"
    result = patch_lead(both, "bbbb0002", "3w")
    refute result.ok?
    assert_match(/second, ignored gate/, result.errors.first)

    lead_first = lead_records(deadline: "2026-11-01", lead: "3w")
    with_lead_store_files(lead_first) do |store|
      snapshot = store.edit_snapshot("bbbb0002")
      scheduled = store.patch_task!(Tasks::TaskPatch.from(snapshot, field: :scheduled,
                                                          value: Date.new(2026, 10, 1)))
      refute scheduled.ok?
      assert_match(/second, ignored gate/, scheduled.errors.first)
    end

    # And from the other direction: a deadline added to a scheduled-anchored
    # lead task would flip the anchor and strand the available-from date.
    scheduled_anchor = lead_records(scheduled: "2026-10-01", lead: "3w")
    with_lead_store_files(scheduled_anchor) do |store|
      snapshot = store.edit_snapshot("bbbb0002")
      deadline = store.patch_task!(Tasks::TaskPatch.from(snapshot, field: :deadline,
                                                         value: Date.new(2026, 11, 1)))
      refute deadline.ok?
      assert_match(/second, ignored gate/, deadline.errors.first)
    end

    # Moving the anchor a lead already measures from stays allowed — that is an
    # edit of the occurrence, not a second gate.
    with_lead_store_files(lead_records(scheduled: "2026-10-01", lead: "3w")) do |store, org|
      snapshot = store.edit_snapshot("bbbb0002")
      moved = store.patch_task!(Tasks::TaskPatch.from(snapshot, field: :scheduled,
                                                      value: Date.new(2026, 10, 15)))
      assert moved.ok?, moved.errors.join(", ")
      assert_equal "2026-10-15", record_for(org, title: "Renew the passport")["scheduled"]
    end
  end

  def test_rule_four_refuses_an_uncanonical_span
    records = lead_records(deadline: "2026-11-01", lead: nil)
    result = patch_lead(records, "bbbb0002", "3 weeks")
    refute result.ok?
    assert_match(/invalid lead time/, result.errors.first)
  end

  def test_rule_five_refuses_a_lead_that_would_open_outside_the_storable_range
    records = lead_records(deadline: "1000-01-05", lead: nil)
    result = patch_lead(records, "bbbb0002", "9999y")
    refute result.ok?
    assert_match(/four-digit years/, result.errors.first)
  end

  def test_clearing_a_lead_is_always_allowed_and_drops_the_skip_stamp
    records = lead_records(deadline: "2026-11-01", lead: "3w")
    records.last["lead_skip"] = "2026-11-01"
    records.last["state"] = "DONE"
    records.last["closed"] = "2026-07-01"

    with_lead_store_files(records) do |store, org|
      snapshot = store.edit_snapshot("bbbb0002")
      result = store.patch_task!(Tasks::TaskPatch.from(snapshot, field: :lead, value: :off))
      assert result.ok?, result.errors.join(", ")
      record = record_for(org, title: "Renew the passport")
      refute record.key?("lead")
      refute record.key?("lead_skip")
    end
  end

  # -- the release stamp -----------------------------------------------------

  def test_activate_releases_exactly_one_occurrence_and_keeps_every_date
    records = lead_records(scheduled: "2026-04-20", lead: "1w")
    records.last["recur"] = "+3m"

    with_lead_store_files(records) do |store, org|
      snapshot = store.edit_snapshot("bbbb0002")
      result = store.apply_changeset!(
        Tasks::TaskChangeset.from(snapshot, changes: { activate: true }), today: Date.new(2026, 4, 1)
      )
      assert result.ok?, result.errors.join(", ")
      record = record_for(org, title: "Renew the passport")
      assert_equal "2026-04-20", record["scheduled"], "the occurrence date survives"
      assert_equal "2026-04-20", record["lead_skip"]
      assert_equal "1w", record["lead"]
      assert Tasks::Check.check(org).ok?
    end

    released = records.map(&:dup)
    released[-1] = released[-1].merge("lead_skip" => "2026-04-20")
    assert_available(released, on: "2026-04-01")
  end

  def test_the_stamp_expires_when_the_roll_moves_the_anchor
    records = lead_records(scheduled: "2026-04-20", lead: "1w")
    records.last["recur"] = "+3m"
    records.last["lead_skip"] = "2026-04-20"

    with_lead_store_files(records) do |store, org|
      snapshot = store.edit_snapshot("bbbb0002")
      result = store.patch_task!(Tasks::TaskPatch.from(snapshot, field: :state, value: "DONE"),
                                 today: Date.new(2026, 4, 20))
      assert result.ok?, result.errors.join(", ")
      record = record_for(org, title: "Renew the passport")
      assert_equal "2026-07-20", record["scheduled"], "the recurrence rolled"
      refute record.key?("lead_skip"), "the released occurrence is history"
      assert_equal "1w", record["lead"], "the window re-arms against the new anchor"
    end
  end

  def test_the_stamp_expires_when_an_anchor_edit_moves_the_occurrence
    records = lead_records(deadline: "2026-11-01", lead: "3w")
    records.last["lead_skip"] = "2026-11-01"

    with_lead_store_files(records) do |store, org|
      snapshot = store.edit_snapshot("bbbb0002")
      result = store.patch_task!(Tasks::TaskPatch.from(snapshot, field: :deadline,
                                                       value: Date.new(2026, 12, 1)))
      assert result.ok?, result.errors.join(", ")
      record = record_for(org, title: "Renew the passport")
      refute record.key?("lead_skip")
      assert_equal "2026-12-01", record["deadline"]
    end
  end

  # A stamp naming a different occurrence — a foreign writer's leftover — must
  # not release the current one.
  def test_a_stale_stamp_releases_nothing
    records = lead_records(deadline: "2026-11-01", lead: "3w")
    records.last["lead_skip"] = "2026-08-01"

    assert_unavailable(records, on: "2026-10-01", until_date: "2026-10-11")
  end

  # A dry-run previews the state AFTER the write, and every lead write clears
  # the release stamp — so a preview must not inherit "already released".
  def test_previewing_a_new_lead_ignores_a_stamp_the_write_would_retire
    records = lead_records(deadline: "2026-11-01", lead: "3w")
    records.last["lead_skip"] = "2026-11-01"

    with_lead_store(records, on: "2026-10-01") do |query|
      item = query.snapshot.items.find { |candidate| candidate.id == "bbbb0002" }
      assert query.availability(item).available?, "the current occurrence is released"

      preview = query.availability_after(item, deferred: false,
                                         scheduled: item.scheduled_value, lead: "2w")
      refute preview.available?, "setting a lead re-arms the window"
      assert_equal Date.new(2026, 10, 18), preview.scheduled
    end
  end

  # -- create ---------------------------------------------------------------

  def test_create_accepts_a_lead_and_applies_the_same_rules
    with_lead_store_files(lead_records(deadline: "2026-11-01", lead: nil)) do |store, org|
      result = store.create_task!(
        Tasks::CreateTask.new(title: "Renew the license", deadline: Date.new(2026, 11, 1),
                              lead: "3w", project: "Work"),
        today: Date.new(2026, 7, 1)
      )
      assert result.ok?, result.errors.join(", ")
      assert_equal "3w", record_for(org, title: "Renew the license")["lead"]

      dateless = store.create_task!(
        Tasks::CreateTask.new(title: "No date at all", lead: "3w", project: "Work"),
        today: Date.new(2026, 7, 1)
      )
      refute dateless.ok?
      assert_match(/needs a date to hide before/, dateless.errors.join(" "))

      junk = store.create_task!(
        Tasks::CreateTask.new(title: "Junk span", deadline: Date.new(2026, 11, 1),
                              lead: "soon", project: "Work"),
        today: Date.new(2026, 7, 1)
      )
      refute junk.ok?
      assert_match(/invalid lead time/, junk.errors.join(" "))
    end
  end

  # -- old fixtures ----------------------------------------------------------

  def test_a_record_with_no_lead_key_behaves_exactly_as_before
    with_lead_store(FIXTURE_RECORDS, on: "2026-07-14") do |query|
      item = query.snapshot.items.find { |candidate| candidate.id == FIX[:eval] }
      assert query.availability(item).available?
      assert_nil query.task(item).lead
      assert_nil query.task(item).lead_human
    end
  end

  private

  # A one-task fixture in a "Work" section, anchored however the caller asks.
  def lead_records(deadline: nil, scheduled: nil, lead: nil)
    task = { "type" => "task", "id" => "bbbb0002", "parent" => "bbbb0001", "state" => "TODO",
             "title" => "Renew the passport" }
    task["scheduled"] = scheduled if scheduled
    task["deadline"] = deadline if deadline
    task["lead"] = lead if lead
    [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "bbbb0001", "title" => "Work" },
      task,
    ]
  end

  def context_on(date)
    Tasks::TemporalContext.new(now: Time.utc(*date.split("-").map(&:to_i), 18), timezone: ZONE)
  end

  def with_lead_files(records)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, dump_fixture(records))
      yield org, archive
    end
  end

  def with_lead_store(records, on:)
    with_lead_files(records) do |org, archive|
      store = Tasks::Store.new(org: org, archive: archive)
      context = context_on(on)
      yield Tasks::TaskQueries.new(store.read_snapshot, temporal_context: context)
    end
  end

  def with_lead_store_files(records)
    with_lead_files(records) do |org, archive|
      yield Tasks::Store.new(org: org, archive: archive), org
    end
  end

  def availability_on(records, date)
    result = nil
    with_lead_store(records, on: date) do |query|
      item = query.snapshot.items.find { |candidate| candidate.id == "bbbb0002" }
      result = query.availability(item)
    end
    result
  end

  def assert_available(records, on:)
    assert availability_on(records, on).available?, "expected availability on #{on}"
  end

  def assert_unavailable(records, on:, until_date:)
    availability = availability_on(records, on)
    refute availability.available?, "expected the lead to hide the task on #{on}"
    assert_equal Date.iso8601(until_date), availability.scheduled
  end

  def patch_lead(records, id, value)
    result = nil
    with_lead_store_files(records) do |store|
      snapshot = store.edit_snapshot(id)
      result = store.patch_task!(Tasks::TaskPatch.from(snapshot, field: :lead, value: value))
    end
    result
  end
end
