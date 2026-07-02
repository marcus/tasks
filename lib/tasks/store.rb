# frozen_string_literal: true

require "date"
require_relative "check"
require_relative "quadrants"

module Tasks
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

    # Set a specific date stamp (kind: :deadline or :scheduled), replacing an
    # existing one of that kind or adding it. INBOX items promote to TODO.
    # Backs the CLI `due` and `schedule` commands (the TUI uses reschedule!,
    # which picks whichever stamp the item already has).
    def set_date!(item, date, kind:)
      key = kind == :scheduled ? "SCHEDULED" : "DEADLINE"
      with_history("#{key.downcase} → #{date.iso8601}: #{item.title}") { set_date_impl(item, date, key) }
    end

    # Transition an item to any state. Entering DONE/CANCELLED adds a CLOSED:
    # stamp (unless one is already present); leaving them removes it.
    def set_state!(item, state)
      with_history("state → #{state}: #{item.title}") { set_state_impl(item, state) }
    end

    # Remove a date stamp (kind: :deadline, :scheduled, or nil for both).
    # Returns false if the item has no matching stamp to remove (in addition
    # to the usual stale-line-number case).
    def undate!(item, kind: nil)
      label = kind ? "remove #{kind}: #{item.title}" : "remove dates: #{item.title}"
      with_history(label) { undate_impl(item, kind) }
    end

    # Replace the headline's title text, leaving state/priority/tags/dates
    # untouched. Same staleness contract as complete!.
    def retitle!(item, new_title)
      new_title = utf8(new_title)
      with_history("retitle → #{new_title}: #{item.title}") { retitle_impl(item, new_title) }
    end

    # Add and/or remove tags (contexts are tags that start with "@"), idempotent
    # per tag. Same staleness contract as complete!.
    def set_tags!(item, add: [], remove: [])
      add    = add.map { |t| utf8(t) }
      remove = remove.map { |t| utf8(t) }
      with_history("tags: #{item.title}") { set_tags_impl(item, add, remove) }
    end

    # Append a body line at the end of the item's block. Same staleness contract.
    def add_note!(item, text)
      text = utf8(text)
      with_history("note: #{item.title}") { add_note_impl(item, text) }
    end

    # Relocate the item's whole block under top-level section `section` (matched
    # case-insensitively, exact then substring). Returns the moved headline's
    # new 1-based line number, or false if the block is stale or no such
    # section exists.
    def move!(item, section)
      with_history("move → #{section}: #{item.title}") { move_impl(item, section) }
    end

    # Create a new item under top-level section `project` (default "Inbox").
    # Returns the new headline's 1-based line number, or false if the section
    # doesn't exist. A capture with a date is already "processed"; callers pass
    # state: "TODO" in that case.
    def capture!(text, due: nil, scheduled: nil, priority: nil, tags: [], state: "INBOX", project: nil)
      text = utf8(text)
      tags = tags.map { |t| utf8(t) }
      with_history("capture: #{text}") { capture_impl(text, due, scheduled, priority, tags, state, project) }
    end

    def archive_swept!
      with_history("archive sweep") { archive_swept_impl }
    end

    private

    # User-supplied text (ARGV, TUI input) is tagged with the process locale,
    # which is ASCII-8BIT/BINARY when LANG is unset. The bytes are UTF-8 — the
    # terminal emits UTF-8 — so re-tag them; otherwise joining a BINARY string
    # into the UTF-8 file lines raises Encoding::CompatibilityError. Genuinely
    # invalid bytes are left as-is so they fail loudly rather than corrupt gtd.org.
    def utf8(str)
      return str if str.nil? || str.encoding == Encoding::UTF_8
      recoded = str.dup.force_encoding(Encoding::UTF_8)
      recoded.valid_encoding? ? recoded : str
    end

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
        # post-write invariant: a mutation must never mangle the file.
        # If it would, roll back and report failure instead.
        unless Check.check(@org).ok?
          restore(before)
          return result.is_a?(Integer) ? 0 : false
        end
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
    # `date`. Items with neither get a DEADLINE added. Delegates to
    # set_date_impl with the inferred stamp kind.
    def reschedule_impl(item, date)
      key = if item.deadline     then "DEADLINE"
            elsif item.scheduled then "SCHEDULED"
            else                      "DEADLINE"
            end
      set_date_impl(item, date, key)
    end

    # Set/replace a specific stamp (key: "DEADLINE" or "SCHEDULED"), adding
    # it if absent. Sole date-write path — reschedule_impl infers the kind
    # and delegates here. Same staleness contract as complete!.
    def set_date_impl(item, date, key)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)

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
      # A dated task has been processed — promote it out of the inbox.
      lines[i] = lines[i].sub(/^(\*+\s+)INBOX\b/, '\1TODO') if item.state == "INBOX"
      File.write(@org, lines.join)
      reload!
      true
    end

    # Remove the SCHEDULED and/or DEADLINE line(s) within item's block. Same
    # staleness contract as complete!. Returns false if nothing matched.
    def undate_impl(item, kind)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)
      level = lines[i][/^\*+/].length
      keys = case kind
             when :scheduled then ["SCHEDULED"]
             when :deadline  then ["DEADLINE"]
             else                 ["SCHEDULED", "DEADLINE"]
             end

      removed = false
      j = i + 1
      while j < lines.length
        lvl = lines[j][/^(\*+)\s/, 1]&.length
        break if lvl && lvl <= level
        # anchored: only delete actual stamp lines, never a prose note that
        # happens to mention "DEADLINE:" mid-sentence
        if keys.any? { |k| lines[j] =~ /^\s*#{k}:/ }
          lines.delete_at(j)
          removed = true
        else
          j += 1
        end
      end
      return false unless removed

      File.write(@org, lines.join)
      reload!
      true
    end

    # Split a headline line into [stars, state, priority, title, tags_array].
    def headline_parts(line)
      m = line.match(HEADLINE)
      [line[/^\*+/], m[1], m[2], m[3].strip, (m[4] || "").split(":").reject(&:empty?)]
    end

    # Rebuild a headline line (with trailing newline) from its parts.
    def build_headline(stars, state, priority, title, tags)
      s = +"#{stars} #{state} "
      s << "[##{priority}] " if priority
      s << title
      s << " :#{tags.join(":")}:" unless tags.empty?
      s << "\n"
      s
    end

    # Downcased title of a section heading line (its text after the stars).
    def heading_title(line) = line.sub(/^\*+\s+/, "").strip.downcase

    # Index of the top-level ("* ") section matching `name` — exact, then
    # substring — or nil.
    def find_section(lines, name)
      want = name.strip.downcase
      lines.index { |l| l =~ /^\* / && heading_title(l) == want } ||
        lines.index { |l| l =~ /^\* / && heading_title(l).include?(want) }
    end

    # 0-based index just past the last content line of the block whose headline
    # is at line index `i` — the insertion point for appended body lines. Skips
    # trailing blank lines that separate this block from the next heading.
    def block_end_index(lines, i)
      level = lines[i][/^\*+/].length
      j = i + 1
      while j < lines.length
        lvl = lines[j][/^(\*+)\s/, 1]&.length
        break if lvl && lvl <= level
        j += 1
      end
      j -= 1 while j > i + 1 && lines[j - 1].strip.empty?
      j
    end

    def retitle_impl(item, new_title)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)
      stars, state, pri, _title, tags = headline_parts(lines[i])
      lines[i] = build_headline(stars, state, pri, new_title.strip, tags)
      File.write(@org, lines.join)
      reload!
      true
    end

    def set_tags_impl(item, add, remove)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)
      stars, state, pri, title, tags = headline_parts(lines[i])
      tags = tags.reject { |t| remove.include?(t) }
      add.each { |t| tags << t unless tags.include?(t) }
      lines[i] = build_headline(stars, state, pri, title, tags)
      File.write(@org, lines.join)
      reload!
      true
    end

    def add_note_impl(item, text)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)
      lines.insert(block_end_index(lines, i), "   #{text.strip}\n")
      File.write(@org, lines.join)
      reload!
      true
    end

    def move_impl(item, section)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)
      block_end = block_end_index(lines, i)
      block = lines[i...block_end]
      block[-1] = "#{block[-1].chomp}\n" if block[-1] && !block[-1].end_with?("\n")
      rest = lines[0...i] + lines[block_end..]

      target = find_section(rest, section)
      return false unless target
      insert_at = ((target + 1)...rest.length).find { |k| rest[k] =~ /^\* / } || rest.length
      insert_at -= 1 while insert_at > target + 1 && rest[insert_at - 1].strip.empty?
      rest.insert(insert_at, *block)
      File.write(@org, rest.join)
      reload!
      insert_at + 1
    end

    def capture_impl(text, due, scheduled, priority, tags, state, project)
      lines = File.readlines(@org, encoding: "UTF-8")
      idx = find_section(lines, project || "Inbox")
      return false unless idx
      stars = "*" * (lines[idx][/^\*+/].length + 1)

      entry = [build_headline(stars, state, priority, text.strip, tags)]
      entry << "   SCHEDULED: <#{scheduled.iso8601} #{scheduled.strftime("%a")}>\n" if scheduled
      entry << "   DEADLINE: <#{due.iso8601} #{due.strftime("%a")}>\n" if due
      entry << "   Captured [#{Date.today}].\n"

      insert_at = ((idx + 1)...lines.length).find { |k| lines[k] =~ /^\* / } || lines.length
      insert_at -= 1 while insert_at > idx + 1 && lines[insert_at - 1].strip.empty?
      lines.insert(insert_at, *entry)
      File.write(@org, lines.join)
      reload!
      insert_at + 1
    end

    def set_state_impl(item, new_state)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)

      old_state = item.state
      lines[i] = lines[i].sub(/^(\*+\s+)(INBOX|TODO|NEXT|WAITING|DONE|CANCELLED)\b/, "\\1#{new_state}")

      if DONE_STATES.include?(new_state) && !DONE_STATES.include?(old_state)
        lines.insert(i + 1, "   CLOSED: [#{Date.today}]\n") unless closed_line_index(lines, i)
      elsif DONE_STATES.include?(old_state) && !DONE_STATES.include?(new_state)
        (c = closed_line_index(lines, i)) && lines.delete_at(c)
      end

      File.write(@org, lines.join)
      reload!
      true
    end

    # Line index of the CLOSED: stamp within the block whose headline is at
    # line index `i`, or nil. Stops at the next same-or-higher-level headline.
    def closed_line_index(lines, i)
      level = lines[i][/^\*+/].length
      j = i + 1
      while j < lines.length
        lvl = lines[j][/^(\*+)\s/, 1]&.length
        break if lvl && lvl <= level
        return j if lines[j] =~ /CLOSED:\s*\[/
        j += 1
      end
      nil
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

    public

    # Items parsed from the archive file (source: :archive). Not cached —
    # the archive is read rarely (`list -x/-a`) and appended rarely.
    def archive_items
      parse_file(@archive, source: :archive)
    end

    private

    def parse = parse_file(@org, source: :org)

    def parse_file(path, source:)
      items = []
      return items unless File.exist?(path)
      current = nil
      File.foreach(path, encoding: "UTF-8").with_index(1) do |line, lineno|
        if (m = line.match(HEADLINE))
          current = Item.new(
            state: m[1], priority: m[2], title: m[3].strip,
            tags: (m[4] || "").split(":").reject(&:empty?),
            line: lineno, source: source
          )
          items << current
        elsif current && (s = line.match(STAMP))
          begin
            d = Date.parse(s[2])
            current.scheduled = d if s[1] == "SCHEDULED"
            current.deadline  = d if s[1] == "DEADLINE"
          rescue Date::Error
            # impossible date — leave nil; Check reports it, don't crash the TUI
          end
        end
      end
      items
    end
  end
end
