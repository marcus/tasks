# frozen_string_literal: true

require_relative "test_helper"
require "tui/right_panel"

class TestRightPanel < Minitest::Test
  A = Tui::Ansi

  def panel(identity: "one")
    Tui::RightPanel.new(
      title: "task", lines: (1..20).map { |index| "line #{index}" },
      kind: :detail, identity: identity
    )
  end

  def test_view_respects_height_and_width_and_reports_overflow
    view = panel.view(height: 8, width: 9)
    assert_equal "task", view[:title]
    assert_operator view[:lines].size, :<=, 6
    assert view[:lines].all? { |line| A.vislen(line) <= 9 }
    assert_match(%r{\d+/20}, A.strip(view[:lines].last))
  end

  def test_replacing_same_identity_preserves_scroll
    subject = panel
    subject.scroll_page(1, 8)
    before = subject.scroll
    subject.replace(title: "updated", lines: (1..30).map(&:to_s), identity: "one")
    assert_equal before, subject.scroll
  end

  def test_replacing_new_identity_resets_scroll
    subject = panel
    subject.scroll_page(1, 8)
    assert_operator subject.scroll, :>, 0
    subject.replace(title: "task", lines: ["new"], identity: "two")
    assert_equal 0, subject.scroll
    assert_equal "two", subject.identity
  end

  def test_tiny_height_returns_no_content_rows
    assert_empty panel.view(height: 1, width: 1)[:lines]
    assert_empty panel.view(height: 2, width: 1)[:lines]
  end


  def test_focused_row_is_revealed_without_replacing_editor_scroll_state
    subject = panel
    subject.replace(title: "editing", lines: (1..30).map(&:to_s), focused_row: 19)
    view = subject.view(height: 8, width: 9)
    assert_operator subject.scroll, :>, 0
    assert_includes view[:lines].map { |line| A.strip(line) }, "20"

    subject.replace(title: "editing", lines: (1..30).map(&:to_s), focused_row: 1)
    subject.view(height: 8, width: 9)
    assert_equal 1, subject.scroll
  end
end
