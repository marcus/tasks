# frozen_string_literal: true

require_relative "test_helper"
require "set"
require "tasks/application"
require "tasks/task_queries"
require "tui/views"

# The compatibility matrix rows the surface files do not already pin: that a
# lead-gated task is just a timed-unavailable task everywhere the view layer
# looks at it (flat/tree parity, reveal, project header counts), that a lead
# write obeys the same optimistic-concurrency rules as any other field, and
# that an old fixture with no lead key is untouched by any of it.
#
# Hermetic by construction: every case builds its own store under Dir.mktmpdir
# and never reads the developer's real task files.
class TestLeadMatrix < Minitest::Test
  V = Tui::Views
  A = Tui::Ansi
  TODAY = Date.new(2026, 7, 1)

  # Work → a lead-gated NEXT (deadline 2026-11-01, opens 2026-10-11) carrying an
  # undated rider, plus an ordinary available NEXT, so every view has both a
  # hidden and a visible anchor.
  RECORDS = [
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "fb000001", "title" => "Work" },
    { "type" => "task", "id" => "fb000002", "parent" => "fb000001", "state" => "NEXT",
      "title" => "Renew the passport", "deadline" => "2026-11-01", "lead" => "3w" },
    { "type" => "task", "id" => "fb000003", "parent" => "fb000002", "state" => "TODO",
      "title" => "Find the old one" },
    { "type" => "task", "id" => "fb000004", "parent" => "fb000001", "state" => "NEXT",
      "title" => "Review PR backlog" },
  ].freeze

  # -- view parity -----------------------------------------------------------

  def test_flat_tree_and_reveal_stay_aligned_on_a_lead_gated_subtree
    with_records(RECORDS) do |store|
      %i[agenda next quadrants inbox].each do |view|
        [false, true].each do |revealed|
          query = V.view_query(view, today: TODAY, urgent_days: 3,
                               show_deferred: revealed, store: store)
          eligible = query.select(store.items).map(&:id).to_set
          flat = V.rows(view, store.items, today: TODAY, urgent_days: 3,
                        show_deferred: revealed, store: store)
                  .filter_map { |row| row.item&.id }
          tree = V.rows(view, store.items, tree: store.tree, today: TODAY, urgent_days: 3,
                        show_deferred: revealed, store: store)
                  .filter_map { |row| row.item&.id }

          assert_equal eligible, flat.to_set,
                       "flat #{view} reveal=#{revealed} uses canonical eligibility"
          assert eligible.subset?(tree.to_set),
                 "tree #{view} reveal=#{revealed} contains every canonical anchor"
          assert_equal tree.uniq, tree, "tree #{view} reveal=#{revealed} renders no duplicates"
        end
      end
    end
  end

  def test_reveal_shows_the_lead_gated_anchor_and_its_rider
    with_records(RECORDS) do |store|
      hidden = tree_rows(store, :next).filter_map { |row| row.item&.id }
      refute_includes hidden, "fb000002", "the lead hides its own anchor"
      refute_includes hidden, "fb000003", "and everything the anchor gates"
      assert_includes hidden, "fb000004"

      shown = tree_rows(store, :next, show_deferred: true).filter_map { |row| row.item&.id }
      assert_equal %w[fb000002 fb000003], shown & %w[fb000002 fb000003],
                   "revealing a lead-gated anchor carries its rider, exactly as a defer does"
    end
  end

  # A lead hides a task from the working views; it does NOT make it "held" the
  # way an indefinite hold does, so the project rollup still counts it.
  def test_project_header_counts_agree_with_the_read_model
    with_records(RECORDS) do |store|
      views = Tasks::TaskQueries.new(store.read_snapshot, today: TODAY).projects
      [false, true].each do |revealed|
        rows = V.rows(:projects, store.items, tree: store.tree, today: TODAY,
                      show_deferred: revealed, store: store, projects: views)
        header = rows.find { |row| row.project&.title == "Work" }
        refute_nil header
        assert_equal header.project.open_count,
                     A.strip(header.text)[/(\d+) open/, 1].to_i,
                     "project header count matches its ProjectView roll-up"
      end

      work = views.find { |view| view.title == "Work" }
      assert_equal 3, work.open_count, "a timed gate is not an indefinite hold"
      assert_equal 0, work.held_count
    end
  end

  # -- boundaries ------------------------------------------------------------

  def test_the_window_opens_on_the_derived_date_and_not_the_day_before
    day_before = availability_on(RECORDS, Date.new(2026, 10, 10))
    refute day_before.available?
    assert_equal Date.new(2026, 10, 11), day_before.scheduled

    opening_day = availability_on(RECORDS, Date.new(2026, 10, 11))
    assert opening_day.available?

    anchor_day = availability_on(RECORDS, Date.new(2026, 11, 1))
    assert anchor_day.available?, "and stays available through the anchor"
  end

  # The boundary is an INSTANT, so probe the minute either side of it rather
  # than mid-day, where a whole-day error would go unnoticed.
  def test_the_window_opens_at_the_first_instant_of_the_derived_date
    # 2026-10-11T06:00:00Z is 00:00 in Denver (MDT).
    refute availability_at(Time.utc(2026, 10, 11, 5, 59)).available?,
           "one minute before the window opens"
    assert availability_at(Time.utc(2026, 10, 11, 6, 0)).available?,
           "the first instant of the derived date"
  end

  # The renderer's date-grained fallback (used when a renderer has no canonical
  # reader) must agree with the query about which rows are hidden — including
  # for a clock lead, which has no date-grained answer of its own, and for a
  # stray release stamp on a task with no lead.
  def test_the_renderer_fallback_agrees_with_the_query_about_hidden_rows
    clock = RECORDS.map(&:dup)
    clock[2] = clock[2].merge("lead" => "5h")
    stray = RECORDS.map(&:dup)
    stray[2] = { "type" => "task", "id" => "fb000002", "parent" => "fb000001", "state" => "NEXT",
                 "title" => "Renew the passport", "scheduled" => "2026-12-01",
                 "lead_skip" => "2026-12-01" }

    [RECORDS, clock, stray].each_with_index do |records, index|
      with_records(records) do |store|
        item = store.items.find { |candidate| candidate.id == "fb000002" }
        fallback = V.availability_for(item, today: TODAY)
        canonical = Tasks::TaskQueries.new(store.read_snapshot, today: TODAY).availability(item)
        assert_equal canonical.available?, fallback.available?,
                     "fixture #{index}: fallback and query disagree about visibility"
      end
    end

    # And the clock fallback rounds away from the anchor, matching the query's
    # instant (5h before a midnight release is the previous day).
    with_records(clock) do |store|
      item = store.items.find { |candidate| candidate.id == "fb000002" }
      assert_equal Date.new(2026, 10, 31), V.availability_for(item, today: TODAY).scheduled
    end
  end

  # -- concurrency -----------------------------------------------------------

  def test_a_lead_write_conflicts_like_any_other_field
    with_files(RECORDS) do |org, archive|
      store = Tasks::Store.new(org: org, archive: archive)
      stale = store.edit_snapshot("fb000002")

      first = store.patch_task!(Tasks::TaskPatch.from(stale, field: :lead, value: "2w"))
      assert first.ok?, first.errors.join(", ")

      # The stale snapshot still carries the pre-write expectation, so a second
      # write from it is refused rather than clobbering the first.
      second = store.patch_task!(Tasks::TaskPatch.from(stale, field: :lead, value: "1w"))
      refute second.ok?
      assert second.stale? || second.conflict?, "expected a conflict, got #{second.status}"
      assert_equal "2w", record_for(org, title: "Renew the passport")["lead"],
                   "the refused write changed nothing"

      fresh = store.edit_snapshot("fb000002")
      retried = store.patch_task!(Tasks::TaskPatch.from(fresh, field: :lead, value: "1w"))
      assert retried.ok?, retried.errors.join(", ")
      assert_equal "1w", record_for(org, title: "Renew the passport")["lead"]
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- forward compatibility -------------------------------------------------

  # The degradation an older binary shows: it round-trips both keys untouched
  # (Format's forward-compat rule) and simply does not apply the gate.
  def test_unknown_writers_round_trip_the_keys_and_no_lead_key_is_untouched
    with_files(RECORDS) do |org, archive|
      before = File.read(org)
      store = Tasks::Store.new(org: org, archive: archive)
      snapshot = store.edit_snapshot("fb000004")
      result = store.patch_task!(Tasks::TaskPatch.from(snapshot, field: :priority, value: "A"))
      assert result.ok?

      untouched = record_for(org, title: "Renew the passport")
      assert_equal "3w", untouched["lead"], "an unrelated write preserves another record's lead"
      refute_equal before, File.read(org)
      assert Tasks::Check.check(org).ok?
    end

    # A record with no lead key at all keeps every pre-lead answer.
    with_files(FIXTURE_RECORDS) do |org, archive|
      store = Tasks::Store.new(org: org, archive: archive)
      query = Tasks::TaskQueries.new(store.read_snapshot, today: TODAY)
      item = query.snapshot.items.find { |candidate| candidate.id == FIX[:plants] }
      assert query.availability(item).available?
      assert_nil item.lead
      assert_nil item.lead_skip
    end
  end

  private

  def with_files(records)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, dump_fixture(records))
      return yield org, archive
    end
  end

  def with_records(records)
    with_files(records) do |org, archive|
      return yield Tui::Store.new(org: org, archive: archive)
    end
  end

  def tree_rows(store, view, show_deferred: false)
    V.rows(view, store.items, tree: store.tree, show_deferred: show_deferred,
           today: TODAY, urgent_days: 3)
  end

  def availability_at(now)
    with_files(RECORDS) do |org, archive|
      store = Tasks::Store.new(org: org, archive: archive)
      context = Tasks::TemporalContext.new(now: now, timezone: "America/Denver")
      query = Tasks::TaskQueries.new(store.read_snapshot, temporal_context: context)
      item = query.snapshot.items.find { |candidate| candidate.id == "fb000002" }
      return query.availability(item)
    end
  end

  def availability_on(records, date)
    with_files(records) do |org, archive|
      store = Tasks::Store.new(org: org, archive: archive)
      context = Tasks::TemporalContext.new(
        now: Time.utc(date.year, date.month, date.day, 18), timezone: "America/Denver"
      )
      query = Tasks::TaskQueries.new(store.read_snapshot, temporal_context: context)
      item = query.snapshot.items.find { |candidate| candidate.id == "fb000002" }
      return query.availability(item)
    end
  end
end
