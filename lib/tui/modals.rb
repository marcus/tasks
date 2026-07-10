# frozen_string_literal: true

require "date"
require_relative "ansi"
require_relative "theme"
require_relative "store"
require_relative "../tasks/links"
require_relative "views"
require_relative "shortcuts"

module Tui
  # Content builders for modal overlays. Pure functions returning
  # { title:, lines: } — the app owns state, Frame owns drawing.
  module Modals
    A = Ansi
    T = Theme

    module_function

    # Generated entirely from the action registry. Shared task actions are
    # repeated in the detail section so their availability is unambiguous.
    def help
      key_w = Shortcuts::REGISTRY.map { |e| e.display_key.length }.max
      groups = [
        ["in the task list", Shortcuts.entries(:list, include_global: false)],
        ["in task details", Shortcuts.entries(:detail, include_global: false)],
        ["in a modal", Shortcuts.entries(:modal, include_global: false)],
        ["everywhere", Shortcuts.entries(:global, include_global: false)],
      ]
      lines = []
      groups.each_with_index do |(title, entries), index|
        lines << "" unless index.zero?
        lines << T.paint(:section, title)
        entries.each { |entry| lines << shortcut_line(entry, key_w) }
      end
      lines << ""
      lines << T.paint(:muted, "prompt/form input: return submits · esc cancels · ctrl-a/e/b/f move")
      { title: "keyboard shortcuts", lines: lines }
    end

    def shortcut_line(entry, key_w)
      "#{T.paint(:accent, entry.display_key.ljust(key_w))} #{entry.description}"
    end

    STATE_SLOT = {
      "NEXT"      => :state_next,
      "WAITING"   => :state_waiting,
      "DONE"      => :state_done,
      "CANCELLED" => :state_done,
    }.freeze

    # A link span inside a note line — org [[url][label]] or a bare URL.
    LINK_SPAN = Regexp.union(Tasks::Links::ORG_LINK, Tasks::Links::BARE_URL)

    # item:  the Views/Store Item
    # notes: the item's prose lines (already filtered) from Store#body
    # links: the item's Tasks::Links (Store#links) — shown under the notes;
    #        `o` opens the first one
    def detail(item, notes, width, today: Date.today, links: [], project: nil)
      w = [width - 12, 64].min
      lines = A.wrap(item.title, w).map { |l| T.paint(:section, l) }
      lines << ""
      lines << row("state", STATE_SLOT.key?(item.state) ? T.paint(STATE_SLOT[item.state], item.state) : item.state)
      lines << row("priority", item.priority ? "[##{item.priority}]" : T.paint(:muted, "—"))
      lines << row("deadline",  date_value(item.deadline, today))  if item.deadline
      lines << row("scheduled", date_value(item.scheduled, today)) if item.scheduled
      lines << row("closed", item.closed.iso8601) if item.closed
      lines << row("project", T.paint(:project, project)) if project
      ctx  = item.contexts
      tags = item.tags - ctx
      lines << row("contexts", ctx.map { |c| T.paint(:context, c) }.join("  ")) unless ctx.empty?
      lines << row("tags", tags.join("  "))    unless tags.empty?
      lines << row("id", T.paint(:muted, item.id)) if item.id

      notes = notes.map(&:strip).reject(&:empty?)
      unless notes.empty?
        lines << ""
        lines << T.paint(:detail_label, "description")
        notes.each { |n| lines.concat(A.wrap(n, w - 2).map { |l| "  #{note_line(l)}" }) }
      end
      unless links.empty?
        lines << ""
        lines << T.paint(:detail_label, "links") + T.paint(:muted, " (o opens the first)")
        lw = links.map { |l| l.system.length }.max
        links.each do |l|
          lines << "  #{T.paint(:link_system, l.system.ljust(lw))} #{T.paint(:link, l.url)}"
        end
      end
      { title: "task", lines: lines }
    end

    def row(label, value)
      "#{T.paint(:detail_label, label.ljust(10))} #{value}"
    end

    # Paint a note line, giving link spans the :link slot and the rest
    # :description.
    # Segments are painted separately (never nested) so a reset inside one
    # span can't bleed the styling of the next.
    def note_line(line)
      out = +""
      last = 0
      line.scan(LINK_SPAN) do
        m = Regexp.last_match
        out << T.paint(:description, line[last...m.begin(0)]) if m.begin(0) > last
        out << T.paint(:link, m[0])
        last = m.end(0)
      end
      out << T.paint(:description, line[last..]) if last < line.length
      out
    end

    def date_value(date, today)
      days = (date - today).to_i
      rel = days.negative? ? "#{-days}d ago" : days.zero? ? "today" : "in #{days}d"
      T.paint(Views.due_slot(days), "#{date.iso8601} #{date.strftime("%a")} · #{rel}")
    end
  end
end
