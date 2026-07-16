# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "rbconfig"
require "tui/task_editor_session"

class TestTaskEditorSession < Minitest::Test
  BIN = File.expand_path("../bin/tasks", __dir__)
  EDIT_TREE = [
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "11110001", "title" => "One" },
    { "type" => "task", "id" => "11110002", "parent" => "11110001", "state" => "NEXT",
      "priority" => "B", "title" => "Parent", "tags" => %w[@home alpha],
      "scheduled" => "2026-07-13", "recur" => ".+1w", "body" => "old\nnotes" },
    { "type" => "task", "id" => "11110003", "parent" => "11110002", "state" => "NEXT",
      "title" => "Child" },
    { "type" => "section", "id" => "22220001", "title" => "Two" },
    { "type" => "task", "id" => "22220002", "parent" => "22220001", "state" => "TODO",
      "title" => "Destination" },
  ].freeze

  def with_editor(today: Date.new(2026, 7, 13))
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(EDIT_TREE))
      store = Tasks::Store.new(org: org, archive: archive)
      application = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive)
      )
      session = Tui::TaskEditorSession.new(
        store: store, application: application, target_id: "11110002", today: today,
      )
      yield session, store, org
    end
  end

  def test_editor_routes_snapshots_and_patches_through_the_application_boundary
    source = File.read(File.expand_path("../lib/tui/task_editor_session.rb", __dir__), encoding: "UTF-8")

    refute_match(/store\.(?:edit_snapshot|patch_task!)/, source)
    assert_match(/application\.edit_snapshot/, source)
    assert_match(/application\.patch_task\(patch, today: operation_today\)/, source)
  end

  def record(path, id = "11110002")
    Tasks::Format.parse(File.read(path, encoding: "UTF-8")).records.find { |entry| entry["id"] == id }
  end

  def run_external_cli(org, *args)
    env = {
      "TASKS_FILE" => org,
      "TASKS_ARCHIVE" => File.join(File.dirname(org), "archive.jsonl"),
      "XDG_STATE_HOME" => ENV.fetch("XDG_STATE_HOME"),
    }
    stdout, stderr, status = Open3.capture3(env, RbConfig.ruby, BIN, *args)
    assert status.success?, "#{BIN} #{args.join(" ")} failed\nstdout: #{stdout}\nstderr: #{stderr}"
    [stdout, stderr]
  end

  def test_adapter_has_exact_field_order_semantic_values_and_read_only_metadata
    with_editor do |session, _store, _org|
      # The placement/location field is deliberately absent from the form; task
      # nesting is handled outside the editor (Store still accepts :location).
      assert_equal %i[
        title priority deferred scheduled deadline recurrence contexts tags body
        state
      ], session.edit_form.field_order
      refute_includes session.edit_form.field_order, :location
      assert_equal [
        "Title", "Priority", "On hold", "Available from", "Deadline", "Recurrence",
        "Contexts", "Tags", "Notes", "State",
      ], session.render_model.rows.map(&:label)
      assert_equal({ id: "11110002", closed: nil }, session.read_only)

      session.form.set_value(:recurrence, "weekly")
      session.form.set_value(:contexts, ["work", "@home", "work"])
      session.form.set_value(:tags, ["alpha", "defer", "@bad", "alpha"])
      assert_equal ".+1w", session.edit_form.semantic_value(:recurrence)
      assert_equal %w[@work @home], session.edit_form.semantic_value(:contexts)
      assert_equal ["alpha"], session.edit_form.semantic_value(:tags)
    end
  end

  def test_ctrl_s_commits_in_place_and_ctrl_o_commits_then_finishes
    with_editor do |session, _store, org|
      session.form.set_value(:title, "Renamed")
      saved = session.handle(Tui::TaskEditorSession::CTRL_S)
      assert_equal :ok, saved.status
      assert_equal :title, session.focused_key
      assert_equal "Renamed", record(org)["title"]
      refute session.dirty?(:title)

      session.form.set_value(:title, "Finished title")
      finished = session.handle(Tui::TaskEditorSession::CTRL_O)
      assert finished.finished?
      assert_equal "Finished title", record(org)["title"]
    end
  end

  def test_mutable_identity_coalesce_and_request_strings_are_bound_at_entry
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(EDIT_TREE))
      store = Tasks::Store.new(org: org, archive: archive)
      application = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive)
      )
      target = +"11110002"
      coalesce = +"editor-session"
      proposed = +"Bound title"
      session = Tui::TaskEditorSession.new(
        store: store, application: application, target_id: target, coalesce_key: coalesce,
        today: Date.new(2026, 7, 13),
      )
      session.form.set_value(:title, proposed)

      target.replace("11110003")
      coalesce.replace("different-session")
      proposed.replace("Mutated title")

      assert session.target_id.frozen?
      assert session.coalesce_key.frozen?
      assert_equal "11110002", session.target_id
      assert_equal "editor-session", session.coalesce_key
      assert_equal :ok, session.save.status
      assert_equal "Bound title", record(org, "11110002")["title"]
      assert_equal "Child", record(org, "11110003")["title"]

      session.form.focus(:priority)
      session.form.set_value(:priority, "A")
      assert_equal :ok, session.save.status
      assert_equal :ok, store.undo!.first
      assert_equal "Parent", record(org, "11110002")["title"]
      assert_equal "B", record(org, "11110002")["priority"]
    end
  end

  def test_blur_commits_and_one_session_coalesces_history
    with_editor do |session, store, org|
      original = File.read(org)
      session.form.set_value(:title, "Renamed")
      assert_equal :ok, session.handle("\t").status
      assert_equal :priority, session.focused_key
      session.form.set_value(:priority, "A")
      assert_equal :ok, session.save.status
      refute_equal original, File.read(org)

      assert_equal :ok, store.undo!.first
      assert_equal original, File.read(org), "session patches should undo as one byte-contiguous edit"
    end
  end

  def test_every_ordinary_field_reaches_its_semantic_store_slice
    with_editor do |session, _store, org|
      session.form.set_value(:title, "Renamed")
      session.form.set_value(:body, "local\nbody")
      assert_equal :ok, session.save.status
      assert_equal "local\nbody", session.edit_form.value(:body), "accepted title refresh preserves another dirty buffer"
      assert session.dirty?(:body)
      session.form.focus(:body)
      assert_equal :ok, session.save.status

      {
        priority: "A",
        deferred: true,
        deadline: Date.new(2026, 7, 31),
        contexts: %w[@work @phone],
        tags: %w[alpha beta],
        body: "new\nnotes",
      }.each do |field, value|
        session.form.focus(field)
        session.form.set_value(field, value)
        assert_equal :ok, session.save.status, field.to_s
      end

      task = record(org)
      assert_equal "Renamed", task["title"]
      assert_equal "A", task["priority"]
      assert_includes task["tags"], "defer"
      assert_equal %w[@work @phone], task["tags"].select { |tag| tag.start_with?("@") }
      assert_equal %w[alpha beta], task["tags"].reject { |tag| tag.start_with?("@") || tag == "defer" }
      assert_equal "2026-07-31", task["deadline"]
      assert_equal "new\nnotes", task["body"]
    end
  end

  def test_picker_escape_closes_picker_before_session_cancel
    with_editor do |session, _store, _org|
      session.form.focus(:deadline)
      assert_equal :handled, session.handle("\r").status
      assert session.form.field(:deadline).picker_open?

      result = session.handle("\e")
      assert_equal :handled, result.status
      refute session.form.field(:deadline).picker_open?
      assert_nil session.pending_revert
    end
  end

  def test_dirty_field_requires_double_escape_and_reverts_only_that_buffer
    with_editor do |session, _store, org|
      session.form.set_value(:title, "Local draft")
      first = session.handle("\e")
      assert_equal :revert_pending, first.status
      assert_equal "Local draft", session.edit_form.value(:title)
      assert_equal "Parent", record(org)["title"]

      second = session.handle("\e")
      assert_equal :reverted, second.status
      assert_equal "Parent", session.edit_form.value(:title)
      refute session.dirty?(:title)
    end
  end


  def test_suspend_disarms_confirmation_and_revert_without_losing_local_values
    with_editor do |session, _store, org|
      session.form.focus(:state)
      session.form.set_value(:state, "DONE")
      assert session.save.confirmation?

      outcome = session.suspend
      assert_match(/Confirmation cancelled/, outcome.message)
      assert_nil session.pending_confirmation
      assert session.dirty?(:state)
      assert_equal "NEXT", record(org)["state"]

      session.form.focus(:title)
      session.form.set_value(:title, "local draft")
      assert_equal :revert_pending, session.handle("\e").status
      outcome = session.suspend
      assert_match(/Discard prompt cancelled/, outcome.message)
      assert_nil session.pending_revert
      assert_equal "local draft", session.edit_form.value(:title)
    end
  end

  def test_state_recurrence_and_coupled_date_changes_require_confirmation
    with_editor do |session, _store, org|
      session.form.focus(:state)
      session.form.set_value(:state, "DONE")
      pending = session.save
      assert pending.confirmation?
      assert_match(/advances its recurrence/, pending.message)
      assert_equal "NEXT", record(org)["state"]
      accepted = session.confirm!
      assert_equal :ok, accepted.status
      assert_equal "NEXT", record(org)["state"]
      assert_equal "2026-07-20", record(org)["scheduled"]
    end

    with_editor do |session, _store, org|
      session.form.focus(:recurrence)
      session.form.set_value(:recurrence, "monthly")
      assert session.save.confirmation?
      session.confirm!
      assert_equal ".+1m", record(org)["recur"]

      session.form.focus(:scheduled)
      session.form.set_value(:scheduled, nil)
      consequence = session.save
      assert consequence.confirmation?
      assert_match(/clears recurrence/, consequence.message)
    end
  end

  def test_recurring_completion_uses_the_editor_operation_date_for_advance_and_log
    with_editor(today: Date.new(2030, 1, 1)) do |session, _store, org|
      session.form.focus(:state)
      session.form.set_value(:state, "DONE")

      assert session.save.confirmation?
      accepted = session.confirm!

      assert_equal :ok, accepted.status
      assert_equal "NEXT", record(org)["state"]
      assert_equal "2030-01-08", record(org)["scheduled"]
      assert_match(/- Did \[2030-01-01\]/, record(org).fetch("body"))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_clear_final_date_confirmation_conflicts_with_newer_recurrence_without_writing
    with_editor do |session, store, org|
      session.form.focus(:scheduled)
      session.form.set_value(:scheduled, nil)
      pending = session.save
      assert pending.confirmation?
      assert_equal ".+1w", pending.data.expectations[:owned][:recurrence]
      assert_equal({ deadline: false }, pending.data.expectations[:predicates][:date_presence])

      external = store.edit_snapshot("11110002")
      changed = store.patch_task!(Tasks::TaskPatch.from(external, field: :recurrence, value: ".+1m"))
      assert_equal :ok, changed.status
      before_confirm = File.read(org)

      refused = session.confirm!
      assert refused.conflict?
      assert_same pending.data, session.pending_confirmation
      assert session.form.pending?
      assert_nil session.edit_form.value(:scheduled)
      assert_equal before_confirm, File.read(org)
      assert_equal "2026-07-13", record(org)["scheduled"]
      assert_equal ".+1m", record(org)["recur"]

      assert_equal :conflict_reloaded, session.reload_conflict!.status
      assert_nil session.pending_confirmation
      refute session.form.pending?
      assert_equal Date.new(2026, 7, 13), session.edit_form.value(:scheduled)
    end
  end

  def test_child_lifecycle_change_coexists_with_parent_final_date_clear
    with_editor do |session, store, org|
      session.form.focus(:scheduled)
      session.form.set_value(:scheduled, nil)
      assert session.save.confirmation?

      child = store.edit_snapshot("11110003")
      changed = store.patch_task!(Tasks::TaskPatch.from(child, field: :state, value: "WAITING"))
      assert_equal :ok, changed.status

      accepted = session.confirm!
      assert_equal :ok, accepted.status
      assert_nil record(org)["scheduled"]
      assert_nil record(org)["recur"]
      assert_equal "WAITING", record(org, "11110003")["state"]
    end
  end

  def test_added_deadline_coexists_with_recurrence_change_when_a_live_date_remains
    with_editor do |session, store, org|
      session.form.focus(:recurrence)
      session.form.set_value(:recurrence, "monthly")
      pending = session.save
      assert pending.confirmation?
      assert_equal({ any_live_date: true }, pending.data.expectations[:predicates])

      external = store.edit_snapshot("11110002")
      added = store.patch_task!(Tasks::TaskPatch.from(
        external, field: :deadline, value: Date.new(2026, 8, 1)
      ))
      assert_equal :ok, added.status

      accepted = session.confirm!
      assert_equal :ok, accepted.status
      assert_equal "2026-08-01", record(org)["deadline"]
      assert_equal ".+1m", record(org)["recur"]
    end
  end

  def test_recurrence_confirmation_conflicts_when_all_dates_disappear_or_recurrence_changes
    with_editor do |session, store, org|
      session.form.focus(:recurrence)
      session.form.set_value(:recurrence, "monthly")
      pending = session.save
      assert pending.confirmation?

      external = store.edit_snapshot("11110002")
      removed = store.patch_task!(Tasks::TaskPatch.from(external, field: :scheduled, value: nil))
      assert_equal :ok, removed.status
      before_confirm = File.read(org)

      refused = session.confirm!
      assert refused.conflict?
      assert_same pending.data, session.pending_confirmation
      assert_equal before_confirm, File.read(org)
      assert_nil record(org)["scheduled"]
      assert_nil record(org)["recur"]
    end

    with_editor do |session, store, org|
      session.form.focus(:recurrence)
      session.form.set_value(:recurrence, "monthly")
      pending = session.save
      assert pending.confirmation?

      external = store.edit_snapshot("11110002")
      changed = store.patch_task!(Tasks::TaskPatch.from(external, field: :recurrence, value: ".+1d"))
      assert_equal :ok, changed.status
      before_confirm = File.read(org)

      refused = session.confirm!
      assert refused.conflict?
      assert_same pending.data, session.pending_confirmation
      assert_equal before_confirm, File.read(org)
      assert_equal ".+1d", record(org)["recur"]
    end
  end

  def test_state_confirmation_conflicts_with_newer_subtree_lifecycle
    with_editor do |session, store, org|
      session.form.focus(:state)
      session.form.set_value(:state, "DONE")
      pending = session.save
      assert pending.confirmation?

      child = store.edit_snapshot("11110003")
      changed = store.patch_task!(Tasks::TaskPatch.from(child, field: :state, value: "WAITING"))
      assert_equal :ok, changed.status
      before_confirm = File.read(org)

      refused = session.confirm!
      assert refused.conflict?
      assert_same pending.data, session.pending_confirmation
      assert session.form.pending?
      assert_equal before_confirm, File.read(org)
      assert_equal "NEXT", record(org)["state"]
      assert_equal "WAITING", record(org, "11110003")["state"]
    end
  end

  def test_clean_refresh_adopts_external_changes_but_dirty_refresh_preserves_buffer_and_conflicts
    with_editor do |session, store, _org|
      session.form.set_value(:title, "Local title")
      external = store.edit_snapshot("11110002")
      result = store.patch_task!(Tasks::TaskPatch.from(external, field: :title, value: "External title"))
      assert_equal :ok, result.status
      result = store.patch_task!(Tasks::TaskPatch.from(result.snapshot, field: :priority, value: "A"))
      assert_equal :ok, result.status

      session.refresh
      assert_equal "Local title", session.edit_form.value(:title)
      assert_equal "A", session.edit_form.value(:priority)
      conflict = session.save
      assert conflict.conflict?
      assert_equal "Local title", session.edit_form.value(:title)
      assert_equal "External title", conflict.data.fresh_value

      kept = session.keep_for_copy!
      assert_equal :copy_kept, kept.status
      assert_equal "Local title", session.kept_copy
      reloaded = session.reload_conflict!
      assert_equal :conflict_reloaded, reloaded.status
      assert_equal "External title", session.edit_form.value(:title)
      refute session.dirty?(:title)
    end
  end

  def test_absolute_cli_writes_merge_unowned_slices_and_conflict_on_the_owned_slice
    with_editor do |session, _store, org|
      session.form.set_value(:title, "Local title")
      run_external_cli(org, "priority", "11110002", "A")

      session.refresh
      assert_equal "Local title", session.edit_form.value(:title)
      assert_equal "A", session.edit_form.value(:priority)
      assert_equal :ok, session.save.status
      assert_equal "Local title", record(org)["title"]
      assert_equal "A", record(org)["priority"]

      session.form.set_value(:title, "Second local title")
      run_external_cli(org, "retitle", "11110002", "External title")
      session.refresh
      before = File.binread(org)

      conflict = session.save
      assert conflict.conflict?
      assert_equal "Second local title", session.edit_form.value(:title)
      assert_equal "External title", conflict.data.fresh_value
      assert_equal before, File.binread(org)
    end
  end

  def test_missing_target_is_inert_and_keeps_the_active_buffer_copyable
    with_editor do |session, _store, org|
      session.form.set_value(:title, "Copy me")
      records = Tasks::Format.parse(File.read(org, encoding: "UTF-8")).records
      records.reject! { |entry| %w[11110002 11110003].include?(entry["id"]) }
      File.write(org, Tasks::Format.dump(records))

      result = session.refresh
      assert result.missing?
      assert session.inert?
      assert_equal "Copy me", session.copy_value
      assert session.handle("x").missing?
      assert_equal "Copy me", session.edit_form.value(:title)
    end
  end

  def test_absolute_cli_archive_makes_target_missing_without_retargeting_the_buffer
    with_editor do |session, _store, org|
      session.form.set_value(:title, "Copy after external archive")
      run_external_cli(org, "recur", "11110002", "off")
      run_external_cli(org, "done", "11110002")
      run_external_cli(org, "archive")

      result = session.refresh
      assert result.missing?
      assert session.inert?
      assert_equal "11110002", session.target_id
      assert_equal "Copy after external archive", session.copy_value
      assert_nil record(org)
      assert_equal "Destination", record(org, "22220002")["title"]
    end
  end

  def test_invalid_store_result_retains_focus_and_buffer
    with_editor do |session, _store, org|
      session.form.focus(:title)
      session.form.set_value(:title, "   ")
      result = session.save
      assert result.invalid?
      assert_equal :title, session.focused_key
      assert_equal "   ", session.edit_form.value(:title)
      assert_equal "Parent", record(org)["title"]
    end
  end
end
