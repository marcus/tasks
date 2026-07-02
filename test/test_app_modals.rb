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
      app = Tui::App.new(root: dir)
      app.send(:rows) # populate @rows like the paint loop does
      app.send(:clamp_selection)
      yield app
    end
  end

  def mode(app)  = app.instance_variable_get(:@mode)
  def modal(app) = app.instance_variable_get(:@modal)
  def modal_text(app) = modal(app)[:lines].map { |l| A.strip(l) }.join("\n")
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
      before = selected_title(app)
      app.send(:handle_key, "?")
      assert_equal :modal, mode(app)
      app.send(:handle_key, "\e[B")
      assert_equal before, selected_title(app), "help modal must not move selection"
      assert_equal 1, app.instance_variable_get(:@modal_scroll)
    end
  end

  def test_esc_closes_modal_and_returns_to_list
    with_app do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\e")
      assert_equal :list, mode(app)
      assert_nil modal(app)
      assert_nil app.instance_variable_get(:@modal_kind)
    end
  end

  def test_left_right_cycle_views_in_list_mode
    with_app do |app|
      views = []
      4.times do
        views << app.instance_variable_get(:@view)
        app.send(:handle_key, "\e[C")
      end
      assert_equal %i[agenda next quadrants inbox], views
      assert_equal :agenda, app.instance_variable_get(:@view), "wraps around"
      app.send(:handle_key, "\e[D")
      assert_equal :inbox, app.instance_variable_get(:@view), "left wraps backward"
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

  def test_prompt_expands_up_to_five_lines_when_input_wraps
    with_app do |app|
      app.send(:handle_key, "\t") # focus prompt
      app.instance_variable_get(:@input) << ("please reschedule everything " * 20)
      plines = app.send(:prompt_lines, 60)
      assert_equal 5, plines.size, "long input caps at 5 lines"
      assert_includes plines.first, "❯"
      assert_includes plines.last, "\e[7m", "cursor on the last line"

      app.instance_variable_set(:@input, +"short message")
      assert_equal 1, app.send(:prompt_lines, 60).size

      app.instance_variable_set(:@input, +("word " * 15)) # ~75 chars: 2 lines at w=60
      assert_equal 2, app.send(:prompt_lines, 60).size
    end
  end

  def test_prompt_single_hint_line_when_not_focused
    with_app do |app|
      assert_equal 1, app.send(:prompt_lines, 60).size
    end
  end

  def test_model_toggle_cycles_and_shows_in_header
    with_app do |app|
      assert_includes Tui::Ansi.strip(app.send(:header, 80)), "sonnet"
      app.send(:handle_key, "M")
      assert_includes Tui::Ansi.strip(app.send(:header, 80)), "opus"
      assert_match(/model: opus/, app.instance_variable_get(:@flash))
      app.send(:handle_key, "M")
      assert_includes Tui::Ansi.strip(app.send(:header, 80)), "haiku"
      app.send(:handle_key, "M")
      assert_includes Tui::Ansi.strip(app.send(:header, 80)), "sonnet", "wraps back around"
    end
  end

  def test_submit_passes_current_model_to_claude
    with_app do |app|
      started = nil
      claude = app.instance_variable_get(:@claude)
      claude.stub(:start, ->(text, model:) { started = [text, model] }) do
        Tui::Claude.stub(:available?, true) do
          app.send(:handle_key, "M") # → opus
          app.send(:handle_key, "\t")
          app.instance_variable_get(:@input) << "hello"
          app.send(:handle_key, "\r")
        end
      end
      assert_equal ["hello", "opus"], started
    end
  end
end
