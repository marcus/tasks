# frozen_string_literal: true

require_relative "edit_snapshot"

module Tasks
  # An immutable, transport-neutral request to change one task in one checked
  # transaction. `expected_revision` is an opaque Store-produced value: callers
  # must carry it forward from an EditSnapshot rather than construct it from a
  # file coordinate or a wall-clock timestamp.
  #
  # The order here is part of the command contract. Several fields interact
  # (dates and recurrence, moves and lifecycle state), so Store applies a
  # changeset in this exact sequence instead of accepting Hash insertion order.
  class TaskChangeset
    # Explicit transport-neutral command for "move to the task's current
    # enclosing section." A plain nil remains invalid: legacy adapters can
    # produce nil from a stale/missing parent, and that must never retarget a
    # task as an accidental unnest.
    UNNEST = Object.new.freeze

    FIELD_ORDER = %i[
      title priority body
      contexts tags deferred tag_delta
      activate
      scheduled deadline date_clear
      recurrence
      location
      state
    ].freeze

    EDIT_FIELDS = EditSnapshot::FIELDS.freeze
    SPECIAL_FIELDS = %i[tag_delta activate date_clear].freeze
    FIELDS = (EDIT_FIELDS + SPECIAL_FIELDS).freeze

    attr_reader :id, :changes, :expected_revision, :coalesce_key, :confirmation,
                :history_label, :force, :duplicate_fields

    alias target_id id
    alias field_values changes
    alias confirmed_consequence confirmation

    def initialize(id: nil, target_id: nil, changes:, expected_revision: nil,
                   coalesce_key: nil, confirmation: nil, confirmed_consequence: nil,
                   history_label: nil, force: false)
      @id = immutable(id || target_id)
      @changes, @duplicate_fields = normalize_changes(changes)
      @expected_revision = immutable(expected_revision)
      @coalesce_key = immutable(coalesce_key)
      @confirmation = immutable(confirmation || confirmed_consequence)
      @history_label = immutable(history_label)
      @force = force == true
      freeze
    end

    # Builds a whole-task optimistic-concurrency request from a Store snapshot.
    # The snapshot owns the semantic revision, so neither an mtime nor a JSONL
    # line shift can accidentally become a write precondition.
    def self.from(snapshot, changes:, **options)
      new(id: snapshot.id, changes: changes, expected_revision: snapshot.revision, **options)
    end

    # TaskPatch remains the save-on-blur convenience used by the CLI and TUI.
    # It deliberately omits a whole-task revision: Store retains its established
    # field-scoped expected-value check while sharing this command's validation,
    # ordering, atomic write, and history path.
    def self.from_patch(patch)
      new(
        id: patch.id, changes: { patch.field => patch.value },
        coalesce_key: patch.respond_to?(:coalesce_key) ? patch.coalesce_key : nil,
        confirmation: patch.respond_to?(:confirmation) ? patch.confirmation : nil,
        history_label: patch.respond_to?(:history_label) ? patch.history_label : nil,
        force: patch.respond_to?(:force) && patch.force
      )
    end

    def fields
      return [] unless changes.is_a?(Hash)

      changes.keys
    end

    def ordered_fields
      fields.sort_by do |field|
        index = FIELD_ORDER.index(field)
        [index || FIELD_ORDER.length, field.to_s]
      end
    end

    private

    def normalize_changes(changes)
      return [immutable(changes), [].freeze] unless changes.is_a?(Hash)

      normalized = {}
      duplicates = []
      changes.each do |field, value|
        key = normalize_field(field)
        duplicates << key if normalized.key?(key)
        normalized[key] = value
      end
      [immutable(normalized), immutable(duplicates)]
    end

    def normalize_field(field)
      key = field.to_sym
      key == :recur ? :recurrence : key
    rescue NoMethodError
      field
    end

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
