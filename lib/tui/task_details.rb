# frozen_string_literal: true

require "date"
require_relative "ansi"
require_relative "theme"
require_relative "store"
require_relative "../tasks/delegation"
require_relative "../tasks/links"
require_relative "../tasks/recur"
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

    def build(item, notes, width, today: Date.today, temporal_context: nil, links: [], project: nil,
              availability_blocker: nil)
      w = [width, 1].max
      lines = A.wrap(item.title, w).map { |line| T.paint(:section, line) }
      lines << ""
      lines << row("state", STATE_SLOT.key?(item.state) ? T.paint(STATE_SLOT[item.state], item.state) : item.state)
      lines << row("priority", item.priority ? "[##{item.priority}]" : T.paint(:muted, "—"))
      lines << row("deadline", temporal_value(item, :deadline, today, temporal_context)) if item.deadline
      lines << row("available from", temporal_value(item, :scheduled, today, temporal_context)) if item.scheduled
      if item.respond_to?(:availability_reason) && item.availability_reason != :available
        lines << row("availability", availability_value(item, availability_blocker, today, temporal_context))
      end
      lines << row("repeats", recurrence_value(item)) if recurrence_of(item)
      lines << row("closed", item.closed.iso8601) if item.closed
      lines << row("project", T.paint(:project, project)) if project
      contexts = item.contexts
      tags = item.tags - contexts
      lines << row("contexts", contexts.map { |context| T.paint(:context, context) }.join("  ")) unless contexts.empty?
      lines << row("tags", tags.join("  ")) unless tags.empty?
      lines << row("id", T.paint(:muted, item.id)) if item.id
      lines.concat(delegation_lines(item))

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

    # The stored schedule, or nil when the task does not repeat. Guarded like the
    # panel's other optional fields, so a host that supplies a leaner item shape
    # renders without it rather than raising.
    def recurrence_of(item)
      cookie = item.respond_to?(:recur) ? item.recur.to_s.strip : ""
      cookie.empty? ? nil : cookie
    end

    # The schedule as prose next to the list's own ↻ badge — "↻ every Mon, Wed",
    # not "↻ w:mon,wed". The canonical cookie is the machine value; a panel read
    # by a person spells it out. An unparsable stored value humanizes to itself,
    # so nothing disappears from view.
    def recurrence_value(item)
      cookie = recurrence_of(item)
      "#{T.paint(:muted, "↻")} #{Tasks::Recur.humanize(cookie)}"
    end

    # The delegation section: every field of the marker, in the record's own
    # fixed key order, or nothing at all when the task is not delegated. It sits
    # in its own block (like links) because a closed task keeps its delegation
    # as provenance — "who held this and where the work landed" is a distinct
    # question from the task's own fields.
    #
    # `work_ref` is painted with the :link slot but is deliberately NOT part of
    # the `o`-openable link list: that list comes from the task body, and one
    # keypress must keep meaning one thing.
    def delegation_lines(item)
      delegation = item.respond_to?(:delegation) ? item.delegation : nil
      return [] unless Tasks::Delegation.object?(delegation)

      lines = ["", T.paint(:detail_label, "delegation")]
      lines << delegation_row("kind", Views.delegation_text(delegation["kind"]))
      lines << delegation_row("mode", Views.delegation_text(delegation["mode"])) if delegation["mode"]
      lines << delegation_row("status", delegation_status(delegation["status"]))
      if delegation["assignee"]
        lines << delegation_row("assignee", Views.delegation_text(delegation["assignee"]))
      end
      lines << delegation_row("at", T.paint(:muted, Views.delegation_text(delegation["at"]))) if delegation["at"]
      if (reference = delegation["work_ref"])
        lines << delegation_row("work ref", T.paint(:link, Views.delegation_text(reference)))
      end
      lines
    end

    # A claim is the one delegation status where someone else is mid-flight, so
    # it gets the accent slot (bold wherever the theme has no color) while the
    # idle statuses stay muted — the same contrast the list marker uses.
    def delegation_status(status)
      slot = status == Tasks::Delegation::CLAIMED ? :accent : :muted
      T.paint(slot, Views.delegation_text(status))
    end

    # Values arrive already sanitized (Views.delegation_text) and, where they
    # carry a slot, already painted. The explicit close is the second belt: a
    # delegation row must never hand its styling to the row underneath it, and
    # the label's own slot is the theme's to change.
    def delegation_row(label, value)
      A.close("  #{T.paint(:detail_label, label.ljust(8))} #{value}")
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

    def temporal_value(item, field, today, context)
      value = item.respond_to?("#{field}_value") && item.public_send("#{field}_value")
      return date_value(item.public_send(field), today) unless value&.local_time

      date = value.date
      text = "#{date.iso8601} #{value.local_time}"
      text += " #{value.timezone}" if value.fixed?
      text += " floating" if value.floating?
      text += " · later fold" if value.fold == 1
      if context && value.fixed? && value.timezone != context.timezone_id
        projected = value.projected(context)
        text += " → #{projected.fetch(:date).iso8601} #{projected.fetch(:local)} #{context.timezone_id}"
        date = projected.fetch(:date)
      end
      text += " · #{temporal_relative(value, field, context)}" if context
      days = (date - today).to_i
      T.paint(Views.due_slot(days), text)
    end

    def temporal_relative(value, field, context)
      boundary = field == :deadline ? value.due_boundary(context) : value.release_instant(context)
      seconds = boundary - context.now
      return field == :deadline ? "due now" : "available now" if seconds.abs < 60

      duration = compact_duration(seconds.abs)
      if seconds.positive?
        field == :deadline ? "due in #{duration}" : "available in #{duration}"
      elsif field == :deadline
        "overdue by #{duration}"
      else
        "available for #{duration}"
      end
    end

    def compact_duration(seconds)
      minutes = [(seconds / 60).floor, 1].max
      return "#{minutes}m" if minutes < 60
      hours, remainder = minutes.divmod(60)
      remainder.zero? ? "#{hours}h" : "#{hours}h #{remainder}m"
    end

    def availability_value(item, blocker, today, context = nil)
      case item.availability_reason
      when :scheduled
        "unavailable until #{temporal_value(item, :scheduled, today, context)}"
      when :ancestor_scheduled
        suffix = blocker ? " via parent #{blocker.title}" : " via parent"
        blocker&.scheduled ? "unavailable until #{temporal_value(blocker, :scheduled, today, context)}#{suffix}" : "unavailable#{suffix}"
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
