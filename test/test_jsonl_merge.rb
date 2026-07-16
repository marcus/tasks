# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "rbconfig"
require "tasks/jsonl_merge"

class TestJsonlMerge < Minitest::Test
  BIN = File.expand_path("../bin/tasks", __dir__)
  HOME_STAMP = "2026-07-16T10:00:00Z#home"
  WORK_STAMP = "2026-07-16T11:00:00Z#work"

  def base_records
    [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "10000001", "title" => "Work" },
      { "type" => "task", "id" => "10000002", "parent" => "10000001", "state" => "NEXT",
        "title" => "Book Sixt car", "tags" => ["@computer"], "scheduled" => "2026-07-18",
        "body" => "Reservation started." },
      { "type" => "task", "id" => "10000003", "parent" => "10000001", "state" => "TODO",
        "title" => "Call PSE" },
      { "type" => "task", "id" => "10000004", "parent" => "10000001", "state" => "TODO",
        "title" => "Review Stash" },
    ]
  end

  def copy(records)
    Tasks::Format.parse(Tasks::Format.dump(records)).records.map { |record| record.reject { |key, _| key == "line" } }
  end

  def change(records, id, **fields)
    changed = copy(records)
    record = changed.find { |entry| entry["id"] == id }
    fields.each { |key, value| value.nil? ? record.delete(key.to_s) : record[key.to_s] = value }
    changed
  end

  def merge(base, ours, theirs)
    result = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(base),
      ours_text: Tasks::Format.dump(ours),
      theirs_text: Tasks::Format.dump(theirs)
    )
    assert result.ok?, result.error
    [Tasks::Format.parse(result.text).records, result]
  end

  def find(records, id)
    records.find { |record| record["id"] == id }
  end

  def test_non_overlapping_fields_merge_without_conflict
    ours = change(base_records, "10000002", tags: ["@computer", "travel"], updated: HOME_STAMP)
    theirs = change(base_records, "10000002", scheduled: "2026-07-19", updated: WORK_STAMP)

    records, result = merge(base_records, ours, theirs)
    task = find(records, "10000002")

    assert_equal ["@computer", "travel"], task["tags"]
    assert_equal "2026-07-19", task["scheduled"]
    assert_equal WORK_STAMP, task["updated"]
    assert_empty result.events.first[:conflicts]
  end

  def test_same_field_uses_newer_updated_and_is_commutative
    ours = change(base_records, "10000003", title: "Call utility", updated: HOME_STAMP)
    theirs = change(base_records, "10000003", title: "Call PSE billing", updated: WORK_STAMP)

    forward_records, = merge(base_records, ours, theirs)
    reverse_records, = merge(base_records, theirs, ours)

    assert_equal "Call PSE billing", find(forward_records, "10000003")["title"]
    assert_equal Tasks::Format.dump(forward_records), Tasks::Format.dump(reverse_records)
  end

  def test_pre_timestamp_conflict_is_ours_wins_and_logged_low_confidence
    ours = change(base_records, "10000003", title: "Ours title")
    theirs = change(base_records, "10000003", title: "Theirs title")

    records, result = merge(base_records, ours, theirs)

    assert_equal "Ours title", find(records, "10000003")["title"]
    assert_includes result.events.first[:low_confidence], "title"
    assert_includes result.log_lines.join("\n"), "low-confidence=title"
  end

  def test_tags_union_preserves_base_order_and_sorts_concurrent_additions
    base = change(base_records, "10000002", tags: %w[@computer important])
    ours = change(base, "10000002", tags: %w[@computer important zeta], updated: HOME_STAMP)
    theirs = change(base, "10000002", tags: %w[@computer important alpha], updated: WORK_STAMP)

    records, = merge(base, ours, theirs)

    assert_equal %w[@computer important alpha zeta], find(records, "10000002")["tags"]
  end

  def test_progressed_state_beats_open_state_and_carries_closed_date
    ours = change(base_records, "10000002", state: "DONE", closed: "2026-07-16", updated: HOME_STAMP)
    theirs = change(base_records, "10000002", state: "TODO", updated: WORK_STAMP)

    records, = merge(base_records, ours, theirs)
    task = find(records, "10000002")

    assert_equal "DONE", task["state"]
    assert_equal "2026-07-16", task["closed"]
  end

  def test_body_prefix_chooses_longer_append
    ours = change(base_records, "10000002", body: "Reservation started.\nConfirmation 1", updated: HOME_STAMP)
    theirs = change(ours, "10000002", body: "Reservation started.\nConfirmation 1\nConfirmation 2",
                     updated: WORK_STAMP)

    records, = merge(base_records, ours, theirs)

    assert_equal "Reservation started.\nConfirmation 1\nConfirmation 2", find(records, "10000002")["body"]
  end

  def test_delete_vs_unchanged_deletes_but_delete_vs_edit_keeps_edit
    ours_deleted = copy(base_records).reject { |record| record["id"] == "10000003" }
    unchanged_records, = merge(base_records, ours_deleted, base_records)
    assert_nil find(unchanged_records, "10000003")

    edited = change(base_records, "10000003", title: "Edited concurrently", updated: WORK_STAMP)
    edited_records, result = merge(base_records, ours_deleted, edited)
    assert_equal "Edited concurrently", find(edited_records, "10000003")["title"]
    assert_equal :kept_theirs_edit_over_ours_delete, result.events.first[:decision]
  end

  def test_subtree_delete_vs_descendant_edit_restores_required_ancestor_chain
    nested = copy(base_records)
    find(nested, "10000003")["parent"] = "10000002"
    ours_deleted = nested.reject { |record| %w[10000002 10000003].include?(record["id"]) }
    theirs = change(nested, "10000003", title: "Edited nested task", updated: WORK_STAMP)

    records, result = merge(nested, ours_deleted, theirs)

    assert_equal "10000002", find(records, "10000003")["parent"]
    assert find(records, "10000002"), "the deleted ancestor is restored to keep the edited child valid"
    assert_includes result.events.map { |event| event[:decision] }, :restored_ancestor_for_edited_descendant
    assert Tasks::Check.check_text(result.text).ok?
  end

  def test_adds_from_both_sides_are_kept_in_valid_ours_first_order
    ours = copy(base_records)
    ours << { "type" => "task", "id" => "10000005", "parent" => "10000001", "state" => "TODO",
              "title" => "Ours add", "updated" => HOME_STAMP }
    theirs = copy(base_records)
    theirs << { "type" => "task", "id" => "10000006", "parent" => "10000001", "state" => "TODO",
                "title" => "Theirs add", "updated" => WORK_STAMP }

    records, result = merge(base_records, ours, theirs)

    assert_equal "meta", records.first["type"]
    assert_operator records.index { |record| record["id"] == "10000005" }, :<,
                    records.index { |record| record["id"] == "10000006" }
    assert Tasks::Check.check_text(result.text).ok?
  end

  def test_theirs_only_parent_and_child_are_inserted_as_a_contiguous_subtree
    theirs = copy(base_records)
    theirs << { "type" => "section", "id" => "20000001", "title" => "Home" }
    theirs << { "type" => "task", "id" => "20000002", "parent" => "20000001", "state" => "TODO",
                "title" => "New child", "updated" => WORK_STAMP }

    records, result = merge(base_records, base_records, theirs)
    parent_index = records.index { |record| record["id"] == "20000001" }
    child_index = records.index { |record| record["id"] == "20000002" }

    assert_equal parent_index + 1, child_index
    assert Tasks::Check.check_text(result.text).ok?
  end

  def test_concurrent_reordering_uses_ours_and_is_logged
    base = base_records
    ours = [base[0], base[1], base[3], base[2], base[4]]
    theirs = [base[0], base[1], base[4], base[2], base[3]]

    records, result = merge(base, ours, theirs)

    task_ids = records.filter_map do |record|
      record["id"] if record["type"] == "task"
    end
    assert_equal %w[10000003 10000002 10000004], task_ids
    assert_includes result.events.map { |event| event[:decision] }, :ours_ordering_conflict
  end

  def test_malformed_or_duplicate_side_fails_without_text
    malformed = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(base_records),
      ours_text: "not-json\n",
      theirs_text: Tasks::Format.dump(base_records)
    )
    refute malformed.ok?
    assert_nil malformed.text

    duplicate = copy(base_records) << copy(base_records).last
    invalid = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(base_records),
      ours_text: Tasks::Format.dump(duplicate),
      theirs_text: Tasks::Format.dump(base_records)
    )
    refute invalid.ok?
    assert_includes invalid.error, "duplicate id"
  end

  def test_empty_base_supports_concurrent_first_archive_creation
    ours = [base_records.first, base_records[1], base_records[2]]
    theirs = [base_records.first, base_records[1], base_records[3]]

    result = Tasks::JsonlMerge.merge(
      base_text: "", ours_text: Tasks::Format.dump(ours), theirs_text: Tasks::Format.dump(theirs)
    )

    assert result.ok?, result.error
    records = Tasks::Format.parse(result.text).records
    assert find(records, "10000002")
    assert find(records, "10000003")
    assert Tasks::Check.check_text(result.text).ok?
  end

  def test_archive_vs_concurrent_edit_pair_is_rejected_by_cross_file_check
    live_base = base_records
    live_archiver = copy(live_base).reject { |record| record["id"] == "10000003" }
    live_editor = change(live_base, "10000003", title: "Edited while archiving", updated: WORK_STAMP)
    merged_live = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(live_base), ours_text: Tasks::Format.dump(live_archiver),
      theirs_text: Tasks::Format.dump(live_editor)
    )
    assert merged_live.ok?, merged_live.error

    archive_base = [live_base.first, { "type" => "section", "id" => "90000001", "title" => "Archive" }]
    archive_archiver = copy(archive_base)
    archive_archiver << {
      "type" => "task", "id" => "10000003", "parent" => "90000001", "state" => "DONE",
      "title" => "Call PSE", "closed" => "2026-07-16", "updated" => HOME_STAMP,
    }
    merged_archive = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(archive_base), ours_text: Tasks::Format.dump(archive_archiver),
      theirs_text: Tasks::Format.dump(archive_base)
    )
    assert merged_archive.ok?, merged_archive.error

    Dir.mktmpdir do |dir|
      live_path = File.join(dir, "tasks.jsonl")
      archive_path = File.join(dir, "archive.jsonl")
      File.write(live_path, merged_live.text)
      File.write(archive_path, merged_archive.text)

      result = Tasks::Check.check_store(live_path, archive_path)

      refute result.ok?
      assert_includes result.errors.map(&:last).join("\n"), 'id "10000003" appears in both'
    end
  end

  def test_cli_driver_leaves_ours_untouched_on_failure_and_logs_it
    Dir.mktmpdir do |dir|
      base = File.join(dir, "base.jsonl")
      ours = File.join(dir, "ours.jsonl")
      theirs = File.join(dir, "theirs.jsonl")
      pathname = File.join(dir, "tasks.jsonl")
      File.write(base, Tasks::Format.dump(base_records))
      File.write(ours, Tasks::Format.dump(base_records))
      File.write(theirs, "<<<<<<< broken\n")
      before = File.binread(ours)

      _stdout, stderr, status = Open3.capture3(RbConfig.ruby, BIN, "merge-driver", base, ours, theirs, pathname)

      refute status.success?
      assert_includes stderr, "merge failed"
      assert_equal before, File.binread(ours)
      assert_includes File.read(File.join(dir, ".tasks-merge.log")), "failed"
    end
  end

  def test_real_world_sixt_pse_stash_divergence_matches_hand_resolution
    ours = copy(base_records)
    find(ours, "10000002")["tags"] = %w[@computer travel]
    find(ours, "10000002")["updated"] = HOME_STAMP
    find(ours, "10000003")["title"] = "Call PSE about final bill"
    find(ours, "10000003")["updated"] = HOME_STAMP

    theirs = copy(base_records)
    find(theirs, "10000002")["scheduled"] = "2026-07-19"
    find(theirs, "10000002")["updated"] = WORK_STAMP
    find(theirs, "10000004")["body"] = "Stash migration notes."
    find(theirs, "10000004")["updated"] = WORK_STAMP

    records, result = merge(base_records, ours, theirs)

    assert_equal %w[@computer travel], find(records, "10000002")["tags"]
    assert_equal "2026-07-19", find(records, "10000002")["scheduled"]
    assert_equal "Call PSE about final bill", find(records, "10000003")["title"]
    assert_equal "Stash migration notes.", find(records, "10000004")["body"]
    assert Tasks::Check.check_text(result.text).ok?
  end
end
