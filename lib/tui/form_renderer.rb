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

    def render(model:, width:, height:, title:, hint: nil, error: nil, suffix: nil)
      width = [Integer(width), 0].max
      height = [Integer(height), 0].max
      return Result.new(lines: [].freeze, focused_content_row: model.focused_row_index) if width.zero? || height.zero?
      return compact(model, width, height, title, error) if width < 6 || height < 3

      content = []
      focused_content_row = nil
      model.groups.each do |group|
        label = group.label.to_s.strip
        content << T.paint(:form_group, label) unless label.empty?
        group.rows.each do |row|
          focused_content_row = content.length if row.focused?
          content << render_row(row, suffix: suffix, external_error: error && row.focused?)
          content.concat(render_picker(row, width: width - 2))
          content.concat(render_choices(row))
        end
      end

      message = error || model.errors[:base]&.first || model.rows.filter_map(&:error).first || hint
      content << T.paint(error || model.errors.any? ? :form_error : :form_hint, cue_message(message, error || model.errors.any?)) if message
      content = [T.paint(:form_hint, "(empty form)")] if content.empty?

      inner_width = width - 2
      budget = height - 2
      offset = viewport_offset(content.length, budget, focused_content_row)
      shown = (content[offset, budget] || []).map { |line| A.vpad(truncate(line, inner_width), inner_width) }
      shown += [" " * inner_width] * (budget - shown.length)

      title_text = truncate(" #{title} ", inner_width)
      lines = ["┌#{title_text}#{"─" * (inner_width - A.vislen(title_text))}┐"]
      lines.concat(shown.map { |line| "│#{line}│" })
      lines << "└#{"─" * inner_width}┘"
      Result.new(lines: lines.freeze, focused_content_row: focused_content_row)
    end

    private

    def compact(model, width, height, title, error)
      row = model.focused_row || model.rows.first
      label = row&.label.to_s.strip
      label = title.to_s if label.empty?
      plain = if error
                "#{row&.focused? ? "›" : ""}! #{label}: #{error}"
              elsif row&.metadata&.dig(:picker_open)
                "> #{picker_selected(row).iso8601}"
              elsif row && !value_text(row).empty?
                "#{row.focused? ? "›" : " "}#{row.dirty? ? "*" : " "} #{value_text(row)}"
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

    def render_row(row, suffix: nil, external_error: false)
      focus = row.focused? ? T.paint(:form_focus, "›") : " "
      status = if external_error || row.error
                 T.paint(:form_error, "!")
               elsif row.dirty?
                 T.paint(:form_unsaved, "*")
               else
                 " "
               end
      label = row.label.to_s
      label += "*" if row.required?
      label = T.paint(row.enabled? ? :form_label : :form_disabled, label)
      value = render_value(row)
      value = T.paint(:form_disabled, value) unless row.enabled?
      tail = suffix.to_s.empty? ? "" : "  #{T.paint(:form_hint, suffix)}"
      "#{focus}#{status} #{label}: #{value}#{tail}"
    end

    def render_value(row)
      text = value_text(row)
      return T.paint(:form_value, text) unless row.focused? && row.cursor

      clusters = text.each_grapheme_cluster.to_a
      cursor = row.cursor.clamp(0, clusters.length)
      before = clusters[0...cursor].join
      at = cursor < clusters.length ? clusters[cursor] : " "
      after = cursor < clusters.length ? clusters[(cursor + 1)..].join : ""
      T.paint(:form_value, before) + T.paint(:form_cursor, at) + T.paint(:form_value, after)
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
        "  #{cursor} #{selected} #{option[:label]}"
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
      "#{error ? "!" : "·"} #{message}"
    end

    def truncate(line, width)
      return +"" unless width.positive?
      return line if A.vislen(line) <= width

      A.cell_slice(line, 0, width - 1) + T.paint(:form_hint, "…")
    end

    def viewport_offset(size, budget, focused)
      return 0 if size <= budget || focused.nil?

      focused.clamp(0, size - budget)
    end
  end
end
