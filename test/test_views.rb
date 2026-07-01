# frozen_string_literal: true

require_relative "test_helper"

class TestViews < Minitest::Test
  V = Tui::Views
  A = Tui::Ansi
  TODAY = Date.new(2026, 7, 1)

  def rows(view)
    with_store { |store, _o, _a| return V.rows(view, store.items, today: TODAY) }
  end

  def texts(rs) = rs.map { |r| A.strip(r.text) }

  def test_agenda_sorted_soonest_first_and_selectable
    rs = rows(:agenda)
    assert_equal 2, rs.size # flight (deadline) + self-eval (scheduled)
    assert_includes rs[0].text, "Book flight"
    assert_includes rs[0].text, "DUE"
    assert_includes rs[1].text, "self-eval"
    assert_includes rs[1].text, "STRT"
    assert rs.all?(&:item), "agenda rows are all selectable"
  end

  def test_next_groups_by_context_with_unselectable_headers
    rs = rows(:next)
    headers = rs.reject(&:item).map { |r| A.strip(r.text) }.reject(&:empty?)
    assert_equal ["@computer", "@home"], headers
    flight_row = rs.find { |r| r.text.include?("Book flight") }
    assert flight_row.item
    assert_includes A.strip(flight_row.text), "7/2"
  end

  def test_quadrants_places_items_by_tags
    rs = texts(rows(:quadrants))
    q1 = rs.index { |t| t.start_with?("Q1") }
    q2 = rs.index { |t| t.start_with?("Q2") }
    q3 = rs.index { |t| t.start_with?("Q3") }
    q4 = rs.index { |t| t.start_with?("Q4") }
    assert rs[q1...q2].any? { |t| t.include?("Book flight") }        # important+urgent
    assert rs[q2...q3].any? { |t| t.include?("Review PR backlog") }  # important only
    assert rs[q3...q4].any? { |t| t.include?("Travel desk") }        # urgent only
    assert rs[q4..].any? { |t| t.include?("Water the plants") }      # neither
  end

  def test_inbox_lists_inbox_items
    rs = rows(:inbox)
    assert_equal 1, rs.size
    assert_includes rs[0].text, "garden"
    assert rs[0].item
  end

  def test_inbox_empty_state
    items = []
    rs = V.rows(:inbox, items, today: TODAY)
    assert_equal 1, rs.size
    assert_nil rs[0].item
    assert_includes A.strip(rs[0].text), "Inbox empty"
  end

  def test_done_items_never_appear_in_open_views
    %i[agenda next quadrants inbox].each do |view|
      refute texts(rows(view)).any? { |t| t.include?("Old finished thing") },
             "DONE item leaked into #{view}"
    end
  end
end
