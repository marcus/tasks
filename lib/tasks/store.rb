# frozen_string_literal: true

require "date"
require "digest"
require "securerandom"
require "set"
require_relative "atomic"
require_relative "check"
require_relative "edit_snapshot"
require_relative "format"
require_relative "journal"
require_relative "links"
require_relative "patch_result"
require_relative "quadrants"
require_relative "recur"
require_relative "task_patch"
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

    # A coherent, immutable view of the task files. A caller can hold one of
    # these while rendering a task and safely ask for its body, links, or tree
    # node without mixing fields from a later file reload. Store builds it
    # while holding the same sidecar lock as mutations.
    class ReadSnapshot
      attr_reader :items, :archive_items, :tree, :nodes_by_line, :live_records,
                  :archive_records, :live_stat, :archive_stat

      def initialize(live_records:, live_stat:, archive_records:, archive_stat:,
                     archive_loaded:, item_builder:, link_shorthands:, link_systems:)
        @live_records = immutable_copy(live_records)
        @archive_records = immutable_copy(archive_records)
        @live_stat = live_stat&.freeze
        @archive_stat = archive_stat&.freeze
        @archive_loaded = archive_loaded
        @link_shorthands = immutable_copy(link_shorthands)
        @link_systems = immutable_copy(link_systems)

        @items = immutable_items(@live_records, :live, item_builder)
        @archive_items = immutable_items(@archive_records, :archive, item_builder)
        by_line = @items.to_h { |item| [item.line, item] }
        @tree = Tree.build(@live_records, by_line)
        @nodes_by_line = {}.tap do |map|
          @tree.each { |root| root.each { |node| map[node.line] = node } }
        end
        freeze_tree(@tree)
        @nodes_by_line.freeze
        freeze
      end

      def archive_loaded? = @archive_loaded

      # The item's own body lines. A snapshot deliberately never falls through
      # to the current Store contents: held items stay coherent with their
      # title, tree node, and link extraction even after a later reload.
      def body(item)
        record = locate(records_for(item), item)
        value = record && record["body"]
        value.is_a?(String) && !value.empty? ? value.split("\n") : []
      end

      def links(item)
        Links.extract([item.title, *body(item)],
                      shorthands: @link_shorthands, systems: @link_systems)
      end

      # The live tree node for an item, or nil for archive items. Prefer the
      # record's current line, then recover stable-id items after line shifts;
      # id-less held items never retarget a different title.
      def node_for(item)
        return nil unless item.source == :live
        node = @nodes_by_line[item.line]
        if node&.item
          return node if item.id ? node.item.id == item.id : node.item.title == item.title
        end
        item.id ? @nodes_by_line.each_value.find { |candidate| candidate.item&.id == item.id } : nil
      end

      private

      def records_for(item)
        item.source == :archive ? @archive_records : @live_records
      end

      def locate(records, item)
        return records.find { |record| record["id"] == item.id } if item.id
        record = records.find { |candidate| candidate["line"] == item.line }
        record if record && record["type"] == "task" && record["title"] == item.title
      end

      def immutable_items(records, source, item_builder)
        records.select { |record| record["type"] == "task" }.map do |record|
          item = item_builder.call(record, source)
          item.tags.freeze
          item.scheduled&.freeze
          item.deadline&.freeze
          item.closed&.freeze
          item.freeze
        end.freeze
      end

      def immutable_copy(value)
        case value
        when Hash
          value.each_with_object({}) do |(key, child), copy|
            copy[immutable_copy(key)] = immutable_copy(child)
          end.freeze
        when Array
          value.map { |child| immutable_copy(child) }.freeze
        when String
          value.dup.freeze
        else
          value.freeze
        end
      end

      def freeze_tree(nodes)
        nodes.each do |node|
          freeze_tree(node.children)
          node.body.each(&:freeze)
          node.body.freeze
          node.children.freeze
          node.freeze
        end
        nodes.freeze
      end
    end

    ArchiveBlock = Struct.new(:root_id, :root_title, :open_ids, :open_titles, keyword_init: true)
    ArchivePreview = Struct.new(:roots, :descendants, :blocks, :candidate_ids, :fingerprint, keyword_init: true) do
      def total = roots + descendants
      def blocked? = !blocks.empty?
      def blocked_roots = blocks.length
      def open_descendants = blocks.sum { |block| block.open_ids.length }
    end
    ArchiveRefusal = Struct.new(:reason, :preview, :details, keyword_init: true)

    ArchivePlan = Struct.new(:kept, :moved, :preview, keyword_init: true)
    private_constant :ArchivePlan

    # Semantic tag marking a task as deferred (someday/maybe). See Item#deferred?.
    DEFER_TAG = "defer"

    attr_reader :org, :archive

    # The nesting depth cap enforced by capture/move --under (see task_depth).
    # Resolved from config (Tasks::Config); Check stays depth-agnostic so deeper
    # legacy files still validate and roll back cleanly.
    attr_reader :max_depth

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
                   links: {}, link_systems: {}, max_depth: Tree::DEFAULT_MAX_DEPTH)
      @org = org
      @archive = archive
      @max_depth = max_depth
      @stat = nil
      @archive_stat = nil
      @cache = nil
      @records = nil
      @read_snapshot = nil
      @link_shorthands = links
      @link_systems = link_systems
      @journal = Journal.new(dir: journal_dir || Journal.dir_for(org), org: org, limit: undo_limit)
    end

    def items
      current_read_snapshot.items
    end

    def changed?
      stat_key(@org) != @stat
    end

    # Capture live records and, when requested, archive records together under
    # the Store lock. The result never changes in place; request/render code
    # should retain this object while it needs a coherent multi-field read.
    def read_snapshot(include_archive: false)
      with_lock do
        live_records, live_stat = fresh_records_with_stat(@org)
        archive_records, archive_stat = if include_archive
                                          fresh_records_with_stat(@archive)
                                        else
                                          [[], nil]
                                        end
        ReadSnapshot.new(
          live_records: live_records, live_stat: live_stat,
          archive_records: archive_records, archive_stat: archive_stat,
          archive_loaded: include_archive, item_builder: method(:build_item),
          link_shorthands: @link_shorthands, link_systems: @link_systems
        )
      end
    end

    def reload!(include_archive: false)
      snapshot = read_snapshot(include_archive: include_archive)
      @read_snapshot = snapshot
      @stat = snapshot.live_stat
      @archive_stat = snapshot.archive_stat if snapshot.archive_loaded?
      @records = snapshot.live_records
      @cache = snapshot.items
      @tree = snapshot.tree
      @nodes_by_line = snapshot.nodes_by_line
      self
    end

    # The structural index (Tasks::Tree) over the live file: sections, tasks,
    # and subtasks as nested nodes built from `parent` pointers, each with its
    # own body lines. Rebuilt whenever the file changes (items() drives the
    # staleness check).
    def tree
      current_read_snapshot.tree
    end

    # The tree node for an item (nil for archive items — the tree indexes the
    # live file only). O(1) via a line-keyed map; if the item carries an id and
    # the node at its line doesn't match (lines shifted underneath a held item),
    # fall back to finding its node by id — same preference locate applies.
    def node_for(item)
      current_read_snapshot.node_for(item)
    end

    # Line-number => node map over the whole tree, built once per tree build so
    # per-item lookups (body, project) are O(1), not a tree walk each.
    def nodes_by_line
      current_read_snapshot.nodes_by_line
    end

    # The item's own body lines — the record's `body` string split back into
    # lines. This is the text body search and link extraction run over; it never
    # includes a child's body (children are separate records). Works for live
    # AND archive items (same record lookup for both).
    def body(item)
      current_read_snapshot(include_archive: item.source == :archive).body(item)
    end

    # Links found in the item's title and body — org links, bare URLs, and
    # configured shorthands (jira:OPS-1) — classified by system (see
    # Tasks::Links).
    def links(item)
      current_read_snapshot(include_archive: item.source == :archive).links(item)
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
    # `under:` (an Item) nests the new task as the last child of that task
    # instead of filing it under a section; it is mutually exclusive with
    # `project` (the CLI enforces that — the impl just prefers `under`). A capture
    # `under` a parent already at the depth cap returns :too_deep before writing.
    def capture!(text, due: nil, scheduled: nil, priority: nil, tags: [], state: "INBOX", project: nil, under: nil)
      text = utf8(text)
      tags = tags.map { |t| utf8(t) }
      with_history("capture: #{text}") { capture_impl(text, due, scheduled, priority, tags, state, project, under) }
    end

    # Nest the item's whole subtree as the last child of `parent_item`. Returns
    # the moved root's new 1-based line, or :cycle (parent is inside the moved
    # subtree, self included), :too_deep (would exceed max_depth), or false
    # (either ref went stale). A move that keeps depth within the cap only.
    def move_under!(item, parent_item)
      with_history("nest under #{parent_item.title}: #{item.title}") { move_under_impl(item, parent_item) }
    end

    # Unnest the item's whole subtree to the end of its nearest ancestor section
    # (top level). Never depth-checked — it can only reduce depth. Returns the
    # new 1-based line, 0 when the item is already top-level (a no-op that burns
    # no undo slot), or false if the item went stale.
    def move_top!(item)
      with_history("unnest: #{item.title}") { move_top_impl(item) }
    end

    def archive_swept!(expected_preview: nil)
      with_history("archive sweep") { archive_swept_impl(expected_preview) }
    end

    # A read-only summary of what the next archive sweep would move. Roots are
    # the DONE/CANCELLED tasks selected by the sweep; descendants excludes those
    # roots. Blocks identify closed roots whose subtree still contains open work.
    def archive_preview
      with_lock { archive_plan(fresh_records(@org)).preview }
    end

    # Build the editor's exact values and semantic conflict baselines from the
    # live file while holding the same lock mutations use. The target may be a
    # stable id, an Item, or any object responding to #id. Missing ids never
    # fall back to a line number: an edit session must not retarget another row.
    # Invalid live bytes/schema return nil, matching the missing-target shape;
    # callers that need the diagnostic use patch_task!, whose failure is typed.
    def edit_snapshot(target)
      with_lock do
        return nil unless Check.check(@org).ok?
        records = fresh_records(@org)
        ri = locate_stable_index(records, stable_id(target))
        ri && build_edit_snapshot(records, ri)
      rescue JSON::GeneratorError, EncodingError, ArgumentError
        nil
      end
    end

    # Apply one field-owned semantic change. Every outcome is typed; only :ok
    # writes or records history, and the write/check/journal sequence rolls back
    # to the exact prior bytes on any validation or writer failure.
    def patch_task!(patch)
      unless patch.respond_to?(:id) && patch.respond_to?(:field) &&
             patch.respond_to?(:value) && patch.respond_to?(:expected)
        return PatchResult.new(status: :invalid, errors: ["expected a Tasks::TaskPatch"])
      end

      with_lock do
        @last_rollback = nil
        before = snapshot
        current = nil
        begin
          # Check raw validity before parsing/building: Format.parse assumes a
          # valid UTF-8 String, while Check deliberately contains bad bytes.
          preflight = Check.check(@org)
          unless preflight.ok?
            return PatchResult.new(status: :invalid,
                                   errors: preflight.errors.map(&:last))
          end

          records = fresh_records(@org)
          ri = locate_stable_index(records, patch.id)
          return PatchResult.new(status: :missing) unless ri

          current = build_edit_snapshot(records, ri)
          field = normalize_patch_field(patch.field)
          unless EditSnapshot::FIELDS.include?(field)
            return PatchResult.new(status: :invalid, snapshot: current,
                                   errors: ["unknown editable field #{patch.field.inspect}"])
          end

          unless confirmation_matches?(current, patch.respond_to?(:confirmation) && patch.confirmation)
            return PatchResult.new(status: :conflict, snapshot: current)
          end

          actual = current.expected_for(field)
          unless semantic_patch_equal?(field, actual, patch.expected)
            return PatchResult.new(status: :conflict, snapshot: current)
          end

          original_records = Format.dump(records)
          applied = apply_semantic_patch(records, ri, field, patch.value)
          if applied[:status] != :ok
            return PatchResult.new(status: applied[:status], snapshot: current,
                                   errors: applied[:errors] || [], summary: applied[:summary])
          end
          proposed_records = Format.dump(records)
        rescue JSON::GeneratorError, EncodingError, ArgumentError => e
          return PatchResult.new(status: :invalid, snapshot: current,
                                 errors: [safe_patch_error(e)])
        end

        if proposed_records == original_records
          return PatchResult.new(status: :no_change, snapshot: current,
                                 summary: applied[:summary])
        end

        label = "edit #{field}: #{current.title}"
        begin
          write_records(@org, records)
          if (reason = post_write_failure)
            @last_rollback = reason
            restore(before)
            return PatchResult.new(status: :invalid,
                                   snapshot: restored_edit_snapshot(patch.id),
                                   errors: [reason])
          end
          after = snapshot
          @journal.record(label: label, before: before, after: after,
                          coalesce_key: patch.coalesce_key)
          reload!
          fresh_ri = locate_stable_index(@records, patch.id)
          PatchResult.new(
            status: :ok,
            snapshot: fresh_ri && build_edit_snapshot(@records, fresh_ri),
            touched_ids: applied[:touched_ids],
            summary: applied[:summary]
          )
        rescue StandardError => e
          @last_rollback = safe_patch_error(e)
          restore(before)
          PatchResult.new(status: :invalid,
                          snapshot: restored_edit_snapshot(patch.id),
                          errors: [safe_patch_error(e)])
        end
      end
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
      current_read_snapshot(include_archive: true).archive_items
    end

    private

    # -- reading ---------------------------------------------------------------

    # The cached Store-facing convenience reads all come from one snapshot. A
    # caller that needs a stable multi-step read should keep the public
    # #read_snapshot result instead; this cache only preserves the existing
    # Store surface and its reload-on-live-change behavior.
    def current_read_snapshot(include_archive: false)
      needs_archive = include_archive &&
                      (@read_snapshot.nil? || !@read_snapshot.archive_loaded? || archive_changed?)
      reload!(include_archive: include_archive) if @read_snapshot.nil? || changed? || needs_archive
      @read_snapshot
    end

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

    def archive_changed?
      stat_key(@archive) != @archive_stat
    end

    # A snapshot must capture the bytes and their staleness key from the same
    # file descriptor. If Atomic.write installs a newer inode after this open,
    # the later stat comparison notices it rather than claiming old bytes are
    # current. Mutations intentionally retain their existing fresh_records
    # path; this helper belongs only to immutable read snapshots.
    def fresh_records_with_stat(path)
      File.open(path, "r", encoding: "UTF-8") do |file|
        stat = file.stat
        records = Format.parse(file.read).records
        [records, [stat.mtime, stat.ino, stat.size]]
      end
    rescue Errno::ENOENT
      [[], nil]
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

    def stable_id(target)
      target.respond_to?(:id) ? target.id : target
    end

    def locate_stable_index(records, id)
      return nil unless id.is_a?(String) && !id.empty?
      records.index { |record| record["type"] == "task" && record["id"] == id }
    end

    def normalize_patch_field(field)
      field = field.to_sym
      field == :recur ? :recurrence : field
    rescue NoMethodError
      field
    end

    def semantic_tags(rec)
      tags = rec["tags"]
      tags.is_a?(Array) ? tags.select { |tag| tag.is_a?(String) } : []
    end

    def build_edit_snapshot(records, ri)
      rec = records[ri]
      tags = semantic_tags(rec)
      contexts = tags.select { |tag| tag.start_with?("@") }
      ordinary_tags = tags.reject { |tag| tag.start_with?("@") || tag == DEFER_TAG }
      parent = records.find { |record| record["id"] == rec["parent"] }
      values = {
        title: rec["title"],
        priority: rec["priority"],
        deferred: tags.include?(DEFER_TAG),
        scheduled: to_date(rec["scheduled"]),
        deadline: to_date(rec["deadline"]),
        recurrence: rec["recur"],
        contexts: contexts,
        tags: ordinary_tags,
        body: rec["body"].is_a?(String) ? rec["body"] : "",
        location: rec["parent"],
        state: rec["state"],
      }
      EditSnapshot.new(
        id: rec["id"], title: values[:title], priority: values[:priority],
        deferred: values[:deferred], scheduled: values[:scheduled],
        deadline: values[:deadline], recurrence: values[:recurrence],
        contexts: contexts, tags: ordinary_tags, body: values[:body],
        parent: rec["parent"], state: rec["state"], closed: to_date(rec["closed"]),
        baselines: values,
        fingerprints: {
          location: location_fingerprint(records, ri),
          state: lifecycle_fingerprint(records, ri),
        },
        metadata: {
          line: rec["line"],
          parent_type: parent && parent["type"],
          parent_title: parent && parent["title"],
          subtree_ids: records[ri...subtree_end(records, ri)].filter_map { |record| record["id"] },
        }
      )
    end

    def location_fingerprint(records, ri)
      rec = records[ri]
      rj = subtree_end(records, ri)
      structural = records[ri...rj].map do |record|
        [record["type"], record["id"], record["parent"]]
      end
      siblings = records.filter_map do |record|
        record["id"] if record["parent"] == rec["parent"]
      end
      semantic_digest([rec["parent"], siblings, structural])
    end

    def lifecycle_fingerprint(records, ri)
      rj = subtree_end(records, ri)
      owned = records[ri...rj].filter_map do |record|
        next unless record["type"] == "task"
        tags = semantic_tags(record)
        [record["id"], record["parent"], record["state"], record["closed"],
         record["scheduled"], record["deadline"], record["recur"],
         tags.include?(DEFER_TAG)]
      end
      semantic_digest(owned)
    end

    def semantic_digest(value)
      Digest::SHA256.hexdigest(JSON.generate(value))
    end

    def semantic_patch_equal?(field, actual, expected)
      case field
      when :scheduled, :deadline
        normalized = normalize_patch_date(expected)
        normalized != :invalid && actual == normalized
      when :contexts, :tags
        actual == Array(expected)
      else
        actual == expected
      end
    end

    # High-impact confirmations may own semantic inputs beyond the focused
    # field. Validate those expectations under the mutation lock so a change
    # between the prompt and confirmation can never erase a concurrent update.
    def confirmation_matches?(snapshot, confirmation)
      return true unless confirmation.is_a?(Hash)

      expected = confirmation[:expected] || confirmation["expected"]
      return true unless expected.is_a?(Hash)

      structured = %i[owned values predicates].any? do |key|
        expected.key?(key) || expected.key?(key.to_s)
      end
      owned = structured ? confirmation_section(expected, :owned, {}) : expected
      values = structured ? confirmation_section(expected, :values, {}) : {}
      predicates = structured ? confirmation_section(expected, :predicates, {}) : {}
      return false unless owned.is_a?(Hash) && values.is_a?(Hash) && predicates.is_a?(Hash)

      owned.all? do |field, baseline|
        normalized = normalize_patch_field(field)
        EditSnapshot::FIELDS.include?(normalized) &&
          semantic_patch_equal?(normalized, snapshot.expected_for(normalized), baseline)
      end && values.all? do |field, baseline|
        normalized = normalize_patch_field(field)
        EditSnapshot::FIELDS.include?(normalized) &&
          semantic_patch_equal?(normalized, snapshot[normalized], baseline)
      end && confirmation_predicates_match?(snapshot, predicates)
    end

    def confirmation_section(expected, key, fallback)
      return expected[key] if expected.key?(key)
      return expected[key.to_s] if expected.key?(key.to_s)

      fallback
    end

    def confirmation_predicates_match?(snapshot, predicates)
      predicates.all? do |name, expected|
        case normalize_patch_field(name)
        when :any_live_date
          expected == !!(snapshot.scheduled || snapshot.deadline)
        when :date_presence
          expected.is_a?(Hash) && expected.all? do |field, present|
            normalized = normalize_patch_field(field)
            %i[scheduled deadline].include?(normalized) &&
              (present == true || present == false) &&
              present == !snapshot[normalized].nil?
          end
        else
          false
        end
      end
    end

    def normalize_patch_date(value)
      return nil if value.nil? || value == ""
      return value if value.is_a?(Date)
      return Date.iso8601(value) if value.is_a?(String)
      :invalid
    rescue ArgumentError, Date::Error
      :invalid
    end

    def restored_edit_snapshot(id)
      records = @records || fresh_records(@org)
      ri = locate_stable_index(records, id)
      ri && build_edit_snapshot(records, ri)
    end

    def apply_semantic_patch(records, ri, field, value)
      case field
      when :title      then patch_title(records, ri, value)
      when :priority   then patch_priority(records, ri, value)
      when :deferred   then patch_deferred(records, ri, value)
      when :scheduled  then patch_date(records, ri, value, :scheduled)
      when :deadline   then patch_date(records, ri, value, :deadline)
      when :recurrence then patch_recurrence(records, ri, value)
      when :contexts   then patch_tag_slice(records, ri, value, :contexts)
      when :tags       then patch_tag_slice(records, ri, value, :tags)
      when :body       then patch_body(records, ri, value)
      when :location   then patch_location(records, ri, value)
      when :state      then patch_state(records, ri, value)
      end
    end

    def patch_ok(rec, touched_ids: nil, summary: nil)
      { status: :ok, touched_ids: touched_ids || [rec["id"]], summary: summary }
    end

    def patch_invalid(message)
      { status: :invalid, errors: [message] }
    end

    def patch_title(records, ri, value)
      return patch_invalid("title must be text") unless value.is_a?(String)
      title = utf8(value).strip
      return patch_invalid("title cannot be blank") if title.empty?
      records[ri]["title"] = title
      patch_ok(records[ri])
    end

    def patch_priority(records, ri, value)
      return patch_invalid("priority must be A, B, C, or nil") unless value.nil? || Check::PRIORITIES.include?(value)
      value ? records[ri]["priority"] = value : records[ri].delete("priority")
      patch_ok(records[ri])
    end

    def patch_deferred(records, ri, value)
      return patch_invalid("deferred must be true or false") unless value == true || value == false
      rec = records[ri]
      tags = semantic_tags(rec)
      return patch_ok(rec) if tags.include?(DEFER_TAG) == value
      if value
        tags << DEFER_TAG
      else
        tags.delete(DEFER_TAG)
      end
      replace_optional(rec, "tags", tags)
      patch_ok(rec)
    end

    def patch_date(records, ri, value, kind)
      date = normalize_patch_date(value)
      return patch_invalid("#{kind} must be a date or nil") if date == :invalid
      rec = records[ri]
      key = kind.to_s
      if date
        rec[key] = date.iso8601
        rec["state"] = "TODO" if rec["state"] == "INBOX"
      else
        rec.delete(key)
        rec.delete("recur") unless rec["scheduled"] || rec["deadline"]
      end
      patch_ok(rec)
    end

    def patch_recurrence(records, ri, value)
      rec = records[ri]
      return patch_invalid("recurrence requires a scheduled date or deadline") unless rec["scheduled"] || rec["deadline"]
      if value.nil? || value == :off
        rec.delete("recur")
      else
        return patch_invalid("invalid recurrence cookie") unless value.is_a?(String) && Recur.cookie?(value)
        rec["recur"] = value
      end
      patch_ok(rec)
    end

    def patch_tag_slice(records, ri, value, slice)
      return patch_invalid("#{slice} must be a list of tags") unless value.is_a?(Array) && value.all? { |tag| tag.is_a?(String) }
      proposed = value.map { |tag| utf8(tag) }
      valid = if slice == :contexts
                proposed.all? { |tag| tag.start_with?("@") && tag.length > 1 }
              else
                proposed.none? { |tag| tag.start_with?("@") || tag == DEFER_TAG || tag.empty? }
              end
      return patch_invalid("invalid #{slice} tag") unless valid
      return patch_invalid("duplicate #{slice} tag") unless proposed.uniq == proposed

      rec = records[ri]
      owns = if slice == :contexts
               ->(tag) { tag.start_with?("@") }
             else
               ->(tag) { !tag.start_with?("@") && tag != DEFER_TAG }
             end
      existing = semantic_tags(rec)
      return patch_ok(rec) if existing.select(&owns) == proposed
      rec["tags"] = merge_owned_slice(existing, proposed, &owns)
      replace_optional(rec, "tags", rec["tags"])
      patch_ok(rec)
    end

    def merge_owned_slice(existing, proposed)
      merged = []
      owned_count = existing.count { |tag| yield(tag) }
      owned_index = 0
      existing.each do |tag|
        if yield(tag)
          merged << proposed[owned_index] if owned_index < proposed.length
          owned_index += 1
          merged.concat(proposed[owned_index..]) if owned_index == owned_count && owned_index < proposed.length
        else
          merged << tag
        end
      end
      merged.concat(proposed) if owned_count.zero?
      merged
    end

    def safe_patch_error(error)
      error.message.to_s.encode(Encoding::UTF_8, invalid: :replace,
                                undef: :replace, replace: "�")
    rescue EncodingError
      "invalid patch data"
    end

    def patch_body(records, ri, value)
      return patch_invalid("body must be text") unless value.is_a?(String)
      replace_optional(records[ri], "body", utf8(value))
      patch_ok(records[ri])
    end

    def replace_optional(rec, key, value)
      value.nil? || (value.respond_to?(:empty?) && value.empty?) ? rec.delete(key) : rec[key] = value
    end

    def patch_location(records, ri, parent_id, force: false)
      return patch_invalid("location must be a parent id") unless parent_id.is_a?(String)
      rec = records[ri]
      if !force && rec["parent"] == parent_id
        return patch_ok(rec, summary: { from: rec["parent"], to: parent_id, moved_ids: [] })
      end

      pi = records.index { |record| record["id"] == parent_id }
      return patch_invalid("location parent does not exist") unless pi
      return patch_invalid("location parent must be a section or task") unless %w[section task].include?(records[pi]["type"])

      rj = subtree_end(records, ri)
      return { status: :cycle, summary: { from: rec["parent"], to: parent_id } } if pi >= ri && pi < rj

      by_id = records.to_h { |record| [record["id"], record] }
      if records[pi]["type"] == "task" &&
         task_depth(by_id, records[pi]) + subtree_height(records, ri) > @max_depth
        return { status: :too_deep, summary: { from: rec["parent"], to: parent_id } }
      end

      from = rec["parent"]
      subtree = records[ri...rj].map(&:dup)
      moved_ids = subtree.filter_map { |record| record["id"] }
      rest = records[0...ri] + records[rj..]
      new_pi = rest.index { |record| record["id"] == parent_id }
      subtree[0]["parent"] = parent_id
      insert_at = subtree_end(rest, new_pi)
      rest[insert_at, 0] = subtree
      records.replace(rest)
      patch_ok(subtree[0], touched_ids: moved_ids,
               summary: { from: from, to: parent_id, moved_ids: moved_ids })
    end

    def patch_state(records, ri, value)
      return patch_invalid("invalid task state") unless Check::STATES.include?(value)
      rec = records[ri]
      from = rec["state"]
      if value == "DONE" && Recur.cookie?(rec["recur"])
        result = advance_recurrence_records(records, ri)
        return result unless result[:status] == :ok
        result[:summary] = { from: from, to: from, recurrence_advanced: true, cascaded_ids: [] }
        return result
      end

      rec["state"] = value
      touched_ids = [rec["id"]]
      cascaded_ids = []
      if DONE_STATES.include?(value) && !DONE_STATES.include?(from)
        rec["tags"] = semantic_tags(rec) - [DEFER_TAG]
        replace_optional(rec, "tags", rec["tags"])
        rec["closed"] ||= Date.today.iso8601
        if value == "DONE"
          cascaded_ids = close_open_descendants(records, ri, return_ids: true)
          touched_ids.concat(cascaded_ids)
        end
      elsif DONE_STATES.include?(from) && !DONE_STATES.include?(value)
        rec.delete("closed")
      end
      patch_ok(rec, touched_ids: touched_ids,
               summary: { from: from, to: value, recurrence_advanced: false,
                          cascaded_ids: cascaded_ids })
    end

    def advance_recurrence_records(records, ri)
      rec = records[ri]
      cookie = rec["recur"]
      return patch_invalid("invalid recurrence cookie") unless Recur.cookie?(cookie)
      field = rec["deadline"] ? "deadline" : ("scheduled" if rec["scheduled"])
      base = field && to_date(rec[field])
      return patch_invalid("recurrence requires a valid date") unless base
      rec[field] = Recur.next_date(cookie, from: base).iso8601
      rec["tags"] = semantic_tags(rec) - [DEFER_TAG]
      replace_optional(rec, "tags", rec["tags"])
      rec["body"] = append_body(rec["body"], "- Did [#{Date.today}].")
      patch_ok(rec)
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

    # Task-depth of `rec`: the number of TASK records on its parent chain,
    # counting itself. A task filed directly under a section is depth 1;
    # sections don't count. `by_id` maps every record id to its record (built
    # once per mutation) so the walk is O(chain length). Drives the nesting cap.
    def task_depth(by_id, rec)
      depth = 0
      cur = rec
      while cur
        depth += 1 if cur["type"] == "task"
        pid = cur["parent"]
        cur = pid && by_id[pid]
      end
      depth
    end

    # Height of the subtree rooted at records[ri]: over the span
    # records[ri...subtree_end), max(task_depth) − task_depth(root) + 1. The span
    # is contiguous and holds only the root's task descendants, so measuring
    # task-depth within the span (root = 1) yields the height directly — the
    # ancestor prefix above the root cancels out of the difference.
    def subtree_height(records, ri)
      rj = subtree_end(records, ri)
      span = records[ri...rj]
      by_id = span.to_h { |r| [r["id"], r] }
      span.map { |r| task_depth(by_id, r) }.max
    end

    # Close every OPEN task inside the subtree rooted at records[ri], excluding
    # the root itself — the cascade behind completing a parent: finishing a
    # project finishes its open work. Each open descendant (state in
    # OPEN_STATES) goes DONE with today's `closed`, drops the DEFER_TAG, and has
    # its `recur` cookie retired outright — a cascaded recurring descendant is
    # NOT advanced (no date roll, no body log): completing the parent completes
    # it. DONE/CANCELLED descendants are left untouched (their prior `closed`
    # stands). Returns the touched records' `line` values, in file order.
    def close_open_descendants(records, ri, return_ids: false)
      rj = subtree_end(records, ri)
      today = Date.today.iso8601
      records[(ri + 1)...rj].each_with_object([]) do |rec, touched|
        next unless rec["type"] == "task" && OPEN_STATES.include?(rec["state"])
        rec["state"] = "DONE"
        rec["closed"] = today
        rec["tags"] = (rec["tags"] || []) - [DEFER_TAG]
        rec.delete("recur")
        touched << (return_ids ? rec["id"] : rec["line"])
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
      if @lock_depth.to_i.positive?
        @lock_depth += 1
        begin
          return yield
        ensure
          @lock_depth -= 1
        end
      end

      File.open(lock_path, File::RDWR | File::CREAT, 0o644) do |f|
        f.flock(File::LOCK_EX)
        @lock_depth = 1
        begin
          yield
        ensure
          @lock_depth = 0
        end
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
      current = snapshot
      paths = restore_archive_first?(current, snap) ? %i[archive org] : %i[org archive]
      paths.each do |kind|
        next if current[kind] == snap[kind]
        restore_file(kind == :org ? @org : @archive, snap[kind])
      end
      reload!
    end

    def restore_file(path, content)
      if content.nil?
        File.delete(path) if File.exist?(path)
      else
        Atomic.write(path, content)
      end
    end

    # Undo/redo can replay an archive sweep, so restore has the same ordering
    # obligation as the forward operation. Install the destination copy before
    # removing the source copy: archive first for live -> archive (redo), live
    # first for archive -> live (undo). Other history entries retain live-first.
    def restore_archive_first?(current, target)
      current_live = snapshot_ids(current[:org])
      target_live = snapshot_ids(target[:org])
      target_archive = snapshot_ids(target[:archive])
      ((current_live - target_live) & target_archive).any?
    end

    def snapshot_ids(content)
      return Set.new unless content
      Format.parse(content).records.filter_map { |record| record["id"] }.to_set
    end

    # Apply an undo (delta -1) or redo (delta +1) planned by the journal, under
    # the lock so the plan and its commit can't race another writer.
    def history_step(delta)
      with_lock do
        step = @journal.plan(delta)
        return [:empty] unless step
        return [:conflict, step[:label]] unless snapshot == step[:expect]
        before = snapshot
        commit_started = false
        begin
          restore(step[:target])
          # A journaled snapshot could pre-date a repair: restoring it would write
          # a state that fails today's invariants. Gate the restored live file the
          # same way with_history gates a forward mutation. A nil target org is the
          # empty first-run state — no file to validate — so skip the gate there.
          if step[:target][:org] && !Check.check(@org).ok?
            rollback_history_files(before)
            return [:conflict, step[:label]]
          end
          commit_started = true
          step[:commit].call
          [:ok, step[:label]]
        rescue SystemCallError, IOError
          cursor_restored = !commit_started || rollback_history_cursor(step)
          rollback_history_files(before) if cursor_restored
          [:conflict, step[:label]]
        rescue Exception # fatal exceptions propagate after best-effort rollback
          cursor_restored = !commit_started || rollback_history_cursor(step)
          rollback_history_files(before) if cursor_restored
          raise
        end
      end
    end

    # Cursor commit is last, so a failed file restore never needs this path.
    # Ordinary rollback trouble is contained; fatal exceptions still propagate.
    def rollback_history_cursor(step)
      2.times do
        begin
          step[:rollback].call
          return true
        rescue SystemCallError, IOError
          # retry once below
        end
      end
      false
    end

    # Atomic replacement means a failed attempt leaves either the complete old
    # or complete new file. Retry once for transient rollback failures; exact
    # snapshot comparison (including nil absence) avoids rewriting paths that
    # never changed and keeps persistent failures loss-safe rather than torn.
    def rollback_history_files(before)
      2.times do
        begin
          restore(before)
          return true if snapshot == before
        rescue SystemCallError, IOError
          # retry once below
        end
      end
      false
    end

    # Record history only when the mutation actually wrote (truthy, nonzero) AND
    # changed the file — an idempotent no-op (e.g. adding a tag already present)
    # succeeds but must not burn an undo slot with a label that reverts nothing.
    # The whole read-modify-write runs under the lock so a concurrent writer
    # can't slip between the steps.
    def with_history(label, coalesce_key: nil)
      with_lock do
        @last_rollback = nil
        before = snapshot
        result = yield
        if result && result != 0
          after = snapshot
          # A typed refusal/no-op may be inspecting a preexisting invalid file
          # specifically to report an actionable conflict. It wrote nothing,
          # so preserve that result; post-write validation applies only when the
          # mutation actually changed a snapshot.
          return result if after == before
          # post-write invariant: a mutation must never mangle either file (the
          # sweep writes the archive too). If it would, record why, roll back —
          # both files are snapshotted — and report failure instead.
          if (reason = post_write_failure)
            @last_rollback = reason
            restore(before)
            return result.is_a?(Integer) ? 0 : false
          end
          @journal.record(label: label, before: before, after: after,
                          coalesce_key: coalesce_key)
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
    # Simple single-record impls use update_record: read fresh records under the
    # lock, locate by id (fallback: line + title guard), mutate, write through
    # Format, and reload. Multi-record, tree, archive, and recurrence-advancement
    # paths stay explicit because their return and atomicity contracts differ.

    def update_record(item)
      records = fresh_records(@org)
      rec = locate(records, item) or return false
      result = yield rec
      return result unless result
      write_records(@org, records)
      reload!
      result
    end

    def complete_impl(item)
      records = fresh_records(@org)
      ri = locate_index(records, item) or return false
      result = patch_state(records, ri, "DONE")
      return false unless result[:status] == :ok
      lines = result[:touched_ids].filter_map do |id|
        records.find { |record| record["id"] == id }&.fetch("line", nil)
      end
      write_records(@org, records)
      reload!
      lines
    end

    def set_priority_impl(item, pri)
      records = fresh_records(@org)
      ri = locate_index(records, item) or return false
      result = patch_priority(records, ri, pri)
      return false unless result[:status] == :ok
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
      ri = locate_index(records, item) or return false
      result = patch_date(records, ri, date, kind)
      return false unless result[:status] == :ok
      write_records(@org, records)
      reload!
      true
    end

    # Remove the scheduled and/or deadline field(s). Returns false if nothing
    # matched. Clears a now-meaningless recur if no date remains.
    def undate_impl(item, kind)
      records = fresh_records(@org)
      ri = locate_index(records, item) or return false
      fields = case kind
               when :scheduled then [:scheduled]
               when :deadline  then [:deadline]
               else                 %i[scheduled deadline]
               end
      return false unless fields.any? { |field| records[ri][field.to_s] }
      fields.each { |field| patch_date(records, ri, nil, field) }
      write_records(@org, records)
      reload!
      true
    end

    def retitle_impl(item, new_title)
      records = fresh_records(@org)
      ri = locate_index(records, item) or return false
      result = patch_title(records, ri, new_title)
      return false unless result[:status] == :ok
      write_records(@org, records)
      reload!
      true
    end

    def set_tags_impl(item, add, remove)
      update_record(item) do |rec|
        tags = (rec["tags"] || []).reject { |t| remove.include?(t) }
        add.each { |t| tags << t unless tags.include?(t) }
        rec["tags"] = tags
        true
      end
    end

    def add_note_impl(item, text)
      update_record(item) do |rec|
        rec["body"] = append_body(rec["body"], text.strip)
        true
      end
    end

    def set_state_impl(item, new_state)
      records = fresh_records(@org)
      ri = locate_index(records, item) or return false
      result = patch_state(records, ri, new_state)
      return false unless result[:status] == :ok
      lines = result[:touched_ids].filter_map do |id|
        records.find { |record| record["id"] == id }&.fetch("line", nil)
      end
      write_records(@org, records)
      reload!
      lines
    end

    # Set/replace/remove the recurrence cookie. Requires a date to repeat from.
    def set_recur_impl(item, cookie)
      records = fresh_records(@org)
      ri = locate_index(records, item) or return false
      result = patch_recurrence(records, ri, cookie)
      return false unless result[:status] == :ok
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
      ri = locate_index(records, item) or return false
      result = advance_recurrence_records(records, ri)
      return false unless result[:status] == :ok
      write_records(@org, records)
      reload!
      [records[ri]["line"]]
    end

    def move_impl(item, section)
      records = fresh_records(@org)
      ri = locate_index(records, item) or return false
      moved_id = records[ri]["id"]
      ti = find_section(records, section) or return false
      target_id = records[ti]["id"]
      result = patch_location(records, ri, target_id, force: true)
      return false unless result[:status] == :ok
      write_records(@org, records)
      reload!
      @records.index { |record| record["id"] == moved_id } + 1
    end

    # Splice the item's subtree in as the last child of parent_item. Guards the
    # cycle (parent inside the moved span — self-nesting included) and the depth
    # cap before touching the file, so a refused move burns no undo slot.
    def move_under_impl(item, parent_item)
      records = fresh_records(@org)
      ri = locate_index(records, item) or return false
      moved_id = records[ri]["id"]
      pi = locate_index(records, parent_item) or return false
      parent_id = records[pi]["id"]
      result = patch_location(records, ri, parent_id, force: true)
      return result[:status] if %i[cycle too_deep].include?(result[:status])
      return false unless result[:status] == :ok
      write_records(@org, records)
      reload!
      @records.index { |record| record["id"] == moved_id } + 1
    end

    # Move the item's subtree to the end of its nearest ancestor section. No
    # depth check (unnesting can only shrink depth). Returns 0 when the item is
    # already parented directly to that section — a no-op with_history skips.
    def move_top_impl(item)
      records = fresh_records(@org)
      ri = locate_index(records, item) or return false
      moved_id = records[ri]["id"]
      by_id = records.to_h { |r| [r["id"], r] }

      # Walk the ancestor chain by explicit parent id — a nil parent must stop
      # the walk (a parentless task has no section to unnest to), not hit the
      # id-less meta record via by_id[nil] and loop forever.
      pid = records[ri]["parent"]
      section = pid && by_id[pid]
      while section && section["type"] != "section"
        pid = section["parent"]
        section = pid && by_id[pid]
      end
      return false unless section
      return 0 if records[ri]["parent"] == section["id"]

      section_id = section["id"]
      result = patch_location(records, ri, section_id, force: true)
      return false unless result[:status] == :ok
      write_records(@org, records)
      reload!
      @records.index { |record| record["id"] == moved_id } + 1
    end

    def capture_impl(text, due, scheduled, priority, tags, state, project, under = nil)
      records = fresh_records(@org)
      if under
        # Nest under an existing task: locate it (stale → false), enforce the
        # depth cap BEFORE any write, then file the new task as its last child.
        pi = locate_index(records, under) or return false
        by_id = records.to_h { |r| [r["id"], r] }
        return :too_deep if task_depth(by_id, records[pi]) + 1 > @max_depth
        parent_id = records[pi]["id"]
        insert_at = subtree_end(records, pi)
      elsif records.empty?
        # First run: a missing or empty file. Bootstrap a brand-new store — a
        # meta line plus the target section (default "Inbox") — then insert into
        # it. (A NAMED --project missing from a NON-empty file still fails below.)
        records = [meta_record,
                   { "type" => "section", "id" => gen_id(archived_ids),
                     "title" => (project || "Inbox").strip }]
        si = records.length - 1
        parent_id = records[si]["id"]
        insert_at = subtree_end(records, si)
      else
        si = find_section(records, project || "Inbox") or return false
        parent_id = records[si]["id"]
        insert_at = subtree_end(records, si)
      end

      rec = { "type" => "task", "id" => gen_id(ids_of(records) + archived_ids),
              "parent" => parent_id, "state" => state }
      rec["priority"] = priority if priority
      rec["title"] = text.strip
      rec["tags"] = tags unless tags.empty?
      rec["scheduled"] = scheduled.iso8601 if scheduled
      rec["deadline"] = due.iso8601 if due
      rec["body"] = "Captured [#{Date.today}]."

      records[insert_at, 0] = [rec]
      write_records(@org, records)
      reload!
      insert_at + 1
    end

    # Move every fully closed DONE/CANCELLED task subtree to the archive file.
    # The archive is written first, then the live file: interruption can leave
    # retry-safe duplicates across the two files, but can never silently lose a
    # task. A retry converges only when every stable ID has one canonically
    # equal archived copy; partial or mismatched overlap refuses safely. Returns
    # the count of roots swept, or ArchiveRefusal when a safety gate blocks it.
    def archive_swept_impl(expected_preview)
      plan = archive_plan(fresh_records(@org))
      if expected_preview && (expected_preview.candidate_ids != plan.preview.candidate_ids ||
                              expected_preview.fingerprint != plan.preview.fingerprint)
        return ArchiveRefusal.new(reason: :preview_changed, preview: plan.preview)
      end
      return ArchiveRefusal.new(reason: :open_descendants, preview: plan.preview) if plan.preview.blocked?
      return 0 if plan.moved.empty?

      arch = File.exist?(@archive) ? fresh_records(@archive) : []
      arch = [meta_record] if arch.empty?
      retry_state, conflicts = archive_retry_state(arch, plan.moved)
      if retry_state == :conflict
        return ArchiveRefusal.new(reason: :archive_conflict, preview: plan.preview, details: conflicts)
      end
      if retry_state == :new
        arch.concat(plan.moved)
        write_records(@archive, arch)
      end

      # A successful atomic archive write is the commit point. Re-read it before
      # deleting live records so even an injected/custom writer cannot make the
      # destructive half proceed without durable copies of every moved id.
      persisted_ids = ids_of(fresh_records(@archive)).to_set
      missing_ids = ids_of(plan.moved).reject { |id| persisted_ids.include?(id) }
      raise "archive write omitted moved ids: #{missing_ids.join(", ")}" unless missing_ids.empty?

      write_records(@org, plan.kept)
      reload!
      plan.preview.roots
    end

    # A retry is safe only when the archive contains either none of the moved
    # IDs (a new sweep), or exactly one canonical copy of every moved record (an
    # interrupted archive-first sweep). Partial overlap, duplicate IDs, or
    # differing content is a conflict: retain live data and require resolution.
    def archive_retry_state(arch, moved)
      by_id = arch.group_by { |record| record["id"] }
      moved_ids = ids_of(moved)
      overlap = moved_ids.select { |id| by_id.key?(id) }
      return [:new, []] if overlap.empty?

      conflicts = moved_ids.select do |id|
        copies = by_id[id] || []
        expected = moved.find { |record| record["id"] == id }
        copies.length != 1 || !archive_retry_record?(expected, copies.first)
      end
      conflicts |= moved_ids - overlap if overlap.length != moved_ids.length
      conflicts.empty? ? [:complete, []] : [:conflict, conflicts]
    end

    def archive_retry_record?(expected, actual)
      expected = expected.reject { |key, _| key == "line" }
      actual = actual.reject { |key, _| key == "line" }
      # The first archive write owns the timestamp. A retry after midnight must
      # not conflict solely because today's proposed stamp has advanced.
      expected["archived"] = actual["archived"] if expected["archived"] && actual["archived"]
      expected == actual
    end

    def archive_plan(records)
      kept = []
      moved = []
      roots = 0
      descendants = 0
      blocks = []
      i = 0
      while i < records.length
        r = records[i]
        if r["type"] == "task" && DONE_STATES.include?(r["state"])
          j = subtree_end(records, i)
          group = records[i...j].map(&:dup)
          open = group.drop(1).select do |record|
            record["type"] == "task" && OPEN_STATES.include?(record["state"])
          end
          unless open.empty?
            blocks << ArchiveBlock.new(
              root_id: r["id"], root_title: r["title"],
              open_ids: open.map { |record| record["id"] },
              open_titles: open.map { |record| record["title"] }
            )
          end
          group[0].delete("parent")
          group[0]["archived"] = Date.today.iso8601
          moved.concat(group)
          roots += 1
          descendants += group.count { |record| record["type"] == "task" } - 1
          i = j
        else
          kept << r
          i += 1
        end
      end
      candidate_ids = ids_of(moved).freeze
      preview = ArchivePreview.new(
        roots: roots, descendants: descendants, blocks: blocks.freeze,
        candidate_ids: candidate_ids,
        fingerprint: Digest::SHA256.hexdigest(Format.dump(moved))
      ).freeze
      ArchivePlan.new(
        kept: kept, moved: moved,
        preview: preview
      )
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
