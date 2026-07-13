# frozen_string_literal: true

require "minitest/autorun"
require_relative "../lib/term_form"

class TestTermFormTextFields < Minitest::Test
  def test_input_edits_combining_and_wide_graphemes_as_units
    form = form_with(TermForm::Fields::Input.new(key: :title, value: "e\u0301👩‍💻界"))

    assert form.handle("\e[D").handled?
    assert form.handle("\x7f").changed?
    assert_equal "e\u0301界", form.value(:title)
    assert_equal 1, form.field(:title).cursor

    form.handle("\x7f")
    assert_equal "界", form.value(:title)
    assert_equal 0, form.field(:title).cursor

    form.handle("\x04")
    assert_equal "", form.value(:title)
  end

  def test_input_supports_word_motion_deletion_and_kill_shortcuts_but_reserves_ctrl_k
    field = TermForm::Fields::Input.new(key: :title, value: "alpha beta gamma")
    form = form_with(field)

    form.handle("\e[1;5D")
    form.handle("X")
    assert_equal "alpha beta Xgamma", form.value(:title)
    form.handle("\x17")
    assert_equal "alpha beta gamma", form.value(:title)
    form.handle("\x15")
    assert_equal "gamma", form.value(:title)

    before = field.cursor
    transition = form.handle("\x0b")
    assert transition.unhandled?
    assert_equal "gamma", form.value(:title)
    assert_equal before, field.cursor
  end

  def test_single_line_paste_normalizes_newlines_and_tabs_without_committing
    form = form_with(TermForm::Fields::Input.new(key: :title))

    transition = form.handle(TermForm::Event.paste("first\r\nsecond\tthird"))

    assert transition.changed?
    assert_equal "first  second third", form.value(:title)
    refute form.pending?
  end

  def test_input_tab_remains_form_traversal
    title = TermForm::Fields::Input.new(key: :title, value: "old")
    other = TermForm::Fields::Input.new(key: :other, value: "next")
    form = TermForm::Form.new(groups: [TermForm::Group.new(key: :main, fields: [title, other])])

    form.handle("X")
    transition = form.handle("\t")

    assert transition.commit_requested?
    assert_equal :title, transition.request.field_key
    assert_equal :other, transition.request.intended_focus
    assert_equal "oldX", transition.request.proposed_value
  end

  def test_decoded_key_events_share_the_injected_key_map
    title = TermForm::Fields::Input.new(key: :title, value: "ab")
    other = TermForm::Fields::Input.new(key: :other)
    form = TermForm::Form.new(groups: [TermForm::Group.new(key: :main, fields: [title, other])])

    assert form.handle(TermForm::Event.key(:left)).handled?
    assert form.handle(TermForm::Event.key("X")).changed?
    assert_equal "aXb", form.value(:title)
    assert form.handle(TermForm::Event.key(:tab)).commit_requested?

    form.reject_commit
    assert form.handle(type: :key, key: :left).handled?
  end

  def test_text_area_return_inserts_newline_while_tab_traverses
    notes = TermForm::Fields::TextArea.new(key: :notes, value: "first")
    other = TermForm::Fields::Input.new(key: :other)
    form = TermForm::Form.new(groups: [TermForm::Group.new(key: :main, fields: [notes, other])])

    assert form.handle("\r").changed?
    assert_equal "first\n", form.value(:notes)
    transition = form.handle("\t")
    assert transition.commit_requested?
    assert_equal "first\n", transition.request.proposed_value
    refute_includes form.value(:notes), "\t"
  end

  def test_text_area_paste_preserves_line_breaks_and_normalizes_tabs_on_entry
    form = form_with(TermForm::Fields::TextArea.new(key: :notes))

    form.handle(TermForm::Event.paste("one\r\ntwo\rthree\tfour"))

    assert_equal "one\ntwo\nthree four", form.value(:notes)
  end

  def test_input_render_uses_cells_and_never_splits_a_wide_grapheme
    field = TermForm::Fields::Input.new(key: :title, value: "界e\u0301")
    field.handle_key("\x01")

    narrow = field.render(width: 1)
    assert_equal [" "], narrow.lines
    assert_equal 1, TermForm::Text.cell_width(narrow.lines.first)
    assert_equal 0, narrow.cursor_column

    field.handle_key("\x05")
    scrolled = field.render(width: 2)
    assert_operator scrolled.column_offset, :>, 0
    assert_operator TermForm::Text.cell_width(scrolled.lines.first), :<=, 2
    assert_equal 1, scrolled.cursor_column
  end

  def test_text_area_wraps_by_cells_and_exposes_a_virtual_cursor
    field = TermForm::Fields::TextArea.new(key: :notes, value: "a界b\nc\u0301d")

    view = field.render(width: 3, height: 4)

    assert_equal ["a界", "b", "c\u0301d", ""], view.lines
    assert_equal [2, 2], [view.virtual_cursor_row, view.virtual_cursor_column]
    assert_equal [2, 2], [view.cursor_row, view.cursor_column]
    view.lines.each { |line| assert_operator TermForm::Text.cell_width(line), :<=, 3 }
  end

  def test_text_area_virtual_cursor_wraps_at_an_exact_cell_boundary
    field = TermForm::Fields::TextArea.new(key: :notes, value: "界")

    view = field.render(width: 2, height: 2)

    assert_equal ["界", ""], view.lines
    assert_equal [1, 0], view.virtual_cursor
    assert_equal [1, 0], view.cursor
  end

  def test_text_area_inner_viewport_tracks_cursor_then_scrolls_back_to_start
    field = TermForm::Fields::TextArea.new(key: :notes, value: "one\ntwo\nthree\nfour")

    bottom = field.render(width: 8, height: 2)
    assert_equal 2, bottom.row_offset
    assert_equal ["three", "four"], bottom.lines
    assert_equal 1, bottom.cursor_row

    field.handle_key("\x01")
    top = field.render(width: 8, height: 2)
    assert_equal 0, top.row_offset
    assert_equal ["one", "two"], top.lines
    assert_equal 0, top.cursor_row
  end

  def test_text_area_vertical_motion_uses_wrapped_rows
    field = TermForm::Fields::TextArea.new(key: :notes, value: "abcd")
    form = form_with(field)
    field.render(width: 2, height: 2)

    assert form.handle("\e[A").handled?
    assert_equal 2, field.cursor
    assert form.handle("X").changed?
    assert_equal "abXcd", form.value(:notes)
  end

  def test_host_refresh_reconciles_the_private_editor_buffer
    field = TermForm::Fields::Input.new(key: :title, value: "old")
    form = form_with(field)

    form.refresh(values: { title: "remote🙂" })

    assert_equal "remote🙂", field.text
    assert_equal 7, field.cursor
    assert_equal "remote🙂", form.value(:title)
  end

  def test_host_values_are_normalized_before_value_and_baseline_reconciliation
    field = TermForm::Fields::Input.new(key: :title, value: "old\nvalue", baseline: "old\nvalue")
    form = form_with(field)

    assert_equal "old value", form.baseline(:title)
    form.refresh(values: { title: "remote\nvalue" })

    assert_equal "remote value", form.value(:title)
    assert_equal "remote value", form.baseline(:title)
    assert_equal "remote value", field.text
  end

  private

  def form_with(field)
    TermForm::Form.new(groups: [TermForm::Group.new(key: :main, fields: [field])])
  end
end
