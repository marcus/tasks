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

    # Both key contexts, one key column: the list-mode shortcuts, then the
    # modal-mode ones (which also apply to this modal itself).
    def help
      key_w = (Shortcuts::LIST + Shortcuts::MODAL).map { |e| e.keys.length }.max
      lines = Shortcuts::LIST.map { |e| shortcut_line(e, key_w) }
      lines << ""
      lines << T.paint(:section, "in a modal")
      Shortcuts::MODAL.each { |e| lines << shortcut_line(e, key_w) }
      lines << ""
      lines << T.paint(:muted, "prompt/date input: return submits · esc cancels · ctrl-a/e/b/f move")
      { title: "keyboard shortcuts", lines: lines }
    end

    def shortcut_line(entry, key_w)
      "#{T.paint(:accent, entry.keys.ljust(key_w))} #{entry.desc}"
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
    # links: the item's Tasks::Links (Store#links) — shown under the notes;
    #        `o` opens the first one
    def detail(item, block, width, today: Date.today, links: [])
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
      unless links.empty?
        lines << ""
        lines << A.dim("links (o opens the first)")
        lw = links.map { |l| l.system.length }.max
        links.each do |l|
          lines << "  #{A.cyan(l.system.ljust(lw))} #{A.dim(l.url)}"
        end
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
