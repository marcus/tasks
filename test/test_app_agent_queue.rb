# frozen_string_literal: true

require_relative "test_helper"
require "tui/app"

class TestAppAgentQueue < Minitest::Test
  A = Tui::Ansi

  FakeStatus = Struct.new(:code, :signal, keyword_init: true) do
    def success? = signal.nil? && code == 0
    def exited? = signal.nil?
    def exitstatus = code
    def signaled? = !signal.nil?
    def termsig = signal
  end

  class FakeAgent
    attr_reader :started, :output, :process_status, :exit_status
    attr_accessor :on_start

    def initialize
      @started = []
      @output = +""
      @process_status = nil
      @exit_status = nil
      @success = true
      @cancelled = false
    end

    def available? = true
    def io = nil
    def start(prompt, model:)
      @started << [prompt, model]
      @on_start&.call
      self
    end
    def success? = @success && !@cancelled

    def finish_with(output, success: true)
      @next_output = output
      @success = success
    end

    def pump
      @output << @next_output.to_s
      @exit_status = @success ? 0 : 9
      @process_status = FakeStatus.new(code: @exit_status)
      :done
    end

    def cancel
      @cancelled = true
      @process_status = FakeStatus.new(code: nil, signal: 15)
      @exit_status = nil
      :done
    end
  end

  def with_app(agent_count: 4)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE_ORG)
      agents = Array.new(agent_count) { FakeAgent.new }
      pool = agents.dup
      app = Tui::App.new(
        root: dir,
        paths: Tasks::Config.for_dir(dir),
        llm_config: default_llm_config,
        agent_factory: ->(_entry) { pool.shift or raise "agent pool exhausted" },
        agent_probe: ->(_entry) { true }
      )
      app.send(:rows)
      yield app, agents
    end
  end

  def submit(app, text)
    input = app.instance_variable_get(:@input)
    input.replace(text)
    app.instance_variable_get(:@ui).mode = :prompt
    app.send(:submit_prompt)
  end

  def queue(app) = app.instance_variable_get(:@agent_queue)
  def ui(app) = app.instance_variable_get(:@ui)

  def test_three_requests_run_fifo_and_all_results_remain_available
    with_app(agent_count: 3) do |app, agents|
      submit(app, "first")
      submit(app, "second")
      app.send(:toggle_model)
      submit(app, "third")

      assert_equal [["first", "sonnet"]], agents[0].started
      assert_empty agents[1].started
      assert_equal 2, queue(app).pending_count

      agents[0].finish_with("first result")
      app.send(:pump_agent_queue)
      assert_equal [["second", "sonnet"]], agents[1].started
      agents[1].finish_with("second result")
      app.send(:pump_agent_queue)
      assert_equal [["third", "opus"]], agents[2].started
      agents[2].finish_with("third result")
      app.send(:pump_agent_queue)

      assert_equal %w[first second third], queue(app).requests.map(&:prompt)
      assert_equal ["first result", "second result", "third result"], queue(app).requests.map(&:output)
      assert_equal 3, app.instance_variable_get(:@resp_request_id)
      assert app.instance_variable_get(:@resp_open)
      refute queue(app).work?
    end
  end

  def test_footer_shows_active_entry_pending_count_and_latest_result_link
    with_app(agent_count: 2) do |app, agents|
      submit(app, "first")
      submit(app, "second")
      running = app.send(:footer, 100).map { |line| line.is_a?(String) ? A.strip(line) : line }.join("\n")
      assert_includes running, "#1 claude-cli:sonnet is working"
      assert_includes running, "1 queued"
      assert_includes running, "A activity"

      queue(app).cancel_pending
      agents[0].finish_with("done")
      app.send(:pump_agent_queue)
      finished = app.send(:footer, 100).map { |line| line.is_a?(String) ? A.strip(line) : line }.join("\n")
      assert_includes finished, "result #1 of 2"
      assert_includes finished, "A opens all agent activity"
    end
  end

  def test_agent_activity_is_filterable_and_refreshes_live_without_losing_filter
    with_app(agent_count: 2) do |app, agents|
      submit(app, "first request")
      submit(app, "second request")
      app.send(:open_agent_activity)
      assert_equal :agent_activity, ui(app).modal.kind
      assert_equal :modal, ui(app).mode

      app.send(:modal_start_filter)
      "second".chars.each { |char| app.send(:modal_filter_key, char) }
      assert_equal "second", ui(app).modal.filter
      before = ui(app).modal.object_id

      agents[0].instance_variable_set(:@output, +"live transcript")
      app.send(:modal_view, app.send(:modal_body_h))
      assert_equal before, ui(app).modal.object_id
      assert_equal "second", ui(app).modal.filter
      filtered = ui(app).modal.lines.map { |line| A.strip(line) }.join("\n")
      assert_includes filtered, "#2"
      assert_includes filtered, "second request"
      assert_includes filtered, "result   (waiting)"
      refute_includes filtered, "first request"
    end
  end

  def test_silent_running_activity_refreshes_on_elapsed_second
    with_app(agent_count: 1) do |app, agents|
      submit(app, "silent request")
      app.send(:open_agent_activity)
      _height, width = app.send(:terminal_size)
      agents[0].instance_variable_set(:@output, +"late transcript")
      app.instance_variable_set(:@agent_activity_second, -1)

      app.send(:modal_view, app.send(:modal_body_h), width: width)

      rendered = ui(app).modal.lines.map { |line| A.strip(line) }.join("\n")
      assert_includes rendered, "late transcript"
    end
  end

  def test_queued_requests_build_fresh_context_so_a_memory_edit_hits_only_the_second
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE_ORG)
      File.write(File.join(dir, "TASK_AGENT.md"), "# TASK_AGENT\n")
      memory = File.join(dir, "agent-memory.md")
      File.write(memory, "- first default\n")

      agents = [FakeAgent.new, FakeAgent.new]
      pool = agents.dup
      captured = []
      # Intercept the real build_agent -> LLM.build path so we can inspect the
      # system context each request was actually handed.
      fake_build = lambda do |_entry, root:, system: nil, config: nil|
        captured << system
        pool.shift or raise "agent pool exhausted"
      end

      LLM.stub(:build, fake_build) do
        app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir),
                           llm_config: default_llm_config,
                           agent_probe: ->(_entry) { true })
        app.send(:rows)
        submit(app, "first")            # starts immediately, builds context now
        File.write(memory, "- second default\n") # edited while the first runs
        submit(app, "second")           # queued behind the active first
        agents[0].finish_with("done")
        app.send(:pump_agent_queue)     # finishes first, then starts second
      end

      assert_equal 2, captured.size, "each request builds its own adapter at start"
      assert_includes captured[0], "first default"
      refute_includes captured[0], "second default"
      assert_includes captured[1], "second default"
      refute_includes captured[1], "first default"
    end
  end

  def test_store_reloads_after_completion_before_next_request_starts
    with_app(agent_count: 2) do |app, agents|
      submit(app, "first")
      submit(app, "second")
      store = app.instance_variable_get(:@store)
      records = Tasks::Format.parse(File.read(store.org, encoding: "UTF-8")).records
      records.find { |record| record["id"] == FIX[:flight] }["title"] = "Reloaded before second"
      File.write(store.org, Tasks::Format.dump(records))

      seen_title = nil
      agents[1].on_start = lambda do
        seen_title = store.items.find { |item| item.id == FIX[:flight] }.title
      end
      agents[0].finish_with("done")
      app.send(:pump_agent_queue)

      assert_equal "Reloaded before second", seen_title
      assert_equal [["second", "sonnet"]], agents[1].started
    end
  end

  def test_full_queue_rejection_keeps_prompt_and_focus
    with_app(agent_count: 3) do |app, _agents|
      queue(app).instance_variable_set(:@max_pending, 1)
      submit(app, "active")
      submit(app, "waiting")
      input = app.instance_variable_get(:@input)
      input.replace("keep this prompt")
      ui(app).mode = :prompt

      app.send(:submit_prompt)

      assert_equal "keep this prompt", input.to_s
      assert_equal :prompt, ui(app).mode
      assert_match(/queue is full/, app.instance_variable_get(:@flash))
      assert_equal %w[active waiting], queue(app).requests.map(&:prompt)
    end
  end

  def test_escape_cancels_active_and_advances_to_next_request
    with_app(agent_count: 2) do |app, agents|
      submit(app, "first")
      submit(app, "second")
      agents[0].instance_variable_set(:@output, +"partial")

      app.send(:dismiss_or_cancel)

      first = queue(app).requests.find { |request| request.id == 1 }
      assert_equal [:cancelled, "partial"], [first.status, first.output]
      assert_equal [["second", "sonnet"]], agents[1].started
      assert_equal 0, queue(app).pending_count
      assert_match(/cancelled agent request #1/, app.instance_variable_get(:@flash))
    end
  end

  def test_palette_cancel_confirmation_discards_waiting_only
    with_app(agent_count: 3) do |app, agents|
      submit(app, "active")
      submit(app, "two")
      submit(app, "three")
      app.send(:cancel_queued_agent_requests)
      assert_equal :agent_queue_cancel_confirm, ui(app).modal.kind
      app.send(:cancel_queued_agent_requests_key, "y")

      assert queue(app).active?
      assert_equal 0, queue(app).pending_count
      assert_equal %i[running cancelled cancelled], queue(app).requests.map(&:status)
      assert_equal [["active", "sonnet"]], agents[0].started
    end
  end

  def test_activity_and_cancel_actions_appear_contextually_in_palette
    with_app(agent_count: 2) do |app, _agents|
      app.send(:open_action_palette)
      empty_handlers = ui(app).action_palette.instance_variable_get(:@entries).map(&:handler)
      refute_includes empty_handlers, :open_agent_activity
      refute_includes empty_handlers, :cancel_queued_agent_requests
      app.send(:close_action_palette)

      submit(app, "active")
      submit(app, "waiting")
      app.send(:open_action_palette)
      handlers = ui(app).action_palette.instance_variable_get(:@entries).map(&:handler)
      assert_includes handlers, :open_agent_activity
      assert_includes handlers, :cancel_queued_agent_requests
    end
  end

  def test_quit_requires_confirmation_then_cancels_active_and_pending
    with_app(agent_count: 2) do |app, _agents|
      submit(app, "active")
      submit(app, "waiting")
      ui(app).mode = :prompt
      app.send(:quit)

      assert_equal :agent_quit_confirm, ui(app).modal.kind
      app.send(:agent_quit_confirmation_key, "q")
      refute app.instance_variable_get(:@quit), "repeated quit must not confirm"
      app.send(:agent_quit_confirmation_key, "n")
      assert_equal :prompt, ui(app).mode
      assert queue(app).work?

      app.send(:quit)
      app.send(:agent_quit_confirmation_key, "y")
      assert app.instance_variable_get(:@quit)
      refute queue(app).work?
      assert_equal %i[cancelled cancelled], queue(app).requests.map(&:status)
    end
  end

  def test_agent_quit_confirmation_restores_context_palette
    with_app(agent_count: 1) do |app, _agents|
      submit(app, "active")
      app.send(:handle_key, "@")
      assert_equal :context_palette, ui(app).mode
      palette = ui(app).context_palette

      app.send(:quit)
      assert_equal :agent_quit_confirm, ui(app).modal.kind
      app.send(:agent_quit_confirmation_key, "n")

      assert_equal :context_palette, ui(app).mode
      assert_same palette, ui(app).context_palette
      refute app.instance_variable_get(:@quit)
    end
  end
end
