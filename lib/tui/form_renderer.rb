# frozen_string_literal: true

require_relative "ansi"
require_relative "theme"

module Tui
  # Pure ANSI adapter for TermForm's semantic render model. The caller supplies
  # the complete cell budget; this renderer never samples terminal geometry or
  # performs cursor-addressing IO.
  class FormRenderer
    A = Ansi
    T = Theme

    Result = Data.define(:lines, :focused_content_row)

    # Lines of the surrounding form kept visible above a focused field when the
    # content is scrolled, so bringing a field into view reads as scrolling
    # rather than jumping it to the very top or bottom edge.
    CONTEXT_ROWS = 2

    def render(model:, width:, height:, title:, hint: nil, error: nil, suffix: nil)
      width = [Integer(width), 0].max
      height = [Integer(height), 0].max
      return Result.new(lines: [].freeze, focused_content_row: model.focused_row_index) if width.zero? || height.zero?
      return compact(model, width, height, title, error) if width < 6 || height < 3

      inner_width = width - 2
      content = []
      focused_content_row = nil
      focused_field_row = nil
      model.groups.each do |group|
        label = inline_text(group.label).strip
        # Section headers render as a padded, background-colored chip (e.g.
        # " Basics ") so the grouped fields are easy to scan; :form_group_label
        # carries the fg/bg pair. Field labels keep their plain :form_label.
        content << T.paint(:form_group_label, " #{label} ") unless label.empty?
        group.rows.each do |row|
          field_lines, focus_offset = render_field_rows(
            row, width: inner_width, suffix: suffix,
            external_error: error && row.focused?,
          )
          if row.focused?
            focused_field_row = content.length
            focused_content_row = content.length + focus_offset
          end
          content.concat(field_lines)
          content.concat(render_picker(row, width: width - 2))
          content.concat(render_choices(row))
        end
      end

      message = error || model.errors[:base]&.first || model.rows.filter_map(&:error).first || hint
      content << T.paint(error || model.errors.any? ? :form_error : :form_hint, cue_message(message, error || model.errors.any?)) if message
      content = [T.paint(:form_hint, "(empty form)")] if content.empty?

      budget = height - 2
      offset = viewport_offset(content.length, budget, focused_field_row, focused_content_row)
      shown = (content[offset, budget] || []).map { |line| A.vpad(truncate(line, inner_width), inner_width) }
      shown += [" " * inner_width] * (budget - shown.length)

      title_text = truncate(" #{inline_text(title)} ", inner_width)
      lines = ["┌#{title_text}#{"─" * (inner_width - A.vislen(title_text))}┐"]
      lines.concat(shown.map { |line| "│#{line}│" })
      lines << "└#{"─" * inner_width}┘"
      visible_focus = focused_content_row && focused_content_row - offset
      Result.new(lines: lines.freeze, focused_content_row: visible_focus)
    end

    private

    def compact(model, width, height, title, error)
      row = model.focused_row || model.rows.first
      label = inline_text(row&.label).strip
      label = inline_text(title) if label.empty?
      compact_value = inline_text(value_text(row), separator: " ↵ ")
      plain = if error
                "#{row&.focused? ? "›" : ""}! #{label}: #{inline_text(error)}"
              elsif row&.metadata&.dig(:picker_open)
                "> #{picker_selected(row).iso8601}"
              elsif row && !compact_value.empty?
                "#{row.focused? ? "›" : " "}#{row.dirty? ? "*" : " "} #{compact_value}"
              else
                label
              end
      clipped = if row&.metadata&.dig(:picker_open)
                  A.cell_slice(plain, 0, width)
                elsif row && value_text(row).empty? && !error
                  A.cell_slice(plain, 0, width)
                else
                  truncate(plain, width)
                end
      line = A.vpad(clipped, width)
      Result.new(lines: [line].first(height).freeze, focused_content_row: row ? 0 : nil)
    end

    def render_field_rows(row, width:, suffix: nil, external_error: false)
      unless multiline_value?(row) || wrap_focused_input?(row)
        return [[render_row(row, suffix: suffix, external_error: external_error)], 0]
      end

      render_multiline_rows(row, width: width, suffix: suffix, external_error: external_error)
    end

    def render_row(row, suffix: nil, external_error: false)
      prefix = row_prefix(row, external_error: external_error)
      tail = suffix.to_s.empty? ? "" : "  #{T.paint(:form_hint, inline_text(suffix))}"
      "#{prefix}#{render_value(row)}#{tail}"
    end

    def row_prefix(row, external_error: false)
      focus = row.focused? ? T.paint(:form_focus, "›") : " "
      status = row_status(row, external_error: external_error)
      label = inline_text(row.label)
      label += "*" if row.required?
      label = T.paint(row.enabled? ? :form_label : :form_disabled, label)
      "#{focus}#{status} #{label}: "
    end

    def render_value(row)
      text = inline_text(value_text(row))
      return T.paint(:form_value, text) unless row.focused? && row.cursor

      clusters = text.each_grapheme_cluster.to_a
      cursor = row.cursor.clamp(0, clusters.length)
      before = clusters[0...cursor].join
      at = cursor < clusters.length ? clusters[cursor] : " "
      after = cursor < clusters.length ? clusters[(cursor + 1)..].join : ""
      T.paint(:form_value, before) + T.paint(:form_cursor, at) + T.paint(:form_value, after)
    end

    def render_multiline_rows(row, width:, suffix:, external_error:)
      prefix = row_prefix(row, external_error: external_error)
      continuation = if row.focused?
                       "#{T.paint(:form_focus, "│")}#{row_status(row, external_error: external_error)} "
                     else
                       "   "
                     end
      first_width = [width - A.vislen(prefix), 0].max
      continuation_width = [width - A.vislen(continuation), 1].max
      layout = multiline_layout(
        value_text(row), first_width: first_width,
        continuation_width: continuation_width, cursor: row.cursor,
      )
      cursor_row, cursor_column = layout.fetch(:cursor)
      lines = layout.fetch(:lines).map.with_index do |segment, index|
        value = if row.focused? && row.cursor && index == cursor_row
                  render_cursor_cell(segment, cursor_column)
                else
                  T.paint(:form_value, segment)
                end
        value = T.paint(:form_disabled, value) unless row.enabled?
        "#{index.zero? ? prefix : continuation}#{value}"
      end
      unless suffix.to_s.empty?
        lines[-1] += "  #{T.paint(:form_hint, inline_text(suffix))}"
      end
      [lines, cursor_row]
    end

    def multiline_layout(value, first_width:, continuation_width:, cursor:)
      text = multiline_text(value)
      units = text.each_grapheme_cluster.to_a
      cursor_visible = !cursor.nil?
      cursor = cursor_visible ? cursor.clamp(0, units.length) : units.length
      lines = [+""]
      positions = []
      row = 0
      column = 0
      capacity = first_width

      new_row = lambda do
        lines << +""
        row += 1
        column = 0
        capacity = continuation_width
      end

      # A label can consume the entire first row. Move to the first real value
      # row once; subsequent newline handling can then distinguish a full line
      # from an intentional empty logical line without double-advancing.
      new_row.call if capacity <= 0 && (!units.empty? || cursor_visible)

      units.each_with_index do |grapheme, index|
        if grapheme == "\n"
          if column == capacity
            new_row.call
            positions[index] = [row, column]
          else
            positions[index] = [row, column]
            new_row.call
          end
          next
        end

        cell_width = A.cluster_width(grapheme)
        new_row.call if column == capacity || (column.positive? && column + cell_width > capacity)
        positions[index] = [row, column]
        if cell_width > capacity
          lines[row] << " " * capacity
          column += capacity
        else
          lines[row] << grapheme
          column += cell_width
        end
      end
      new_row.call if column == capacity && cursor_visible
      positions[units.length] = [row, column]
      { lines: lines.freeze, cursor: positions.fetch(cursor).freeze }.freeze
    end

    def render_cursor_cell(text, column)
      before = A.cell_slice(text, 0, column)
      cell = 0
      cluster = nil
      text.each_grapheme_cluster do |grapheme|
        if cell == column && A.cluster_width(grapheme).positive?
          cluster = grapheme
          break
        end
        cell += A.cluster_width(grapheme)
      end
      if cluster
        width = A.cluster_width(cluster)
        after = A.cell_slice(text, column + width, [A.vislen(text) - column - width, 0].max)
        T.paint(:form_value, before) + T.paint(:form_cursor, cluster) + T.paint(:form_value, after)
      else
        T.paint(:form_value, before) + T.paint(:form_cursor, " ")
      end
    end

    def row_status(row, external_error: false)
      if external_error || row.error
        T.paint(:form_error, "!")
      elsif row.dirty?
        T.paint(:form_unsaved, "*")
      else
        " "
      end
    end

    def value_text(row)
      text = row.metadata[:text]
      text = row.metadata[:query] if text.nil? && row.metadata[:searchable]
      text = row.value if text.nil?
      text.nil? ? "" : text.to_s
    end

    def render_choices(row)
      Array(row.metadata[:options]).map do |option|
        cursor = option[:highlighted] ? T.paint(:form_choice_cursor, ">") : " "
        selected = option[:selected] ? T.paint(:form_choice_selected, "[x]") : "[ ]"
        "  #{cursor} #{selected} #{inline_text(option[:label])}"
      end
    end

    def render_picker(row, width:)
      picker = row.metadata[:picker]
      return [] unless row.metadata[:picker_open] && picker

      month = picker.fetch(:month)
      selected = picker.fetch(:selected)
      if width < 28
        return [
          T.paint(:form_group, month.strftime("%B %Y")),
          "#{T.paint(:form_choice_cursor, ">")} #{selected.strftime("%b")} #{selected.day} selected",
        ]
      end

      selected_label = T.paint(:form_choice_selected, "[#{format("%02d", selected.day)}]")
      lines = [
        T.paint(:form_group, month.strftime("%B %Y")),
        "Selected #{selected_label} · #{selected.iso8601}",
        picker.fetch(:weekday_labels).map { |label| format(" %-2s ", label) }.join,
      ]
      lines.concat(picker.fetch(:weeks).map do |week|
        week.map do |day|
          if day == selected
            T.paint(:form_choice_selected, "[#{format("%02d", day.day)}]")
          elsif day.month == month.month
            format(" %2d ", day.day)
          else
            "    "
          end
        end.join
      end)
      lines
    end

    def picker_selected(row)
      row.metadata.fetch(:picker).fetch(:selected)
    end

    def cue_message(message, error)
      "#{error ? "!" : "·"} #{inline_text(message)}"
    end

    def multiline_value?(row)
      row.metadata[:kind] == :text_area || value_text(row).match?(/[\r\n]/)
    end

    # A single-line text field wraps across rows only while it is being edited,
    # so the whole value (or as much as the panel height allows around the
    # cursor) stays visible instead of truncating at the panel edge. Blurred
    # fields keep the compact one-line form.
    def wrap_focused_input?(row)
      row.focused? && row.metadata[:kind] == :input
    end

    def multiline_text(value)
      value.to_s.gsub(/\R/, "\n")
    end

    def inline_text(value, separator: " ")
      value.to_s.gsub(/\R/, separator)
    end

    def truncate(line, width)
      return +"" unless width.positive?
      return line if A.vislen(line) <= width

      A.cell_slice(line, 0, width - 1) + T.paint(:form_hint, "…")
    end

    # Choose the first visible content row. The focused field is anchored a
    # couple of rows below the top so navigation keeps context above it, and the
    # cursor is followed downward only when the field is taller than the budget
    # (so typing near the end of a long field never scrolls the cursor away).
    def viewport_offset(size, budget, field_row, cursor_row)
      return 0 if size <= budget || field_row.nil?

      offset = field_row - CONTEXT_ROWS
      offset = cursor_row - budget + 1 if cursor_row > offset + budget - 1
      offset.clamp(0, size - budget)
    end
  end
end
