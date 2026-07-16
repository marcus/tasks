# frozen_string_literal: true

require_relative "test_helper"
require "tasks/application"
require "tasks/update_stamp"

class TestUpdateStamp < Minitest::Test
  STAMP = "2026-07-16T14:03:11Z#home"
  OLD_STAMP = "2026-07-15T09:00:00Z#work"

  def stamped_records
    records = FIXTURE_RECORDS.map(&:dup)
    records.find { |record| record["id"] == FIX[:flight] }["updated"] = OLD_STAMP
    records
  end

  def with_stamped_store
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(stamped_records))
      store = Tasks::Store.new(
        org: org, archive: archive,
        now: -> { Time.utc(2026, 7, 16, 14, 3, 11) }, device: "home"
      )
      yield store, org, archive
    end
  end

  def record(path, id)
    Tasks::Format.parse(File.read(path, encoding: "UTF-8")).records.find { |entry| entry["id"] == id }
  end

  def test_format_round_trips_updated_after_body
    input = {
      "updated" => STAMP, "body" => "note", "title" => "Task", "state" => "TODO",
      "id" => "abcd1234", "type" => "task",
    }

    line = Tasks::Format.dump_record(input)

    assert_operator line.index('"body"'), :<, line.index('"updated"')
    assert_equal STAMP, Tasks::Format.parse("#{line}\n").records.first["updated"]
  end

  def test_check_rejects_malformed_updated_value
    records = stamped_records
    records.find { |entry| entry["id"] == FIX[:flight] }["updated"] = "yesterday#home"

    result = Tasks::Check.check_text(Tasks::Format.dump(records))

    refute result.ok?
    assert_includes result.errors.map(&:last).join("\n"), "is not an RFC3339 UTC timestamp with device slug"
  end

  def test_patch_stamps_only_the_semantically_touched_record
    with_stamped_store do |store, org, _archive|
      snapshot = store.edit_snapshot(FIX[:pr])
      result = store.patch_task!(Tasks::TaskPatch.from(snapshot, field: :title, value: "Reviewed backlog"))

      assert result.ok?
      assert_equal STAMP, record(org, FIX[:pr])["updated"]
      assert_equal OLD_STAMP, record(org, FIX[:flight])["updated"]
      assert_nil record(org, FIX[:eval])["updated"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_no_op_save_preserves_bytes_and_existing_stamps
    with_stamped_store do |store, org, _archive|
      records = Tasks::Format.parse(File.read(org, encoding: "UTF-8")).records
      before = File.binread(org)

      store.send(:write_records, org, records)

      assert_equal before, File.binread(org)
      assert_equal OLD_STAMP, record(org, FIX[:flight])["updated"]
      assert_nil record(org, FIX[:pr])["updated"]
    end
  end

  def test_create_stamps_new_record_delete_removes_it_and_undo_restores_raw_snapshot
    with_stamped_store do |store, org, _archive|
      create = store.create_task!(Tasks::CreateTask.new(title: "Merge proof", project: "Work"))
      created_id = create.touched_ids.fetch(0)

      assert_equal STAMP, record(org, created_id)["updated"]
      created_bytes = File.binread(org)

      deletion = store.delete_task!(Tasks::DeleteTask.new(id: created_id))
      assert deletion.ok?
      assert_nil record(org, created_id)

      assert_equal [:ok, "delete: Merge proof"], store.undo!
      assert_equal created_bytes, File.binread(org), "undo restores bytes without re-stamping"
      assert_equal STAMP, record(org, created_id)["updated"]
    end
  end

  def test_clock_device_and_hostname_slug_are_deterministic
    assert_equal "marcus", Tasks::UpdateStamp.slug("Marcus-MBP.local")
    assert_equal "home2", Tasks::UpdateStamp.device(env: { "TASKS_DEVICE" => "Home2" }, hostname: "ignored")
    assert_equal STAMP, Tasks::UpdateStamp.format(Time.utc(2026, 7, 16, 14, 3, 11), "HOME")
    assert_operator Tasks::UpdateStamp.compare("2026-07-16T14:03:11Z#home",
                                               "2026-07-16T14:03:11Z#work"), :<, 0
  end

  def test_updated_alone_does_not_change_task_revision
    with_stamped_store do |store, org, archive|
      before = store.edit_snapshot(FIX[:flight]).revision
      records = Tasks::Format.parse(File.read(org, encoding: "UTF-8")).records
      records.find { |record| record["id"] == FIX[:flight] }["updated"] = STAMP
      File.write(org, Tasks::Format.dump(records))

      after = Tasks::Store.new(org: org, archive: archive).edit_snapshot(FIX[:flight]).revision

      assert_equal before, after
    end
  end
end
