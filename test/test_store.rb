# frozen_string_literal: true

require_relative "test_helper"
require "tasks/format"
require "timeout"

class TestStore < Minitest::Test
  def test_parse_finds_all_items
    with_store do |store, _org, _archive|
      assert_equal 7, store.items.size
      assert_equal %w[INBOX NEXT NEXT TODO WAITING DONE NEXT], store.items.map(&:state)
    end
  end

  def test_parse_reads_priority_tags_and_dates
    with_store do |store, _org, _archive|
      flight = find_item(store, "Book flight")
      assert_equal "A", flight.priority
      assert_equal Date.new(2026, 7, 2), flight.deadline
      assert_includes flight.tags, "@computer"
      assert_includes flight.tags, "important"

      eval = find_item(store, "self-eval")
      assert_equal Date.new(2026, 7, 3), eval.scheduled
      assert_nil eval.deadline
    end
  end

  def test_parse_reads_closed_date
    with_store do |store, _org, _archive|
      done = find_item(store, "Old finished thing")
      assert_equal Date.new(2026, 6, 20), done.closed
      assert_nil find_item(store, "Book flight").closed
    end
  end

  # Each record owns its own fields, so `closed` never leaks between a parent
  # and a child the way an org CLOSED: line's block scope once could.
  def test_closed_is_per_record_parent_and_child
    with_store do |store, org, _archive|
      File.write(org, dump_fixture([
        { "type" => "meta", "version" => 1 },
        { "type" => "section", "id" => "cccc0001", "title" => "Work" },
        { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001", "state" => "DONE",
          "title" => "Parent", "closed" => "2026-06-20" },
        { "type" => "task", "id" => "cccc0003", "parent" => "cccc0002", "state" => "NEXT",
          "title" => "Child" },
      ]))
      store.reload!
      assert_equal Date.new(2026, 6, 20), find_item(store, "Parent").closed
      assert_nil find_item(store, "Child").closed
    end
  end

  def test_headline_renders_state_priority_title_tags
    with_store do |store, _org, _archive|
      flight = find_item(store, "Book flight")
      assert_equal "NEXT [#A] Book flight in Concur :@computer:important:urgent:",
                   store.headline(flight)
    end
  end

  def test_headline_omits_priority_when_absent
    with_store do |store, _org, _archive|
      plants = find_item(store, "Water the plants")
      assert_equal "NEXT Water the plants :@home:", store.headline(plants)
    end
  end

  def test_headline_omits_tags_when_absent
    with_store do |store, _org, _archive|
      thought = find_item(store, "random thought")
      assert_equal "INBOX random thought about the garden", store.headline(thought)
    end
  end

  def test_changed_detects_external_writes
    with_store do |store, org, _archive|
      store.reload!
      refute store.changed?
      future = Time.now + 2
      extra = dump_fixture([{ "type" => "task", "id" => "bbbb0001", "parent" => FIX[:home],
                              "state" => "TODO", "title" => "added later" }])
      File.write(org, FIXTURE + extra)
      File.utime(future, future, org) # avoid same-mtime flakiness
      assert store.changed?
      assert_equal 8, store.items.size
    end
  end

  def test_complete_marks_done_with_closed_stamp
    with_store do |store, org, _archive|
      flight = find_item(store, "Book flight")
      assert store.complete!(flight)
      rec = record_for(org, title: "Book flight in Concur")
      assert_equal "DONE", rec["state"]
      assert_equal Date.today.iso8601, rec["closed"]
      assert_equal "DONE", find_item(store, "Book flight").state
    end
  end

  def test_complete_rejects_stale_line_numbers
    with_store do |store, org, _archive|
      flight = find_item(store, "Book flight")
      stale = flight.dup
      stale.id = nil    # no id to relocate by...
      stale.line = 1    # ...and the line points at the meta record, not the flight
      refute store.complete!(stale)
      assert_equal "NEXT", record_for(org, title: "Book flight in Concur")["state"]
    end
  end

  def test_reschedule_updates_existing_deadline
    with_store do |store, org, _archive|
      flight = find_item(store, "Book flight")
      assert store.reschedule!(flight, Date.new(2026, 7, 10))
      assert_equal "2026-07-10", record_for(org, title: "Book flight in Concur")["deadline"]
      assert_equal Date.new(2026, 7, 10), find_item(store, "Book flight").deadline
    end
  end

  def test_reschedule_updates_scheduled_when_no_deadline
    with_store do |store, org, _archive|
      eval = find_item(store, "self-eval")
      assert store.reschedule!(eval, Date.new(2026, 7, 8))
      rec = record_for(org, title: "Midyear self-eval")
      assert_equal "2026-07-08", rec["scheduled"]
      refute rec.key?("deadline"), "the self-eval must not have gained a DEADLINE"
    end
  end

  def test_reschedule_adds_deadline_when_item_has_no_stamp
    with_store do |store, org, _archive|
      plants = find_item(store, "Water the plants")
      assert store.reschedule!(plants, Date.new(2026, 7, 5))
      assert_equal Date.new(2026, 7, 5), find_item(store, "Water the plants").deadline
      assert_equal "2026-07-05", record_for(org, title: "Water the plants")["deadline"]
    end
  end

  def test_reschedule_promotes_inbox_item_to_todo
    with_store do |store, org, _a|
      garden = find_item(store, "garden")
      assert_equal "INBOX", garden.state
      assert store.reschedule!(garden, Date.new(2026, 7, 10))
      fresh = find_item(store, "garden")
      assert_equal "TODO", fresh.state
      assert_equal Date.new(2026, 7, 10), fresh.deadline
      assert_equal "TODO", record_for(org, title: "random thought about the garden")["state"]
    end
  end

  def test_reschedule_does_not_promote_non_inbox_states
    with_store do |store, _o, _a|
      waiting = find_item(store, "Travel desk")
      assert store.reschedule!(waiting, Date.new(2026, 7, 9))
      assert_equal "WAITING", find_item(store, "Travel desk").state
    end
  end

  def test_reschedule_promotion_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.reschedule!(find_item(store, "garden"), Date.new(2026, 7, 10))
      store.undo!
      assert_equal before, File.read(org)
      assert_equal "INBOX", find_item(store, "garden").state
    end
  end

  def test_reschedule_does_not_touch_other_items
    with_store do |store, _org, _archive|
      waiting = find_item(store, "Travel desk")
      assert store.reschedule!(waiting, Date.new(2026, 7, 9))
      assert_equal Date.new(2026, 7, 2), find_item(store, "Book flight").deadline
      assert_equal Date.new(2026, 7, 9), find_item(store, "Travel desk").deadline
    end
  end

  def test_archive_sweeps_done_blocks
    with_store do |store, org, archive|
      n = store.archive_swept!
      assert_equal 1, n
      assert_nil record_for(org, title: "Old finished thing"), "swept out of the live file"
      arch = record_for(archive, title: "Old finished thing")
      assert_equal "DONE", arch["state"]
      assert_equal "2026-06-20", arch["closed"] # closed field travels too
      assert_equal Date.today.iso8601, arch["archived"]
      refute arch.key?("parent"), "a swept root loses its parent"
      assert_equal 6, store.items.size
    end
  end

  ARCHIVE_TREE_RECORDS = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "accc0001", "title" => "Projects" },
    { "type" => "task", "id" => "accc0002", "parent" => "accc0001", "state" => "DONE",
      "title" => "Closed project", "closed" => "2026-07-01" },
    { "type" => "task", "id" => "accc0003", "parent" => "accc0002", "state" => "CANCELLED",
      "title" => "Closed child", "closed" => "2026-07-02" },
    { "type" => "task", "id" => "accc0004", "parent" => "accc0003", "state" => "DONE",
      "title" => "Closed grandchild", "closed" => "2026-07-03" },
    { "type" => "task", "id" => "accc0005", "parent" => "accc0001", "state" => "NEXT",
      "title" => "Still live" },
  ].freeze

  def with_archive_tree_store(records = ARCHIVE_TREE_RECORDS)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, dump_fixture(records))
      yield Tasks::Store.new(org: org, archive: archive), org, archive
    end
  end

  def test_archive_preview_counts_roots_and_descendants_without_writing
    with_archive_tree_store do |store, org, archive|
      before = File.read(org)
      preview = store.archive_preview

      assert_equal 1, preview.roots
      assert_equal 2, preview.descendants
      assert_equal 3, preview.total
      refute preview.blocked?
      assert_equal before, File.read(org)
      refute File.exist?(archive)
    end
  end

  def test_archive_nested_subtree_preserves_dfs_structure_and_undo
    with_archive_tree_store do |store, org, archive|
      before = File.read(org)

      assert_equal 1, store.archive_swept!
      root = record_for(archive, title: "Closed project")
      child = record_for(archive, title: "Closed child")
      grandchild = record_for(archive, title: "Closed grandchild")
      refute root.key?("parent")
      assert_equal root["id"], child["parent"]
      assert_equal child["id"], grandchild["parent"]
      assert Tasks::Check.check(org).ok?
      assert Tasks::Check.check(archive).ok?

      assert_equal [:ok, "archive sweep"], store.undo!
      assert_equal before, File.read(org)
      refute File.exist?(archive)
      assert Tasks::Check.check(org).ok?

      assert_equal [:ok, "archive sweep"], store.redo!
      assert_nil record_for(org, title: "Closed project")
      assert record_for(archive, title: "Closed grandchild")
      assert Tasks::Check.check(org).ok?
      assert Tasks::Check.check(archive).ok?
    end
  end

  def test_archive_refuses_closed_root_with_open_descendant
    records = ARCHIVE_TREE_RECORDS.map(&:dup)
    records.find { |r| r["id"] == "accc0004" }["state"] = "NEXT"
    records.find { |r| r["id"] == "accc0004" }.delete("closed")

    with_archive_tree_store(records) do |store, org, archive|
      before = File.read(org)
      result = store.archive_swept!

      assert_instance_of Tasks::Store::ArchiveRefusal, result
      assert_equal :open_descendants, result.reason
      assert_equal 1, result.preview.blocked_roots
      assert_equal 1, result.preview.open_descendants
      assert_equal ["Closed grandchild"], result.preview.blocks.first.open_titles
      assert_equal before, File.read(org), "the entire blocked subtree remains live"
      refute File.exist?(archive)
      assert_equal [:empty], store.undo!, "a refusal does not consume history"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_archive_refuses_complete_id_overlap_when_canonical_content_differs
    with_archive_tree_store do |store, org, archive|
      before = File.read(org)
      archived = ARCHIVE_TREE_RECORDS[2..4].map(&:dup)
      archived.first.delete("parent")
      archived.first["archived"] = Date.today.iso8601
      archived.first["title"] = "Stale project title"
      File.write(archive, dump_fixture([{ "type" => "meta", "version" => 1 }] + archived))

      result = store.archive_swept!

      assert_instance_of Tasks::Store::ArchiveRefusal, result
      assert_equal :archive_conflict, result.reason
      assert_includes result.details, "accc0002"
      assert_equal before, File.read(org), "newer live content is never replaced by ID alone"
      assert_equal "Stale project title", record_for(archive, title: "Stale project title")["title"]
      assert Tasks::Check.check(org).ok?
      assert Tasks::Check.check(archive).ok?
    end
  end

  def test_archive_refuses_partial_prior_archive_without_deleting_live_subtree
    with_archive_tree_store do |store, org, archive|
      before = File.read(org)
      root = ARCHIVE_TREE_RECORDS[2].dup
      root.delete("parent")
      root["archived"] = Date.today.iso8601
      File.write(archive, dump_fixture([{ "type" => "meta", "version" => 1 }, root]))

      result = store.archive_swept!

      assert_instance_of Tasks::Store::ArchiveRefusal, result
      assert_equal :archive_conflict, result.reason
      assert_includes result.details, "accc0003"
      assert_includes result.details, "accc0004"
      assert_equal before, File.read(org)
      assert_nil record_for(archive, title: "Closed child")
      assert Tasks::Check.check(org).ok?
      assert Tasks::Check.check(archive).ok?
    end
  end

  def test_archive_expected_preview_is_validated_atomically_by_candidate_fingerprint
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "accc2001", "title" => "Projects" },
      { "type" => "task", "id" => "accc2002", "parent" => "accc2001", "state" => "DONE",
        "title" => "Candidate A", "closed" => "2026-07-09" },
      { "type" => "task", "id" => "accc2003", "parent" => "accc2001", "state" => "NEXT",
        "title" => "Candidate B" },
    ]
    with_archive_tree_store(records) do |store, org, archive|
      expected = store.archive_preview
      changed = Tasks::Format.parse(File.read(org, encoding: "UTF-8")).records
      candidate_a = changed.find { |record| record["id"] == "accc2002" }
      candidate_a["state"] = "NEXT"
      candidate_a.delete("closed")
      candidate_b = changed.find { |record| record["id"] == "accc2003" }
      candidate_b["state"] = "DONE"
      candidate_b["closed"] = "2026-07-10"
      File.write(org, dump_fixture(changed))

      current = store.archive_preview
      assert_equal expected.roots, current.roots
      refute_equal expected.candidate_ids, current.candidate_ids
      refute_equal expected.fingerprint, current.fingerprint

      result = store.archive_swept!(expected_preview: expected)
      assert_instance_of Tasks::Store::ArchiveRefusal, result
      assert_equal :preview_changed, result.reason
      assert record_for(org, title: "Candidate A")
      assert record_for(org, title: "Candidate B")
      refute File.exist?(archive)
    end
  end

  def test_archive_interruption_after_archive_write_is_retry_safe_and_idempotent
    with_archive_tree_store do |store, org, archive|
      original_write = store.method(:write_records)
      injected = false
      failing_write = lambda do |path, records|
        if path == org && !injected
          injected = true
          raise "injected interruption before live commit"
        end
        original_write.call(path, records)
      end

      error = assert_raises(RuntimeError) do
        store.stub(:write_records, failing_write) { store.archive_swept! }
      end
      assert_match(/injected interruption/, error.message)
      assert record_for(org, title: "Closed project"), "live copy survives the interruption"
      assert record_for(archive, title: "Closed project"), "archive copy was durable first"
      assert Tasks::Check.check(org).ok?
      assert Tasks::Check.check(archive).ok?

      assert_equal 1, store.archive_swept!, "retry finishes the interrupted sweep"
      assert_nil record_for(org, title: "Closed project")
      archived = Tasks::Format.parse(File.read(archive, encoding: "UTF-8")).records
      moved_ids = %w[accc0002 accc0003 accc0004]
      assert_equal moved_ids.sort, archived.filter_map { |r| r["id"] if moved_ids.include?(r["id"]) }.sort
      assert_equal 0, store.archive_swept!, "a completed retry is idempotent"
      assert_equal moved_ids.length,
                   Tasks::Format.parse(File.read(archive, encoding: "UTF-8")).records.count { |r| moved_ids.include?(r["id"]) }
      assert Tasks::Check.check(org).ok?
      assert Tasks::Check.check(archive).ok?
    end
  end

  def test_set_priority_replaces_existing_cookie
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      assert store.set_priority!(flight, "B")
      assert_equal "B", record_for(org, title: "Book flight in Concur")["priority"]
      assert_equal "B", find_item(store, "Book flight").priority
    end
  end

  def test_set_priority_adds_cookie_to_unprioritized_item
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      assert store.set_priority!(plants, "C")
      assert_equal "C", record_for(org, title: "Water the plants")["priority"]
    end
  end

  def test_set_priority_nil_removes_cookie
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      assert store.set_priority!(flight, nil)
      refute record_for(org, title: "Book flight in Concur").key?("priority")
      assert_nil find_item(store, "Book flight").priority
    end
  end

  def test_set_priority_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Book flight").dup
      stale.id = nil
      stale.line = 1
      refute store.set_priority!(stale, "B")
      assert_equal "A", record_for(org, title: "Book flight in Concur")["priority"]
    end
  end

  def test_undo_with_empty_history
    with_store do |store, _o, _a|
      assert_equal [:empty], store.undo!
      assert_equal [:empty], store.redo!
    end
  end

  def test_undo_and_redo_roundtrip_a_complete
    with_store do |store, org, _a|
      before = File.read(org)
      store.complete!(find_item(store, "Book flight"))
      after = File.read(org)

      kind, label = store.undo!
      assert_equal :ok, kind
      assert_includes label, "complete: Book flight"
      assert_equal before, File.read(org)
      assert_equal "NEXT", find_item(store, "Book flight").state

      kind, = store.redo!
      assert_equal :ok, kind
      assert_equal after, File.read(org)
      assert_equal "DONE", find_item(store, "Book flight").state
    end
  end

  def test_undo_stacks_multiple_mutations_in_order
    with_store do |store, org, _a|
      original = File.read(org)
      store.set_priority!(find_item(store, "Book flight"), "B")
      store.reschedule!(find_item(store, "Book flight"), Date.new(2026, 7, 20))
      store.undo! # reschedule
      assert_equal "2026-07-02", record_for(org, title: "Book flight in Concur")["deadline"]
      assert_equal "B", record_for(org, title: "Book flight in Concur")["priority"]
      store.undo! # priority
      assert_equal original, File.read(org)
    end
  end

  def test_new_mutation_clears_redo
    with_store do |store, _o, _a|
      store.set_priority!(find_item(store, "Book flight"), "B")
      store.undo!
      store.set_priority!(find_item(store, "Book flight"), "C")
      assert_equal [:empty], store.redo!
    end
  end

  def test_undo_refuses_after_external_edit
    with_store do |store, org, _a|
      store.complete!(find_item(store, "Book flight"))
      File.write(org, File.read(org) +
        dump_fixture([{ "type" => "task", "id" => "bbbb0002", "parent" => FIX[:work],
                        "state" => "TODO", "title" => "claude added this" }]))
      kind, label = store.undo!
      assert_equal :conflict, kind
      assert_includes label, "Book flight"
      assert_match(/claude added this/, File.read(org), "conflict must not clobber the file")
    end
  end

  def test_undo_archive_sweep_restores_both_files
    with_store do |store, org, archive|
      org_before = File.read(org)
      refute File.exist?(archive)
      store.archive_swept!
      assert File.exist?(archive)

      kind, = store.undo!
      assert_equal :ok, kind
      assert_equal org_before, File.read(org)
      refute File.exist?(archive), "archive file created by the sweep is removed"
      assert_equal 7, store.items.size
    end
  end

  def test_failed_mutation_records_no_history
    with_store do |store, _o, _a|
      stale = find_item(store, "Book flight").dup
      stale.id = nil
      stale.line = 1
      refute store.complete!(stale)
      assert_equal [:empty], store.undo!
    end
  end

  def test_undo_history_is_capped
    with_store do |store, _o, _a|
      55.times do |i|
        store.set_priority!(find_item(store, "Book flight"), %w[A B C][i % 3])
      end
      undone = 0
      undone += 1 while store.undo!.first == :ok
      assert_equal Tui::Store::UNDO_LIMIT, undone
    end
  end

  def test_archive_with_nothing_to_do
    with_store do |store, _org, _archive|
      assert_equal 1, store.archive_swept!
      assert_equal 0, store.archive_swept!, "second sweep has nothing to move"
      assert_equal 1, store.archive_items.size
    end
  end

  # -- set_deferred! (backs `defer`/`activate`) -------------------------------

  def test_set_deferred_adds_defer_tag_and_keeps_state
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      assert store.set_deferred!(plants, true)
      assert_equal %w[@home defer], record_for(org, title: "Water the plants")["tags"]
      fresh = find_item(store, "Water the plants")
      assert fresh.deferred?
      assert_equal "NEXT", fresh.state # state is untouched
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_deferred_preserves_existing_tags
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      assert store.set_deferred!(flight, true)
      fresh = find_item(store, "Book flight")
      assert fresh.deferred?
      assert_includes fresh.tags, "@computer"
      assert_includes fresh.tags, "important"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_deferred_false_removes_defer_tag
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      store.set_deferred!(plants, true)
      assert store.set_deferred!(find_item(store, "Water the plants"), false)
      assert_equal %w[@home], record_for(org, title: "Water the plants")["tags"]
      refute find_item(store, "Water the plants").deferred?
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_deferred_is_undoable
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      store.set_deferred!(plants, true)
      assert find_item(store, "Water the plants").deferred?
      assert_equal :ok, store.undo!.first
      refute find_item(store, "Water the plants").deferred?
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_deferred_rejects_stale_line_numbers
    with_store do |store, _org, _a|
      stale = find_item(store, "Book flight").dup
      stale.id = nil
      stale.line = 1
      refute store.set_deferred!(stale, true)
    end
  end

  def test_completing_a_deferred_task_strips_the_defer_tag
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      store.set_deferred!(plants, true)
      assert store.complete!(find_item(store, "Water the plants"))
      done = find_item(store, "Water the plants")
      assert_equal "DONE", done.state
      refute done.deferred?, "a completed task must not keep the someday/maybe marker"
      assert_equal %w[@home], record_for(org, title: "Water the plants")["tags"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cancelling_a_deferred_task_via_set_state_strips_the_defer_tag
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      store.set_deferred!(plants, true)
      assert store.set_state!(find_item(store, "Water the plants"), "CANCELLED")
      cancelled = find_item(store, "Water the plants")
      assert_equal "CANCELLED", cancelled.state
      refute cancelled.deferred?
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- recurrence ------------------------------------------------------------

  RECUR_RECORDS = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "dddd0001", "title" => "Work" },
    { "type" => "task", "id" => "dddd0002", "parent" => "dddd0001", "state" => "NEXT",
      "title" => "Pay rent", "tags" => %w[@home], "deadline" => "2026-08-01", "recur" => "+1m" },
    { "type" => "task", "id" => "dddd0003", "parent" => "dddd0001", "state" => "TODO",
      "title" => "Weekly review", "tags" => %w[@computer], "scheduled" => "2026-06-20", "recur" => ".+1w" },
    { "type" => "task", "id" => "dddd0004", "parent" => "dddd0001", "state" => "NEXT",
      "title" => "Plain dated task", "deadline" => "2026-07-02" },
    { "type" => "task", "id" => "dddd0005", "parent" => "dddd0001", "state" => "TODO",
      "title" => "No date task" },
  ].freeze

  def with_recur_store
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, Tasks::Format.dump(RECUR_RECORDS))
      yield Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl")), org
    end
  end

  def test_parse_reads_repeater_cookie
    with_recur_store do |store, _org|
      assert_equal "+1m", find_item(store, "Pay rent").recur
      assert find_item(store, "Pay rent").recurring?
      assert_equal ".+1w", find_item(store, "Weekly review").recur
      assert_nil find_item(store, "Plain dated task").recur
      refute find_item(store, "Plain dated task").recurring?
    end
  end

  def test_set_recur_attaches_cookie_preserving_date
    with_recur_store do |store, org|
      assert store.set_recur!(find_item(store, "Plain dated task"), ".+2w")
      rec = record_for(org, title: "Plain dated task")
      assert_equal "2026-07-02", rec["deadline"]
      assert_equal ".+2w", rec["recur"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_recur_off_removes_cookie
    with_recur_store do |store, org|
      assert store.set_recur!(find_item(store, "Pay rent"), :off)
      rec = record_for(org, title: "Pay rent")
      assert_equal "2026-08-01", rec["deadline"]
      refute rec.key?("recur")
      assert_nil find_item(store, "Pay rent").recur
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_recur_rides_scheduled_when_no_deadline
    with_recur_store do |store, org|
      review = find_item(store, "Weekly review")
      assert store.set_recur!(review, "+3d")
      rec = record_for(org, title: "Weekly review")
      assert_equal "2026-06-20", rec["scheduled"]
      assert_equal "+3d", rec["recur"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_recur_on_undated_task_returns_false
    with_recur_store do |store, org|
      before = File.read(org)
      refute store.set_recur!(find_item(store, "No date task"), ".+1w")
      assert_equal before, File.read(org)
    end
  end

  def test_set_recur_rejects_stale_line
    with_recur_store do |store, _org|
      stale = find_item(store, "Plain dated task").dup
      stale.id = nil
      stale.line = 1
      refute store.set_recur!(stale, ".+1w")
    end
  end

  def test_complete_recurring_rolls_forward_and_stays_open
    with_recur_store do |store, org|
      rent = find_item(store, "Pay rent")
      assert store.complete!(rent)
      fresh = find_item(store, "Pay rent")
      assert_equal "NEXT", fresh.state, "recurring task stays open"
      assert_equal Date.new(2026, 9, 1), fresh.deadline # +1m fixed hop from 2026-08-01
      assert_equal "+1m", fresh.recur, "cookie is retained"
      rec = record_for(org, title: "Pay rent")
      refute rec.key?("closed"), "recurring completion adds no closed date"
      assert_match(/- Did \[#{Date.today}\]/, rec["body"], "logs the occurrence")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_complete_recurring_from_completion_uses_today
    with_recur_store do |store, org|
      review = find_item(store, "Weekly review") # .+1w
      assert store.complete!(review)
      assert_equal Date.today + 7, find_item(store, "Weekly review").scheduled
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_done_via_set_state_also_rolls_recurring
    with_recur_store do |store, org|
      rent = find_item(store, "Pay rent")
      assert store.set_state!(rent, "DONE")
      assert_equal "NEXT", find_item(store, "Pay rent").state
      assert_equal Date.new(2026, 9, 1), find_item(store, "Pay rent").deadline
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cancel_recurring_truly_closes
    with_recur_store do |store, org|
      rent = find_item(store, "Pay rent")
      assert store.set_state!(rent, "CANCELLED")
      assert_equal "CANCELLED", find_item(store, "Pay rent").state
      assert_equal Date.today.iso8601, record_for(org, title: "Pay rent")["closed"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_complete_non_recurring_still_closes
    with_recur_store do |store, org|
      plain = find_item(store, "Plain dated task")
      assert store.complete!(plain)
      assert_equal "DONE", find_item(store, "Plain dated task").state
      assert_equal Date.today.iso8601, record_for(org, title: "Plain dated task")["closed"]
    end
  end

  def test_complete_recurring_is_undoable
    with_recur_store do |store, org|
      before = File.read(org)
      store.complete!(find_item(store, "Pay rent"))
      refute_equal before, File.read(org)
      store.undo!
      assert_equal before, File.read(org)
      assert_equal "+1m", find_item(store, "Pay rent").recur
    end
  end

  # A recurring parent's roll-forward touches only its own record — a child
  # subtask's date must not move (each record's dates are its own).
  def test_complete_recurring_parent_does_not_touch_child_stamp
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture([
        { "type" => "meta", "version" => 1 },
        { "type" => "section", "id" => "eeee0001", "title" => "W" },
        { "type" => "task", "id" => "eeee0002", "parent" => "eeee0001", "state" => "NEXT",
          "title" => "Parent", "scheduled" => "2026-07-01", "recur" => "+1w" },
        { "type" => "task", "id" => "eeee0003", "parent" => "eeee0002", "state" => "NEXT",
          "title" => "Child", "scheduled" => "2026-07-02", "recur" => "+1d" },
      ]))
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      assert store.complete!(store.items.find { |i| i.title == "Parent" })
      assert_equal "2026-07-08", record_for(org, title: "Parent")["scheduled"], "parent rolled"
      # A recurring parent rolls forward and must NOT cascade — the child keeps
      # its own open state, date, and recur cookie.
      child = record_for(org, title: "Child")
      assert_equal "NEXT", child["state"], "recurring parent does not cascade"
      assert_equal "2026-07-02", child["scheduled"], "child untouched"
      assert_equal "+1d", child["recur"], "child recur cookie survives"
      refute child.key?("closed"), "child stays open"
      assert_match(/- Did \[#{Date.today}\]/, record_for(org, title: "Parent")["body"])
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_recur_on_parent_does_not_attach_to_child
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture([
        { "type" => "meta", "version" => 1 },
        { "type" => "section", "id" => "ffff0001", "title" => "W" },
        { "type" => "task", "id" => "ffff0002", "parent" => "ffff0001", "state" => "NEXT",
          "title" => "Parent", "scheduled" => "2026-07-01" },
        { "type" => "task", "id" => "ffff0003", "parent" => "ffff0002", "state" => "NEXT",
          "title" => "Child", "deadline" => "2026-09-01" },
      ]))
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      assert store.set_recur!(store.items.find { |i| i.title == "Parent" }, "+1w")
      assert_equal "+1w", record_for(org, title: "Parent")["recur"]
      child = record_for(org, title: "Child")
      refute child.key?("recur"), "child gains no recurrence"
      assert_equal "2026-09-01", child["deadline"]
    end
  end

  def test_set_date_preserves_repeater_cookie
    with_recur_store do |store, org|
      rent = find_item(store, "Pay rent") # +1m
      assert store.set_date!(rent, Date.new(2026, 12, 25), kind: :deadline)
      rec = record_for(org, title: "Pay rent")
      assert_equal "2026-12-25", rec["deadline"]
      assert_equal "+1m", rec["recur"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_reschedule_preserves_repeater_cookie
    with_recur_store do |store, org|
      review = find_item(store, "Weekly review") # SCHEDULED .+1w
      assert store.reschedule!(review, Date.new(2026, 7, 10))
      rec = record_for(org, title: "Weekly review")
      assert_equal "2026-07-10", rec["scheduled"]
      assert_equal ".+1w", rec["recur"]
      assert Tasks::Check.check(org).ok?
    end
  end

  # M1: the single `recur` field belongs to the DEADLINE when both dates are
  # present. Only that owning date rolls; the fixed SCHEDULED is left untouched.
  def test_recurring_completion_rolls_only_the_owning_date
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture([
        { "type" => "meta", "version" => 1 },
        { "type" => "section", "id" => "aaaa0001", "title" => "W" },
        { "type" => "task", "id" => "aaaa0002", "parent" => "aaaa0001", "state" => "NEXT",
          "title" => "Both dates", "scheduled" => "2026-07-01", "deadline" => "2026-08-01",
          "recur" => "+1w" },
      ]))
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      assert store.complete!(store.items.find { |i| i.title == "Both dates" })
      rec = record_for(org, title: "Both dates")
      assert_equal "2026-08-08", rec["deadline"], "the owning (deadline) date rolls +1w"
      assert_equal "2026-07-01", rec["scheduled"], "the fixed scheduled date is untouched"
      assert_equal "NEXT", rec["state"], "recurring task stays open"
      assert Tasks::Check.check(org).ok?
    end
  end

  # m1: a deferred recurring task, once completed, drops the defer marker (like
  # complete_impl) while still rolling its date.
  def test_recurring_completion_strips_defer_tag
    with_recur_store do |store, org|
      store.set_deferred!(find_item(store, "Weekly review"), true)
      assert find_item(store, "Weekly review").deferred?
      assert store.complete!(find_item(store, "Weekly review")) # scheduled .+1w
      fresh = find_item(store, "Weekly review")
      refute fresh.deferred?, "defer tag dropped on recurring completion"
      assert_equal Date.today + 7, fresh.scheduled, "date still rolled"
      assert Tasks::Check.check(org).ok?
    end
  end

  # m4: an invalid recur cookie (e.g. a hand-edited "++0d") is not a repeater, so
  # completion routes through the normal close instead of Recur.next_date (which
  # would raise ArgumentError). Check still reports the bad cookie — which, with
  # the post-write validation gate, means the close rolls back; crucially nothing
  # raises (the old crash is gone).
  def test_zero_count_cookie_is_not_a_repeater
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture([
        { "type" => "meta", "version" => 1 },
        { "type" => "section", "id" => "aaaa0001", "title" => "W" },
        { "type" => "task", "id" => "aaaa0002", "parent" => "aaaa0001", "state" => "NEXT",
          "title" => "Rent", "deadline" => "2020-01-01", "recur" => "++0d" },
      ]))
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      rent = store.items.find { |i| i.title == "Rent" }
      refute rent.recurring?, "++0d must not register as recurrence (was an ArgumentError)"
      before = File.read(org)
      refute store.complete!(rent), "post-write gate rolls back the still-invalid cookie"
      assert_equal before, File.read(org), "file unchanged — but no raise"
      assert_match(/invalid recur cookie/, Tasks::Check.check(org).errors.map { |_l, m| m }.join)
    end
  end

  # C1: a file containing a non-string (integer) id must not crash mutations on a
  # DIFFERENT, valid task — the write happens, post-write Check flags the bad id,
  # and the whole thing rolls back cleanly (false, file unchanged, no raise).
  def test_mutation_rolls_back_cleanly_in_a_file_with_a_non_string_id
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture([
        { "type" => "meta", "version" => 1 },
        { "type" => "section", "id" => "aaaa0001", "title" => "W" },
        { "type" => "task", "id" => "aaaa0002", "parent" => "aaaa0001", "state" => "NEXT",
          "title" => "Valid task" },
        { "type" => "task", "id" => 12345678, "parent" => "aaaa0001", "state" => "NEXT",
          "title" => "Bad id task" },
      ]))
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      assert_equal 2, store.items.size, "readers survive the integer id"
      before = File.read(org)
      refute store.complete!(store.items.find { |i| i.title == "Valid task" })
      assert_equal before, File.read(org), "rolled back byte-for-byte"
    end
  end

  # M2: an item that HAS an id no longer present in the file must fail to locate
  # — never fall back to whatever same-title record now sits at its line.
  def test_present_but_missing_id_does_not_fall_back_to_line_title
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight") # holds a real id
      records = Tasks::Format.parse(File.read(org)).records
      # Same title, same line — but the held id has vanished (record re-ided).
      records.find { |r| r["title"] == "Book flight in Concur" }["id"] = "ffff9999"
      File.write(org, Tasks::Format.dump(records))
      before = File.read(org)
      refute store.retitle!(flight, "hijacked"), "id-bearing item must not match by line+title"
      assert_equal before, File.read(org), "the record at that line is untouched"
    end
  end

  # M3: undo/redo restores are gated by Check. Repairing a pre-existing invalid
  # field succeeds, but undoing back to the invalid state is refused.
  def test_undo_refuses_to_restore_an_invalid_prior_state
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture([
        { "type" => "meta", "version" => 1 },
        { "type" => "section", "id" => "aaaa0001", "title" => "W" },
        { "type" => "task", "id" => "aaaa0002", "parent" => "aaaa0001", "state" => "TODO",
          "title" => "Fix me", "scheduled" => "not-a-date" },
      ]))
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      refute Tasks::Check.check(org).ok?, "seed is invalid"
      assert store.set_date!(store.items.find { |i| i.title == "Fix me" },
                             Date.new(2026, 8, 1), kind: :scheduled)
      assert Tasks::Check.check(org).ok?, "the mutation repaired the bad date"
      repaired = File.read(org)
      assert_equal :conflict, store.undo!.first, "undo to the invalid state is refused"
      assert_equal repaired, File.read(org), "file stays valid after the refused undo"
    end
  end

  # M5: a string `tags` value coerces to [] in readers (no crash) while Check
  # still reports it.
  def test_string_tags_does_not_crash_readers
    with_store do |store, org, _a|
      records = Tasks::Format.parse(File.read(org)).records
      records.find { |r| r["title"] == "Book flight in Concur" }["tags"] = "@x"
      File.write(org, Tasks::Format.dump(records))
      store.reload!
      flight = find_item(store, "Book flight")
      assert_equal [], flight.tags, "coerced to an empty array"
      assert_kind_of String, store.headline(flight) # doesn't raise
      refute Tasks::Check.check(org).ok?
      assert_match(/tags must be an array/, Tasks::Check.check(org).errors.map { |_l, m| m }.join)
    end
  end

  # -- cascading completion ----------------------------------------------------
  #
  # A nested project: the root "Project" has open descendants at two depths, a
  # pre-existing DONE and CANCELLED child (must keep their own closed dates), a
  # recurring child (cascade retires it, does NOT roll it), and a sibling
  # subtree that must stay untouched.
  CASCADE_RECORDS = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "cccc0001", "title" => "Work" },
    { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001", "state" => "TODO",
      "title" => "Project" },
    { "type" => "task", "id" => "cccc0003", "parent" => "cccc0002", "state" => "TODO",
      "title" => "Task A" },
    { "type" => "task", "id" => "cccc0004", "parent" => "cccc0003", "state" => "NEXT",
      "title" => "Subtask A1" },
    { "type" => "task", "id" => "cccc0005", "parent" => "cccc0002", "state" => "WAITING",
      "title" => "Task B" },
    { "type" => "task", "id" => "cccc0006", "parent" => "cccc0002", "state" => "INBOX",
      "title" => "Loose idea" },
    { "type" => "task", "id" => "cccc0007", "parent" => "cccc0002", "state" => "DONE",
      "title" => "Already done sub", "closed" => "2026-06-01" },
    { "type" => "task", "id" => "cccc0008", "parent" => "cccc0002", "state" => "CANCELLED",
      "title" => "Dropped sub", "closed" => "2026-06-05" },
    { "type" => "task", "id" => "cccc0009", "parent" => "cccc0002", "state" => "NEXT",
      "title" => "Recurring sub", "scheduled" => "2026-07-01", "recur" => ".+1w",
      "tags" => %w[defer] },
    { "type" => "task", "id" => "cccc000a", "parent" => "cccc0001", "state" => "TODO",
      "title" => "Sibling" },
    { "type" => "task", "id" => "cccc000b", "parent" => "cccc000a", "state" => "NEXT",
      "title" => "Sibling child" },
  ].freeze

  def with_cascade_store
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, Tasks::Format.dump(CASCADE_RECORDS))
      yield Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl")), org
    end
  end

  def test_complete_cascades_open_descendants_at_all_depths
    with_cascade_store do |store, org|
      lines = store.complete!(store.items.find { |i| i.title == "Project" })
      assert_kind_of Array, lines
      today = Date.today.iso8601

      %w[Project Task\ A Subtask\ A1 Task\ B Loose\ idea].each do |title|
        title = title.tr("\\", " ")
        rec = record_for(org, title: title)
        assert_equal "DONE", rec["state"], "#{title} closed"
        assert_equal today, rec["closed"], "#{title} closed today"
      end

      # Pre-existing DONE/CANCELLED descendants keep their own closed dates.
      assert_equal "2026-06-01", record_for(org, title: "Already done sub")["closed"]
      done = record_for(org, title: "Dropped sub")
      assert_equal "CANCELLED", done["state"]
      assert_equal "2026-06-05", done["closed"]

      # Sibling subtree untouched.
      assert_equal "TODO", record_for(org, title: "Sibling")["state"]
      assert_equal "NEXT", record_for(org, title: "Sibling child")["state"]

      # Returns root + every touched descendant line (root first, file order).
      assert_equal lines, lines.sort
      assert_equal 6, lines.size, "root + 5 open descendants"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_complete_cascade_retires_recurring_descendant
    with_cascade_store do |store, org|
      store.complete!(store.items.find { |i| i.title == "Project" })
      rec = record_for(org, title: "Recurring sub")
      assert_equal "DONE", rec["state"]
      assert_equal Date.today.iso8601, rec["closed"]
      assert_equal "2026-07-01", rec["scheduled"], "date NOT advanced — retired, not rolled"
      refute rec.key?("recur"), "recur cookie retired"
      refute rec.fetch("tags", []).include?("defer"), "defer tag dropped"
      refute_match(/- Did/, rec["body"].to_s, "no occurrence log — the sub is retired")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_complete_cascade_is_one_undo_step_restoring_bytes
    with_cascade_store do |store, org|
      before = File.read(org)
      store.complete!(store.items.find { |i| i.title == "Project" })
      refute_equal before, File.read(org)
      assert_equal [:ok, "complete: Project"], store.undo!
      assert_equal before, File.read(org), "one undo restores the subtree byte-identically"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_state_done_cascades
    with_cascade_store do |store, org|
      lines = store.set_state!(store.items.find { |i| i.title == "Project" }, "DONE")
      assert_kind_of Array, lines
      assert_equal 6, lines.size
      assert_equal "DONE", record_for(org, title: "Task A")["state"]
      assert_equal "DONE", record_for(org, title: "Subtask A1")["state"]
      assert_equal Date.today.iso8601, record_for(org, title: "Task B")["closed"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_state_cancelled_does_not_cascade
    with_cascade_store do |store, org|
      lines = store.set_state!(store.items.find { |i| i.title == "Project" }, "CANCELLED")
      assert_equal 1, lines.size, "only the root touched"
      assert_equal "CANCELLED", record_for(org, title: "Project")["state"]
      # Open descendants keep their own open states.
      assert_equal "TODO", record_for(org, title: "Task A")["state"]
      assert_equal "NEXT", record_for(org, title: "Subtask A1")["state"]
      assert_equal "WAITING", record_for(org, title: "Task B")["state"]
      assert_equal "INBOX", record_for(org, title: "Loose idea")["state"]
      refute record_for(org, title: "Task A").key?("closed")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_reclose_of_done_parent_does_not_recascade
    # A DONE parent with an out-of-band open child: re-closing the parent
    # (old_state already DONE) is not a transition INTO DONE, so no cascade.
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture([
        { "type" => "meta", "version" => 1 },
        { "type" => "section", "id" => "99990001", "title" => "W" },
        { "type" => "task", "id" => "99990002", "parent" => "99990001", "state" => "DONE",
          "title" => "Closed parent", "closed" => "2026-06-01" },
        { "type" => "task", "id" => "99990003", "parent" => "99990002", "state" => "TODO",
          "title" => "Stray open child" },
      ]))
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      parent = store.items.find { |i| i.title == "Closed parent" }
      lines = store.set_state!(parent, "DONE")
      assert_equal 1, lines.size, "no cascade off an already-DONE parent"
      assert_equal "TODO", record_for(org, title: "Stray open child")["state"]
      assert_equal "2026-06-01", record_for(org, title: "Closed parent")["closed"], "closed preserved"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cascade_works_on_a_six_deep_file
    # Depth cap is a mutation-only concern (Stage 3); cascade itself walks any
    # depth. A 6-level chain closes end to end from the root.
    recs = [{ "type" => "meta", "version" => 1 },
            { "type" => "section", "id" => "8888aaaa", "title" => "Deep" }]
    prev = "8888aaaa"
    6.times do |n|
      id = format("8888%04d", n)
      recs << { "type" => "task", "id" => id, "parent" => prev, "state" => "TODO",
                "title" => "Level #{n}" }
      prev = id
    end
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture(recs))
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      lines = store.complete!(store.items.find { |i| i.title == "Level 0" })
      assert_equal 6, lines.size, "root + 5 descendants"
      6.times { |n| assert_equal "DONE", record_for(org, title: "Level #{n}")["state"] }
      assert Tasks::Check.check(org).ok?
    end
  end

  # A WAITING parent transitioning to DONE cascades to its open child — covers
  # the WAITING → DONE edge the other cascade tests (TODO root) don't hit.
  def test_set_state_waiting_parent_to_done_cascades
    with_cascade_store do |store, org|
      project = store.items.find { |i| i.title == "Project" }
      store.set_state!(project, "WAITING")
      waiting = store.items.find { |i| i.title == "Project" }
      lines = store.set_state!(waiting, "DONE")
      assert_kind_of Array, lines
      assert_equal "DONE", record_for(org, title: "Project")["state"]
      assert_equal "DONE", record_for(org, title: "Task A")["state"], "open child cascaded"
      assert_equal Date.today.iso8601, record_for(org, title: "Task B")["closed"]
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- nesting: capture --under, move --under/--top ---------------------------
  #
  # A tree with two depth levels plus a two-node subtree (Solo/Solo child, a
  # height-2 anchor for the depth-math tests) and a second section (Home) to
  # unnest into.
  NEST_RECORDS = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "dddd0001", "title" => "Work" },
    { "type" => "task", "id" => "dddd0002", "parent" => "dddd0001", "state" => "TODO",
      "title" => "Project" },
    { "type" => "task", "id" => "dddd0003", "parent" => "dddd0002", "state" => "TODO",
      "title" => "Phase 1" },
    { "type" => "task", "id" => "dddd0004", "parent" => "dddd0003", "state" => "NEXT",
      "title" => "Step A" },
    { "type" => "task", "id" => "dddd0005", "parent" => "dddd0002", "state" => "TODO",
      "title" => "Phase 2" },
    { "type" => "task", "id" => "dddd0006", "parent" => "dddd0001", "state" => "TODO",
      "title" => "Solo" },
    { "type" => "task", "id" => "dddd0007", "parent" => "dddd0006", "state" => "NEXT",
      "title" => "Solo child" },
    { "type" => "section", "id" => "dddd0008", "title" => "Home" },
    { "type" => "task", "id" => "dddd0009", "parent" => "dddd0008", "state" => "TODO",
      "title" => "Chore" },
  ].freeze

  def with_nest_store(max_depth: 4)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, Tasks::Format.dump(NEST_RECORDS))
      yield Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"),
                             max_depth: max_depth), org
    end
  end

  # Order of the task titles as they appear in the file (DFS pre-order).
  def title_order(org)
    Tasks::Format.parse(File.read(org, encoding: "UTF-8")).records
                 .select { |r| r["type"] == "task" }.map { |r| r["title"] }
  end

  def test_capture_under_appends_as_last_child
    with_nest_store do |store, org|
      project = store.items.find { |i| i.title == "Project" }
      line = store.capture!("New phase", under: project)
      assert_kind_of Integer, line
      rec = record_for(org, title: "New phase")
      assert_equal "dddd0002", rec["parent"], "parented to Project"
      # Last child: lands after Project's existing descendants (Phase 1, Step A,
      # Phase 2), before the Solo subtree.
      order = title_order(org)
      assert_equal %w[Project Phase\ 1 Step\ A Phase\ 2 New\ phase Solo Solo\ child Chore]
        .map { |t| t.tr("\\", " ") }, order
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_capture_under_at_cap_returns_too_deep_and_leaves_file_unchanged
    with_nest_store(max_depth: 3) do |store, org|
      before = File.read(org)
      # Step A is at depth 3; a child would be depth 4 > cap 3.
      step = store.items.find { |i| i.title == "Step A" }
      assert_equal :too_deep, store.capture!("Too deep", under: step)
      assert_equal before, File.read(org), "no write on refusal"
    end
  end

  def test_move_under_moves_whole_subtree_contiguously
    with_nest_store do |store, org|
      # Move Phase 1 (with Step A) under Solo — grandchild rides along, in order.
      phase1 = store.items.find { |i| i.title == "Phase 1" }
      solo = store.items.find { |i| i.title == "Solo" }
      line = store.move_under!(phase1, solo)
      assert_kind_of Integer, line
      assert_equal "dddd0006", record_for(org, title: "Phase 1")["parent"], "reparented to Solo"
      assert_equal "dddd0003", record_for(org, title: "Step A")["parent"], "child keeps its parent"
      order = title_order(org)
      # Phase 1 + Step A land contiguously as Solo's last children.
      assert_equal %w[Project Phase\ 2 Solo Solo\ child Phase\ 1 Step\ A Chore]
        .map { |t| t.tr("\\", " ") }, order
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_move_under_depth_math
    # max_depth 4: Solo's subtree has height 2. Under Step A (depth 3): 3+2=5 →
    # too deep. Under Phase 2 (depth 2): 2+2=4 → ok.
    with_nest_store(max_depth: 4) do |store, org|
      solo = store.items.find { |i| i.title == "Solo" }
      step = store.items.find { |i| i.title == "Step A" }
      before = File.read(org)
      assert_equal :too_deep, store.move_under!(solo, step)
      assert_equal before, File.read(org), "refused move writes nothing"

      solo = store.items.find { |i| i.title == "Solo" }
      phase2 = store.items.find { |i| i.title == "Phase 2" }
      assert_kind_of Integer, store.move_under!(solo, phase2)
      assert_equal "dddd0005", record_for(org, title: "Solo")["parent"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_move_under_cycle_guard
    with_nest_store do |store, org|
      before = File.read(org)
      project = store.items.find { |i| i.title == "Project" }
      # Self: a task cannot nest under itself.
      assert_equal :cycle, store.move_under!(project, project)
      # Descendant: Project cannot nest under its own Step A.
      step = store.items.find { |i| i.title == "Step A" }
      assert_equal :cycle, store.move_under!(project, step)
      assert_equal before, File.read(org), "no write on either cycle"
    end
  end

  def test_move_top_reparents_to_nearest_section
    with_nest_store do |store, org|
      step = store.items.find { |i| i.title == "Step A" }
      line = store.move_top!(step)
      assert_kind_of Integer, line
      assert_equal "dddd0001", record_for(org, title: "Step A")["parent"], "unnested to Work"
      # Lands at the end of Work's span (after Solo child, before the Home section).
      order = title_order(org)
      assert_equal %w[Project Phase\ 1 Phase\ 2 Solo Solo\ child Step\ A Chore]
        .map { |t| t.tr("\\", " ") }, order
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_move_top_on_top_level_task_is_a_noop_that_burns_no_undo
    with_nest_store do |store, _org|
      # A prior real mutation to prove the no-op doesn't shadow it in the journal.
      store.set_priority!(store.items.find { |i| i.title == "Solo" }, "A")
      solo = store.items.find { |i| i.title == "Solo" }
      assert_equal 0, store.move_top!(solo), "already top-level → 0"
      # Undo reaches PAST the no-op to the priority change (no slot burned).
      assert_equal [:ok, "priority [#A]: Solo"], store.undo!
    end
  end

  def test_move_top_on_parentless_task_returns_false_without_hanging
    # A task with no parent at all is Check-valid but has no ancestor section.
    # The ancestor walk must stop at the nil parent (not resolve by_id[nil] to
    # the id-less meta record and spin forever holding the lock).
    recs = [{ "type" => "meta", "version" => 1 },
            { "type" => "task", "id" => "8888aaaa", "state" => "TODO", "title" => "Rootless" },
            { "type" => "task", "id" => "8888bbbb", "parent" => "8888aaaa", "state" => "TODO",
              "title" => "Rootless child" }]
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture(recs))
      assert Tasks::Check.check(org).ok?, "parentless task is a valid file"
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      before = File.read(org)
      Timeout.timeout(5) do
        refute store.move_top!(store.items.find { |i| i.title == "Rootless" }),
               "no section to unnest to → false"
        refute store.move_top!(store.items.find { |i| i.title == "Rootless child" }),
               "descendant of a rootless task → false"
      end
      assert_equal before, File.read(org), "no write on the failure path"
    end
  end

  def test_move_over_cap_height_subtree_still_moves_to_a_section
    # Escape hatch: a subtree deeper than the cap is never depth-checked when the
    # destination is a section (move!). A 5-deep chain (cap 4) relocates freely.
    recs = [{ "type" => "meta", "version" => 1 },
            { "type" => "section", "id" => "7777aaaa", "title" => "Deep" },
            { "type" => "section", "id" => "7777bbbb", "title" => "Elsewhere" }]
    prev = "7777aaaa"
    5.times do |n|
      id = format("7777%04d", n)
      recs << { "type" => "task", "id" => id, "parent" => prev, "state" => "TODO",
                "title" => "Level #{n}" }
      prev = id
    end
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture(recs))
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"), max_depth: 4)
      # Level 0 heads a height-5 subtree; moving it to another section succeeds.
      assert_kind_of Integer, store.move!(store.items.find { |i| i.title == "Level 0" }, "Elsewhere")
      assert_equal "7777bbbb", record_for(org, title: "Level 0")["parent"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_move_under_and_top_reject_stale_items
    with_nest_store do |store, org|
      before = File.read(org)
      stale = store.items.find { |i| i.title == "Step A" }.dup
      stale.id = nil
      stale.line = 999
      parent = store.items.find { |i| i.title == "Solo" }
      refute store.move_under!(stale, parent), "stale subject → false"
      refute store.move_top!(stale), "stale subject → false"
      # A live subject with a stale parent ref also fails.
      live = store.items.find { |i| i.title == "Step A" }
      stale_parent = store.items.find { |i| i.title == "Solo" }.dup
      stale_parent.id = nil
      stale_parent.line = 999
      refute store.move_under!(live, stale_parent), "stale parent → false"
      assert_equal before, File.read(org), "no write on any stale path"
    end
  end

  def test_move_under_is_undoable_byte_for_byte
    with_nest_store do |store, org|
      before = File.read(org)
      phase1 = store.items.find { |i| i.title == "Phase 1" }
      solo = store.items.find { |i| i.title == "Solo" }
      store.move_under!(phase1, solo)
      refute_equal before, File.read(org)
      assert_equal [:ok, "nest under Solo: Phase 1"], store.undo!
      assert_equal before, File.read(org), "one undo restores the file exactly"
      assert Tasks::Check.check(org).ok?
    end
  end

  # m9: notes accumulate — the body joins successive notes with "\n" and
  # store.body splits them back into N lines.
  def test_multiple_notes_accumulate_in_body
    with_store do |store, org, _a|
      store.add_note!(find_item(store, "Water the plants"), "first note")
      store.add_note!(find_item(store, "Water the plants"), "second note")
      assert_equal "first note\nsecond note", record_for(org, title: "Water the plants")["body"]
      assert_equal ["first note", "second note"], store.body(find_item(store, "Water the plants"))
      assert Tasks::Check.check(org).ok?
    end
  end
end
