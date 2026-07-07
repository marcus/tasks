# frozen_string_literal: true

require "date"
require_relative "ansi"
require_relative "theme"
require_relative "store"
require_relative "../tasks/quadrants"

module Tui
  # Builds the rows for each tab. A Row with an item is selectable
  # (actions apply to it); a Row with item: nil is a header/blank.
  module Views
    class Row
      attr_reader :text, :item, :segments

      def initialize(text = nil, item = nil, segments: nil)
        @item = item
        @segments = segments
        @text = text || self.class.render_segments(segments)
      end

      def self.segment(text, slot = nil) = [text, slot]

      def self.render_segments(segments, selected: false)
        segments.map do |text, slot|
          if selected
            slot = Theme.selected_slot(slot) if slot
            Theme.paint_over(:selection, slot, text)
          elsif slot
            Theme.paint(slot, text)
          else
            text
          end
        end.join
      end

      def selected_text
        if segments
          self.class.render_segments([["▸ ", nil], *segments], selected: true)
        else
          Theme.paint_over(:selection, nil, "▸ " + Ansi.strip(text))
        end
      end
    end

    A = Ansi
    T = Theme
    S = Row.method(:segment)

    TABS = [
      ["1 Agenda",    :agenda],
      ["2 Next",      :next],
      ["3 Quadrants", :quadrants],
      ["4 Inbox",     :inbox],
      ["5 Projects",  :projects],
    ].freeze

    module_function

    def rows(view, items, today: Date.today, urgent_days: Tasks::Quadrants::DEFAULT_URGENT_DAYS, store: nil)
      case view
      when :agenda    then agenda(items, today: today, store: store)
      when :next      then next_actions(items, today: today, store: store)
      when :quadrants then quadrants(items, today: today, urgent_days: urgent_days, store: store)
      when :inbox     then inbox(items)
      when :projects  then projects(items, today: today, store: store)
      else []
      end
    end

    def agenda(items, today: Date.today, store: nil)
      dated = items.select { |i| i.open? && (i.scheduled || i.deadline) }
      # same date → priority order (A first, none last)
      dated.sort_by { |i| [i.deadline || i.scheduled, i.priority || "Z"] }.map do |i|
        d    = i.deadline || i.scheduled
        kind = i.deadline ? "DUE " : "STRT"
        days = (d - today).to_i
        when_s = days.negative? ? "#{-days}d ago" : days.zero? ? "today" : "in #{days}d"
        stamp = "#{d.strftime("%m-%d")} #{kind} #{("(" + when_s + ")").ljust(8)}"
        Row.new(nil, i, segments: [
          S.call(stamp, due_slot(days)),
          S.call(" "),
          *decorated_segments(i, store: store),
          *badge_segments(i),
        ])
      end
    end

    def next_actions(items, today: Date.today, store: nil)
      by_ctx = Hash.new { |h, k| h[k] = [] }
      items.select { |i| i.state == "NEXT" }.each do |i|
        ctxs = i.contexts
        (ctxs.empty? ? ["(no context)"] : ctxs).each { |c| by_ctx[c] << i }
      end
      rows = []
      by_ctx.sort.each do |ctx, list|
        rows << Row.new(T.paint(:context, ctx), nil)
        list.sort_by { |i| i.priority || "Z" }.each do |i|
          rows << task_row(i, today: today, store: store, indent: "  ", show_due: true)
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    # Classification (importance/urgency) lives in Tasks::Quadrants so the CLI
    # and TUI agree; this just lays the four buckets out as rows.
    def quadrants(items, today: Date.today, urgent_days: Tasks::Quadrants::DEFAULT_URGENT_DAYS, store: nil)
      open_items = items.select(&:open?)
      by_q = open_items.group_by { |i| Tasks::Quadrants.of(i, today: today, urgent_days: urgent_days) }
      rows = []
      Tasks::Quadrants::LABELS.each do |key, label|
        rows << Row.new(T.paint(:section, label), nil)
        matched = by_q[key] || []
        if matched.empty?
          rows << Row.new(T.paint(:muted, "  —"), nil)
        else
          matched.each { |i| rows << task_row(i, today: today, store: store, indent: "  ") }
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    def inbox(items)
      inbox = items.select { |i| i.state == "INBOX" }
      return [Row.new(T.paint(:muted, "Inbox empty. ✨"), nil)] if inbox.empty?
      inbox.map { |i| task_row(i, indent: "  ") }
    end

    def projects(items, today: Date.today, store: nil)
      return [Row.new(T.paint(:muted, "Project data needs the task tree."), nil)] unless store

      groups = Hash.new { |h, k| h[k] = [] }
      items.select(&:open?).each do |item|
        project = project_name(item, store)
        next if project == "Inbox"
        groups[project] << item if project
      end
      return [Row.new(T.paint(:muted, "No active projects."), nil)] if groups.empty?

      project_rows = groups.sort_by { |name, list| [next_project_date(list) || Date.new(9999, 12, 31), name] }
                           .flat_map do |name, list|
        open = list.size
        nexts = list.count { |i| i.state == "NEXT" }
        upcoming = next_project_date(list)
        header = [
          S.call(name, :project),
          S.call("  "),
          S.call("#{open} open", :muted),
          S.call(" · #{nexts} next", nexts.zero? ? :warning : :muted),
        ]
        header << S.call(" · next #{upcoming.strftime("%m-%d")}", due_slot((upcoming - today).to_i)) if upcoming
        rows = [Row.new(nil, nil, segments: header)]
        list.sort_by { |i| [i.deadline || i.scheduled || Date.new(9999, 12, 31), i.priority || "Z", i.title] }
            .each { |i| rows << task_row(i, today: today, store: nil, indent: "  ", show_due: true, show_project: false) }
        rows << Row.new("", nil)
        rows
      end
      project_rows.pop
      project_rows
    end

    # -- shared bits ---------------------------------------------------------

    # The urgency ladder as theme slots; Modals reuses it for dates.
    def due_slot(days)
      if    days <= 0 then :due_overdue
      elsif days <= 2 then :due_soon
      elsif days <= 7 then :due_week
      else                 :due_far
      end
    end

    def task_row(item, today: nil, store: nil, indent: "", show_due: false, show_project: true)
      segments = [S.call(indent)]
      segments.concat(decorated_segments(item, store: store, show_project: show_project))
      if show_due && item.deadline && today
        days = (item.deadline - today).to_i
        segments << S.call("  ")
        segments << S.call("#{item.deadline.month}/#{item.deadline.day}", due_slot(days))
      end
      Row.new(nil, item, segments: segments.concat(badge_segments(item)))
    end

    def decorated_segments(item, store: nil, show_project: true)
      ctx = item.contexts
      project = show_project ? project_name(item, store) : nil
      [].tap do |segments|
        segments << S.call("[#{item.priority}] ", :priority) if item.priority
        segments << S.call(item.title, :title)
        segments << S.call("  #{project}", :project) if project
        ctx.each { |c| segments << S.call("  #{c}", :context) }
      end
    end

    def badge_segments(item)
      [].tap do |segments|
        segments << S.call(" ↻", :muted) if item.recurring?
        segments << S.call(" ⏸", :muted) if item.deferred?
      end
    end

    def project_name(item, store)
      return nil unless store
      store.node_for(item)&.project&.title
    end

    def next_project_date(items)
      items.filter_map { |i| i.deadline || i.scheduled }.min
    end
  end
end
