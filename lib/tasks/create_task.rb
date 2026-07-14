# frozen_string_literal: true

module Tasks
  # Immutable, transport-neutral input for creating one live task. Store owns
  # semantic validation and the transaction itself; this command only captures
  # the complete requested value set without retaining mutable caller input.
  #
  # `body` is the canonical initial note input. It accepts either one String
  # (including embedded newlines) or an ordered Array of note strings. `notes`
  # is a descriptive alias for Array callers. Store rejects requests that try
  # to supply both, so the persisted order is never implicit.
  class CreateTask
    attr_reader :title, :priority, :tags, :scheduled, :deadline, :state,
                :project, :parent_id, :recurrence, :body, :notes

    alias text title
    alias due deadline
    alias under parent_id
    alias recur recurrence

    def initialize(title: nil, text: nil, priority: nil, tags: [],
                   scheduled: nil, deadline: nil, due: nil, state: nil,
                   project: nil, parent_id: nil, under: nil,
                   recurrence: nil, recur: nil, body: nil, notes: nil)
      @title = immutable(title.nil? ? text : title)
      @priority = immutable(priority)
      @tags = immutable(tags)
      @scheduled = immutable(scheduled)
      @deadline = immutable(deadline.nil? ? due : deadline)
      @state = immutable(state)
      @project = immutable(project)
      @parent_id = immutable(parent_id.nil? ? under : parent_id)
      @recurrence = immutable(recurrence.nil? ? recur : recurrence)
      @body = immutable(body)
      @notes = immutable(notes)
      freeze
    end

    private

    def immutable(value)
      case value
      when Hash
        value.each_with_object({}) { |(key, child), copy| copy[immutable(key)] = immutable(child) }.freeze
      when Array
        value.map { |child| immutable(child) }.freeze
      when String
        value.dup.freeze
      else
        value.freeze
      end
    end
  end
end
