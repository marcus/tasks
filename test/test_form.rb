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
    assert_includes text, "example"
    assert_includes text, "value: 界"
    assert_includes text, "bad value"
  end
end
