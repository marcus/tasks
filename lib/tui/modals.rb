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

    def help
      lines = Shortcuts::LIST.map do |e|
        "#{T.paint(:accent, e.keys.ljust(9))} #{e.desc}"
      end
      lines << ""
      lines << T.paint(:muted, "prompt/date input: return submits · esc cancels · ctrl-a/e/b/f move")
      lines << T.paint(:muted, "in this modal:     ↑↓ scroll · esc closes")
      { title: "keyboard shortcuts", lines: lines }
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
    # block: raw file lines of the item (headline + body) from Store#block
    def detail(item, block, width, today: Date.today)
      w = [width - 12, 64].min
      lines = A.wrap(item.title, w).map { |l| T.paint(:section, l) }
      lines << ""
      lines << row("state", STATE_SLOT.key?(item.state) ? T.paint(STATE_SLOT[item.state], item.state) : item.state)
      lines << row("priority", item.priority ? "[##{item.priority}]" : T.paint(:muted, "—"))
      lines << row("deadline",  date_value(item.deadline, today))  if item.deadline
      lines << row("scheduled", date_value(item.scheduled, today)) if item.scheduled
      ctx  = item.contexts
      tags = item.tags - ctx
      lines << row("contexts", ctx.join("  ")) unless ctx.empty?
      lines << row("tags", tags.join("  "))    unless tags.empty?
      lines << row("id", T.paint(:muted, item.id)) if item.id

      notes = Tasks::Store.strip_drawer(block).drop(1)
                          .reject { |l| l =~ /^\s*(SCHEDULED|DEADLINE):/ }
                          .map(&:strip).reject(&:empty?)
      unless notes.empty?
        lines << ""
        lines << T.paint(:note, "notes")
        notes.each { |n| lines.concat(A.wrap(n, w - 2).map { |l| "  #{note_line(l)}" }) }
      end
      { title: "task", lines: lines }
    end

    def row(label, value)
      "#{T.paint(:muted, label.ljust(10))} #{value}"
    end

    # Paint a note line, giving link spans the :link slot and the rest :note.
    # Segments are painted separately (never nested) so a reset inside one
    # span can't bleed the styling of the next.
    def note_line(line)
      out = +""
      last = 0
      line.scan(LINK_SPAN) do
        m = Regexp.last_match
        out << T.paint(:note, line[last...m.begin(0)]) if m.begin(0) > last
        out << T.paint(:link, m[0])
        last = m.end(0)
      end
      out << T.paint(:note, line[last..]) if last < line.length
      out
    end

    def date_value(date, today)
      days = (date - today).to_i
      rel = days.negative? ? "#{-days}d ago" : days.zero? ? "today" : "in #{days}d"
      T.paint(Views.due_slot(days), "#{date.iso8601} #{date.strftime("%a")} · #{rel}")
    end
  end
end
