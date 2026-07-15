# frozen_string_literal: true

require_relative "test_helper"
require "tasks/application"

class TestTaskChangeset < Minitest::Test
  TREE = [
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

  def with_changeset_store
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(TREE))
      yield Tasks::Store.new(org: org, archive: archive), org, archive
    end
  end

  def records(path)
    Tasks::Format.parse(File.read(path, encoding: "UTF-8")).records
  end

  def changeset(snapshot, changes = nil, **options)
    Tasks::TaskChangeset.from(snapshot, changes: changes || options)
  end

  def test_changeset_is_immutable_and_normalizes_recurrence_aliases
    changes = { "title" => +"Renamed", recur: +".+1w" }
    request = Tasks::TaskChangeset.new(
      id: +"11110002", changes: changes, expected_revision: +"v1.invalid"
    )
    changes["title"].replace("mutated")
    changes[:recur].replace("mutated")

    assert request.frozen?
    assert request.id.frozen?
    assert request.changes.frozen?
    assert request.changes[:title].frozen?
    assert_equal "Renamed", request.changes[:title]
    assert_equal ".+1w", request.changes[:recurrence]
    assert_equal %i[title recurrence], request.ordered_fields
  end

  def test_store_revision_is_semantic_immutable_and_not_a_line_or_mtime_token
    with_changeset_store do |store, org, _archive|
      before = store.edit_snapshot("11110002")
      assert before.revision.frozen?
      assert_match(/\Av1\.[0-9a-f]{64}\.[0-9a-f]{64}\.[0-9a-f]{64}\z/, before.revision)

      # Put a distinct section before this task. Its physical line changes, but
      # this task's own values, sibling sequence, and subtree lifecycle do not.
      source = records(org)
      split = source.index { |record| record["id"] == "22220001" }
      reordered = [source.first] + source[split..] + source[1...split]
      File.write(org, Tasks::Format.dump(reordered))

      after = store.edit_snapshot("11110002")
      refute_equal before.metadata[:line], after.metadata[:line]
      assert_equal before.revision, after.revision
    end
  end

  def test_nil_location_resolves_current_enclosing_section_under_mutation_lock
    with_changeset_store do |store, org, _archive|
      child_snapshot = store.edit_snapshot("11110003")
      ancestor_snapshot = store.edit_snapshot("11110002")

      moved = store.apply_changeset!(
        changeset(ancestor_snapshot, location: "22220001")
      )
      assert moved.ok?
      assert_equal child_snapshot.revision, store.edit_snapshot("11110003").revision,
                   "ancestor-only moves deliberately leave the child revision unchanged"

      unnested = store.apply_changeset!(
        changeset(child_snapshot, location: Tasks::TaskChangeset::UNNEST)
      )
      assert unnested.ok?
      child = records(org).find { |record| record["id"] == "11110003" }
      assert_equal "22220001", child["parent"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_multi_field_changeset_applies_in_documented_order_as_one_checked_undoable_write
    with_changeset_store do |store, org, _archive|
      snapshot = store.edit_snapshot("11110002")
      original_bytes = File.binread(org)
      writes = 0
      original_writer = store.method(:write_records)
      writer = lambda do |path, proposed|
        writes += 1
        original_writer.call(path, proposed)
      end
      request = changeset(
        snapshot,
        state: "WAITING", recurrence: ".+1w", scheduled: Date.new(2026, 8, 1),
        deferred: false, contexts: ["@work"], body: "replacement", title: "Renamed"
      )

      result = store.stub(:write_records, writer) { store.apply_changeset!(request) }

      assert_equal :ok, result.status
      assert_equal 1, writes
      assert_equal %i[title body contexts deferred scheduled recurrence state], result.summary[:fields]
      assert_equal ["11110002"], result.touched_ids
      task = records(org).find { |record| record["id"] == "11110002" }
      assert_equal "Renamed", task["title"]
      assert_equal "replacement", task["body"]
      assert_equal ["@work", "alpha"], task["tags"]
      assert_equal "2026-08-01", task["scheduled"]
      assert_equal ".+1w", task["recur"]
      assert_equal "WAITING", task["state"]
      assert Tasks::Check.check(org).ok?

      assert_equal :ok, store.undo!.first
      assert_equal original_bytes, File.binread(org)
      assert_equal :ok, store.redo!.first
      assert_equal "Renamed", records(org).find { |record| record["id"] == "11110002" }["title"]
    end
  end

  def test_invalid_later_field_has_zero_writes_and_no_history
    with_changeset_store do |store, org, _archive|
      snapshot = store.edit_snapshot("11110002")
      before = File.binread(org)
      request = changeset(snapshot, title: "Would be partial", recurrence: ".+1w")

      result = store.apply_changeset!(request)

      assert_equal :invalid, result.status
      assert_match(/recurrence requires/, result.errors.first)
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_changeset_command_shape_is_validated_before_lookup_without_writes_or_history
    with_changeset_store do |store, org, _archive|
      before = File.binread(org)
      writes = 0
      writer = lambda do |*|
        writes += 1
        raise "invalid TaskChangeset must not write"
      end
      valid_revision = "v1.#{"0" * 64}.#{"0" * 64}.#{"0" * 64}"
      invalid_requests = [
        [Tasks::TaskChangeset.new(id: nil, changes: { title: "Renamed" }, expected_revision: valid_revision),
         { id: ["task id is required"] }],
        [Tasks::TaskChangeset.new(id: "", changes: { title: "Renamed" }, expected_revision: valid_revision),
         { id: ["task id is required"] }],
        [Tasks::TaskChangeset.new(id: "deadbeef", changes: {}, expected_revision: valid_revision),
         { changes: ["changes must be a non-empty mapping"] }],
        [Tasks::TaskChangeset.new(id: "deadbeef", changes: [], expected_revision: valid_revision),
         { changes: ["changes must be a non-empty mapping"] }],
      ]

      invalid_requests.each do |request, field_errors|
        result = store.stub(:write_records, writer) { store.apply_changeset!(request) }

        assert_equal :invalid, result.status
        assert_equal field_errors, result.field_errors
        assert_equal field_errors.values.flatten, result.errors
        assert_nil result.snapshot
        assert_equal before, File.binread(org)
        assert_equal [:empty], store.undo!
      end

      missing = store.apply_changeset!(Tasks::TaskChangeset.new(
        id: "deadbeef", changes: { title: "Renamed" }, expected_revision: valid_revision
      ))
      assert_equal :not_found, missing.status
      assert_empty missing.field_errors
      assert_equal 0, writes
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_changeset_rejects_ambiguous_composite_fields_before_any_write
    with_changeset_store do |store, org, _archive|
      snapshot = store.edit_snapshot("11110002")
      before = File.binread(org)
      request = changeset(snapshot, tag_delta: { add: ["beta"], remove: [] }, tags: ["gamma"])

      result = store.apply_changeset!(request)

      assert_equal :invalid, result.status
      assert_match(/tag_delta cannot/, result.errors.first)
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_changeset_retains_typed_conflict_results_for_failed_confirmations
    with_changeset_store do |store, org, _archive|
      snapshot = store.edit_snapshot("11110002")
      before = File.binread(org)
      request = Tasks::TaskChangeset.from(
        snapshot,
        changes: { title: "Renamed" },
        confirmation: { expected: { values: { title: "Wrong baseline" } } }
      )

      result = store.apply_changeset!(request)

      assert_equal :conflict, result.status
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_no_change_does_not_write_or_consume_undo
    with_changeset_store do |store, org, _archive|
      snapshot = store.edit_snapshot("11110002")
      before = File.binread(org)

      result = store.apply_changeset!(changeset(snapshot, title: "Parent", contexts: ["@home"]))

      assert_equal :no_change, result.status
      assert_equal snapshot.revision, result.snapshot.revision
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_changeset_returns_stale_for_a_changed_own_semantic_revision
    with_changeset_store do |store, org, _archive|
      snapshot = store.edit_snapshot("11110002")
      source = records(org)
      source.find { |record| record["id"] == "11110002" }["body"] = "external body"
      File.write(org, Tasks::Format.dump(source))
      before = File.binread(org)

      result = store.apply_changeset!(changeset(snapshot, title: "Renamed"))

      assert_equal :stale, result.status
      assert_equal "external body", result.snapshot.body
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_title_only_changeset_ignores_a_sibling_change_but_state_guards_lifecycle
    with_changeset_store do |store, org, _archive|
      title_snapshot = store.edit_snapshot("11110002")
      source = records(org)
      insertion = source.index { |record| record["id"] == "22220001" }
      source.insert(insertion, {
        "type" => "task", "id" => "11110005", "parent" => "11110001", "state" => "TODO", "title" => "New sibling"
      })
      File.write(org, Tasks::Format.dump(source))

      changed = store.apply_changeset!(changeset(title_snapshot, title: "Renamed"))
      assert_equal :ok, changed.status

      state_snapshot = store.edit_snapshot("11110002")
      source = records(org)
      source.find { |record| record["id"] == "11110003" }["state"] = "WAITING"
      File.write(org, Tasks::Format.dump(source))
      before = File.binread(org)

      stale = store.apply_changeset!(changeset(state_snapshot, state: "DONE"))
      assert_equal :stale, stale.status
      assert_equal before, File.binread(org)
    end
  end

  def test_one_field_task_patch_uses_the_shared_changeset_history_path
    with_changeset_store do |store, _org, _archive|
      snapshot = store.edit_snapshot("11110002")
      patch = Tasks::TaskPatch.from(snapshot, field: :title, value: "Renamed")

      result = store.patch_task!(patch)

      assert_equal :ok, result.status
      assert_equal [:ok, "edit title: Parent"], store.undo!
    end
  end

  def test_application_updates_by_values_or_a_typed_changeset_without_exposing_store
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(TREE))
      factory = Tasks::StoreFactory.new(org: org, archive: archive)
      app = Tasks::Application.new(store_factory: factory)
      revision = app.get_task("11110002").revision

      first = app.update_task("11110002", { title: "Via values" }, expected_revision: revision)
      assert_equal :ok, first.status
      assert_match(/\As1\.[0-9a-f]{64}\z/, first.store_revision)
      typed = Tasks::TaskChangeset.from(first.snapshot, changes: { body: "Via command" })
      second = app.update_task(typed)
      assert_equal :ok, second.status
      assert_equal app.read_status_result.store_revision, second.store_revision
      assert_equal "Via command", app.get_task("11110002").body.first
      assert_equal second.snapshot.revision, app.get_task("11110002").revision

      no_change = app.update_task(
        "11110002", { body: "Via command" }, expected_revision: second.snapshot.revision
      )
      assert_equal :no_change, no_change.status
      assert_equal app.read_status_result.store_revision, no_change.store_revision
    end
  end
end
