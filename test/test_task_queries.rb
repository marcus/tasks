# frozen_string_literal: true

require_relative "test_helper"
require "tasks/operation_context"
require "tasks/task_queries"

class TestTaskQueries < Minitest::Test
  def with_query_store(records: FIXTURE_RECORDS, archive_records: nil)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, dump_fixture(records))
      File.write(archive, dump_fixture(archive_records)) if archive_records
      store = Tasks::Store.new(org: org, archive: archive)
      yield store
    end
  end

  def queries(store, include_archive: false, today: Date.new(2026, 7, 14))
    Tasks::TaskQueries.new(store.read_snapshot(include_archive: include_archive), today: today)
  end

  def test_filter_parser_preserves_cli_scope_and_filter_composition
    parsed = Tasks::TaskFilter.parse_cli(
      ["--all", "--deferred", "--recurring", "--body", "@computer", "+important", "-A", "/flight", "plans", "--json"]
    )

    assert parsed.json
    filter = parsed.filter
    assert_equal :all, filter.scope
    assert filter.include_archive?
    assert filter.deferred_only
    assert filter.recurring_only
    assert filter.body_search
    assert_equal ["@computer"], filter.contexts
    assert_equal ["important"], filter.tags
    assert_equal "A", filter.priority
    assert_equal ["flight", "plans"], filter.text
    assert_equal "flight plans", filter.text_query
    assert_raises(ArgumentError) { Tasks::TaskFilter.parse_cli(["--not-a-list-flag"]) }
  end

  def test_filter_parser_supports_unavailable_and_own_hold_filters
    unavailable = Tasks::TaskFilter.parse_cli(["--unavailable"]).filter
    assert unavailable.unavailable_only
    refute unavailable.someday_only

    someday = Tasks::TaskFilter.parse_cli(["--on-hold"]).filter
    assert someday.someday_only
    refute someday.unavailable_only

    error = assert_raises(ArgumentError) do
      Tasks::TaskFilter.parse_cli(["--done", "--unavailable"])
    end
    assert_match(/only valid with --open/, error.message)
    error = assert_raises(ArgumentError) do
      Tasks::TaskFilter.parse_cli(["--deferred", "--someday"])
    end
    assert_match(/mutually exclusive/, error.message)
  end

  def test_list_filter_uses_snapshot_bodies_and_never_exposes_lines_in_resources
    with_query_store do |store|
      filter = Tasks::TaskFilter.parse_cli(["--body", "/some note"]).filter
      result = queries(store).list(filter)

      assert_equal [FIX[:travel]], result.tasks.map(&:id)
      task = result.tasks.first
      assert_equal ["Some note line."], task.body
      assert_equal "Work", task.section_title
      assert_equal "Work", task.project
      assert_equal FIX[:work], task.parent_id
      assert_equal [FIX[:work]], task.ancestor_ids
      refute_includes task.to_h.keys, :line
      assert_equal :live, task.to_h[:source]
      assert task.frozen?
      assert task.tags.frozen?
      assert task.body.frozen?
    end
  end

  def test_list_filter_preserves_deferred_and_archive_scope_behavior
    archive_records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "task", "id" => "dead0001", "state" => "DONE", "title" => "Archived report" },
    ]

    with_query_store(records: Tasks::Format.parse(deferred_fixture).records, archive_records: archive_records) do |store|
      deferred = Tasks::TaskFilter.parse_cli(["--deferred"]).filter
      assert_equal [FIX[:plants]], queries(store).list(deferred).tasks.map(&:id)

      archived = Tasks::TaskFilter.parse_cli(["--archived"]).filter
      result = queries(store, include_archive: true).list(archived)
      assert_equal ["dead0001"], result.tasks.map(&:id)
      assert_equal :archive, result.tasks.first.source

      all = Tasks::TaskFilter.parse_cli(["--all"]).filter
      assert_includes queries(store, include_archive: true).list(all).tasks.map(&:id), "dead0001"
    end
  end

  def test_named_views_keep_legacy_selection_and_order_and_attach_quadrants
    with_query_store do |store|
      query = queries(store)
      assert_equal [FIX[:flight], FIX[:eval]], query.view(:agenda).tasks.map(&:id)
      assert_equal [FIX[:flight], FIX[:pr], FIX[:plants]], query.view(:next).tasks.map(&:id)
      assert_equal [FIX[:garden]], query.view(:inbox).tasks.map(&:id)

      quadrants = query.view(:quadrants, today: Date.new(2026, 7, 1))
      flight = quadrants.items.find { |item| item.id == FIX[:flight] }
      plants = quadrants.items.find { |item| item.id == FIX[:plants] }
      assert_equal "Q1", quadrants.metadata_for(flight)[:quadrant]
      assert_equal "Q4", quadrants.metadata_for(plants)[:quadrant]
      assert_raises(ArgumentError) { query.view(:projects) }
    end
  end

  def test_sections_are_canonical_tree_resources
    with_query_store do |store|
      sections = queries(store).sections
      work = sections.find { |section| section.id == FIX[:work] }

      assert_equal "Work", work.title
      assert_nil work.parent_id
      assert_equal [FIX[:flight], FIX[:pr], FIX[:eval], FIX[:travel], FIX[:old]], work.task_ids
      assert_empty work.child_section_ids
      assert_equal work.to_h, work.to_h.freeze
      assert work.frozen?
    end
  end

  def test_operation_context_is_typed_and_immutable
    context = Tasks::OperationContext.new(operation_id: "request-42", source: :cli, actor: "marcus")

    assert_equal({ operation_id: "request-42", source: :cli, actor: "marcus" }, context.to_h)
    assert context.frozen?
    assert_raises(ArgumentError) { Tasks::OperationContext.new(operation_id: "", source: :cli) }
    assert_raises(ArgumentError) { Tasks::OperationContext.new(operation_id: "request-42", source: " ") }
    assert_raises(ArgumentError) { Tasks::OperationContext.new(operation_id: "request-42", source: :webhook) }
  end

  # MRI's sort_by is unstable: without an index tiebreak, equal-priority tasks
  # reorder arbitrarily — visible in `tasks next` output and as a
  # nondeterministic canonical order. Ties must keep DFS file order.
  def test_next_view_keeps_file_order_for_equal_priorities
    records = FIXTURE_RECORDS.map(&:dup)
    tie_ids = Array.new(10) { |i| format("bbbb%04x", i) }
    tie_ids.each do |id|
      records << { "type" => "task", "id" => id, "parent" => FIX[:home],
                   "state" => "NEXT", "title" => "tie #{id}", "tags" => %w[@home] }
    end

    with_query_store(records: records) do |store|
      result = queries(store).view(:next)
      assert_equal [FIX[:flight], FIX[:pr], FIX[:plants], *tie_ids],
                   result.items.map(&:id)
    end
  end

  # A snapshot built without archive records must refuse an archive lookup
  # loudly; silently searching the empty archive turns "not loaded" into a
  # wrong not-found answer (a 404 in the future HTTP adapter).
  def test_find_with_include_archive_requires_an_archive_loaded_snapshot
    archive_records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "task", "id" => "dead0001", "state" => "DONE", "title" => "Archived report" },
    ]

    with_query_store(archive_records: archive_records) do |store|
      error = assert_raises(ArgumentError) do
        queries(store).find("dead0001", include_archive: true)
      end
      assert_match(/include_archive/, error.message)

      loaded = queries(store, include_archive: true)
      assert_equal "dead0001", loaded.find("dead0001", include_archive: true).id
    end
  end

  AVAILABILITY_RECORDS = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "aa000001", "title" => "Work" },
    { "type" => "task", "id" => "aa000002", "parent" => "aa000001", "state" => "NEXT",
      "title" => "Future parent", "scheduled" => "2026-07-15" },
    { "type" => "task", "id" => "aa000003", "parent" => "aa000002", "state" => "NEXT",
      "title" => "Inherited child" },
  ].freeze

  def test_timed_availability_is_inclusive_and_filters_default_and_named_views
    with_query_store(records: AVAILABILITY_RECORDS) do |store|
      before = queries(store, today: Date.new(2026, 7, 14))
      parent = before.snapshot.items.find { |item| item.id == "aa000002" }
      child = before.snapshot.items.find { |item| item.id == "aa000003" }

      assert_equal :scheduled, before.availability(parent).reason
      assert_equal "aa000002", before.availability(parent).blocker_id
      assert_equal :ancestor_scheduled, before.availability(child).reason
      assert_equal "aa000002", before.availability(child).blocker_id
      assert_equal Date.new(2026, 7, 15), before.availability(child).scheduled
      assert_empty before.view(:next).tasks
      assert_empty before.list(Tasks::TaskFilter.new).tasks
      assert_equal %w[aa000002 aa000003],
                   before.list(Tasks::TaskFilter.new(deferred_only: true)).tasks.map(&:id)

      on_date = queries(store, today: Date.new(2026, 7, 15))
      assert on_date.availability(parent).available?
      assert on_date.availability(child).available?
      assert_equal %w[aa000002 aa000003], on_date.view(:next).tasks.map(&:id)
      assert_equal %w[aa000002 aa000003], on_date.list(Tasks::TaskFilter.new).tasks.map(&:id)
    end
  end

  def test_unavailable_and_someday_filters_distinguish_effective_from_own_hold
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "ad000001", "title" => "Work" },
      { "type" => "task", "id" => "ad000002", "parent" => "ad000001", "state" => "TODO",
        "title" => "Held parent", "tags" => %w[defer] },
      { "type" => "task", "id" => "ad000003", "parent" => "ad000002", "state" => "NEXT",
        "title" => "Inherited child" },
      { "type" => "task", "id" => "ad000004", "parent" => "ad000001", "state" => "NEXT",
        "title" => "Timed task", "scheduled" => "2026-07-20" },
      { "type" => "task", "id" => "ad000005", "parent" => "ad000001", "state" => "DONE",
        "title" => "Closed hold", "tags" => %w[defer], "closed" => "2026-07-01" },
    ]

    with_query_store(records: records) do |store|
      query = queries(store)
      unavailable = Tasks::TaskFilter.parse_cli(["--unavailable"]).filter
      assert_equal %w[ad000002 ad000003 ad000004], query.list(unavailable).tasks.map(&:id)

      someday = Tasks::TaskFilter.parse_cli(["--someday"]).filter
      assert_equal ["ad000002"], query.list(someday).tasks.map(&:id)

      done_deferred = Tasks::TaskFilter.parse_cli(["--done", "--deferred"]).filter
      assert_equal ["ad000005"], query.list(done_deferred).tasks.map(&:id)
      done_someday = Tasks::TaskFilter.parse_cli(["--done", "--someday"]).filter
      assert_equal ["ad000005"], query.list(done_someday).tasks.map(&:id)
    end
  end

  def test_availability_after_previews_own_fields_with_canonical_ancestor_precedence
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "ae000001", "title" => "Work" },
      { "type" => "task", "id" => "ae000002", "parent" => "ae000001", "state" => "TODO",
        "title" => "Later parent", "scheduled" => "2026-07-30" },
      { "type" => "task", "id" => "ae000003", "parent" => "ae000002", "state" => "NEXT",
        "title" => "Held child", "tags" => %w[defer] },
    ]

    with_query_store(records: records) do |store|
      query = queries(store)
      child = query.snapshot.items.find { |item| item.id == "ae000003" }
      preview = query.availability_after(
        child, deferred: false, scheduled: Date.new(2026, 7, 18)
      )

      assert_equal :ancestor_scheduled, preview.reason
      assert_equal "ae000002", preview.blocker_id
      assert_equal Date.new(2026, 7, 30), preview.scheduled
      assert child.deferred?, "preview never mutates the held snapshot"
      assert_nil child.scheduled
    end
  end

  def test_blocker_precedence_is_hold_then_latest_date_then_nearest
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "bb000001", "title" => "Work" },
      { "type" => "task", "id" => "bb000002", "parent" => "bb000001", "state" => "TODO",
        "title" => "Held root", "tags" => %w[defer], "scheduled" => "2026-07-30" },
      { "type" => "task", "id" => "bb000003", "parent" => "bb000002", "state" => "TODO",
        "title" => "Timed middle", "scheduled" => "2026-08-01" },
      { "type" => "task", "id" => "bb000004", "parent" => "bb000003", "state" => "TODO",
        "title" => "Timed leaf", "scheduled" => "2026-08-01" },
      { "type" => "task", "id" => "bb000005", "parent" => "bb000001", "state" => "TODO",
        "title" => "Latest root", "scheduled" => "2026-08-02" },
      { "type" => "task", "id" => "bb000006", "parent" => "bb000005", "state" => "TODO",
        "title" => "Earlier leaf", "scheduled" => "2026-08-01" },
    ]

    with_query_store(records: records) do |store|
      query = queries(store)
      leaf = query.snapshot.items.find { |item| item.id == "bb000004" }
      earlier = query.snapshot.items.find { |item| item.id == "bb000006" }

      held = query.availability(leaf)
      assert_equal :ancestor_on_hold, held.reason
      assert_equal "bb000002", held.blocker_id
      assert_nil held.scheduled

      latest = query.availability(earlier)
      assert_equal :ancestor_scheduled, latest.reason
      assert_equal "bb000005", latest.blocker_id
      assert_equal Date.new(2026, 8, 2), latest.scheduled

      records_without_hold = records.map(&:dup)
      records_without_hold.find { |record| record["id"] == "bb000002" }.delete("tags")
      with_query_store(records: records_without_hold) do |unheld_store|
        unheld = queries(unheld_store)
        timed_leaf = unheld.snapshot.items.find { |item| item.id == "bb000004" }
        result = unheld.availability(timed_leaf)
        assert_equal :scheduled, result.reason, "self wins an equal-date tie"
        assert_equal "bb000004", result.blocker_id
      end
    end
  end

  def test_closed_ancestors_are_hoisted_but_their_blockers_still_apply
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "cc000001", "title" => "Work" },
      { "type" => "task", "id" => "cc000002", "parent" => "cc000001", "state" => "DONE",
        "title" => "Transparent closed", "closed" => "2026-07-01" },
      { "type" => "task", "id" => "cc000003", "parent" => "cc000002", "state" => "NEXT",
        "title" => "Visible child" },
      { "type" => "task", "id" => "cc000004", "parent" => "cc000001", "state" => "DONE",
        "title" => "Timed closed", "scheduled" => "2026-07-20", "closed" => "2026-07-01" },
      { "type" => "task", "id" => "cc000005", "parent" => "cc000004", "state" => "NEXT",
        "title" => "Timed hidden child" },
      { "type" => "task", "id" => "cc000006", "parent" => "cc000001", "state" => "DONE",
        "title" => "Held closed", "tags" => %w[defer], "closed" => "2026-07-01" },
      { "type" => "task", "id" => "cc000007", "parent" => "cc000006", "state" => "NEXT",
        "title" => "Held hidden child" },
    ]

    with_query_store(records: records) do |store|
      query = queries(store)
      by_id = query.snapshot.items.to_h { |item| [item.id, item] }

      assert query.availability(by_id["cc000003"]).available?
      assert_equal :ancestor_scheduled, query.availability(by_id["cc000005"]).reason
      assert_equal "cc000004", query.availability(by_id["cc000005"]).blocker_id
      assert_equal :ancestor_on_hold, query.availability(by_id["cc000007"]).reason
      assert_equal ["cc000003"], query.view(:next).tasks.map(&:id)
      assert_equal :closed, query.availability(by_id["cc000004"]).reason
      assert_nil query.availability(by_id["cc000004"]).blocker_id

      done_held = Tasks::TaskFilter.new(scope: :done, deferred_only: true)
      assert_equal ["cc000006"], query.list(done_held).tasks.map(&:id)
    end
  end

  def test_task_resource_exposes_own_marker_and_effective_availability_separately
    with_query_store(records: AVAILABILITY_RECORDS) do |store|
      task = queries(store).find("aa000003")
      json = task.to_h

      assert_equal false, json[:deferred]
      assert_equal false, json[:available]
      assert_equal "ancestor_scheduled", json[:availability_reason]
      assert_equal "aa000002", json[:availability_blocker_id]
      assert_nil json[:scheduled], "stored scheduled remains the task's own field"
      assert_equal 1, Tasks::Format::VERSION
      assert task.frozen?
    end
  end

  def test_version_one_boundary_matrix_is_inclusive_inherited_and_read_only
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "bd000001", "title" => "Work" },
      { "type" => "task", "id" => "bd000002", "parent" => "bd000001", "state" => "NEXT",
        "title" => "Yesterday", "scheduled" => "2026-07-13" },
      { "type" => "task", "id" => "bd000003", "parent" => "bd000001", "state" => "NEXT",
        "title" => "Today", "scheduled" => "2026-07-14" },
      { "type" => "task", "id" => "bd000004", "parent" => "bd000001", "state" => "TODO",
        "title" => "Tomorrow parent", "scheduled" => "2026-07-15" },
      { "type" => "task", "id" => "bd000005", "parent" => "bd000004", "state" => "DONE",
        "title" => "Closed middle", "closed" => "2026-07-01" },
      { "type" => "task", "id" => "bd000006", "parent" => "bd000005", "state" => "NEXT",
        "title" => "Inherited grandchild" },
      { "type" => "task", "id" => "bd000007", "parent" => "bd000001", "state" => "NEXT",
        "title" => "Held despite old date", "tags" => %w[defer], "scheduled" => "2026-07-13" },
    ]

    with_query_store(records: records) do |store|
      path = store.org
      before = File.binread(path)
      today = queries(store, today: Date.new(2026, 7, 14))
      by_id = today.snapshot.items.to_h { |item| [item.id, item] }

      assert today.availability(by_id["bd000002"]).available?, "yesterday is already available"
      assert today.availability(by_id["bd000003"]).available?, "the boundary date is inclusive"
      inherited = today.availability(by_id["bd000006"])
      assert_equal :ancestor_scheduled, inherited.reason
      assert_equal "bd000004", inherited.blocker_id
      assert_equal Date.new(2026, 7, 15), inherited.scheduled
      assert_equal :on_hold, today.availability(by_id["bd000007"]).reason,
                   "an own indefinite hold wins even when its own date is in the past"
      assert_equal %w[bd000002 bd000003], today.view(:next).tasks.map(&:id)

      tomorrow = queries(store, today: Date.new(2026, 7, 15))
      assert tomorrow.availability(tomorrow.snapshot.items.find { |item| item.id == "bd000006" }).available?
      assert_includes tomorrow.view(:next).tasks.map(&:id), "bd000006"
      assert_equal before, File.binread(path), "derived availability never migrates or rewrites descendants"
      assert_equal 1, Tasks::Format.parse(File.read(path)).records.first["version"]
      assert Tasks::Check.check(path).ok?
    end
  end

  def test_closed_and_archived_scopes_keep_legacy_own_hold_filtering
    live = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "lc000001", "title" => "Work" },
      { "type" => "task", "id" => "lc000002", "parent" => "lc000001", "state" => "DONE",
        "title" => "Live held", "tags" => %w[defer], "closed" => "2026-07-01" },
      { "type" => "task", "id" => "lc000003", "parent" => "lc000001", "state" => "DONE",
        "title" => "Live timed only", "scheduled" => "2026-08-01", "closed" => "2026-07-01" },
    ]
    archived = [
      { "type" => "meta", "version" => 1 },
      { "type" => "task", "id" => "ac000001", "state" => "DONE", "title" => "Archived held",
        "tags" => %w[defer], "closed" => "2026-06-01" },
      { "type" => "task", "id" => "ac000002", "state" => "DONE", "title" => "Archived timed only",
        "scheduled" => "2026-08-01", "closed" => "2026-06-01" },
    ]

    with_query_store(records: live, archive_records: archived) do |store|
      query = queries(store, include_archive: true)
      expectations = {
        ["--done", "--deferred"] => %w[lc000002],
        ["--done", "--someday"] => %w[lc000002],
        ["--archived", "--deferred"] => %w[ac000001],
        ["--archived", "--someday"] => %w[ac000001],
        ["--all", "--deferred"] => %w[lc000002 ac000001],
        ["--all", "--on-hold"] => %w[lc000002 ac000001],
      }
      expectations.each do |args, ids|
        filter = Tasks::TaskFilter.parse_cli(args).filter
        assert_equal ids, query.list(filter).tasks.map(&:id), args.join(" ")
      end

      assert_raises(ArgumentError) { Tasks::TaskFilter.parse_cli(%w[--archived --unavailable]) }
      assert_raises(ArgumentError) { Tasks::TaskFilter.parse_cli(%w[--all --unavailable]) }
    end
  end
end
