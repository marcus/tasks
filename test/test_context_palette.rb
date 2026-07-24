# frozen_string_literal: true

require_relative "test_helper"
require "tui/context_palette"

class TestContextPalette < Minitest::Test
  A = Tui::Ansi
  CLEAR = Tui::ContextPalette::CLEAR_ID

  def palette(contexts: %w[@home @work @computer], current: nil, current_filters: nil)
    Tui::ContextPalette.new(
      contexts: contexts, current: current, current_filters: current_filters
    )
  end

  def test_clear_row_is_always_first_and_contexts_are_normalized_unique_sorted
    picker = palette(contexts: ["work", "@home", "@work", "  @email  ", ""])
    assert_equal [CLEAR, "@email", "@home", "@work"], picker.options.map(&:id)
    assert_equal Tui::ContextPalette::CLEAR_LABEL, picker.options.first.label
  end

  def test_active_contexts_are_checked_and_first_active_context_has_cursor
    picker = palette(current_filters: %w[home @work])
    assert_equal "@home", picker.current.id
    assert_equal Set["@home", "@work"], picker.staged_selection
    assert_equal %w[@home @work], picker.current_filters
  end

  def test_filtering_ranks_prefix_and_preserves_one_key_enter_behavior
    picker = palette
    picker.paste("wor")
    assert_equal ["@work"], picker.results.map(&:id)
    assert_equal "@work", picker.current.id
    event = picker.handle_key("\r")
    assert_equal [:apply, ["@work"]], event
  end

  def test_space_toggles_without_closing_or_moving_cursor
    picker = palette(current_filters: ["@home"])
    assert_equal "@home", picker.current.id
    assert_equal :changed, picker.handle_key(" ")
    assert_empty picker.staged_selection
    assert_equal "@home", picker.current.id

    assert_equal :changed, picker.handle_key(" ")
    assert_equal Set["@home"], picker.staged_selection
    assert_equal "@home", picker.current.id
  end

  def test_arrow_navigation_clamps_and_letters_type_into_search
    picker = palette
    picker.handle_key("\e[B")
    assert_equal 1, picker.selected
    20.times { picker.handle_key("\e[B") }
    assert_equal picker.results.size - 1, picker.selected
    picker.handle_key("\e[A")
    assert_equal picker.results.size - 2, picker.selected

    # j/k remain searchable context characters, not list-navigation aliases.
    picker.input.replace("")
    picker.handle_key("j")
    assert_equal "j", picker.input.to_s
    assert_equal 0, picker.selected
  end

  def test_refresh_rebuilds_live_options_and_preserves_cursor_by_id
    picker = palette(contexts: %w[@home @work], current_filters: ["@work"])
    picker.refresh_options(contexts: %w[@home @office @work],
                           current_filters: ["@work"])
    assert_equal [CLEAR, "@home", "@office", "@work"], picker.options.map(&:id)
    assert_equal "@work", picker.current.id

    picker.refresh_options(contexts: %w[@home @office],
                           current_filters: ["@home"])
    assert_equal [CLEAR, "@home", "@office"], picker.options.map(&:id)
    assert_empty picker.staged_selection, "removed staged choices are pruned, not replaced"
  end

  def test_refresh_preserves_staged_choices_during_external_reload
    picker = palette(contexts: %w[@home @work])
    picker.handle_key("\e[B") # Clear -> @home
    picker.handle_key(" ")
    picker.refresh_options(contexts: %w[@email @home @work], current_filters: [])
    assert_equal Set["@home"], picker.staged_selection
    assert_equal "@home", picker.current.id
  end

  def test_reload_prunes_removed_baseline_without_breaking_quick_apply
    picker = palette(contexts: %w[@home @work], current_filters: ["@home"])
    picker.refresh_options(contexts: ["@work"], current_filters: [])
    picker.paste("work")
    assert_equal [:apply, ["@work"]], picker.handle_key("\r")
  end

  def test_clear_command_space_stages_empty_and_return_applies_empty
    picker = palette(current_filters: %w[@home @work])
    picker.input.replace("")
    2.times { picker.handle_key("\e[A") } # @home -> @computer -> clear
    assert_equal CLEAR, picker.current.id

    assert_equal :changed, picker.handle_key(" ")
    assert_empty picker.staged_selection
    assert_equal CLEAR, picker.current.id
    assert_equal [:apply, []], picker.handle_key("\r")
  end

  def test_enter_applies_staged_multiple_selection_and_escape_cancels
    picker = palette
    picker.handle_key("\e[B") # clear -> @computer
    picker.handle_key(" ")
    picker.handle_key("\e[B") # @home
    picker.handle_key(" ")
    event = picker.handle_key("\r")
    assert_equal :apply, event.first
    assert_equal Set["@computer", "@home"], Set.new(event.last)

    assert_equal :cancelled, palette.handle_key("\e")
  end

  def test_empty_filter_results_refuse_enter
    picker = palette
    picker.paste("zzzz")
    assert_empty picker.results
    assert_equal :handled, picker.handle_key("\r")
  end

  def test_popup_is_fixed_while_filtering_and_marks_cursor_and_checks
    picker = palette(current_filters: %w[@home @work])
    unfiltered = picker.popup(row: 1, col: 2, max_width: 60, max_height: 14,
                              inline_input: ->(input) { input.to_s })
    picker.paste("wor")
    filtered = picker.popup(row: 1, col: 2, max_width: 60, max_height: 14,
                            inline_input: ->(input) { input.to_s })
    assert_equal unfiltered[:lines].size, filtered[:lines].size
    assert_equal unfiltered[:lines].map { |line| A.vislen(line) },
                 filtered[:lines].map { |line| A.vislen(line) }

    text = filtered[:lines].map { |line| A.strip(line) }.join("\n")
    assert_includes text, "❯"
    assert_includes text, "●"
    assert_includes text, "@work"
    assert_includes text, "contexts"
  end

  def test_popup_adapts_to_every_narrow_rectangle
    picker = palette(current_filters: ["@work"])
    (1..10).each do |width|
      (1..6).each do |height|
        box = picker.popup(row: 0, col: 0, max_width: width, max_height: height,
                           inline_input: ->(_input) { " " })
        assert_operator box[:lines].size, :<=, height
        assert box[:lines].all? { |line| A.vislen(line) <= width }
      end
    end
  end
end
