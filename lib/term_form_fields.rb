# frozen_string_literal: true

require_relative "term_form_model"
require_relative "term_form_text"

module TermForm
  module Fields
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
                 when :input, :key then handle_key(event.raw || event.key || event.text)
                 end
        edit_result(result)
      end

      def cursor_for(_value, _context) = cursor

      private

      def edit_result(status)
        return nil unless status

        Field::Result.new(status, text)
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
  end
end
