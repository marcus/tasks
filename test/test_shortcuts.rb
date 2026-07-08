# frozen_string_literal: true

require_relative "test_helper"
require "tui/shortcuts"
require "tui/modals"
require "tui/app"

class TestShortcuts < Minitest::Test
  S = Tui::Shortcuts
  A = Tui::Ansi

  def test_every_action_is_an_app_method
    (S::LIST + S::MODAL).each do |e|
      assert Tui::App.private_method_defined?(e.action),
             "shortcut #{e.keys.inspect} points at missing App##{e.action}"
    end
  end

  def test_sequences_are_unique_within_each_context
    [S::LIST, S::MODAL].each do |list|
      seqs = list.flat_map(&:seqs)
      assert_equal seqs.uniq, seqs, "duplicate key sequences"
    end
  end

  def test_find_modal_resolves_modal_sequences
    assert_equal :modal_half_down, S.find_modal("\x04").action
    assert_equal :modal_half_up, S.find_modal("\x15").action
    assert_equal :modal_page_down, S.find_modal("\x06").action
    assert_equal :modal_page_down, S.find_modal("\e[6~").action
    assert_equal :modal_page_up, S.find_modal("\x02").action
    assert_equal :modal_start_filter, S.find_modal("/").action
    assert_equal :close_modal, S.find_modal("\e").action
    assert_equal :close_modal, S.find_modal("q").action
    assert_nil S.find_modal("c"), "task actions are App fallthrough, not modal entries"
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

  def test_help_modal_lists_every_shortcut_in_both_contexts
    help = Tui::Modals.help
    text = help[:lines].map { |l| A.strip(l) }.join("\n")
    (S::LIST + S::MODAL).each do |e|
      assert_includes text, e.desc
      assert_includes text, e.keys
    end
    assert_includes text, "in a modal"
    assert_equal "keyboard shortcuts", help[:title]
  end
end
