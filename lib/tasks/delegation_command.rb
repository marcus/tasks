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
    # Clearing a work reference is spelled `off` or `none` at every surface
    # (`tasks workref <ref> off`, the TUI's `W` form, an HTTP body) and reaches
    # Store as nil. Both words normalize here, once: when a surface kept its own
    # list the CLI stored the literal string "none" while the TUI cleared.
    CLEAR_WORDS = %w[off none].freeze
    CLEAR = CLEAR_WORDS.first
    # The same instruction spelled as a symbol by a programmatic caller.
    CLEAR_SYMBOLS = CLEAR_WORDS.map(&:to_sym).freeze

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

    # nil, `off`, and `none` are the same instruction; anything else is a
    # reference string Store validates (non-empty, single line, bounded).
    def normalize_work_ref(value)
      return nil if value.nil? || CLEAR_SYMBOLS.include?(value)

      text = utf8(value.to_s)
      # Bytes that are still not valid UTF-8 after re-tagging cannot be stripped
      # or compared without raising, so hand them through untouched: Store owns
      # the refusal, and a typed refusal beats a backtrace from a constructor.
      return text.dup.freeze unless text.valid_encoding?
      return nil if CLEAR_WORDS.any? { |word| text.strip.casecmp?(word) }

      text.dup.freeze
    end

    # Text from argv or a TUI field carries the process locale's encoding
    # (BINARY under LANG=C), and every String operation below would raise
    # Encoding::CompatibilityError on it. Same recovery as Store#utf8.
    def utf8(text)
      return text if text.encoding == Encoding::UTF_8

      recoded = text.dup.force_encoding(Encoding::UTF_8)
      recoded.valid_encoding? ? recoded : text
    end

    def immutable(value)
      value.is_a?(String) ? value.dup.freeze : value
    end
  end
end
