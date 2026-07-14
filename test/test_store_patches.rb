# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "rbconfig"

class TestStorePatches < Minitest::Test
  BIN = File.expand_path("../bin/tasks", __dir__)
  PATCH_TREE = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "11110001", "title" => "One" },
    { "type" => "task", "id" => "11110002", "parent" => "11110001", "state" => "NEXT",
      "title" => "Parent", "tags" => %w[@home alpha defer], "body" => "raw\nbody" },
    { "type" => "task", "id" => "11110003", "parent" => "11110002", "state" => "NEXT",
      "title" => "Child", "scheduled" => "2026-07-13", "recur" => ".+1w" },
    { "type" => "task", "id" => "11110004", "parent" => "11110001", "state" => "WAITING",
      "title" => "Sibling" },
    { "type" => "section", "id" => "22220001", "title" => "Two" },
    { "type" => "task", "id" => "22220002", "parent" => "22220001", "state" => "TODO",
      "title" => "Destination" },
  ].freeze

  def with_patch_store(max_depth: Tasks::Tree::DEFAULT_MAX_DEPTH)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(PATCH_TREE))
      yield Tasks::Store.new(org: org, archive: archive, max_depth: max_depth), org, archive
    end
  end

  def patch(snapshot, field, value, expected: snapshot.expected_for(field), coalesce_key: nil)
    Tasks::TaskPatch.new(id: snapshot.id, field: field, value: value, expected: expected,
                         coalesce_key: coalesce_key)
  end

  def parsed(path)
    Tasks::Format.parse(File.read(path, encoding: "UTF-8")).records
  end

  def run_external_cli(org, *args)
    env = {
      "TASKS_FILE" => org,
      "TASKS_ARCHIVE" => File.join(File.dirname(org), "archive.jsonl"),
      "XDG_STATE_HOME" => ENV.fetch("XDG_STATE_HOME"),
    }
    stdout, stderr, status = Open3.capture3(env, RbConfig.ruby, BIN, *args)
    assert status.success?, "#{BIN} #{args.join(" ")} failed\nstdout: #{stdout}\nstderr: #{stderr}"
    [stdout, stderr]
  end

  def install_interleaved_tags(path)
    records = parsed(path)
    records.find { |record| record["id"] == "11110002" }["tags"] =
      %w[@a x @b defer y]
    File.write(path, Tasks::Format.dump(records))
  end

  def test_edit_snapshot_is_exact_and_deeply_immutable
    with_patch_store do |store, _org|
      snapshot = store.edit_snapshot("11110002")
      assert_instance_of Tasks::EditSnapshot, snapshot
      assert_equal "raw\nbody", snapshot.body
      assert_equal "11110001", snapshot.parent
      assert_equal ["@home"], snapshot.contexts
      assert_equal ["alpha"], snapshot.tags
      assert snapshot.deferred
      assert snapshot.frozen?
      assert snapshot.contexts.frozen?
      assert snapshot.baselines.frozen?
      assert_raises(FrozenError) { snapshot.contexts << "@work" }
      assert_equal snapshot.fingerprints[:location], snapshot.expected_for(:location)
      assert_equal snapshot.fingerprints[:state], snapshot.expected_for(:state)
    end
  end

  def test_task_patch_and_result_are_immutable_typed_values
    with_patch_store do |store, _org|
      snapshot = store.edit_snapshot("11110002")
      confirmation_key = +"expected"
      confirmation_value = +"baseline"
      request = Tasks::TaskPatch.from(
        snapshot,
        field: :contexts,
        value: ["@work"],
        confirmation: { confirmation_key => confirmation_value },
      )
      confirmation_key.replace("mutated")
      confirmation_value.replace("mutated")
      assert request.frozen?
      assert request.value.frozen?
      assert_equal({ "expected" => "baseline" }, request.confirmation)
      assert request.confirmation.keys.first.frozen?
      assert request.confirmation.values.first.frozen?
      result = store.patch_task!(request)
      assert_equal :ok, result.status
      assert result.ok?
      assert result.changed?
      assert result.frozen?
      assert result.touched_ids.frozen?
      assert_equal ["11110002"], result.touched_ids
    end
  end

  def test_confirmation_expectations_atomically_guard_coupled_date_recurrence
    with_patch_store do |store, org|
      original = store.edit_snapshot("11110003")
      external = store.patch_task!(patch(original, :recurrence, ".+1m"))
      assert_equal :ok, external.status
      before = File.read(org)

      request = Tasks::TaskPatch.new(
        id: original.id,
        field: :scheduled,
        value: nil,
        expected: original.expected_for(:scheduled),
        confirmation: {
          token: "confirmation-token",
          expected: {
            owned: {
              scheduled: original.expected_for(:scheduled),
              recurrence: original.expected_for(:recurrence),
            },
            predicates: { date_presence: { deadline: false } },
          },
        },
      )
      result = store.patch_task!(request)

      assert_equal :conflict, result.status
      assert_equal before, File.read(org)
      task = parsed(org).find { |record| record["id"] == "11110003" }
      assert_equal "2026-07-13", task["scheduled"]
      assert_equal ".+1m", task["recur"]
    end
  end

  def test_recurrence_confirmation_uses_live_date_availability_not_exact_other_date
    with_patch_store do |store, org|
      original = store.edit_snapshot("11110003")
      added = store.patch_task!(patch(original, :deadline, Date.new(2026, 8, 1)))
      assert_equal :ok, added.status

      request = Tasks::TaskPatch.new(
        id: original.id,
        field: :recurrence,
        value: ".+1m",
        expected: original.expected_for(:recurrence),
        confirmation: {
          expected: {
            owned: { recurrence: original.expected_for(:recurrence) },
            predicates: { any_live_date: true },
          },
        },
      )
      result = store.patch_task!(request)

      assert_equal :ok, result.status
      task = parsed(org).find { |record| record["id"] == "11110003" }
      assert_equal "2026-08-01", task["deadline"]
      assert_equal ".+1m", task["recur"]
    end
  end

  def test_title_patch_conflicts_only_with_the_title_slice
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      records = parsed(org)
      records.find { |record| record["id"] == "11110002" }["priority"] = "A"
      records.find { |record| record["id"] == "11110004" }["title"] = "Other task changed"
      File.write(org, Tasks::Format.dump(records))

      result = store.patch_task!(patch(snapshot, :title, "Renamed"))
      assert_equal :ok, result.status
      rec = parsed(org).find { |record| record["id"] == "11110002" }
      assert_equal "Renamed", rec["title"]
      assert_equal "A", rec["priority"], "unrelated same-task field survives"

      stale = result.snapshot
      records = parsed(org)
      records.find { |record| record["id"] == "11110002" }["title"] = "External title"
      File.write(org, Tasks::Format.dump(records))
      before = File.read(org)
      conflict = store.patch_task!(patch(stale, :title, "Local title"))
      assert_equal :conflict, conflict.status
      assert_equal "External title", conflict.snapshot.title
      assert_equal before, File.read(org)
    end
  end

  def test_missing_and_invalid_field_are_typed_and_write_nothing
    with_patch_store do |store, org|
      before = File.read(org)
      missing = Tasks::TaskPatch.new(id: "deadbeef", field: :title, value: "X", expected: "Y")
      result = store.patch_task!(missing)
      assert_equal :not_found, result.status
      assert result.missing?
      unknown = Tasks::TaskPatch.new(id: "11110002", field: :bogus, value: "X", expected: nil)
      assert_equal :invalid, store.patch_task!(unknown).status
      assert_equal before, File.read(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_no_change_writes_no_bytes_and_records_no_history
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      before = File.read(org)
      result = store.patch_task!(patch(snapshot, :title, "  Parent  "))
      assert_equal :no_change, result.status
      assert_equal before, File.read(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_no_change_preserves_valid_noncanonical_source_bytes
    with_patch_store do |store, org|
      raw = File.read(org)
      raw = raw.sub('{"type":"task","id":"11110002"',
                    '{ "id": "11110002", "type": "task"')
      File.write(org, raw)
      snapshot = store.edit_snapshot("11110002")
      before = File.read(org)
      result = store.patch_task!(patch(snapshot, :title, "Parent"))
      assert_equal :no_change, result.status
      assert_equal before, File.read(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_priority_deferred_context_and_tag_slices_merge_without_erasing_each_other
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      result = store.patch_task!(patch(snapshot, :contexts, %w[@work @phone]))
      assert_equal :ok, result.status
      rec = parsed(org).find { |record| record["id"] == "11110002" }
      assert_equal %w[@work @phone alpha defer], rec["tags"]

      snapshot = result.snapshot
      result = store.patch_task!(patch(snapshot, :tags, %w[beta gamma]))
      assert_equal :ok, result.status
      rec = parsed(org).find { |record| record["id"] == "11110002" }
      assert_equal %w[@work @phone beta gamma defer], rec["tags"]

      snapshot = result.snapshot
      result = store.patch_task!(patch(snapshot, :deferred, false))
      assert_equal :ok, result.status
      assert_equal %w[@work @phone beta gamma], parsed(org).find { |r| r["id"] == "11110002" }["tags"]

      snapshot = result.snapshot
      result = store.patch_task!(patch(snapshot, :priority, "B"))
      assert_equal :ok, result.status
      assert_equal "B", parsed(org).find { |r| r["id"] == "11110002" }["priority"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_interleaved_tag_slice_noops_preserve_exact_bytes_and_history
    with_patch_store do |store, org|
      install_interleaved_tags(org)
      before = File.binread(org)

      snapshot = store.edit_snapshot("11110002")
      result = store.patch_task!(patch(snapshot, :contexts, %w[@a @b]))
      assert_equal :no_change, result.status
      assert_equal before, File.binread(org)

      snapshot = result.snapshot
      result = store.patch_task!(patch(snapshot, :tags, %w[x y]))
      assert_equal :no_change, result.status
      assert_equal before, File.binread(org)

      snapshot = result.snapshot
      result = store.patch_task!(patch(snapshot, :deferred, true))
      assert_equal :no_change, result.status
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_changed_contexts_preserve_interleaved_unowned_tag_placement
    with_patch_store do |store, org|
      install_interleaved_tags(org)
      snapshot = store.edit_snapshot("11110002")
      result = store.patch_task!(patch(snapshot, :contexts, %w[@c @d @e]))
      assert_equal :ok, result.status
      assert_equal %w[@c x @d @e defer y],
                   parsed(org).find { |record| record["id"] == "11110002" }["tags"]
    end
  end

  def test_changed_plain_tags_preserve_interleaved_unowned_tag_placement
    with_patch_store do |store, org|
      install_interleaved_tags(org)
      snapshot = store.edit_snapshot("11110002")
      result = store.patch_task!(patch(snapshot, :tags, ["m"]))
      assert_equal :ok, result.status
      assert_equal %w[@a m @b defer],
                   parsed(org).find { |record| record["id"] == "11110002" }["tags"]
    end
  end

  def test_changed_defer_preserves_interleaved_unowned_tag_placement
    with_patch_store do |store, org|
      install_interleaved_tags(org)
      snapshot = store.edit_snapshot("11110002")
      result = store.patch_task!(patch(snapshot, :deferred, false))
      assert_equal :ok, result.status
      assert_equal %w[@a x @b y],
                   parsed(org).find { |record| record["id"] == "11110002" }["tags"]
    end
  end

  def test_invalid_tag_slice_is_typed_and_atomic
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      before = File.read(org)
      result = store.patch_task!(patch(snapshot, :tags, ["@not-owned"]))
      assert_equal :invalid, result.status
      assert_match(/invalid tags/, result.errors.first)
      assert_equal before, File.read(org)
    end
  end

  def test_body_replacement_preserves_exact_whitespace_and_newlines
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      body = "  first\n\nlast  \n"
      result = store.patch_task!(patch(snapshot, :body, body))
      assert_equal :ok, result.status
      assert_equal body, result.snapshot.body
      assert_equal body, parsed(org).find { |record| record["id"] == "11110002" }["body"]
    end
  end

  def test_date_patch_promotes_inbox_and_clearing_final_date_retires_recurrence
    with_store do |store, org, _archive|
      snapshot = store.edit_snapshot(FIX[:garden])
      result = store.patch_task!(patch(snapshot, :scheduled, Date.new(2026, 8, 1)))
      assert_equal :ok, result.status
      rec = record_for(org, title: "random thought about the garden")
      assert_equal "TODO", rec["state"]
      assert_equal "2026-08-01", rec["scheduled"]

      store.set_recur!(find_item(store, "garden"), ".+1w")
      snapshot = store.edit_snapshot(FIX[:garden])
      result = store.patch_task!(patch(snapshot, :scheduled, nil))
      assert_equal :ok, result.status
      rec = record_for(org, title: "random thought about the garden")
      refute rec.key?("scheduled")
      refute rec.key?("recur")
      assert_equal "TODO", rec["state"]
    end
  end

  def test_recurrence_patch_validates_cookie_and_fresh_dates
    with_patch_store do |store, org|
      parent = store.edit_snapshot("11110002")
      invalid = store.patch_task!(patch(parent, :recurrence, ".+1w"))
      assert_equal :invalid, invalid.status

      child = store.edit_snapshot("11110003")
      invalid = store.patch_task!(patch(child, :recurrence, "++0d"))
      assert_equal :invalid, invalid.status
      valid = store.patch_task!(patch(child, :recurrence, "+2w"))
      assert_equal :ok, valid.status
      assert_equal "+2w", parsed(org).find { |r| r["id"] == "11110003" }["recur"]
    end
  end

  def test_location_moves_the_whole_subtree_and_reports_summary
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      result = store.patch_task!(patch(snapshot, :location, "22220002"))
      assert_equal :ok, result.status
      assert_equal %w[11110002 11110003], result.touched_ids
      assert_equal "11110001", result.summary[:from]
      assert_equal "22220002", result.summary[:to]
      records = parsed(org)
      parent_i = records.index { |record| record["id"] == "22220002" }
      moved_i = records.index { |record| record["id"] == "11110002" }
      child_i = records.index { |record| record["id"] == "11110003" }
      assert_operator moved_i, :>, parent_i
      assert_equal moved_i + 1, child_i
      assert_equal "22220002", records[moved_i]["parent"]
      assert_equal "11110002", records[child_i]["parent"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_location_fingerprint_conflicts_on_structural_change_but_not_field_change
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      records = parsed(org)
      records.find { |record| record["id"] == "11110003" }["title"] = "Child field changed"
      File.write(org, Tasks::Format.dump(records))
      result = store.patch_task!(patch(snapshot, :location, "22220001"))
      assert_equal :ok, result.status, "a descendant title is outside location ownership"

      snapshot = result.snapshot
      records = parsed(org)
      sibling = records.delete_at(records.index { |record| record["id"] == "11110004" })
      destination_index = records.index { |record| record["id"] == "22220002" }
      sibling["parent"] = "22220001"
      records.insert(destination_index, sibling)
      File.write(org, Tasks::Format.dump(records))
      conflict = store.patch_task!(patch(snapshot, :location, "11110001"))
      assert_equal :conflict, conflict.status
    end
  end

  def test_location_cycle_and_depth_failures_are_typed_and_atomic
    with_patch_store(max_depth: 2) do |store, org|
      before = File.read(org)
      parent = store.edit_snapshot("11110002")
      assert_equal :cycle, store.patch_task!(patch(parent, :location, "11110003")).status
      assert_equal before, File.read(org)

      destination = store.edit_snapshot("22220002")
      result = store.patch_task!(patch(destination, :location, "11110003"))
      assert_equal :too_deep, result.status
      assert_equal before, File.read(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_state_patch_cascades_and_uses_lifecycle_fingerprint
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      result = store.patch_task!(patch(snapshot, :state, "DONE"))
      assert_equal :ok, result.status
      assert_equal %w[11110002 11110003], result.touched_ids
      records = parsed(org)
      parent = records.find { |record| record["id"] == "11110002" }
      child = records.find { |record| record["id"] == "11110003" }
      assert_equal "DONE", parent["state"]
      assert_equal "DONE", child["state"]
      refute_includes parent.fetch("tags", []), "defer"
      refute child.key?("recur")
      assert_equal Date.today.iso8601, parent["closed"]
      assert_equal Date.today.iso8601, child["closed"]
    end
  end

  def test_state_patch_advances_recurrence_without_cascade
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110003")
      result = store.patch_task!(patch(snapshot, :state, "DONE"))
      assert_equal :ok, result.status
      assert result.summary[:recurrence_advanced]
      rec = parsed(org).find { |record| record["id"] == "11110003" }
      assert_equal "NEXT", rec["state"]
      assert_equal (Date.today + 7).iso8601, rec["scheduled"]
      assert_match(/- Did \[#{Date.today}\]/, rec["body"])
      refute rec.key?("closed")
    end
  end

  def test_state_conflicts_when_affected_descendant_lifecycle_changes
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      records = parsed(org)
      child = records.find { |record| record["id"] == "11110003" }
      child["state"] = "WAITING"
      File.write(org, Tasks::Format.dump(records))
      before = File.read(org)
      result = store.patch_task!(patch(snapshot, :state, "DONE"))
      assert_equal :conflict, result.status
      assert_equal before, File.read(org)
    end
  end

  def test_state_adopts_an_unrelated_body_change
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      records = parsed(org)
      records.find { |record| record["id"] == "11110002" }["body"] = "external note"
      File.write(org, Tasks::Format.dump(records))
      result = store.patch_task!(patch(snapshot, :state, "CANCELLED"))
      assert_equal :ok, result.status
      rec = parsed(org).find { |record| record["id"] == "11110002" }
      assert_equal "external note", rec["body"]
      assert_equal "CANCELLED", rec["state"]
    end
  end

  def test_malformed_file_is_rejected_before_write
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      File.write(org, File.read(org).sub("\n", "\nnot-json\n"))
      before = File.binread(org)
      result = store.patch_task!(patch(snapshot, :title, "Renamed"))
      assert_equal :store_invalid, result.status
      assert result.errors.any? { |error| error.include?("invalid JSON") }
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_invalid_utf8_live_bytes_are_contained_and_preserved
    with_patch_store do |store, org|
      bytes = File.binread(org).sub("Parent", "Par\xFFent".b)
      File.binwrite(org, bytes)
      request = Tasks::TaskPatch.new(id: "11110002", field: :title,
                                     value: "Renamed", expected: "Parent")

      assert_nil store.edit_snapshot("11110002")
      result = store.patch_task!(request)
      assert_equal :store_invalid, result.status
      assert result.errors.all?(&:valid_encoding?)
      assert result.errors.any? { |error| error.include?("UTF-8") }
      assert_equal bytes, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_invalid_utf8_proposed_value_is_a_typed_atomic_failure
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      invalid = "\xFF".b.force_encoding(Encoding::UTF_8)
      before = File.binread(org)

      result = store.patch_task!(patch(snapshot, :body, invalid))
      assert_equal :invalid, result.status
      assert result.errors.all?(&:valid_encoding?)
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_patch_boundary_does_not_swallow_fatal_exceptions
    with_patch_store do |store, _org|
      snapshot = store.edit_snapshot("11110002")
      fatal_reader = ->(_path) { raise NoMemoryError, "injected fatal" }
      assert_raises(NoMemoryError) do
        store.stub(:fresh_records, fatal_reader) do
          store.patch_task!(patch(snapshot, :title, "Renamed"))
        end
      end
    end
  end

  def test_post_write_check_failure_rolls_back_and_records_no_history
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      before = File.read(org)
      result = store.stub(:post_write_failure, "injected check failure") do
        store.patch_task!(patch(snapshot, :title, "Renamed"))
      end
      assert_equal :store_invalid, result.status
      assert_equal ["injected check failure"], result.errors
      assert_equal before, File.read(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_writer_failure_rolls_back_and_records_no_history
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      before = File.read(org)
      writer = lambda do |path, records|
        File.write(path, Tasks::Format.dump(records))
        raise "injected writer failure"
      end
      result = store.stub(:write_records, writer) do
        store.patch_task!(patch(snapshot, :title, "Renamed"))
      end
      assert_equal :unavailable, result.status
      assert_equal ["injected writer failure"], result.errors
      assert_equal before, File.read(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_successful_patch_is_one_undoable_checked_write
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      before = File.read(org)
      writes = 0
      original = store.method(:write_records)
      writer = lambda do |path, records|
        writes += 1
        original.call(path, records)
      end
      result = store.stub(:write_records, writer) do
        store.patch_task!(patch(snapshot, :body, "replacement"))
      end
      assert_equal :ok, result.status
      assert_equal 1, writes
      assert Tasks::Check.check(org).ok?
      assert_match(/edit body/, store.undo![1])
      assert_equal before, File.read(org)
    end
  end

  def test_byte_contiguous_patches_with_one_session_key_are_one_undo_step
    with_patch_store do |store, org|
      key = "edit-session-1"
      initial = File.read(org)
      first = store.patch_task!(patch(store.edit_snapshot("11110002"), :title, "Renamed",
                                      coalesce_key: key))
      assert_equal :ok, first.status
      after_first = File.read(org)
      refute_equal initial, after_first, "the first blur is durable before the next patch"

      second = store.patch_task!(patch(first.snapshot, :body, "replacement",
                                       coalesce_key: key))
      assert_equal :ok, second.status
      final = File.read(org)
      refute_equal after_first, final, "the second blur is independently durable"
      assert Tasks::Check.check(org).ok?

      assert_equal [:ok, "edit body: Renamed"], store.undo!
      assert_equal initial, File.read(org), "one undo restores the session's earliest bytes"
      assert_equal [:empty], store.undo!
      assert_equal [:ok, "edit body: Renamed"], store.redo!
      assert_equal final, File.read(org), "one redo restores the session's latest bytes"
      assert_equal [:empty], store.redo!
    end
  end

  def test_nil_and_mismatched_keys_keep_separate_patch_entries
    [[nil, nil], ["session-a", "session-b"]].each do |first_key, second_key|
      with_patch_store do |store, org|
        initial = File.read(org)
        first = store.patch_task!(patch(store.edit_snapshot("11110002"), :title, "Renamed",
                                        coalesce_key: first_key))
        assert_equal :ok, first.status
        after_first = File.read(org)
        second = store.patch_task!(patch(first.snapshot, :body, "replacement",
                                         coalesce_key: second_key))
        assert_equal :ok, second.status

        assert_equal :ok, store.undo!.first
        assert_equal after_first, File.read(org)
        assert_equal :ok, store.undo!.first
        assert_equal initial, File.read(org)
      end
    end
  end

  def test_intervening_cli_mutation_breaks_patch_coalescing
    with_patch_store do |store, org|
      key = "session"
      initial = File.read(org)
      first = store.patch_task!(patch(store.edit_snapshot("11110002"), :title, "Renamed",
                                      coalesce_key: key))
      assert_equal :ok, first.status
      after_first = File.read(org)
      item = store.items.find { |candidate| candidate.id == "11110002" }
      assert store.set_priority!(item, "A")
      after_cli = File.read(org)
      second = store.patch_task!(patch(store.edit_snapshot("11110002"), :body, "replacement",
                                       coalesce_key: key))
      assert_equal :ok, second.status

      assert_equal :ok, store.undo!.first
      assert_equal after_cli, File.read(org)
      assert_equal :ok, store.undo!.first
      assert_equal after_first, File.read(org)
      assert_equal :ok, store.undo!.first
      assert_equal initial, File.read(org)
    end
  end

  def test_external_absolute_cli_write_preserves_one_step_segment_and_breaks_the_next_segment
    with_patch_store do |store, org|
      key = "editor-session"
      initial = File.binread(org)
      first = store.patch_task!(patch(
        store.edit_snapshot("11110002"), :title, "Renamed", coalesce_key: key,
      ))
      second = store.patch_task!(patch(
        first.snapshot, :body, "coalesced body", coalesce_key: key,
      ))
      assert_equal :ok, second.status
      after_segment = File.binread(org)

      run_external_cli(org, "priority", "11110002", "A")
      after_external = File.binread(org)
      third = store.patch_task!(patch(
        store.edit_snapshot("11110002"), :body, "after CLI", coalesce_key: key,
      ))
      assert_equal :ok, third.status

      assert_equal :ok, store.undo!.first
      assert_equal after_external, File.binread(org), "latest editor patch is its own segment"
      assert_equal :ok, store.undo!.first
      assert_equal after_segment, File.binread(org), "external CLI mutation keeps its own boundary"
      assert_equal :ok, store.undo!.first
      assert_equal initial, File.binread(org), "the two earlier field patches remain one undo step"
      assert_equal [:empty], store.undo!
    end
  end

  def test_undo_redo_breaks_patch_coalescing_even_back_at_exact_tip
    with_patch_store do |store, org|
      key = "session"
      initial = File.read(org)
      first = store.patch_task!(patch(store.edit_snapshot("11110002"), :title, "Renamed",
                                      coalesce_key: key))
      assert_equal :ok, first.status
      after_first = File.read(org)
      assert_equal :ok, store.undo!.first
      assert_equal :ok, store.redo!.first
      assert_equal after_first, File.read(org)
      second = store.patch_task!(patch(first.snapshot, :body, "replacement", coalesce_key: key))
      assert_equal :ok, second.status

      assert_equal :ok, store.undo!.first
      assert_equal after_first, File.read(org)
      assert_equal :ok, store.undo!.first
      assert_equal initial, File.read(org)
    end
  end

  def test_history_branch_breaks_patch_coalescing
    with_patch_store do |store, org|
      key = "session"
      initial = File.read(org)
      first = store.patch_task!(patch(store.edit_snapshot("11110002"), :title, "Renamed",
                                      coalesce_key: key))
      assert_equal :ok, first.status
      after_first = File.read(org)
      item = store.items.find { |candidate| candidate.id == "11110002" }
      assert store.set_priority!(item, "A")
      assert_equal :ok, store.undo!.first
      second = store.patch_task!(patch(first.snapshot, :body, "replacement", coalesce_key: key))
      assert_equal :ok, second.status

      assert_equal :ok, store.undo!.first
      assert_equal after_first, File.read(org)
      assert_equal :ok, store.undo!.first
      assert_equal initial, File.read(org)
    end
  end

  def test_new_store_instance_cannot_extend_a_coalesced_segment
    with_patch_store do |store, org, archive|
      key = "reused-session-key"
      initial = File.read(org)
      first = store.patch_task!(patch(store.edit_snapshot("11110002"), :title, "Renamed",
                                      coalesce_key: key))
      assert_equal :ok, first.status
      after_first = File.read(org)

      reopened = Tasks::Store.new(org: org, archive: archive)
      second = reopened.patch_task!(patch(reopened.edit_snapshot("11110002"), :body,
                                          "replacement", coalesce_key: key))
      assert_equal :ok, second.status
      assert_equal :ok, reopened.undo!.first
      assert_equal after_first, File.read(org)
      assert_equal :ok, reopened.undo!.first
      assert_equal initial, File.read(org)
    end
  end

  def test_external_org_bytes_break_coalescing_and_become_the_safe_baseline
    with_patch_store do |store, org|
      key = "session"
      first = store.patch_task!(patch(store.edit_snapshot("11110002"), :title, "Renamed",
                                      coalesce_key: key))
      assert_equal :ok, first.status
      records = parsed(org)
      records.find { |record| record["id"] == "11110004" }["title"] = "External sibling"
      File.write(org, Tasks::Format.dump(records))
      external = File.read(org)

      second = store.patch_task!(patch(first.snapshot, :body, "replacement", coalesce_key: key))
      assert_equal :ok, second.status
      assert_equal :ok, store.undo!.first
      assert_equal external, File.read(org), "undo preserves the out-of-band bytes"
      assert_equal [:empty], store.undo!, "unsafe history before the external write is discarded"
    end
  end

  def test_external_archive_bytes_break_coalescing
    with_patch_store do |store, org, archive|
      key = "session"
      first = store.patch_task!(patch(store.edit_snapshot("11110002"), :title, "Renamed",
                                      coalesce_key: key))
      assert_equal :ok, first.status
      File.write(archive, Tasks::Format.dump([{ "type" => "meta", "version" => 1 }]))
      external_archive = File.read(archive)

      second = store.patch_task!(patch(first.snapshot, :body, "replacement", coalesce_key: key))
      assert_equal :ok, second.status
      assert_equal :ok, store.undo!.first
      assert_equal external_archive, File.read(archive)
      assert_equal "Renamed", parsed(org).find { |record| record["id"] == "11110002" }["title"]
      assert_equal [:empty], store.undo!
    end
  end

  def test_location_patch_matches_move_under_cli_semantics
    patch_bytes = nil
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      assert_equal :ok, store.patch_task!(patch(snapshot, :location, "22220002")).status
      patch_bytes = File.read(org)
    end

    with_patch_store do |store, org|
      parent = store.items.find { |item| item.id == "11110002" }
      destination = store.items.find { |item| item.id == "22220002" }
      assert_kind_of Integer, store.move_under!(parent, destination)
      assert_equal patch_bytes, File.read(org)
    end
  end

  def test_lifecycle_patch_matches_set_state_cli_semantics
    patch_bytes = nil
    with_patch_store do |store, org|
      snapshot = store.edit_snapshot("11110002")
      assert_equal :ok, store.patch_task!(patch(snapshot, :state, "DONE")).status
      patch_bytes = File.read(org)
    end

    with_patch_store do |store, org|
      assert store.set_state!(store.items.find { |item| item.id == "11110002" }, "DONE")
      assert_equal patch_bytes, File.read(org)
    end
  end
end
