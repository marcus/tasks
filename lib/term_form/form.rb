# frozen_string_literal: true

require_relative "event"
require_relative "model"

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
      synchronize_fields!
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

    def focus(key, event: nil, direction: nil)
      normalized = resolve_key(key)
      return transition(:handled, event) unless focusable_fields.any? { |field| field.key == normalized }
      return transition(:handled, event) if @focus_key == normalized

      request_focus(normalized, event: event, direction: direction)
    end

    def focus_next(event: nil) = move_focus(1, event: event)
    def focus_previous(event: nil) = move_focus(-1, event: event)

    def set_value(key, value, event: nil)
      normalized = resolve_key(key)
      copied = Support.copy(@field_by_key.fetch(normalized).normalize_value(value))
      return transition(:handled, event) if @values[normalized] == copied

      old_focus = @focus_key
      @values[normalized] = copied
      @field_by_key.fetch(normalized).sync_value(copied)
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
        apply_focus(first_error) if first_error
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
      if host_values.key?(request.field_key)
        committed_value = host_values.delete(request.field_key)
        reconcile_committed_field(request, committed_value)
      elsif @baselines.fetch(request.field_key) == request.expected_baseline
        reconcile_committed_field(request, request.proposed_value)
      elsif @values.fetch(request.field_key) != @baselines.fetch(request.field_key)
        raise ArgumentError, "commit token is stale after refresh"
      end

      host_values.each do |key, fresh|
        current = @values.fetch(key)
        old_baseline = @baselines.fetch(key)
        @values[key] = Support.copy(fresh) if current == old_baseline
        @baselines[key] = Support.copy(fresh)
      end
      @pending_commit = nil
      @errors = {}
      @validation_active = false
      apply_focus(request.intended_focus) if request.intended_focus && focusable_key?(request.intended_focus)
      ensure_focus!
      synchronize_fields!
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
      ensure_focus!
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
      synchronize_fields!
      validate if @validation_active
      ensure_focus!
      transition(:refreshed)
    end

    def handle(value)
      event = value.is_a?(String) ? @key_map.event_for(value) : Event.normalize(value)
      if event.type == :key
        raw = event.raw || event.key
        raw = Event::KEY_BYTES.fetch(raw, raw) if raw.is_a?(Symbol)
        event = @key_map.event_for(raw)
      end
      if @focus_key && (result = @field_by_key.fetch(@focus_key).handle_event(event, @values.fetch(@focus_key), context))
        return set_value(@focus_key, result.value, event: event) if result.changed?
        return transition(:handled, event) if result.handled?
      end

      case event.type
      when :next then focus_next(event: event)
      when :previous then focus_previous(event: event)
      when :focus then focus(event.key, event: event, direction: event[:direction])
      when :change then set_value(event.key, event.value, event: event)
      when :commit then request_commit(intended_focus: event[:intended_focus], event: event)
      when :cancel then transition(:cancel_requested, event)
      else transition(:unhandled, event)
      end
    end

    def render_model
      current = context
      pending_owner_key = @pending_commit&.field_key
      row_index = 0
      rendered_groups = @groups.filter_map do |group|
        group_visible = group.visible?(current)
        group_owns_pending = group.fields.any? { |field| field.key == pending_owner_key }
        next unless group_visible || group_owns_pending

        group_enabled = group_visible && group.enabled?(current)
        rows = group.fields.filter_map do |field|
          pending = field.key == pending_owner_key
          field_visible = group_visible && field.visible?(current)
          next unless field_visible || pending

          value = @values.fetch(field.key)
          enabled = field_visible && group_enabled && field.enabled?(current)
          focused = field.key == @focus_key
          row = RenderModel::Row.new(
            key: field.key,
            group_key: group.key,
            label: field.label_for(current),
            value: value,
            index: row_index,
            enabled: enabled,
            focused: focused,
            pending: pending,
            dirty: current.dirty?(field.key),
            required: field.required?(current),
            errors: @errors.fetch(field.key, []),
            cursor: focused ? field.cursor_for(value, current) : nil,
            metadata: field.metadata_for(value, current),
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
      direction = offset.positive? ? :next : :previous
      focus(target.key, event: event, direction: direction)
    end

    def request_focus(target, event:, direction:)
      if pending?
        if dirty?(@pending_commit.field_key)
          return transition(:commit_pending, event, request: @pending_commit)
        end

        @pending_commit = nil
      end

      if @focus_key && dirty?(@focus_key)
        request_commit(intended_focus: target, direction: direction, field_key: @focus_key, event: event)
      else
        apply_focus(target)
        transition(:focus_changed, event)
      end
    end

    def apply_focus(key)
      @focus_key = key
    end

    def focusable_key?(key)
      focusable_fields.any? { |field| field.key == key }
    end

    def reconcile_committed_field(request, committed_value)
      current = @values.fetch(request.field_key)
      old_baseline = @baselines.fetch(request.field_key)
      if current == request.proposed_value || current == old_baseline
        @values[request.field_key] = Support.copy(committed_value)
      end
      @baselines[request.field_key] = Support.copy(committed_value)
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
      # A pending commit owns logical focus until the host resolves it. Reactivity
      # may hide or disable that field, but moving focus here would orphan the
      # request. render_model keeps the owner available as a semantic pending row.
      if @pending_commit
        apply_focus(@pending_commit.field_key)
        return @focus_key
      end

      candidates = focusable_fields
      if @focus_key && candidates.any? { |field| field.key == @focus_key }
        return @focus_key
      end
      if after && (position = @fields.index { |field| field.key == after })
        ordered = @fields.rotate(position + 1)
        apply_focus(ordered.find { |field| candidates.include?(field) }&.key)
      else
        apply_focus(candidates.first&.key)
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
        normalized = resolve_key(key)
        result[normalized] = Support.copy(@field_by_key.fetch(normalized).normalize_value(value))
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

    def synchronize_fields!
      @fields.each { |field| field.sync_value(@values.fetch(field.key)) }
    end
  end
end
