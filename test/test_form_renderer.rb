# frozen_string_literal: true

require_relative "test_helper"
require "io/console"
require "term_form"
require "tui/form_renderer"

class TestFormRenderer < Minitest::Test
  A = Tui::Ansi
  T = Tui::Theme

  def teardown
    T.reset!
  end

  def test_focus_unsaved_and_cursor_have_textual_and_semantic_cues
    form = input_form(value: "界", baseline: "")
    result = render(form, width: 32, height: 4)
    plain = A.strip(result.lines.join("\n"))

    assert_includes plain, "›* Name: 界"
    assert_includes result.lines.join, "\e[", "default theme paints semantic form slots"
    assert_equal 0, result.focused_content_row
  end

  def test_error_cues_survive_mono_no_color_mode_and_no_styling
    form = input_form(value: "bad", validate: ->(*) { "not valid" })
    form.validate

    T.configure!(name: "mono")
    mono = render(form, width: 32, height: 4).lines.join("\n")
    refute_match(/\e\[(?:3[0-9]|4[0-8]|9[0-7])m/, mono)
    assert_includes A.strip(mono), "›! Name: bad"
    assert_includes A.strip(mono), "! not valid"

    T.configure!(overrides: T::DEFAULTS.to_h { |slot, _| [slot, "none"] })
    plain = render(form, width: 32, height: 4).lines.join("\n")
    refute_includes plain, "\e["
    assert_includes plain, "›! Name: bad"
    assert_includes plain, "! not valid"
  end

  def test_choice_cursor_and_selection_do_not_depend_on_color
    field = TermForm::Fields::Select.new(
      key: :state, label: "State", value: "TODO",
      options: [["NEXT", "Next"], ["TODO", "Todo"]], searchable: false,
    )
    form = TermForm::Form.new(groups: [TermForm::Group.new(key: :main, label: "", fields: [field])])
    T.configure!(overrides: T::DEFAULTS.to_h { |slot, _| [slot, "none"] })

    plain = render(form, width: 32, height: 6).lines.join("\n")
    assert_includes plain, "> [ ] Next"
    assert_includes plain, "[x] Todo"
  end

  def test_date_input_switches_from_text_to_a_deterministic_wide_picker
    form = date_form
    text = A.strip(render(form, width: 42, height: 12).lines.join("\n"))
    assert_includes text, "Date: 2026-07-13"
    refute_includes text, "Selected [13]"

    form.handle("\r")
    picker = A.strip(render(form, width: 42, height: 12).lines.join("\n"))
    assert_includes picker, "July 2026"
    assert_includes picker, "Selected [13] · 2026-07-13"
    assert_includes picker, "[13]", "selected day has a non-color calendar cue"
    assert_includes picker, "Mo  Tu  We  Th  Fr  Sa  Su"
  end

  def test_date_picker_selected_day_updates_in_narrow_mono_no_color_output
    form = date_form
    form.handle("\r")
    form.handle("\e[C")
    T.configure!(name: "mono")

    output = render(form, width: 19, height: 6).lines.join("\n")
    plain = A.strip(output)
    refute_match(/\e\[(?:3[0-9]|4[0-8]|9[0-7])m/, output)
    assert_includes plain, "July 2026"
    assert_includes plain, "> Jul 14 selected"
    refute_includes plain, "2026-07-13", "picker output differs from the closed text field"
  end

  def test_zero_and_one_cell_picker_budgets_never_overrun
    form = date_form
    form.handle("\r")
    renderer = Tui::FormRenderer.new

    [[0, 0], [0, 4], [4, 0]].each do |width, height|
      result = renderer.render(model: form.render_model, width: width, height: height, title: "Date")
      assert_empty result.lines
    end

    (0..18).each do |width|
      (0..8).each do |height|
        result = renderer.render(model: form.render_model, width: width, height: height, title: "Date")
        assert_operator result.lines.size, :<=, height
        assert result.lines.all? { |line| A.vislen(line) <= width },
               "picker #{width}x#{height}: #{result.lines.inspect}"
      end
    end

    one_cell = renderer.render(model: form.render_model, width: 1, height: 1, title: "Date")
    assert_equal [">"], one_cell.lines
  end

  def test_unicode_and_every_tiny_positive_rectangle_stay_inside_cell_budget
    form = input_form(value: "界🙂e\u0301", baseline: "")
    renderer = Tui::FormRenderer.new

    (1..14).each do |width|
      (1..6).each do |height|
        result = renderer.render(
          model: form.render_model, width: width, height: height,
          title: "編集", hint: "金曜日 · esc",
        )
        assert_operator result.lines.size, :<=, height
        assert result.lines.all? { |line| A.vislen(line) <= width },
               "#{width}x#{height}: #{result.lines.inspect}"
      end
    end

    compact = renderer.render(
      model: form.render_model, width: 8, height: 1,
      title: "編集", hint: "金曜日 · esc",
    )
    assert A.strip(compact.lines.first).start_with?("›*"), "narrow focus/dirty cues must remain textual"
  end

  def test_long_notes_and_options_stay_bounded_in_short_stacked_and_wide_panels
    notes = TermForm::Fields::TextArea.new(
      key: :notes, label: "Notes", value: ("界🙂 long note " * 30),
    )
    location = TermForm::Fields::Select.new(
      key: :location, label: "Location", value: "one",
      options: [
        ["one", "A very long project and parent-task option " * 4],
        ["two", "Another destination with Unicode 界🙂 " * 4],
      ],
      searchable: false,
    )
    form = TermForm::Form.new(
      groups: [TermForm::Group.new(key: :main, label: "Long data", fields: [notes, location])],
      focus: :location,
    )
    form.handle("\r")
    renderer = Tui::FormRenderer.new

    [[8, 1], [32, 4], [48, 6], [96, 10]].each do |width, height|
      result = renderer.render(
        model: form.render_model, width: width, height: height,
        title: "Long data", hint: "tab saves",
      )
      assert_operator result.lines.size, :<=, height
      assert result.lines.all? { |line| A.vislen(line) <= width },
             "long data escaped #{width}x#{height}: #{result.lines.inspect}"
      if height < 3
        assert_equal 0, result.focused_content_row
      else
        focused_line = A.strip(result.lines.fetch(result.focused_content_row + 1))
        assert_includes focused_line, "Location",
                        "visible focus row follows the viewport after multiline notes expand"
      end
    end

    wide = A.strip(renderer.render(
      model: form.render_model, width: 96, height: 10,
      title: "Long data", hint: "tab saves",
    ).lines.join("\n"))
    assert_includes wide, "Location: one"
    assert_includes wide, "A very long project and parent-task option"
  end

  def test_unicode_multiline_notes_expand_into_explicit_rows_with_cursor_mapping
    notes = TermForm::Fields::TextArea.new(
      key: :notes, label: "Notes", value: "first 👩‍💻界\nsecond e\u0301 line\n\n尾",
      baseline: "", validate: ->(*) { nil },
    )
    form = TermForm::Form.new(
      groups: [TermForm::Group.new(key: :main, label: "", fields: [notes])],
    )
    result = render(form, width: 32, height: 12)
    plain = result.lines.map { |line| A.strip(line) }

    refute result.lines.any? { |line| line.match?(/[\r\n]/) }
    assert plain.any? { |line| line.include?("›* Notes: first") }
    assert plain.any? { |line| line.include?("│* second e\u0301 line") }
    assert plain.any? { |line| line.include?("│* 尾") }
    cursor_line = result.lines.index { |line| line.include?("\e[7m") }
    refute_nil cursor_line
    assert_equal cursor_line - 1, result.focused_content_row,
                 "focused content row points at the explicit row containing the cursor"
  end

  def test_multiline_error_and_generic_semantic_value_never_embed_newlines
    notes = TermForm::Fields::TextArea.new(
      key: :notes, label: "Notes", value: "draft\n尾", baseline: "",
      validate: ->(*) { "notes\nmust be valid" },
    )
    form = TermForm::Form.new(
      groups: [TermForm::Group.new(key: :main, label: "", fields: [notes])],
    )
    form.validate
    result = render(form, width: 30, height: 8)
    plain = result.lines.map { |line| A.strip(line) }
    assert plain.any? { |line| line.include?("›! Notes: draft") }
    assert plain.any? { |line| line.include?("! notes must be valid") }
    refute result.lines.any? { |line| line.match?(/[\r\n]/) }

    form.set_value(:notes, (1..12).map { |index| "invalid #{index}" }.join("\n"))
    short = render(form, width: 24, height: 6)
    focused = short.lines.fetch(short.focused_content_row + 1)
    assert_includes A.strip(focused), "│!",
                    "scrolled cursor row retains the focused error cue"
    assert_includes focused, "\e[7m", "focused row still owns the cursor cell"

    generic = TermForm::Form.new(groups: [
      TermForm::Group.new(key: :main, label: "", fields: [
        TermForm::Field.new(key: :value, label: "Value", value: "alpha\nbeta"),
      ]),
    ])
    generic_result = render(generic, width: 24, height: 6)
    generic_plain = generic_result.lines.map { |line| A.strip(line) }
    assert generic_plain.any? { |line| line.include?("Value: alpha") }
    assert generic_plain.any? { |line| line.include?("beta") }
    refute generic_result.lines.any? { |line| line.match?(/[\r\n]/) }
  end

  def test_multiline_unicode_respects_every_width_and_height_budget
    notes = TermForm::Fields::TextArea.new(
      key: :notes, label: "Notes", baseline: "",
      value: (1..18).map { |index| "#{index} 👩‍💻界 e\u0301 and a long tail" }.join("\n"),
    )
    form = TermForm::Form.new(
      groups: [TermForm::Group.new(key: :main, label: "", fields: [notes])],
    )
    renderer = Tui::FormRenderer.new

    (0..64).each do |width|
      (0..18).each do |height|
        result = renderer.render(
          model: form.render_model, width: width, height: height,
          title: "Notes", hint: "tab saves",
        )
        assert_operator result.lines.size, :<=, height
        assert result.lines.all? { |line| A.vislen(line) <= width },
               "multiline escaped #{width}x#{height}: #{result.lines.inspect}"
        refute result.lines.any? { |line| line.match?(/[\r\n]/) },
               "embedded newline escaped #{width}x#{height}"
      end
    end

    visible = renderer.render(model: form.render_model, width: 40, height: 8, title: "Notes")
    cursor_line = visible.lines.index { |line| line.include?("\e[7m") }
    assert_equal cursor_line - 1, visible.focused_content_row
  end

  def test_exact_full_line_before_newline_does_not_create_a_phantom_row
    [["abcd\nZ", 12], ["界界\nZ", 12]].each do |value, width|
      form = notes_form(value, label: "N")
      form.handle("\e[F") # phantom rows are only threatened by an end-of-buffer cursor
      result = Tui::FormRenderer.new.render(
        model: form.render_model, width: width, height: 6, title: "N",
      )
      fields = multiline_field_lines(result)

      assert_equal 2, fields.size, "#{value.inspect} at width #{width}: #{fields.inspect}"
      assert_match(/›\* N: (?:abcd|界界)/, fields.first)
      assert_match(/│\* Z/, fields.last)
      assert_equal 1, result.focused_content_row
      assert_includes result.lines.fetch(2), "\e[7m", "cursor remains on the second semantic row"
    end

    continuation_form = notes_form("a\n1234567\nZ", label: "N")
    continuation_form.handle("\e[F")
    continuation = Tui::FormRenderer.new.render(
      model: continuation_form.render_model,
      width: 12, height: 7, title: "N",
    )
    continuation_fields = multiline_field_lines(continuation)
    assert_equal 3, continuation_fields.size, continuation_fields.inspect
    assert_match(/│\* 1234567/, continuation_fields[1])
    assert_match(/│\* Z/, continuation_fields[2])
  end

  def test_intentional_leading_middle_and_trailing_blank_lines_remain_explicit
    {
      "a\n\nb" => ["›* N: a", "│*", "│* b"],
      "\na" => ["›* N:", "│* a"],
      "a\n" => ["›* N: a", "│*"],
    }.each do |value, expected|
      result = Tui::FormRenderer.new.render(
        model: notes_form(value, label: "N").render_model,
        width: 20, height: 8, title: "N",
      )
      actual = multiline_field_lines(result).map(&:rstrip)
      assert_equal expected, actual, value.inspect
      refute result.lines.any? { |line| line.match?(/[\r\n]/) }
    end
  end

  def test_exact_boundary_newlines_hold_across_ascii_and_unicode_widths
    (9..40).each do |width|
      first_capacity = width - 8 # outer borders plus `›* N: `
      values = ["a" * first_capacity]
      values << "界" * (first_capacity / 2) if first_capacity.even?
      values.each do |first_line|
        form = notes_form("#{first_line}\nZ", label: "N")
        form.handle("\e[F") # boundary rows are only threatened by an end-of-buffer cursor
        result = Tui::FormRenderer.new.render(
          model: form.render_model, width: width, height: 6, title: "N",
        )
        fields = multiline_field_lines(result)
        assert_equal 2, fields.size, "#{width}: #{first_line.inspect} => #{fields.inspect}"
        assert_match(/│\* Z/, fields.last)
        assert_equal 1, result.focused_content_row
      end
    end
  end

  def test_focused_single_line_field_wraps_while_a_blurred_one_stays_truncated
    editing = TermForm::Fields::Input.new(
      key: :title, label: "Title", value: "alpha beta gamma delta epsilon zeta",
    )
    blurred = TermForm::Fields::Input.new(
      key: :note, label: "Note", value: "one two three four five six seven eight",
    )
    form = TermForm::Form.new(
      groups: [TermForm::Group.new(key: :main, label: "", fields: [editing, blurred])],
      focus: :title,
    )
    result = Tui::FormRenderer.new.render(
      model: form.render_model, width: 30, height: 12, title: "Edit",
    )
    plain = result.lines.map { |line| A.strip(line) }

    # The focused single-line field wraps onto continuation rows so the whole
    # value stays visible, including text that would fall past a truncation.
    title_rows = multiline_field_lines(result)
    assert_equal 1, title_rows.count { |line| line.start_with?("›") }
    assert title_rows.any? { |line| line.start_with?("│") },
           "focused single-line field wraps onto continuation rows"
    assert plain.any? { |line| line.include?("zeta") },
           "tail of the edited value survives instead of being truncated away"
    assert result.lines.any? { |line| line.include?("\e[7m") }, "cursor stays visible while wrapping"

    # The neighbouring blurred field keeps the compact one-line truncated form.
    note_lines = plain.select { |line| line.include?("Note:") }
    assert_equal 1, note_lines.size, "blurred single-line field stays on one row"
    assert_includes note_lines.first, "…", "blurred single-line field truncates as before"
    refute note_lines.first.include?("eight"), "blurred value is not fully expanded"
  end

  def test_focusing_a_field_keeps_context_above_it_instead_of_jumping_to_an_edge
    fillers_before = (0...10).map do |index|
      TermForm::Fields::Input.new(key: :"before#{index}", label: "Item#{index}", value: "x")
    end
    notes = TermForm::Fields::TextArea.new(key: :notes, label: "Notes", value: "n1\nn2\nn3")
    fillers_after = (0...5).map do |index|
      TermForm::Fields::Input.new(key: :"after#{index}", label: "Tail#{index}", value: "y")
    end
    form = TermForm::Form.new(
      groups: [TermForm::Group.new(key: :main, label: "",
                                   fields: fillers_before + [notes] + fillers_after)],
      focus: :notes,
    )
    result = Tui::FormRenderer.new.render(
      model: form.render_model, width: 40, height: 8, title: "Edit", hint: "tab saves",
    )
    plain = result.lines.map { |line| A.strip(line) }

    # The multiline field is shown from its top (label row visible) rather than
    # pinned to the bottom, and a couple of context rows precede it.
    notes_index = plain.index { |line| line.include?("Notes:") }
    refute_nil notes_index, "focused field top stays visible: #{plain.inspect}"
    context_rows = plain[1...notes_index] # drop the top border at index 0
    assert_equal Tui::FormRenderer::CONTEXT_ROWS, context_rows.size,
                 "focused field keeps context rows above it: #{plain.inspect}"
    assert context_rows.all? { |line| line.include?("Item") },
           "context above the field is the surrounding form, not blank padding"
    assert result.lines.any? { |line| line.include?("\e[7m") }, "cursor remains visible"
    # Content below the field is also visible, proving it was not jammed to the edge.
    assert plain.any? { |line| line.include?("Tail") }, "field is not pinned to the bottom edge"
  end

  def test_tall_multiline_field_opens_at_its_top_and_follows_the_cursor_to_the_end
    notes = TermForm::Fields::TextArea.new(
      key: :notes, label: "Notes",
      value: (1..20).map { |index| "note line #{index}" }.join("\n"),
    )
    form = TermForm::Form.new(
      groups: [TermForm::Group.new(key: :main, label: "", fields: [notes])],
      focus: :notes,
    )
    renderer = Tui::FormRenderer.new

    opened = renderer.render(model: form.render_model, width: 30, height: 8, title: "Edit")
    opened_plain = opened.lines.map { |line| A.strip(line) }
    assert opened_plain.any? { |line| line.include?("Notes: note line 1") },
           "focusing a tall field shows its label and first row: #{opened_plain.inspect}"
    refute opened_plain.any? { |line| line.include?("note line 20") },
           "bottom rows stay out of view until the cursor travels there"
    assert opened.lines.any? { |line| line.include?("\e[7m") }, "entry cursor is visible at the top"

    form.handle("\e[F") # end-of-buffer key
    ended = renderer.render(model: form.render_model, width: 30, height: 8, title: "Edit")
    ended_plain = ended.lines.map { |line| A.strip(line) }
    assert ended_plain.any? { |line| line.include?("note line 20") },
           "moving to the end scrolls the bottom into view: #{ended_plain.inspect}"
    refute ended_plain.any? { |line| line.include?("Notes: note line 1") },
           "the field top scrolls away once the cursor reaches the end"
    assert ended.lines.any? { |line| line.include?("\e[7m") }, "cursor stays visible at the end"
  end

  def test_group_labels_render_as_a_themed_background_chip
    field = TermForm::Fields::Input.new(key: :title, label: "Title", value: "x")
    form = TermForm::Form.new(
      groups: [TermForm::Group.new(key: :basics, label: "Basics", fields: [field])],
      focus: :title,
    )
    result = Tui::FormRenderer.new.render(
      model: form.render_model, width: 30, height: 6, title: "Edit",
    )

    header = result.lines.find { |line| A.strip(line).include?("Basics") }
    refute_nil header, "group header row is rendered"
    # The section header is a padded chip carrying the themed :form_group_label
    # fg/bg pair, distinct from the plain :form_label used on the field row.
    assert_includes header, T.sgr(:form_group_label), "group label carries its themed role"
    assert_includes A.strip(header), " Basics ", "group label renders as a padded chip"

    T.configure!(overrides: { "form_group_label" => "black on-magenta" })
    recolored = Tui::FormRenderer.new.render(
      model: form.render_model, width: 30, height: 6, title: "Edit",
    ).lines.find { |line| A.strip(line).include?("Basics") }
    assert_includes recolored, "\e[30;45m", "chip follows the configured background role"
  end

  def test_rendering_does_not_sample_terminal_geometry
    form = input_form(value: "value")
    IO.stub(:console, -> { raise "geometry IO is forbidden" }) do
      assert_equal 4, render(form, width: 24, height: 4).lines.size
    end
  end

  private

  def input_form(value:, baseline: value, validate: nil)
    field = TermForm::Fields::Input.new(
      key: :name, label: "Name", value: value, baseline: baseline, validate: validate,
    )
    TermForm::Form.new(groups: [TermForm::Group.new(key: :main, label: "", fields: [field])])
  end

  def notes_form(value, label: "Notes")
    field = TermForm::Fields::TextArea.new(
      key: :notes, label: label, value: value, baseline: "",
    )
    TermForm::Form.new(groups: [TermForm::Group.new(key: :main, label: "", fields: [field])])
  end

  def multiline_field_lines(result)
    result.lines[1...-1].to_a.filter_map do |line|
      content = A.strip(line).delete_prefix("│").delete_suffix("│").rstrip
      content if content.start_with?("›", "│")
    end
  end

  def date_form
    field = TermForm::Fields::DateInput.new(
      key: :date, label: "Date", value: Date.new(2026, 7, 13),
      today: -> { Date.new(2026, 7, 13) },
    )
    TermForm::Form.new(groups: [TermForm::Group.new(key: :main, label: "", fields: [field])])
  end

  def render(form, width:, height:)
    Tui::FormRenderer.new.render(
      model: form.render_model, width: width, height: height,
      title: "Example", hint: "enter saves · esc cancels",
    )
  end
end
