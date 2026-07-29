# frozen_string_literal: true

module Tasks
  # Immutable, transport-neutral intent to accept or decline one proposal.
  # Store owns the atomic state/descendant/revision checks. `notes` are only
  # meaningful on reject — withdrawal rationale appended to the body in the
  # same write (mirrors repeatable CLI `--note` on `propose` / `reject`).
  class ProposalDecision
    ACTIONS = %i[approve reject].freeze

    attr_reader :id, :action, :expected_revision, :notes

    def initialize(id:, action:, expected_revision: nil, notes: nil)
      @id = immutable(id)
      @action = action.respond_to?(:to_sym) ? action.to_sym : action
      @expected_revision = immutable(expected_revision)
      @notes = immutable(normalize_notes(notes))
      freeze
    end

    private

    def normalize_notes(value)
      return nil if value.nil?

      list = case value
             when String then [value]
             when Array then value
             else
               raise ArgumentError, "notes must be text or an ordered list of text"
             end
      list.each do |note|
        raise ArgumentError, "notes must be text or an ordered list of text" unless note.is_a?(String)
      end
      list
    end

    def immutable(value)
      case value
      when Array
        value.map { |child| immutable(child) }.freeze
      when String
        value.dup.freeze
      else
        value
      end
    end
  end
end
