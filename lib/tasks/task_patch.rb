# frozen_string_literal: true

require_relative "edit_snapshot"

module Tasks
  # One normalized editor mutation plus the semantic value it was based on.
  class TaskPatch
    FIELDS = EditSnapshot::FIELDS

    attr_reader :id, :field, :value, :expected, :coalesce_key, :confirmation,
                :history_label, :force

    alias target_id id
    alias field_key field
    alias confirmed_consequence confirmation

    def initialize(id: nil, target_id: nil, field:, value:, expected:,
                   coalesce_key: nil, confirmation: nil, confirmed_consequence: nil,
                   history_label: nil, force: false)
      @id = immutable(id || target_id)
      @field = normalize_field(field)
      @value = immutable(value)
      @expected = immutable(expected)
      @coalesce_key = immutable(coalesce_key)
      @confirmation = immutable(confirmation || confirmed_consequence)
      @history_label = immutable(history_label)
      @force = force == true
      freeze
    end

    def self.from(snapshot, field:, value:, **options)
      new(id: snapshot.id, field: field, value: value,
          expected: snapshot.expected_for(field), **options)
    end

    private

    def normalize_field(field)
      field = field.to_sym
      field == :recur ? :recurrence : field
    end

    def immutable(value)
      case value
      when Hash
        value.each_with_object({}) do |(key, item), copy|
          copy[immutable(key)] = immutable(item)
        end.freeze
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
