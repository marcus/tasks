# frozen_string_literal: true

require_relative "test_helper"
require "tui/text_input"

class TestTextInput < Minitest::Test
  def test_emacs_movement_and_insertion
    input = Tui::TextInput.new("abcd")
    input.handle_key("\x02") # ctrl-b
    input.handle_key("\x02")
    input.handle_key("X")
    assert_equal "abXcd", input.text

    input.handle_key("\x01") # ctrl-a
    input.handle_key(">")
    input.handle_key("\x05") # ctrl-e
    input.handle_key("<")
    assert_equal ">abXcd<", input.text
  end

  def test_delete_backspace_and_kill_shortcuts
    input = Tui::TextInput.new("alpha beta gamma")
    6.times { input.handle_key("\x02") } # before " gamma"
    input.handle_key("\x0b") # ctrl-k
    assert_equal "alpha beta", input.text

    input.handle_key("\x17") # ctrl-w
    assert_equal "alpha ", input.text

    input.handle_key("\x15") # ctrl-u
    assert_equal "", input.text
  end

  def test_forward_delete_and_arrows
    input = Tui::TextInput.new("abc")
    input.handle_key("\x01")
    input.handle_key("\x04") # ctrl-d
    assert_equal "bc", input.text

    input.handle_key("\e[C")
    input.handle_key("X")
    assert_equal "bXc", input.text

    input.handle_key("\e[3~")
    assert_equal "bX", input.text
  end

  def test_modified_arrows_move_by_word
    input = Tui::TextInput.new("alpha beta gamma")
    input.handle_key("\x01")
    input.handle_key("\e[1;5C")
    input.handle_key("X")
    assert_equal "alpha Xbeta gamma", input.text

    input.handle_key("\e[1;5D")
    input.handle_key("Y")
    assert_equal "alpha YXbeta gamma", input.text
  end

  def test_handle_key_distinguishes_changed_from_handled
    input = Tui::TextInput.new("a")
    assert_equal :handled, input.handle_key("\x02")
    assert_equal :handled, input.handle_key("\x02")
    assert_equal :changed, input.handle_key("b")
    assert_equal :changed, input.handle_key("")
    assert_nil input.handle_key("\x00")
  end

  def test_modified_home_end_sequences_are_handled
    input = Tui::TextInput.new("abc")
    assert_equal :handled, input.handle_key("\e[1;5H")
    input.handle_key(">")
    assert_equal ">abc", input.text
    assert_equal :handled, input.handle_key("\e[1;5F")
    input.handle_key("<")
    assert_equal ">abc<", input.text
  end

  def test_paste_sanitizes_line_breaks_without_submitting
    input = Tui::TextInput.new
    input.insert("first\nsecond\thttps://example.com")
    assert_equal "first second https://example.com", input.text
  end

  def test_unicode_cursor_moves_by_grapheme
    input = Tui::TextInput.new("aé🙂b")
    input.handle_key("\x02")
    input.handle_key("X")
    assert_equal "aé🙂Xb", input.text
  end
end
