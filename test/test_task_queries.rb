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

  def queries(store, include_archive: false)
    Tasks::TaskQueries.new(store.read_snapshot(include_archive: include_archive))
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
end
