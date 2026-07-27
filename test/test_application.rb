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
      { "type" => "meta", "version" => 2 },
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
    with_application do |org, _archive, app|
      snapshot = app.edit_snapshot(FIX[:flight])
      body_change = Tasks::TaskPatch.from(snapshot, field: :body, value: "A new note")
      assert_equal :ok, app.patch_task(body_change).status

      title_change = Tasks::TaskPatch.from(snapshot, field: :title, value: "Rebook flight")
      result = app.patch_task(title_change)

      assert_equal :ok, result.status
      task = app.get_task(FIX[:flight])
      assert_equal "Rebook flight", task.title
      assert_equal ["A new note"], task.body
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_keyed_patches_across_fresh_stores_coalesce_into_one_undo_step
    with_application do |org, archive, app|
      initial = File.read(org)
      first = app.patch_task(Tasks::TaskPatch.from(
        app.edit_snapshot(FIX[:flight]), field: :title, value: "Renamed",
        coalesce_key: "editor-session"
      ))
      assert_equal :ok, first.status
      second = app.patch_task(Tasks::TaskPatch.from(
        first.snapshot, field: :body, value: "replacement",
        coalesce_key: "editor-session"
      ))
      assert_equal :ok, second.status
      assert Tasks::Check.check(org).ok?

      undo_store = Tasks::Store.new(org: org, archive: archive)
      assert_equal :ok, undo_store.undo!.first
      assert_equal initial, File.read(org)
      assert_equal [:empty], undo_store.undo!
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

  def test_application_injects_one_today_into_list_view_and_resource_reads
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "dd000001", "title" => "Work" },
      { "type" => "task", "id" => "dd000002", "parent" => "dd000001", "state" => "NEXT",
        "title" => "Tomorrow", "scheduled" => "2026-07-15" },
    ]

    with_application(records: records) do |_org, _archive, app|
      before = Date.new(2026, 7, 14)
      on_date = Date.new(2026, 7, 15)

      assert_empty app.list_tasks(Tasks::TaskFilter.new, today: before).tasks
      assert_empty app.view_tasks(:next, today: before).tasks
      blocked = app.get_task("dd000002", today: before)
      refute blocked.available?
      assert_equal "scheduled", blocked.to_h[:availability_reason]

      assert_equal ["dd000002"], app.list_tasks(Tasks::TaskFilter.new, today: on_date).tasks.map(&:id)
      assert_equal ["dd000002"], app.view_tasks(:next, today: on_date).tasks.map(&:id)
      assert app.get_task("dd000002", today: on_date).available?
      model = app.read_tasks(today: before)
      refute model.task_for("dd000002").available?
      assert_empty model.view_tasks(:next).tasks
    end
  end

  def test_checked_results_carry_data_and_global_revision_from_one_snapshot
    archive_records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "task", "id" => "dead0001", "state" => "DONE", "title" => "Archived report" },
    ]

    with_application(archive_records: archive_records) do |_org, archive, app|
      filter = Tasks::TaskFilter.new(scope: :all)
      first = app.list_tasks_result(filter, today: Date.new(2026, 7, 14))

      assert first.ok?
      assert_match(/\As1\.[0-9a-f]{64}\z/, first.store_revision)
      assert_equal [FIX[:garden], FIX[:flight], FIX[:pr], FIX[:eval], FIX[:travel],
                    FIX[:old], FIX[:plants], "dead0001"], first.data.tasks.map(&:id)

      changed_archive = archive_records.map(&:dup)
      changed_archive.last["title"] = "Archived update"
      File.write(archive, dump_fixture(changed_archive))
      second = app.list_tasks_result(filter, today: Date.new(2026, 7, 14))

      refute_equal first.store_revision, second.store_revision
      assert_equal "Archived update", second.data.tasks.last.title
      assert first.frozen?
      assert first.data.frozen?
      assert first.errors.frozen?
      assert first.warnings.frozen?
    end
  end

  def test_checked_results_return_typed_safe_invalid_and_not_found_outcomes
    with_application do |org, _archive, app|
      missing = app.get_task_result("ffffffff")
      assert missing.not_found?
      assert_nil missing.data
      assert_match(/\As1\./, missing.store_revision)

      File.write(org, "not json\n")
      invalid = app.list_sections_result

      assert invalid.store_invalid?
      assert_nil invalid.data
      assert_equal :live, invalid.errors.first[:source]
      assert_equal 1, invalid.errors.first[:line]
      refute_includes invalid.errors.first[:message], org
      assert_match(/invalid JSON/, invalid.errors.first[:message])
    end
  end

  def test_checked_status_treats_a_missing_archive_as_empty_but_requires_live_file
    with_application do |org, _archive, app|
      status = app.read_status_result
      assert status.ok?
      assert_equal({}, status.data)

      File.delete(org)
      missing = app.read_status_result
      assert missing.store_invalid?
      assert_equal "file not found", missing.errors.first[:message]
    end
  end

  def test_checked_task_lookup_is_exact_to_the_requested_source
    shared_id = FIX[:flight]
    archive_records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "task", "id" => shared_id, "state" => "DONE", "title" => "Archived flight" },
    ]

    with_application(archive_records: archive_records) do |_org, _archive, app|
      live = app.get_task_result(shared_id)
      archived = app.get_task_result(shared_id, source: :archive)

      assert live.ok?
      assert_equal :live, live.data.source
      assert_equal "Book flight in Concur", live.data.title
      assert archived.ok?
      assert_equal :archive, archived.data.source
      assert_equal "Archived flight", archived.data.title
      assert_raises(ArgumentError) { app.get_task_result(shared_id, source: :other) }
    end
  end

  # -- delegation --------------------------------------------------------------
  #
  # Store owns eligibility, the claim compare-and-set, and worker matching
  # (test_delegation.rb). What is proved here is the application contract the
  # CLI, HTTP, and TUI adapters build on: the typed commands, the composed
  # WAITING default and release note as single undo steps, and a summary rich
  # enough that no adapter has to re-derive the outcome.

  WORKER = "claude-code/claude-fable-5/aaaa1111"
  RIVAL  = "claude-code/claude-opus-5/bbbb2222"

  def undo_store(org, archive) = Tasks::Store.new(org: org, archive: archive)

  def test_agent_delegation_returns_the_marker_and_the_canonical_resource
    with_application do |org, _archive, app|
      result = app.delegate_task(FIX[:plants], kind: "agent", mode: "research")

      assert result.changed?
      summary = result.summary
      assert_equal :delegate, summary[:action]
      assert_equal FIX[:plants], summary[:task_id]
      assert_nil summary[:previous]
      assert_equal "research", summary[:delegation]["mode"]
      assert_equal "ready", summary[:delegation]["status"]
      assert_equal "NEXT", summary[:state]
      refute summary[:state_changed], "agent delegation never moves lifecycle state"
      assert_equal FIX[:plants], summary[:task].id
      assert summary[:task].agent_ready?
      assert_equal [FIX[:plants]], result.touched_ids
      assert_match(/\As1\.[0-9a-f]{64}\z/, result.store_revision)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_human_delegation_moves_to_waiting_in_one_undo_step
    with_application do |org, archive, app|
      before = File.read(org)
      result = app.delegate_task(FIX[:plants], kind: "human", assignee: "pat@example.com")

      assert result.changed?
      assert result.summary[:state_changed]
      assert_equal "WAITING", result.summary[:state]
      assert_equal "WAITING", result.summary[:task].state
      assert_equal "pat@example.com", result.summary[:task].delegation_assignee
      assert_equal "delegated", result.summary[:delegation]["status"]

      store = undo_store(org, archive)
      assert_equal :ok, store.undo!.first
      assert_equal before, File.read(org), "the WAITING default undoes with the delegation"
      assert_equal :ok, store.redo!.first
      assert_equal "WAITING", app.get_task(FIX[:plants]).state
    end
  end

  def test_keep_state_opts_out_of_the_waiting_default
    with_application do |_org, _archive, app|
      result = app.delegate_task(FIX[:plants], kind: "human", assignee: "pat@example.com",
                                 keep_state: true)

      assert result.changed?
      refute result.summary[:state_changed]
      assert_equal "NEXT", result.summary[:state]
      assert_equal "pat@example.com", app.get_task(FIX[:plants]).delegation_assignee
    end
  end

  def test_mode_update_keeps_the_work_ref_and_reports_the_previous_marker
    with_application do |_org, _archive, app|
      app.delegate_task(FIX[:plants], kind: "agent", mode: "research")
      app.set_work_ref(FIX[:plants], "https://example.com/brief")
      result = app.delegate_task(FIX[:plants], kind: "agent", mode: "implement")

      assert result.changed?
      assert_equal "research", result.summary[:previous]["mode"]
      assert_equal "implement", result.summary[:delegation]["mode"]
      assert_equal "https://example.com/brief", result.summary[:task].work_ref
      assert_equal "ready", result.summary[:task].delegation_status
    end
  end

  def test_replacing_human_with_agent_delegation_and_back
    with_application do |_org, _archive, app|
      app.delegate_task(FIX[:plants], kind: "human", assignee: "pat@example.com")
      to_agent = app.delegate_task(FIX[:plants], kind: "agent", mode: "refine")

      assert to_agent.changed?
      assert_equal "human", to_agent.summary[:previous]["kind"]
      assert_equal "agent", to_agent.summary[:delegation]["kind"]
      assert_equal "WAITING", to_agent.summary[:state], "undelegating never leaves WAITING by itself"

      back = app.delegate_task(FIX[:plants], kind: "human", assignee: "sam@example.com")
      assert_equal "agent", back.summary[:previous]["kind"]
      assert_equal "sam@example.com", back.summary[:task].delegation_assignee
    end
  end

  def test_claim_returns_the_full_resource_and_a_lost_race_names_the_holder
    with_application do |_org, _archive, app|
      app.delegate_task(FIX[:plants], kind: "agent", mode: "research")
      won = app.claim_task(FIX[:plants], worker: WORKER)

      assert won.changed?
      assert_equal :claim, won.summary[:action]
      assert_equal WORKER, won.summary[:task].delegation_assignee
      assert_equal "research", won.summary[:task].delegation_mode
      assert_equal "Water the plants", won.summary[:task].title

      lost = app.claim_task(FIX[:plants], worker: RIVAL)
      assert lost.conflict?
      assert_equal :claim, lost.summary[:action]
      assert_equal FIX[:plants], lost.summary[:task_id]
      assert_equal WORKER, lost.summary[:holder]
      assert_match(/\A\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\z/, lost.summary[:at])
      assert_equal 1, lost.cli_exit_code
      assert_equal WORKER, app.get_task(FIX[:plants]).delegation_assignee
    end
  end

  def test_release_enforces_the_worker_match_and_the_owner_can_force_it
    with_application do |_org, _archive, app|
      app.delegate_task(FIX[:plants], kind: "agent", mode: "research")
      app.claim_task(FIX[:plants], worker: WORKER)

      mismatch = app.release_task(FIX[:plants], worker: RIVAL)
      assert mismatch.conflict?
      assert_equal :release, mismatch.summary[:action]
      assert_equal WORKER, mismatch.summary[:holder]
      assert_equal WORKER, app.get_task(FIX[:plants]).delegation_assignee

      forced = app.release_task(FIX[:plants], force: true)
      assert forced.changed?
      assert_equal WORKER, forced.summary[:released_from]
      assert forced.summary[:forced]
      assert forced.summary[:task].agent_ready?
    end
  end

  def test_release_note_is_appended_in_the_same_undo_step
    with_application do |org, archive, app|
      app.delegate_task(FIX[:plants], kind: "agent", mode: "implement")
      app.claim_task(FIX[:plants], worker: WORKER)
      before = File.read(org)

      result = app.release_task(FIX[:plants], worker: WORKER, note: "blocked: need repo access")

      assert result.changed?
      assert result.summary[:note_applied]
      assert result.summary[:task].agent_ready?
      assert_equal ["blocked: need repo access"], result.summary[:task].body

      store = undo_store(org, archive)
      assert_equal :ok, store.undo!.first
      assert_equal before, File.read(org), "the note and the release are one undo step"
      assert_equal WORKER, app.get_task(FIX[:plants]).delegation_assignee
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_work_ref_is_set_cleared_and_worker_matched
    with_application do |_org, _archive, app|
      app.delegate_task(FIX[:plants], kind: "agent", mode: "research")
      app.claim_task(FIX[:plants], worker: WORKER)

      stale = app.set_work_ref(FIX[:plants], "https://example.com/other", worker: RIVAL)
      assert stale.conflict?

      set = app.set_work_ref(FIX[:plants], "https://example.com/brief", worker: WORKER)
      assert set.changed?
      assert_equal "https://example.com/brief", set.summary[:task].work_ref

      cleared = app.set_work_ref(FIX[:plants], "off")
      assert cleared.changed?
      assert_nil cleared.summary[:task].work_ref
      assert_equal :work_ref, cleared.summary[:action]
    end
  end

  def test_undelegate_clears_the_marker_and_leaves_lifecycle_alone
    with_application do |_org, _archive, app|
      app.delegate_task(FIX[:plants], kind: "human", assignee: "pat@example.com")
      result = app.undelegate_task(FIX[:plants])

      assert result.changed?
      assert_equal :undelegate, result.summary[:action]
      assert_equal "pat@example.com", result.summary[:previous]["assignee"]
      assert_nil result.summary[:delegation]
      assert_equal "WAITING", result.summary[:state], "the owner decides when to leave WAITING"
      refute result.summary[:task].delegated?

      repeat = app.undelegate_task(FIX[:plants])
      assert repeat.no_change?
      assert_nil repeat.summary[:previous]
      refute_nil repeat.summary[:task], "an idempotent repeat still returns the resource"
    end
  end

  def test_delegation_refuses_proposed_and_closed_tasks
    records = FIXTURE_RECORDS.map(&:dup)
    records << { "type" => "task", "id" => "eeee0001", "parent" => FIX[:home],
                 "state" => "PROPOSED", "title" => "Maybe repaint" }

    with_application(records: records) do |_org, _archive, app|
      proposed = app.delegate_task("eeee0001", kind: "agent", mode: "refine")
      assert proposed.invalid?
      assert_match(/PROPOSED/, proposed.errors.first)

      closed = app.delegate_task(FIX[:old], kind: "human", assignee: "pat@example.com")
      assert closed.invalid?
      assert_match(/DONE/, closed.errors.first)

      missing = app.claim_task("ffffffff", worker: WORKER)
      assert missing.not_found?
      assert_equal 2, missing.cli_exit_code
    end
  end

  def test_delegation_honors_an_expected_revision
    with_application do |_org, _archive, app|
      stale_revision = app.get_task(FIX[:plants]).revision
      app.delegate_task(FIX[:plants], kind: "agent", mode: "refine")

      refused = app.delegate_task(FIX[:plants], kind: "agent", mode: "implement",
                                  expected_revision: stale_revision)
      assert refused.stale?
      assert_equal "refine", app.get_task(FIX[:plants]).delegation_mode

      current = app.get_task(FIX[:plants]).revision
      accepted = app.delegate_task(FIX[:plants], kind: "agent", mode: "implement",
                                   expected_revision: current)
      assert accepted.changed?
    end
  end

  def test_delegation_accepts_prebuilt_typed_commands
    with_application do |_org, _archive, app|
      command = Tasks::DelegationCommand.new(id: FIX[:plants], action: :delegate,
                                             kind: "agent", mode: "research")
      assert app.delegate_task(command).changed?
      assert app.claim_task(
        Tasks::DelegationCommand.new(id: FIX[:plants], action: :claim, worker: WORKER)
      ).changed?

      assert_raises(ArgumentError) { app.delegate_task(command, mode: "implement") }
      assert_raises(ArgumentError) { app.claim_task(command) }
      assert_raises(ArgumentError) do
        Tasks::DelegationCommand.new(id: FIX[:plants], action: :promote)
      end
    end
  end

  def test_every_delegation_mutation_is_individually_undoable
    with_application do |org, archive, app|
      states = [File.read(org)]
      app.delegate_task(FIX[:plants], kind: "agent", mode: "research")
      states << File.read(org)
      app.claim_task(FIX[:plants], worker: WORKER)
      states << File.read(org)
      app.set_work_ref(FIX[:plants], "https://example.com/brief", worker: WORKER)
      states << File.read(org)
      app.release_task(FIX[:plants], worker: WORKER)
      states << File.read(org)
      app.undelegate_task(FIX[:plants])

      store = undo_store(org, archive)
      states.reverse.each do |expected|
        assert_equal :ok, store.undo!.first
        assert_equal expected, File.read(org)
      end
      states.drop(1).each do |expected|
        assert_equal :ok, store.redo!.first
        assert_equal expected, File.read(org)
      end
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_checked_result_owns_an_immutable_copy_of_plain_payloads
    key = +"items"
    value = +"one"
    payload = { key => [value] }
    result = Tasks::ApplicationReadResult.new(status: :ok, data: payload)
    key.replace("changed")
    value.replace("changed")
    payload.values.first << "two"

    assert_equal({ "items" => ["one"] }, result.data)
    assert result.data.frozen?
    assert result.data["items"].frozen?
    assert result.data["items"].first.frozen?
  end
end
