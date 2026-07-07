# frozen_string_literal: true

require_relative "test_helper"
require "tui/app"
require "tui/text_input"

class TestApp < Minitest::Test
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
      File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)
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
      assert_equal :list, app.instance_variable_get(:@mode)
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

  # -- deferral ----------------------------------------------------------------

  # Build an app on a sandbox gtd.org (optionally a modified fixture), park it
  # on a given view, and select the row whose item title includes `select`.
  def app_on(view:, select:, content: FIXTURE_ORG)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "gtd.org"), content)
      app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir),
                         llm_config: default_llm_config)
      app.instance_variable_set(:@view, view)
      app.send(:rows)
      rws = app.instance_variable_get(:@rows)
      idx = rws.index { |r| r.item&.title&.include?(select) }
      raise "no selectable row for #{select.inspect}" unless idx
      app.instance_variable_set(:@sel, idx)
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
    deferred = FIXTURE_ORG.sub("Water the plants :@home:", "Water the plants :@home:defer:")
    app_on(view: :next, select: "Review PR", content: deferred) do |app|
      refute_includes row_titles(app), "Water the plants", "deferred hidden by default"
      app.send(:toggle_deferred_view)
      assert app.instance_variable_get(:@show_deferred)
      assert_includes row_titles(app), "Water the plants", "Z reveals deferred tasks"
      app.send(:toggle_deferred_view)
      refute app.instance_variable_get(:@show_deferred)
      refute_includes row_titles(app), "Water the plants", "Z again hides them"
    end
  end

  def test_defer_selected_reactivates_when_already_deferred
    deferred = FIXTURE_ORG.sub("Water the plants :@home:", "Water the plants :@home:defer:")
    app_on(view: :next, select: "Review PR", content: deferred) do |app|
      app.instance_variable_set(:@show_deferred, true) # so the deferred task is selectable
      app.send(:rows)
      idx = app.instance_variable_get(:@rows).index { |r| r.item&.title&.include?("Water the plants") }
      app.instance_variable_set(:@sel, idx)
      app.send(:defer_selected)
      store = app.instance_variable_get(:@store)
      refute store.items.find { |i| i.title.include?("Water the plants") }.deferred?
      assert_match(/activated/, app.instance_variable_get(:@flash))
    end
  end

  # -- recurrence ------------------------------------------------------------

  RECUR_FIXTURE = <<~ORG
    * Work
    ** NEXT Pay rent :@home:
       DEADLINE: <2026-08-01 Sat +1m>
    ** NEXT Standup notes :@computer:
  ORG

  def test_open_recur_popup_prefills_current_cookie
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      assert_equal :recur, app.instance_variable_get(:@mode)
      assert_equal "+1m", app.instance_variable_get(:@recur_input)
    end
  end

  def test_open_recur_popup_refuses_undated_task
    app_on(view: :next, select: "Standup notes", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      assert_equal :list, app.instance_variable_get(:@mode), "no popup for a task with no date"
      assert_match(/schedule it first/, app.instance_variable_get(:@flash))
    end
  end

  def test_submit_recur_sets_cookie
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      app.instance_variable_get(:@recur_input).replace("weekly")
      app.send(:submit_recur)
      store = app.instance_variable_get(:@store)
      assert_equal ".+1w", store.items.find { |i| i.title.include?("Pay rent") }.recur
      assert_equal :list, app.instance_variable_get(:@mode)
    end
  end

  def test_submit_recur_off_clears
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      app.instance_variable_get(:@recur_input).replace("off")
      app.send(:submit_recur)
      assert_nil app.instance_variable_get(:@store).items.find { |i| i.title.include?("Pay rent") }.recur
    end
  end

  def test_submit_recur_reports_parse_error
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      app.instance_variable_get(:@recur_input).replace("bananas")
      app.send(:submit_recur)
      assert_equal :recur, app.instance_variable_get(:@mode), "stays open on bad input"
      assert_match(/can't parse/, app.instance_variable_get(:@recur_error))
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
end
