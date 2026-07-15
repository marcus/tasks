# frozen_string_literal: true

require "date"
require_relative "ansi"
require_relative "theme"
require_relative "store"
require_relative "../tasks/links"
require_relative "views"

module Tui
  # Pure task-detail content builder. Its output can be hosted by a right panel
  # today and by future task-editing surfaces without depending on modal state.
  module TaskDetails
    A = Ansi
    T = Theme

    module_function

    STATE_SLOT = {
      "NEXT"      => :state_next,
      "WAITING"   => :state_waiting,
      "DONE"      => :state_done,
      "CANCELLED" => :state_done,
    }.freeze

    LINK_SPAN = Regexp.union(Tasks::Links::ORG_LINK, Tasks::Links::BARE_URL)

    def build(item, notes, width, today: Date.today, links: [], project: nil,
              availability_blocker: nil)
      w = [width, 1].max
      lines = A.wrap(item.title, w).map { |line| T.paint(:section, line) }
      lines << ""
      lines << row("state", STATE_SLOT.key?(item.state) ? T.paint(STATE_SLOT[item.state], item.state) : item.state)
      lines << row("priority", item.priority ? "[##{item.priority}]" : T.paint(:muted, "—"))
      lines << row("deadline", date_value(item.deadline, today)) if item.deadline
      lines << row("available from", date_value(item.scheduled, today)) if item.scheduled
      if item.respond_to?(:availability_reason) && item.availability_reason != :available
        lines << row("availability", availability_value(item, availability_blocker, today))
      end
      lines << row("closed", item.closed.iso8601) if item.closed
      lines << row("project", T.paint(:project, project)) if project
      contexts = item.contexts
      tags = item.tags - contexts
      lines << row("contexts", contexts.map { |context| T.paint(:context, context) }.join("  ")) unless contexts.empty?
      lines << row("tags", tags.join("  ")) unless tags.empty?
      lines << row("id", T.paint(:muted, item.id)) if item.id

      notes = notes.map(&:strip).reject(&:empty?)
      unless notes.empty?
        lines << ""
        lines << T.paint(:detail_label, "description")
        notes.each { |note| lines.concat(A.wrap(note, [w - 2, 1].max).map { |line| "  #{note_line(line)}" }) }
      end
      unless links.empty?
        lines << ""
        lines << T.paint(:detail_label, "links") + T.paint(:muted, " (o opens the first)")
        system_width = links.map { |link| link.system.length }.max
        links.each do |link|
          lines << "  #{T.paint(:link_system, link.system.ljust(system_width))} #{T.paint(:link, link.url)}"
        end
      end
      { title: "task", lines: lines }
    end

    def row(label, value)
      "#{T.paint(:detail_label, label.ljust(10))} #{value}"
    end

    def note_line(line)
      out = +""
      last = 0
      line.scan(LINK_SPAN) do
        match = Regexp.last_match
        out << T.paint(:description, line[last...match.begin(0)]) if match.begin(0) > last
        out << T.paint(:link, match[0])
        last = match.end(0)
      end
      out << T.paint(:description, line[last..]) if last < line.length
      out
    end

    def date_value(date, today)
      days = (date - today).to_i
      relative = days.negative? ? "#{-days}d ago" : days.zero? ? "today" : "in #{days}d"
      T.paint(Views.due_slot(days), "#{date.iso8601} #{date.strftime("%a")} · #{relative}")
    end

    def availability_value(item, blocker, today)
      case item.availability_reason
      when :scheduled
        "unavailable until #{date_value(item.scheduled, today)}"
      when :ancestor_scheduled
        date = blocker&.scheduled
        suffix = blocker ? " via parent #{blocker.title}" : " via parent"
        date ? "unavailable until #{date_value(date, today)}#{suffix}" : "unavailable#{suffix}"
      when :on_hold
        "on hold"
      when :ancestor_on_hold
        blocker ? "on hold via parent #{blocker.title}" : "on hold via parent"
      when :closed
        "closed"
      else
        "available now"
      end
    end
  end
end
