# frozen_string_literal: true

require_relative "term_form_model"
require_relative "term_form_event"
require_relative "term_form_text"
require "date"

module TermForm
  module Fields
    Option = Data.define(:value, :label, :metadata) do
      def initialize(value:, label: value.to_s, metadata: {})
        super(value: Support.frozen_copy(value), label: label.to_s.dup.freeze,
              metadata: Support.frozen_copy(metadata))
      end
    end

    View = Data.define(
      :lines,
      :cursor_row,
      :cursor_column,
      :virtual_cursor_row,
      :virtual_cursor_column,
      :row_offset,
      :column_offset,
      :width,
      :height,
    ) do
      def cursor = [cursor_row, cursor_column].freeze
      def virtual_cursor = [virtual_cursor_row, virtual_cursor_column].freeze
    end

    State = Struct.new(:editor, :row_offset, :column_offset, :width, :height, :positions)

    class TextField < Field
      def initialize(key:, value:, metadata:, kind:, multiline:, **options)
        @multiline = multiline
        @state = State.new(TextEditor.new(value, multiline: multiline, kill_to_end: false), 0, 0, 1, 1, nil)
        if options.key?(:baseline)
          baseline = TextEditor.new(options.fetch(:baseline), multiline: multiline, kill_to_end: false).text
          options = options.merge(baseline: baseline)
        end
        super(key: key, value: @state.editor.text,
              metadata: { kind: kind }.merge(metadata), **options)
      end

      def text = @state.editor.text
      def cursor = @state.editor.cursor
      def handle_key(key) = @state.editor.handle_key(key)
      def paste(text) = @state.editor.insert(text)

      def normalize_value(value)
        TextEditor.new(value, multiline: @multiline, kill_to_end: false).text
      end

      def sync_value(value)
        normalized = normalize_value(value)
        @state.editor.replace(normalized) unless @state.editor.text == normalized
      end

      def handle_event(event, _value, _context)
        result = case event.type
                 when :paste then paste(event.text)
                 when :input then handle_key(event.text || event.raw)
                 when :key then handle_key(key_bytes(event))
                 end
        edit_result(result)
      end

      def cursor_for(_value, _context) = cursor

      private

      def edit_result(status)
        return nil unless status

        Field::Result.new(status, text)
      end

      def key_bytes(event)
        key = event.key || event.raw
        key.is_a?(Symbol) ? Event::KEY_BYTES.fetch(key) : key
      end
    end

    class Input < TextField
      def initialize(key:, value: "", metadata: {}, **options)
        super(key: key, value: value, metadata: metadata, kind: :input, multiline: false, **options)
      end

      def render(width:, height: 1)
        width = [Integer(width), 1].max
        Integer(height)
        cursor_cell = Text.graphemes(text).first(cursor).sum { |grapheme| Text.cluster_width(grapheme) }
        offset = @state.column_offset
        offset = cursor_cell if cursor_cell < offset
        offset = cursor_cell - width + 1 if cursor_cell >= offset + width
        offset = [offset, 0].max
        max_offset = [Text.cell_width(text) - width + 1, 0].max
        offset = [offset, max_offset].min

        @state.column_offset = offset
        @state.width = width
        @state.height = 1
        line = Text.cell_slice(text, offset, width).freeze
        View.new(
          lines: [line].freeze,
          cursor_row: 0,
          cursor_column: cursor_cell - offset,
          virtual_cursor_row: 0,
          virtual_cursor_column: cursor_cell,
          row_offset: 0,
          column_offset: offset,
          width: width,
          height: 1,
        )
      end
      alias layout render
      alias view render
    end

    class TextArea < TextField
      def initialize(key:, value: "", metadata: {}, **options)
        super(key: key, value: value, metadata: metadata, kind: :text_area, multiline: true, **options)
      end

      def handle_event(event, value, context)
        if event.type == :key
          vertical = case key_bytes(event)
                     when "\e[A" then move_vertical(-1)
                     when "\e[B" then move_vertical(1)
                     end
          return edit_result(vertical) if vertical
        end

        inherited = super
        return inherited if inherited

        result = case event.type
                 when :commit
                   handle_key(event.raw) if ["\r", "\n"].include?(event.raw)
                 when :next
                   move_vertical(1) if event.raw == "\e[B"
                 when :previous
                   move_vertical(-1) if event.raw == "\e[A"
                 end
        edit_result(result)
      end

      def render(width:, height:)
        width = [Integer(width), 1].max
        height = [Integer(height), 1].max
        lines, positions = wrapped_layout(width)
        virtual_row, virtual_column = positions.fetch(cursor)
        max_offset = [lines.length - height, 0].max
        offset = [@state.row_offset, max_offset].min
        offset = virtual_row if virtual_row < offset
        offset = virtual_row - height + 1 if virtual_row >= offset + height
        offset = [[offset, 0].max, max_offset].min

        @state.row_offset = offset
        @state.column_offset = 0
        @state.width = width
        @state.height = height
        @state.positions = positions
        visible = lines.slice(offset, height) || []
        visible += ["".freeze] * (height - visible.length)
        View.new(
          lines: visible.freeze,
          cursor_row: virtual_row - offset,
          cursor_column: virtual_column,
          virtual_cursor_row: virtual_row,
          virtual_cursor_column: virtual_column,
          row_offset: offset,
          column_offset: 0,
          width: width,
          height: height,
        )
      end
      alias layout render
      alias view render

      private

      def wrapped_layout(width)
        lines = [+""]
        positions = []
        row = 0
        column = 0

        Text.graphemes(text).each do |grapheme|
          if grapheme == "\n"
            if column == width
              lines << +""
              row += 1
              column = 0
              positions << [row, column].freeze
            else
              positions << [row, column].freeze
              lines << +""
              row += 1
              column = 0
            end
            next
          end

          cell_width = Text.cluster_width(grapheme)
          if column == width || (column.positive? && column + cell_width > width)
            lines << +""
            row += 1
            column = 0
          end
          positions << [row, column].freeze
          if cell_width > width
            lines[row] << " " * width
            column += width
          else
            lines[row] << grapheme
            column += cell_width
          end
        end
        if column == width
          lines << +""
          row += 1
          column = 0
        end
        positions << [row, column].freeze
        [lines.map!(&:freeze).freeze, positions.freeze]
      end

      def move_vertical(offset)
        _lines, positions = wrapped_layout(@state.width || 1)
        row, column = positions.fetch(cursor)
        target_row = row + offset
        candidates = positions.each_index.select { |index| positions[index][0] == target_row }
        return :handled if candidates.empty?

        @state.editor.cursor = candidates.min_by { |index| [(positions[index][1] - column).abs, index] }
        :handled
      end
    end


    # Shared searchable option behavior. Options may be scalars, [value, label]
    # pairs, Option values, or hashes with value/label/metadata keys. The option
    # source may be a callable over the current read-only form context.
    ChoiceState = Struct.new(:editor, :highlight_index, :open)

    class ChoiceField < Field
      def initialize(key:, value:, options:, searchable:, metadata:, kind:, **field_options)
        @option_source = options
        @searchable = searchable
        @choice_state = ChoiceState.new(TextEditor.new("", multiline: false, kill_to_end: false), 0, false)
        if field_options.key?(:baseline)
          field_options = field_options.merge(baseline: normalize_value(field_options.fetch(:baseline)))
        end
        super(key: key, value: normalize_value(value),
              metadata: { kind: kind }.merge(metadata), **field_options)
      end

      def query = @choice_state.editor.text
      def highlight_index = @choice_state.highlight_index
      def open? = @choice_state.open

      def options(context)
        source = Support.property(@option_source, context)
        entries = if source.is_a?(Hash) && !(source.key?(:value) || source.key?("value"))
                    source.map { |value, label| [value, label] }
                  else
                    Array(source)
                  end
        entries.map { |entry| normalize_option(entry) }.freeze
      end
      alias available_options options

      def filtered_options(context)
        choices = options(context)
        return choices if query.empty?

        needle = query.downcase
        choices.select do |option|
          option.label.downcase.include?(needle) || option.value.to_s.downcase.include?(needle)
        end.freeze
      end

      def highlighted_option(context)
        choices = filtered_options(context)
        choices[[highlight_index, choices.length - 1].min] unless choices.empty?
      end

      def validation_errors(value, context)
        (super + availability_errors(value, context)).freeze
      end

      def metadata_for(value, context)
        choices = filtered_options(context)
        selected = selected_values(value)
        metadata.merge(
          open: open?, query: query, searchable: @searchable,
          invalid_selection: !availability_errors(value, context).empty?,
          options: choices.each_with_index.map do |option, index|
            {
              value: option.value, label: option.label, metadata: option.metadata,
              selected: selected.include?(option.value), highlighted: index == highlight_index,
            }.freeze
          end.freeze,
        )
      end

      def cursor_for(_value, _context) = @searchable ? @choice_state.editor.cursor : nil

      private

      def normalize_option(entry)
        case entry
        when Option then entry
        when Hash
          value = entry.key?(:value) ? entry[:value] : entry.fetch("value")
          label = entry[:label] || entry["label"] || value.to_s
          metadata = entry[:metadata] || entry["metadata"] || {}
          Option.new(value: value, label: label, metadata: metadata)
        when Array
          Option.new(value: entry.fetch(0), label: entry.fetch(1, entry.fetch(0).to_s),
                     metadata: entry.fetch(2, {}))
        else
          Option.new(value: entry)
        end
      end

      def selected_values(value) = [value]

      def availability_errors(value, context)
        return [] if options(context).any? { |option| option.value == value }

        ["selection is no longer available"]
      end

      def move_highlight(offset, context)
        choices = filtered_options(context)
        @choice_state.open = true
        @choice_state.highlight_index = choices.empty? ? 0 : (highlight_index + offset) % choices.length
        Field::Result.new(:handled, nil)
      end

      def update_query(event, context)
        return unless @searchable

        status = case event.type
                 when :paste then @choice_state.editor.insert(event.text)
                 when :input then @choice_state.editor.handle_key(event.text || event.raw)
                 when :key then @choice_state.editor.handle_key(event.raw)
                 end
        return unless status

        @choice_state.open = true
        @choice_state.highlight_index = 0
        Field::Result.new(:handled, nil)
      end

      def clear_query
        @choice_state.editor.clear
        @choice_state.highlight_index = 0
      end

      def arrow_offset(event)
        case event.raw
        when "\e[A" then -1
        when "\e[B" then 1
        end
      end
    end

    class Select < ChoiceField
      def initialize(key:, options:, value: nil, searchable: true, metadata: {}, **field_options)
        super(key: key, value: value, options: options, searchable: searchable,
              metadata: metadata, kind: :select, **field_options)
      end

      def handle_event(event, value, context)
        return move_highlight(arrow_offset(event), context) if arrow_offset(event)

        if event.type == :cancel && open?
          @choice_state.open = false
          clear_query
          return Field::Result.new(:handled, value)
        end

        if event.type == :commit && ["\r", "\n"].include?(event.raw)
          unless open?
            @choice_state.open = true
            current = filtered_options(context).index { |option| option.value == value }
            @choice_state.highlight_index = current || 0
            return Field::Result.new(:handled, value)
          end

          chosen = highlighted_option(context)
          @choice_state.open = false
          clear_query
          return Field::Result.new(chosen && chosen.value != value ? :changed : :handled,
                                   chosen ? chosen.value : value)
        end

        update_query(event, context)
      end
    end

    class MultiSelect < ChoiceField
      attr_reader :creatable

      def initialize(key:, options:, value: [], searchable: true, creatable: false,
                     normalize: nil, token_normalizer: nil, metadata: {}, **field_options)
        @creatable = creatable
        @token_normalizer = token_normalizer || normalize
        super(key: key, value: value, options: options, searchable: searchable,
              metadata: metadata, kind: :multi_select, **field_options)
      end

      def normalize_value(value)
        Array(value).each_with_object([]) do |token, result|
          normalized = normalize_token(token)
          next if normalized.nil? || (normalized.respond_to?(:empty?) && normalized.empty?)
          result << normalized unless result.include?(normalized)
        end
      end

      def handle_event(event, value, context)
        current = normalize_value(value)
        return move_highlight(arrow_offset(event), context) if arrow_offset(event)

        if event.type == :cancel && open?
          @choice_state.open = false
          clear_query
          return Field::Result.new(:handled, current)
        end

        if backspace?(event) && query.empty? && !current.empty?
          return Field::Result.new(:changed, current[0...-1])
        end

        if event.type == :commit && ["\r", "\n"].include?(event.raw)
          chosen = highlighted_option(context)
          token = if !query.empty? && @creatable
                    exact = filtered_options(context).find do |option|
                      option.label.casecmp?(query) || option.value.to_s.casecmp?(query)
                    end
                    exact ? exact.value : query
                  else
                    chosen&.value
                  end
          token = normalize_token(token) unless token.nil?
          @choice_state.open = false
          clear_query
          return Field::Result.new(:handled, current) if token.nil? || current.include?(token)

          return Field::Result.new(:changed, current + [token])
        end

        update_query(event, context)
      end

      def metadata_for(value, context)
        super.merge(tokens: normalize_value(value).freeze, creatable: @creatable)
      end

      private

      def selected_values(value) = normalize_value(value)

      def availability_errors(value, context)
        return [] if @creatable

        available = options(context).map(&:value)
        missing = normalize_value(value).reject { |token| available.include?(token) }
        missing.empty? ? [] : ["selection is no longer available: #{missing.join(", ")}"]
      end

      def normalize_token(token)
        return token unless @token_normalizer

        @token_normalizer.arity.zero? ? @token_normalizer.call : @token_normalizer.call(token)
      end

      def backspace?(event)
        event.raw == "\x7f" || event.raw == "\b"
      end
    end

    class Confirm < Field
      def initialize(key:, value: false, yes_label: "Yes", no_label: "No",
                     consequence: nil, metadata: {}, **field_options)
        @yes_label = yes_label.to_s.dup.freeze
        @no_label = no_label.to_s.dup.freeze
        @consequence = consequence
        if field_options.key?(:baseline)
          field_options = field_options.merge(baseline: normalize_value(field_options.fetch(:baseline)))
        end
        super(key: key, value: normalize_value(value),
              metadata: { kind: :confirm }.merge(metadata), **field_options)
      end

      def normalize_value(value) = !!value

      def handle_event(event, value, _context)
        next_value = case event.raw
                     when "y", "Y", "\e[C", "\e[B" then true
                     when "n", "N", "\e[D", "\e[A" then false
                     when " ", "\r", "\n" then !value
                     end
        return unless [true, false].include?(next_value)

        Field::Result.new(next_value == value ? :handled : :changed, next_value)
      end

      def metadata_for(value, context)
        metadata.merge(
          options: [
            { value: false, label: @no_label, selected: !value }.freeze,
            { value: true, label: @yes_label, selected: !!value }.freeze,
          ].freeze,
          consequence: Support.frozen_copy(Support.property(@consequence, context)),
        )
      end
    end

    DateState = Struct.new(:editor, :picker_open, :anchor, :column_offset)

    class DateInput < Field
      attr_reader :state

      def initialize(key:, value: nil, parser: nil, formatter: nil, today: -> { Date.today },
                     suggestions: [], default_anchor: nil, metadata: {}, **field_options)
        @parser = parser || ->(text, _today) { Date.iso8601(text) }
        @formatter = formatter || ->(date) { date.iso8601 }
        @today_source = today
        @suggestion_source = suggestions
        @default_anchor = default_anchor
        normalized = normalize_value(value)
        text = normalized.is_a?(Date) ? format_date(normalized) : value.to_s
        @state = DateState.new(TextEditor.new(text, multiline: false, kill_to_end: false), false, nil, 0)
        if field_options.key?(:baseline)
          field_options = field_options.merge(baseline: normalize_value(field_options.fetch(:baseline)))
        end
        super(key: key, value: normalized, metadata: { kind: :date_input }.merge(metadata), **field_options)
      end

      def text = @state.editor.text
      def cursor = @state.editor.cursor
      def picker_open? = @state.picker_open
      alias open? picker_open?
      def picker_date = @state.anchor

      def normalize_value(value)
        return nil if value.nil? || (value.respond_to?(:strip) && value.strip.empty?)
        return value if value.is_a?(Date)

        parsed = parse_text(value.to_s)
        parsed || value.to_s
      end

      def sync_value(value)
        normalized = normalize_value(value)
        return if normalize_value(text) == normalized

        @state.editor.replace(normalized.is_a?(Date) ? format_date(normalized) : normalized.to_s)
      end

      def handle_event(event, value, context)
        return handle_picker_event(event, value) if picker_open?

        if event.type == :commit && ["\r", "\n"].include?(event.raw)
          @state.picker_open = true
          @state.anchor = anchor_for(value, context)
          return Field::Result.new(:handled, value)
        end

        status = case event.type
                 when :paste then @state.editor.insert(event.text)
                 when :input then @state.editor.handle_key(event.text || event.raw)
                 when :key then @state.editor.handle_key(event.raw)
                 end
        return unless status

        Field::Result.new(:changed, normalize_value(text))
      end

      def validation_errors(value, context)
        errors = super
        if !value.nil? && !value.is_a?(Date)
          errors = errors + ["is not a valid date"]
        end
        errors.freeze
      end

      def preview(value = normalize_value(text))
        value.is_a?(Date) ? format_date(value) : nil
      end

      def cursor_for(_value, _context) = picker_open? ? nil : cursor

      def metadata_for(value, context)
        date = value.is_a?(Date) ? value : parse_text(text)
        metadata.merge(
          text: text, preview: date && format_date(date), picker_open: picker_open?,
          suggestions: Array(Support.property(@suggestion_source, context)).map(&:to_s).freeze,
          picker: picker_open? ? calendar_metadata(@state.anchor) : nil,
        )
      end

      def render(width:, height: nil)
        width = [Integer(width), 1].max
        lines = if picker_open?
                  calendar_lines(@state.anchor, width)
                else
                  rendered = Text.cell_slice(text, text_offset(width), width)
                  [rendered]
                end
        if height
          height = [Integer(height), 1].max
          lines = lines.first(height)
        else
          height = lines.length
        end
        lines += [""] * (height - lines.length)
        offset = picker_open? ? 0 : @state.column_offset
        cursor_column = picker_open? ? 0 : text_cursor_cell - offset
        View.new(lines: lines.map(&:freeze).freeze, cursor_row: 0, cursor_column: cursor_column,
                 virtual_cursor_row: 0, virtual_cursor_column: text_cursor_cell,
                 row_offset: 0, column_offset: offset, width: width, height: height)
      end
      alias layout render
      alias view render

      private

      def parse_text(raw)
        text = raw.to_s.strip
        return nil if text.empty?

        result = call_with_today(@parser, text)
        result if result.is_a?(Date)
      rescue ArgumentError, Date::Error
        nil
      end

      def format_date(date)
        result = call_with_today(@formatter, date)
        result.to_s
      end

      def today
        value = @today_source.respond_to?(:call) ? @today_source.call : @today_source
        raise ArgumentError, "today must return a Date" unless value.is_a?(Date)

        value
      end

      def call_with_today(callable, value)
        parameters = callable.parameters
        if parameters.any? { |kind, name| %i[key keyreq].include?(kind) && name == :today }
          callable.call(value, today: today)
        elsif callable.arity == 1
          callable.call(value)
        else
          callable.call(value, today)
        end
      end

      def anchor_for(value, context)
        candidates = [value, parse_text(text), Support.property(@default_anchor, context), today]
        candidates.find { |candidate| candidate.is_a?(Date) }
      end

      def handle_picker_event(event, value)
        case event.raw
        when "\e"
          @state.picker_open = false
          return Field::Result.new(:handled, value)
        when "\e[D" then @state.anchor -= 1
        when "\e[C" then @state.anchor += 1
        when "\e[A" then @state.anchor -= 7
        when "\e[B" then @state.anchor += 7
        when "\e[5~" then @state.anchor = shift_month(@state.anchor, -1)
        when "\e[6~" then @state.anchor = shift_month(@state.anchor, 1)
        when "t", "T" then @state.anchor = today
        when "\r", "\n"
          selected = @state.anchor
          @state.editor.replace(format_date(selected))
          @state.picker_open = false
          return Field::Result.new(selected == value ? :handled : :changed, selected)
        else
          return nil
        end
        Field::Result.new(:handled, value)
      end

      def shift_month(date, offset)
        month_index = date.year * 12 + date.month - 1 + offset
        year, zero_month = month_index.divmod(12)
        month = zero_month + 1
        last_day = Date.new(year, month, -1).day
        Date.new(year, month, [date.day, last_day].min)
      end

      def calendar_metadata(date)
        first = Date.new(date.year, date.month, 1)
        start = first - (first.cwday - 1)
        {
          month: first, selected: date, weekday_labels: %w[Mo Tu We Th Fr Sa Su].freeze,
          weeks: 6.times.map { |week| 7.times.map { |day| start + week * 7 + day }.freeze }.freeze,
        }.freeze
      end

      def calendar_lines(date, width)
        if width < 20
          compact = [date.strftime("%Y-%m"), format_date(date), "arrows move; enter picks"]
          return compact.map { |line| Text.cell_slice(line, 0, width) }
        end

        calendar = calendar_metadata(date)
        lines = [date.strftime("%B %Y"), calendar[:weekday_labels].join(" ")]
        lines.concat(calendar[:weeks].map do |week|
          week.map { |day| day.month == date.month ? format("%2d", day.day) : "  " }.join(" ")
        end)
        lines.map { |line| Text.cell_slice(line, 0, width) }
      end

      def text_cursor_cell
        Text.graphemes(text).first(cursor).sum { |grapheme| Text.cluster_width(grapheme) }
      end

      def text_offset(width)
        cursor_cell = text_cursor_cell
        offset = @state.column_offset
        offset = cursor_cell if cursor_cell < offset
        offset = cursor_cell - width + 1 if cursor_cell >= offset + width
        max_offset = [Text.cell_width(text) - width + 1, 0].max
        @state.column_offset = [[offset, 0].max, max_offset].min
      end
    end
  end
end
