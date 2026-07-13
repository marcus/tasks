# frozen_string_literal: true

module Tasks
  # Typed outcome from Store#patch_task!. Refusals are values, not exceptions,
  # so an editor can retain the pending buffer and present the right recovery.
  class PatchResult
    STATUSES = %i[ok no_change conflict missing invalid cycle too_deep].freeze

    attr_reader :status, :snapshot, :errors, :field_errors, :form_errors,
                :touched_ids, :summary

    def initialize(status:, snapshot: nil, errors: [], field_errors: {},
                   form_errors: nil, touched_ids: [], summary: nil)
      status = status.to_sym
      raise ArgumentError, "unknown patch status #{status.inspect}" unless STATUSES.include?(status)

      @status = status
      @snapshot = snapshot
      @errors = immutable(Array(errors))
      @field_errors = immutable(field_errors)
      @form_errors = immutable(form_errors.nil? ? Array(errors) : Array(form_errors))
      @touched_ids = immutable(Array(touched_ids))
      @summary = immutable(summary)
      freeze
    end

    def ok? = status == :ok || status == :no_change
    def changed? = status == :ok
    def no_change? = status == :no_change
    def conflict? = status == :conflict
    def missing? = status == :missing
    def invalid? = status == :invalid
    def cycle? = status == :cycle
    def too_deep? = status == :too_deep

    private

    def immutable(value)
      case value
      when Hash
        value.each_with_object({}) { |(key, item), copy| copy[key] = immutable(item) }.freeze
      when Array
        value.map { |item| immutable(item) }.freeze
      when String
        value.dup.freeze
      else
        value.freeze
      end
    end
  end
end
