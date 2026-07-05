# frozen_string_literal: true

require_relative "test_helper"
require "tui/shortcuts"
require "tui/modals"
require "tui/app"

class TestShortcuts < Minitest::Test
  S = Tui::Shortcuts
  A = Tui::Ansi

  def test_every_action_is_an_app_method
    S::LIST.each do |e|
      assert Tui::App.private_method_defined?(e.action),
             "shortcut #{e.keys.inspect} points at missing App##{e.action}"
    end
  end

  def test_sequences_are_unique
    seqs = S::LIST.flat_map(&:seqs)
    assert_equal seqs.uniq, seqs, "duplicate key sequences in Shortcuts::LIST"
  end

  def test_find_resolves_sequences
    assert_equal :complete_selected, S.find("c").action
    assert_equal :select_prev, S.find("\e[A").action
    assert_equal :select_prev, S.find("k").action
    assert_equal :prev_view, S.find("\e[D").action
    assert_equal :next_view, S.find("\e[C").action
    assert_equal :open_detail, S.find("\r").action
    assert_equal :open_help, S.find("?").action
    assert_equal :quit, S.find("q").action
    assert_equal :defer_selected, S.find("z").action
    assert_equal :toggle_deferred_view, S.find("Z").action
    assert_equal :open_recur_popup, S.find("r").action
    assert_nil S.find("Q")
  end

  def test_help_modal_lists_every_shortcut
    help = Tui::Modals.help
    text = help[:lines].map { |l| A.strip(l) }.join("\n")
    S::LIST.each do |e|
      assert_includes text, e.desc
      assert_includes text, e.keys
    end
    assert_equal "keyboard shortcuts", help[:title]
  end
end
