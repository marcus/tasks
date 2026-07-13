# frozen_string_literal: true

require_relative "test_helper"
require "tui/task_editor_session"

class TestTaskEditorSession < Minitest::Test
  EDIT_TREE = [
    { "type" => "meta", "version" => 1 },
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

  def with_editor
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(EDIT_TREE))
      store = Tasks::Store.new(org: org, archive: archive)
      locations = [["11110001", "One"], ["22220001", "Two"], ["22220002", "Destination"]]
      session = Tui::TaskEditorSession.new(
        store: store, target_id: "11110002", today: Date.new(2026, 7, 13),
        locations: locations,
      )
      yield session, store, org
    end
  end

  def record(path, id = "11110002")
    Tasks::Format.parse(File.read(path, encoding: "UTF-8")).records.find { |entry| entry["id"] == id }
  end

  def test_adapter_has_exact_field_order_semantic_values_and_read_only_metadata
    with_editor do |session, _store, _org|
      assert_equal %i[
        title priority deferred scheduled deadline recurrence contexts tags body
        location state
      ], session.edit_form.field_order
      assert_equal [
        "Title", "Priority", "Deferred", "Scheduled", "Deadline", "Recurrence",
        "Contexts", "Tags", "Notes", "Location", "State",
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

  def test_state_location_recurrence_and_coupled_date_changes_require_confirmation
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
      session.form.focus(:location)
      session.form.set_value(:location, "22220001")
      assert session.save.confirmation?
      session.cancel_confirmation!
      assert_equal "11110001", record(org)["parent"]
      assert session.dirty?(:location)
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
