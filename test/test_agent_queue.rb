# frozen_string_literal: true

require_relative "test_helper"
require "tui/agent_queue"

class TestAgentQueue < Minitest::Test
  Q = Tui::AgentQueue

  FakeStatus = Struct.new(:code, :signal, keyword_init: true) do
    def success? = signal.nil? && code == 0
    def exited? = signal.nil?
    def exitstatus = code
    def signaled? = !signal.nil?
    def termsig = signal
  end

  class FakeAgent
    attr_reader :started, :output, :process_status, :exit_status

    def initialize(available: true, success: true, output: "ok", start_error: nil, pump_error: nil)
      @availability = Array(available)
      @success = success
      @final_output = output
      @start_error = start_error
      @pump_error = pump_error
      @started = []
      @output = +""
      @process_status = nil
      @exit_status = nil
      @running = false
      @cancelled = false
    end

    def available?
      value = @availability.length > 1 ? @availability.shift : @availability.first
      value != false
    end

    def start(prompt, model:)
      raise @start_error if @start_error

      @started << [prompt, model]
      @running = true
      self
    end

    def pump
      raise @pump_error if @pump_error

      @output << @final_output
      @running = false
      @exit_status = @success ? 0 : 7
      @process_status = FakeStatus.new(code: @exit_status)
      :done
    end

    def cancel
      @cancelled = true
      @running = false
      @process_status = FakeStatus.new(code: nil, signal: 15)
      @exit_status = nil
      :done
    end

    def success? = @success && !@cancelled
    def cancelled? = @cancelled
    def running? = @running
    def io = nil
  end

  def entry(model = "sonnet", provider: "claude-cli")
    LLM::Entry.new(provider: provider, model: model)
  end

  def queue_with(*agents, **options)
    built = []
    factory = lambda do |selected|
      built << selected
      agents.shift or raise "no fake agent left"
    end
    [Q.new(agent_factory: factory, **options), built]
  end

  def test_runs_three_requests_fifo_with_only_one_live_adapter
    agents = [FakeAgent.new(output: "one"), FakeAgent.new(output: "two"), FakeAgent.new(output: "three")]
    queue, = queue_with(*agents)
    %w[first second third].each { |prompt| assert queue.enqueue(prompt: prompt, entry: entry).accepted? }

    assert_equal :started, queue.start_next.type
    assert_equal [["first", "sonnet"]], agents[0].started
    assert_empty agents[1].started
    assert_equal 2, queue.pending_count

    first = queue.pump.request
    assert_equal [:succeeded, "one"], [first.status, first.output]
    assert_equal :started, queue.start_next.type
    assert_equal [["second", "sonnet"]], agents[1].started
    second = queue.pump.request
    assert_equal "two", second.output
    queue.start_next
    third = queue.pump.request

    assert_equal %i[succeeded succeeded succeeded], queue.requests.map(&:status)
    assert_equal %w[one two three], queue.requests.map(&:output)
    assert_equal "three", third.output
    refute queue.work?
  end

  def test_each_request_snapshots_its_provider_and_model
    agents = [FakeAgent.new, FakeAgent.new]
    queue, built = queue_with(*agents)
    first = entry("sonnet")
    second = entry("qwen", provider: "hermes")
    queue.enqueue(prompt: "one", entry: first)
    queue.start_next
    queue.enqueue(prompt: "two", entry: second)

    assert_equal [first, second], built
    assert_equal [first, second], queue.requests.map(&:entry)
    queue.pump
    queue.start_next
    assert_equal [["two", "qwen"]], agents[1].started
  end

  def test_entry_snapshot_cannot_be_changed_by_source_or_consumer
    agent = FakeAgent.new
    queue, built = queue_with(agent)
    source = entry("sonnet")
    accepted = queue.enqueue(prompt: "stable", entry: source)

    source.model = "opus"
    snapshot = accepted.request
    assert_equal "sonnet", snapshot.entry.model
    assert snapshot.entry.frozen?
    assert snapshot.entry.model.frozen?
    assert_raises(FrozenError) { snapshot.entry.model << "-changed" }
    assert_raises(FrozenError) { snapshot.entry.model = "haiku" }
    assert_same snapshot.entry, queue.requests.first.entry

    queue.start_next
    assert_equal [["stable", "sonnet"]], agent.started
    assert_same snapshot.entry, built.first
  end

  def test_rejects_unavailable_or_over_capacity_without_recording_request
    unavailable = FakeAgent.new(available: false)
    queue, = queue_with(unavailable)
    rejected = queue.enqueue(prompt: "keep me", entry: entry)
    refute rejected.accepted?
    assert_match(/not available/, rejected.error)
    assert_empty queue.requests

    active = FakeAgent.new
    waiting = FakeAgent.new
    extra = FakeAgent.new
    queue, = queue_with(active, waiting, extra, max_pending: 1)
    queue.enqueue(prompt: "active", entry: entry)
    queue.start_next
    assert queue.enqueue(prompt: "waiting", entry: entry).accepted?
    full = queue.enqueue(prompt: "extra", entry: entry)
    refute full.accepted?
    assert_match(/queue is full/, full.error)
    assert_equal %w[active waiting], queue.requests.map(&:prompt)
  end

  def test_start_failure_is_recorded_and_does_not_strand_later_work
    broken = FakeAgent.new(start_error: Errno::ENOENT.new("gone"))
    good = FakeAgent.new
    queue, = queue_with(broken, good)
    queue.enqueue(prompt: "broken", entry: entry)
    queue.enqueue(prompt: "good", entry: entry)

    failed = queue.start_next
    assert_equal :failed, failed.request.status
    assert_match(/could not start/, failed.request.error)
    started = queue.start_next
    assert_equal :started, started.type
    assert_equal [["good", "sonnet"]], good.started
  end

  def test_provider_becoming_unavailable_at_start_fails_one_request
    agent = FakeAgent.new(available: [true, false])
    queue, = queue_with(agent)
    assert queue.enqueue(prompt: "later", entry: entry).accepted?
    event = queue.start_next
    assert_equal :failed, event.request.status
    assert_match(/became unavailable/, event.request.error)
    refute queue.work?
  end

  def test_nonzero_exit_retains_output_and_error
    queue, = queue_with(FakeAgent.new(success: false, output: "partial transcript"))
    queue.enqueue(prompt: "fail", entry: entry)
    queue.start_next
    result = queue.pump.request

    assert_equal :failed, result.status
    assert_equal 7, result.exit_status
    assert_equal "partial transcript", result.output
    assert_match(/exited 7/, result.error)
  end

  def test_pump_exception_fails_one_request_cleans_up_and_allows_next
    broken = FakeAgent.new(pump_error: IOError.new("read exploded"))
    good = FakeAgent.new
    queue, = queue_with(broken, good)
    queue.enqueue(prompt: "broken", entry: entry)
    queue.enqueue(prompt: "good", entry: entry)
    queue.start_next

    failed = queue.pump
    assert_equal :failed, failed.request.status
    assert_match(/stream failed: read exploded/, failed.request.error)
    assert broken.cancelled?

    assert_equal :started, queue.start_next.type
    assert_equal [["good", "sonnet"]], good.started
  end

  def test_cancel_active_preserves_partial_output_and_leaves_pending_for_advance
    active = FakeAgent.new
    active.instance_variable_set(:@output, +"partial")
    waiting = FakeAgent.new
    queue, = queue_with(active, waiting)
    queue.enqueue(prompt: "active", entry: entry)
    queue.start_next
    queue.enqueue(prompt: "waiting", entry: entry)

    cancelled = queue.cancel_active.request
    assert_equal [:cancelled, "partial"], [cancelled.status, cancelled.output]
    assert_equal 1, queue.pending_count
    assert_equal :started, queue.start_next.type
    assert_equal [["waiting", "sonnet"]], waiting.started
  end

  def test_cancel_pending_never_touches_active_adapter
    active = FakeAgent.new
    queue, = queue_with(active, FakeAgent.new, FakeAgent.new)
    queue.enqueue(prompt: "active", entry: entry)
    queue.start_next
    queue.enqueue(prompt: "two", entry: entry)
    queue.enqueue(prompt: "three", entry: entry)

    cancelled = queue.cancel_pending
    assert_equal [2, 3], cancelled.map(&:id)
    assert cancelled.all? { |request| request.status == :cancelled }
    assert active.running?
    assert queue.active?
    refute queue.pending?
  end

  def test_history_limit_evicts_only_oldest_finished_requests
    agents = Array.new(4) { FakeAgent.new }
    queue, = queue_with(*agents, history_limit: 2)
    4.times do |index|
      queue.enqueue(prompt: "request #{index}", entry: entry)
      queue.start_next
      queue.pump
    end

    assert_equal [3, 4], queue.requests.map(&:id)
    assert_equal 4, queue.latest_finished.id
  end

  def test_live_snapshot_exposes_streamed_output_without_mutable_internals
    agent = FakeAgent.new
    queue, = queue_with(agent)
    queue.enqueue(prompt: "stream", entry: entry)
    queue.start_next
    agent.instance_variable_set(:@output, +"live")

    snapshot = queue.active_request
    assert_equal "live", snapshot.output
    assert snapshot.frozen?
    assert snapshot.output.frozen?
  end
end
