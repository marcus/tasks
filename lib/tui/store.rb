# frozen_string_literal: true

require "date"

module Tui
  Item = Struct.new(
    :state, :priority, :title, :tags, :scheduled, :deadline, :line, :source,
    keyword_init: true
  ) do
    def open?    = Store::OPEN_STATES.include?(state)
    def contexts = tags.select { |t| t.start_with?("@") }
  end

  # Owns gtd.org: parsing, change detection, and the mutations the TUI
  # performs directly (complete, reschedule, archive sweep). Claude edits
  # the file out-of-band; `changed?` picks those up.
  class Store
    HEADLINE = /^\*+\s+(INBOX|TODO|NEXT|WAITING|DONE|CANCELLED)\s+(?:\[#([ABC])\]\s+)?(.*?)\s*(:[\w@:]+:)?\s*$/
    STAMP    = /(SCHEDULED|DEADLINE):\s*<(\d{4}-\d{2}-\d{2})/

    OPEN_STATES = %w[INBOX TODO NEXT WAITING].freeze
    DONE_STATES = %w[DONE CANCELLED].freeze

    attr_reader :org, :archive

    UNDO_LIMIT = 50 # in-memory only; history does not survive a restart

    def initialize(org:, archive:)
      @org = org
      @archive = archive
      @mtime = nil
      @cache = nil
      @undo_stack = []
      @redo_stack = []
    end

    def items
      reload! if @cache.nil? || changed?
      @cache
    end

    def changed?
      File.mtime(@org) != @mtime
    rescue Errno::ENOENT
      false
    end

    def reload!
      @mtime = File.mtime(@org)
      @cache = parse
      self
    end

    # Raw file lines of an item: its headline plus body, up to the next
    # same-or-higher-level headline. Empty array on stale line numbers.
    def block(item)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return [] unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)
      level = lines[i][/^\*+/].length
      out = [lines[i].chomp]
      j = i + 1
      while j < lines.length
        lvl = lines[j][/^(\*+)\s/, 1]&.length
        break if lvl && lvl <= level
        out << lines[j].chomp
        j += 1
      end
      out
    end

    # -- undo/redo -------------------------------------------------------------
    #
    # Every TUI mutation snapshots both files before and after the write.
    # undo!/redo! restore snapshots, but only when the current file content
    # matches what the mutation left behind — an out-of-band edit (Claude,
    # another process) makes the entry unsafe and it is refused, not forced.

    # Returns [:ok, label] | [:empty] | [:conflict, label]
    def undo!
      entry = @undo_stack.last
      return [:empty] unless entry
      return [:conflict, entry[:label]] unless snapshot == entry[:after]
      @undo_stack.pop
      restore(entry[:before])
      @redo_stack << entry
      [:ok, entry[:label]]
    end

    def redo!
      entry = @redo_stack.last
      return [:empty] unless entry
      return [:conflict, entry[:label]] unless snapshot == entry[:before]
      @redo_stack.pop
      restore(entry[:after])
      @undo_stack << entry
      [:ok, entry[:label]]
    end

    # -- mutations ---------------------------------------------------------------

    # Mark an item DONE in place. Returns true, or false if the file shifted
    # under us (stale line number) — caller should reload and retry.
    def complete!(item)
      with_history("complete: #{item.title}") { complete_impl(item) }
    end

    def set_priority!(item, pri)
      label = pri ? "priority [##{pri}]: #{item.title}" : "clear priority: #{item.title}"
      with_history(label) { set_priority_impl(item, pri) }
    end

    def reschedule!(item, date)
      with_history("reschedule → #{date.iso8601}: #{item.title}") { reschedule_impl(item, date) }
    end

    def archive_swept!
      with_history("archive sweep") { archive_swept_impl }
    end

    private

    def snapshot
      {
        org: File.read(@org, encoding: "UTF-8"),
        archive: File.exist?(@archive) ? File.read(@archive, encoding: "UTF-8") : nil,
      }
    end

    def restore(snap)
      File.write(@org, snap[:org])
      if snap[:archive].nil?
        File.delete(@archive) if File.exist?(@archive)
      else
        File.write(@archive, snap[:archive])
      end
      reload!
    end

    # Record history only when the mutation actually wrote (truthy, nonzero).
    def with_history(label)
      before = snapshot
      result = yield
      if result && result != 0
        @undo_stack << { label: label, before: before, after: snapshot }
        @undo_stack.shift while @undo_stack.size > UNDO_LIMIT
        @redo_stack.clear
      end
      result
    end

    def complete_impl(item)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)
      lines[i] = lines[i].sub(/^(\*+\s+)(INBOX|TODO|NEXT|WAITING)\b/, '\1DONE')
      lines.insert(i + 1, "   CLOSED: [#{Date.today}]\n")
      File.write(@org, lines.join)
      reload!
      true
    end

    # Set the [#A]/[#B]/[#C] cookie on the headline, or remove it (pri: nil).
    # Same staleness contract as complete!.
    def set_priority_impl(item, pri)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)
      cookie = pri ? "[##{pri}] " : ""
      lines[i] = lines[i].sub(/^(\*+\s+#{item.state}\s+)(?:\[#[ABC]\]\s+)?/, "\\1#{cookie}")
      File.write(@org, lines.join)
      reload!
      true
    end

    # Update the item's DEADLINE (or SCHEDULED, if that's all it has) to
    # `date`. Items with neither get a DEADLINE added. Same staleness
    # contract as complete!.
    def reschedule_impl(item, date)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)

      # update the deadline if there is one; else the scheduled date;
      # items with neither get a deadline
      key   = if item.deadline     then "DEADLINE"
              elsif item.scheduled then "SCHEDULED"
              else                      "DEADLINE"
              end
      stamp = "#{key}: <#{date.iso8601} #{date.strftime("%a")}>"
      level = lines[i][/^\*+/].length

      j = i + 1
      stamp_at = nil
      while j < lines.length
        lvl = lines[j][/^(\*+)\s/, 1]&.length
        break if lvl && lvl <= level
        stamp_at = j if lines[j].include?("#{key}:")
        j += 1
      end

      if stamp_at
        lines[stamp_at] = lines[stamp_at].sub(/#{key}:\s*<[^>]*>/, stamp)
      else
        lines.insert(i + 1, "   #{stamp}\n")
      end
      # A dated task has been processed by definition — promote it out of
      # the inbox (see docs/conventions.md).
      lines[i] = lines[i].sub(/^(\*+\s+)INBOX\b/, '\1TODO') if item.state == "INBOX"
      File.write(@org, lines.join)
      reload!
      true
    end

    # Move all DONE/CANCELLED blocks to the archive file. Returns the count.
    def archive_swept_impl
      lines = File.readlines(@org, encoding: "UTF-8")
      kept  = []
      moved = []
      i = 0
      while i < lines.length
        st = lines[i][/^\*+\s+(INBOX|TODO|NEXT|WAITING|DONE|CANCELLED)\b/, 1]
        if st && DONE_STATES.include?(st)
          level = lines[i][/^\*+/].length
          block = [lines[i]]
          j = i + 1
          while j < lines.length
            lvl = lines[j][/^(\*+)\s/, 1]&.length
            break if lvl && lvl <= level
            block << lines[j]
            j += 1
          end
          moved << block.join.rstrip + "\n"
          i = j
        else
          kept << lines[i]
          i += 1
        end
      end
      return 0 if moved.empty?

      File.write(@org, kept.join)
      arch = File.exist?(@archive) ? File.read(@archive, encoding: "UTF-8") : +""
      arch << "\n" unless arch.empty? || arch.end_with?("\n")
      arch << "\n# Archived #{Date.today}\n" << moved.join("\n") << "\n"
      File.write(@archive, arch)
      reload!
      moved.size
    end

    private

    def parse
      items = []
      current = nil
      File.foreach(@org, encoding: "UTF-8").with_index(1) do |line, lineno|
        if (m = line.match(HEADLINE))
          current = Item.new(
            state: m[1], priority: m[2], title: m[3].strip,
            tags: (m[4] || "").split(":").reject(&:empty?),
            line: lineno, source: :org
          )
          items << current
        elsif current && (s = line.match(STAMP))
          d = Date.parse(s[2])
          current.scheduled = d if s[1] == "SCHEDULED"
          current.deadline  = d if s[1] == "DEADLINE"
        end
      end
      items
    end
  end
end
