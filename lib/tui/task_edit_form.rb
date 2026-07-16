# frozen_string_literal: true

require_relative "../term_form"
require_relative "../tasks/dates"
require_relative "../tasks/recur"
require_relative "../tasks/temporal_context"
require_relative "../tasks/temporal_parser"

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
    DATE_SUGGESTIONS = ["today", "tomorrow 9am", "fri noon", "+3 17:30"].freeze

    attr_reader :form, :snapshot, :store, :today

    def self.parse_temporal(text, today, context: nil)
      tokens = text.to_s.strip.split
      fold_token = tokens.last&.match?(/\Afold=(?:earlier|later)\z/) ? tokens.pop : nil
      mode = if tokens.last == "floating" || tokens.last&.include?("/") || tokens.last == "UTC"
               tokens.pop
             end
      fold = fold_token == "fold=later" ? 1 : 0
      timezone = mode unless mode.nil? || mode == "floating"
      Tasks::TemporalParser.parse(tokens.join(" "), today: today, timezone: timezone,
                                  floating: mode == "floating", fold: fold, context: context)
    end

    def self.format_temporal(value)
      parts = [value.date.iso8601]
      if value.local_time
        parts << value.local_time
        parts << (value.timezone || "floating")
        parts << "fold=later" if value.fold == 1
      end
      parts.join(" ")
    end

    def initialize(snapshot:, store: nil, today: -> { Date.today },
                   temporal_context: nil, context_options: nil, tag_options: nil, focus: nil)
      raise ArgumentError, "snapshot must have a stable id" if snapshot.nil? || snapshot.id.to_s.empty?

      @snapshot = snapshot
      @store = store
      @today = today.respond_to?(:call) ? today : -> { today }
      @temporal_context = temporal_context
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
          key: :deferred, label: "On hold", value: values[:deferred],
        ),
        temporal_field(:scheduled, "Available from", values[:scheduled]),
        temporal_field(:deadline, "Deadline", values[:deadline]),
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

    class TemporalInput < TermForm::Fields::DateInput
      PICKER_ROWS = %i[date time mode zone fold].freeze
      TemporalPickerState = Struct.new(:open, :row, :calendar, :zone_search, :zone_index, :value, :error)

      def initialize(temporal_context: nil, **options)
        @temporal_context = temporal_context
        @temporal_state = TemporalPickerState.new(false, 0, nil, nil, 0, nil, nil)
        super(**options)
      end

      def picker_open? = @temporal_state.open || super

      def handle_event(event, value, context)
        return handle_temporal_picker(event, value) if @temporal_state.open

        if TermForm::Fields::DecodedEvent.command?(event, :commit, "\r", "\n")
          @temporal_state.open = true
          @temporal_state.value = temporal_value(value)
          @temporal_state.row = 0
          @temporal_state.error = nil
          return TermForm::Field::Result.new(:handled, value)
        end

        super
      end

      def cursor_for(value, context) = @temporal_state.open ? nil : super

      def metadata_for(value, context)
        return super unless @temporal_state.open

        metadata.merge(
          text: text,
          preview: @temporal_state.value && format_date(@temporal_state.value),
          picker_open: true,
          suggestions: Array(TermForm::Support.property(@suggestion_source, context)).map { |entry|
            entry.to_s.dup.freeze
          }.freeze,
          picker: {
            kind: :temporal,
            row: visible_rows(@temporal_state.value).fetch(@temporal_state.row),
            mode: temporal_mode(@temporal_state.value),
            zone_search: @temporal_state.zone_search,
            lines: temporal_picker_lines(@temporal_state.value, 200),
          }.freeze,
        ).freeze
      end

      def render(width:, height: nil)
        return super unless @temporal_state.open

        width = [Integer(width), 1].max
        lines = temporal_picker_lines(@temporal_state.value, width)
        if height
          height = [Integer(height), 1].max
          lines = lines.first(height)
        else
          height = lines.length
        end
        lines += [""] * (height - lines.length)
        TermForm::Fields::View.new(
          lines: lines.map(&:freeze).freeze, cursor_row: 0, cursor_column: 0,
          virtual_cursor_row: 0, virtual_cursor_column: 0,
          row_offset: 0, column_offset: 0, width: width, height: height
        )
      end

      private

      def parsed_value?(value) = value.is_a?(Tasks::TemporalValue)
      def date_for_value(value) = value.is_a?(Tasks::TemporalValue) ? value.date : super
      def value_for_date(date, current)
        current.is_a?(Tasks::TemporalValue) ? current.with_date(date) : Tasks::TemporalValue.new(date: date)
      end

      def handle_temporal_picker(event, value)
        key = temporal_picker_key(event)
        return handle_zone_search(event, value) unless @temporal_state.zone_search.nil?
        return handle_temporal_calendar(key, value) if @temporal_state.calendar

        if TermForm::Fields::DecodedEvent.command?(event, :cancel, "\e")
          @temporal_state.open = false
          return TermForm::Field::Result.new(:handled, value)
        end

        rows = visible_rows(@temporal_state.value)
        case key
        when "\e[A"
          @temporal_state.row = (@temporal_state.row - 1) % rows.length
        when "\e[B"
          @temporal_state.row = (@temporal_state.row + 1) % rows.length
        when "\e[D"
          return adjust_temporal(rows.fetch(@temporal_state.row), -1, value)
        when "\e[C"
          return adjust_temporal(rows.fetch(@temporal_state.row), 1, value)
        when "\r", "\n"
          return activate_temporal_row(rows.fetch(@temporal_state.row), value)
        else
          return nil
        end
        TermForm::Field::Result.new(:handled, value)
      end

      def handle_temporal_calendar(key, value)
        case key
        when "\e"
          @temporal_state.calendar = nil
          return TermForm::Field::Result.new(:handled, value)
        when "\r", "\n"
          updated = rebuild(@temporal_state.value, date: @temporal_state.calendar)
          @temporal_state.calendar = nil
          return set_picker_value(updated, value)
        when "\e[D" then @temporal_state.calendar -= 1
        when "\e[C" then @temporal_state.calendar += 1
        when "\e[A" then @temporal_state.calendar -= 7
        when "\e[B" then @temporal_state.calendar += 7
        when "\e[5~" then @temporal_state.calendar = shift_month(@temporal_state.calendar, -1)
        when "\e[6~" then @temporal_state.calendar = shift_month(@temporal_state.calendar, 1)
        when "t", "T" then @temporal_state.calendar = today
        else return nil
        end
        TermForm::Field::Result.new(:handled, value)
      rescue Tasks::Timezones::Error, ArgumentError => error
        @temporal_state.error = error.message
        TermForm::Field::Result.new(:handled, value)
      end

      def handle_zone_search(event, value)
        key = temporal_picker_key(event)
        matches = zone_matches
        case key
        when "\e"
          @temporal_state.zone_search = nil
          @temporal_state.zone_index = 0
        when "\e[A"
          @temporal_state.zone_index = [@temporal_state.zone_index - 1, 0].max
        when "\e[B"
          @temporal_state.zone_index = [@temporal_state.zone_index + 1, [matches.length - 1, 0].max].min
        when "\r", "\n"
          return TermForm::Field::Result.new(:handled, value) if matches.empty?
          updated = rebuild(@temporal_state.value, timezone: matches.fetch(@temporal_state.zone_index))
          @temporal_state.zone_search = nil
          @temporal_state.zone_index = 0
          return set_picker_value(updated, value)
        when "\x7f", "\b"
          @temporal_state.zone_search = @temporal_state.zone_search[0...-1]
          @temporal_state.zone_index = 0
        else
          text = event.type == :paste ? event.text : key
          return nil unless text&.match?(/\A[[:alnum:]_+\-\/.]+\z/)
          @temporal_state.zone_search << text
          @temporal_state.zone_index = 0
        end
        TermForm::Field::Result.new(:handled, value)
      rescue Tasks::Timezones::Error, ArgumentError => error
        @temporal_state.error = error.message
        TermForm::Field::Result.new(:handled, value)
      end

      def activate_temporal_row(row, value)
        case row
        when :date
          @temporal_state.calendar = @temporal_state.value.date
          TermForm::Field::Result.new(:handled, value)
        when :zone
          if temporal_mode(@temporal_state.value) == :fixed
            @temporal_state.zone_search = +""
            @temporal_state.zone_index = 0
          end
          TermForm::Field::Result.new(:handled, value)
        else
          adjust_temporal(row, 1, value)
        end
      end

      def adjust_temporal(row, direction, value)
        updated = case row
                  when :date then rebuild(@temporal_state.value, date: @temporal_state.value.date + direction)
                  when :time then adjust_time(@temporal_state.value, direction)
                  when :mode then adjust_mode(@temporal_state.value, direction)
                  when :zone
                    return activate_temporal_row(:zone, value)
                  when :fold then rebuild(@temporal_state.value, fold: @temporal_state.value.fold == 1 ? 0 : 1)
                  end
        set_picker_value(updated, value)
      rescue Tasks::Timezones::Error, ArgumentError => error
        @temporal_state.error = error.message
        TermForm::Field::Result.new(:handled, value)
      end

      def adjust_time(current, direction)
        return Tasks::TemporalValue.new(date: current.date, local_time: "09:00") if current.all_day?

        hour, minute = current.local_time.split(":").map(&:to_i)
        total = (hour * 60 + minute + direction * 15) % 1_440
        rebuild(current, local_time: format("%02d:%02d", total / 60, total % 60))
      end

      def adjust_mode(current, direction)
        modes = %i[all_day floating fixed]
        mode = modes[(modes.index(temporal_mode(current)) + direction) % modes.length]
        case mode
        when :all_day then Tasks::TemporalValue.new(date: current.date)
        when :floating
          Tasks::TemporalValue.new(date: current.date, local_time: current.local_time || "09:00")
        when :fixed
          zone = current.timezone || @temporal_context&.timezone_id || "Etc/UTC"
          Tasks::TemporalValue.new(date: current.date, local_time: current.local_time || "09:00", timezone: zone)
        end
      end

      def rebuild(current, date: current.date, local_time: current.local_time,
                  timezone: current.timezone, fold: current.fold)
        rebuilt = Tasks::TemporalValue.new(
          date: date, local_time: local_time, timezone: timezone, fold: fold
        )
        if rebuilt.local_time && !ambiguous?(rebuilt) && rebuilt.fold == 1
          rebuilt = Tasks::TemporalValue.new(
            date: rebuilt.date, local_time: rebuilt.local_time, timezone: rebuilt.timezone, fold: 0
          )
        end
        rebuilt.instant(@temporal_context) if rebuilt.floating? && @temporal_context
        rebuilt
      end

      def set_picker_value(updated, old_value)
        @temporal_state.value = updated
        @temporal_state.error = nil
        @state.editor.replace(format_date(updated))
        @temporal_state.row = [@temporal_state.row, visible_rows(updated).length - 1].min
        TermForm::Field::Result.new(updated == old_value ? :handled : :changed, updated)
      end

      def temporal_value(value)
        candidate = value if value.is_a?(Tasks::TemporalValue)
        candidate ||= parse_text(text)
        candidate || Tasks::TemporalValue.new(date: today)
      end

      def temporal_mode(value)
        return :all_day if value.all_day?
        value.fixed? ? :fixed : :floating
      end

      def visible_rows(value)
        rows = %i[date time mode]
        rows << :zone if temporal_mode(value) == :fixed
        rows << :fold if value.local_time && ambiguous?(value)
        rows.freeze
      end

      def ambiguous?(value)
        zone = value.timezone || @temporal_context&.timezone
        zone && Tasks::Timezones.ambiguous?(value.date, value.local_time, zone)
      end

      def zone_matches
        query = @temporal_state.zone_search.to_s.downcase
        TZInfo::Timezone.all_identifiers.select { |identifier| identifier.downcase.include?(query) }
      end

      def temporal_picker_lines(value, width)
        if @temporal_state.calendar
          return (["Date picker · arrows move · Enter selects · Esc returns"] +
                  calendar_lines(@temporal_state.calendar, width)).map { |line| TermForm::Text.cell_slice(line, 0, width) }
        end
        if !@temporal_state.zone_search.nil?
          matches = zone_matches
          lines = ["Zone search: #{@temporal_state.zone_search}", "type to filter · ↑/↓ choose · Enter selects · Esc returns"]
          lines.concat(matches.first(6).each_with_index.map do |zone, index|
            "#{index == @temporal_state.zone_index ? "›" : " "} #{zone}"
          end)
          lines << "  no matching IANA zones" if matches.empty?
          return lines.map { |line| TermForm::Text.cell_slice(line, 0, width) }
        end

        rows = visible_rows(value)
        lines = ["Temporal value · ↑/↓ field · ←/→ change · Enter opens · Esc closes"]
        rows.each_with_index do |row, index|
          marker = index == @temporal_state.row ? "›" : " "
          label = case row
                  when :date then "Date: #{value.date.iso8601}"
                  when :time then "Time: #{value.local_time || "All day"}"
                  when :mode then "Mode: #{temporal_mode(value).to_s.tr("_", " ").capitalize}"
                  when :zone then "Zone: #{value.timezone} (Enter to search)"
                  when :fold then "Fold: #{value.fold == 1 ? "Later" : "Earlier"}"
                  end
          lines << "#{marker} #{label}"
        end
        lines << "Error: #{@temporal_state.error}" if @temporal_state.error
        lines.map { |line| TermForm::Text.cell_slice(line, 0, width) }
      end

      def temporal_picker_key(event)
        return "\r" if TermForm::Fields::DecodedEvent.command?(event, :commit, "\r", "\n")
        return "\e" if TermForm::Fields::DecodedEvent.command?(event, :cancel, "\e")

        TermForm::Fields::DecodedEvent.key(event)
      end
    end

    def temporal_field(key, label, value)
      TemporalInput.new(
        key: key, label: label, value: value,
        parser: ->(text, today) { self.class.parse_temporal(text, today, context: @temporal_context) },
        formatter: self.class.method(:format_temporal), today: @today,
        temporal_context: @temporal_context,
        expose_parse_errors: true,
        suggestions: DATE_SUGGESTIONS,
      )
    end

    def validate_recurrence(value, context)
      raw = value.to_s.strip
      return nil if raw.empty?
      if context.dirty?(:recurrence) && !context[:scheduled].is_a?(Tasks::TemporalValue) &&
         !context[:deadline].is_a?(Tasks::TemporalValue)
        return "Recurrence requires an Available from date or deadline"
      end
      return "Recurrence is not valid" unless Tasks::Recur.parse_interval(raw)

      nil
    end

    def snapshot_values(value)
      {
        title: value.title,
        priority: value.priority,
        deferred: value.deferred,
        scheduled: value.scheduled_value,
        deadline: value.deadline_value,
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
