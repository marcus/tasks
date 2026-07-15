# frozen_string_literal: true

module Tasks
  # Immutable transport-neutral result for coherent application reads. The
  # HTTP adapter can map this vocabulary to 200/404/503 without receiving a
  # Store, a path, or a second change-token read.
  class ApplicationReadResult
    STATUSES = %i[ok not_found store_invalid unavailable].freeze

    attr_reader :status, :data, :store_revision, :errors, :warnings

    def initialize(status:, data: nil, store_revision: nil, errors: [], warnings: [])
      @status = status.to_sym
      raise ArgumentError, "unknown application-read status #{@status.inspect}" unless STATUSES.include?(@status)

      @data = immutable(data)
      @store_revision = store_revision&.dup&.freeze
      @errors = immutable(errors)
      @warnings = immutable(warnings)
      freeze
    end

    def ok? = status == :ok
    def not_found? = status == :not_found
    def store_invalid? = status == :store_invalid
    def unavailable? = status == :unavailable

    private

    def immutable(value)
      case value
      when Hash
        value.each_with_object({}) do |(key, child), copy|
          copy[immutable(key)] = immutable(child)
        end.freeze
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
