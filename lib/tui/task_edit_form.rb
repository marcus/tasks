# frozen_string_literal: true

require_relative "../term_form"
require_relative "../tasks/dates"
require_relative "../tasks/recur"

module Tui
  # Task-domain policy adapter around the persistence-neutral TermForm engine.
  # It owns field order, task-specific normalization/options, and the semantic
  # expectations used by Store#patch_task!. The session owns external effects.
  class TaskEditForm
    FIELD_ORDER = %i[
      title priority deferred scheduled deadline recurrence contexts tags body
      state
    ].freeze

    # The "placement" group and its `location` Select (task nesting via parent
    # move) are intentionally omitted: indent/outdent will be handled outside
    # this form. Store#patch_task! still accepts a `:location` patch, so the
    # store/move-level nesting path is untouched — only the form surface is gone.
    GROUPS = {
      basics: %i[title priority deferred],
      timing: %i[scheduled deadline recurrence],
      organization: %i[contexts tags],
      notes: %i[body],
      lifecycle: %i[state],
    }.freeze

    STATE_OPTIONS = %w[INBOX TODO NEXT WAITING DONE CANCELLED].freeze
    PRIORITY_OPTIONS = [[nil, "None"], %w[A A], %w[B B], %w[C C]].freeze
    RECURRENCE_PRESETS = %w[daily weekly monthly yearly].freeze
    DATE_SUGGESTIONS = %w[today tomorrow fri +3].freeze

    attr_reader :form, :snapshot, :store, :today

    def initialize(snapshot:, store: nil, today: -> { Date.today },
                   context_options: nil, tag_options: nil, focus: nil)
      raise ArgumentError, "snapshot must have a stable id" if snapshot.nil? || snapshot.id.to_s.empty?

      @snapshot = snapshot
      @store = store
      @today = today.respond_to?(:call) ? today : -> { today }
      @context_source = context_options
      @tag_source = tag_options
      @expectations = FIELD_ORDER.to_h { |field| [field, snapshot.expected_for(field)] }
      @form = TermForm::Form.new(groups: build_groups, focus: focus)
    end

    def target_id = snapshot.id
    def fields = form.fields
    def field_order = fields.map(&:key).freeze
    def values = form.values
    def value(key) = form.value(key)
    def baseline(key) = form.baseline(key)
    def dirty?(key = nil) = form.dirty?(key)
    def focused_key = form.focus_key
    def pending? = form.pending?
    def pending_blur = form.pending_commit
    def render_model = form.render_model
    def handle(event) = form.handle(event)
    def focus(key) = form.focus(key)

    def read_only
      { id: snapshot.id, closed: snapshot.closed }.freeze
    end

    def expected_for(field)
      @expectations.fetch(normalize_field(field))
    end

    # Convert a form value into the exact semantic value owned by TaskPatch.
    def semantic_value(field, value = form.value(field))
      case normalize_field(field)
      when :title then value.to_s.strip
      when :recurrence
        raw = value.to_s.strip
        raw.empty? ? nil : Tasks::Recur.parse_interval(raw)
      when :contexts then normalize_tokens(value, context: true)
      when :tags then normalize_tokens(value, context: false)
      else value
      end
    end

    # Adopt a fresh Store snapshot without replacing any dirty buffer or its
    # original semantic expectation. This is what makes later blur detect a
    # same-field external edit instead of silently overwriting it.
    def refresh_snapshot(fresh_snapshot)
      raise ArgumentError, "snapshot target changed" unless fresh_snapshot&.id == target_id

      dirty = FIELD_ORDER.select { |field| form.dirty?(field) }
      @snapshot = fresh_snapshot
      form.refresh(snapshot_values(fresh_snapshot))
      (FIELD_ORDER - dirty).each do |field|
        @expectations[field] = fresh_snapshot.expected_for(field)
      end
      form.render_model
    end

    def accept_commit(fresh_snapshot, token: nil)
      raise ArgumentError, "snapshot target changed" unless fresh_snapshot&.id == target_id

      request = form.pending_commit
      dirty_others = FIELD_ORDER.select do |field|
        field != request&.field_key && form.dirty?(field)
      end
      @snapshot = fresh_snapshot
      transition = form.accept_commit(snapshot_values(fresh_snapshot), token: token)
      (FIELD_ORDER - dirty_others).each do |field|
        @expectations[field] = fresh_snapshot.expected_for(field)
      end
      transition
    end

    def reject_commit(errors: nil, message: nil, token: nil)
      form.reject_commit(errors: errors, message: message, token: token)
    end

    # Explicitly discard one local buffer while adopting a fresh baseline.
    def reload_field(field, fresh_snapshot = snapshot)
      key = normalize_field(field)
      raise ArgumentError, "snapshot target changed" unless fresh_snapshot&.id == target_id

      @snapshot = fresh_snapshot
      fresh = snapshot_values(fresh_snapshot)
      form.set_value(key, fresh.fetch(key))
      form.refresh(fresh)
      @expectations[key] = fresh_snapshot.expected_for(key)
      form.render_model
    end

    def revert_field(field)
      key = normalize_field(field)
      form.set_value(key, form.baseline(key))
      form.refresh({ key => form.baseline(key) })
      @expectations[key] = snapshot.expected_for(key)
      form.render_model
    end

    private

    def build_groups
      fields = build_fields.to_h { |field| [field.key, field] }
      GROUPS.map do |key, members|
        TermForm::Group.new(
          key: key,
          label: key.to_s.capitalize,
          fields: members.map { |member| fields.fetch(member) },
        )
      end
    end

    def build_fields
      values = snapshot_values(snapshot)
      [
        TermForm::Fields::Input.new(
          key: :title, label: "Title", value: values[:title], required: true,
          validate: ->(value, _context) { "Title is required" if value.to_s.strip.empty? },
        ),
        TermForm::Fields::Select.new(
          key: :priority, label: "Priority", value: values[:priority],
          options: PRIORITY_OPTIONS, searchable: false,
        ),
        TermForm::Fields::Confirm.new(
          key: :deferred, label: "Deferred", value: values[:deferred],
        ),
        date_field(:scheduled, "Scheduled", values[:scheduled]),
        date_field(:deadline, "Deadline", values[:deadline]),
        TermForm::Fields::Input.new(
          key: :recurrence, label: "Recurrence", value: values[:recurrence],
          metadata: { presets: RECURRENCE_PRESETS },
          validate: method(:validate_recurrence),
        ),
        TermForm::Fields::MultiSelect.new(
          key: :contexts, label: "Contexts", value: values[:contexts],
          options: method(:context_options), creatable: true,
          token_normalizer: method(:normalize_context),
        ),
        TermForm::Fields::MultiSelect.new(
          key: :tags, label: "Tags", value: values[:tags],
          options: method(:tag_options), creatable: true,
          token_normalizer: method(:normalize_tag),
        ),
        TermForm::Fields::TextArea.new(
          key: :body, label: "Notes", value: values[:body],
        ),
        TermForm::Fields::Select.new(
          key: :state, label: "State", value: values[:state],
          options: STATE_OPTIONS, searchable: false,
        ),
      ]
    end

    def date_field(key, label, value)
      TermForm::Fields::DateInput.new(
        key: key, label: label, value: value,
        parser: ->(text, today) { Tasks::Dates.parse_when(text, today: today) },
        formatter: ->(date) { date.iso8601 }, today: @today,
        suggestions: DATE_SUGGESTIONS,
      )
    end

    def validate_recurrence(value, context)
      raw = value.to_s.strip
      return nil if raw.empty?
      if context.dirty?(:recurrence) && !context[:scheduled].is_a?(Date) && !context[:deadline].is_a?(Date)
        return "Recurrence requires a scheduled date or deadline"
      end
      return "Recurrence is not valid" unless Tasks::Recur.parse_interval(raw)

      nil
    end

    def snapshot_values(value)
      {
        title: value.title,
        priority: value.priority,
        deferred: value.deferred,
        scheduled: value.scheduled,
        deadline: value.deadline,
        recurrence: value.recurrence.to_s,
        contexts: value.contexts,
        tags: value.tags,
        body: value.body,
        state: value.state,
      }
    end

    def context_options(context = nil)
      source = option_source(@context_source, context)
      source ||= store&.items&.flat_map(&:contexts)
      Array(source).map { |value| normalize_context(value) }.compact.uniq
    end

    def tag_options(context = nil)
      source = option_source(@tag_source, context)
      source ||= store&.items&.flat_map(&:tags)
      Array(source).map { |value| normalize_tag(value) }.compact.uniq
    end

    def option_source(source, context)
      return nil if source.nil?
      return source unless source.respond_to?(:call)

      source.arity.zero? ? source.call : source.call(context)
    end

    def normalize_tokens(values, context:)
      Array(values).filter_map do |value|
        context ? normalize_context(value) : normalize_tag(value)
      end.uniq
    end

    def normalize_context(value)
      token = value.to_s.strip
      return nil if token.empty?
      token.start_with?("@") ? token : "@#{token}"
    end

    def normalize_tag(value)
      token = value.to_s.strip
      return nil if token.empty? || token.start_with?("@") || token == Tasks::Store::DEFER_TAG
      token
    end

    def normalize_field(field)
      field = field.to_sym
      field == :notes ? :body : (field == :recur ? :recurrence : field)
    end
  end
end
