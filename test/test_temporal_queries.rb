# frozen_string_literal: true

require_relative "test_helper"
require "tasks/application"
require "tasks/temporal_context"

class TestTemporalQueries < Minitest::Test
  RECORDS = [
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "aa000001", "title" => "Work" },
    { "type" => "task", "id" => "aa000002", "parent" => "aa000001", "state" => "NEXT",
      "title" => "Released", "scheduled" => "2026-07-20", "scheduled_time" => { "local" => "09:00" } },
    { "type" => "task", "id" => "aa000003", "parent" => "aa000001", "state" => "NEXT",
      "title" => "Later today", "scheduled" => "2026-07-20", "scheduled_time" => { "local" => "09:01" } },
    { "type" => "task", "id" => "aa000004", "parent" => "aa000001", "state" => "NEXT",
      "title" => "Timed deadline", "deadline" => "2026-07-20", "deadline_time" => { "local" => "08:00" } },
    { "type" => "task", "id" => "aa000005", "parent" => "aa000001", "state" => "NEXT",
      "title" => "All day deadline", "deadline" => "2026-07-20" },
  ].freeze

  def with_application(records = RECORDS, context:)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(records))
      factory = Tasks::StoreFactory.new(org: org, archive: archive, journal_dir: File.join(dir, "journal"))
      app = Tasks::Application.new(store_factory: factory, temporal_context_factory: -> { context })
      yield app, org
    end
  end

  def test_exact_release_boundary_and_agenda_ordering
    context = Tasks::TemporalContext.new(now: Time.utc(2026, 7, 20, 16),
                                         timezone: "America/Los_Angeles")
    with_application(context: context) do |app, _org|
      result = app.view_tasks(:next)
      assert_includes result.items.map(&:title), "Released"
      refute_includes result.items.map(&:title), "Later today"

      later = app.get_task("aa000003")
      assert_equal "2026-07-20T16:01:00Z", later.available_at
      assert_equal "09:01", later.scheduled_time[:local]
      assert_equal "America/Los_Angeles", later.scheduled_time[:effective_timezone]

      agenda = app.view_tasks(:agenda).items.map(&:title)
      assert_operator agenda.index("Timed deadline"), :<, agenda.index("All day deadline")
    end
  end

  def test_timed_availability_releases_at_its_instant_without_writing
    before_context = Tasks::TemporalContext.new(now: Time.utc(2026, 7, 20, 15, 59),
                                                timezone: "America/Los_Angeles")
    at_context = Tasks::TemporalContext.new(now: Time.utc(2026, 7, 20, 16),
                                            timezone: "America/Los_Angeles")
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(RECORDS))
      factory = Tasks::StoreFactory.new(org: org, archive: archive,
                                        journal_dir: File.join(dir, "journal"))
      bytes = File.binread(org)

      before = Tasks::Application.new(store_factory: factory,
                                      temporal_context_factory: -> { before_context })
      at = Tasks::Application.new(store_factory: factory,
                                  temporal_context_factory: -> { at_context })
      refute_includes before.view_tasks(:next).items.map(&:title), "Released"
      assert_includes at.view_tasks(:next).items.map(&:title), "Released"
      assert_equal bytes, File.binread(org)
      refute File.exist?(File.join(dir, "journal", "index.json"))
    end
  end

  def test_activate_clears_a_later_today_time_without_touching_due
    context = Tasks::TemporalContext.new(now: Time.utc(2026, 7, 20, 16),
                                         timezone: "America/Los_Angeles")
    with_application(context: context) do |app, org|
      result = app.update_task("aa000003", { activate: true },
                               expected_revision: app.get_task("aa000003").revision)
      assert result.ok?, result.errors.inspect
      record = record_for(org, title: "Later today")
      assert_nil record["scheduled"]
      assert_nil record["scheduled_time"]
    end
  end

  def test_recurrence_skips_a_nonexistent_fixed_local_occurrence
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "bb000001", "title" => "Work" },
      { "type" => "task", "id" => "bb000002", "parent" => "bb000001", "state" => "NEXT",
        "title" => "DST recurrence", "scheduled" => "2026-03-01",
        "scheduled_time" => { "local" => "02:30", "timezone" => "America/Los_Angeles" },
        "recur" => "+1w" },
    ]
    context = Tasks::TemporalContext.new(now: Time.utc(2026, 3, 2, 12), timezone: "Etc/UTC")
    with_application(records, context: context) do |app, org|
      revision = app.get_task("bb000002").revision
      result = app.update_task("bb000002", { state: "DONE" }, expected_revision: revision)
      assert result.ok?, result.errors.inspect
      record = record_for(org, title: "DST recurrence")
      assert_equal "2026-03-15", record["scheduled"]
      assert_equal({ "local" => "02:30", "timezone" => "America/Los_Angeles" },
                   record["scheduled_time"])
    end
  end
end
