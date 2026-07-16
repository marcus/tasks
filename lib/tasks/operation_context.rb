# frozen_string_literal: true

module Tasks
  # Immutable metadata that identifies where a domain operation originated.
  # Commands do not interpret this yet; the application facade will carry it
  # through its typed command results and future audit/logging seams.
  class OperationContext
    SOURCES = %i[cli tui api].freeze

    attr_reader :operation_id, :source, :actor, :temporal_context

    def initialize(operation_id:, source:, actor: nil, temporal_context: nil)
      @operation_id = required_text(operation_id, "operation_id")
      source_name = required_text(source, "source")
      @source = SOURCES.find { |candidate| candidate.to_s == source_name }
      raise ArgumentError, "unknown operation source: #{source_name}" unless @source
      @actor = optional_text(actor)
      @temporal_context = temporal_context
      freeze
    end

    def to_h
      { operation_id: operation_id, source: source, actor: actor }
    end

    private

    def required_text(value, name)
      text = value.to_s.strip
      raise ArgumentError, "#{name} is required" if text.empty?

      text.freeze
    end

    def optional_text(value)
      return nil if value.nil?

      text = value.to_s.strip
      text.empty? ? nil : text.freeze
    end
  end
end
