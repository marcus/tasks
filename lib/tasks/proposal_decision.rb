# frozen_string_literal: true

module Tasks
  # Immutable, transport-neutral intent to accept or decline one proposal.
  # Store owns the atomic state/descendant/revision checks.
  class ProposalDecision
    ACTIONS = %i[approve reject].freeze

    attr_reader :id, :action, :expected_revision

    def initialize(id:, action:, expected_revision: nil)
      @id = immutable(id)
      @action = action.respond_to?(:to_sym) ? action.to_sym : action
      @expected_revision = immutable(expected_revision)
      freeze
    end

    private

    def immutable(value)
      value.is_a?(String) ? value.dup.freeze : value
    end
  end
end
