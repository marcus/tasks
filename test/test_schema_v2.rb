# frozen_string_literal: true

require_relative "test_helper"
require "tasks/application"
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
      stale = store.edit_snapshot(item.id)
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
      stale_result = store.patch_task!(Tasks::TaskPatch.from(
        stale, field: :deadline,
        value: Tasks::TemporalValue.new(date: "2026-11-01", local_time: "02:30")
      ))
      assert stale_result.conflict?, "time/zone metadata must participate in field conflicts"
      assert Tasks::Check.check(org).ok?
      assert_equal :ok, store.undo!.first
      assert_nil record_for(org, title: "Book flight in Concur")["deadline_time"]
    end
  end

  # Delegation is additive like PROPOSED: absent means not delegated, so there
  # is no backfill, no migration, and no version bump. The compatibility cost is
  # one-directional — an older binary fails `check` on a store that uses it.
  def test_delegation_is_additive_and_does_not_bump_the_schema_version
    assert_equal 2, Tasks::Format::VERSION

    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, FIXTURE)
      store = Tasks::Store.new(org: org, archive: archive, journal_dir: File.join(dir, "journal"))
      assert store.delegate_task!(FIX[:plants], kind: "agent", mode: "research").ok?

      assert_equal 2, JSON.parse(File.foreach(org).first).fetch("version")
      assert Tasks::Check.check(org).ok?
      refute store.checked_read_snapshot.unsupported_schema?
    end
  end

  def test_ordinary_mutation_against_v1_is_refused_as_an_unsupported_schema
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      records = [{ "type" => "meta", "version" => 1 },
                 { "type" => "task", "id" => "aaaa0001", "state" => "NEXT", "title" => "Existing" }]
      File.write(org, Tasks::Format.dump(records))
      store = Tasks::Store.new(org: org, archive: archive, journal_dir: File.join(dir, "journal"))
      patch = Tasks::TaskPatch.new(id: "aaaa0001", field: :priority, value: "A", expected: nil)
      result = store.patch_task!(patch)
      assert_equal :unsupported_schema, result.status
      assert result.unsupported_schema?
      assert_equal ["unsupported meta version 1 (expected 2)"], result.errors
      assert_equal 1, JSON.parse(File.foreach(org).first).fetch("version")
    end
  end

  def test_project_mutation_against_v1_is_refused_as_an_unsupported_schema
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump([{ "type" => "meta", "version" => 1 }]))
      application = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive)
      )

      result = application.create_project(title: "New project")

      assert_equal :unsupported_schema, result.status
      assert_equal ["unsupported meta version 1 (expected 2)"], result.errors
      assert_equal 1, JSON.parse(File.foreach(org).first).fetch("version")
    end
  end

  def test_task_mutation_refuses_a_v1_archive_beside_a_v2_live_store
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump([
        { "type" => "meta", "version" => 2 },
        { "type" => "task", "id" => "aaaa0001", "state" => "NEXT", "title" => "Existing" },
      ]))
      File.write(archive, Tasks::Format.dump([{ "type" => "meta", "version" => 1 }]))
      store = Tasks::Store.new(org: org, archive: archive)
      patch = Tasks::TaskPatch.new(id: "aaaa0001", field: :priority, value: "A", expected: nil)

      result = store.patch_task!(patch)

      assert_equal :unsupported_schema, result.status
      assert_equal ["archive: unsupported meta version 1 (expected 2)"], result.errors
      assert_nil JSON.parse(File.readlines(org)[1]).fetch("priority", nil)
    end
  end

  def test_archive_and_history_against_v1_are_refused_as_an_unsupported_schema
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump([
        { "type" => "meta", "version" => 1 },
        { "type" => "task", "id" => "aaaa0001", "state" => "DONE", "title" => "Existing" },
      ]))
      store = Tasks::Store.new(org: org, archive: archive)

      assert_equal [:unsupported_schema], store.undo!
      refusal = store.archive_swept!
      assert_instance_of Tasks::Store::ArchiveRefusal, refusal
      assert_equal :unsupported_schema, refusal.reason
      refute File.exist?(archive)
    end
  end
  # There is no migration path in either direction, so the version gate is the
  # only thing standing between v1 bytes and a reader that would interpret them
  # as v2. It refuses, names the version, and never names a command to run.
  def test_checked_read_refuses_a_v1_store_and_names_no_migration
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump([
        { "type" => "meta", "version" => 1 },
        { "type" => "task", "id" => "aaaa0001", "state" => "NEXT", "title" => "Existing" },
      ]))
      checked = Tasks::Store.new(org: org, archive: archive).checked_read_snapshot

      assert_equal :unsupported_schema, checked.status
      assert checked.unsupported_schema?
      assert_nil checked.snapshot, "v1 records must never be handed to a v2 reader"
      assert_equal [{ source: :live, line: 1,
                      message: "unsupported meta version 1 (expected 2)" }], checked.errors
      refute_match(/migrat/i, checked.errors.map { |error| error[:message] }.join)
    end
  end

  # Forward skew is the live case (Marcus runs several devices): a store written
  # by a newer binary is refused by exactly the same gate, not silently read.
  def test_a_future_schema_version_is_refused_on_read_and_on_mutation
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump([
        { "type" => "meta", "version" => 3 },
        { "type" => "task", "id" => "aaaa0001", "state" => "NEXT", "title" => "Existing" },
      ]))
      store = Tasks::Store.new(org: org, archive: archive, journal_dir: File.join(dir, "journal"))

      assert_equal :unsupported_schema, store.checked_read_snapshot.status
      result = store.patch_task!(
        Tasks::TaskPatch.new(id: "aaaa0001", field: :priority, value: "A", expected: nil)
      )
      assert_equal :unsupported_schema, result.status
      assert_equal ["unsupported meta version 3 (expected 2)"], result.errors
      assert_equal 3, JSON.parse(File.foreach(org).first).fetch("version")
    end
  end

  # A create is the one mutation that bootstraps missing files, so it gets its
  # own proof that the gate runs before the bootstrap.
  def test_create_against_a_v1_store_is_refused_and_writes_nothing
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump([{ "type" => "meta", "version" => 1 }]))
      before = File.binread(org)
      store = Tasks::Store.new(org: org, archive: archive, journal_dir: File.join(dir, "journal"))

      result = store.create_task!(Tasks::CreateTask.new(title: "New"))

      assert_equal :unsupported_schema, result.status
      assert_equal before, File.binread(org)
    end
  end

  # The gate covers BOTH files, and it must not be possible for trouble reading
  # one to quietly cancel the check on the other. A blanket `rescue nil` around
  # the whole scan did exactly that: any read error on the live file skipped
  # the archive check and reported the store "supported" — the gate switched
  # itself off in response to the I/O trouble that should make it more careful.
  def test_an_unreadable_live_file_does_not_suppress_the_archive_half_of_the_gate
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump([{ "type" => "meta", "version" => 2 }]))
      File.write(archive, Tasks::Format.dump([{ "type" => "meta", "version" => 1 }]))
      File.chmod(0o000, org)
      skip "running as a user that ignores file permissions" if File.readable?(org)

      store = Tasks::Store.new(org: org, archive: archive)

      assert store.unsupported_schema?, "the v1 archive must still be seen"
      assert_equal "archive: unsupported meta version 1 (expected 2)", store.unsupported_schema_error
    ensure
      File.chmod(0o600, org) if File.exist?(org)
    end
  end

  # An unreadable file is not itself a version refusal — that is Check's report
  # to make, with a line number. The narrowed rescue tolerates exactly that.
  def test_an_unreadable_file_alone_is_not_reported_as_version_skew
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump([{ "type" => "meta", "version" => 2 }]))
      File.write(archive, Tasks::Format.dump([{ "type" => "meta", "version" => 2 }]))
      File.chmod(0o000, org)
      skip "running as a user that ignores file permissions" if File.readable?(org)

      refute Tasks::Store.new(org: org, archive: archive).unsupported_schema?
    ensure
      File.chmod(0o600, org) if File.exist?(org)
    end
  end

  # One rule, one implementation. The TUI used to hand-roll a third copy of the
  # version check beside Store's gate and Check's linter; three implementations
  # of one rule is three chances for a surface to disagree about whether a
  # store is readable.
  def test_the_tui_and_the_store_answer_the_version_question_identically
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      [1, 2, 3].each do |version|
        File.write(org, Tasks::Format.dump([{ "type" => "meta", "version" => version }]))
        File.write(archive, Tasks::Format.dump([{ "type" => "meta", "version" => 2 }]))
        store = Tasks::Store.new(org: org, archive: archive)
        expected = version != Tasks::Format::VERSION

        assert_equal expected, store.unsupported_schema?, "live v#{version}"
        assert_equal expected, !Tasks::Check.unsupported_version(
          Tasks::Format.parse(File.read(org)).records
        ).nil?, "Check disagrees with Store at v#{version}"
      end
    end
  end
end
