# frozen_string_literal: true

require_relative "test_helper"
require "tui/theme"
require "tui/modals"
require "tui/task_details"

# Tui::Theme — spec parsing, slot painting, named themes, config overrides.
class TestTheme < Minitest::Test
  T = Tui::Theme

  def teardown
    T.reset!
  end

  # -- spec parsing ------------------------------------------------------------

  def test_parses_named_colors_and_attributes
    assert_equal [36], T.parse("cyan")
    assert_equal [1, 36], T.parse("bold cyan")
    assert_equal [91], T.parse("bright-red")
    assert_equal [90], T.parse("gray")
    assert_equal [7], T.parse("reverse")
  end

  def test_parses_backgrounds_256_and_hex
    assert_equal [44], T.parse("on-blue")
    assert_equal [100], T.parse("on-gray")
    assert_equal ["38;5;208"], T.parse("208")
    assert_equal ["48;5;17"], T.parse("on-17")
    assert_equal ["38;2;255;136;0"], T.parse("#ff8800")
    assert_equal ["48;2;30;32;48"], T.parse("on-#1e2030")
    assert_equal [1, "38;2;255;136;0", 44], T.parse("bold #ff8800 on-blue")
  end

  def test_none_means_unstyled_and_invalid_specs_are_rejected_whole
    assert_equal [], T.parse("none")
    assert_nil T.parse("chartreuse")
    assert_nil T.parse("bold chartreuse") # one bad token poisons the spec
    assert_nil T.parse("300")             # out of 256-color range
    assert_nil T.parse("#ff88")           # short hex
    assert_nil T.parse("")
    assert_nil T.parse("on-bold")         # attributes have no background form
  end

  # -- painting ----------------------------------------------------------------

  def test_default_theme_matches_stock_look
    assert_equal "\e[36mx\e[0m", T.paint(:accent, "x")
    assert_equal "\e[7mx\e[0m", T.paint(:selection, "x")
    assert_equal "\e[90mx\e[0m", T.paint(:muted, "x")
    assert_equal "\e[1;7mx\e[0m", T.paint(:tab_active, "x")
    assert_equal "\e[36mx\e[0m", T.paint(:tab_agenda, "x")
    assert_equal "\e[1;90mx\e[0m", T.paint(:detail_label, "x")
  end

  def test_unknown_slot_passes_through
    assert_equal "x", T.paint(:nonexistent, "x")
  end

  # -- configure! ---------------------------------------------------------------

  def test_overrides_restyle_a_slot
    T.configure!(overrides: { "accent" => "magenta", "tab_projects_active" => "black on-yellow" })
    assert_equal "\e[35mx\e[0m", T.paint(:accent, "x")
    assert_equal "\e[30;43mx\e[0m", T.paint(:tab_projects_active, "x")
    assert_equal "\e[90mx\e[0m", T.paint(:muted, "x") # untouched slots keep defaults
  end

  def test_invalid_override_falls_back_to_default
    T.configure!(overrides: { "accent" => "chartreuse" })
    assert_equal "\e[36mx\e[0m", T.paint(:accent, "x")
  end

  def test_unknown_override_slot_is_ignored
    T.configure!(overrides: { "sparkle" => "red" })
    assert_equal "\e[36mx\e[0m", T.paint(:accent, "x")
  end

  def test_none_override_disables_styling
    T.configure!(overrides: { "muted" => "none" })
    assert_equal "x", T.paint(:muted, "x")
  end

  def test_mono_theme_uses_no_color_codes
    T.configure!(name: "mono")
    T::DEFAULTS.each_key do |slot|
      codes = T.current[slot].join(";")
      refute_match(/3[0-9]|4[0-8]|9[0-7]/, codes, "mono #{slot} should carry no color")
    end
    assert_equal "\e[2mx\e[0m", T.paint(:muted, "x")
    assert_equal "\e[4mx\e[0m", T.paint(:link, "x")
  end

  def test_unknown_theme_name_falls_back_to_default
    T.configure!(name: "solarized-post-punk")
    assert_equal "\e[36mx\e[0m", T.paint(:accent, "x")
  end

  def test_overrides_apply_on_top_of_named_theme
    T.configure!(name: "mono", overrides: { "accent" => "underline" })
    assert_equal "\e[4mx\e[0m", T.paint(:accent, "x")
    assert_equal "\e[2mx\e[0m", T.paint(:muted, "x")
  end

  # -- composite_over ----------------------------------------------------------

  def test_composite_over_unset_slot_is_noop
    T.configure!(overrides: { "outline_container" => "none" })
    styled = Tui::Ansi.bold("row")
    assert_equal styled, T.composite_over(:outline_container, styled)
  end

  def test_composite_over_wraps_and_closes_with_one_reset
    T.configure! # outline_container defaults to bold
    # a line with its own field color, closed by its own reset
    line = "a" + Tui::Ansi.color("b", 36) + "c" # "a\e[36mb\e[0mc"
    out = T.composite_over(:outline_container, line)
    # opens bold, re-injects bold after the field's reset, closes once
    assert_equal "\e[1ma\e[36mb\e[0m\e[1mc\e[0m", out
    assert out.end_with?("\e[0m"), "closes with a trailing reset"
    # exactly one reset beyond the line's own embedded field reset
    assert_equal 2, out.scan("\e[0m").length
  end

  def test_popular_generated_themes_are_available
    %w[
      catppuccin-mocha
      dracula
      gruvbox-dark
      nord
      solarized-dark
      tokyonight-night
    ].each { |name| assert_includes T::THEMES.keys, name }
  end

  def test_generated_theme_specs_parse
    T::THEMES.each do |name, slots|
      slots.each do |slot, spec|
        assert T::DEFAULTS.key?(slot), "#{name} defines unknown slot #{slot}"
        assert T.parse(spec), "#{name}.#{slot} has invalid spec #{spec.inspect}"
      end
    end
  end

  def test_generated_theme_can_be_configured_by_name
    T.configure!(name: "dracula")
    assert_equal "\e[38;2;164;255;255mx\e[0m", T.paint(:accent, "x")
    assert_includes T.paint(:selection, "x"), "\e[38;2;"
    assert_includes T.paint(:selection, "x"), ";48;2;"
  end

  # -- link painting in task details --------------------------------------------

  def test_note_line_paints_links_and_prose_separately
    line = "see https://github.com/a/b for details"
    out = Tui::TaskDetails.note_line(line)
    assert_includes out, "\e[4;36mhttps://github.com/a/b\e[0m"
    assert_includes out, "\e[90msee \e[0m"
    assert_includes out, "\e[90m for details\e[0m"
  end

  def test_note_line_paints_org_links_whole
    out = Tui::TaskDetails.note_line("[[https://x.dev][the doc]] rest")
    assert_includes out, "\e[4;36m[[https://x.dev][the doc]]\e[0m"
  end

  def test_note_line_without_links_is_all_note_styled
    assert_equal "\e[90mjust prose\e[0m", Tui::TaskDetails.note_line("just prose")
  end
end
