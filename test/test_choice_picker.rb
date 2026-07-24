# frozen_string_literal: true

require_relative "test_helper"
require "tui/choice_picker"

class TestChoicePicker < Minitest::Test
  A = Tui::Ansi
  Option = Tui::ChoicePicker::Option

  def options
    [
      Option.new(id: :home, label: "@home"),
      Option.new(id: :work, label: "@work"),
      Option.new(id: :phone, label: "@phone"),
      Option.new(id: :office, label: "Main office", search_text: ["Main office", "HQ"]),
    ]
  end

  def picker(**kwargs)
    Tui::ChoicePicker.new(
      title: "choices", options: options, max_visible: 3,
      selection_mode: :multiple, **kwargs
    )
  end

  def test_relevance_ranks_exact_prefix_token_prefix_then_substring
    subject = picker
    subject.paste("work")
    assert_equal [:work], subject.results.map(&:id)

    subject.input.replace("off")
    assert_equal [:office], subject.results.map(&:id)
    subject.input.replace("hq")
    assert_equal [:office], subject.results.map(&:id)
  end

  def test_cursor_scrolls_stable_results_without_reordering
    subject = picker
    original = subject.results.map(&:id)
    3.times { subject.handle_key("\e[B") }
    assert_equal :office, subject.cursor_id
    assert_equal original, subject.results.map(&:id)
    assert_operator subject.viewport_start, :>, 0
  end

  def test_moving_and_toggling_do_not_move_choice_rows
    subject = picker
    render = lambda do
      subject.popup(row: 0, col: 0, max_width: 80, max_height: 20,
                    inline_input: ->(input) { input.to_s })[:lines].map { |line| A.strip(line) }
    end
    before = render.call
    subject.handle_key("\e[B")
    subject.handle_key(" ")
    after = render.call

    options.each do |option|
      before_index = before.index { |line| line.include?(option.label) }
      next unless before_index

      assert_equal before_index, after.index { |line| line.include?(option.label) },
                   "#{option.label} moved when cursor/selection changed"
    end
  end

  def test_multiple_choice_toggles_stage_until_accept
    subject = picker(selection: [:home])
    assert_equal Set[:home], subject.staged_selection
    subject.handle_key(" ")
    assert_empty subject.staged_selection
    subject.handle_key("\e[B")
    subject.handle_key(" ")
    assert_equal [:accepted, [:work]], subject.handle_key("\r")
  end

  def test_generic_selected_labels_do_not_require_a_context_theme_slot
    subject = picker(selection: [:home])
    line = subject.send(:option_line, subject.current, cursor: true)
    context_style = Tui::Theme.sgr(:context_filter_active)
    refute_includes line, context_style unless context_style.empty?
    assert_includes A.strip(line), "●"
    assert_includes A.strip(line), "@home"
  end

  def test_single_choice_space_remains_search_input
    subject = Tui::ChoicePicker.new(
      title: "one", options: options, selection_mode: :single
    )
    assert_equal :changed, subject.handle_key(" ")
    assert_equal " ", subject.input.to_s
  end

  def test_refresh_preserves_query_cursor_and_never_shrinks_geometry
    subject = picker(selection: [:work], preferred_id: :work)
    initial = subject.popup(row: 0, col: 0, max_width: 80, max_height: 20,
                            inline_input: ->(input) { input.to_s })
    subject.refresh_options(options: options.first(2), selection: [:work])
    refreshed = subject.popup(row: 0, col: 0, max_width: 80, max_height: 20,
                              inline_input: ->(input) { input.to_s })
    assert_equal :work, subject.cursor_id
    assert_equal initial[:lines].size, refreshed[:lines].size
    assert_equal initial[:lines].map { |line| A.vislen(line) },
                 refreshed[:lines].map { |line| A.vislen(line) }
  end

  def test_no_matches_keep_geometry_and_recover_to_top_ranked_item
    subject = picker
    initial = subject.popup(row: 0, col: 0, max_width: 80, max_height: 20,
                            inline_input: ->(input) { input.to_s })
    subject.paste("zzz")
    empty = subject.popup(row: 0, col: 0, max_width: 80, max_height: 20,
                          inline_input: ->(input) { input.to_s })
    assert_empty subject.results
    assert_equal initial[:lines].size, empty[:lines].size

    3.times { subject.handle_key("\x7f") }
    assert_equal :home, subject.cursor_id
  end

  def test_duplicate_ids_are_ignored_and_nil_ids_are_rejected
    duplicate = options + [Option.new(id: :home, label: "duplicate")]
    subject = Tui::ChoicePicker.new(title: "one", options: duplicate)
    assert_equal 4, subject.options.size
    assert_raises(ArgumentError) do
      Tui::ChoicePicker.new(title: "bad", options: [Option.new(id: nil, label: "bad")])
    end
  end
end
