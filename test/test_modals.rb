# frozen_string_literal: true

require_relative "test_helper"
require "tui/modals"

class TestModals < Minitest::Test
  M = Tui::Modals
  A = Tui::Ansi
  TODAY = Date.new(2026, 7, 1)

  def detail_for(text)
    with_store do |store, _o, _a|
      item = find_item(store, text)
      return M.detail(item, store.body(item), 100, today: TODAY)
    end
  end

  def texts(modal) = modal[:lines].map { |l| A.strip(l) }

  def test_detail_shows_core_fields
    lines = texts(detail_for("Book flight"))
    assert_includes lines.first, "Book flight in Concur"
    assert lines.any? { |l| l =~ /state\s+NEXT/ }
    assert lines.any? { |l| l =~ /priority\s+\[#A\]/ }
    assert lines.any? { |l| l =~ /deadline\s+2026-07-02 Thu · in 1d/ }
    assert lines.any? { |l| l =~ /contexts\s+@computer/ }
    assert lines.any? { |l| l =~ /tags\s+important\s+urgent/ }
  end

  def test_detail_scheduled_item_has_no_deadline_row
    lines = texts(detail_for("self-eval"))
    assert lines.any? { |l| l =~ /scheduled\s+2026-07-03 Fri · in 2d/ }
    refute lines.any? { |l| l.start_with?("deadline") }
  end

  def test_detail_includes_notes_but_not_stamps
    lines = texts(detail_for("Travel desk"))
    assert lines.any? { |l| l.include?("Some note line.") }
    refute lines.any? { |l| l.include?("SCHEDULED:") }
  end

  def test_detail_shows_closed_row_when_present
    lines = texts(detail_for("Old finished thing"))
    assert lines.any? { |l| l =~ /closed\s+2026-06-20/ }
  end

  def test_detail_open_item_has_no_closed_row
    lines = texts(detail_for("Book flight"))
    refute lines.any? { |l| l.start_with?("closed") }
  end

  def test_detail_item_without_extras_is_minimal
    lines = texts(detail_for("Water the plants"))
    refute lines.any? { |l| l.start_with?("deadline") }
    refute lines.any? { |l| l.include?("notes") }
    assert lines.any? { |l| l =~ /contexts\s+@home/ }
  end

  def test_detail_wraps_long_titles
    with_store do |store, org, _a|
      File.write(org, "* X\n** TODO #{"very long title word " * 10}:@computer:\n")
      store.reload!
      item = store.items.first
      modal = M.detail(item, store.body(item), 60, today: TODAY)
      title_lines = modal[:lines].take_while { |l| !A.strip(l).empty? }
      assert title_lines.size > 1, "long title should wrap to multiple lines"
    end
  end
end
