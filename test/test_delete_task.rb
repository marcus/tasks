# frozen_string_literal: true

require_relative "test_helper"
require "tasks/application"

# Store + Application coverage for the guarded, undoable hard delete. A hard
# delete removes a task's contiguous subtree from the live file only; it never
# consults or writes the archive, and it is not an alias for CANCELLED.
class TestDeleteTask < Minitest::Test
  # A nested fixture: the flat FIXTURE has no task-with-children, and the whole
  # point of delete is the subtree guard, so build a tree with a parent, two
  # children (one with a grandchild), an aunt sibling, and a separate section.
  IDS = {
    inbox:      "de1e0001",
    garden:     "de1e0002",
    projects:   "de1e0003",
    parent:     "de1e0004",
    design:     "de1e0005",
    build:      "de1e0006",
    test_gc:    "de1e0007",
    aunt:       "de1e0008",
  }.freeze

  NESTED_RECORDS = [
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => IDS[:inbox], "title" => "Inbox" },
    { "type" => "task", "id" => IDS[:garden], "parent" => IDS[:inbox], "state" => "INBOX",
      "title" => "random garden thought" },
    { "type" => "section", "id" => IDS[:projects], "title" => "Projects" },
    { "type" => "task", "id" => IDS[:parent], "parent" => IDS[:projects], "state" => "NEXT",
      "title" => "Launch the site" },
    { "type" => "task", "id" => IDS[:design], "parent" => IDS[:parent], "state" => "TODO",
      "title" => "Design the layout" },
    { "type" => "task", "id" => IDS[:build], "parent" => IDS[:parent], "state" => "NEXT",
      "title" => "Build the pages" },
    { "type" => "task", "id" => IDS[:test_gc], "parent" => IDS[:build], "state" => "TODO",
      "title" => "Write the tests" },
    { "type" => "task", "id" => IDS[:aunt], "parent" => IDS[:projects], "state" => "NEXT",
      "title" => "Separate project" },
  ].freeze

  def with_delete_store(records: NESTED_RECORDS, archive_records: nil)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, dump_fixture(records))
      File.write(archive, dump_fixture(archive_records)) if archive_records
      yield Tasks::Store.new(org: org, archive: archive), org, archive
    end
  end

  def command(**attributes) = Tasks::DeleteTask.new(**attributes)

  # -- command shape ----------------------------------------------------------

  def test_delete_task_is_immutable
    id = +"de1e0002"
    request = Tasks::DeleteTask.new(id: id, cascade: true, expected_revision: +"v1.a.b.c")
    id.replace("mutated")

    assert request.frozen?
    assert request.id.frozen?
    assert_equal "de1e0002", request.id
    assert_equal true, request.cascade
    assert_equal "v1.a.b.c", request.expected_revision
  end

  def test_wrong_command_type_is_invalid
    with_delete_store do |store, _org, _archive|
      result = store.delete_task!(Object.new)
      assert_equal :invalid, result.status
    end
  end

  # -- leaf delete ------------------------------------------------------------

  def test_leaf_delete_removes_only_the_target_and_records_one_journal_entry
    with_delete_store do |store, org, _archive|
      writes = 0
      original = store.method(:write_records)
      writer = ->(path, records) { writes += 1; original.call(path, records) }

      result = store.stub(:write_records, writer) { store.delete_task!(command(id: IDS[:garden])) }

      assert_equal :ok, result.status
      assert_equal 1, writes
      assert_equal [IDS[:garden]], result.touched_ids
      assert_equal({ removed: 1, descendants: 0, open_descendants: 0 }, result.summary)
      assert_nil record_for(org, title: "random garden thought")
      assert Tasks::Check.check(org).ok?

      assert_equal [:ok, "delete: random garden thought"], store.undo!
      assert_equal [:empty], store.undo!, "one delete is exactly one undo step"
    end
  end

  def test_post_write_check_failure_marks_the_delete_as_rolled_back
    with_delete_store do |store, org, _archive|
      before = File.binread(org)
      result = store.stub(:post_write_failure, "injected check failure") do
        store.delete_task!(command(id: IDS[:garden]))
      end

      assert_equal :store_invalid, result.status
      assert result.rolled_back?
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  # -- descendant guard -------------------------------------------------------

  def test_parent_without_cascade_is_a_conflict_and_writes_nothing
    with_delete_store do |store, org, _archive|
      before = File.binread(org)

      result = store.delete_task!(command(id: IDS[:parent]))

      assert_equal :conflict, result.status
      assert_equal({ descendants: 3, open_descendants: 3 }, result.summary)
      refute_nil result.snapshot
      assert_equal before, File.binread(org), "a refused delete writes nothing"
    end
  end

  def test_cascade_removes_the_exact_subtree_and_leaves_siblings_untouched
    with_delete_store do |store, org, _archive|
      result = store.delete_task!(command(id: IDS[:parent], cascade: true))

      assert_equal :ok, result.status
      # target first, then DFS pre-order of the removed subtree.
      assert_equal [IDS[:parent], IDS[:design], IDS[:build], IDS[:test_gc]], result.touched_ids
      assert_equal({ removed: 4, descendants: 3, open_descendants: 3 }, result.summary)

      records = Tasks::Format.parse(File.read(org)).records
      remaining = records.filter_map { |r| r["id"] if r["type"] == "task" }
      assert_equal [IDS[:garden], IDS[:aunt]], remaining, "siblings and aunts survive"
      assert Tasks::Check.check(org).ok?, "file stays DFS-valid after the splice"
    end
  end

  def test_undo_restores_byte_exact_file_and_redo_re_deletes
    with_delete_store do |store, org, _archive|
      before = File.binread(org)

      assert_equal :ok, store.delete_task!(command(id: IDS[:parent], cascade: true)).status
      after_delete = File.binread(org)
      refute_equal before, after_delete

      assert_equal [:ok, "delete 4 tasks: Launch the site"], store.undo!
      assert_equal before, File.binread(org), "undo restores the exact prior bytes"

      assert_equal [:ok, "delete 4 tasks: Launch the site"], store.redo!
      assert_equal after_delete, File.binread(org), "redo re-applies the same delete"
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- revision (optimistic concurrency) --------------------------------------

  def test_stale_when_a_descendant_changed_between_snapshot_and_cascade_delete
    with_delete_store do |store, org, _archive|
      revision = store.edit_snapshot(IDS[:parent]).revision
      # A state change on a descendant moves the lifecycle fingerprint, which
      # spans the whole subtree.
      store.apply_changeset!(Tasks::TaskChangeset.new(
        id: IDS[:test_gc], changes: { state: "WAITING" },
        expected_revision: store.edit_snapshot(IDS[:test_gc]).revision
      ))

      result = store.delete_task!(command(id: IDS[:parent], cascade: true, expected_revision: revision))

      assert_equal :stale, result.status
      refute_nil result.snapshot
      refute_nil record_for(org, title: "Launch the site"), "a stale delete writes nothing"
    end
  end

  def test_strict_check_catches_a_sibling_captured_under_the_same_parent
    with_delete_store do |store, org, _archive|
      # A leaf whose parent is a section: capturing a sibling into that section
      # changes only the location fingerprint (sibling id list), not lifecycle.
      revision = store.edit_snapshot(IDS[:aunt]).revision
      assert_equal :ok, store.create_task!(Tasks::CreateTask.new(title: "Fresh sibling", project: "Projects")).status

      result = store.delete_task!(command(id: IDS[:aunt], expected_revision: revision))

      assert_equal :stale, result.status
      refute_nil record_for(org, title: "Separate project")
    end
  end

  def test_nil_expected_revision_skips_the_check
    with_delete_store do |store, _org, _archive|
      store.apply_changeset!(Tasks::TaskChangeset.new(
        id: IDS[:test_gc], changes: { state: "WAITING" },
        expected_revision: store.edit_snapshot(IDS[:test_gc]).revision
      ))

      result = store.delete_task!(command(id: IDS[:parent], cascade: true, expected_revision: nil))

      assert_equal :ok, result.status
    end
  end

  def test_matching_revision_deletes_a_leaf
    with_delete_store do |store, _org, _archive|
      revision = store.edit_snapshot(IDS[:garden]).revision
      result = store.delete_task!(command(id: IDS[:garden], expected_revision: revision))
      assert_equal :ok, result.status
    end
  end

  def test_malformed_expected_revision_is_invalid
    with_delete_store do |store, org, _archive|
      before = File.binread(org)
      result = store.delete_task!(command(id: IDS[:garden], expected_revision: "not-a-revision"))
      assert_equal :invalid, result.status
      assert_equal before, File.binread(org)
    end
  end

  # -- lookup edges -----------------------------------------------------------

  def test_missing_or_blank_id_is_invalid
    with_delete_store do |store, _org, _archive|
      assert_equal :invalid, store.delete_task!(command(id: nil)).status
      assert_equal :invalid, store.delete_task!(command(id: "")).status
    end
  end

  def test_unknown_id_is_not_found
    with_delete_store do |store, _org, _archive|
      assert_equal :not_found, store.delete_task!(command(id: "ffffffff")).status
    end
  end

  def test_archived_only_id_is_not_found_and_archive_is_left_alone
    archive_records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "task", "id" => "arc00001", "state" => "DONE", "title" => "Archived thing",
        "archived" => "2026-06-01", "closed" => "2026-06-01" },
    ]
    with_delete_store(archive_records: archive_records) do |store, _org, archive|
      before = File.binread(archive)
      result = store.delete_task!(command(id: "arc00001"))
      assert_equal :not_found, result.status
      assert_equal before, File.binread(archive), "the archive is read-only and untouched"
    end
  end

  def test_section_id_is_invalid_delete_targets_tasks
    with_delete_store do |store, _org, _archive|
      result = store.delete_task!(command(id: IDS[:projects]))
      assert_equal :invalid, result.status
      assert_includes result.errors, "delete targets tasks"
    end
  end

  def test_invalid_file_is_store_invalid_and_writes_nothing
    invalid = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "cccc0001", "title" => "Work" },
      { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001", "state" => "TODO",
        "title" => "Fix widget", "scheduled" => "not-a-date" },
    ]
    with_delete_store(records: invalid) do |store, org, _archive|
      before = File.binread(org)
      result = store.delete_task!(command(id: "cccc0002"))
      assert_equal :store_invalid, result.status
      assert_equal before, File.binread(org), "deletion is never a repair route"
    end
  end

  # -- Application facade ------------------------------------------------------

  def test_application_deletes_through_the_facade_with_a_fresh_store_per_call
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, dump_fixture(NESTED_RECORDS))
      built = []
      factory = lambda do
        store = Tasks::Store.new(org: org, archive: archive)
        built << store
        store
      end
      app = Tasks::Application.new(store_factory: factory)
      context = Tasks::OperationContext.new(operation_id: "delete-1", source: :cli)

      refused = app.delete_task(IDS[:parent], context: context)
      assert_equal :conflict, refused.status

      ok = app.delete_task(IDS[:parent], cascade: true, context: context)
      assert_equal :ok, ok.status
      assert_match(/\As1\.[0-9a-f]{64}\z/, ok.store_revision)
      assert_equal app.read_status_result.store_revision, ok.store_revision
      assert_equal [IDS[:parent], IDS[:design], IDS[:build], IDS[:test_gc]], ok.touched_ids

      assert_equal 3, built.length
      assert_equal 3, built.uniq.length
      assert_raises(ArgumentError) { app.delete_task(IDS[:garden], context: :cli) }
    end
  end
end
