# frozen_string_literal: true

require "date"
require_relative "atomic"
require_relative "check"
require_relative "journal"
require_relative "quadrants"
require_relative "recur"

module Tasks
  Item = Struct.new(
    :state, :priority, :title, :tags, :scheduled, :deadline, :line, :source,
    :recur, keyword_init: true
  ) do
    def open?    = Store::OPEN_STATES.include?(state)
    def contexts = tags.select { |t| t.start_with?("@") }
    # Deferred (someday/maybe) is a semantic tag, like important/urgent — it
    # rides alongside the task's real state rather than replacing it.
    def deferred? = tags.include?(Store::DEFER_TAG)
    # A recurring task carries an org repeater cookie (e.g. ".+1w") on its
    # date stamp; `done` rolls the date forward instead of closing it.
    def recurring? = !recur.nil?
  end

  # Owns gtd.org: parsing, change detection, and the mutations the TUI
  # performs directly (complete, reschedule, archive sweep). Claude edits
  # the file out-of-band; `changed?` picks those up.
  class Store
    HEADLINE = /^\*+\s+(INBOX|TODO|NEXT|WAITING|DONE|CANCELLED)\s+(?:\[#([ABC])\]\s+)?(.*?)\s*(:[\w@:]+:)?\s*$/
    # An org repeater cookie inside a timestamp: +1w, ++2d, .+1m (see Tasks::Recur).
    # The count is a positive integer (a zero count like ++0d is not a repeater —
    # it would never terminate a catch-up roll — so it's parsed as a plain date).
    REPEATER = /(?:\.\+|\+\+|\+)[1-9]\d*[dwmy]/
    # A SCHEDULED:/DEADLINE: stamp, capturing the date and (optionally) the
    # repeater cookie that may sit after a day name/time but before the `>`.
    STAMP    = /(SCHEDULED|DEADLINE):\s*<(\d{4}-\d{2}-\d{2})(?:[^>]*?\s(#{REPEATER}))?[^>]*>/

    OPEN_STATES = %w[INBOX TODO NEXT WAITING].freeze
    DONE_STATES = %w[DONE CANCELLED].freeze

    # Semantic tag marking a task as deferred (someday/maybe). See Item#deferred?.
    DEFER_TAG = "defer"

    attr_reader :org, :archive

    UNDO_LIMIT = 50 # deepest undo history the journal retains

    # `journal_dir` defaults to an XDG_STATE_HOME location derived from the org
    # path, so the CLI and TUI editing the same file share one undo history;
    # tests pass an explicit dir to stay hermetic.
    def initialize(org:, archive:, journal_dir: nil, undo_limit: UNDO_LIMIT)
      @org = org
      @archive = archive
      @mtime = nil
      @cache = nil
      @journal = Journal.new(dir: journal_dir || Journal.dir_for(org), org: org, limit: undo_limit)
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
    # History lives in an on-disk Journal (see journal.rb) shared by the CLI and
    # the TUI, so it survives a restart and one tool can undo the other's edit.
    # A step is applied only when the live files still match what that mutation
    # left behind — an out-of-band edit (Claude, another process) makes the step
    # unsafe and it is refused, not forced.

    # Returns [:ok, label] | [:empty] | [:conflict, label]
    def undo! = history_step(-1)
    def redo! = history_step(1)

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

    # Defer (someday/maybe) or reactivate a task by toggling the DEFER_TAG.
    # Idempotent per direction; delegates to the tag machinery so deferral is
    # just a semantic tag. Same staleness contract as complete!.
    def set_deferred!(item, deferred)
      label = deferred ? "defer: #{item.title}" : "activate: #{item.title}"
      add    = deferred ? [DEFER_TAG] : []
      remove = deferred ? [] : [DEFER_TAG]
      with_history(label) { set_tags_impl(item, add, remove) }
    end

    # Attach, replace, or (cookie == :off) remove a recurrence repeater on the
    # item's date stamp. Returns false on a stale line, or when the item has no
    # SCHEDULED/DEADLINE stamp to carry the cookie. Same staleness contract.
    def set_recur!(item, cookie)
      label = cookie == :off ? "recur off: #{item.title}" : "recur #{cookie}: #{item.title}"
      with_history(label) { set_recur_impl(item, cookie) }
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

    # Serialize the read-modify-write of a mutation across *tasks* processes (the
    # CLI and the TUI): without it, two of them could interleave their
    # readlines/write and silently drop one change. The lock is an advisory flock
    # on a sidecar next to the real gtd.org, so every process reaches the same
    # inode regardless of how the path was spelled (symlink, relative, differing
    # XDG_STATE_HOME) — that shared identity is why the lock file lives beside the
    # file it guards, not in the journal dir. It does NOT constrain out-of-band
    # editors (Claude, an editor) that don't take the lock; those are caught
    # instead by the post-write Check and the journal's conflict detection, and
    # Atomic.write keeps even an unlocked concurrent read from ever tearing.
    def with_lock
      File.open(lock_path, File::RDWR | File::CREAT, 0o644) do |f|
        f.flock(File::LOCK_EX)
        yield
      end
    end

    # A per-file lock sidecar (".gtd.org.lock") beside the resolved org file.
    # Journal.canonical resolves the symlink (so two spellings of the same file
    # lock in common) and is ENOENT-safe (a delete race falls back to the
    # expanded path instead of crashing with_lock).
    def lock_path
      target = Journal.canonical(@org)
      File.join(File.dirname(target), ".#{File.basename(target)}.lock")
    end

    def snapshot
      {
        org: File.read(@org, encoding: "UTF-8"),
        archive: File.exist?(@archive) ? File.read(@archive, encoding: "UTF-8") : nil,
      }
    end

    def restore(snap)
      Atomic.write(@org, snap[:org])
      if snap[:archive].nil?
        File.delete(@archive) if File.exist?(@archive)
      else
        Atomic.write(@archive, snap[:archive])
      end
      reload!
    end

    # Apply an undo (delta -1) or redo (delta +1) planned by the journal, under
    # the lock so the plan and its commit can't race another writer.
    def history_step(delta)
      with_lock do
        step = @journal.plan(delta)
        return [:empty] unless step
        return [:conflict, step[:label]] unless snapshot == step[:expect]
        restore(step[:target])
        step[:commit].call
        [:ok, step[:label]]
      end
    end

    # Record history only when the mutation actually wrote (truthy, nonzero) AND
    # changed the file — an idempotent no-op (e.g. adding a tag already present)
    # succeeds but must not burn an undo slot with a label that reverts nothing.
    # The whole read-modify-write (snapshot, mutate, validate, journal) runs
    # under the lock so a concurrent writer can't slip between the steps.
    def with_history(label)
      with_lock do
        before = snapshot
        result = yield
        if result && result != 0
          # post-write invariant: a mutation must never mangle the file.
          # If it would, roll back and report failure instead.
          unless Check.check(@org).ok?
            restore(before)
            return result.is_a?(Integer) ? 0 : false
          end
          after = snapshot
          @journal.record(label: label, before: before, after: after) unless after == before
        end
        result
      end
    end

    def complete_impl(item)
      # A recurring task rolls its date forward and stays open instead of
      # closing — completing an occurrence, not the task.
      return advance_recurrence_impl(item) if item.recurring?

      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)
      lines[i] = lines[i].sub(/^(\*+\s+)(INBOX|TODO|NEXT|WAITING)\b/, '\1DONE')
      # A completed task is no longer someday/maybe — drop the defer marker so
      # it can't orphan (invisible to `list --deferred`, unreachable by activate).
      lines[i] = strip_headline_tag(lines[i], DEFER_TAG)
      lines.insert(i + 1, "   CLOSED: [#{Date.today}]\n")
      Atomic.write(@org, lines.join)
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
      Atomic.write(@org, lines.join)
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
        # Preserve any repeater cookie already on this stamp — `due`/`schedule`
        # change the date, not the recurrence.
        cookie = lines[stamp_at][REPEATER]
        lines[stamp_at] = lines[stamp_at].sub(/#{key}:\s*<[^>]*>/, timestamp(key, date, cookie))
      else
        lines.insert(i + 1, "   #{timestamp(key, date)}\n")
      end
      # A dated task has been processed — promote it out of the inbox.
      lines[i] = lines[i].sub(/^(\*+\s+)INBOX\b/, '\1TODO') if item.state == "INBOX"
      Atomic.write(@org, lines.join)
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

      Atomic.write(@org, lines.join)
      reload!
      true
    end

    # 0-based index just past the item's OWN lines: its headline's metadata and
    # body, stopping at the first following headline of ANY level. Recurrence is
    # scoped this way to match parse_file, which binds a stamp to its immediate
    # headline — a wider (subtree) scan would let a parent's roll mutate a
    # child's stamp. Trailing blank lines are excluded (insertion point for notes).
    def own_block_end(lines, i)
      j = i + 1
      j += 1 while j < lines.length && lines[j] !~ /^\*+\s/
      j -= 1 while j > i + 1 && lines[j - 1].strip.empty?
      j
    end

    # 0-based index of the stamp the recurrence rides — DEADLINE first, then
    # SCHEDULED (matching reschedule_impl's precedence) — among item's OWN lines,
    # returned as [index, "DEADLINE"|"SCHEDULED"], or nil if the item has no
    # dated stamp. Only real ISO-dated stamps qualify (a diary/sexp timestamp
    # like <%%(...)> is not something we can roll).
    def recur_stamp_line(lines, i)
      scheduled_at = nil
      j = i + 1
      while j < lines.length && lines[j] !~ /^\*+\s/
        return [j, "DEADLINE"] if lines[j] =~ /^\s*DEADLINE:\s*<\d{4}-\d{2}-\d{2}/
        scheduled_at ||= j if lines[j] =~ /^\s*SCHEDULED:\s*<\d{4}-\d{2}-\d{2}/
        j += 1
      end
      scheduled_at && [scheduled_at, "SCHEDULED"]
    end

    # Set/replace/remove the repeater cookie on the item's precedence stamp.
    # cookie is a canonical cookie string, or :off to strip it. Returns false
    # if the item is stale or has no date stamp.
    def set_recur_impl(item, cookie)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)

      target = recur_stamp_line(lines, i)
      return false unless target
      at, key = target
      date = Date.parse(lines[at][/#{key}:\s*<(\d{4}-\d{2}-\d{2})/, 1])
      body = cookie == :off ? timestamp(key, date) : timestamp(key, date, cookie)
      lines[at] = lines[at].sub(/#{key}:\s*<[^>]*>/, body)
      Atomic.write(@org, lines.join)
      reload!
      true
    end

    # Complete a recurring occurrence: roll every repeating stamp in the block
    # forward by its cookie and leave the task open (no DONE, no CLOSED). Logs
    # the completion so history survives even though the task never closes.
    # Returns false if stale, or if no stamp actually carries a cookie.
    def advance_recurrence_impl(item)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)

      # Scope to the item's own lines so a parent's roll never touches a child
      # subtask's stamp (see own_block_end).
      own_end = own_block_end(lines, i)
      rolled = false
      (i + 1...own_end).each do |j|
        next unless (m = lines[j].match(STAMP)) && m[3]
        key, cookie = m[1], m[3]
        nxt = Recur.next_date(cookie, from: Date.parse(m[2]))
        lines[j] = lines[j].sub(/#{key}:\s*<[^>]*>/, timestamp(key, nxt, cookie))
        rolled = true
      end
      return false unless rolled

      lines.insert(own_end, "   - Did [#{Date.today}].\n")
      Atomic.write(@org, lines.join)
      reload!
      true
    end

    # Split a headline line into [stars, state, priority, title, tags_array].
    def headline_parts(line)
      m = line.match(HEADLINE)
      [line[/^\*+/], m[1], m[2], m[3].strip, (m[4] || "").split(":").reject(&:empty?)]
    end

    # Render a SCHEDULED:/DEADLINE: stamp body, e.g. "DEADLINE: <2026-07-15 Wed>"
    # or, with a cookie, "DEADLINE: <2026-07-15 Wed +1w>". The day name is
    # regenerated from the date so it never goes stale.
    def timestamp(key, date, cookie = nil)
      inner = "#{date.iso8601} #{date.strftime("%a")}"
      inner << " #{cookie}" if cookie
      "#{key}: <#{inner}>"
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

    # Remove `tag` from a headline's tag cluster, rebuilding the line (and
    # dropping the cluster entirely if it was the last tag). No-op if the line
    # isn't a headline or lacks the tag.
    def strip_headline_tag(line, tag)
      return line unless line.match?(HEADLINE)
      stars, state, pri, title, tags = headline_parts(line)
      return line unless tags.include?(tag)
      build_headline(stars, state, pri, title, tags - [tag])
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
      Atomic.write(@org, lines.join)
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
      Atomic.write(@org, lines.join)
      reload!
      true
    end

    def add_note_impl(item, text)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)
      lines.insert(block_end_index(lines, i), "   #{text.strip}\n")
      Atomic.write(@org, lines.join)
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
      Atomic.write(@org, rest.join)
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
      Atomic.write(@org, lines.join)
      reload!
      insert_at + 1
    end

    def set_state_impl(item, new_state)
      # Completing a recurring task advances the occurrence rather than closing
      # it — the same rule complete_impl applies, so `done` and `state … DONE`
      # agree. CANCELLED still truly closes (stops the recurrence).
      return advance_recurrence_impl(item) if new_state == "DONE" && item.recurring?

      lines = File.readlines(@org, encoding: "UTF-8")
      i = item.line - 1
      return false unless lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)

      old_state = item.state
      lines[i] = lines[i].sub(/^(\*+\s+)(INBOX|TODO|NEXT|WAITING|DONE|CANCELLED)\b/, "\\1#{new_state}")

      if DONE_STATES.include?(new_state) && !DONE_STATES.include?(old_state)
        # Entering DONE/CANCELLED clears the defer marker (see complete_impl).
        lines[i] = strip_headline_tag(lines[i], DEFER_TAG)
        lines.insert(i + 1, "   CLOSED: [#{Date.today}]\n") unless closed_line_index(lines, i)
      elsif DONE_STATES.include?(old_state) && !DONE_STATES.include?(new_state)
        (c = closed_line_index(lines, i)) && lines.delete_at(c)
      end

      Atomic.write(@org, lines.join)
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

      Atomic.write(@org, kept.join)
      arch = File.exist?(@archive) ? File.read(@archive, encoding: "UTF-8") : +""
      arch << "\n" unless arch.empty? || arch.end_with?("\n")
      arch << "\n# Archived #{Date.today}\n" << moved.join("\n") << "\n"
      Atomic.write(@archive, arch)
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
            # Record the repeater cookie, if any. DEADLINE takes precedence over
            # SCHEDULED (matching reschedule_impl) when both carry one.
            current.recur = s[3] if s[3] && (s[1] == "DEADLINE" || current.recur.nil?)
          rescue Date::Error
            # impossible date — leave nil; Check reports it, don't crash the TUI
          end
        end
      end
      items
    end
  end
end
