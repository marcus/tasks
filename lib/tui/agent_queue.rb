# frozen_string_literal: true

module Tui
  # Serial coordinator for autonomous TUI agent requests. Each request owns the
  # provider/model selected when it was submitted, but at most one adapter is
  # ever running. The queue records transcripts and outcomes for presentation;
  # it never reads or writes task data itself.
  class AgentQueue
    MAX_PENDING = 100
    HISTORY_LIMIT = 50
    FINISHED = %i[succeeded failed cancelled].freeze

    Snapshot = Data.define(
      :id, :prompt, :entry, :status, :queued_at, :started_at, :finished_at,
      :output, :exit_status, :error
    ) do
      def finished? = FINISHED.include?(status)

      def elapsed(now)
        return 0 unless started_at

        ((finished_at || now) - started_at).clamp(0, Float::INFINITY)
      end
    end

    Submission = Data.define(:request, :error) do
      def accepted? = !request.nil?
    end

    Event = Data.define(:type, :request)

    Item = Struct.new(
      :id, :prompt, :entry, :status, :queued_at, :started_at, :finished_at,
      :output, :exit_status, :error, :agent,
      keyword_init: true
    ) do
      def finished? = FINISHED.include?(status)
    end
    private_constant :Item

    def initialize(agent_factory:, clock: nil, max_pending: MAX_PENDING,
                   history_limit: HISTORY_LIMIT)
      @agent_factory = agent_factory
      @clock = clock || -> { Process.clock_gettime(Process::CLOCK_MONOTONIC) }
      @max_pending = Integer(max_pending)
      @history_limit = Integer(history_limit)
      raise ArgumentError, "max_pending must be positive" unless @max_pending.positive?
      raise ArgumentError, "history_limit must be positive" unless @history_limit.positive?

      @items = []
      @pending = []
      @active = nil
      @agent = nil
      @next_id = 1
    end

    def enqueue(prompt:, entry:)
      return Submission.new(request: nil, error: "agent queue is full (#{@max_pending} waiting)") \
        if pending_count >= @max_pending

      captured_entry = immutable_entry(entry)
      agent = @agent_factory.call(captured_entry)
      unless agent.available?
        return Submission.new(
          request: nil,
          error: "#{captured_entry.provider} not available — check the CLI is installed and any local model server is running"
        )
      end

      item = Item.new(
        id: @next_id,
        prompt: prompt.to_s.dup.freeze,
        entry: captured_entry,
        status: :queued,
        queued_at: now,
        output: +"",
        agent: agent
      )
      @next_id += 1
      @items << item
      @pending << item
      Submission.new(request: snapshot(item), error: nil)
    rescue StandardError => e
      Submission.new(request: nil, error: "#{entry.provider} unavailable: #{e.message}")
    end

    # Attempt exactly one queued request. A start-time availability/spawn
    # failure is a finished event so the caller can report it and call again;
    # successful starts return :started and own the single live adapter.
    def start_next
      return if active? || @pending.empty?

      item = @pending.shift
      item.started_at = now
      unless item.agent.available?
        finish_item(item, status: :failed,
                          error: "#{item.entry.provider} became unavailable before start")
        return Event.new(type: :finished, request: snapshot(item))
      end

      item.status = :running
      @active = item
      @agent = item.agent
      @agent.start(item.prompt, model: item.entry.model)
      Event.new(type: :started, request: snapshot(item))
    rescue StandardError => e
      @active = nil
      @agent = nil
      finish_item(item, status: :failed, error: "could not start agent: #{e.message}")
      Event.new(type: :finished, request: snapshot(item))
    end

    # Drain one readable chunk. A finished adapter is recorded but the next
    # request is deliberately not started here: App first reloads task state,
    # then advances the queue to preserve a visible checkpoint between runs.
    def pump
      return unless active?
      return unless @agent.pump == :done

      status = @agent.success? ? :succeeded : :failed
      error = process_error(@agent) if status == :failed
      item = @active
      finish_item(item, status: status, output: @agent.output,
                        exit_status: @agent.exit_status, error: error)
      clear_active
      Event.new(type: :finished, request: snapshot(item))
    rescue StandardError => e
      item = @active
      agent = @agent
      cancel_error = nil
      begin
        agent.cancel
      rescue StandardError => cancel_exception
        cancel_error = cancel_exception.message
      end
      message = "agent stream failed: #{e.message}"
      message += " (cleanup also failed: #{cancel_error})" if cancel_error
      finish_item(item, status: :failed, output: agent.output,
                        exit_status: agent.exit_status, error: message)
      clear_active
      Event.new(type: :finished, request: snapshot(item))
    end

    def cancel_active
      return unless active?

      item = @active
      @agent.cancel
      finish_item(item, status: :cancelled, output: @agent.output,
                        exit_status: @agent.exit_status, error: "cancelled")
      clear_active
      Event.new(type: :finished, request: snapshot(item))
    end

    def cancel_pending
      cancelled = @pending.map do |item|
        finish_item(item, status: :cancelled, error: "cancelled before start")
        snapshot(item)
      end
      @pending.clear
      cancelled
    end

    # Exit path: stop everything without advancing to another request.
    def shutdown
      active_event = cancel_active
      cancelled = cancel_pending
      [active_event, *cancelled].compact
    end

    def io = @agent&.io
    def active? = !@active.nil?
    def pending? = !@pending.empty?
    def pending_count = @pending.size
    def submitted_count = @next_id - 1
    def any? = !@items.empty?
    def work? = active? || pending?
    def active_output = @agent&.output.to_s
    def active_request = @active && snapshot(@active)
    def requests = @items.map { |item| snapshot(item) }.freeze

    def latest_finished
      item = @items.reverse_each.find(&:finished?)
      item && snapshot(item)
    end

    private

    def now = @clock.call

    # LLM::Entry is intentionally mutable for registry/config assembly. Queue
    # requests are not: provider/model must remain exactly as submitted even if
    # the UI selection or a returned snapshot is mutated later.
    def immutable_entry(entry)
      copy = entry.dup
      copy.provider = immutable_value(entry.provider)
      copy.model = immutable_value(entry.model)
      copy.freeze
    end

    def immutable_value(value)
      value.is_a?(String) ? value.dup.freeze : value
    end

    def finish_item(item, status:, output: nil, exit_status: nil, error: nil)
      item.status = status
      item.finished_at = now
      item.output = output.to_s.dup
      item.exit_status = exit_status
      item.error = error
      item.agent = nil
      trim_history
      item
    end

    def clear_active
      @active = nil
      @agent = nil
    end

    def process_error(agent)
      status = agent.process_status
      return "agent exited without a process status" unless status
      return "agent exited #{status.exitstatus}" if status.exited?
      return "agent terminated by signal #{status.termsig}" if status.signaled?

      "agent did not exit cleanly"
    end

    def trim_history
      excess = @items.count(&:finished?) - @history_limit
      return unless excess.positive?

      @items.delete_if do |item|
        remove = excess.positive? && item.finished?
        excess -= 1 if remove
        remove
      end
    end

    def snapshot(item)
      output = item.equal?(@active) ? active_output : item.output.to_s
      Snapshot.new(
        id: item.id,
        prompt: item.prompt,
        entry: item.entry,
        status: item.status,
        queued_at: item.queued_at,
        started_at: item.started_at,
        finished_at: item.finished_at,
        output: output.dup.freeze,
        exit_status: item.exit_status,
        error: item.error&.dup&.freeze
      )
    end
  end
end
