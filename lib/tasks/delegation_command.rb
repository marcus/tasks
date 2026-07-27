# frozen_string_literal: true

require_relative "delegation"

module Tasks
  # One immutable, transport-neutral delegation intent. Store owns eligibility,
  # stamping, validation, and the atomic write; this only carries what the
  # caller asked for, in one shape the CLI, HTTP, and TUI adapters all build.
  #
  # `keep_state` and `note` are the two composition flags: a human delegation
  # moves the task to WAITING unless the owner keeps the state, and a release
  # may carry a blocker note appended in the same undo step. Both are owned by
  # Tasks::Application, not by Store, because they compose two writes.
  class DelegationCommand
    ACTIONS = %i[delegate undelegate claim release work_ref].freeze
    # Clearing a work reference is spelled `off` at every surface (`tasks
    # workref <ref> off`, the TUI form) and reaches Store as nil.
    CLEAR = "off"

    attr_reader :id, :action, :kind, :mode, :assignee, :worker, :work_ref, :note,
                :keep_state, :force, :expected_revision

    def initialize(id:, action:, kind: nil, mode: nil, assignee: nil, worker: nil,
                   work_ref: nil, note: nil, keep_state: false, force: false,
                   expected_revision: nil)
      @action = action.respond_to?(:to_sym) ? action.to_sym : action
      raise ArgumentError, "unknown delegation action: #{action}" unless ACTIONS.include?(@action)

      @id = immutable(id)
      @kind = immutable(kind&.to_s)
      @mode = immutable(mode&.to_s)
      @assignee = immutable(assignee)
      @worker = immutable(worker)
      @work_ref = normalize_work_ref(work_ref)
      @note = immutable(note)
      @keep_state = keep_state == true
      @force = force == true
      @expected_revision = immutable(expected_revision)
      freeze
    end

    def human? = kind == "human"
    def agent? = kind == "agent"
    def clears_work_ref? = action == :work_ref && work_ref.nil?

    private

    # nil and `off` are the same instruction; anything else is a reference
    # string Store validates (non-empty, single line).
    def normalize_work_ref(value)
      return nil if value.nil? || value == :off
      text = value.to_s
      return nil if text.strip.casecmp?(CLEAR)

      text.dup.freeze
    end

    def immutable(value)
      value.is_a?(String) ? value.dup.freeze : value
    end
  end
end
