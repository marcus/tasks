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

  def test_agenda_same_date_sorts_by_priority
    org = <<~ORG
      * Work
      ** NEXT [#B] beta task tomorrow
         DEADLINE: <2026-07-02>
      ** NEXT [#A] alpha task tomorrow
         DEADLINE: <2026-07-02>
      ** NEXT no-priority task tomorrow
         DEADLINE: <2026-07-02>
      ** NEXT [#C] later but urgent-ish
         DEADLINE: <2026-07-05>
    ORG
    Dir.mktmpdir do |dir|
      path = File.join(dir, "gtd.org")
      File.write(path, org)
      store = Tui::Store.new(org: path, archive: File.join(dir, "archive.org"))
      titles = V.agenda(store.items, today: TODAY).map { |r| A.strip(r.text) }
      assert_operator titles.index { |t| t.include?("alpha") }, :<, titles.index { |t| t.include?("beta") }
      assert_operator titles.index { |t| t.include?("beta") }, :<, titles.index { |t| t.include?("no-priority") }
      # date still dominates: C-priority on a later date sorts below all of them
      assert_equal 3, titles.index { |t| t.include?("later but") }
    end
  end

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

  # Hybrid model keeps tagged fixture items where they were: the :important:/
  # :urgent: tags force their axes, and the fixture's A/B priorities line up.
  def test_quadrants_places_fixture_items
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

  # The point of the hybrid model: priority + deadline place a task with no
  # important/urgent tags at all.
  def test_quadrants_derived_from_priority_and_deadline
    org = <<~ORG
      * Work
      ** NEXT [#A] alpha no date
      ** NEXT [#B] beta near deadline
         DEADLINE: <2026-07-02>
      ** NEXT [#C] gamma near deadline
         DEADLINE: <2026-07-02>
      ** NEXT delta far deadline
         DEADLINE: <2026-07-20>
      ** TODO [#A] epsilon scheduled only
         SCHEDULED: <2026-07-02>
    ORG
    Dir.mktmpdir do |dir|
      path = File.join(dir, "gtd.org")
      File.write(path, org)
      store = Tui::Store.new(org: path, archive: File.join(dir, "archive.org"))
      rs = texts(V.rows(:quadrants, store.items, today: TODAY, urgent_days: 3))
      q1 = rs.index { |t| t.start_with?("Q1") }
      q2 = rs.index { |t| t.start_with?("Q2") }
      q3 = rs.index { |t| t.start_with?("Q3") }
      q4 = rs.index { |t| t.start_with?("Q4") }
      assert rs[q1...q2].any? { |t| t.include?("beta") },    "B + near deadline → Q1"
      assert rs[q2...q3].any? { |t| t.include?("alpha") },   "A, no date → Q2"
      assert rs[q2...q3].any? { |t| t.include?("epsilon") }, "scheduled-only is not urgent → Q2"
      assert rs[q3...q4].any? { |t| t.include?("gamma") },   "C + near deadline → Q3"
      assert rs[q4..].any?   { |t| t.include?("delta") },    "far deadline → Q4"
    end
  end

  # A wider urgent_days window pulls a far-out deadline into the urgent column.
  def test_quadrants_urgent_days_widens_window
    org = <<~ORG
      * Work
      ** NEXT [#A] far deadline task
         DEADLINE: <2026-07-20>
    ORG
    Dir.mktmpdir do |dir|
      path = File.join(dir, "gtd.org")
      File.write(path, org)
      store = Tui::Store.new(org: path, archive: File.join(dir, "archive.org"))
      default = texts(V.rows(:quadrants, store.items, today: TODAY))
      q2 = default.index { |t| t.start_with?("Q2") }
      q3 = default.index { |t| t.start_with?("Q3") }
      assert default[q2...q3].any? { |t| t.include?("far deadline") }, "default 3d → Q2"

      wide = texts(V.rows(:quadrants, store.items, today: TODAY, urgent_days: 30))
      w1 = wide.index { |t| t.start_with?("Q1") }
      w2 = wide.index { |t| t.start_with?("Q2") }
      assert wide[w1...w2].any? { |t| t.include?("far deadline") }, "30d window → Q1"
    end
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
