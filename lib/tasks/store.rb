# frozen_string_literal: true

require "date"
require "securerandom"
require "set"
require_relative "atomic"
require_relative "check"
require_relative "journal"
require_relative "links"
require_relative "quadrants"
require_relative "recur"
require_relative "tree"

module Tasks
  Item = Struct.new(
    :state, :priority, :title, :tags, :scheduled, :deadline, :line, :source,
    :recur, :id, keyword_init: true
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

    # A task's stable handle lives as :ID: inside an org PROPERTIES drawer, right
    # after the headline's planning lines. We only honor an :ID: that sits inside
    # a real :PROPERTIES:…:END: drawer under a *task* headline — not one on a
    # section heading, a child subtask, or a bare line in prose (org wouldn't
    # treat those as the task's property either). Keys match case-insensitively.
    ID_LINE      = /^\s*:ID:\s+(\S+)\s*$/i
    DRAWER_START = /^\s*:PROPERTIES:\s*$/i
    DRAWER_END   = /^\s*:END:\s*$/i
    # Planning lines (org keeps these between the headline and the drawer).
    PLANNING = /^\s*(?:SCHEDULED|DEADLINE|CLOSED):/

    # Semantic tag marking a task as deferred (someday/maybe). See Item#deferred?.
    DEFER_TAG = "defer"

    attr_reader :org, :archive

    UNDO_LIMIT = 50 # deepest undo history the journal retains

    # Drop org PROPERTIES drawers (:PROPERTIES:…:END:) from block lines, for
    # display — the :ID: is surfaced on its own; the drawer is machinery, not a
    # note. Shared by the CLI's `show` and the TUI detail modal so they agree.
    # A block's prose: drawer machinery, planning stamps, and org comment lines
    # (e.g. the archive's "# Archived <date>" sweep separators) removed. What
    # body search and link extraction should see — metadata has its own
    # filters/columns, so matching "/fri" must not hit every Friday DEADLINE.
    def self.prose(lines)
      strip_drawer(lines).reject { |l| l =~ PLANNING || l.start_with?("#") }
    end

    def self.strip_drawer(lines)
      in_drawer = false
      lines.reject do |l|
        if l =~ DRAWER_START
          in_drawer = true
        elsif in_drawer && l =~ DRAWER_END
          in_drawer = false
          true # drop the closing :END:
        elsif in_drawer && l =~ /^\s*:[\w-]+:/
          true # a :KEY: property line inside the drawer
        elsif in_drawer
          # Real prose while still "in" a drawer means the drawer was never
          # closed (malformed input) — stop swallowing so notes aren't eaten.
          in_drawer = false
          false
        else
          false
        end
      end
    end

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
      @tree = nil # derived from the same lines; rebuild lazily on next ask
      @nodes_by_line = nil
      self
    end

    # The structural index (Tasks::Tree) over gtd.org: sections, tasks, and
    # subtasks as nested nodes, each with its own body lines. Rebuilt whenever
    # the file changes (items() drives the staleness check).
    def tree
      items # ensures a fresh parse and clears @tree if the file changed
      @tree ||= Tree.build(read_lines(@org), @cache.to_h { |i| [i.line, i] })
    end

    # The tree node for an item (nil for archive items — the tree indexes the
    # live file only). O(1) via a line-keyed map; if the item carries an id and
    # the node at its line doesn't match (lines shifted underneath a held item),
    # fall back to finding its node by id — same preference locate applies.
    def node_for(item)
      return nil unless item.source == :org
      n = nodes_by_line[item.line]
      if n&.item
        # Same identity check the mutation paths apply: id when the item has
        # one, title otherwise — a held id-less item whose line was taken over
        # by a different task must degrade to nil, not to the wrong task.
        return n if item.id ? n.item.id == item.id : n.item.title == item.title
      end
      item.id ? nodes_by_line.each_value.find { |x| x.item&.id == item.id } : nil
    end

    # Line-number => node map over the whole tree, built once per tree build so
    # per-item lookups (body, project) are O(1), not a tree walk each.
    def nodes_by_line
      tree
      @nodes_by_line ||= {}.tap do |map|
        @tree.each { |root| root.each { |n| map[n.line] = n } }
      end
    end

    # The item's own body lines (prose under its headline, stopping at any
    # child headline), filtered to prose — the text that body search and link
    # extraction run over. Works for org AND archive items: live items read
    # the cached tree; archive items walk the archive lines (one block-boundary
    # rule for both — own_block_end).
    def body(item)
      if item.source == :org
        node = node_for(item)
        node ? Store.prose(node.body) : []
      else
        lines = read_lines(@archive)
        i = guard_line(lines, item) or return []
        Store.prose(lines[(i + 1)...own_block_end(lines, i)])
      end
    end

    # Links found in the item's title and body, classified by system
    # (see Tasks::Links).
    def links(item)
      Links.extract([item.title, *body(item)])
    end

    # Raw file lines of an item: its headline plus body, up to the next
    # same-or-higher-level headline. Empty array if the item can't be located.
    def block(item)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = locate(lines, item) or return []
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

    # Ensure the item carries a stable :ID:, returning it. Idempotent: an item
    # that already has one is returned untouched (no write); otherwise a fresh
    # unique id is stamped into a PROPERTIES drawer. Returns false only if the
    # item can't be located (stale line and no id to find it by).
    def ensure_id!(item)
      return item.id if item.id
      with_history("id: #{item.title}") { ensure_id_impl(item) }
    end

    private

    # Read a file's lines with a small cache, so read surfaces that ask per
    # item (body search over the archive, links over every task) cost one file
    # read, not one per task. Keyed on (mtime, inode, size): Atomic.write
    # installs a fresh inode on every write, so even two writes inside one
    # coarse-mtime tick can't serve stale lines. Mutation impls read the org
    # file directly under the lock; the cache serves the read/parse surfaces
    # (and the archive id sweep, where the inode key keeps it write-fresh).
    def read_lines(path)
      stat = File.stat(path)
      key = [stat.mtime, stat.ino, stat.size]
      @lines_cache ||= {}
      cached = @lines_cache[path]
      return cached[1] if cached && cached[0] == key
      lines = File.readlines(path, encoding: "UTF-8")
      @lines_cache[path] = [key, lines]
      lines
    rescue Errno::ENOENT
      []
    end

    # 0-based index of the item's headline in `lines`, or nil if it can't be
    # found. Prefers the stable :ID: (so a mutation still lands even if lines
    # shifted or the title changed out from under us); falls back to the
    # line-number + title guard for tasks that don't yet carry an id.
    def locate(lines, item)
      if item.id && (i = id_index(lines)[item.id])
        return i
      end
      guard_line(lines, item)
    end

    # The pre-id staleness guard: the recorded line still holds a headline
    # containing the item's title. Used directly for archive items (no id
    # index over the archive) and as locate's fallback.
    def guard_line(lines, item)
      i = item.line - 1
      i if lines[i]&.match?(HEADLINE) && lines[i].include?(item.title)
    end

    # Map of :ID: value => 0-based headline index it belongs to — the single
    # source of truth for "which task owns which id", shared by locate and id
    # minting so they can never disagree with parse_file. An id counts only
    # inside a PROPERTIES drawer under a *task* headline: a section heading or a
    # deeper subtask resets ownership (headline = nil / the new index), and a
    # bare :ID: outside a drawer is ignored — matching org's own scoping.
    def id_index(lines)
      map = {}
      headline = nil
      in_drawer = false
      lines.each_with_index do |line, idx|
        if line.match?(HEADLINE)      then headline = idx; in_drawer = false
        elsif line =~ /^\*+\s/        then headline = nil; in_drawer = false
        elsif line =~ DRAWER_START    then in_drawer = true
        elsif line =~ DRAWER_END      then in_drawer = false
        elsif headline && in_drawer && (m = line.match(ID_LINE))
          map[m[1]] ||= headline
        end
      end
      map
    end

    # A short, unique, CLI-typeable id (8 hex chars). Collisions are astronomically
    # unlikely, but cheap to exclude across BOTH files so a fresh id can't clash
    # with one already swept into the archive (which id_index(@org) can't see).
    def gen_id(taken)
      taken = taken.to_set
      loop do
        id = SecureRandom.hex(4)
        break id unless taken.include?(id)
      end
    end

    # 0-based index just past the headline's contiguous planning lines
    # (SCHEDULED/DEADLINE/CLOSED) — where an org PROPERTIES drawer must sit to be
    # valid (planning first, then the drawer).
    def planning_end(lines, i)
      j = i + 1
      j += 1 while j < lines.length && lines[j] =~ PLANNING
      j
    end

    # The three lines of a PROPERTIES drawer carrying `id` (3-space indent, org
    # order). The one place the drawer's shape is defined — capture and
    # first-touch stamping both build it here.
    def drawer_lines(id)
      ["   :PROPERTIES:\n", "   :ID: #{id}\n", "   :END:\n"]
    end

    # Guarantee the block at headline index `i` has an :ID:, returning the id
    # (existing or freshly minted). If the task already has its property drawer
    # (the one org recognizes: immediately after the headline's planning lines),
    # add the :ID: INTO it — a second drawer would orphan the existing keys.
    # Otherwise insert a fresh, org-canonical drawer at that spot. Mutates
    # `lines` in place, but only below `i`, so the caller's headline index holds.
    def ensure_drawer(lines, i)
      index = id_index(lines)
      existing = index.key(i)
      return existing if existing

      id = gen_id(index.keys + archived_ids)
      at = planning_end(lines, i)
      if lines[at] =~ DRAWER_START
        lines.insert(at + 1, "   :ID: #{id}\n")
      else
        lines.insert(at, *drawer_lines(id))
      end
      id
    end

    def archived_ids
      return [] unless File.exist?(@archive)
      parse_file(@archive, source: :archive).map(&:id).compact
    end

    def ensure_id_impl(item)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = locate(lines, item) or return false
      id = ensure_drawer(lines, i)
      Atomic.write(@org, lines.join)
      reload!
      id
    end

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
      i = locate(lines, item) or return false
      ensure_drawer(lines, i)
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
      i = locate(lines, item) or return false
      ensure_drawer(lines, i)
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
      i = locate(lines, item) or return false
      ensure_drawer(lines, i)

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
      i = locate(lines, item) or return false
      ensure_drawer(lines, i)
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
      i = locate(lines, item) or return false
      ensure_drawer(lines, i)

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
      i = locate(lines, item) or return false
      ensure_drawer(lines, i)

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
      i = locate(lines, item) or return false
      ensure_drawer(lines, i)
      stars, state, pri, _title, tags = headline_parts(lines[i])
      lines[i] = build_headline(stars, state, pri, new_title.strip, tags)
      Atomic.write(@org, lines.join)
      reload!
      true
    end

    def set_tags_impl(item, add, remove)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = locate(lines, item) or return false
      ensure_drawer(lines, i)
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
      i = locate(lines, item) or return false
      ensure_drawer(lines, i)
      lines.insert(block_end_index(lines, i), "   #{text.strip}\n")
      Atomic.write(@org, lines.join)
      reload!
      true
    end

    def move_impl(item, section)
      lines = File.readlines(@org, encoding: "UTF-8")
      i = locate(lines, item) or return false
      ensure_drawer(lines, i)
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
      # Every new task gets a stable id (drawer after the planning lines, per org).
      entry.concat(drawer_lines(gen_id(id_index(lines).keys + archived_ids)))
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
      i = locate(lines, item) or return false
      ensure_drawer(lines, i)

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

    # Parse via the shared line cache, so items and the tree (which binds nodes
    # to items by line number) always derive from the SAME read of the file — a
    # write landing between two separate reads could otherwise mis-bind them.
    def parse_file(path, source:)
      parse_lines(read_lines(path), source: source)
    end

    def parse_lines(all_lines, source:)
      items = []
      current = nil
      in_drawer = false
      all_lines.each.with_index(1) do |line, lineno|
        if (m = line.match(HEADLINE))
          current = Item.new(
            state: m[1], priority: m[2], title: m[3].strip,
            tags: (m[4] || "").split(":").reject(&:empty?),
            line: lineno, source: source
          )
          items << current
          in_drawer = false
        elsif line =~ /^\*+\s/
          # A non-task headline (a section like "* Work") ends the task's scope,
          # so its own properties/stamps aren't misread as the task's above it.
          current = nil
          in_drawer = false
        elsif line =~ DRAWER_START
          in_drawer = true
        elsif line =~ DRAWER_END
          in_drawer = false
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
        elsif current && in_drawer && (idm = line.match(ID_LINE))
          # First :ID: in the drawer wins (a well-formed task has exactly one).
          current.id ||= idm[1]
        end
      end
      items
    end
  end
end
