# frozen_string_literal: true

require "date"
require_relative "ansi"
require_relative "store"
require_relative "views"
require_relative "shortcuts"

module Tui
  # Content builders for modal overlays. Pure functions returning
  # { title:, lines: } — the app owns state, Frame owns drawing.
  module Modals
    A = Ansi

    module_function

    def help
      lines = Shortcuts::LIST.map do |e|
        "#{A.cyan(e.keys.ljust(9))} #{e.desc}"
      end
      lines << ""
      lines << A.dim("prompt/date input: return submits · esc cancels · ctrl-a/e/b/f move")
      lines << A.dim("in this modal:     ↑↓ scroll · esc closes")
      { title: "keyboard shortcuts", lines: lines }
    end

    STATE_STYLE = {
      "NEXT"      => ->(s) { A.cyan(s) },
      "WAITING"   => ->(s) { A.yellow(s) },
      "DONE"      => ->(s) { A.dim(s) },
      "CANCELLED" => ->(s) { A.dim(s) },
    }.freeze

    # item:  the Views/Store Item
    # block: raw file lines of the item (headline + body) from Store#block
    # links: the item's Tasks::Links (Store#links) — shown under the notes;
    #        `o` opens the first one
    def detail(item, block, width, today: Date.today, links: [])
      w = [width - 12, 64].min
      lines = A.wrap(item.title, w).map { |l| A.bold(l) }
      lines << ""
      lines << row("state", (STATE_STYLE[item.state] || ->(s) { s }).call(item.state))
      lines << row("priority", item.priority ? "[##{item.priority}]" : A.dim("—"))
      lines << row("deadline",  date_value(item.deadline, today))  if item.deadline
      lines << row("scheduled", date_value(item.scheduled, today)) if item.scheduled
      ctx  = item.contexts
      tags = item.tags - ctx
      lines << row("contexts", ctx.join("  ")) unless ctx.empty?
      lines << row("tags", tags.join("  "))    unless tags.empty?
      lines << row("id", A.dim(item.id)) if item.id

      notes = Tasks::Store.strip_drawer(block).drop(1)
                          .reject { |l| l =~ /^\s*(SCHEDULED|DEADLINE):/ }
                          .map(&:strip).reject(&:empty?)
      unless notes.empty?
        lines << ""
        lines << A.dim("notes")
        notes.each { |n| lines.concat(A.wrap(n, w - 2).map { |l| A.dim("  #{l}") }) }
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
      "#{A.dim(label.ljust(10))} #{value}"
    end

    def date_value(date, today)
      days = (date - today).to_i
      rel = days.negative? ? "#{-days}d ago" : days.zero? ? "today" : "in #{days}d"
      A.color("#{date.iso8601} #{date.strftime("%a")} · #{rel}", Views.due_color(days))
    end
  end
end
