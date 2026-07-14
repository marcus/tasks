# frozen_string_literal: true

require_relative "test_helper"
require "tasks/application"

class TestApplication < Minitest::Test
  def with_application(records: FIXTURE_RECORDS, archive_records: nil)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, dump_fixture(records))
      File.write(archive, dump_fixture(archive_records)) if archive_records
      yield org, archive, Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive)
      )
    end
  end

  def test_query_methods_return_phase_two_views_without_exposing_a_store
    archive_records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "task", "id" => "dead0001", "state" => "DONE", "title" => "Archived report" },
    ]

    with_application(archive_records: archive_records) do |_org, _archive, app|
      filter = Tasks::TaskFilter.parse_cli(["--all"]).filter
      result = app.list_tasks(filter)

      assert_equal [FIX[:garden], FIX[:flight], FIX[:pr], FIX[:eval], FIX[:travel], FIX[:old], FIX[:plants], "dead0001"],
                   result.tasks.map(&:id)
      assert_equal FIX[:flight], app.get_task(FIX[:flight]).id
      assert_equal "dead0001", app.get_task("dead0001", include_archive: true).id
      assert_nil app.get_task("does-not-exist")
      assert_equal [FIX[:inbox], FIX[:work], FIX[:home]], app.list_sections.map(&:id)
      assert_equal [FIX[:flight], FIX[:eval]], app.view_tasks(:agenda).tasks.map(&:id)
      assert_raises(ArgumentError) { app.list_tasks(:open) }
    end
  end

  def test_every_application_call_gets_a_fresh_store_instance
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, FIXTURE)
      built = []
      factory = lambda do
        store = Tasks::Store.new(org: org, archive: archive)
        built << store
        store
      end
      app = Tasks::Application.new(store_factory: factory)

      app.list_tasks(Tasks::TaskFilter.new)
      app.view_tasks(:inbox)
      app.get_task(FIX[:garden])
      app.list_sections

      assert_equal 4, built.length
      assert_equal 4, built.uniq.length
      built.each { |store| assert_nil store.instance_variable_get(:@read_snapshot) }
    end
  end

  def test_patch_task_preserves_field_scoped_conflicts_without_exposing_a_store
    with_application do |_org, _archive, app|
      snapshot = app.edit_snapshot(FIX[:flight])
      body_change = Tasks::TaskPatch.from(snapshot, field: :body, value: "A new note")
      assert_equal :ok, app.patch_task(body_change).status

      title_change = Tasks::TaskPatch.from(snapshot, field: :title, value: "Rebook flight")
      result = app.patch_task(title_change)

      assert_equal :ok, result.status
      task = app.get_task(FIX[:flight])
      assert_equal "Rebook flight", task.title
      assert_equal ["A new note"], task.body
    end
  end

  def test_live_read_model_keeps_presentation_items_and_canonical_views_on_one_snapshot
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, FIXTURE)
      built = []
      factory = lambda do
        store = Tasks::Store.new(org: org, archive: archive)
        built << store
        store
      end
      app = Tasks::Application.new(store_factory: factory)

      first = app.read_tasks
      flight = first.items.find { |item| item.id == FIX[:flight] }
      task = first.task_for(flight)

      assert_equal FIX[:flight], task.id
      assert_equal task, first.task_for(FIX[:flight])
      assert_equal FIX[:work], task.section_id
      assert_equal [FIX[:flight], FIX[:eval]], first.view_tasks(:agenda).tasks.map(&:id)
      assert_equal "Work", first.node_for(flight).parent.title
      assert first.items.frozen?
      assert first.tasks.frozen?
      assert_nil built.first.instance_variable_get(:@read_snapshot), "read models do not retain Store caches"

      records = Tasks::Format.parse(File.read(org, encoding: "UTF-8")).records
      records.find { |record| record["id"] == FIX[:flight] }["title"] = "Changed externally"
      File.write(org, dump_fixture(records))
      second = app.read_tasks

      assert_equal "Book flight in Concur", first.task_for(FIX[:flight]).title
      assert_equal "Changed externally", second.task_for(FIX[:flight]).title
      assert_equal 2, built.length
    end
  end

  def test_factory_keeps_construction_settings_immutable
    Dir.mktmpdir do |dir|
      links = { "jira" => "https://jira.example/browse/%s" }
      factory = Tasks::StoreFactory.new(
        org: File.join(dir, "tasks.jsonl"), archive: File.join(dir, "archive.jsonl"), links: links
      )
      links["jira"] = "changed"

      first = factory.call
      second = factory.call

      refute_same first, second
      assert_equal "https://jira.example/browse/%s", first.instance_variable_get(:@link_shorthands)["jira"]
      assert first.instance_variable_get(:@link_shorthands).frozen?
    end
  end

  def test_read_model_reports_staleness_after_an_external_write
    with_application do |org, _archive, app|
      model = app.read_tasks
      refute model.stale?(org), "a freshly built model must not report stale"

      records = FIXTURE_RECORDS.map(&:dup)
      records << { "type" => "task", "id" => "bbbb0001", "parent" => FIX[:home],
                   "state" => "TODO", "title" => "External write" }
      File.write(org, dump_fixture(records))

      assert model.stale?(org), "an external write must mark the held model stale"
      refute app.read_tasks.stale?(org), "a rebuilt model over the new bytes is current"
    end
  end
end
