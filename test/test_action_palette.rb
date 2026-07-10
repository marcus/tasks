# frozen_string_literal: true

require_relative "test_helper"
require "tui/action_palette"
require "tui/shortcuts"

class TestActionPalette < Minitest::Test
  A = Tui::Ansi

  def entries
    Tui::Shortcuts::REGISTRY.values_at(10, 14, 25, 26)
  end

  def palette
    Tui::ActionPalette.new(entries: entries, return_mode: :modal)
  end

  def test_filtering_is_registry_ordered_and_searches_description_key_and_handler
    p = palette
    p.paste("selected")
    assert_equal %i[complete_selected defer_selected], p.results.map(&:handler)
    p.input.replace("ctrl-r")
    assert_equal [:redo_last], p.results.map(&:handler)
    p.input.replace("focus_prompt")
    assert_equal [:focus_prompt], p.results.map(&:handler)
  end

  def test_navigation_clamps_and_filter_reset_is_deterministic
    p = palette
    p.handle_key("\e[B")
    assert_equal 1, p.selected
    20.times { p.handle_key("\e[B") }
    assert_equal entries.size - 1, p.selected
    p.handle_key("\e[A")
    assert_equal entries.size - 2, p.selected
    p.handle_key("c")
    assert_equal 0, p.selected
  end

  def test_enter_escape_empty_results_and_unicode_paste
    p = palette
    event = p.handle_key("\r")
    assert_equal :execute, event.first
    assert_same entries.first, event.last
    assert_equal :cancelled, p.handle_key("\e")

    p.paste("🦄界")
    assert_empty p.results
    assert_equal :handled, p.handle_key("\r")
    assert p.input.text.valid_encoding?
  end

  def test_error_and_small_resize_render_without_losing_state
    p = palette
    p.paste("complete")
    p.fail!("action failed")
    popup = p.popup(row: 2, cols: 40, inline_input: ->(input) { input.to_s })
    text = popup[:lines].map { |line| A.strip(line) }.join("\n")
    assert_equal :modal, p.return_mode
    assert_includes text, "complete"
    assert_includes text, "action failed"
    assert popup[:lines].all? { |line| A.vislen(line) <= 34 }
  end
end
