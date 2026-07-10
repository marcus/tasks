# frozen_string_literal: true

require_relative "test_helper"
require "tui/app"
require "tui/text_input"

class TestApp < Minitest::Test
  def ui(app) = app.instance_variable_get(:@ui)

  # Records calls to #start and reports whatever running?/available? state we
  # set, so we can drive submit_prompt without spawning a real agent process.
  class FakeAgent
    attr_reader :started

    def initialize(running:, available: true)
      @running = running
      @available = available
      @started = []
    end

    def running? = @running
    def available? = @available
    def start(text, model:) = @started << [text, model]
  end

  def app_with(agent:, input:)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE_ORG)
      app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir),
                         llm_config: default_llm_config)
      app.instance_variable_set(:@agent, agent)
      app.instance_variable_set(:@input, Tui::TextInput.new(input))
      yield app
    end
  end

  def test_submit_prompt_rejected_while_agent_running
    fake = FakeAgent.new(running: true)
    app_with(agent: fake, input: "reschedule the flight") do |app|
      app.send(:submit_prompt)
      assert_empty fake.started, "must not orphan the in-flight run by starting a second"
      assert_match(/still working/, app.instance_variable_get(:@flash))
      # input is cleared and focus returns to the list even on rejection
      assert_equal "", app.instance_variable_get(:@input)
      assert_equal :list, ui(app).mode
    end
  end

  def test_submit_prompt_ignores_blank_input_without_touching_agent
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "   ") do |app|
      app.send(:submit_prompt)
      assert_empty fake.started
      assert_nil app.instance_variable_get(:@flash)
    end
  end

  def test_submit_prompt_flashes_when_agent_unavailable
    fake = FakeAgent.new(running: false, available: false)
    app_with(agent: fake, input: "do a thing") do |app|
      app.send(:submit_prompt)
      assert_empty fake.started, "must not start an unavailable agent"
      assert_match(/not available/, app.instance_variable_get(:@flash))
    end
  end

  def test_submit_prompt_starts_agent_with_selected_model
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "reschedule the flight") do |app|
      app.send(:submit_prompt)
      assert_equal [["reschedule the flight", "sonnet"]], fake.started
    end
  end

  def test_terminal_size_uses_current_console_dimensions
    fake = FakeAgent.new(running: false)
    console = Struct.new(:winsize).new([13, 47])
    app_with(agent: fake, input: "") do |app|
      IO.stub(:console, console) do
        assert_equal [13, 47], app.send(:terminal_size)
      end
    end
  end

  def test_terminal_size_retains_narrow_but_renderable_dimensions
    fake = FakeAgent.new(running: false)
    console = Struct.new(:winsize).new([7, 11])
    app_with(agent: fake, input: "") do |app|
      IO.stub(:console, console) do
        assert_equal [7, 11], app.send(:terminal_size)
      end
    end
  end

  def test_footer_height_is_calculated_at_the_current_width
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "界 " * 60) do |app|
      ui(app).mode = :prompt
      narrow = app.send(:footer_size, width: 40)
      wide = app.send(:footer_size, width: 120)
      assert_operator narrow, :>, wide
      assert_operator narrow, :<=, Tui::App::PROMPT_MAX
    end
  end

  def test_paint_threads_one_terminal_size_through_frame_geometry
    fake = FakeAgent.new(running: false)
    console = Struct.new(:winsize).new([12, 43])
    captured = nil
    popup_geometry = nil
    builder = lambda do |**args|
      captured = args
      Array.new(args[:height], " " * args[:width])
    end
    popup_builder = lambda do |**args|
      popup_geometry = args
      nil
    end

    app_with(agent: fake, input: "") do |app|
      IO.stub(:console, console) do
        app.stub(:current_popup, popup_builder) do
          Tui::Frame.stub(:build, builder) { capture_io { app.send(:paint) } }
        end
      end
    end
    assert_equal 43, captured[:width]
    assert_equal 12, captured[:height]
    assert_equal 43, popup_geometry[:layout].width
    assert_equal 12, popup_geometry[:layout].height
    assert_equal captured[:footer].size, popup_geometry[:layout].footer_size
  end

  def test_paint_samples_terminal_size_once_during_resize
    fake = FakeAgent.new(running: false)
    calls = 0
    console = Object.new
    console.define_singleton_method(:winsize) do
      calls += 1
      calls == 1 ? [12, 43] : [40, 120]
    end
    captured = nil

    app_with(agent: fake, input: "") do |app|
      IO.stub(:console, console) do
        Tui::Frame.stub(:build, ->(**args) { captured = args; Array.new(args[:height], "") }) do
          capture_io { app.send(:paint) }
        end
      end
    end

    assert_equal 1, calls, "one frame must not mix dimensions across a resize"
    assert_equal [43, 12], captured.values_at(:width, :height)
  end

  def test_prompt_mode_hides_selection_without_scrolling_to_it
    fake = FakeAgent.new(running: false)
    captured = nil
    console = Struct.new(:winsize).new([8, 43])

    app_with(agent: fake, input: "ask") do |app|
      app.send(:rows)
      original_rows = app.instance_variable_get(:@rows).dup
      app.instance_variable_set(:@sel, original_rows.length - 1)
      ui(app).mode = :prompt
      IO.stub(:console, console) do
        Tui::Frame.stub(:build, ->(**args) { captured = args; Array.new(args[:height], "") }) do
          capture_io { app.send(:paint) }
        end
      end

      assert_nil captured[:selected]
      assert_equal 0, captured[:layout].viewport_offset
      assert_equal original_rows.first.item.id, captured[:rows].first.item.id
      assert_equal original_rows.length, captured[:rows].length
    end
  end

  def test_extracted_state_has_no_shadow_app_ivars
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "") do |app|
      extracted = %i[@mode @selected_id @view @filter @collapsed @show_deferred
                     @modal @form @action_palette]
      assert_empty extracted & app.instance_variables
      assert_instance_of Tui::UiState, ui(app)
    end
  end

  def test_popup_placement_uses_supplied_terminal_geometry
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:open_action_palette)
      app.instance_variable_set(:@sel, 99)
      popup = app.send(:current_popup, width: 42, height: 12, footer_size: 3)
      body_width = 42 - 4
      body_height = 12 - 5 - 3
      assert_operator popup[:row], :>=, 0
      assert_operator popup[:row] + popup[:lines].size, :<=, body_height
      assert_operator popup[:col], :>=, 0
      assert popup[:lines].all? { |line| Tui::Ansi.vislen(line) <= body_width },
             "palette is sized from the supplied 42-column terminal body"
    end
  end

  def test_form_popup_remains_visible_inside_an_eight_by_six_terminal
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:open_date_popup)
      popup = app.send(:current_popup, width: 8, height: 6, footer_size: 0)
      assert_equal 0, popup[:row]
      assert_equal 0, popup[:col]
      assert_equal 1, popup[:lines].size
      assert popup[:lines].all? { |line| Tui::Ansi.vislen(line) <= 4 }
    end
  end

  def test_palette_popup_remains_visible_inside_an_eight_by_six_terminal
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:open_action_palette)
      popup = app.send(:current_popup, width: 8, height: 6, footer_size: 0)
      assert_equal 0, popup[:row]
      assert_equal 0, popup[:col]
      assert_equal 1, popup[:lines].size
      assert popup[:lines].all? { |line| Tui::Ansi.vislen(line) <= 4 }
    end
  end

  def test_popup_placement_chooses_below_then_above_and_clamps_column
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "") do |app|
      popup = { lines: ["123456", "abcdef"], row: 99, col: 99 }
      below = Tui::ScreenLayout.new(width: 14, height: 11, footer: [], selected: 1)
                               .place_popup(popup, preferred_col: 8)
      assert_equal [2, 4], below.values_at(:row, :col)

      above = Tui::ScreenLayout.new(width: 14, height: 11, footer: [], selected: 5)
                               .place_popup(popup, preferred_col: 8)
      assert_equal [3, 4], above.values_at(:row, :col)
    end
  end

  def test_short_footer_keeps_active_filter_input_over_generic_hint
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "") do |app|
      ui(app).mode = :filter
      ui(app).filter_input.replace("界")
      footer = app.send(:fitted_footer, width: 8, height: 7)
      assert_equal 1, footer.size
      assert_includes Tui::Ansi.strip(footer.first), "界"
      refute_includes Tui::Ansi.strip(footer.first), "tab to ask"
    end
  end

  # -- deferral ----------------------------------------------------------------

  # Build an app on a sandbox gtd.org (optionally a modified fixture), park it
  # on a given view, and select the row whose item title includes `select`.
  def app_on(view:, select:, content: FIXTURE_ORG)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), content)
      app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir),
                         llm_config: default_llm_config)
      ui(app).view = view
      app.send(:rows)
      rws = app.instance_variable_get(:@rows)
      idx = rws.index { |r| r.item&.title&.include?(select) }
      raise "no selectable row for #{select.inspect}" unless idx
      app.send(:select_row, idx)
      yield app
    end
  end

  def row_titles(app)
    (app.instance_variable_get(:@rows) || []).map { |r| r.item&.title }.compact
  end

  def test_defer_selected_marks_task_and_hides_it
    app_on(view: :next, select: "Water the plants") do |app|
      app.send(:defer_selected)
      store = app.instance_variable_get(:@store)
      assert store.items.find { |i| i.title.include?("Water the plants") }.deferred?
      # hidden by default (show_deferred is off), so it leaves the Next view
      refute_includes row_titles(app), "Water the plants"
      assert_match(/deferred/, app.instance_variable_get(:@flash))
    end
  end

  def test_toggle_deferred_view_reveals_and_hides
    app_on(view: :next, select: "Review PR", content: deferred_fixture) do |app|
      refute_includes row_titles(app), "Water the plants", "deferred hidden by default"
      app.send(:toggle_deferred_view)
      assert ui(app).show_deferred
      assert_includes row_titles(app), "Water the plants", "Z reveals deferred tasks"
      app.send(:toggle_deferred_view)
      refute ui(app).show_deferred
      refute_includes row_titles(app), "Water the plants", "Z again hides them"
    end
  end

  def test_filter_respects_deferred_parent_visibility
    content = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "aaaa0001", "title" => "Work" },
      { "type" => "task", "id" => "aaaa0002", "parent" => "aaaa0001", "state" => "NEXT",
        "title" => "deferred parent", "tags" => %w[defer] },
      { "type" => "task", "id" => "aaaa0003", "parent" => "aaaa0002", "state" => "NEXT",
        "title" => "child match" },
      { "type" => "task", "id" => "aaaa0004", "parent" => "aaaa0001", "state" => "NEXT",
        "title" => "live sibling" },
    ])

    app_on(view: :next, select: "live sibling", content: content) do |app|
      ui(app).filter = "child"
      app.send(:rows)
      refute_includes row_titles(app), "child match",
                      "flat filtering hides descendants of a deferred parent"

      ui(app).show_deferred = true
      app.send(:rows)
      assert_includes row_titles(app), "child match", "Z reveals the filtered descendant"
    end
  end

  def test_defer_selected_reactivates_when_already_deferred
    app_on(view: :next, select: "Review PR", content: deferred_fixture) do |app|
      ui(app).show_deferred = true # so the deferred task is selectable
      app.send(:rows)
      idx = app.instance_variable_get(:@rows).index { |r| r.item&.title&.include?("Water the plants") }
      app.send(:select_row, idx)
      app.send(:defer_selected)
      store = app.instance_variable_get(:@store)
      refute store.items.find { |i| i.title.include?("Water the plants") }.deferred?
      assert_match(/activated/, app.instance_variable_get(:@flash))
    end
  end

  # -- recurrence ------------------------------------------------------------

  RECUR_FIXTURE = dump_fixture([
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "cccc0001", "title" => "Work" },
    { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001", "state" => "NEXT",
      "title" => "Pay rent", "tags" => %w[@home], "deadline" => "2026-08-01", "recur" => "+1m" },
    { "type" => "task", "id" => "cccc0003", "parent" => "cccc0001", "state" => "NEXT",
      "title" => "Standup notes", "tags" => %w[@computer] },
  ])

  def test_open_recur_popup_prefills_current_cookie
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      assert_equal :form, ui(app).mode
      assert_equal :recurrence, ui(app).form.kind
      assert_equal "+1m", ui(app).form.input
    end
  end

  def test_open_recur_popup_refuses_undated_task
    app_on(view: :next, select: "Standup notes", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      assert_equal :list, ui(app).mode, "no popup for a task with no date"
      assert_match(/schedule it first/, app.instance_variable_get(:@flash))
    end
  end

  def test_submit_recur_sets_cookie
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      ui(app).form.input.replace("weekly")
      app.send(:handle_key, "\r")
      store = app.instance_variable_get(:@store)
      assert_equal ".+1w", store.items.find { |i| i.title.include?("Pay rent") }.recur
      assert_equal :list, ui(app).mode
    end
  end

  def test_submit_recur_off_clears
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      ui(app).form.input.replace("off")
      app.send(:handle_key, "\r")
      assert_nil app.instance_variable_get(:@store).items.find { |i| i.title.include?("Pay rent") }.recur
    end
  end

  def test_submit_recur_reports_parse_error
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      ui(app).form.input.replace("bananas")
      app.send(:handle_key, "\r")
      assert_equal :form, ui(app).mode, "stays open on bad input"
      assert_match(/can't parse/, ui(app).form.error)
    end
  end

  def test_complete_selected_rolls_recurring_task_and_keeps_it
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:complete_selected)
      store = app.instance_variable_get(:@store)
      rent = store.items.find { |i| i.title.include?("Pay rent") }
      assert_equal "NEXT", rent.state, "recurring task stays open"
      assert_equal Date.new(2026, 9, 1), rent.deadline
      assert_match(/↻ Pay rent/, app.instance_variable_get(:@flash))
      # still selectable in the agenda view
      assert_includes row_titles(app), "Pay rent"
    end
  end

  # -- stable selection identity ---------------------------------------------

  SELECTION_FIXTURE = dump_fixture([
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "5e1e0001", "title" => "Work" },
    { "type" => "task", "id" => "5e1e0002", "parent" => "5e1e0001", "state" => "NEXT",
      "title" => "Alpha", "deadline" => "2026-07-11" },
    { "type" => "task", "id" => "5e1e0003", "parent" => "5e1e0001", "state" => "NEXT",
      "title" => "Beta", "deadline" => "2026-07-12" },
    { "type" => "task", "id" => "5e1e0004", "parent" => "5e1e0001", "state" => "NEXT",
      "title" => "Gamma", "deadline" => "2026-07-13" },
  ])

  def rewrite_records(app)
    store = app.instance_variable_get(:@store)
    records = Tasks::Format.parse(File.read(store.org, encoding: "UTF-8")).records
    yield records
    File.write(store.org, dump_fixture(records))
    app.send(:reload_store)
  end

  def test_external_resort_retains_selected_task_by_id
    app_on(view: :agenda, select: "Beta", content: SELECTION_FIXTURE) do |app|
      old_row = app.instance_variable_get(:@sel)
      rewrite_records(app) do |records|
        records.find { |record| record["id"] == "5e1e0004" }["deadline"] = "2026-07-10"
      end

      assert_equal "Beta", app.send(:current_item).title
      assert_equal "5e1e0003", ui(app).selected_id
      refute_equal old_row, app.instance_variable_get(:@sel), "render coordinate follows the resort"
    end
  end

  def test_inserting_an_earlier_record_retains_id_across_line_shift
    app_on(view: :agenda, select: "Beta", content: SELECTION_FIXTURE) do |app|
      old_line = app.send(:current_item).line
      rewrite_records(app) do |records|
        records.insert(2,
          { "type" => "task", "id" => "5e1e0005", "parent" => "5e1e0001", "state" => "DONE",
            "title" => "Inserted history", "closed" => "2026-07-09" })
      end

      assert_equal "5e1e0003", app.send(:current_item).id
      assert_operator app.send(:current_item).line, :>, old_line
    end
  end

  def test_deleted_selection_falls_back_to_nearest_row_and_updates_id
    app_on(view: :agenda, select: "Beta", content: SELECTION_FIXTURE) do |app|
      rewrite_records(app) do |records|
        records.reject! { |record| record["id"] == "5e1e0003" }
      end

      assert_equal "Gamma", app.send(:current_item).title
      assert_equal "5e1e0004", ui(app).selected_id
    end
  end

  def test_view_filter_and_navigation_keep_id_synchronized
    app_on(view: :agenda, select: "Book flight", content: FIXTURE_ORG) do |app|
      app.send(:switch_view, 2)
      assert_equal FIX[:flight], app.send(:current_item).id

      ui(app).filter = "flight"
      app.send(:rows)
      assert_equal FIX[:flight], app.send(:current_item).id

      ui(app).filter = nil
      app.send(:rows)
      app.send(:move, 1)
      assert_equal app.send(:current_item).id, ui(app).selected_id
    end
  end

  def test_rebuild_keeps_selected_occurrence_when_task_has_multiple_contexts
    records = Tasks::Format.parse(SELECTION_FIXTURE).records
    beta = records.find { |record| record["id"] == "5e1e0003" }
    beta["tags"] = %w[@alpha @omega]
    content = dump_fixture(records)

    app_on(view: :next, select: "Beta", content: content) do |app|
      app.send(:move, 1)
      second_occurrence = app.instance_variable_get(:@sel)
      assert_equal "5e1e0003", app.send(:current_item).id

      app.send(:rows)
      assert_equal second_occurrence, app.instance_variable_get(:@sel)
      assert_equal "5e1e0003", ui(app).selected_id
    end
  end

  # -- outliner collapse / expand (h l H L) ----------------------------------

  # Work → "Ship release" (07-10) → "write notes" (07-12) → "grandchild task",
  # plus a sibling leaf "undated rider"; Home → "solo top" (07-15), a top-level
  # leaf. Rendered in agenda the rows are, in order: Ship release, write notes,
  # grandchild task, undated rider, solo top.
  NESTED_APP = dump_fixture([
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "aaaa0001", "title" => "Work" },
    { "type" => "task", "id" => "aaaa0002", "parent" => "aaaa0001", "state" => "NEXT",
      "title" => "Ship release", "deadline" => "2026-07-10" },
    { "type" => "task", "id" => "aaaa0003", "parent" => "aaaa0002", "state" => "TODO",
      "title" => "write notes", "deadline" => "2026-07-12" },
    { "type" => "task", "id" => "aaaa0004", "parent" => "aaaa0003", "state" => "NEXT",
      "title" => "grandchild task" },
    { "type" => "task", "id" => "aaaa0005", "parent" => "aaaa0002", "state" => "TODO",
      "title" => "undated rider" },
    { "type" => "section", "id" => "aaaa0006", "title" => "Home" },
    { "type" => "task", "id" => "aaaa0007", "parent" => "aaaa0006", "state" => "NEXT",
      "title" => "solo top", "deadline" => "2026-07-15" },
  ])

  def sel_title(app)
    rws = app.instance_variable_get(:@rows)
    rws[app.instance_variable_get(:@sel)]&.item&.title
  end

  def collapsed(app) = ui(app).collapsed

  def test_collapse_selected_folds_subtree_and_holds_selection
    app_on(view: :agenda, select: "Ship release", content: NESTED_APP) do |app|
      before = row_titles(app).size
      app.send(:collapse_selected)
      titles = row_titles(app)
      assert_equal "Ship release", sel_title(app), "selection stays on the folded parent"
      refute_includes titles, "write notes", "subtree hidden"
      refute_includes titles, "grandchild task"
      refute_includes titles, "undated rider"
      assert_operator titles.size, :<, before, "rows shrank"
      ship = app.instance_variable_get(:@rows).find { |r| r.item&.title == "Ship release" }
      assert_includes Tui::Ansi.strip(ship.text), "(3)", "hidden-descendant count shows"
      assert_includes collapsed(app), "aaaa0002"
    end
  end

  def test_collapse_again_on_top_level_collapsed_is_noop
    app_on(view: :agenda, select: "Ship release", content: NESTED_APP) do |app|
      app.send(:collapse_selected) # fold
      folded = row_titles(app)
      sel = app.instance_variable_get(:@sel)
      app.send(:collapse_selected) # again: parent is a section → no-op
      assert_equal folded, row_titles(app)
      assert_equal sel, app.instance_variable_get(:@sel)
      assert_equal "Ship release", sel_title(app)
    end
  end

  def test_collapse_again_on_folded_child_jumps_to_parent
    app_on(view: :agenda, select: "write notes", content: NESTED_APP) do |app|
      app.send(:collapse_selected) # write notes has a child → folds
      assert_equal "write notes", sel_title(app)
      assert_includes collapsed(app), "aaaa0003"
      app.send(:collapse_selected) # folded now → climb to parent
      assert_equal "Ship release", sel_title(app)
    end
  end

  def test_collapse_on_leaf_jumps_to_parent
    app_on(view: :agenda, select: "grandchild task", content: NESTED_APP) do |app|
      app.send(:collapse_selected)
      assert_equal "write notes", sel_title(app), "leaf climbs to its parent row"
      assert_empty collapsed(app), "a leaf never folds anything"
    end
  end

  def test_collapse_on_top_level_leaf_is_noop
    app_on(view: :agenda, select: "solo top", content: NESTED_APP) do |app|
      before = row_titles(app)
      app.send(:collapse_selected)
      assert_equal "solo top", sel_title(app)
      assert_equal before, row_titles(app)
      assert_empty collapsed(app)
    end
  end

  def test_expand_selected_unfolds_and_holds_selection
    app_on(view: :agenda, select: "Ship release", content: NESTED_APP) do |app|
      app.send(:collapse_selected)
      refute_includes row_titles(app), "write notes"
      app.send(:expand_selected)
      assert_includes row_titles(app), "write notes", "subtree back"
      assert_equal "Ship release", sel_title(app)
      assert_empty collapsed(app)
    end
  end

  def test_expand_selected_on_expanded_node_is_noop
    app_on(view: :agenda, select: "Ship release", content: NESTED_APP) do |app|
      before = row_titles(app)
      app.send(:expand_selected) # nothing folded → no-op
      assert_equal before, row_titles(app)
      assert_empty collapsed(app)
    end
  end

  def test_collapse_all_folds_every_parent
    app_on(view: :agenda, select: "grandchild task", content: NESTED_APP) do |app|
      app.send(:collapse_all)
      set = collapsed(app)
      assert_includes set, "aaaa0002", "Ship release folded"
      assert_includes set, "aaaa0003", "write notes folded"
      refute_includes set, "aaaa0004", "the leaf grandchild is not a parent"
      refute_includes set, "aaaa0007", "the top-level leaf is not a parent"
      titles = row_titles(app)
      refute_includes titles, "write notes"
      refute_includes titles, "grandchild task"
      assert_includes titles, "Ship release"
      assert_includes titles, "solo top"
      # the selection sat on a now-hidden row; clamp lands it on a visible task
      landed = app.instance_variable_get(:@rows)[app.instance_variable_get(:@sel)]
      assert landed&.item, "selection clamps onto a visible task"
    end
  end

  def test_expand_all_restores_full_tree
    app_on(view: :agenda, select: "Ship release", content: NESTED_APP) do |app|
      app.send(:collapse_all)
      app.send(:expand_all)
      assert_empty collapsed(app)
      titles = row_titles(app)
      ["Ship release", "write notes", "grandchild task", "undated rider", "solo top"].each do |t|
        assert_includes titles, t
      end
    end
  end

  def test_collapse_expand_do_not_crash_during_filter
    app_on(view: :agenda, select: "Ship release", content: NESTED_APP) do |app|
      ui(app).filter = "e" # flat path: rows carry no node
      app.send(:rows)
      before = row_titles(app)
      app.send(:collapse_selected) # node nil → no-op
      app.send(:expand_selected)   # node nil → no-op
      assert_equal before, row_titles(app), "flat filter rows unchanged by h/l"
      # H/L still touch the store tree, but the flat filter rows don't change.
      app.send(:collapse_all)
      app.send(:expand_all)
      assert_equal before, row_titles(app)
    end
  end
end
