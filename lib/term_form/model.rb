# frozen_string_literal: true

require_relative "support"

module TermForm
  class Context
    attr_reader :values, :baselines, :focused_key, :errors

    def initialize(values:, baselines:, focused_key:, errors: {})
      @values = Support.frozen_copy(values)
      @baselines = Support.frozen_copy(baselines)
      @focused_key = focused_key
      @errors = Support.frozen_copy(errors)
      freeze
    end

    def [](key) = @values[Support.key(key)]
    def fetch(key, *fallback, &block) = @values.fetch(Support.key(key), *fallback, &block)
    def baseline(key) = @baselines[Support.key(key)]

    def dirty?(key = nil)
      return changed_keys.any? unless key

      normalized = Support.key(key)
      @values.fetch(normalized) != @baselines.fetch(normalized)
    end

    def changed_keys
      @values.each_key.select { |key| dirty?(key) }.freeze
    end
  end

  class Field
    UNSET = Object.new.freeze
    Result = Data.define(:status, :value) do
      def changed? = status == :changed
      def handled? = status == :handled
    end

    attr_reader :key, :label, :initial_value, :initial_baseline, :metadata

    def initialize(key:, value: nil, baseline: UNSET, label: nil, visible: true, enabled: true,
                   required: false, validate: nil, cursor: nil, metadata: {})
      @key = Support.key(key)
      @label = Support.frozen_copy(label || @key.to_s)
      @initial_value = Support.frozen_copy(value)
      baseline = value if baseline.equal?(UNSET)
      @initial_baseline = Support.frozen_copy(baseline)
      @visible = visible
      @enabled = enabled
      @required = required
      @validators = Array(validate).compact.freeze
      @cursor = cursor
      @metadata = Support.frozen_copy(metadata)
      freeze
    end

    def visible?(context) = !!Support.property(@visible, context)
    def enabled?(context) = !!Support.property(@enabled, context)
    def required?(context) = !!Support.property(@required, context)
    def label_for(context) = Support.frozen_copy(Support.property(@label, context))

    def cursor_for(value, context)
      return nil if @cursor.nil?
      return @cursor unless @cursor.respond_to?(:call)

      Support.callable(@cursor, value, context)
    end

    # Stateful field subclasses consume normalized events here. Returning nil
    # leaves the event to Form's navigation/commit protocol.
    def handle_event(_event, _value, _context) = nil

    def normalize_value(value) = value

    # Stateful fields can add current, renderer-neutral presentation data while
    # preserving the immutable metadata supplied by the host.
    def metadata_for(_value, _context) = metadata

    # Form calls this after host-driven refreshes or direct value changes so a
    # stateful field can reconcile its private editing buffer.
    def sync_value(_value) = nil

    def validation_errors(value, context)
      errors = []
      errors << "is required" if required?(context) && blank?(value)
      @validators.each do |validator|
        result = Support.callable(validator, value, context)
        errors.concat(Array(result == false ? "is invalid" : result).compact.map(&:to_s))
      end
      errors.freeze
    end

    private

    def blank?(value)
      value.nil? || (value.respond_to?(:empty?) && value.empty?)
    end
  end

  class Group
    attr_reader :key, :label, :fields, :metadata

    def initialize(key:, fields:, label: nil, visible: true, enabled: true, metadata: {})
      @key = Support.key(key)
      @label = Support.frozen_copy(label || @key.to_s)
      @fields = Array(fields).dup.freeze
      raise ArgumentError, "group fields must all be TermForm::Field values" unless @fields.all? { |field| field.is_a?(Field) }

      @visible = visible
      @enabled = enabled
      @metadata = Support.frozen_copy(metadata)
      freeze
    end

    def visible?(context) = !!Support.property(@visible, context)
    def enabled?(context) = !!Support.property(@enabled, context)
    def label_for(context) = Support.frozen_copy(Support.property(@label, context))
  end

  class RenderModel
    class Row
      attr_reader :key, :group_key, :label, :value, :index, :enabled, :focused,
                  :pending, :dirty, :required, :errors, :cursor, :metadata

      def initialize(key:, group_key:, label:, value:, index:, enabled:, focused:,
                     dirty:, required:, errors:, cursor:, metadata:, pending: false)
        @key = key
        @group_key = group_key
        @label = Support.frozen_copy(label)
        @value = Support.frozen_copy(value)
        @index = index
        @enabled = enabled
        @focused = focused
        @pending = pending
        @dirty = dirty
        @required = required
        @errors = Support.frozen_copy(errors)
        @cursor = cursor
        @metadata = Support.frozen_copy(metadata)
        freeze
      end

      def enabled? = @enabled
      def focused? = @focused
      def pending? = @pending
      def dirty? = @dirty
      def required? = @required
      def error = @errors.first
    end

    class Group
      attr_reader :key, :label, :rows, :enabled, :metadata

      def initialize(key:, label:, rows:, enabled:, metadata:)
        @key = key
        @label = Support.frozen_copy(label)
        @rows = rows.freeze
        @enabled = enabled
        @metadata = Support.frozen_copy(metadata)
        freeze
      end

      def enabled? = @enabled
    end

    Cursor = Data.define(:row, :column, :key)

    attr_reader :groups, :rows, :focused_key, :focused_row, :cursor, :errors

    def initialize(groups:, focused_key:, errors:)
      @groups = groups.freeze
      @rows = groups.flat_map(&:rows).freeze
      @focused_key = focused_key
      @focused_row = @rows.find(&:focused?)
      @cursor = if @focused_row&.cursor
                  Cursor.new(@focused_row.index, @focused_row.cursor, @focused_row.key)
                end
      @errors = Support.frozen_copy(errors)
      freeze
    end

    def focused_row_index = @focused_row&.index
  end

  class CommitRequest
    attr_reader :token, :values, :changed_keys, :focus_key, :field_key,
                :proposed_value, :expected_baseline, :intended_focus, :direction

    def initialize(token:, values:, changed_keys:, focus_key:, field_key:,
                   proposed_value:, expected_baseline:, intended_focus:, direction:)
      @token = token
      @values = Support.frozen_copy(values)
      @changed_keys = changed_keys.dup.freeze
      @focus_key = focus_key
      @field_key = field_key
      @proposed_value = Support.frozen_copy(proposed_value)
      @expected_baseline = Support.frozen_copy(expected_baseline)
      @intended_focus = intended_focus
      @direction = direction
      freeze
    end

    alias intended_direction direction
  end
end
