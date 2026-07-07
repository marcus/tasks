# frozen_string_literal: true

require_relative "test_helper"
require "tui/app"

# Exercises App's modal-mode key handling through its private interface —
# the pieces a pty smoke test can't assert on.
class TestAppModals < Minitest::Test
  A = Tui::Ansi

  def with_app
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)
      app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir),
                         llm_config: default_llm_config)
      app.send(:rows) # populate @rows like the paint loop does
      app.send(:clamp_selection)
      yield app
    end
  end

  def mode(app)  = app.instance_variable_get(:@mode)
  def modal(app) = app.instance_variable_get(:@modal)
  def modal_text(app) = modal(app).lines.map { |l| A.strip(l) }.join("\n")
  def selected_title(app) = app.send(:current_item).title

  def test_enter_opens_detail_modal_for_selection
    with_app do |app|
      app.send(:handle_key, "\r")
      assert_equal :modal, mode(app)
      assert_includes modal_text(app), selected_title(app)
    end
  end

  def test_arrows_walk_tasks_while_detail_modal_open
    with_app do |app|
      app.send(:handle_key, "\r")
      before = selected_title(app)
      app.send(:handle_key, "\e[B")
      assert_equal :modal, mode(app), "modal stays open"
      refute_equal before, selected_title(app), "selection moved"
      assert_includes modal_text(app), selected_title(app), "modal follows selection"
      app.send(:handle_key, "\e[A")
      assert_equal before, selected_title(app)
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

  def test_vim_scroll_keys_scroll_detail_modal_without_moving_selection
    with_app do |app|
      app.send(:handle_key, "\r")
      before = selected_title(app)
      app.send(:handle_key, "\x04")
      assert_equal before, selected_title(app), "ctrl-d scrolls the modal, not the task list"
      assert_equal :modal, mode(app)
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

  def test_slash_does_nothing_in_detail_modal
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "/")
      assert_equal :modal, mode(app), "detail modal is not filterable yet"
      refute modal(app).filterable?
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

  def test_esc_closes_modal_and_returns_to_list
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\e")
      assert_equal :list, mode(app)
      assert_nil modal(app)
    end
  end

  def test_left_right_cycle_views_in_list_mode
    with_app do |app|
      views = []
      5.times do
        views << app.instance_variable_get(:@view)
        app.send(:handle_key, "\e[C")
      end
      assert_equal %i[agenda next quadrants inbox projects], views
      assert_equal :agenda, app.instance_variable_get(:@view), "wraps around"
      app.send(:handle_key, "\e[D")
      assert_equal :projects, app.instance_variable_get(:@view), "left wraps backward"
    end
  end

  def test_arrows_do_not_cycle_views_while_modal_open
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\e[C") # right arrow: no binding in modal mode
      assert_equal :agenda, app.instance_variable_get(:@view)
      assert_equal :modal, mode(app)
    end
  end

  def test_yank_works_in_list_and_modal_mode
    with_app do |app|
      copied = []
      Tui::Clipboard.stub(:copy, ->(text, **) { copied << text; true }) do
        app.send(:handle_key, "y")                      # list mode: ref
        app.send(:handle_key, "\r")                     # open detail modal
        app.send(:handle_key, "Y")                      # modal mode: markdown
        assert_equal :modal, mode(app), "yank must not close the modal"
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

  def test_priority_bump_works_in_detail_modal
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "J")
      assert_equal :modal, mode(app)
      assert_equal "B", app.send(:current_item).priority
      assert_match(/priority\s+\[#B\]/, modal_text(app), "modal content refreshed")
    end
  end

  def test_complete_from_detail_modal_closes_it
    with_app do |app|
      app.send(:handle_key, "\r")                 # open detail on the selection
      title = selected_title(app)
      app.send(:handle_key, "c")                  # complete it
      assert_equal :list, mode(app), "modal closes once the task leaves the view"
      assert_nil modal(app)
      app.send(:rows)
      titles = app.instance_variable_get(:@rows).map { |r| r.item&.title }
      refute_includes titles, title, "completed task gone from the open view"
    end
  end

  def test_reschedule_from_detail_modal_updates_and_stays_open
    with_app do |app|
      app.send(:handle_key, "\r")                 # detail on Book flight (has deadline)
      title = selected_title(app)
      app.send(:handle_key, "d")                  # reschedule
      assert_equal :date, mode(app)
      assert_equal :detail, modal(app).kind,
                   "the detail modal stays open behind the date popup"
      "2026-07-20".chars.each { |c| app.send(:handle_key, c) }
      app.send(:handle_key, "\r")                 # submit
      assert_equal :modal, mode(app), "returns to the detail modal, not the bare list"
      assert_equal title, selected_title(app), "cursor still on the task"
      assert_equal Date.new(2026, 7, 20), app.send(:current_item).deadline
      assert_includes modal_text(app), "2026-07-20", "modal shows the new deadline"
    end
  end

  def test_esc_during_reschedule_from_modal_returns_to_modal
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "d")
      assert_equal :date, mode(app)
      app.send(:handle_key, "\e")                 # cancel the date entry
      assert_equal :modal, mode(app), "esc returns to the modal it came from"
      refute_nil modal(app)
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

  def test_undo_inside_detail_modal_refreshes_content
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "J")
      assert_match(/priority\s+\[#B\]/, modal_text(app))
      app.send(:handle_key, "u")
      assert_equal :modal, mode(app)
      assert_match(/priority\s+\[#A\]/, modal_text(app), "modal shows undone state")
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

  def test_p_from_modal_closes_it_and_appends_to_existing_input
    with_app do |app|
      app.instance_variable_get(:@input) << "complete"
      app.send(:handle_key, "\r")
      title = selected_title(app)
      app.send(:handle_key, "p")
      assert_equal :prompt, mode(app)
      assert_nil modal(app)
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
      assert_equal "flight", app.instance_variable_get(:@filter)

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
      assert_nil app.instance_variable_get(:@filter)
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
      assert_nil app.instance_variable_get(:@filter)
      assert_match(/filter cleared/, app.instance_variable_get(:@flash))
    end
  end

  def test_slash_with_active_filter_edits_it
    with_app do |app|
      app.send(:handle_key, "/")
      "flight".chars.each { |c| app.send(:handle_key, c) }
      app.send(:handle_key, "\r")
      app.send(:handle_key, "/")
      assert_equal "flight", app.instance_variable_get(:@filter_input)
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

  def test_date_error_clears_only_when_input_changes
    with_app do |app|
      app.send(:open_date_popup)
      app.instance_variable_set(:@date_error, "can't parse")
      app.send(:handle_key, "\x02") # ctrl-b at start: handled, no edit
      assert_equal "can't parse", app.instance_variable_get(:@date_error)
      app.send(:handle_key, "f")
      assert_nil app.instance_variable_get(:@date_error)
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

  def test_switching_to_another_provider_rebuilds_the_agent
    with_app do |app|
      app.send(:handle_key, "M") until app.send(:current_entry).provider == "hermes"
      agent = app.instance_variable_get(:@agent)
      assert_instance_of LLM::Agent::Hermes, agent,
                         "cycling to the hermes entry swaps in its adapter"
    end
  end

  def test_model_only_change_keeps_same_agent_instance
    with_app do |app|
      before = app.instance_variable_get(:@agent) # claude-cli:sonnet
      app.send(:handle_key, "M")                   # → claude-cli:opus (same provider)
      assert_equal "opus", app.send(:current_entry).model
      assert_same before, app.instance_variable_get(:@agent),
                  "a model-only switch must not rebuild the adapter"
    end
  end

  def test_provider_switch_is_deferred_while_a_run_is_in_flight
    with_app do |app|
      # stand in a running agent so the switcher must not swap it out
      running = Object.new
      def running.running? = true
      def running.io = nil
      app.instance_variable_set(:@agent, running)
      app.instance_variable_set(:@agent_provider, "claude-cli")

      app.send(:handle_key, "M") until app.send(:current_entry).provider == "hermes"
      assert_same running, app.instance_variable_get(:@agent),
                  "must never drop a running agent's io from IO.select"
      assert_equal "claude-cli", app.instance_variable_get(:@agent_provider)
    end
  end

  def test_submit_passes_current_entry_model_to_agent
    with_app do |app|
      started = nil
      agent = app.instance_variable_get(:@agent)
      agent.stub(:start, ->(text, model:) { started = [text, model] }) do
        agent.stub(:available?, true) do
          app.send(:handle_key, "M") # → claude-cli:opus
          app.send(:handle_key, "\t")
          app.instance_variable_get(:@input) << "hello"
          app.send(:handle_key, "\r")
        end
      end
      assert_equal ["hello", "opus"], started
    end
  end
end
