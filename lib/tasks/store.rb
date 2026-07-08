# frozen_string_literal: true

require "date"
require "securerandom"
require "set"
require_relative "atomic"
require_relative "check"
require_relative "format"
require_relative "journal"
require_relative "links"
require_relative "quadrants"
require_relative "recur"
require_relative "tree"

module Tasks
  Item = Struct.new(
    :state, :priority, :title, :tags, :scheduled, :deadline, :line, :source,
    :recur, :id, :closed, keyword_init: true
  ) do
    def open?    = Store::OPEN_STATES.include?(state)
    def contexts = tags.select { |t| t.start_with?("@") }
    # Deferred (someday/maybe) is a semantic tag, like important/urgent — it
    # rides alongside the task's real state rather than replacing it.
    def deferred? = tags.include?(Store::DEFER_TAG)
    # A recurring task carries a VALID repeater cookie (e.g. ".+1w") in its own
    # `recur` field; `done` rolls the date forward instead of closing it. A cookie
    # that doesn't match the grammar (a hand-edited "++0d", say) is treated as
    # non-recurring so completion closes the task normally — Check still reports
    # the bad cookie. Guards Recur.next_date from raising on a junk value.
    def recurring? = Recur.cookie?(recur)
  end

  # Owns tasks.jsonl: parsing records into Items, change detection, and the
  # mutations the CLI and TUI perform. Every record is an explicit JSON object
  # (see Tasks::Format); the tree lives in the `parent` pointers, so the store
  # never infers block boundaries by scanning — a whole class of bugs the old
  # org line-walker was prone to is structurally gone. Claude edits the file
  # out-of-band via the CLI; `changed?` picks up any write by mtime.
  class Store
    OPEN_STATES = %w[INBOX TODO NEXT WAITING].freeze
    DONE_STATES = %w[DONE CANCELLED].freeze

    # Semantic tag marking a task as deferred (someday/maybe). See Item#deferred?.
    DEFER_TAG = "defer"

    attr_reader :org, :archive

    # The Check error summary from the most recent mutation that wrote a file
    # then failed post-write validation and was rolled back — nil when the last
    # mutation was clean. Lets the CLI tell a validation rollback (run `check`)
    # apart from a genuine stale-line staleness. Cleared at each mutation's entry.
    attr_reader :last_rollback

    UNDO_LIMIT = 50 # deepest undo history the journal retains

    # `journal_dir` defaults to an XDG_STATE_HOME location derived from the live
    # path, so the CLI and TUI editing the same file share one undo history;
    # tests pass an explicit dir to stay hermetic. `links`/`link_systems` are
    # the user's configured shorthand templates and custom host rows
    # (Config#links / #link_systems), consulted by #links. The keyword stays
    # `org:` for constructor compatibility though it now names the jsonl file.
    def initialize(org:, archive:, journal_dir: nil, undo_limit: UNDO_LIMIT,
                   links: {}, link_systems: {})
      @org = org
      @archive = archive
      @stat = nil
      @cache = nil
      @records = nil
      @link_shorthands = links
      @link_systems = link_systems
      @journal = Journal.new(dir: journal_dir || Journal.dir_for(org), org: org, limit: undo_limit)
    end

    def items
      reload! if @cache.nil? || changed?
      @cache
    end

    def changed?
      stat_key(@org) != @stat
    end

    def reload!
      @stat = stat_key(@org)
      @records = parse_records(@org)
      @cache = @records.select { |r| r["type"] == "task" }.map { |r| build_item(r, :live) }
      @tree = nil # derived from the same records; rebuild lazily on next ask
      @nodes_by_line = nil
      self
    end

    # The structural index (Tasks::Tree) over the live file: sections, tasks,
    # and subtasks as nested nodes built from `parent` pointers, each with its
    # own body lines. Rebuilt whenever the file changes (items() drives the
    # staleness check).
    def tree
      items # ensures a fresh parse and clears @tree if the file changed
      @tree ||= Tree.build(@records, @cache.to_h { |i| [i.line, i] })
    end

    # The tree node for an item (nil for archive items — the tree indexes the
    # live file only). O(1) via a line-keyed map; if the item carries an id and
    # the node at its line doesn't match (lines shifted underneath a held item),
    # fall back to finding its node by id — same preference locate applies.
    def node_for(item)
      return nil unless item.source == :live
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

    # The item's own body lines — the record's `body` string split back into
    # lines. This is the text body search and link extraction run over; it never
    # includes a child's body (children are separate records). Works for live
    # AND archive items (same record lookup for both).
    def body(item)
      rec = locate(records_for(item), item)
      return [] unless rec
      b = rec["body"]
      b.nil? || b.empty? ? [] : b.split("\n")
    end

    # Links found in the item's title and body — org links, bare URLs, and
    # configured shorthands (jira:OPS-1) — classified by system (see
    # Tasks::Links).
    def links(item)
      Links.extract([item.title, *body(item)],
                    shorthands: @link_shorthands, systems: @link_systems)
    end

    # The item's headline rendered from its fields, star-less: state, optional
    # priority cookie, title, trailing tag cluster (stored order). The single
    # rendered summary the CLI and TUI show — works for live and archive items.
    def headline(item)
      s = +"#{item.state} "
      s << "[##{item.priority}] " if item.priority
      s << item.title
      s << " :#{item.tags.join(":")}:" unless item.tags.empty?
      s
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

    # Set a specific date field (kind: :deadline or :scheduled), replacing an
    # existing one of that kind or adding it. INBOX items promote to TODO.
    # Backs the CLI `due` and `schedule` commands (the TUI uses reschedule!,
    # which picks whichever date the item already has).
    def set_date!(item, date, kind:)
      key = kind == :scheduled ? "SCHEDULED" : "DEADLINE"
      with_history("#{key.downcase} → #{date.iso8601}: #{item.title}") { set_date_impl(item, date, kind) }
    end

    # Transition an item to any state. Entering DONE/CANCELLED sets `closed`
    # (unless already set); leaving them clears it.
    def set_state!(item, state)
      with_history("state → #{state}: #{item.title}") { set_state_impl(item, state) }
    end

    # Remove a date (kind: :deadline, :scheduled, or nil for both). Returns
    # false if the item has no matching date to remove (in addition to the
    # usual stale-line-number case).
    def undate!(item, kind: nil)
      label = kind ? "remove #{kind}: #{item.title}" : "remove dates: #{item.title}"
      with_history(label) { undate_impl(item, kind) }
    end

    # Replace the title text, leaving state/priority/tags/dates untouched.
    # Same staleness contract as complete!.
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

    # Attach, replace, or (cookie == :off) remove a recurrence cookie on the
    # item. Returns false on a stale line, or when the item has no
    # SCHEDULED/DEADLINE date to repeat from. Same staleness contract.
    def set_recur!(item, cookie)
      label = cookie == :off ? "recur off: #{item.title}" : "recur #{cookie}: #{item.title}"
      with_history(label) { set_recur_impl(item, cookie) }
    end

    # Append a line to the item's body. Same staleness contract.
    def add_note!(item, text)
      text = utf8(text)
      with_history("note: #{item.title}") { add_note_impl(item, text) }
    end

    # Relocate the item's whole subtree under top-level section `section`
    # (matched case-insensitively, exact then substring). Returns the moved
    # record's new 1-based line number, or false if the item is stale or no
    # such section exists.
    def move!(item, section)
      with_history("move → #{section}: #{item.title}") { move_impl(item, section) }
    end

    # Create a new item under top-level section `project` (default "Inbox").
    # Returns the new record's 1-based line number, or false if the section
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

    # Ensure the item carries a stable id, returning it. Idempotent: an item
    # that already has one is returned untouched (no write). Post-migration ids
    # always exist; this is the repair path for a record somehow missing one.
    def ensure_id!(item)
      return item.id if item.id
      with_history("id: #{item.title}") { ensure_id_impl(item) }
    end

    # Items parsed from the archive file (source: :archive). Not cached — the
    # archive is read rarely (`list -x/-a`) and appended rarely.
    def archive_items
      parse_records(@archive).select { |r| r["type"] == "task" }.map { |r| build_item(r, :archive) }
    end

    private

    # -- reading ---------------------------------------------------------------

    # The staleness key for a file: [mtime, inode, size] — the same triple the
    # read cache keys on, so two out-of-band writes within one coarse mtime tick
    # (which bare mtime can't tell apart) still register as a change. nil when
    # the file is absent.
    def stat_key(path)
      st = File.stat(path)
      [st.mtime, st.ino, st.size]
    rescue Errno::ENOENT
      nil
    end

    # Read + parse a file into records via a small cache, so read surfaces that
    # ask per item (body search over the archive, links over every task) cost
    # one file read, not one per task. Keyed on (mtime, inode, size):
    # Atomic.write installs a fresh inode on every write, so even two writes in
    # one coarse-mtime tick can't serve stale records. Mutation impls bypass the
    # cache (fresh_records) to read under the lock.
    def parse_records(path)
      stat = File.stat(path)
      key = [stat.mtime, stat.ino, stat.size]
      @records_cache ||= {}
      cached = @records_cache[path]
      return cached[1] if cached && cached[0] == key
      records = Format.parse(File.read(path, encoding: "UTF-8")).records
      @records_cache[path] = [key, records]
      records
    rescue Errno::ENOENT
      []
    end

    # Uncached read for a mutation: the freshest records under the lock, so a
    # concurrent writer's change is never overwritten from a stale cache.
    def fresh_records(path)
      Format.parse(File.read(path, encoding: "UTF-8")).records
    rescue Errno::ENOENT
      []
    end

    def records_for(item)
      item.source == :archive ? parse_records(@archive) : parse_records(@org)
    end

    # Build an Item, coercing defensively so a hand-edited/malformed record can
    # never crash a reader (list, headline, resolve_ref) before Check gets to
    # report it: id → String, tags → Array of Strings. Check still flags the
    # underlying breakage; the coercion only keeps the readers alive.
    def build_item(rec, source)
      tags = rec["tags"]
      tags = tags.is_a?(Array) ? tags.map(&:to_s) : []
      Item.new(
        state: rec["state"], priority: rec["priority"], title: rec["title"],
        tags: tags,
        scheduled: to_date(rec["scheduled"]), deadline: to_date(rec["deadline"]),
        recur: rec["recur"], id: rec["id"]&.to_s, closed: to_date(rec["closed"]),
        line: rec["line"], source: source
      )
    end

    # Parse an ISO date string, returning nil for a missing, non-string, or
    # malformed value (Check reports the malformed one — readers must not crash).
    def to_date(str)
      return nil unless str.is_a?(String) && !str.empty?
      Date.iso8601(str)
    rescue ArgumentError, Date::Error
      nil
    end

    # Locate the item's record among `records`, preferring its stable id (so a
    # mutation still lands after lines shifted or the title changed out from
    # under us). Only an id-less item falls back to the record at its line whose
    # title still matches (the pre-id staleness guard): an item that HAS an id
    # no longer present in the file must fail the locate — never silently land on
    # whatever task now occupies that line. Returns the record hash, or nil.
    def locate(records, item)
      return records.find { |x| x["id"] == item.id } if item.id
      r = records.find { |x| x["line"] == item.line }
      r if r && r["type"] == "task" && r["title"] == item.title
    end

    # As locate, but returns the index into `records` (mutations that splice
    # subtrees need the position, not just the hash).
    def locate_index(records, item)
      return records.index { |x| x["id"] == item.id } if item.id
      i = records.index { |x| x["line"] == item.line }
      i if i && records[i]["type"] == "task" && records[i]["title"] == item.title
    end

    # -- id minting ------------------------------------------------------------

    # Every id present across a set of records (live or archive), for exclusion.
    def ids_of(records) = records.map { |r| r["id"] }.compact

    def archived_ids
      parse_records(@archive).map { |r| r["id"] }.compact
    end

    # A short, unique, CLI-typeable id (8 hex chars). Collisions are astronomically
    # unlikely, but cheap to exclude across BOTH files so a fresh id can't clash
    # with one already swept into the archive.
    def gen_id(taken)
      taken = taken.to_set
      loop do
        id = SecureRandom.hex(4)
        break id unless taken.include?(id)
      end
    end

    # -- subtree spans ---------------------------------------------------------

    # Index just past the subtree rooted at records[ri] (its record plus the
    # contiguous following records whose parent chain roots at it). The DFS
    # pre-order invariant guarantees a subtree is contiguous, so a single scan
    # — extend while the next record's parent is inside the subtree — finds it.
    def subtree_end(records, ri)
      ids = Set[records[ri]["id"]]
      j = ri + 1
      while j < records.length && (p = records[j]["parent"]) && ids.include?(p)
        ids << records[j]["id"]
        j += 1
      end
      j
    end

    # Close every OPEN task inside the subtree rooted at records[ri], excluding
    # the root itself — the cascade behind completing a parent: finishing a
    # project finishes its open work. Each open descendant (state in
    # OPEN_STATES) goes DONE with today's `closed`, drops the DEFER_TAG, and has
    # its `recur` cookie retired outright — a cascaded recurring descendant is
    # NOT advanced (no date roll, no body log): completing the parent completes
    # it. DONE/CANCELLED descendants are left untouched (their prior `closed`
    # stands). Returns the touched records' `line` values, in file order.
    def close_open_descendants(records, ri)
      rj = subtree_end(records, ri)
      today = Date.today.iso8601
      records[(ri + 1)...rj].each_with_object([]) do |rec, touched|
        next unless rec["type"] == "task" && OPEN_STATES.include?(rec["state"])
        rec["state"] = "DONE"
        rec["closed"] = today
        rec["tags"] = (rec["tags"] || []) - [DEFER_TAG]
        rec.delete("recur")
        touched << rec["line"]
      end
    end

    # Index of the top-level ("* ") section matching `name` — a section record
    # with no parent, exact title then substring (case-insensitive) — or nil.
    def find_section(records, name)
      want = name.strip.downcase
      top = records.each_index.select { |i| records[i]["type"] == "section" && !records[i]["parent"] }
      top.find { |i| records[i]["title"].to_s.downcase == want } ||
        top.find { |i| records[i]["title"].to_s.downcase.include?(want) }
    end

    # -- write plumbing --------------------------------------------------------

    def write_records(path, records)
      Atomic.write(path, Format.dump(records))
    end

    def meta_record = { "type" => "meta", "version" => Format::VERSION }

    # Append `line` to an existing body string (or start one).
    def append_body(body, line)
      body.nil? || body.empty? ? line : "#{body}\n#{line}"
    end

    # User-supplied text (ARGV, TUI input) is tagged with the process locale,
    # which is ASCII-8BIT/BINARY when LANG is unset. The bytes are UTF-8 — the
    # terminal emits UTF-8 — so re-tag them; otherwise joining a BINARY string
    # into UTF-8 file text raises Encoding::CompatibilityError. Genuinely invalid
    # bytes are left as-is so they fail loudly rather than corrupt the store.
    def utf8(str)
      return str if str.nil? || str.encoding == Encoding::UTF_8
      recoded = str.dup.force_encoding(Encoding::UTF_8)
      recoded.valid_encoding? ? recoded : str
    end

    # Serialize the read-modify-write of a mutation across *tasks* processes (the
    # CLI and the TUI): without it, two of them could interleave their read/write
    # and silently drop one change. The lock is an advisory flock on a sidecar
    # next to the real file (".tasks.jsonl.lock"), so every process reaches the
    # same inode regardless of how the path was spelled. It does NOT constrain
    # out-of-band editors; those are caught by the post-write Check and the
    # journal's conflict detection, and Atomic.write keeps even an unlocked
    # concurrent read from ever tearing.
    def with_lock
      File.open(lock_path, File::RDWR | File::CREAT, 0o644) do |f|
        f.flock(File::LOCK_EX)
        yield
      end
    end

    # A per-file lock sidecar (".tasks.jsonl.lock") beside the resolved live
    # file. Journal.canonical resolves the symlink (so two spellings of the same
    # file lock in common) and is ENOENT-safe.
    def lock_path
      target = Journal.canonical(@org)
      File.join(File.dirname(target), ".#{File.basename(target)}.lock")
    end

    # A nil org means "no file yet" — the first-run state before `capture`
    # bootstraps the store. restore mirrors it by deleting the file, the same
    # way it handles a nil archive.
    def snapshot
      {
        org: File.exist?(@org) ? File.read(@org, encoding: "UTF-8") : nil,
        archive: File.exist?(@archive) ? File.read(@archive, encoding: "UTF-8") : nil,
      }
    end

    def restore(snap)
      if snap[:org].nil?
        File.delete(@org) if File.exist?(@org)
      else
        Atomic.write(@org, snap[:org])
      end
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
        before = snapshot
        restore(step[:target])
        # A journaled snapshot could pre-date a repair: restoring it would write
        # a state that fails today's invariants. Gate the restored live file the
        # same way with_history gates a forward mutation; on failure put the
        # pre-undo state back and refuse (reusing the :conflict shape callers
        # already handle). A nil target org is the empty first-run state — no
        # file to validate — so skip the gate there.
        if step[:target][:org] && !Check.check(@org).ok?
          restore(before)
          return [:conflict, step[:label]]
        end
        step[:commit].call
        [:ok, step[:label]]
      end
    end

    # Record history only when the mutation actually wrote (truthy, nonzero) AND
    # changed the file — an idempotent no-op (e.g. adding a tag already present)
    # succeeds but must not burn an undo slot with a label that reverts nothing.
    # The whole read-modify-write runs under the lock so a concurrent writer
    # can't slip between the steps.
    def with_history(label)
      with_lock do
        @last_rollback = nil
        before = snapshot
        result = yield
        if result && result != 0
          # post-write invariant: a mutation must never mangle either file (the
          # sweep writes the archive too). If it would, record why, roll back —
          # both files are snapshotted — and report failure instead.
          if (reason = post_write_failure)
            @last_rollback = reason
            restore(before)
            return result.is_a?(Integer) ? 0 : false
          end
          after = snapshot
          @journal.record(label: label, before: before, after: after) unless after == before
        end
        result
      end
    end

    # The first Check error summary if the live file — or the archive, when it
    # exists (sweep writes it) — fails validation after a write; nil when both
    # are clean. Drives the rollback and the CLI's "run `tasks check`" hint.
    def post_write_failure
      [@org, (@archive if File.exist?(@archive))].compact.each do |path|
        res = Check.check(path)
        return res.errors.first&.last || "validation failed" unless res.ok?
      end
      nil
    end

    # -- mutation impls --------------------------------------------------------
    #
    # Every impl: read fresh records under the lock, locate the target record by
    # id (fallback: line + title guard), mutate its hash fields, and write the
    # whole record list back through Format. No line-walking anywhere.

    def complete_impl(item)
      # A recurring task rolls its date forward and stays open instead of
      # closing — completing an occurrence, not the task.
      return advance_recurrence_impl(item) if item.recurring?

      records = fresh_records(@org)
      ri = locate_index(records, item) or return false
      rec = records[ri]
      rec["state"] = "DONE"
      rec["closed"] = Date.today.iso8601
      # A completed task is no longer someday/maybe — drop the defer marker.
      rec["tags"] = (rec["tags"] || []) - [DEFER_TAG]
      # Completing a parent completes its open descendants — one journal entry,
      # one undo. Returns the touched line numbers (root first, then children in
      # file order); an Array is truthy and != 0 so with_history still records.
      lines = [rec["line"]] + close_open_descendants(records, ri)
      write_records(@org, records)
      reload!
      lines
    end

    def set_priority_impl(item, pri)
      records = fresh_records(@org)
      rec = locate(records, item) or return false
      if pri then rec["priority"] = pri else rec.delete("priority") end
      write_records(@org, records)
      reload!
      true
    end

    # Update the item's DEADLINE (or SCHEDULED, if that's all it has). Items
    # with neither get a DEADLINE. Delegates to set_date_impl with the kind.
    def reschedule_impl(item, date)
      kind = if item.deadline     then :deadline
             elsif item.scheduled then :scheduled
             else                      :deadline
             end
      set_date_impl(item, date, kind)
    end

    # Set/replace a specific date field, adding it if absent. Sole date-write
    # path — reschedule_impl infers the kind and delegates here.
    def set_date_impl(item, date, kind)
      records = fresh_records(@org)
      rec = locate(records, item) or return false
      rec[kind == :scheduled ? "scheduled" : "deadline"] = date.iso8601
      # A dated task has been processed — promote it out of the inbox.
      rec["state"] = "TODO" if rec["state"] == "INBOX"
      write_records(@org, records)
      reload!
      true
    end

    # Remove the scheduled and/or deadline field(s). Returns false if nothing
    # matched. Clears a now-meaningless recur if no date remains.
    def undate_impl(item, kind)
      records = fresh_records(@org)
      rec = locate(records, item) or return false
      fields = case kind
               when :scheduled then ["scheduled"]
               when :deadline  then ["deadline"]
               else                 %w[scheduled deadline]
               end
      removed = fields.any? { |f| rec[f] }
      return false unless removed
      fields.each { |f| rec.delete(f) }
      rec.delete("recur") unless rec["scheduled"] || rec["deadline"]
      write_records(@org, records)
      reload!
      true
    end

    def retitle_impl(item, new_title)
      records = fresh_records(@org)
      rec = locate(records, item) or return false
      rec["title"] = new_title.strip
      write_records(@org, records)
      reload!
      true
    end

    def set_tags_impl(item, add, remove)
      records = fresh_records(@org)
      rec = locate(records, item) or return false
      tags = (rec["tags"] || []).reject { |t| remove.include?(t) }
      add.each { |t| tags << t unless tags.include?(t) }
      rec["tags"] = tags
      write_records(@org, records)
      reload!
      true
    end

    def add_note_impl(item, text)
      records = fresh_records(@org)
      rec = locate(records, item) or return false
      rec["body"] = append_body(rec["body"], text.strip)
      write_records(@org, records)
      reload!
      true
    end

    def set_state_impl(item, new_state)
      # Completing a recurring task advances the occurrence rather than closing
      # it — the same rule complete_impl applies, so `done` and `state … DONE`
      # agree. CANCELLED still truly closes (stops the recurrence).
      return advance_recurrence_impl(item) if new_state == "DONE" && item.recurring?

      records = fresh_records(@org)
      ri = locate_index(records, item) or return false
      rec = records[ri]
      old_state = rec["state"]
      rec["state"] = new_state

      lines = [rec["line"]]
      if DONE_STATES.include?(new_state) && !DONE_STATES.include?(old_state)
        rec["tags"] = (rec["tags"] || []) - [DEFER_TAG]
        rec["closed"] ||= Date.today.iso8601
        # Cascade only on a real transition INTO DONE — completing a parent
        # completes its open descendants. CANCELLED closes the root alone.
        lines.concat(close_open_descendants(records, ri)) if new_state == "DONE"
      elsif DONE_STATES.include?(old_state) && !DONE_STATES.include?(new_state)
        rec.delete("closed")
      end

      write_records(@org, records)
      reload!
      lines
    end

    # Set/replace/remove the recurrence cookie. Requires a date to repeat from.
    def set_recur_impl(item, cookie)
      records = fresh_records(@org)
      rec = locate(records, item) or return false
      return false unless rec["scheduled"] || rec["deadline"]
      if cookie == :off then rec.delete("recur") else rec["recur"] = cookie end
      write_records(@org, records)
      reload!
      true
    end

    # Complete a recurring occurrence: roll ONLY the date the cookie owns forward
    # and leave the task open (no DONE, no closed). The schema's single `recur`
    # field belongs to the DEADLINE when present, else the SCHEDULED (the
    # migrator discarded a SCHEDULED cookie when both stamps had one). Rolling
    # the other date too would rewrite a fixed deadline the user never repeated
    # and desync a `++` catch-up — old org semantics never rolled a stamp that
    # carried no repeater. Logs the completion in the body so history survives.
    # Returns false if stale or there's no date to repeat from.
    def advance_recurrence_impl(item)
      records = fresh_records(@org)
      rec = locate(records, item) or return false
      cookie = rec["recur"]
      return false unless cookie

      field = rec["deadline"] ? "deadline" : ("scheduled" if rec["scheduled"])
      base = field && to_date(rec[field]) or return false
      rec[field] = Recur.next_date(cookie, from: base).iso8601

      # A completed occurrence is no longer someday/maybe — drop the defer
      # marker, matching complete_impl.
      rec["tags"] = (rec["tags"] || []) - [DEFER_TAG] if rec["tags"]
      rec["body"] = append_body(rec["body"], "- Did [#{Date.today}].")
      write_records(@org, records)
      reload!
      # Returns the touched line as a one-element Array so complete_impl and
      # set_state_impl hand callers a uniform lines array (no cascade — a
      # recurring parent rolls forward and does not complete its descendants).
      [rec["line"]]
    end

    def move_impl(item, section)
      records = fresh_records(@org)
      ri = locate_index(records, item) or return false
      rj = subtree_end(records, ri)
      subtree = records[ri...rj].map(&:dup)
      rest = records[0...ri] + records[rj..]

      ti = find_section(rest, section) or return false
      subtree[0]["parent"] = rest[ti]["id"]
      insert_at = subtree_end(rest, ti)
      rest[insert_at, 0] = subtree
      write_records(@org, rest)
      reload!
      insert_at + 1
    end

    def capture_impl(text, due, scheduled, priority, tags, state, project)
      records = fresh_records(@org)
      if records.empty?
        # First run: a missing or empty file. Bootstrap a brand-new store — a
        # meta line plus the target section (default "Inbox") — then insert into
        # it. (A NAMED --project missing from a NON-empty file still fails below.)
        records = [meta_record,
                   { "type" => "section", "id" => gen_id(archived_ids),
                     "title" => (project || "Inbox").strip }]
        si = records.length - 1
      else
        si = find_section(records, project || "Inbox") or return false
      end

      rec = { "type" => "task", "id" => gen_id(ids_of(records) + archived_ids),
              "parent" => records[si]["id"], "state" => state }
      rec["priority"] = priority if priority
      rec["title"] = text.strip
      rec["tags"] = tags unless tags.empty?
      rec["scheduled"] = scheduled.iso8601 if scheduled
      rec["deadline"] = due.iso8601 if due
      rec["body"] = "Captured [#{Date.today}]."

      insert_at = subtree_end(records, si)
      records[insert_at, 0] = [rec]
      write_records(@org, records)
      reload!
      insert_at + 1
    end

    # Move every DONE/CANCELLED task's whole subtree to the archive file. The
    # swept root drops its `parent` and gains `archived: today`; descendants
    # keep their internal parents. Returns the count of roots swept.
    def archive_swept_impl
      records = fresh_records(@org)
      kept = []
      moved = []
      roots = 0
      i = 0
      while i < records.length
        r = records[i]
        if r["type"] == "task" && DONE_STATES.include?(r["state"])
          j = subtree_end(records, i)
          group = records[i...j].map(&:dup)
          group[0].delete("parent")
          group[0]["archived"] = Date.today.iso8601
          moved.concat(group)
          roots += 1
          i = j
        else
          kept << r
          i += 1
        end
      end
      return 0 if moved.empty?

      write_records(@org, kept)
      arch = File.exist?(@archive) ? fresh_records(@archive) : []
      arch = [meta_record] if arch.empty?
      arch.concat(moved)
      write_records(@archive, arch)
      reload!
      roots
    end

    def ensure_id_impl(item)
      records = fresh_records(@org)
      rec = locate(records, item) or return false
      return rec["id"] if rec["id"] && !rec["id"].empty?
      id = gen_id(ids_of(records) + archived_ids)
      rec["id"] = id
      write_records(@org, records)
      reload!
      id
    end
  end
end
