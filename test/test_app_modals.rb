# frozen_string_literal: true

require_relative "test_helper"
require "tui/app"

# Exercises App's panel, modal, and popup handling through its private
# interface — the pieces a pty smoke test can't assert on.
class TestAppModals < Minitest::Test
  A = Tui::Ansi

  class QueueAgent
    attr_reader :started, :output

    def initialize
      @started = []
      @output = +""
    end

    def available? = true
    def start(prompt, model:) = (@started << [prompt, model]; self)
    def io = nil
  end

  def with_app(content: FIXTURE_ORG, agent_factory: nil)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), content)
      app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir),
                         llm_config: default_llm_config, agent_factory: agent_factory,
                         agent_probe: ->(_entry) { true })
      app.send(:rows) # populate @rows like the paint loop does
      app.send(:clamp_selection)
      yield app
    end
  end

  def ui(app) = app.instance_variable_get(:@ui)
  def mode(app)  = ui(app).mode
  def modal(app) = ui(app).modal
  def panel(app) = ui(app).panel
  def modal_text(app) = modal(app).lines.map { |l| A.strip(l) }.join("\n")
  def panel_text(app) = panel(app).lines.map { |line| A.strip(line) }.join("\n")
  def selected_title(app) = app.send(:current_item).title

  def rewrite_records(app)
    store = app.instance_variable_get(:@store)
    records = Tasks::Format.parse(File.read(store.org, encoding: "UTF-8")).records
    yield records
    File.write(store.org, dump_fixture(records))
    app.send(:reload_store)
  end

  def test_enter_opens_detail_panel_for_selection
    with_app do |app|
      app.send(:handle_key, "\r")
      assert_equal :list, mode(app)
      assert_nil modal(app)
      assert_equal :detail, panel(app).kind
      assert_includes panel_text(app), selected_title(app)
    end
  end

  def test_arrows_walk_tasks_while_detail_panel_stays_open
    with_app do |app|
      app.send(:handle_key, "\r")
      before = selected_title(app)
      app.send(:handle_key, "\e[B")
      assert_equal :list, mode(app)
      assert_equal :detail, panel(app).kind, "panel stays open"
      refute_equal before, selected_title(app), "selection moved"
      assert_includes panel_text(app), selected_title(app), "panel follows selection"
      app.send(:handle_key, "\e[A")
      assert_equal before, selected_title(app)
    end
  end

  def test_external_resort_keeps_detail_bound_to_original_task_id
    with_app do |app|
      app.send(:handle_key, "\r")
      selected_id = app.send(:current_item).id
      rewrite_records(app) do |records|
        records.find { |record| record["id"] == FIX[:flight] }["deadline"] = "2026-08-20"
      end

      assert_equal :list, mode(app)
      assert_equal selected_id, app.send(:current_item).id
      assert_equal selected_id, panel(app).identity
      assert_includes panel_text(app), "Book flight in Concur"
    end
  end

  def test_external_hide_keeps_panel_open_on_fallback_neighbor
    with_app do |app|
      app.send(:handle_key, "\r")
      rewrite_records(app) do |records|
        flight = records.find { |record| record["id"] == FIX[:flight] }
        flight["state"] = "DONE"
        flight["closed"] = "2026-07-10"
      end

      assert_equal :list, mode(app)
      assert_nil modal(app)
      refute_equal FIX[:flight], app.send(:current_item).id
      assert_equal app.send(:current_item).id, ui(app).selected_id
      assert_equal app.send(:current_item).id, panel(app).identity
      assert_includes panel_text(app), selected_title(app)
    end
  end

  def test_arrows_scroll_help_modal_without_moving_selection
    with_app do |app|
      IO.stub(:console, nil) do # pin the 24x80 default so the help modal overflows
        before = selected_title(app)
        app.send(:handle_key, "?")
        assert_equal :modal, mode(app)
        app.send(:handle_key, "\e[B")
        assert_equal before, selected_title(app), "help modal must not move selection"
        assert_equal 1, modal(app).scroll
      end
    end
  end

  def test_vim_scroll_keys_page_and_half_page_in_help_modal
    with_app do |app|
      IO.stub(:console, nil) do # pin the 24x80 default so the help modal overflows
        app.send(:handle_key, "?")
        viewport = modal(app).viewport(app.send(:modal_body_h))
        app.send(:handle_key, "\x04") # ctrl-d: half page down
        assert_equal [viewport / 2, 1].max, modal(app).scroll
        app.send(:handle_key, "\x15") # ctrl-u: back up
        assert_equal 0, modal(app).scroll
        app.send(:handle_key, "\x06") # ctrl-f: full page
        assert_equal viewport, modal(app).scroll
        app.send(:handle_key, "\x02") # ctrl-b
        assert_equal 0, modal(app).scroll
        app.send(:handle_key, "\e[6~") # pgdn = page too
        assert_equal viewport, modal(app).scroll
        app.send(:handle_key, "\e[5~")
        assert_equal 0, modal(app).scroll
      end
    end
  end

  def test_vim_scroll_keys_scroll_detail_panel_without_moving_selection
    with_app do |app|
      IO.stub(:console, Struct.new(:winsize).new([10, 80])) do
        app.send(:handle_key, "\r")
        before = selected_title(app)
        app.send(:handle_key, "\x04")
        assert_equal before, selected_title(app), "ctrl-d scrolls the panel, not the task list"
        assert_equal :list, mode(app)
        assert_operator panel(app).scroll, :>, 0
      end
    end
  end

  def test_slash_filters_help_modal_live
    with_app do |app|
      app.send(:handle_key, "?")
      total = modal(app).lines.size
      app.send(:handle_key, "/")
      assert_equal :modal_filter, mode(app)
      "yank".chars.each { |c| app.send(:handle_key, c) }
      matches = modal(app).lines.map { |l| A.strip(l) }
      assert_operator matches.size, :<, total
      assert matches.all? { |l| l.downcase.include?("yank") }

      app.send(:handle_key, "\r") # enter keeps the filter
      assert_equal :modal, mode(app)
      assert_equal "yank", modal(app).filter

      app.send(:handle_key, "/") # `/` again edits it
      app.send(:handle_key, "\e") # esc clears entirely
      assert_equal :modal, mode(app)
      assert_nil modal(app).filter
      assert_equal total, modal(app).lines.size
    end
  end

  def test_modal_filter_input_renders_inside_modal_not_the_footer
    with_app do |app|
      app.send(:handle_key, "?")
      app.send(:handle_key, "/")
      "yank".chars.each { |c| app.send(:handle_key, c) }

      filter_line = app.send(:modal_filter_line)
      refute_nil filter_line, "a filterable modal exposes a filter line"
      assert_includes A.strip(filter_line), "/ yank"

      view = app.send(:modal_view, app.send(:modal_body_h))
      assert_includes A.strip(view[:lines].first), "/ yank",
                      "the filter input renders on the modal's top line"

      footer = app.send(:footer, 80).map { |f| f.is_a?(String) ? A.strip(f) : f }
      refute(footer.any? { |f| f.is_a?(String) && f.include?("/ yank") },
             "the filter input must not leak into the main prompt/footer area")
    end
  end

  def test_modal_keeps_height_as_filter_shrinks_matches
    with_app do |app|
      IO.stub(:console, nil) do # pin the 24x80 default so the help modal overflows
        app.send(:handle_key, "?")
        body_h = app.send(:modal_body_h)
        full = app.send(:modal_view, body_h)[:lines].size
        app.send(:handle_key, "/")
        "yank".chars.each { |c| app.send(:handle_key, c) }
        assert_operator modal(app).lines.size, :<, full, "the filter really narrows the list"
        filtered = app.send(:modal_view, body_h)[:lines].size
        assert_equal full, filtered, "the modal box keeps its height while matches shrink"
      end
    end
  end

  def test_slash_filters_task_list_while_detail_panel_remains_open
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "/")
      assert_equal :filter, mode(app)
      assert_equal :detail, panel(app).kind
    end
  end

  def test_paste_while_filtering_modal_applies_live
    with_app do |app|
      app.send(:handle_key, "?")
      app.send(:handle_key, "/")
      app.instance_variable_set(:@key_data, "\e[200~quit\e[201~")
      app.send(:drain_key_data)
      assert_equal :modal_filter, mode(app)
      assert_equal "quit", modal(app).filter
    end
  end

  def test_esc_closes_detail_panel_and_stays_in_list
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\e")
      assert_equal :list, mode(app)
      assert_nil modal(app)
      assert_nil panel(app)
    end
  end

  def test_x_previews_archive_counts_before_any_write
    with_app do |app|
      store = app.instance_variable_get(:@store)
      before = File.read(store.org)

      app.send(:handle_key, "x")

      assert_equal :modal, mode(app)
      assert_equal :archive_confirm, modal(app).kind
      assert_includes modal_text(app), "1 completed root"
      assert_includes modal_text(app), "0 descendants"
      assert_includes modal_text(app), "Press y to archive"
      assert_equal before, File.read(store.org)
      refute File.exist?(store.archive)
    end
  end

  def test_archive_confirmation_y_moves_candidates
    with_app do |app|
      store = app.instance_variable_get(:@store)
      app.send(:handle_key, "x")
      app.send(:handle_key, "y")

      assert_equal :list, mode(app)
      assert_nil record_for(store.org, title: "Old finished thing")
      assert record_for(store.archive, title: "Old finished thing")
      assert_match(/archived 1 root/, app.instance_variable_get(:@flash))
    end
  end

  def test_archive_confirmation_refuses_when_preview_changed
    with_app do |app|
      store = app.instance_variable_get(:@store)
      app.send(:handle_key, "x")

      records = Tasks::Format.parse(File.read(store.org, encoding: "UTF-8")).records
      old = records.find { |record| record["title"] == "Old finished thing" }
      old["state"] = "NEXT"
      old.delete("closed")
      replacement = records.find { |record| record["title"] == "Water the plants" }
      replacement["state"] = "DONE"
      replacement["closed"] = "2026-07-10"
      File.write(store.org, dump_fixture(records))
      app.send(:handle_key, "y")

      assert_equal :list, mode(app)
      assert record_for(store.org, title: "Old finished thing")
      assert record_for(store.org, title: "Water the plants")
      refute File.exist?(store.archive)
      assert_match(/task list changed/, app.instance_variable_get(:@flash))
    end
  end

  def test_archive_confirmation_n_and_escape_are_no_ops
    %w[n escape].each do |answer|
      with_app do |app|
        store = app.instance_variable_get(:@store)
        before = File.read(store.org)
        app.send(:handle_key, "x")
        app.send(:handle_key, answer == "escape" ? "\e" : answer)

        assert_equal :list, mode(app)
        assert_equal before, File.read(store.org)
        refute File.exist?(store.archive)
        assert_match(/cancelled/, app.instance_variable_get(:@flash))
      end
    end
  end

  def test_archive_blocked_modal_names_open_descendant_and_cannot_confirm
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "accc0001", "title" => "Projects" },
      { "type" => "task", "id" => "accc0002", "parent" => "accc0001", "state" => "DONE",
        "title" => "Closed project", "closed" => "2026-07-01" },
      { "type" => "task", "id" => "accc0003", "parent" => "accc0002", "state" => "NEXT",
        "title" => "Open child" },
    ]

    with_app(content: dump_fixture(records)) do |app|
      store = app.instance_variable_get(:@store)
      before = File.read(store.org)
      app.send(:handle_key, "x")

      assert_equal :archive_blocked, modal(app).kind
      assert_includes modal_text(app), "1 open descendant"
      assert_includes modal_text(app), "Open child"
      app.send(:handle_key, "y")
      assert_equal before, File.read(store.org)
      refute File.exist?(store.archive)
    end
  end

  def test_left_right_cycle_views_in_list_mode
    with_app do |app|
      views = []
      5.times do
        views << ui(app).view
        app.send(:handle_key, "\e[C")
      end
      assert_equal %i[agenda next quadrants inbox projects], views
      assert_equal :agenda, ui(app).view, "wraps around"
      app.send(:handle_key, "\e[D")
      assert_equal :projects, ui(app).view, "left wraps backward"
    end
  end

  def test_arrows_cycle_views_and_refresh_open_detail_panel
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\e[C")
      assert_equal :next, ui(app).view
      assert_equal :list, mode(app)
      assert_equal app.send(:current_item).id, panel(app).identity
      assert_includes panel_text(app), selected_title(app)
    end
  end

  def test_detail_panel_remains_available_across_all_five_views
    with_app do |app|
      app.send(:handle_key, "\r")
      (1..5).each do |number|
        app.send(:handle_key, number.to_s)
        assert_equal Tui::Views::TABS[number - 1].last, ui(app).view
        assert_equal :detail, panel(app).kind
        assert_equal app.send(:current_item).id, panel(app).identity
        assert_includes panel_text(app).tr("\n", " "), selected_title(app)
      end
    end
  end

  def test_yank_works_with_detail_panel_open
    with_app do |app|
      copied = []
      Tui::Clipboard.stub(:copy, ->(text, **) { copied << text; true }) do
        app.send(:handle_key, "y")                      # list mode: ref
        app.send(:handle_key, "\r")                     # open detail panel
        app.send(:handle_key, "Y")                      # markdown while panel is open
        assert_equal :list, mode(app)
        assert_equal :detail, panel(app).kind, "yank must not close the panel"
      end
      assert_equal 2, copied.size
      assert_equal selected_title(app), copied[0]
      assert_includes copied[1], "## #{selected_title(app)}"
      assert_includes copied[1], "- state:"
    end
  end

  def test_yank_with_no_clipboard_tool_flashes_error
    with_app do |app|
      Tui::Clipboard.stub(:copy, false) do
        app.send(:handle_key, "y")
      end
      assert_match(/no clipboard tool/, app.instance_variable_get(:@flash))
    end
  end

  def test_priority_bump_down_and_up
    with_app do |app|
      title = selected_title(app)
      assert_equal "A", app.send(:current_item).priority
      app.send(:handle_key, "J")
      assert_equal "B", app.send(:current_item).priority
      assert_equal title, selected_title(app), "selection follows the task after re-sort"
      app.send(:handle_key, "K")
      assert_equal "A", app.send(:current_item).priority
      app.send(:handle_key, "K") # already at A — no-op
      assert_equal "A", app.send(:current_item).priority
    end
  end

  def test_priority_lowers_to_none_and_stops
    with_app do |app|
      3.times { app.send(:handle_key, "J") } # A → B → C → none
      assert_nil app.send(:current_item).priority
      app.send(:handle_key, "J") # no-op at the bottom
      assert_nil app.send(:current_item).priority
    end
  end

  def test_priority_bump_refreshes_detail_panel
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "J")
      assert_equal :list, mode(app)
      assert_equal "B", app.send(:current_item).priority
      assert_match(/priority\s+\[#B\]/, panel_text(app), "panel content refreshed")
    end
  end

  def test_complete_with_detail_panel_open_follows_next_selection
    with_app do |app|
      app.send(:handle_key, "\r")                 # open detail on the selection
      title = selected_title(app)
      app.send(:handle_key, "c")                  # complete it
      assert_equal :list, mode(app)
      assert_nil modal(app)
      app.send(:rows)
      titles = app.instance_variable_get(:@rows).map { |r| r.item&.title }
      refute_includes titles, title, "completed task gone from the open view"
      assert_equal app.send(:current_item).id, panel(app).identity
      assert_includes panel_text(app), selected_title(app)
    end
  end

  def test_reschedule_from_detail_panel_updates_and_stays_open
    with_app do |app|
      app.send(:handle_key, "\r")                 # detail on Book flight (has deadline)
      title = selected_title(app)
      app.send(:handle_key, "d")                  # reschedule
      assert_equal :form, mode(app)
      assert_equal :date, ui(app).form.kind
      assert_equal :detail, panel(app).kind,
                   "the detail panel stays open behind the date popup"
      "2026-07-20".chars.each { |c| app.send(:handle_key, c) }
      app.send(:handle_key, "\r")                 # submit
      assert_equal :list, mode(app), "returns to the list with the detail panel open"
      assert_equal title, selected_title(app), "cursor still on the task"
      assert_equal Date.new(2026, 7, 20), app.send(:current_item).deadline
      assert_includes panel_text(app), "2026-07-20", "panel shows the new deadline"
    end
  end

  def test_esc_during_reschedule_returns_to_list_with_panel
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "d")
      assert_equal :form, mode(app)
      app.send(:handle_key, "\e")                 # cancel the date entry
      assert_equal :list, mode(app)
      assert_equal :detail, panel(app).kind
    end
  end

  def test_stale_form_write_keeps_error_visible_above_detail_panel
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "d")
      app.send(:handle_paste, "2026-07-20")
      store = app.instance_variable_get(:@store)
      store.stub(:patch_task!, Tasks::MutationResult.new(status: :conflict)) do
        app.send(:handle_key, "\r")
      end

      assert_equal :form, mode(app)
      assert_equal :detail, panel(app).kind
      assert_match(/file changed underneath/, ui(app).form.error)
      refute_nil app.send(:current_popup), "the stale-write error remains visible"
    end
  end

  def test_recurrence_form_submit_from_detail_refreshes_panel
    with_app do |app|
      app.send(:handle_key, "\r")
      item_id = app.send(:current_item).id
      app.send(:handle_key, "r")
      assert_equal :form, mode(app)
      assert_equal :recurrence, ui(app).form.kind
      app.send(:handle_paste, "weekly")
      app.send(:handle_key, "\r")

      assert_equal :list, mode(app)
      assert_equal :detail, panel(app).kind
      assert_equal ".+1w", app.instance_variable_get(:@store).items.find { |item| item.id == item_id }.recur
    end
  end

  def test_p_pastes_quoted_ref_into_prompt
    with_app do |app|
      title = selected_title(app)
      app.send(:handle_key, "p")
      assert_equal :prompt, mode(app)
      assert_equal "\"#{title}\" ", app.instance_variable_get(:@input)
    end
  end

  def test_undo_redo_roundtrip_with_flashes
    with_app do |app|
      app.send(:handle_key, "J")
      assert_equal "B", app.send(:current_item).priority

      app.send(:handle_key, "u")
      assert_equal "A", app.send(:current_item).priority
      assert_match(/undid: priority/, app.instance_variable_get(:@flash))

      app.send(:handle_key, "\x12")
      assert_equal "B", app.send(:current_item).priority
      assert_match(/redid: priority/, app.instance_variable_get(:@flash))
    end
  end

  def test_undo_with_nothing_flashes
    with_app do |app|
      app.send(:handle_key, "u")
      assert_match(/nothing to undo/, app.instance_variable_get(:@flash))
      app.send(:handle_key, "\x12")
      assert_match(/nothing to redo/, app.instance_variable_get(:@flash))
    end
  end

  def test_undo_with_detail_panel_refreshes_content
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "J")
      assert_match(/priority\s+\[#B\]/, panel_text(app))
      app.send(:handle_key, "u")
      assert_equal :list, mode(app)
      assert_match(/priority\s+\[#A\]/, panel_text(app), "panel shows undone state")
    end
  end

  def test_undo_of_complete_restores_task_to_view
    with_app do |app|
      title = selected_title(app)
      count = app.instance_variable_get(:@rows).size
      app.send(:handle_key, "c") # complete: task leaves the agenda
      app.send(:rows)
      assert_equal count - 1, app.instance_variable_get(:@rows).size
      app.send(:handle_key, "u")
      titles = app.instance_variable_get(:@rows).map { |r| r.item&.title }
      assert_includes titles, title
    end
  end

  def test_p_from_panel_keeps_it_open_and_appends_to_existing_input
    with_app do |app|
      app.instance_variable_get(:@input) << "complete"
      app.send(:handle_key, "\r")
      title = selected_title(app)
      app.send(:handle_key, "p")
      assert_equal :prompt, mode(app)
      assert_nil modal(app)
      assert_equal :detail, panel(app).kind
      assert_equal "complete \"#{title}\" ", app.instance_variable_get(:@input)
    end
  end

  def test_slash_filters_live_while_typing
    with_app do |app|
      all = app.instance_variable_get(:@rows).count(&:item)
      app.send(:handle_key, "/")
      assert_equal :filter, mode(app)
      "flight".chars.each { |c| app.send(:handle_key, c) }
      app.send(:rows)
      titles = app.instance_variable_get(:@rows).select(&:item).map { |r| r.item.title }
      assert_equal ["Book flight in Concur"], titles
      assert_operator all, :>, 1
    end
  end

  def test_filter_is_case_insensitive
    with_app do |app|
      app.send(:handle_key, "/")
      "FLIGHT".chars.each { |c| app.send(:handle_key, c) }
      app.send(:rows)
      assert_equal 1, app.instance_variable_get(:@rows).count(&:item)
    end
  end

  def test_enter_commits_filter_and_it_survives_view_switch
    with_app do |app|
      app.send(:handle_key, "/")
      "flight".chars.each { |c| app.send(:handle_key, c) }
      app.send(:handle_key, "\r")
      assert_equal :list, mode(app)
      assert_equal "flight", ui(app).filter

      app.send(:handle_key, "2") # Next view
      titles = app.instance_variable_get(:@rows).select(&:item).map { |r| r.item.title }
      assert_equal ["Book flight in Concur"], titles
    end
  end

  def test_esc_while_typing_clears_filter
    with_app do |app|
      all = app.instance_variable_get(:@rows).count(&:item)
      app.send(:handle_key, "/")
      "flight".chars.each { |c| app.send(:handle_key, c) }
      app.send(:handle_key, "\e")
      assert_equal :list, mode(app)
      assert_nil ui(app).filter
      app.send(:rows)
      assert_equal all, app.instance_variable_get(:@rows).count(&:item)
    end
  end

  def test_esc_in_list_mode_clears_committed_filter
    with_app do |app|
      app.send(:handle_key, "/")
      "flight".chars.each { |c| app.send(:handle_key, c) }
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\e")
      assert_nil ui(app).filter
      assert_match(/filter cleared/, app.instance_variable_get(:@flash))
    end
  end

  def test_slash_with_active_filter_edits_it
    with_app do |app|
      app.send(:handle_key, "/")
      "flight".chars.each { |c| app.send(:handle_key, c) }
      app.send(:handle_key, "\r")
      app.send(:handle_key, "/")
      assert_equal "flight", ui(app).filter_input
    end
  end

  def test_no_match_filter_shows_empty_view_not_crash
    with_app do |app|
      app.send(:handle_key, "/")
      "zzzznope".chars.each { |c| app.send(:handle_key, c) }
      rows = app.send(:rows)
      assert_equal 0, rows.count(&:item)
      app.send(:clamp_selection) # must not raise on empty selectables
    end
  end

  def test_prompt_expands_up_to_five_lines_when_input_wraps
    with_app do |app|
      app.send(:handle_key, "\t") # focus prompt
      app.instance_variable_get(:@input) << ("please reschedule everything " * 20)
      plines = app.send(:prompt_lines, 60)
      assert_equal 5, plines.size, "long input caps at 5 lines"
      assert_includes plines.first, "❯"
      assert_includes plines.last, "\e[7m", "cursor on the last line"

      app.instance_variable_get(:@input).replace("short message")
      assert_equal 1, app.send(:prompt_lines, 60).size

      app.instance_variable_get(:@input).replace("word " * 15) # ~75 chars: 2 lines at w=60
      assert_equal 2, app.send(:prompt_lines, 60).size
    end
  end

  def test_prompt_renders_trailing_space_immediately
    with_app do |app|
      app.send(:handle_key, "\t")
      app.instance_variable_get(:@input) << "hello "
      line = app.send(:prompt_lines, 60).last
      assert_includes Tui::Ansi.strip(line), "hello █".sub("█", " "), # trailing space present
                      "trailing space must render"
      assert_match(/hello \e\[7m/, line, "cursor sits after the space")
    end
  end

  def test_prompt_cursor_renders_at_insertion_point
    with_app do |app|
      app.send(:handle_key, "\t")
      app.instance_variable_get(:@input).replace("hello")
      app.send(:handle_key, "\x02") # ctrl-b
      app.send(:handle_key, "\x02")
      line = app.send(:prompt_lines, 60).last
      assert_match(/hel\e\[7ml/, line, "cursor highlights the character at point")
      app.send(:handle_key, "X")
      assert_equal "helXlo", app.instance_variable_get(:@input).text
    end
  end

  def test_bracketed_paste_in_list_mode_focuses_prompt_without_shortcut_actions
    with_app do |app|
      before = selected_title(app)
      app.instance_variable_set(:@key_data, "\e[200~cdd https://example.com/a\nb\e[201~")
      app.send(:drain_key_data)
      assert_equal :prompt, mode(app)
      assert_equal "cdd https://example.com/a b", app.instance_variable_get(:@input).text
      assert_equal before, selected_title(app), "pasted c/d characters must not complete or reschedule"
    end
  end

  def test_bracketed_paste_from_modal_closes_modal_before_prompt_focus
    with_app do |app|
      app.send(:handle_key, "?")
      assert_equal :modal, mode(app)
      app.instance_variable_set(:@key_data, "\e[200~hello from paste\e[201~")
      app.send(:drain_key_data)
      assert_equal :prompt, mode(app)
      assert_nil modal(app)
      assert_nil app.instance_variable_get(:@modal_kind)
      assert_equal "hello from paste", app.instance_variable_get(:@input).text
    end
  end

  def test_escape_is_not_held_as_partial_paste_prefix
    with_app do |app|
      app.send(:handle_key, "\t")
      assert_equal :prompt, mode(app)
      app.instance_variable_set(:@key_data, "\e")
      app.send(:drain_key_data)
      assert_equal :list, mode(app)
      assert_equal "", app.instance_variable_get(:@key_data)
    end
  end

  def test_utf8_fragment_at_start_of_read_is_preserved
    with_app do |app|
      app.instance_variable_set(:@input_bytes, "\xF0".b)
      assert_equal "", app.send(:drain_utf8_input)
      assert_equal "\xF0".b, app.instance_variable_get(:@input_bytes)

      app.instance_variable_get(:@input_bytes) << "\x9F\x99\x82".b
      assert_equal "🙂", app.send(:drain_utf8_input)
      assert_equal "".b, app.instance_variable_get(:@input_bytes)
    end
  end

  def test_utf8_fragmented_two_and_three_byte_sequences_are_preserved
    with_app do |app|
      app.instance_variable_set(:@input_bytes, "\xC3".b)
      assert_equal "", app.send(:drain_utf8_input)
      app.instance_variable_get(:@input_bytes) << "\xA9".b
      assert_equal "é", app.send(:drain_utf8_input)

      app.instance_variable_set(:@input_bytes, "\xE2\x80".b)
      assert_equal "", app.send(:drain_utf8_input)
      app.instance_variable_get(:@input_bytes) << "\x94".b
      assert_equal "—", app.send(:drain_utf8_input)
    end
  end

  def test_prompt_wraps_wide_characters_by_terminal_width
    with_app do |app|
      app.send(:handle_key, "\t")
      app.instance_variable_get(:@input).replace("🙂🙂🙂")
      lines = app.send(:wrapped_input, app.instance_variable_get(:@input), 5)
      assert_operator lines.size, :>=, 2
      assert lines.all? { |line| A.vislen(line) <= 5 }, "each input line must fit terminal cells"
    end
  end

  def test_prompt_width_one_substitutes_wide_grapheme_and_draws_one_cursor
    with_app do |app|
      app.send(:handle_key, "\t")
      app.instance_variable_get(:@input).replace("界")
      lines = app.send(:wrapped_input, app.instance_variable_get(:@input), 1)
      assert lines.all? { |line| A.vislen(line) <= 1 }, lines.inspect
      assert_equal 1, lines.join.scan(/\e\[7m/).size, "cursor must render exactly once"
      refute_includes A.strip(lines.join), "界", "a two-cell cluster cannot fit a one-cell budget"
    end
  end

  def test_prompt_exact_ascii_boundary_draws_cursor_once
    with_app do |app|
      app.send(:handle_key, "\t")
      app.instance_variable_get(:@input).replace("abc")
      lines = app.send(:wrapped_input, app.instance_variable_get(:@input), 3)
      assert lines.all? { |line| A.vislen(line) <= 3 }, lines.inspect
      assert_equal ["abc", " "], lines.map { |line| A.strip(line) }
      assert_equal 1, lines.join.scan(/\e\[7m/).size, "cursor must render exactly once"
    end
  end

  def test_date_error_clears_only_when_input_changes
    with_app do |app|
      app.send(:open_date_popup)
      form = ui(app).form
      form.instance_variable_set(:@error, "can't parse")
      app.send(:handle_key, "\x02") # ctrl-b at start: handled, no edit
      assert_equal "can't parse", form.error
      app.send(:handle_key, "f")
      assert_nil form.error
    end
  end

  def test_prompt_single_hint_line_when_not_focused
    with_app do |app|
      assert_equal 1, app.send(:prompt_lines, 60).size
    end
  end

  def test_model_toggle_cycles_provider_and_model_in_header
    with_app do |app|
      # Out of the box the switcher starts at claude-cli:sonnet and cycles the
      # flattened (provider, model) list; the header shows provider:model.
      assert_includes Tui::Ansi.strip(app.send(:header, 80)), "claude-cli:sonnet"
      app.send(:handle_key, "M")
      assert_includes Tui::Ansi.strip(app.send(:header, 80)), "claude-cli:opus"
      assert_match(/agent: claude-cli:opus/, app.instance_variable_get(:@flash))
      # cycle all the way around back to the first entry
      entries = app.instance_variable_get(:@entries).size
      (entries - 1).times { app.send(:handle_key, "M") }
      assert_includes Tui::Ansi.strip(app.send(:header, 80)), "claude-cli:sonnet", "wraps back around"
    end
  end

  def test_switching_provider_builds_that_adapter_only_when_request_is_submitted
    built = []
    agent = QueueAgent.new
    with_app(agent_factory: ->(entry) { built << entry; agent }) do |app|
      app.send(:handle_key, "M") until app.send(:current_entry).provider == "hermes"
      assert_empty built, "cycling only selects the entry; no queued adapter exists yet"
      app.send(:focus_prompt)
      app.instance_variable_get(:@input) << "hello"
      app.send(:submit_prompt)

      assert_equal ["hermes"], built.map(&:provider)
      assert_equal [["hello", app.send(:current_entry).model]], agent.started
    end
  end

  def test_model_change_while_active_is_snapshotted_only_for_new_request
    first = QueueAgent.new
    second = QueueAgent.new
    pool = [first, second]
    with_app(agent_factory: ->(_entry) { pool.shift }) do |app|
      app.send(:focus_prompt)
      app.instance_variable_get(:@input) << "first"
      app.send(:submit_prompt)
      app.send(:toggle_model) # sonnet -> opus while first remains active
      app.send(:focus_prompt)
      app.instance_variable_get(:@input) << "second"
      app.send(:submit_prompt)

      queue = app.instance_variable_get(:@agent_queue)
      assert_equal %w[sonnet opus], queue.requests.map { |request| request.entry.model }
      assert_equal [["first", "sonnet"]], first.started
      assert_empty second.started, "the new model's request waits behind the active one"
    end
  end

  def test_colon_opens_palette_while_tab_still_focuses_agent_prompt
    with_app do |app|
      app.send(:handle_key, ":")
      assert_equal :palette, mode(app)
      refute_nil ui(app).action_palette
      app.send(:handle_key, "\e")
      assert_equal :list, mode(app)

      app.send(:handle_key, "\t")
      assert_equal :prompt, mode(app)
      assert_nil ui(app).action_palette
    end
  end

  def test_palette_filters_and_executes_existing_form_action
    with_app do |app|
      app.send(:handle_key, ":")
      app.send(:handle_paste, "reschedule")
      palette = ui(app).action_palette
      assert_equal [:open_date_popup], palette.results.map(&:handler)
      app.send(:handle_key, "\r")
      assert_equal :form, mode(app)
      assert_equal :date, ui(app).form.kind
    end
  end

  def test_palette_cancel_and_form_cancel_restore_list_with_detail_panel
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, ":")
      assert_equal :palette, mode(app)
      app.send(:handle_key, "\e")
      assert_equal :list, mode(app)
      assert_equal :detail, panel(app).kind

      app.send(:handle_key, ":")
      app.send(:handle_paste, "reschedule")
      app.send(:handle_key, "\r")
      assert_equal :form, mode(app)
      app.send(:handle_key, "\e")
      assert_equal :list, mode(app)
      assert_equal :detail, panel(app).kind
    end
  end

  def test_panel_keeps_list_only_actions_in_action_palette
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, ":")
      handlers = ui(app).action_palette.entries.map(&:handler)
      assert_includes handlers, :archive_sweep
      assert_includes handlers, :open_help
      assert_includes handlers, :complete_selected
    end
  end

  def test_external_detail_removal_cancels_palette_before_neighbor_can_be_acted_on
    with_app do |app|
      app.send(:handle_key, "\r")
      removed_id = app.send(:current_item).id
      app.send(:handle_key, ":")
      rewrite_records(app) do |records|
        removed = records.find { |record| record["id"] == removed_id }
        removed["state"] = "DONE"
        removed["closed"] = "2026-07-10"
      end

      assert_equal :list, mode(app)
      assert_nil modal(app)
      assert_nil ui(app).action_palette
      neighbor = app.send(:current_item)
      refute_equal removed_id, neighbor.id
      assert neighbor.open?, "the fallback neighbor was not acted on"
      assert_equal neighbor.id, panel(app).identity
    end
  end

  def test_external_detail_removal_cancels_form_before_hidden_task_can_be_mutated
    with_app do |app|
      app.send(:handle_key, "\r")
      removed_id = app.send(:current_item).id
      original_deadline = app.send(:current_item).deadline
      app.send(:handle_key, "d")
      rewrite_records(app) do |records|
        removed = records.find { |record| record["id"] == removed_id }
        removed["state"] = "DONE"
        removed["closed"] = "2026-07-10"
      end

      assert_equal :list, mode(app)
      assert_nil modal(app)
      assert_nil ui(app).form
      removed = app.instance_variable_get(:@store).items.find { |item| item.id == removed_id }
      assert_equal original_deadline, removed.deadline
    end
  end

  def test_external_list_selection_removal_cancels_palette_before_neighbor_action
    with_app do |app|
      removed_id = app.send(:current_item).id
      app.send(:handle_key, ":")
      rewrite_records(app) do |records|
        removed = records.find { |record| record["id"] == removed_id }
        removed["state"] = "DONE"
        removed["closed"] = "2026-07-10"
      end

      assert_equal :list, mode(app)
      assert_nil ui(app).action_palette
      refute_equal removed_id, app.send(:current_item).id
      assert app.send(:current_item).open?, "the fallback neighbor was not acted on"
    end
  end

  def test_external_list_selection_removal_cancels_form_before_hidden_task_mutation
    with_app do |app|
      removed_id = app.send(:current_item).id
      original_deadline = app.send(:current_item).deadline
      app.send(:handle_key, "d")
      rewrite_records(app) do |records|
        removed = records.find { |record| record["id"] == removed_id }
        removed["state"] = "DONE"
        removed["closed"] = "2026-07-10"
      end

      assert_equal :list, mode(app)
      assert_nil ui(app).form
      removed = app.instance_variable_get(:@store).items.find { |item| item.id == removed_id }
      assert_equal original_deadline, removed.deadline
    end
  end

  def test_form_and_palette_cancel_fall_back_to_list_if_retained_modal_disappears
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, ":")
      ui(app).modal = nil
      app.send(:handle_key, "\e")
      assert_equal :list, mode(app)

      app.send(:open_date_popup)
      ui(app).form.instance_variable_set(:@return_mode, :modal)
      app.send(:handle_key, "\e")
      assert_equal :list, mode(app)
    end
  end

  def test_palette_excludes_unavailable_actions_and_handles_empty_unicode_query
    with_app do |app|
      ui(app).view = :next
      app.send(:rows)
      index = app.instance_variable_get(:@rows).index { |row| row.item&.title&.include?("Review PR") }
      app.send(:select_row, index)
      app.send(:handle_key, ":")
      handlers = ui(app).action_palette.entries.map(&:handler)
      refute_includes handlers, :open_recur_popup
      refute_includes handlers, :open_link

      app.send(:handle_paste, "🦄界")
      assert_empty ui(app).action_palette.results
      app.send(:handle_key, "\r")
      assert_equal :palette, mode(app), "enter on no results is inert"
    end
  end

  def test_palette_action_error_is_contained_and_palette_remains_open
    with_app do |app|
      entry = Tui::Shortcuts::Entry.new(
        sequences: ["!"], display_key: "!", description: "explode",
        contexts: [:list], handler: :explode, availability: :action_available?,
        palette: true, form: nil, confirmation: nil
      )
      app.define_singleton_method(:explode) { raise "boom" }
      ui(app).action_palette = Tui::ActionPalette.new(entries: [entry], return_mode: :list)
      ui(app).mode = :palette
      app.send(:handle_key, "\r")
      assert_equal :palette, mode(app)
      assert_match(/explode failed: boom/, ui(app).action_palette.error)
    end
  end

  def test_palette_action_error_remains_visible_after_panel_closes
    with_app do |app|
      app.send(:handle_key, "\r")
      entry = Tui::Shortcuts::Entry.new(
        sequences: ["!"], display_key: "!", description: "explode",
        contexts: [:detail], handler: :explode, availability: :action_available?,
        palette: true, form: nil, confirmation: nil
      )
      app.define_singleton_method(:explode) do
        send(:close_panel)
        raise "boom"
      end
      ui(app).action_palette = Tui::ActionPalette.new(
        entries: [entry], return_mode: :list, target_id: app.send(:current_item).id
      )
      ui(app).mode = :palette
      app.send(:handle_key, "\r")

      assert_equal :palette, mode(app)
      assert_nil modal(app)
      assert_nil panel(app)
      assert_match(/explode failed: boom/, ui(app).action_palette.error)
    end
  end
end
