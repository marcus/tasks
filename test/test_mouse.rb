# frozen_string_literal: true

require_relative "test_helper"
require "tui/mouse"

class TestMouse < Minitest::Test
  M = Tui::Mouse

  def test_left_press_and_release
    press = M.decode("\e[<0;5;7M")
    assert_equal :left, press.button
    assert_equal :press, press.action
    assert_equal 4, press.col
    assert_equal 6, press.row
    refute press.shift
    refute press.alt
    refute press.ctrl

    release = M.decode("\e[<0;5;7m")
    assert_equal :left, release.button
    assert_equal :release, release.action
  end

  def test_middle_and_right_buttons
    assert_equal :middle, M.decode("\e[<1;1;1M").button
    assert_equal :right, M.decode("\e[<2;1;1M").button
  end

  def test_wheel_directions
    assert_equal :wheel_up, M.decode("\e[<64;10;10M").button
    assert_equal :wheel_down, M.decode("\e[<65;10;10M").button
    assert_equal :wheel_left, M.decode("\e[<66;10;10M").button
    assert_equal :wheel_right, M.decode("\e[<67;10;10M").button
    assert M.decode("\e[<64;10;10M").wheel?
  end

  def test_modifier_bits
    ev = M.decode("\e[<28;3;4M") # left + shift(4) + alt(8) + ctrl(16)
    assert_equal :left, ev.button
    assert ev.shift
    assert ev.alt
    assert ev.ctrl
  end

  def test_motion_bit
    ev = M.decode("\e[<32;3;4M")
    assert_equal :motion, ev.action
    assert_equal :left, ev.button
  end

  def test_extra_buttons
    assert_equal :button8, M.decode("\e[<128;1;1M").button
    assert_equal :button9, M.decode("\e[<129;1;1M").button
  end

  def test_coordinates_past_223_round_trip
    ev = M.decode("\e[<0;300;400M")
    assert_equal 299, ev.col
    assert_equal 399, ev.row
  end

  def test_one_based_to_zero_based
    ev = M.decode("\e[<0;1;1M")
    assert_equal 0, ev.col
    assert_equal 0, ev.row
  end

  def test_malformed_returns_nil
    assert_nil M.decode("\e[<0;1M")
    assert_nil M.decode("\e[<0;1;2X")
    assert_nil M.decode("")
    assert_nil M.decode(nil)
    assert_nil M.decode("\e[<abc;1;2M")
    assert_nil M.decode("\e[<0;0;1M") # col 0 → -1 after conversion
  end

  def test_enable_disable_sequences
    assert_equal "\e[?1000h\e[?1006h", M::ENABLE
    assert_equal "\e[?1006l\e[?1000l", M::DISABLE
  end

  def test_sequence_regex_matches_sgr_reports
    assert_match M::SEQUENCE, "\e[<0;12;34M"
    assert_match M::SEQUENCE, "\e[<65;1;1m"
    refute_match M::SEQUENCE, "\e[A"
  end
end
