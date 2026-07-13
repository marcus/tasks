# frozen_string_literal: true

require_relative "term_form_event"
require_relative "term_form_model"

module TermForm
  class Form
    attr_reader :groups, :fields, :focus_key, :key_map, :pending_commit

    def initialize(groups:, key_map: KeyMap.new, focus: nil)
      @groups = Array(groups).dup.freeze
      raise ArgumentError, "groups must all be TermForm::Group values" unless @groups.all? { |group| group.is_a?(Group) }
      raise ArgumentError, "key_map must be a TermForm::KeyMap" unless key_map.is_a?(KeyMap)

      @fields = @groups.flat_map(&:fields).freeze
      ensure_unique_keys!
      @field_by_key = @fields.to_h { |field| [field.key, field] }.freeze
      @group_by_field = @groups.each_with_object({}) do |group, result|
        group.fields.each { |field| result[field.key] = group }
      end.freeze
      @values = @fields.to_h { |field| [field.key, Support.copy(field.initial_value)] }
      @baselines = @fields.to_h { |field| [field.key, Support.copy(field.initial_baseline)] }
      @errors = {}
      @validation_active = false
      @key_map = key_map
      @commit_sequence = 0
      @pending_commit = nil
      @focus_key = normalize_optional_key(focus)
      raise ArgumentError, "unknown focus key: #{@focus_key}" if @focus_key && !@field_by_key.key?(@focus_key)

      ensure_focus!
    end

    def values = Support.frozen_copy(@values)
    def baselines = Support.frozen_copy(@baselines)
    def errors = Support.frozen_copy(@errors)
    def context = Context.new(values: @values, baselines: @baselines, focused_key: @focus_key, errors: @errors)
    def field(key) = @field_by_key.fetch(resolve_key(key))
    def value(key) = Support.frozen_copy(@values.fetch(resolve_key(key)))
    def baseline(key) = Support.frozen_copy(@baselines.fetch(resolve_key(key)))
    def dirty?(key = nil) = context.dirty?(key)
    def changed_keys = context.changed_keys
    def pending? = !@pending_commit.nil?

    def visible_fields
      current = context
      @fields.select { |field| field_visible?(field, current) }.freeze
    end

    def focusable_fields
      current = context
      @fields.select { |field| field_focusable?(field, current) }.freeze
    end

    def focus(key, event: nil)
      normalized = resolve_key(key)
      return transition(:handled, event) unless focusable_fields.any? { |field| field.key == normalized }

      changed = @focus_key != normalized
      @focus_key = normalized
      transition(changed ? :focus_changed : :handled, event)
    end

    def focus_next(event: nil) = move_focus(1, event: event)
    def focus_previous(event: nil) = move_focus(-1, event: event)

    def set_value(key, value, event: nil)
      normalized = resolve_key(key)
      copied = Support.copy(value)
      return transition(:handled, event) if @values[normalized] == copied

      old_focus = @focus_key
      @values[normalized] = copied
      validate if @validation_active
      ensure_focus!(after: old_focus)
      transition(:changed, event, changed_key: normalized)
    end
    alias change set_value

    def validate
      current = context
      @errors = @fields.each_with_object({}) do |field, result|
        next unless field_focusable?(field, current)

        messages = field.validation_errors(@values.fetch(field.key), current)
        result[field.key] = messages unless messages.empty?
      end
      @validation_active = true
      errors
    end

    def valid?
      validate.empty?
    end

    def request_commit(intended_focus: nil, direction: nil, field_key: nil, event: nil)
      return transition(:commit_pending, event, request: @pending_commit) if pending?

      validate
      unless @errors.empty?
        first_error = @errors.each_key.find { |key| focusable_fields.any? { |field| field.key == key } }
        @focus_key = first_error if first_error
        return transition(:invalid, event, errors: errors)
      end

      intended = intended_focus.nil? ? @focus_key : resolve_key(intended_focus)
      committed_key = field_key.nil? ? @focus_key : resolve_key(field_key)
      raise RuntimeError, "cannot request a commit without a focused field" unless committed_key

      @commit_sequence += 1
      @pending_commit = CommitRequest.new(
        token: @commit_sequence,
        values: @values,
        changed_keys: changed_keys,
        focus_key: @focus_key,
        field_key: committed_key,
        proposed_value: @values.fetch(committed_key),
        expected_baseline: @baselines.fetch(committed_key),
        intended_focus: intended,
        direction: direction,
      )
      transition(:commit_requested, event, request: @pending_commit, values: @pending_commit.values)
    end

    def accept_commit(fresh_values = nil, values: nil, token: nil)
      request = require_pending!(token)
      raise ArgumentError, "pass fresh values either positionally or with values:, not both" if fresh_values && values

      host_values = normalize_values(values || fresh_values || {})
      committed_value = host_values.delete(request.field_key) { request.proposed_value }

      current = @values.fetch(request.field_key)
      @values[request.field_key] = Support.copy(committed_value) if current == request.proposed_value
      @baselines[request.field_key] = Support.copy(committed_value)

      host_values.each do |key, fresh|
        current = @values.fetch(key)
        old_baseline = @baselines.fetch(key)
        @values[key] = Support.copy(fresh) if current == old_baseline
        @baselines[key] = Support.copy(fresh)
      end
      @pending_commit = nil
      @errors = {}
      @validation_active = false
      focus(request.intended_focus) if request.intended_focus
      ensure_focus!
      transition(:commit_accepted, nil, request: request)
    end
    alias accept accept_commit

    def reject_commit(errors: nil, message: nil, token: nil)
      request = require_pending!(token)
      @pending_commit = nil
      supplied = normalize_errors(errors)
      supplied[:base] = [message.to_s] if message
      unless supplied.empty?
        @errors = supplied
        @validation_active = true
      end
      transition(:commit_rejected, nil, request: request, errors: self.errors)
    end
    alias reject reject_commit

    def refresh(fresh_values = nil, values: nil)
      raise ArgumentError, "pass fresh values either positionally or with values:, not both" if fresh_values && values

      normalize_values(values || fresh_values || {}).each do |key, fresh|
        current = @values.fetch(key)
        old_baseline = @baselines.fetch(key)
        @values[key] = Support.copy(fresh) if current == old_baseline
        @baselines[key] = Support.copy(fresh)
      end
      validate if @validation_active
      ensure_focus!
      transition(:refreshed)
    end

    def handle(value)
      event = value.is_a?(String) ? @key_map.event_for(value) : Event.normalize(value)
      case event.type
      when :next then navigate_or_commit(1, :next, event)
      when :previous then navigate_or_commit(-1, :previous, event)
      when :focus then focus(event.key, event: event)
      when :change then set_value(event.key, event.value, event: event)
      when :commit then request_commit(intended_focus: event[:intended_focus], event: event)
      when :cancel then transition(:cancel_requested, event)
      else transition(:unhandled, event)
      end
    end

    def render_model
      current = context
      row_index = 0
      rendered_groups = @groups.filter_map do |group|
        next unless group.visible?(current)

        group_enabled = group.enabled?(current)
        rows = group.fields.filter_map do |field|
          next unless field.visible?(current)

          value = @values.fetch(field.key)
          enabled = group_enabled && field.enabled?(current)
          focused = field.key == @focus_key
          row = RenderModel::Row.new(
            key: field.key,
            group_key: group.key,
            label: field.label_for(current),
            value: value,
            index: row_index,
            enabled: enabled,
            focused: focused,
            dirty: current.dirty?(field.key),
            required: field.required?(current),
            errors: @errors.fetch(field.key, []),
            cursor: focused ? field.cursor_for(value, current) : nil,
            metadata: field.metadata,
          )
          row_index += 1
          row
        end
        RenderModel::Group.new(key: group.key, label: group.label_for(current), rows: rows,
                               enabled: group_enabled, metadata: group.metadata)
      end
      RenderModel.new(groups: rendered_groups, focused_key: @focus_key, errors: @errors)
    end

    private

    def transition(type, event = nil, **data)
      Transition.new(type, event: event, focus_key: @focus_key, render_model: render_model, **data)
    end

    def move_focus(offset, event: nil)
      candidates = focusable_fields
      return transition(:handled, event) if candidates.empty?

      index = candidates.index { |field| field.key == @focus_key }
      target = index ? candidates[(index + offset) % candidates.length] : candidates.first
      focus(target.key, event: event)
    end

    def navigate_or_commit(offset, direction, event)
      candidates = focusable_fields
      return transition(:handled, event) if candidates.empty?

      index = candidates.index { |field| field.key == @focus_key }
      target = index ? candidates[(index + offset) % candidates.length] : candidates.first
      if @focus_key && dirty?(@focus_key)
        request_commit(intended_focus: target.key, direction: direction, field_key: @focus_key, event: event)
      else
        focus(target.key, event: event)
      end
    end

    def field_visible?(field, current)
      group = @group_by_field.fetch(field.key)
      group.visible?(current) && field.visible?(current)
    end

    def field_focusable?(field, current)
      group = @group_by_field.fetch(field.key)
      group.visible?(current) && group.enabled?(current) && field.visible?(current) && field.enabled?(current)
    end

    def ensure_focus!(after: nil)
      candidates = focusable_fields
      if @focus_key && candidates.any? { |field| field.key == @focus_key }
        return @focus_key
      end
      if after && (position = @fields.index { |field| field.key == after })
        ordered = @fields.rotate(position + 1)
        @focus_key = ordered.find { |field| candidates.include?(field) }&.key
      else
        @focus_key = candidates.first&.key
      end
    end

    def ensure_unique_keys!
      keys = @groups.map(&:key) + @fields.map(&:key)
      duplicate = keys.tally.find { |_key, count| count > 1 }&.first
      raise ArgumentError, "duplicate TermForm key: #{duplicate}" if duplicate
    end

    def resolve_key(key)
      normalized = Support.key(key)
      raise KeyError, "unknown field key: #{normalized}" unless @field_by_key.key?(normalized)

      normalized
    end

    def normalize_optional_key(key)
      key.nil? ? nil : Support.key(key)
    end

    def normalize_values(values)
      raise ArgumentError, "values must be a Hash" unless values.is_a?(Hash)

      values.each_with_object({}) do |(key, value), result|
        result[resolve_key(key)] = Support.copy(value)
      end
    end

    def normalize_errors(errors)
      return {} unless errors
      raise ArgumentError, "errors must be a Hash" unless errors.is_a?(Hash)

      errors.each_with_object({}) do |(key, messages), result|
        normalized = key.to_sym == :base ? :base : resolve_key(key)
        result[normalized] = Array(messages).map(&:to_s)
      end
    end

    def require_pending!(token)
      raise RuntimeError, "no commit is pending" unless @pending_commit
      raise ArgumentError, "commit token does not match" if token && token != @pending_commit.token

      @pending_commit
    end
  end
end
