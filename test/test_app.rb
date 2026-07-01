# frozen_string_literal: true

require_relative "test_helper"
require "tui/app"

class TestApp < Minitest::Test
  # Records calls to #start and reports whatever running? state we set,
  # so we can drive submit_prompt without spawning a real claude process.
  class FakeClaude
    attr_reader :started

    def initialize(running:)
      @running = running
      @started = []
    end

    def running? = @running
    def start(text) = @started << text
  end

  def app_with(claude:, input:)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)
      app = Tui::App.new(root: dir)
      app.instance_variable_set(:@claude, claude)
      app.instance_variable_set(:@input, +input)
      yield app
    end
  end

  def test_submit_prompt_rejected_while_claude_running
    fake = FakeClaude.new(running: true)
    app_with(claude: fake, input: "reschedule the flight") do |app|
      app.send(:submit_prompt)
      assert_empty fake.started, "must not orphan the in-flight run by starting a second"
      assert_match(/still working/, app.instance_variable_get(:@flash))
      # input is cleared and focus returns to the list even on rejection
      assert_equal "", app.instance_variable_get(:@input)
      assert_equal :list, app.instance_variable_get(:@mode)
    end
  end

  def test_submit_prompt_ignores_blank_input_without_touching_claude
    fake = FakeClaude.new(running: false)
    app_with(claude: fake, input: "   ") do |app|
      app.send(:submit_prompt)
      assert_empty fake.started
      assert_nil app.instance_variable_get(:@flash)
    end
  end
end
