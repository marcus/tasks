# frozen_string_literal: true

module Tasks
  # Immutable, transport-neutral input for creating one live task. Store owns
  # semantic validation and the transaction itself; this command only captures
  # the complete requested value set without retaining mutable caller input.
  #
  # `body` is the canonical initial note input. It accepts either one String
  # (including embedded newlines) or an ordered Array of note strings. `notes`
  # is a descriptive alias for Array callers. Store rejects requests that try
  # to supply both, so the persisted order is never implicit. `deferred` owns
  # the task's initial indefinite On Hold marker and must be boolean.
  class CreateTask
    attr_reader :title, :priority, :tags, :deferred, :scheduled, :deadline, :state,
                :project, :parent_id, :recurrence, :body, :notes, :apply_host_context

    alias text title
    alias due deadline
    alias under parent_id
    alias recur recurrence

    def initialize(title: nil, text: nil, priority: nil, tags: [], deferred: false,
                   scheduled: nil, deadline: nil, due: nil, state: nil,
                   project: nil, parent_id: nil, under: nil,
                   recurrence: nil, recur: nil, body: nil, notes: nil,
                   apply_host_context: true)
      @title = immutable(title.nil? ? text : title)
      @priority = immutable(priority)
      @tags = immutable(tags)
      @deferred = immutable(deferred)
      @scheduled = immutable(scheduled)
      @deadline = immutable(deadline.nil? ? due : deadline)
      @state = immutable(state)
      @project = immutable(project)
      @parent_id = immutable(parent_id.nil? ? under : parent_id)
      @recurrence = immutable(recurrence.nil? ? recur : recurrence)
      @body = immutable(body)
      @notes = immutable(notes)
      @apply_host_context = immutable(apply_host_context)
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

  # Applies creation defaults that belong to the application runtime rather
  # than persistence. The Store receives a complete CreateTask and remains
  # unaware of hostnames or configuration.
  module CreateTaskPolicy
    module_function

    def apply_host_context(command, host_context)
      return command unless command.is_a?(CreateTask)
      return command unless command.apply_host_context == true
      return command if host_context.nil?
      return command unless command.tags.is_a?(Array)

      contexts, tags = command.tags.partition do |tag|
        tag.is_a?(String) && tag.start_with?("@")
      end
      effective_contexts = [host_context, *contexts].uniq
      rebuild(command, tags: effective_contexts + tags)
    end

    def rebuild(command, tags:)
      CreateTask.new(
        title: command.title, priority: command.priority, tags: tags,
        deferred: command.deferred, scheduled: command.scheduled,
        deadline: command.deadline, state: command.state, project: command.project,
        parent_id: command.parent_id, recurrence: command.recurrence,
        body: command.body, notes: command.notes,
        apply_host_context: command.apply_host_context
      )
    end
    private_class_method :rebuild
  end
end
