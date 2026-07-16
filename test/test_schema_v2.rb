# frozen_string_literal: true

require_relative "test_helper"
require "tasks/temporal_value"

class TestSchemaV2 < Minitest::Test
  def test_time_metadata_is_canonical_and_checked
    record = { "deadline_time" => { "fold" => 1, "timezone" => "Europe/London", "local" => "17:00" },
               "deadline" => "2026-07-20", "title" => "Call", "state" => "NEXT",
               "id" => "aaaa0001", "type" => "task" }
    line = Tasks::Format.dump_record(record)
    assert_operator line.index('"deadline"'), :<, line.index('"deadline_time"')
    assert_includes line, '"deadline_time":{"local":"17:00","timezone":"Europe/London","fold":1}'

    valid = Tasks::Check.check_text(Tasks::Format.dump([
      { "type" => "meta", "version" => 2 }, record,
    ]))
    assert valid.ok?, valid.errors.inspect
  end

  def test_check_rejects_orphans_shapes_zones_and_dst_gaps
    cases = [
      { "deadline_time" => { "local" => "09:00" } },
      { "deadline" => "2026-07-20", "deadline_time" => "09:00" },
      { "deadline" => "2026-07-20", "deadline_time" => { "local" => "9:00" } },
      { "deadline" => "2026-07-20", "deadline_time" => { "local" => "09:00", "timezone" => "PST" } },
      { "deadline" => "2026-03-08", "deadline_time" =>
        { "local" => "02:30", "timezone" => "America/Los_Angeles" } },
    ]
    cases.each_with_index do |fields, index|
      records = [{ "type" => "meta", "version" => 2 },
                 { "type" => "task", "id" => format("aaaa%04d", index),
                   "state" => "NEXT", "title" => "Bad" }.merge(fields)]
      refute Tasks::Check.check_text(Tasks::Format.dump(records)).ok?, fields.inspect
    end
  end

  def test_store_round_trips_and_undoes_atomic_temporal_patch
    with_store do |store, org, _archive|
      item = find_item(store, "Book flight")
      value = Tasks::TemporalValue.new(date: "2026-11-01", local_time: "01:30",
                                       timezone: "America/Los_Angeles", fold: 1)
      result = store.patch_task!(Tasks::TaskPatch.from(
        store.edit_snapshot(item.id), field: :deadline, value: value,
        history_label: "timed deadline"
      ))
      assert result.ok?, result.errors.inspect
      record = record_for(org, title: "Book flight in Concur")
      assert_equal "2026-11-01", record["deadline"]
      assert_equal({ "local" => "01:30", "timezone" => "America/Los_Angeles", "fold" => 1 },
                   record["deadline_time"])
      assert_equal value, store.items.find { |candidate| candidate.id == item.id }.deadline_value
      assert Tasks::Check.check(org).ok?
      assert_equal :ok, store.undo!.first
      assert_nil record_for(org, title: "Book flight in Concur")["deadline_time"]
    end
  end

  def test_v1_migration_changes_only_meta_and_establishes_backups
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      journal = File.join(dir, "journal")
      live_records = [{ "type" => "meta", "version" => 1 },
                      { "type" => "task", "id" => "aaaa0001", "state" => "NEXT",
                        "title" => "Existing", "deadline" => "2026-07-20" }]
      archived_records = [{ "type" => "meta", "version" => 1 },
                          { "type" => "task", "id" => "bbbb0001", "state" => "DONE",
                            "title" => "Old", "closed" => "2026-07-01" }]
      File.write(org, Tasks::Format.dump(live_records))
      File.write(archive, Tasks::Format.dump(archived_records))
      store = Tasks::Store.new(org: org, archive: archive, journal_dir: journal)

      preview = store.migrate_schema!(dry_run: true)
      assert_equal :dry_run, preview.status
      assert_equal 1, JSON.parse(File.foreach(org).first)["version"]

      result = store.migrate_schema!
      assert result.ok?, result.errors.inspect
      assert_equal 2, JSON.parse(File.foreach(org).first)["version"]
      assert_equal 2, JSON.parse(File.foreach(archive).first)["version"]
      assert_equal live_records.drop(1), Tasks::Format.parse(File.read(org)).records.drop(1).map { |r| r.except("line") }
      assert File.exist?("#{org}.v1.bak")
      assert File.exist?("#{archive}.v1.bak")
      assert_equal :empty, store.undo!.first
      assert_equal :already_current, store.migrate_schema!.status
    end
  end

  def test_migration_rolls_both_files_back_when_installation_fails
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      v1 = Tasks::Format.dump([{ "type" => "meta", "version" => 1 }])
      File.write(org, v1)
      File.write(archive, v1)
      store = Tasks::Store.new(org: org, archive: archive, journal_dir: File.join(dir, "journal"))
      original = Tasks::Atomic.method(:write)
      Tasks::Atomic.stub(:write, lambda { |path, content|
        raise IOError, "install failed" if path == archive && content.include?('"version":2')
        original.call(path, content)
      }) do
        result = store.migrate_schema!
        assert_equal :rolled_back, result.status
      end
      assert_equal v1, File.read(org)
      assert_equal v1, File.read(archive)
    end
  end
end
