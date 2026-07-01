#!/usr/bin/env ruby
# frozen_string_literal: true

# proto/frame.rb — static frame renders of the proposed tasks TUI.
# No interactivity; just draws one snapshot so we can judge the layout.
#
#   ruby proto/frame.rb            frame with Agenda tab active
#   ruby proto/frame.rb 2          Next tab (1=agenda 2=next 3=quadrants 4=inbox)
#   ruby proto/frame.rb --date     the reschedule popup over the selected task
#   ruby proto/frame.rb --claude   prompt mid-conversation with Claude's reply
#
# Stdlib only, same as bin/tasks.

require "date"
require "io/console"

ROOT = File.expand_path("..", __dir__)
ORG  = File.join(ROOT, "gtd.org")

Item = Struct.new(:state, :priority, :title, :tags, :scheduled, :deadline, keyword_init: true)

HEADLINE = /^\*+\s+(INBOX|TODO|NEXT|WAITING|DONE|CANCELLED)\s+(?:\[#([ABC])\]\s+)?(.*?)\s*(:[\w@:]+:)?\s*$/
STAMP    = /(SCHEDULED|DEADLINE):\s*<(\d{4}-\d{2}-\d{2})/
OPEN_STATES = %w[INBOX TODO NEXT WAITING].freeze

def parse
  items = []
  current = nil
  File.foreach(ORG, encoding: "UTF-8") do |line|
    if (m = line.match(HEADLINE))
      current = Item.new(state: m[1], priority: m[2], title: m[3].strip,
                         tags: (m[4] || "").split(":").reject(&:empty?))
      items << current
    elsif current && (s = line.match(STAMP))
      d = Date.parse(s[2])
      current.scheduled = d if s[1] == "SCHEDULED"
      current.deadline  = d if s[1] == "DEADLINE"
    end
  end
  items
end

# ---- ANSI helpers ------------------------------------------------------------
def c(str, *codes) = "\e[#{codes.join(";")}m#{str}\e[0m"
def bold(s)    = c(s, 1)
def dim(s)     = c(s, 90)
def red(s)     = c(s, 31)
def yellow(s)  = c(s, 33)
def cyan(s)    = c(s, 36)
def invert(s)  = c(s, 7)
def strip_ansi(s) = s.gsub(/\e\[[0-9;]*m/, "")
def vislen(s)     = strip_ansi(s).length

# Pad a string (which may contain ANSI codes) to visible width w.
def vpad(s, w)
  pad = w - vislen(s)
  pad.positive? ? s + " " * pad : s
end

# Truncate to visible width, keeping ANSI reset sane (crude but fine for a proto).
def vtrunc(s, w)
  return s if vislen(s) <= w
  out = +""
  count = 0
  s.scan(/\e\[[0-9;]*m|./m) do |tok|
    if tok.start_with?("\e[")
      out << tok
    else
      break if count >= w - 1
      out << tok
      count += 1
    end
  end
  out << "\e[0m" << dim("…")
end

def due_color(days)
  if    days <= 0 then 31
  elsif days <= 2 then 33
  elsif days <= 7 then 36
  else                 90
  end
end

# ---- row formatting ----------------------------------------------------------
def agenda_rows(items)
  dated = items.select { |i| OPEN_STATES.include?(i.state) && (i.scheduled || i.deadline) }
  dated.sort_by { |i| i.deadline || i.scheduled }.map do |i|
    d    = i.deadline || i.scheduled
    kind = i.deadline ? "DUE " : "STRT"
    days = (d - Date.today).to_i
    when_s = days.negative? ? "#{-days}d ago" : days.zero? ? "today" : "in #{days}d"
    stamp  = c("#{d.strftime("%m-%d")} #{kind} #{("(" + when_s + ")").ljust(8)}", due_color(days))
    pri    = i.priority ? bold("[#{i.priority}] ") : ""
    ctx    = i.tags.select { |t| t.start_with?("@") }
    "#{stamp} #{pri}#{i.title}#{ctx.empty? ? "" : dim("  " + ctx.join(" "))}"
  end
end

def next_rows(items)
  rows = []
  by_ctx = Hash.new { |h, k| h[k] = [] }
  items.select { |i| i.state == "NEXT" }.each do |i|
    ctxs = i.tags.select { |t| t.start_with?("@") }
    (ctxs.empty? ? ["(no context)"] : ctxs).each { |ctx| by_ctx[ctx] << i }
  end
  by_ctx.sort.each do |ctx, list|
    rows << bold(cyan(ctx))
    list.sort_by { |i| i.priority || "Z" }.each do |i|
      pri = i.priority ? bold("[#{i.priority}] ") : ""
      due = i.deadline ? "  " + c("#{i.deadline.month}/#{i.deadline.day}", due_color((i.deadline - Date.today).to_i)) : ""
      rows << "  #{pri}#{i.title}#{due}"
    end
    rows << ""
  end
  rows.pop
  rows
end

def quadrant_rows(items)
  open_items = items.select { |i| OPEN_STATES.include?(i.state) }
  quads = {
    "Q1 · Important + Urgent  (do now)"      => ->(i) { i.tags.include?("important") && i.tags.include?("urgent") },
    "Q2 · Important, Not Urgent  (schedule)" => ->(i) { i.tags.include?("important") && !i.tags.include?("urgent") },
    "Q3 · Urgent, Not Important  (delegate)" => ->(i) { !i.tags.include?("important") && i.tags.include?("urgent") },
    "Q4 · Neither  (eliminate)"              => ->(i) { !i.tags.include?("important") && !i.tags.include?("urgent") },
  }
  rows = []
  quads.each do |label, test|
    rows << bold(label)
    matched = open_items.select(&test)
    matched.empty? ? rows << dim("  —") : matched.each do |i|
      pri = i.priority ? bold("[#{i.priority}] ") : ""
      rows << "  #{pri}#{i.title}"
    end
    rows << ""
  end
  rows.pop
  rows
end

def inbox_rows(items)
  inbox = items.select { |i| i.state == "INBOX" }
  inbox.empty? ? [dim("Inbox empty. ✨")] : inbox.map { |i| "  #{i.title}" }
end

# ---- frame -------------------------------------------------------------------
TABS = [
  ["1 Agenda",    :agenda],
  ["2 Next",      :next],
  ["3 Quadrants", :quadrants],
  ["4 Inbox",     :inbox],
].freeze

def render(items, active:, selected: 0, popup: false, claude: false)
  rows_h, cols = IO.console&.winsize || [24, 80]
  cols = [cols, 100].min          # cap so it stays readable on very wide terminals
  w = cols - 2                    # interior width
  body_h = rows_h - 7             # header(2) + divider + keybar + prompt + border(2)

  open_count = items.count { |i| OPEN_STATES.include?(i.state) }

  # header
  tab_bar = TABS.map.with_index do |(label, key), idx|
    key == active ? invert(bold(" #{label} ")) : dim(" #{label} ")
  end.join(" ")
  count_s = dim("#{open_count} open")
  header = " #{tab_bar}#{" " * [w - vislen(tab_bar) - vislen(count_s) - 2, 1].max}#{count_s} "

  # body rows for the active view
  rows = case active
         when :agenda    then agenda_rows(items)
         when :next      then next_rows(items)
         when :quadrants then quadrant_rows(items)
         when :inbox     then inbox_rows(items)
         end

  body = rows.first(body_h).map.with_index do |r, idx|
    marker = idx == selected && active == :agenda ? cyan("▸ ") : "  "
    line = vtrunc("#{marker}#{r}", w - 2)
    idx == selected && active == :agenda ? c(strip_ansi("▸ " + strip_ansi(r)), 7) : line
  end
  body.fill("", body.size...body_h)

  # overlay the reschedule popup on rows 2-6
  if popup
    pw = 34
    plines = [
      "┌ reschedule ─" + "─" * (pw - 15) + "┐",
      "│ #{vpad("new date: " + bold("fri") + "▏", pw - 4)} │",
      "│ #{vpad(dim("fri · +3 · 07-15 · esc cancels"), pw - 4)} │",
      "└" + "─" * (pw - 2) + "┘",
    ]
    plines.each_with_index do |pl, i|
      row = 2 + i
      base = vpad(body[row] || "", w - 2)
      lead = strip_ansi(base)[0, 8]
      body[row] = "#{lead}#{pl}"
    end
  end

  keybar = dim(" ↑↓ select · c complete · d date · e edit · 1-4 views · q quit")
  prompt =
    if claude
      cyan(" ✓ ") + "Moved “Book Denver trip flight” to 07-03 (Fri). Anything else?"
    else
      " " + bold(cyan("❯ ")) + dim("ask claude — reschedule, capture, edit anything…")
    end

  out = +""
  out << "┌" << "─" * w << "┐\n"
  out << "│" << vpad(header, w) << "│\n"
  out << "├" << "─" * w << "┤\n"
  body.each { |r| out << "│ " << vpad(r, w - 2) << " │\n" }
  out << "├" << "─" * w << "┤\n"
  out << "│" << vpad(keybar, w) << "│\n"
  out << "│" << vpad(prompt, w) << "│\n"
  out << "└" << "─" * w << "┘\n"
  out
end

# ---- main --------------------------------------------------------------------
items = parse
active = :agenda
popup = ARGV.delete("--date")
claude = ARGV.delete("--claude")
active = TABS[ARGV[0].to_i - 1]&.last || :agenda if ARGV[0]

puts render(items, active: active, selected: 0, popup: !!popup, claude: !!claude)
