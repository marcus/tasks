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
end
