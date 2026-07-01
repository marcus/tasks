# frozen_string_literal: true

require "date"
require_relative "ansi"
require_relative "store"

module Tui
  # Builds the rows for each tab. A Row with an item is selectable
  # (actions apply to it); a Row with item: nil is a header/blank.
  module Views
    Row = Struct.new(:text, :item)

    A = Ansi

    TABS = [
      ["1 Agenda",    :agenda],
      ["2 Next",      :next],
      ["3 Quadrants", :quadrants],
      ["4 Inbox",     :inbox],
    ].freeze

    module_function

    def rows(view, items, today: Date.today)
      case view
      when :agenda    then agenda(items, today: today)
      when :next      then next_actions(items, today: today)
      when :quadrants then quadrants(items)
      when :inbox     then inbox(items)
      else []
      end
    end

    def agenda(items, today: Date.today)
      dated = items.select { |i| i.open? && (i.scheduled || i.deadline) }
      dated.sort_by { |i| i.deadline || i.scheduled }.map do |i|
        d    = i.deadline || i.scheduled
        kind = i.deadline ? "DUE " : "STRT"
        days = (d - today).to_i
        when_s = days.negative? ? "#{-days}d ago" : days.zero? ? "today" : "in #{days}d"
        stamp  = A.color("#{d.strftime("%m-%d")} #{kind} #{("(" + when_s + ")").ljust(8)}", due_color(days))
        Row.new("#{stamp} #{decorated_title(i)}", i)
      end
    end

    def next_actions(items, today: Date.today)
      by_ctx = Hash.new { |h, k| h[k] = [] }
      items.select { |i| i.state == "NEXT" }.each do |i|
        ctxs = i.contexts
        (ctxs.empty? ? ["(no context)"] : ctxs).each { |c| by_ctx[c] << i }
      end
      rows = []
      by_ctx.sort.each do |ctx, list|
        rows << Row.new(A.bold(A.cyan(ctx)), nil)
        list.sort_by { |i| i.priority || "Z" }.each do |i|
          due = short_due(i, today)
          rows << Row.new("  #{pri(i)}#{i.title}#{due.empty? ? "" : "  #{due}"}", i)
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    QUADRANTS = {
      "Q1 · Important + Urgent  (do now)"      => ->(i) { i.tags.include?("important") && i.tags.include?("urgent") },
      "Q2 · Important, Not Urgent  (schedule)" => ->(i) { i.tags.include?("important") && !i.tags.include?("urgent") },
      "Q3 · Urgent, Not Important  (delegate)" => ->(i) { !i.tags.include?("important") && i.tags.include?("urgent") },
      "Q4 · Neither  (eliminate)"              => ->(i) { !i.tags.include?("important") && !i.tags.include?("urgent") },
    }.freeze

    def quadrants(items)
      open_items = items.select(&:open?)
      rows = []
      QUADRANTS.each do |label, test|
        rows << Row.new(A.bold(label), nil)
        matched = open_items.select(&test)
        if matched.empty?
          rows << Row.new(A.dim("  —"), nil)
        else
          matched.each { |i| rows << Row.new("  #{pri(i)}#{i.title}", i) }
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    def inbox(items)
      inbox = items.select { |i| i.state == "INBOX" }
      return [Row.new(A.dim("Inbox empty. ✨"), nil)] if inbox.empty?
      inbox.map { |i| Row.new("  #{i.title}", i) }
    end

    # -- shared bits ---------------------------------------------------------

    def due_color(days)
      if    days <= 0 then 31
      elsif days <= 2 then 33
      elsif days <= 7 then 36
      else                 90
      end
    end

    def short_due(item, today)
      return "" unless item.deadline
      days = (item.deadline - today).to_i
      A.color("#{item.deadline.month}/#{item.deadline.day}", due_color(days))
    end

    def pri(item) = item.priority ? A.bold("[#{item.priority}] ") : ""

    def decorated_title(item)
      ctx = item.contexts
      "#{pri(item)}#{item.title}#{ctx.empty? ? "" : A.dim("  " + ctx.join(" "))}"
    end
  end
end
