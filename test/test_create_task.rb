# frozen_string_literal: true

require_relative "test_helper"
require "tasks/application"

class TestCreateTask < Minitest::Test
  def with_create_store(records: FIXTURE_RECORDS, max_depth: Tasks::Tree::DEFAULT_MAX_DEPTH)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, dump_fixture(records))
      yield Tasks::Store.new(org: org, archive: archive, max_depth: max_depth), org, archive
    end
  end

  def command(**attributes)
    Tasks::CreateTask.new(title: "Draft proposal", **attributes)
  end

  def test_create_task_is_immutable_and_copies_mutable_inputs
    title = +"Draft proposal"
    notes = [+"first", +"second"]
    request = Tasks::CreateTask.new(title: title, notes: notes, recurrence: +".+1w")
    title.replace("mutated")
    notes.first.replace("mutated")

    assert request.frozen?
    assert request.title.frozen?
    assert request.notes.frozen?
    assert request.notes.first.frozen?
    assert_equal "Draft proposal", request.title
    assert_equal %w[first second], request.notes
    assert_equal ".+1w", request.recurrence
    refute_includes Tasks::Store.public_instance_methods(false), :capture!
  end

  def test_create_writes_all_fields_and_initial_notes_once_then_undoes_as_one_step
    with_create_store do |store, org, _archive|
      before = File.binread(org)
      writes = 0
      original_writer = store.method(:write_records)
      writer = lambda do |path, records|
        writes += 1
        original_writer.call(path, records)
      end
      request = command(
        priority: "A", tags: %w[@work important], scheduled: Date.new(2026, 8, 1),
        deadline: Date.new(2026, 8, 8), state: "WAITING", project: "Work",
        recurrence: ".+1w", notes: ["first supplied note", "second supplied note"]
      )

      result = store.stub(:write_records, writer) { store.create_task!(request) }

      assert_equal :ok, result.status
      assert_equal 1, writes
      assert_equal [result.snapshot.id], result.touched_ids
      record = record_for(org, title: "Draft proposal")
      assert_equal FIX[:work], record["parent"]
      assert_equal "A", record["priority"]
      assert_equal %w[@work important], record["tags"]
      assert_equal "2026-08-01", record["scheduled"]
      assert_equal "2026-08-08", record["deadline"]
      assert_equal ".+1w", record["recur"]
      assert_equal "WAITING", record["state"]
      assert_equal "Captured [#{Date.today}].\nfirst supplied note\nsecond supplied note", record["body"]
      assert Tasks::Check.check(org).ok?

      assert_equal [:ok, "capture: Draft proposal"], store.undo!
      assert_equal before, File.binread(org), "one undo removes the complete create transaction"
      assert_equal [:empty], store.undo!
    end
  end

  def test_create_recurring_task_defaults_to_today_and_uses_the_processed_state
    with_create_store do |store, org, _archive|
      result = store.create_task!(command(recurrence: ".+1w"), today: Date.new(2026, 9, 4))

      assert_equal :ok, result.status
      record = record_for(org, title: "Draft proposal")
      assert_equal "2026-09-04", record["scheduled"]
      assert_equal "TODO", record["state"]
      assert_equal ".+1w", record["recur"]
      assert_match(/Captured \[2026-09-04\]/, record["body"])
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_application_create_accepts_own_indefinite_hold_atomically
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, FIXTURE)
      app = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive)
      )
      before = File.binread(org)

      result = app.create_task(
        { title: "Held from creation", project: "Work", deferred: true },
        today: Date.new(2026, 7, 14)
      )

      assert result.ok?
      record = record_for(org, title: "Held from creation")
      assert_includes record.fetch("tags"), Tasks::Store::DEFER_TAG
      task = Tasks::TaskQueries.new(result.read_snapshot, today: Date.new(2026, 7, 14))
                                .find(result.touched_ids.fetch(0))
      refute task.available?
      assert_equal :on_hold, task.availability_reason
      store = Tasks::Store.new(org: org, archive: archive)
      assert_equal [:ok, "capture: Held from creation"], store.undo!
      assert_equal before, File.binread(org)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_application_adds_host_context_alongside_explicit_contexts
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, FIXTURE)
      app = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive),
        host_context: "@home"
      )

      result = app.create_task(
        { title: "Call from laptop", tags: %w[@computer follow-up] }
      )

      assert result.ok?
      assert_equal %w[@home @computer follow-up],
                   record_for(org, title: "Call from laptop").fetch("tags")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_application_deduplicates_or_explicitly_suppresses_host_context
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, FIXTURE)
      app = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive),
        host_context: "@home"
      )

      assert app.create_task({ title: "Already home", tags: %w[@home @computer] }).ok?
      assert app.create_task(
        { title: "Work only", tags: %w[@work], apply_host_context: false }
      ).ok?
      assert_equal %w[@home @computer],
                   record_for(org, title: "Already home").fetch("tags")
      assert_equal %w[@work], record_for(org, title: "Work only").fetch("tags")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_create_rejects_non_boolean_host_context_policy
    with_create_store do |store, org, _archive|
      result = store.create_task!(command(apply_host_context: "no"))

      assert_equal :invalid, result.status
      assert_equal ["apply_host_context must be true or false"],
                   result.field_errors.fetch(:apply_host_context)
      refute record_for(org, title: "Draft proposal")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_create_rejects_non_boolean_deferred_without_writing
    with_create_store do |store, org, _archive|
      before = File.binread(org)

      result = store.create_task!(command(deferred: "yes"))

      assert_equal :invalid, result.status
      assert_includes result.field_errors.fetch(:deferred), "deferred must be true or false"
      assert_equal before, File.binread(org)
    end
  end

  def test_create_rejects_invalid_recurrence_or_ambiguous_initial_body_without_writing
    with_create_store do |store, org, _archive|
      before = File.binread(org)
      invalid = command(recurrence: "weekly", body: "body", notes: ["note"])

      result = store.create_task!(invalid)

      assert_equal :invalid, result.status
      assert_includes result.errors, "invalid recurrence cookie"
      assert_includes result.errors, "body and notes cannot both be supplied"
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_post_write_check_failure_rolls_back_the_entire_create_without_history
    with_create_store do |store, org, _archive|
      before = File.binread(org)
      original_writer = store.method(:write_records)
      writer = lambda do |path, records|
        original_writer.call(path, records)
        File.write(path, "{not json}\n", encoding: "UTF-8")
      end

      result = store.stub(:write_records, writer) { store.create_task!(command(notes: ["never persists"])) }

      assert_equal :store_invalid, result.status
      assert result.rolled_back?
      assert_equal before, File.binread(org)
      assert Tasks::Check.check(org).ok?
      assert_equal [:empty], store.undo!
    end
  end

  def test_application_accepts_attributes_or_a_typed_command_and_validates_context
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, FIXTURE)
      app = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive)
      )
      context = Tasks::OperationContext.new(operation_id: "capture-1", source: :cli)

      first = app.create_task({ title: "Via attributes", project: "Home" }, context: context)
      second = app.create_task(command(title: "Via command", parent_id: FIX[:flight]))

      assert_equal :ok, first.status
      assert_equal :ok, second.status
      assert_match(/\As1\.[0-9a-f]{64}\z/, first.store_revision)
      assert_match(/\As1\.[0-9a-f]{64}\z/, second.store_revision)
      assert_equal app.read_status_result.store_revision, second.store_revision
      assert_equal FIX[:home], record_for(org, title: "Via attributes")["parent"]
      assert_equal FIX[:flight], record_for(org, title: "Via command")["parent"]
      assert_raises(ArgumentError) { app.create_task(command, context: :cli) }
    end
  end
end
