# frozen_string_literal: true

require_relative "test_helper"
require "tasks/application"

# Calendar schedules through the store's write and completion paths.
# test_recur_calendar.rb owns the grammar itself; this file owns what the store
# accepts, stores, refuses, and rolls. The interval-cookie counterparts live in
# the recurrence section of test_store.rb and must keep passing unchanged.
class TestStoreRecurCalendar < Minitest::Test
  SECTION = { "type" => "section", "id" => "ca000001", "title" => "Work" }.freeze

  # A one-task store. `task` is merged over the defaults, so a case only spells
  # out the fields it cares about.
  def with_calendar_store(**task)
    record = { "type" => "task", "id" => "ca000002", "parent" => "ca000001",
               "state" => "NEXT", "title" => "Recurring" }.merge(task.transform_keys(&:to_s))
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture([{ "type" => "meta", "version" => 2 }, SECTION, record]))
      yield Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl")), org
    end
  end

  def complete(store, id, today:)
    snapshot = store.edit_snapshot(id)
    store.patch_task!(
      Tasks::TaskPatch.from(snapshot, field: :state, value: "DONE", history_label: "complete"),
      today: today
    )
  end

  def set_recur(store, id, cookie, today: Date.today)
    snapshot = store.edit_snapshot(id)
    store.patch_task!(
      Tasks::TaskPatch.from(snapshot, field: :recurrence, value: cookie, history_label: "recur"),
      today: today
    )
  end

  # -- writes accept canonical calendar cookies ------------------------------

  def test_patch_stores_every_canonical_calendar_shape_verbatim
    %w[w:mon w:mon,wed,fri 2w:tue +w:mon m:15 m:last m:2tue 3m:1,15 y:07-04 y:11:3thu]
      .each do |cookie|
      with_calendar_store(deadline: "2026-08-01") do |store, org|
        result = set_recur(store, "ca000002", cookie)

        assert result.ok?, "#{cookie}: #{result.errors.inspect}"
        assert_equal cookie, record_for(org, title: "Recurring")["recur"]
        assert_equal "2026-08-01", record_for(org, title: "Recurring")["deadline"],
                     "attaching a schedule never moves the stamp"
        assert Tasks::Check.check(org).ok?, "#{cookie} must survive the post-write check"
        assert store.items.find { |i| i.id == "ca000002" }.recurring?
      end
    end
  end

  def test_patch_still_refuses_an_unparsable_cookie
    with_calendar_store(deadline: "2026-08-01") do |store, org|
      before = File.read(org)

      result = set_recur(store, "ca000002", "w:funday")

      assert_equal :invalid, result.status
      assert_includes result.errors, "invalid recurrence cookie"
      assert_equal before, File.read(org)
    end
  end

  # The store takes the canonical spelling only; natural phrases are a surface
  # concern (the CLI/API/TUI normalize before they call in).
  def test_patch_refuses_a_natural_phrase
    with_calendar_store(deadline: "2026-08-01") do |store, _org|
      assert_equal :invalid, set_recur(store, "ca000002", "every monday").status
    end
  end

  def test_create_accepts_a_calendar_schedule_and_seeds_todays_stamp
    with_calendar_store(deadline: "2026-08-01") do |store, org|
      result = store.create_task!(
        Tasks::CreateTask.new(title: "Standup notes", recurrence: "w:mon", project: "Work"),
        today: Date.new(2026, 7, 28)
      )

      assert_equal :ok, result.status
      record = record_for(org, title: "Standup notes")
      assert_equal "w:mon", record["recur"]
      assert_equal "2026-07-28", record["scheduled"], "a recurring create starts repeating now"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_create_rejects_an_unparsable_calendar_cookie
    with_calendar_store(deadline: "2026-08-01") do |store, _org|
      result = store.create_task!(
        Tasks::CreateTask.new(title: "Broken", recurrence: "m:99", project: "Work"),
        today: Date.new(2026, 7, 28)
      )

      assert_equal :invalid, result.status
      assert_includes result.errors, "invalid recurrence cookie"
    end
  end

  # -- satisfiability guard ---------------------------------------------------
  #
  # `2y:02:5fri` parses, but a February with five Fridays needs a leap year, and
  # an odd anchor year's every-other-year parity only ever lands on odd years.
  # Storing it would leave a task that `done` can never roll.

  def test_patch_refuses_a_schedule_that_can_never_fire_from_this_stamp
    with_calendar_store(deadline: "2027-02-01") do |store, org|
      before = File.read(org)

      result = set_recur(store, "ca000002", "2y:02:5fri")

      assert_equal :invalid, result.status
      assert_match(/no occurrence of "2y:02:5fri"/, result.errors.first)
      assert_match(/may never fire for this anchor/, result.errors.first)
      assert_equal before, File.read(org), "a refused write leaves the file byte-identical"
    end
  end

  # Same schedule, an anchor whose parity does reach a leap February: accepted.
  # The guard is about this cookie on this stamp, not about the grammar.
  def test_the_same_schedule_is_accepted_from_a_reachable_stamp
    with_calendar_store(deadline: "2028-02-01") do |store, org|
      assert set_recur(store, "ca000002", "2y:02:5fri").ok?
      assert_equal "2y:02:5fri", record_for(org, title: "Recurring")["recur"]
      assert Tasks::Check.check(org).ok?
    end
  end

  # The guard reads the stamp completion would roll: deadline wins over
  # scheduled, exactly as advance_recurrence_records does.
  def test_the_guard_anchors_on_the_deadline_when_both_dates_exist
    with_calendar_store(scheduled: "2028-01-01", deadline: "2027-02-01") do |store, _org|
      assert_equal :invalid, set_recur(store, "ca000002", "2y:02:5fri").status,
                   "the deadline carries the schedule, so its parity decides"
    end
  end

  def test_create_refuses_a_schedule_that_can_never_fire_from_its_seeded_stamp
    with_calendar_store(deadline: "2026-08-01") do |store, org|
      before = File.read(org)

      result = store.create_task!(
        Tasks::CreateTask.new(title: "Never fires", recurrence: "2y:02:5fri",
                              deadline: Date.new(2027, 2, 1), project: "Work"),
        today: Date.new(2026, 7, 28)
      )

      assert_equal :invalid, result.status
      assert_match(/may never fire for this anchor/, result.errors.first)
      assert_equal [:recurrence], result.field_errors.keys
      assert_equal before, File.read(org)
    end
  end

  # The other way a parseable cookie is unwritable: the roll it would produce
  # lands outside the four-digit years a stored date is written with, so the
  # write would pass and then fail the post-write check — rolling every
  # completion back forever. Both stored shapes can overshoot.
  def test_patch_refuses_a_schedule_that_rolls_past_the_storable_year_range
    { "9999y:07-04" => "12025-07-04", "+9999y" => "12025-08-01",
      ".+9999y" => "12025-07-28", "++9999y" => "12025-08-01" }.each do |cookie, rolled|
      with_calendar_store(deadline: "2026-08-01") do |store, org|
        before = File.read(org)

        result = set_recur(store, "ca000002", cookie, today: Date.new(2026, 7, 28))

        assert_equal :invalid, result.status, cookie
        assert_match(/outside the four-digit years/, result.errors.first, cookie)
        assert_includes result.errors.first, rolled, cookie
        assert_equal before, File.read(org), "#{cookie}: a refused write leaves the file alone"
      end
    end
  end

  def test_create_refuses_a_recurrence_that_rolls_past_the_storable_year_range
    with_calendar_store(deadline: "2026-08-01") do |store, _org|
      result = store.create_task!(
        Tasks::CreateTask.new(title: "Millennial", recurrence: "+9999y",
                              deadline: Date.new(2026, 8, 1), project: "Work"),
        today: Date.new(2026, 7, 28)
      )

      assert_equal :invalid, result.status
      assert_match(/outside the four-digit years/, result.errors.first)
      assert_equal [:recurrence], result.field_errors.keys
    end
  end

  # Years within the range are untouched by the storability check.
  def test_a_far_but_storable_roll_is_accepted
    with_calendar_store(deadline: "2026-08-01") do |store, org|
      assert set_recur(store, "ca000002", "+500y", today: Date.new(2026, 7, 28)).ok?
      assert_equal "+500y", record_for(org, title: "Recurring")["recur"]
      assert Tasks::Check.check(org).ok?
    end
  end

  # `recur --on <date>` seeds the stamp with a separate date patch and then
  # patches the cookie, so the guard sees the seeded stamp — no CLI-side check
  # needed for the seeding path to be covered.
  def test_a_seeded_stamp_is_what_the_guard_validates
    with_calendar_store(title: "No date") do |store, org|
      seed = store.edit_snapshot("ca000002")
      assert store.patch_task!(Tasks::TaskPatch.from(
        seed, field: :deadline, value: Date.new(2027, 2, 1), history_label: "seed"
      )).ok?

      assert_equal :invalid, set_recur(store, "ca000002", "2y:02:5fri").status
      refute record_for(org, title: "No date").key?("recur")
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- rolling forward on done ------------------------------------------------

  def test_catch_up_rolls_to_the_next_occurrence_strictly_after_today
    with_calendar_store(scheduled: "2026-06-01", recur: "w:mon") do |store, org|
      result = complete(store, "ca000002", today: Date.new(2026, 7, 28))

      assert result.ok?
      record = record_for(org, title: "Recurring")
      assert_equal "2026-08-03", record["scheduled"], "the first Monday after a Tuesday completion"
      assert_equal "NEXT", record["state"], "a recurring task stays open"
      assert_equal "w:mon", record["recur"]
      refute record.key?("closed")
      assert_match(/- Did \[2026-07-28\]/, record["body"], "the occurrence is logged")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_one_hop_advances_a_single_occurrence_and_may_stay_in_the_past
    with_calendar_store(scheduled: "2026-06-01", recur: "+w:mon") do |store, org|
      result = complete(store, "ca000002", today: Date.new(2026, 7, 28))

      assert result.ok?
      record = record_for(org, title: "Recurring")
      assert_equal "2026-06-08", record["scheduled"],
                   "one hop from the stored Monday, still behind today"
      assert_equal "NEXT", record["state"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_both_dates_shift_by_the_same_offset
    with_calendar_store(scheduled: "2026-07-08", deadline: "2026-07-15",
                        recur: "m:15") do |store, org|
      assert complete(store, "ca000002", today: Date.new(2026, 7, 28)).ok?

      record = record_for(org, title: "Recurring")
      assert_equal "2026-08-15", record["deadline"], "the deadline carries the schedule"
      assert_equal "2026-08-08", record["scheduled"], "the seven-day lead time is preserved"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_every_nth_week_parity_survives_a_roll
    with_calendar_store(scheduled: "2026-07-06", recur: "2w:mon") do |store, org|
      assert complete(store, "ca000002", today: Date.new(2026, 8, 10)).ok?
      first = Date.iso8601(record_for(org, title: "Recurring")["scheduled"])
      assert_equal Date.new(2026, 8, 17), first
      assert_equal 0, (first - Date.new(2026, 7, 6)).to_i % 14, "still on the anchor's parity"

      assert complete(store, "ca000002", today: Date.new(2026, 9, 20)).ok?
      second = Date.iso8601(record_for(org, title: "Recurring")["scheduled"])
      assert_equal Date.new(2026, 9, 28), second
      assert_equal 0, (second - Date.new(2026, 7, 6)).to_i % 14, "parity holds across rolls"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_catch_up_rolls_a_stamp_years_stale_in_one_completion
    with_calendar_store(scheduled: "2019-01-07", recur: "w:mon") do |store, org|
      assert complete(store, "ca000002", today: Date.new(2030, 6, 1)).ok?

      assert_equal "2030-06-03", record_for(org, title: "Recurring")["scheduled"],
                   "the first Monday after the completion day, not the next one in 2019"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_rolling_a_calendar_schedule_drops_the_defer_tag
    with_calendar_store(scheduled: "2026-06-01", recur: "w:mon",
                        tags: %w[@home defer]) do |store, org|
      assert complete(store, "ca000002", today: Date.new(2026, 7, 28)).ok?

      assert_equal %w[@home], record_for(org, title: "Recurring")["tags"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_rolling_a_calendar_schedule_returns_a_delegated_occurrence_to_the_pool
    with_calendar_store(scheduled: "2026-06-01", recur: "w:mon") do |store, org|
      assert store.delegate_task!("ca000002", kind: "agent", mode: "implement").ok?
      assert store.claim_task!("ca000002", worker: "claude/opus/aaa").ok?

      assert complete(store, "ca000002", today: Date.new(2026, 7, 28)).ok?

      delegation = record_for(org, title: "Recurring")["delegation"]
      assert_equal "ready", delegation["status"], "the next occurrence is unclaimed work"
      assert_equal "implement", delegation["mode"]
      refute delegation.key?("assignee")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_a_calendar_roll_is_undoable_byte_for_byte
    with_calendar_store(scheduled: "2026-06-01", recur: "w:mon") do |store, org|
      before = File.read(org)

      assert complete(store, "ca000002", today: Date.new(2026, 7, 28)).ok?
      refute_equal before, File.read(org)

      assert_equal :ok, store.undo!.first
      assert_equal before, File.read(org)
    end
  end

  # An unreachable cookie that predates the guard (or arrived by a hand edit or
  # a device merge) is refused at completion time with the engine's reason —
  # the backstop behind the write-time check.
  def test_completion_refuses_a_stored_schedule_that_cannot_advance
    with_calendar_store(deadline: "2027-02-01", recur: "2y:02:5fri") do |store, org|
      before = File.read(org)

      result = complete(store, "ca000002", today: Date.new(2027, 2, 2))

      assert_equal :invalid, result.status
      assert_match(/may never fire for this anchor/, result.errors.first)
      assert_equal before, File.read(org)
    end
  end

  # -- cascade rules are unchanged by the second stored shape ------------------

  def cascade_records
    [
      { "type" => "meta", "version" => 2 },
      SECTION,
      { "type" => "task", "id" => "ca000010", "parent" => "ca000001", "state" => "NEXT",
        "title" => "Project" },
      { "type" => "task", "id" => "ca000011", "parent" => "ca000010", "state" => "NEXT",
        "title" => "Child", "scheduled" => "2026-06-01", "recur" => "w:mon" },
    ]
  end

  def with_records(records)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture(records))
      yield Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl")), org
    end
  end

  def test_a_cascaded_calendar_recurring_descendant_retires_instead_of_rolling
    with_records(cascade_records) do |store, org|
      assert complete(store, "ca000010", today: Date.new(2026, 7, 28)).ok?

      child = record_for(org, title: "Child")
      assert_equal "DONE", child["state"], "completing the parent completes it"
      assert_equal "2026-07-28", child["closed"]
      assert_equal "2026-06-01", child["scheduled"], "no date roll"
      refute child.key?("recur"), "the cookie is retired outright"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_a_calendar_recurring_parent_rolls_without_cascading
    records = cascade_records
    records[2] = records[2].merge("scheduled" => "2026-06-02", "recur" => "m:15")
    with_records(records) do |store, org|
      assert complete(store, "ca000010", today: Date.new(2026, 7, 28)).ok?

      parent = record_for(org, title: "Project")
      assert_equal "2026-08-15", parent["scheduled"], "the parent rolls"
      assert_equal "NEXT", parent["state"]
      child = record_for(org, title: "Child")
      assert_equal "NEXT", child["state"], "a rolling parent does not cascade"
      assert_equal "2026-06-01", child["scheduled"]
      assert_equal "w:mon", child["recur"]
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- check ------------------------------------------------------------------

  def test_check_accepts_stored_calendar_cookies_and_still_flags_junk
    good = %w[w:mon w:mon,wed,fri 2w:tue +w:mon m:15 m:last m:2tue m:lastfri 3m:1,15
              y:07-04 y:02-29 y:11:3thu 2y:02:5fri .+1w ++1m +2d]
    good.each do |cookie|
      with_calendar_store(deadline: "2026-08-01", recur: cookie) do |_store, org|
        assert Tasks::Check.check(org).ok?, "#{cookie} should lint clean"
      end
    end

    junk = ["w:funday", "m:0", "m:32", "y:13-01", "w:", ".+w:mon", "++m:15", "every monday",
            "W:MON", "w:mon,", "2w:mon,mon", " w:mon", "+1w ", 7]
    junk.each do |cookie|
      with_calendar_store(deadline: "2026-08-01", recur: cookie) do |_store, org|
        result = Tasks::Check.check(org)
        refute result.ok?, "#{cookie.inspect} should be reported"
        assert_match(/invalid recur cookie/, result.errors.map { |_line, msg| msg }.join)
      end
    end
  end

  # A junk cookie still degrades to non-recurring on read, so completion closes
  # the task normally instead of crashing.
  def test_an_unparsable_stored_cookie_still_reads_as_non_recurring
    with_calendar_store(deadline: "2026-08-01", recur: "w:funday") do |store, _org|
      refute store.items.find { |i| i.id == "ca000002" }.recurring?
    end
  end
end
