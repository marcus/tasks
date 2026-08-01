# frozen_string_literal: true

require_relative "test_helper"
require "tasks/application"
require "tui/app"
require "tui/export"
require "tui/task_details"
require "tui/task_editor_session"

# TUI coverage for lead time: the editor field (ownership, validation, the
# resolved date it displays), the read surfaces that render the window, and the
# picker/list behavior a lead-gated row inherits from the existing timed-
# unavailable path rather than from anything new.
class TestTuiLead < Minitest::Test
  def ui(app) = app.instance_variable_get(:@ui)

  LEAD_TREE = [
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "11110001", "title" => "One" },
    { "type" => "task", "id" => "11110002", "parent" => "11110001", "state" => "NEXT",
      "title" => "Renew the passport", "deadline" => "2026-11-01" },
  ].freeze

  # A fixture whose lead-gated row is hidden today, plus an ordinary sibling.
  APP_RECORDS = [
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "22220001", "title" => "Work" },
    { "type" => "task", "id" => "22220002", "parent" => "22220001", "state" => "NEXT",
      "title" => "Renew the passport", "deadline" => "2036-11-01", "lead" => "3w" },
    { "type" => "task", "id" => "22220003", "parent" => "22220001", "state" => "NEXT",
      "title" => "Review PR backlog" },
  ].freeze

  # -- the editor field ------------------------------------------------------

  def test_lead_field_sits_beside_recurrence_and_writes_only_lead
    with_editor do |session, _store, org|
      assert_equal %i[scheduled deadline recurrence lead],
                   session.edit_form.field_order.select { |key| %i[scheduled deadline recurrence lead].include?(key) }

      session.form.focus(:lead)
      session.form.set_value(:lead, "3 weeks")
      assert_equal "3w", session.edit_form.semantic_value(:lead)

      pending = session.save
      assert pending.confirmation?
      assert_equal "Hide this task until 3 weeks before its deadline (3w)?", pending.message
      session.confirm!

      record = record(org)
      assert_equal "3w", record["lead"]
      assert_equal "2026-11-01", record["deadline"], "the save-on-blur field owns lead and nothing else"
      assert_equal "NEXT", record["state"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_lead_field_clears_with_off_or_an_empty_buffer
    with_editor(records: lead_tree(lead: "3w")) do |session, _store, org|
      session.form.focus(:lead)
      session.form.set_value(:lead, "off")
      assert_nil session.edit_form.semantic_value(:lead),
                 "a clearing word reads as an empty field, so the prompt says clear"
      pending = session.save
      assert pending.confirmation?
      assert_equal "Clear the lead time?", pending.message
      session.confirm!
      refute record(org).key?("lead")
    end
  end

  def test_lead_field_reports_the_engines_reason_and_the_two_rules_it_can_see
    with_editor do |session, _store, _org|
      session.form.focus(:lead)
      session.form.set_value(:lead, "soonish")
      assert_includes session.form.validate[:lead].join, "unrecognized lead time"

      session.form.set_value(:lead, "5h")
      assert_nil session.form.validate[:lead], "a clock lead is valid input"
      assert_equal "5h", session.edit_form.semantic_value(:lead)

      # Rule 3, visible from the form's own buffers: a second timed gate.
      session.form.set_value(:scheduled, Tasks::TemporalValue.new(date: Date.new(2026, 10, 1)))
      session.form.set_value(:lead, "3w")
      assert_includes session.form.validate[:lead].join, "measures from the deadline"

      # Rule 1, same way: nothing to hide before.
      session.form.set_value(:scheduled, nil)
      session.form.set_value(:deadline, nil)
      assert_includes session.form.validate[:lead].join, "needs an Available from date or deadline"
    end
  end

  def test_committed_lead_renders_as_prose_with_its_resolved_date
    with_editor(records: lead_tree(lead: "3w")) do |session, _store, _org|
      row = ->(model) { model.rows.find { |r| r.key == :lead } }

      blurred = row.call(session.render_model)
      assert_equal "3w", blurred.value
      assert_equal "3 weeks before — opens 2026-10-11", blurred.metadata[:text]

      session.form.focus(:lead)
      assert_nil row.call(session.render_model).metadata[:text], "focused editing shows the raw span"

      session.form.focus(:title)
      session.form.set_value(:lead, "2w")
      assert_nil row.call(session.render_model).metadata[:text], "a dirty buffer is not rendered as prose"
    end
  end

  # -- read surfaces ---------------------------------------------------------

  def test_details_panel_and_export_render_the_span_and_the_derived_date
    with_query(lead_tree(lead: "3w")) do |query, item|
      panel = Tui::TaskDetails.build(query.task(item), [], 60, today: Date.new(2026, 7, 1))
      rendered = panel.fetch(:lines).join("\n")
      assert_match(/lead time/, rendered)
      assert_match(/3 weeks before — opens 2026-10-11/, rendered)

      markdown = Tui::Export.markdown(query.task(item), [])
      assert_match(/- lead time: 3 weeks before — opens 2026-10-11/, markdown)
    end
  end

  def test_a_lead_gated_row_reuses_the_existing_timed_unavailable_marker
    app_on(view: :next, select: "Review PR") do |app|
      refute_includes row_titles(app), "Renew the passport", "hidden like any timed-unavailable row"

      app.send(:toggle_deferred_view)
      assert_includes row_titles(app), "Renew the passport"
      row = app.instance_variable_get(:@rows).find { |r| r.item&.title == "Renew the passport" }
      rendered = Tui::Views.badge(row.item, reader: app.send(:read_model), today: Date.today)
      assert_match(/⏳/, rendered, "the timed-unavailable marker, not a new one")
      assert_match(%r{10/11}, rendered, "carrying the DERIVED date, which no stamp holds")
    end
  end

  # The gate before the clock-unit work (td-556c53): an idle TUI must notice a
  # gate INSTANT passing on its own. It already does — every minute boundary
  # invalidates the read model — so this is verification, not new behavior.
  def test_an_idle_tui_notices_a_gate_instant_passing_without_a_reload
    app_on(view: :next, select: "Review PR") do |app|
      refute_includes row_titles(app), "Renew the passport"

      # Move the window into the past exactly as the clock passing it would,
      # then let one idle tick's minute check run.
      store = app.instance_variable_get(:@store)
      snapshot = store.edit_snapshot("22220002")
      result = store.patch_task!(
        Tasks::TaskPatch.from(snapshot, field: :deadline, value: Date.today + 1)
      )
      assert result.ok?, result.errors.join(", ")

      app.instance_variable_set(:@read_model_minute, -1)
      assert app.send(:idle_layout_changed?), "a minute boundary invalidates the read model"
      app.send(:rows)
      assert_includes row_titles(app), "Renew the passport",
                     "the row appears without a manual reload"
    end
  end

  # -- picker ----------------------------------------------------------------

  def test_the_picker_refuses_a_timed_choice_and_releases_one_occurrence_with_now
    app_on(view: :next, select: "Review PR") do |app|
      app.send(:toggle_deferred_view)
      select(app, "Renew the passport")

      app.send(:defer_selected)
      ui(app).form.input.replace("2036-09-01")
      app.send(:handle_key, "\r")
      assert_match(/already hides until 3 weeks before/, ui(app).form.error.to_s)

      ui(app).form.input.replace("now")
      app.send(:handle_key, "\r")
      task = app.instance_variable_get(:@store).items.find { |item| item.id == "22220002" }
      assert_equal "2036-11-01", task.deadline.iso8601, "the anchor survives"
      assert_equal "2036-11-01", task.lead_skip, "exactly this occurrence is released"
      assert_equal "3w", task.lead, "the window itself survives"
      assert_match(/available now/, app.instance_variable_get(:@flash))
    end
  end

  def test_someday_still_holds_a_lead_task_indefinitely
    app_on(view: :next, select: "Review PR") do |app|
      app.send(:toggle_deferred_view)
      select(app, "Renew the passport")

      app.send(:defer_selected)
      ui(app).form.input.replace("someday")
      app.send(:handle_key, "\r")
      task = app.instance_variable_get(:@store).items.find { |item| item.id == "22220002" }
      assert task.deferred?, "an indefinite hold is unaffected by rule 3"
      assert_match(/on hold/, app.instance_variable_get(:@flash))
    end
  end

  private

  def lead_tree(lead: nil)
    records = LEAD_TREE.map(&:dup)
    records.last["lead"] = lead if lead
    records
  end

  def with_editor(records: LEAD_TREE, today: Date.new(2026, 7, 1))
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(records))
      store = Tasks::Store.new(org: org, archive: archive)
      application = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive)
      )
      session = Tui::TaskEditorSession.new(
        store: store, application: application, target_id: "11110002", today: today
      )
      yield session, store, org
    end
  end

  def with_query(records)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(records))
      store = Tasks::Store.new(org: org, archive: archive)
      query = Tasks::TaskQueries.new(store.read_snapshot, today: Date.new(2026, 7, 1))
      item = query.snapshot.items.find { |candidate| candidate.id == "11110002" }
      yield query, item
    end
  end

  def record(path, id = "11110002")
    Tasks::Format.parse(File.read(path, encoding: "UTF-8")).records.find { |entry| entry["id"] == id }
  end

  def select(app, title)
    rows = app.instance_variable_get(:@rows)
    index = rows.index { |row| row.item&.title == title } or raise "no row for #{title}"
    app.send(:select_row, index)
  end

  def app_on(view:, select:)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), Tasks::Format.dump(APP_RECORDS))
      paths = Tasks::Config.for_dir(dir)
      app = Tui::App.new(root: dir, paths: paths, llm_config: default_llm_config)
      ui(app).view = view
      app.send(:rows)
      rows = app.instance_variable_get(:@rows)
      index = rows.index { |row| row.item&.title&.include?(select) } or raise "no row for #{select}"
      app.send(:select_row, index)
      yield app
    end
  end

  def row_titles(app)
    (app.instance_variable_get(:@rows) || []).map { |row| row.item&.title }.compact
  end
end
