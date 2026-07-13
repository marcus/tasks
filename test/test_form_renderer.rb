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
      assert_equal(height < 3 ? 0 : 2, result.focused_content_row)
    end

    wide = A.strip(renderer.render(
      model: form.render_model, width: 96, height: 10,
      title: "Long data", hint: "tab saves",
    ).lines.join("\n"))
    assert_includes wide, "Location: one"
    assert_includes wide, "A very long project and parent-task option"
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
