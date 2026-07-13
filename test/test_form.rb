# frozen_string_literal: true

require_relative "test_helper"
require "tui/form"

class TestForm < Minitest::Test
  A = Tui::Ansi

  def form(initial: +"", &submit)
    Tui::Form.new(
      kind: :example, title: "example", prompt: "value", hint: "enter a value",
      min_width: 32, return_mode: :modal, initial: initial, &submit
    )
  end

  def test_edit_paste_and_unicode_are_owned_by_one_input
    f = form { nil }
    assert_equal :changed, f.handle_key("界")
    assert_equal :changed, f.paste("🙂e\u0301\nnext")
    assert_equal "界🙂e\u0301 next", f.input.to_s
    assert f.input.text.valid_encoding?
  end

  def test_validation_error_stays_until_content_changes
    f = form { |raw| raw == "ok" ? nil : "not valid" }
    assert_equal :error, f.handle_key("\r")
    assert_equal "not valid", f.error
    assert_equal :handled, f.handle_key("\x02")
    assert_equal "not valid", f.error
    assert_equal :changed, f.handle_key("o")
    assert_nil f.error
  end

  def test_submit_cancel_and_callback_errors_have_deterministic_results
    assert_equal :cancelled, form { nil }.handle_key("\e")

    submitted = nil
    f = form(initial: "ready") { |raw| submitted = raw; nil }
    assert_equal :submitted, f.handle_key("\n")
    assert_equal "ready", submitted

    raised = form { raise "write failed" }
    assert_equal :error, raised.handle_key("\r")
    assert_equal "write failed", raised.error
  end

  def test_popup_renders_shared_prompt_hint_error_and_return_mode
    f = form(initial: "界") { "bad value" }
    f.submit
    popup = f.popup(row: 4, col: 8, inline_input: ->(input) { input.to_s })
    text = popup[:lines].map { |line| A.strip(line) }.join("\n")
    assert_equal :modal, f.return_mode
    assert_equal 4, popup[:row]
    assert_equal 8, popup[:col]
    assert_equal 0, popup[:focused_content_row]
    assert_includes text, "example"
    assert_includes text, "›! value"
    assert_includes text, "value: 界"
    assert_includes text, "bad value"
  end

  def test_popup_adapts_to_every_narrow_body_rectangle
    f = form(initial: "界") { nil }
    (1..12).each do |width|
      (1..4).each do |height|
        popup = f.popup(row: 0, col: 0, max_width: width, max_height: height,
                        inline_input: ->(_input) { A.invert(" ") })
        assert_operator popup[:lines].size, :<=, height
        assert popup[:lines].all? { |line| A.vislen(line) <= width },
               "#{width}x#{height}: #{popup[:lines].inspect}"
        if width >= 6 && height >= 3
          assert A.strip(popup[:lines].first).start_with?("┌")
          assert A.strip(popup[:lines].last).end_with?("┘")
        end
      end
    end
  end

  def test_empty_form_is_labeled_at_every_positive_height
    f = form { nil }
    (1..6).each do |height|
      popup = f.popup(row: 0, col: 0, max_width: 12, max_height: height,
                      inline_input: ->(_input) { A.invert(" ") })
      text = popup[:lines].map { |line| A.strip(line) }.join("\n")
      assert_match(/value|example/, text, "empty form unlabeled at height #{height}")
      assert_operator popup[:lines].size, :<=, height
      assert popup[:lines].all? { |line| A.vislen(line) <= 12 }
    end
  end

  def test_one_row_form_uses_available_cells_for_prompt_label
    f = form { nil }
    (1..5).each do |width|
      popup = f.popup(row: 0, col: 0, max_width: width, max_height: 1,
                      inline_input: ->(_input) { A.invert(" ") })
      assert_equal "value"[0, width], A.strip(popup[:lines].first)
      assert_equal width, A.vislen(popup[:lines].first)
    end
  end

  def test_zero_width_or_height_popup_budget_emits_no_lines
    f = form(initial: "value") { nil }
    assert_empty f.popup(
      row: 0, col: 0, max_width: 0, max_height: 4,
      inline_input: ->(input) { input.to_s },
    )[:lines]
    assert_empty f.popup(
      row: 0, col: 0, max_width: 8, max_height: 0,
      inline_input: ->(input) { input.to_s },
    )[:lines]
  end
end
