# frozen_string_literal: true

require_relative "test_helper"
require "tasks/quadrants"

# Unit tests for the shared classifier. The CLI and TUI both route through
# Tasks::Quadrants, so the axis rules are pinned here once.
class TestQuadrants < Minitest::Test
  Q = Tasks::Quadrants
  TODAY = Date.new(2026, 7, 1)

  def item(priority: nil, tags: [], deadline: nil, scheduled: nil)
    Tasks::Item.new(state: "NEXT", priority: priority, title: "t", tags: tags,
                    scheduled: scheduled, deadline: deadline, line: 1, source: :org)
  end

  # -- importance ------------------------------------------------------------

  def test_priority_a_or_b_is_important
    assert Q.important?(item(priority: "A"))
    assert Q.important?(item(priority: "B"))
  end

  def test_priority_c_or_none_is_not_important
    refute Q.important?(item(priority: "C"))
    refute Q.important?(item(priority: nil))
  end

  def test_important_tag_overrides_low_priority
    assert Q.important?(item(priority: "C", tags: %w[important]))
    assert Q.important?(item(priority: nil, tags: %w[important]))
  end

  # -- urgency ---------------------------------------------------------------

  def test_deadline_within_window_is_urgent
    assert Q.urgent?(item(deadline: TODAY + 3), today: TODAY, urgent_days: 3)  # == window
    assert Q.urgent?(item(deadline: TODAY),     today: TODAY, urgent_days: 3)  # today
  end

  def test_overdue_deadline_is_urgent
    assert Q.urgent?(item(deadline: TODAY - 5), today: TODAY, urgent_days: 3)
  end

  def test_deadline_past_window_is_not_urgent
    refute Q.urgent?(item(deadline: TODAY + 4), today: TODAY, urgent_days: 3)  # window + 1
  end

  def test_scheduled_alone_is_not_urgent
    refute Q.urgent?(item(scheduled: TODAY + 1), today: TODAY, urgent_days: 3)
  end

  def test_no_dates_is_not_urgent
    refute Q.urgent?(item, today: TODAY, urgent_days: 3)
  end

  def test_urgent_tag_overrides_absent_deadline
    assert Q.urgent?(item(tags: %w[urgent]), today: TODAY, urgent_days: 3)
  end

  def test_urgent_days_is_configurable
    far = item(deadline: TODAY + 20)
    refute Q.urgent?(far, today: TODAY, urgent_days: 3)
    assert Q.urgent?(far, today: TODAY, urgent_days: 30)
  end

  # -- combined quadrant -----------------------------------------------------

  def test_of_covers_all_four_quadrants
    q1 = item(priority: "A", deadline: TODAY + 1)
    q2 = item(priority: "A")
    q3 = item(deadline: TODAY + 1)
    q4 = item
    assert_equal "Q1", Q.of(q1, today: TODAY, urgent_days: 3)
    assert_equal "Q2", Q.of(q2, today: TODAY, urgent_days: 3)
    assert_equal "Q3", Q.of(q3, today: TODAY, urgent_days: 3)
    assert_equal "Q4", Q.of(q4, today: TODAY, urgent_days: 3)
  end

  def test_default_urgent_days_constant
    assert_equal 3, Q::DEFAULT_URGENT_DAYS
  end

  def test_labels_cover_q1_through_q4
    assert_equal %w[Q1 Q2 Q3 Q4], Q::LABELS.keys
  end
end
