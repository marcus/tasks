# frozen_string_literal: true

require "date"

module Tasks
  # Covey's Important/Urgent 2x2, computed from an Item. The single source of
  # truth for quadrant classification — both the CLI (`bin/tasks quadrants`) and
  # the TUI quadrants view call through here, so they can never disagree.
  #
  # Hybrid model: the axes are derived from the fields you already set, with the
  # :important:/:urgent: tags as explicit overrides.
  #   important = priority A or B, OR the :important: tag
  #   urgent    = a DEADLINE within `urgent_days` (overdue counts), OR the
  #               :urgent: tag. A SCHEDULED start date alone is not urgent.
  module Quadrants
    DEFAULT_URGENT_DAYS = 3

    # Canonical labels, shared so the CLI and TUI never drift. Each frontend
    # applies its own formatting (bold/color) around the text.
    LABELS = {
      "Q1" => "Q1 · Important + Urgent  (do now)",
      "Q2" => "Q2 · Important, Not Urgent  (schedule)",
      "Q3" => "Q3 · Urgent, Not Important  (delegate)",
      "Q4" => "Q4 · Neither  (eliminate)",
    }.freeze

    module_function

    # "Q1".."Q4" for an item.
    def of(item, today: Date.today, urgent_days: DEFAULT_URGENT_DAYS)
      imp = important?(item)
      urg = urgent?(item, today: today, urgent_days: urgent_days)
      imp && urg ? "Q1" : imp ? "Q2" : urg ? "Q3" : "Q4"
    end

    def important?(item)
      item.tags.include?("important") || %w[A B].include?(item.priority)
    end

    def urgent?(item, today: Date.today, urgent_days: DEFAULT_URGENT_DAYS)
      return true if item.tags.include?("urgent")
      d = item.deadline
      !d.nil? && (d - today).to_i <= urgent_days # overdue (negative) counts
    end
  end
end
