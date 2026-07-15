# frozen_string_literal: true

require_relative "test_helper"
require "tasks/application"

class TestTaskPlacement < Minitest::Test
  IDS = {
    one: "10000001", a: "a0000001", a_child: "a0000002", a_grand: "a0000003",
    b: "b0000001", b_child: "b0000002", c: "c0000001", d: "d0000001",
    two: "20000001", e: "e0000001", e_child: "e0000002", f: "f0000001",
    three: "30000001",
  }.freeze

  TREE = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => IDS[:one], "title" => "One" },
    { "type" => "task", "id" => IDS[:a], "parent" => IDS[:one], "state" => "TODO", "title" => "A" },
    { "type" => "task", "id" => IDS[:a_child], "parent" => IDS[:a], "state" => "TODO", "title" => "A child" },
    { "type" => "task", "id" => IDS[:a_grand], "parent" => IDS[:a_child], "state" => "TODO", "title" => "A grandchild" },
    { "type" => "task", "id" => IDS[:b], "parent" => IDS[:one], "state" => "TODO", "title" => "B" },
    { "type" => "task", "id" => IDS[:b_child], "parent" => IDS[:b], "state" => "TODO", "title" => "B child" },
    { "type" => "task", "id" => IDS[:c], "parent" => IDS[:one], "state" => "TODO", "title" => "C" },
    { "type" => "task", "id" => IDS[:d], "parent" => IDS[:one], "state" => "TODO", "title" => "D" },
    { "type" => "section", "id" => IDS[:two], "title" => "Two" },
    { "type" => "task", "id" => IDS[:e], "parent" => IDS[:two], "state" => "TODO", "title" => "E" },
    { "type" => "task", "id" => IDS[:e_child], "parent" => IDS[:e], "state" => "TODO", "title" => "E child" },
    { "type" => "task", "id" => IDS[:f], "parent" => IDS[:two], "state" => "TODO", "title" => "F" },
    { "type" => "section", "id" => IDS[:three], "title" => "Three" },
  ].freeze

  def with_placement_store(max_depth: Tasks::Tree::DEFAULT_MAX_DEPTH)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(TREE))
      yield Tasks::Store.new(org: org, archive: archive, max_depth: max_depth), org, archive
    end
  end

  def parsed(path)
    Tasks::Format.parse(File.read(path, encoding: "UTF-8")).records
  end

  def child_ids(path, parent_id)
    parsed(path).filter_map do |record|
      record["id"] if record["type"] == "task" && record["parent"] == parent_id
    end
  end

  def request(store, id, parent_id:, before_id: nil, snapshot: nil, changes: nil)
    snapshot ||= store.edit_snapshot(id)
    location = Tasks::TaskPlacement.new(parent_id: parent_id, before_id: before_id)
    Tasks::TaskChangeset.from(snapshot, changes: (changes || {}).merge(location: location))
  end

  def place(store, id, parent_id:, before_id: nil, snapshot: nil, changes: nil)
    store.apply_changeset!(
      request(store, id, parent_id: parent_id, before_id: before_id,
              snapshot: snapshot, changes: changes)
    )
  end

  def assert_checked(path)
    result = Tasks::Check.check(path)
    assert result.ok?, result.errors.inspect
  end

  def test_reorders_first_middle_and_last_siblings_before_an_anchor
    cases = [
      [IDS[:a], IDS[:c], [IDS[:b], IDS[:a], IDS[:c], IDS[:d]]],
      [IDS[:c], IDS[:a], [IDS[:c], IDS[:a], IDS[:b], IDS[:d]]],
      [IDS[:d], IDS[:b], [IDS[:a], IDS[:d], IDS[:b], IDS[:c]]],
    ]

    cases.each do |moving_id, before_id, expected|
      with_placement_store do |store, org, _archive|
        result = place(store, moving_id, parent_id: IDS[:one], before_id: before_id)

        assert_equal :ok, result.status
        assert_equal expected, child_ids(org, IDS[:one])
        assert_checked(org)
      end
    end
  end

  def test_same_parent_append_reorders_instead_of_taking_legacy_early_noop
    with_placement_store do |store, org, _archive|
      result = place(store, IDS[:b], parent_id: IDS[:one])

      assert_equal :ok, result.status
      assert_equal [IDS[:a], IDS[:c], IDS[:d], IDS[:b]], child_ids(org, IDS[:one])
      assert_equal [IDS[:b], IDS[:b_child]], result.touched_ids
      assert_equal({ from: IDS[:one], to: IDS[:one], before: nil,
                     moved_ids: [IDS[:b], IDS[:b_child]] }, result.summary)
      assert_checked(org)
    end
  end

  def test_cross_parent_move_inserts_full_subtree_before_anchor
    with_placement_store do |store, org, _archive|
      result = place(store, IDS[:a], parent_id: IDS[:two], before_id: IDS[:f])

      assert_equal :ok, result.status
      assert_equal [IDS[:e], IDS[:a], IDS[:f]], child_ids(org, IDS[:two])
      assert_equal [IDS[:a], IDS[:a_child], IDS[:a_grand]], result.touched_ids
      assert_equal result.touched_ids, result.summary[:moved_ids]
      records = parsed(org).to_h { |record| [record["id"], record] }
      assert_equal IDS[:two], records.fetch(IDS[:a])["parent"]
      assert_equal IDS[:a], records.fetch(IDS[:a_child])["parent"]
      assert_equal IDS[:a_child], records.fetch(IDS[:a_grand])["parent"]
      assert_checked(org)
    end
  end

  def test_moves_to_empty_section_and_from_section_level_under_a_task
    with_placement_store do |store, org, _archive|
      first = place(store, IDS[:c], parent_id: IDS[:three])
      second = place(store, IDS[:d], parent_id: IDS[:c])

      assert_equal :ok, first.status
      assert_equal :ok, second.status
      assert_equal [IDS[:c]], child_ids(org, IDS[:three])
      assert_equal [IDS[:d]], child_ids(org, IDS[:c])
      assert_checked(org)
    end
  end

  def test_moves_a_nested_task_to_a_section
    with_placement_store do |store, org, _archive|
      result = place(store, IDS[:a_child], parent_id: IDS[:three])

      assert_equal :ok, result.status
      assert_equal [IDS[:a_child], IDS[:a_grand]], result.touched_ids
      assert_equal [IDS[:a_child]], child_ids(org, IDS[:three])
      assert_equal [IDS[:a_grand]], child_ids(org, IDS[:a_child])
      assert_checked(org)
    end
  end

  def test_missing_parent_and_anchor_are_field_specific_and_resolved_before_cycles
    with_placement_store do |store, org, _archive|
      before = File.binread(org)
      missing_parent = place(store, IDS[:a], parent_id: "deadbeef", before_id: IDS[:a_child])
      missing_anchor = place(store, IDS[:a], parent_id: IDS[:a_child], before_id: "deadbeef")

      assert_equal :not_found, missing_parent.status
      assert_equal({ parent_id: ["parent_id does not identify a live task or section"] },
                   missing_parent.field_errors)
      assert_equal :not_found, missing_anchor.status,
                   "all ids resolve before the descendant-parent cycle check"
      assert_equal({ before_id: ["before_id does not identify a live task"] },
                   missing_anchor.field_errors)
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_self_and_descendant_parents_or_anchors_are_cycles_before_parentage_conflicts
    with_placement_store do |store, org, _archive|
      before = File.binread(org)
      requests = [
        request(store, IDS[:a], parent_id: IDS[:a]),
        request(store, IDS[:a], parent_id: IDS[:a_child]),
        request(store, IDS[:a], parent_id: IDS[:one], before_id: IDS[:a]),
        request(store, IDS[:a], parent_id: IDS[:two], before_id: IDS[:a_child]),
      ]

      requests.each do |placement|
        result = store.apply_changeset!(placement)
        assert_equal :cycle, result.status
        assert_equal before, File.binread(org)
      end
      assert_equal [:empty], store.undo!
    end
  end

  def test_unrelated_wrong_parent_anchor_is_validated_before_same_parent_noop
    with_placement_store do |store, org, _archive|
      before = File.binread(org)
      result = place(store, IDS[:a], parent_id: IDS[:one], before_id: IDS[:e])

      assert_equal :conflict, result.status
      assert_equal IDS[:two], result.summary[:current_parent_id]
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_full_subtree_height_is_checked_against_destination_depth
    with_placement_store(max_depth: 4) do |store, org, _archive|
      before = File.binread(org)
      result = place(store, IDS[:a], parent_id: IDS[:e_child])

      assert_equal :too_deep, result.status
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_exact_anchor_and_append_slots_are_noops_without_writes_or_history
    [[IDS[:a], IDS[:b]], [IDS[:d], nil]].each do |moving_id, before_id|
      with_placement_store do |store, org, _archive|
        before = File.binread(org)
        writes = 0
        writer = ->(*) { writes += 1 }
        result = store.stub(:write_records, writer) do
          place(store, moving_id, parent_id: IDS[:one], before_id: before_id)
        end

        assert_equal :no_change, result.status
        assert_equal [], result.summary[:moved_ids]
        assert_equal 0, writes
        assert_equal before, File.binread(org)
        assert_equal [:empty], store.undo!
      end
    end
  end

  def test_undo_and_redo_restore_byte_identical_placement_states
    with_placement_store do |store, org, _archive|
      original = File.binread(org)
      result = place(store, IDS[:a], parent_id: IDS[:two], before_id: IDS[:f])
      moved = File.binread(org)

      assert_equal :ok, result.status
      assert_checked(org)
      assert_equal :ok, store.undo!.first
      assert_equal original, File.binread(org)
      assert_equal :ok, store.redo!.first
      assert_equal moved, File.binread(org)
      assert_checked(org)
    end
  end

  def test_post_write_check_failure_rolls_back_placement_and_records_no_history
    with_placement_store do |store, org, _archive|
      before = File.binread(org)
      result = store.stub(:post_write_failure, "injected placement check failure") do
        place(store, IDS[:a], parent_id: IDS[:two], before_id: IDS[:f])
      end

      assert_equal :store_invalid, result.status
      assert_equal before, File.binread(org)
      assert_equal [:empty], store.undo!
    end
  end

  def test_revision_own_component_excludes_location_but_order_changes_location_components
    with_placement_store do |store, _org, _archive|
      before = IDS.values_at(:a, :b, :c, :d).to_h { |id| [id, store.edit_snapshot(id).revision] }
      result = place(store, IDS[:c], parent_id: IDS[:one], before_id: IDS[:a])
      after = IDS.values_at(:a, :b, :c, :d).to_h { |id| [id, store.edit_snapshot(id).revision] }

      assert_equal :ok, result.status
      before.each_key do |id|
        assert_equal before[id].split(".")[1], after[id].split(".")[1], "own digest changed for #{id}"
        refute_equal before[id].split(".")[2], after[id].split(".")[2], "location digest did not change for #{id}"
      end

      cross_before = result.snapshot.revision
      cross = place(store, IDS[:c], parent_id: IDS[:two], before_id: IDS[:f], snapshot: result.snapshot)
      assert_equal :ok, cross.status
      assert_equal cross_before.split(".")[1], cross.snapshot.revision.split(".")[1]
      refute_equal cross_before.split(".")[2], cross.snapshot.revision.split(".")[2]
    end
  end

  def test_consecutive_drags_and_unrelated_sibling_churn_use_live_stable_anchors
    with_placement_store do |store, org, _archive|
      a_snapshot = store.edit_snapshot(IDS[:a])
      b_snapshot = store.edit_snapshot(IDS[:b])
      c_snapshot = store.edit_snapshot(IDS[:c])

      assert_equal :ok, place(store, IDS[:c], parent_id: IDS[:one], before_id: IDS[:a], snapshot: c_snapshot).status
      assert_equal :ok, place(store, IDS[:b], parent_id: IDS[:one], before_id: IDS[:c], snapshot: b_snapshot).status
      assert_equal :ok, place(store, IDS[:a], parent_id: IDS[:one], before_id: IDS[:c], snapshot: a_snapshot).status

      records = parsed(org)
      section_two = records.index { |record| record["id"] == IDS[:two] }
      records.insert(section_two, {
        "type" => "task", "id" => "aa000001", "parent" => IDS[:one],
        "state" => "TODO", "title" => "Unrelated sibling",
      })
      File.write(org, Tasks::Format.dump(records))

      assert_equal :ok, place(store, IDS[:a], parent_id: IDS[:two], before_id: IDS[:f], snapshot: a_snapshot).status
      assert_checked(org)
    end
  end

  def test_placement_stales_on_own_edit_but_legacy_move_still_stales_on_sibling_order
    with_placement_store do |store, _org, _archive|
      stale_placement = store.edit_snapshot(IDS[:c])
      edited = store.apply_changeset!(Tasks::TaskChangeset.from(
        stale_placement, changes: { title: "C edited" }
      ))
      assert_equal :ok, edited.status
      assert_equal :stale,
                   place(store, IDS[:c], parent_id: IDS[:two], before_id: IDS[:f],
                         snapshot: stale_placement).status

      legacy_snapshot = store.edit_snapshot(IDS[:b])
      assert_equal :ok, place(store, IDS[:d], parent_id: IDS[:one], before_id: IDS[:b]).status
      legacy = Tasks::TaskChangeset.from(legacy_snapshot, changes: { location: IDS[:two] })
      assert_equal :stale, store.apply_changeset!(legacy).status
    end
  end

  def test_ordinary_field_edit_ignores_a_concurrent_location_change
    with_placement_store do |store, _org, _archive|
      original = store.edit_snapshot(IDS[:a])
      moved = place(store, IDS[:a], parent_id: IDS[:two], before_id: IDS[:f], snapshot: original)
      assert_equal :ok, moved.status

      edit = Tasks::TaskChangeset.from(original, changes: { title: "A renamed" })
      result = store.apply_changeset!(edit)

      assert_equal :ok, result.status
      assert_equal "A renamed", result.snapshot.title
      assert_equal IDS[:two], result.snapshot.parent_id
    end
  end

  def test_application_applies_multi_field_placement_as_one_atomic_command
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(TREE))
      app = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive)
      )
      snapshot = app.edit_snapshot(IDS[:a])
      command = request(
        app, IDS[:a], parent_id: IDS[:two], before_id: IDS[:f], snapshot: snapshot,
        changes: { title: "A moved" }
      )

      result = app.update_task(command)

      assert_equal :ok, result.status
      assert_equal "A moved", result.snapshot.title
      assert_equal IDS[:two], result.snapshot.parent_id
      assert_equal %i[title location], result.summary[:fields]
      assert_equal [IDS[:a], IDS[:a_child], IDS[:a_grand]], result.touched_ids
      assert_match(/\As1\.[0-9a-f]{64}\z/, result.store_revision)
      assert_checked(org)
    end
  end
end
