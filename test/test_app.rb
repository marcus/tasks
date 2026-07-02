# frozen_string_literal: true

require_relative "test_helper"
require "tui/app"

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
                         entries: default_llm_entries)
      app.instance_variable_set(:@agent, agent)
      app.instance_variable_set(:@input, +input)
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
end
