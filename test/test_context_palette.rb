# frozen_string_literal: true

require_relative "test_helper"
require "tui/context_palette"

class TestContextPalette < Minitest::Test
  A = Tui::Ansi

  def palette(contexts: %w[@home @work @computer], current: nil)
    Tui::ContextPalette.new(contexts: contexts, current: current)
  end

  def test_clear_row_is_always_first_and_contexts_are_normalized_unique_sorted
    p = palette(contexts: ["work", "@home", "@work", "  @email  ", ""])
    assert_equal [nil, "@email", "@home", "@work"], p.options.map(&:id)
    assert_equal Tui::ContextPalette::CLEAR_LABEL, p.options.first.label
  end

  def test_current_filter_preselects_matching_option
    p = palette(current: "home")
    assert_equal "@home", p.current.id
    assert_equal "@home", p.current_filter
  end

  def test_filtering_searches_labels_and_resets_selection
    p = palette
    p.handle_key("\e[B")
    assert_equal 1, p.selected
    p.paste("wor")
    assert_equal 0, p.selected
    assert_equal ["@work"], p.results.map(&:id)
  end

  def test_navigation_clamps_and_letters_type_into_search
    p = palette
    p.handle_key("\e[B")
    assert_equal 1, p.selected
    20.times { p.handle_key("\e[B") }
    assert_equal p.results.size - 1, p.selected
    p.handle_key("\e[A")
    assert_equal p.results.size - 2, p.selected

    # j/k must narrow the query (e.g. @john), not steal movement like list mode.
    p.input.replace("")
    p.handle_key("j")
    assert_equal "j", p.input.to_s
    assert_equal 0, p.selected
  end

  def test_refresh_options_rebuilds_from_live_contexts
    p = palette(contexts: %w[@home @work], current: "@work")
    p.paste("wor")
    p.refresh_options(contexts: %w[@home @office], current: "@home")
    assert_equal [nil, "@home", "@office"], p.options.map(&:id)
    assert_equal "wor", p.input.to_s
    assert_equal "@home", p.current_filter
    assert_empty p.results # query "wor" matches neither remaining context
  end

  def test_enter_applies_and_escape_cancels
    p = palette
    event = p.handle_key("\r")
    assert_equal :apply, event.first
    assert_nil event.last.id

    p.handle_key("\e[B")
    event = p.handle_key("\r")
    assert_equal :apply, event.first
    assert_equal "@computer", event.last.id

    assert_equal :cancelled, p.handle_key("\e")
  end

  def test_empty_filter_results_refuse_enter
    p = palette
    p.paste("zzzz")
    assert_empty p.results
    assert_equal :handled, p.handle_key("\r")
  end

  def test_popup_adapts_to_narrow_rectangles_and_marks_active
    p = palette(current: "@work")
    p.options.each_with_index { |opt, i| break p.instance_variable_set(:@selected, i) if opt.id == "@work" }
    popup = p.popup(row: 1, col: 2, max_width: 48, max_height: 12,
                    inline_input: ->(input) { input.to_s })
    text = popup[:lines].map { |line| A.strip(line) }.join("\n")
    assert_includes text, "@work"
    assert_includes text, "active"
    assert_includes text, "context"

    (1..10).each do |width|
      (1..6).each do |height|
        box = p.popup(row: 0, col: 0, max_width: width, max_height: height,
                      inline_input: ->(_input) { " " })
        assert_operator box[:lines].size, :<=, height
        assert box[:lines].all? { |line| A.vislen(line) <= width }
      end
    end
  end
end
