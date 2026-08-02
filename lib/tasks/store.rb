# frozen_string_literal: true

require "date"
require "digest"
require "securerandom"
require "set"
require_relative "atomic"
require_relative "check"
require_relative "create_task"
require_relative "delegation"
require_relative "delete_task"
require_relative "edit_snapshot"
require_relative "format"
require_relative "journal"
require_relative "lead"
require_relative "links"
require_relative "patch_result"
require_relative "proposal_decision"
require_relative "quadrants"
require_relative "recur"
require_relative "task_patch"
require_relative "temporal_value"
require_relative "temporal_context"
require_relative "tree"
require_relative "update_stamp"

module Tasks
  Item = Struct.new(
    :state, :priority, :title, :tags, :scheduled, :deadline,
    :scheduled_value, :deadline_value, :line, :source,
    :recur, :lead, :lead_skip, :id, :closed, keyword_init: true
  ) do
    def open?    = Store::OPEN_STATES.include?(state)
    def proposed? = Store::PROPOSED_STATES.include?(state)
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

    # A lead-time task carries a VALID span in its own `lead` field; its own
    # timed gate is then `anchor - lead` rather than its available-from date.
    # A malformed span (a hand-edited "0w") reads as no lead at all, exactly as
    # a malformed cookie reads as non-recurring — Check still reports it.
    def lead_time? = Lead.span?(lead)

    # The occurrence date a lead is measured back from: deadline first,
    # available-from second (see Lead.anchor_date).
    def lead_anchor = Lead.anchor_date(deadline, scheduled)

    # The date this task's lead window opens, or nil when it has no lead, no
    # anchor, or an `activate` already released this occurrence. The canonical
    # derivation for availability lives in TaskQueries#effective_gate; this is
    # the same answer for callers holding only an Item.
    def lead_gate_date
      return nil unless lead_time?

      anchor = lead_anchor
      return nil if anchor.nil? || lead_skip == anchor.iso8601

      Lead.gate_date(anchor, lead)
    end

    # The item's headline rendered from its own fields, star-less: state,
    # optional priority cookie, title, trailing tag cluster (stored order).
    # The single source of the summary the CLI and TUI show; Store#headline and
    # TaskQueries#headline_for both delegate here so the string can never fork
    # between read commands and mutation reporting. Derives purely from item
    # fields, so it belongs on the item itself. `title.to_s` keeps a malformed
    # (nil-title) record from crashing a reader before Check reports it.
    def headline
      s = +"#{state} "
      s << "[##{priority}] " if priority
      s << title.to_s
      s << " :#{tags.join(":")}:" unless tags.empty?
      s
    end
  end

  # Owns tasks.jsonl: parsing records into Items, change detection, and the
  # mutations the CLI and TUI perform. Every record is an explicit JSON object
  # (see Tasks::Format); the tree lives in the `parent` pointers, so the store
  # never infers block boundaries by scanning — a whole class of bugs the old
  # org line-walker was prone to is structurally gone. Claude edits the file
  # out-of-band via the CLI; `changed?` picks up any write by mtime.
  class Store
    PROPOSED_STATES = Check::PROPOSED_STATES
    OPEN_STATES = Check::OPEN_STATES
    CLOSED_STATES = Check::CLOSED_STATES
    STATES = Check::STATES
    DONE_STATES = CLOSED_STATES
    REVISION_OWN_FIELDS = (EditSnapshot::FIELDS - [:location]).freeze
    # The fields whose write can leave a task with no date at all, and so are
    # the only ones that retire a recurrence or a lead time.
    DATE_OWNING_FIELDS = %i[scheduled deadline date_clear].freeze
    # A different Fiber on the same thread cannot wait on the sidecar flock:
    # doing so would block the thread's scheduler before the owning Fiber can
    # resume and release it. Callers must resume the owner first.
    CrossFiberLockError = Class.new(StandardError)

    # A coherent, immutable view of the task files. A caller can hold one of
    # these while rendering a task and safely ask for its body, links, or tree
    # node without mixing fields from a later file reload. Store builds it
    # while holding the same sidecar lock as mutations.
    class ReadSnapshot
      attr_reader :items, :archive_items, :tree, :nodes_by_line, :live_records,
                  :archive_records, :live_stat, :archive_stat

      def initialize(live_records:, live_stat:, archive_records:, archive_stat:,
                     archive_loaded:, item_builder:, task_revisions:, link_shorthands:, link_systems:)
        @live_records = immutable_copy(live_records)
        @archive_records = immutable_copy(archive_records)
        @live_stat = live_stat&.freeze
        @archive_stat = archive_stat&.freeze
        @archive_loaded = archive_loaded
        @task_revisions = immutable_copy(task_revisions)
        @link_shorthands = immutable_copy(link_shorthands)
        @link_systems = immutable_copy(link_systems)

        @items = immutable_items(@live_records, :live, item_builder)
        @archive_items = immutable_items(@archive_records, :archive, item_builder)
        @records_by_id = {
          live: index_records_by_id(@live_records),
          archive: index_records_by_id(@archive_records),
        }.freeze
        by_line = @items.to_h { |item| [item.line, item] }
        @tree = Tree.build(@live_records, by_line)
        @nodes_by_line = {}.tap do |map|
          @tree.each { |root| root.each { |node| map[node.line] = node } }
        end
        @nodes_by_id = @nodes_by_line.each_value.to_h do |node|
          [node.item&.id, node]
        end.tap { |map| map.delete(nil) }.freeze
        freeze_tree(@tree)
        @nodes_by_line.freeze
        freeze
      end

      def archive_loaded? = @archive_loaded

      # The canonical representation and edit snapshot share this exact
      # Store-produced token. There is deliberately no line-number fallback:
      # an id-less legacy item has no API-safe revision.
      def revision_for(item)
        return nil unless item&.id

        @task_revisions.fetch(item.source, {}).fetch(item.id, nil)
      end

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
        item.id ? @nodes_by_id[item.id] : nil
      end

      private

      def records_for(item)
        item.source == :archive ? @archive_records : @live_records
      end

      # O(1) per item: every read surface (body, links, TaskView building) runs
      # this per task, so a linear scan here made whole-list reads quadratic.
      def locate(records, item)
        if item.id
          source = records.equal?(@archive_records) ? :archive : :live
          return @records_by_id.fetch(source)[item.id]
        end
        record = records.find { |candidate| candidate["line"] == item.line }
        record if record && record["type"] == "task" && record["title"] == item.title
      end

      def index_records_by_id(records)
        records.each_with_object({}) do |record, map|
          id = record["id"]
          map[id] = record if id
        end.freeze
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

    # Typed result of the checked, API-grade read path. Unlike ReadSnapshot,
    # this can represent an invalid or unavailable store without attempting to
    # build a tree from untrusted records. Errors carry only source, line, and a
    # safe validation message — never configured filesystem paths.
    class CheckedRead
      STATUSES = %i[ok unsupported_schema store_invalid unavailable].freeze

      attr_reader :status, :snapshot, :store_revision, :errors, :warnings

      def initialize(status:, snapshot: nil, store_revision: nil, errors: [], warnings: [])
        @status = status.to_sym
        raise ArgumentError, "unknown checked-read status #{@status.inspect}" unless STATUSES.include?(@status)

        @snapshot = snapshot
        @store_revision = store_revision&.dup&.freeze
        @errors = immutable(errors)
        @warnings = immutable(warnings)
        freeze
      end

      def ok? = status == :ok
      def unsupported_schema? = status == :unsupported_schema
      def store_invalid? = status == :store_invalid
      def unavailable? = status == :unavailable

      private

      def immutable(value)
        case value
        when Hash
          value.each_with_object({}) { |(key, child), copy| copy[key] = immutable(child) }.freeze
        when Array
          value.map { |child| immutable(child) }.freeze
        when String
          value.dup.freeze
        else
          value.freeze
        end
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

    # One defect #repair! knows how to converge, named by the file and physical
    # line it sits on. `kind` is the machine-readable discriminator (:minted_id,
    # :dropped_temporal_keys).
    #
    # `message` deliberately restates the DEFECT in Check's own wording, not the
    # action taken: the report then reads as a line-for-line answer to what
    # `tasks check` just printed, and it means the same thing whether the pass
    # wrote or refused. `id` carries the minted id, and only a written pass
    # should show it — a plan that is never written mints an id nothing will
    # ever hold.
    RepairFix = Struct.new(:file, :line, :kind, :message, :id, keyword_init: true) do
      def to_h
        base = { file: file, line: line, kind: kind.to_s, message: message }
        id ? base.merge(id: id) : base
      end
    end

    # A defect #repair! does NOT know how to converge. Reported so the caller
    # learns what still blocks the store rather than only that repair refused.
    RepairBlocker = Struct.new(:file, :line, :message, keyword_init: true) do
      def to_h = { file: file, line: line, message: message }
    end

    # The outcome of one #repair! pass. `:ok` means the store validates now (or
    # would, under --dry-run); `:unrepairable` means at least one blocker was
    # left and NOTHING was written; `:unsupported_schema` is the version gate.
    # `written` distinguishes a pass that changed the files from a clean store
    # and from a dry run, so a caller never has to infer it from `fixes`.
    RepairResult = Struct.new(:status, :fixes, :blockers, :written, :dry_run, keyword_init: true) do
      def ok? = status == :ok
      def written? = written == true
      def dry_run? = dry_run == true

      def to_h
        {
          ok: ok?, status: status.to_s, dry_run: dry_run?, written: written?,
          # A minted id is reported only when it was actually written. A dry run
          # and a refused pass both mint one to prove the file would validate,
          # and that id is discarded — publishing it would invite a caller to
          # record an id no record will ever carry.
          fixes: fixes.map { |fix| written? ? fix.to_h : fix.to_h.except(:id) },
          blockers: blockers.map(&:to_h),
        }
      end
    end

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

    # WHICH STAGE produced #last_rollback: `:write` when the atomic replace
    # itself raised (validation never ran), `:validation` when the bytes landed
    # and the post-write Check refused them. nil when the last mutation was
    # clean. `last_rollback` alone cannot answer this — both stages set it — and
    # a caller that guesses blames validation for a failure it never saw
    # (td-fea097). Set and cleared only through #record_rollback/#clear_rollback
    # so the pair can never drift apart.
    attr_reader :last_rollback_stage

    UNDO_LIMIT = 50 # deepest undo history the journal retains

    # `journal_dir` defaults to an XDG_STATE_HOME location derived from the live
    # path, so the CLI and TUI editing the same file share one undo history;
    # tests pass an explicit dir to stay hermetic. `links`/`link_systems` are
    # the user's configured shorthand templates and custom host rows
    # (Config#links / #link_systems), consulted by #links. The keyword stays
    # `org:` for constructor compatibility though it now names the jsonl file.
    # `id_source` is the seam the conformance harness pins so a mutation mints a
    # reproducible id (see Tasks::Determinism). nil keeps the production mint —
    # SecureRandom — unchanged; a pinned source is still filtered through
    # #gen_id's collision loop, so it can never hand out an id already in use.
    def initialize(org:, archive:, journal_dir: nil, undo_limit: UNDO_LIMIT, coalesce_scope: nil,
                   links: {}, link_systems: {}, max_depth: Tree::DEFAULT_MAX_DEPTH,
                   now: -> { Time.now.utc }, device: nil, id_source: nil)
      @id_source = id_source
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
      @last_rollback = nil
      @last_rollback_stage = nil
      @now = now
      @device = UpdateStamp.slug(device || UpdateStamp.device)
      @journal = Journal.new(dir: journal_dir || Journal.dir_for(org), org: org, limit: undo_limit,
                             coalesce_scope: coalesce_scope)
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
        live = capture_read_source(@org)
        archive = include_archive ? capture_read_source(@archive, optional: true) : empty_read_source
        build_read_snapshot(live, archive, include_archive: include_archive)
      end
    end

    # Capture and validate both files under one Store lock, returning canonical
    # resources and a content-derived global revision from those exact bytes.
    # The archive is optional, matching the existing first-run behavior; the
    # live file is required. Invalid records never reach Tree/TaskQueries.
    def checked_read_snapshot
      with_lock do
        live = capture_read_source(@org, validate: true)
        archive = capture_read_source(@archive, optional: true, validate: true)
        store_revision = store_revision_for_contents(live[:raw], archive[:raw])
        errors = annotated_check_entries(live[:check].errors, :live) +
                 annotated_check_entries(archive[:check].errors, :archive)
        warnings = annotated_check_entries(live[:check].warnings, :live) +
                   annotated_check_entries(archive[:check].warnings, :archive)

        # A store written under a different schema version is refused before its
        # records are interpreted: reading v1 (or a future v3) bytes as if they
        # were v2 is how a store gets silently corrupted. There is no migration
        # path — this binary reads exactly Format::VERSION.
        skew = [[:live, live], [:archive, archive]].find { |_source, capture| unsupported_meta?(capture[:records]) }
        if skew
          source, capture = skew
          return CheckedRead.new(
            status: :unsupported_schema, store_revision: store_revision,
            errors: [{ source: source, line: 1,
                       message: unsupported_meta_message(capture[:records].first["version"]) }],
            warnings: warnings
          )
        end

        unless errors.empty?
          return CheckedRead.new(
            status: :store_invalid, store_revision: store_revision,
            errors: errors, warnings: warnings
          )
        end

        CheckedRead.new(
          status: :ok,
          snapshot: build_read_snapshot(live, archive, include_archive: true),
          store_revision: store_revision,
          warnings: warnings
        )
      end
    rescue SystemCallError
      CheckedRead.new(
        status: :unavailable,
        errors: [{ source: nil, line: 0, message: "task store unavailable" }]
      )
    end

    # Coherent sync-safety validation over live and archive. Unlike the API's
    # checked read, this deliberately rejects even retry-safe transient copies
    # shared across both files: a git push must wait until an archive operation
    # converges to one durable location.
    def check_files
      with_lock do
        live = capture_read_source(@org, validate: true)
        archive = capture_read_source(@archive, optional: true, validate: true)
        errors = live[:check].errors.map { |line, message| [line, "tasks.jsonl: #{message}"] } +
                 archive[:check].errors.map { |line, message| [line, "archive.jsonl: #{message}"] } +
                 Check.cross_file_duplicate_errors(live[:records], archive[:records])
        warnings = live[:check].warnings.map { |line, message| [line, "tasks.jsonl: #{message}"] } +
                   archive[:check].warnings.map { |line, message| [line, "archive.jsonl: #{message}"] }
        Check::Result.new(errors.sort_by(&:first), warnings.sort_by(&:first))
      end
    rescue SystemCallError
      Check::Result.new([[0, "task store unavailable"]], [])
    end

    # Default `tasks check`: the live file's structural lint, plus the archive's
    # schema-version header.
    #
    # Structural errors stay file-scoped — the archive's own records are what
    # `--all-files` is for, and folding them in would make the everyday check
    # noisy about a file the everyday commands do not read. The version gate is
    # the deliberate exception, because it is the one condition that is
    # store-wide rather than file-scoped: `unsupported_schema_source` consults
    # BOTH files, so a v1 archive under a v2 live file makes every read and
    # every mutation refuse the whole store. A `check` that could not see it
    # answered "ok — no structural errors" to a user who had just been told, by
    # the refusal, to run `tasks check` — a closed diagnostic loop with the
    # answer sitting in a file this command declined to open.
    def check_live
      result = Check.check(@org)
      source, version = unsupported_schema_source
      return result unless source == :archive

      Check::Result.new(
        [[1, "archive.jsonl: #{Check.unsupported_version_message(version)}"]] + result.errors,
        result.warnings
      )
    end

    def reload!(include_archive: false)
      # Build AND publish under one lock acquisition. Publishing after the lock
      # released let a descheduled reader clobber @records/@read_snapshot with
      # pre-mutation state while a mutation on another thread was still reading
      # them inside its own locked section.
      with_lock do
        publish_read_snapshot(read_snapshot(include_archive: include_archive))
      end
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

    # The item's headline rendered from its fields — see Item#headline, the
    # single definition this delegates to. Kept as a Store method because the
    # TUI and tests call store.headline(item); works for live and archive items.
    def headline(item) = item.headline

    # -- undo/redo -------------------------------------------------------------
    #
    # History lives in an on-disk Journal (see journal.rb) shared by the CLI and
    # the TUI, so it survives a restart and one tool can undo the other's edit.
    # A step is applied only when the live files still match what that mutation
    # left behind — an out-of-band edit (Claude, another process) makes the step
    # unsafe and it is refused, not forced.

    # Returns [:ok, label] | [:empty] | [:conflict, label] | [:unsupported_schema]
    def undo! = history_step(-1)
    def redo! = history_step(1)

    # Create one task from a complete typed command in one checked transaction.
    # Unlike the retired capture! path, recurrence and initial notes are part of
    # the same write and journal step as the new record itself.
    def create_task!(command, today: Date.today)
      unless command.is_a?(CreateTask)
        return MutationResult.new(status: :invalid, errors: ["expected a Tasks::CreateTask"])
      end

      with_lock do
        clear_rollback
        before = snapshot
        refusal = unsupported_schema_refusal
        return refusal if refusal
        begin
          preflight = create_preflight_failure
          if preflight
            return MutationResult.new(status: :store_invalid, errors: [preflight])
          end

          attributes, validation = normalize_create_task(command, today: today)
          unless validation.empty?
            return MutationResult.new(status: :invalid, errors: validation.values.flatten,
                                      field_errors: validation)
          end

          records = fresh_records(@org)
          working_records = duplicate_records(records)
          planned = plan_create_task(working_records, attributes, today: today)
          unless planned[:status] == :ok
            return MutationResult.new(status: planned[:status], errors: planned[:errors] || [],
                                      field_errors: planned[:field_errors] || {})
          end

          # Serialize before replacing the file so encoding/JSON errors are an
          # invalid command result, never a partially installed task record.
          Format.dump(planned[:records])
        rescue JSON::GeneratorError, EncodingError, ArgumentError => e
          return MutationResult.new(status: :invalid, errors: [safe_patch_error(e)])
        end

        begin
          write_records(@org, planned[:records])
          if (reason = post_write_failure)
            record_rollback(reason, stage: :validation)
            restore(before)
            return MutationResult.new(status: :store_invalid, errors: [reason], rolled_back: true,
                                      rollback_stage: @last_rollback_stage)
          end

          after = snapshot
          @journal.record(label: "capture: #{attributes[:title]}", before: before, after: after)
          reload!
          ri = locate_stable_index(@records, planned[:id])
          MutationResult.new(
            status: :ok,
            snapshot: ri && build_edit_snapshot(@records, ri),
            read_snapshot: @read_snapshot,
            store_revision: store_revision_for(after),
            touched_ids: [planned[:id]],
            summary: { parent_id: planned[:parent_id], inserted_id: planned[:id] }
          )
        rescue StandardError => e
          record_rollback(safe_patch_error(e), stage: :write)
          restore(before)
          MutationResult.new(status: :unavailable, errors: [safe_patch_error(e)], rolled_back: true,
                             rollback_stage: @last_rollback_stage)
        end
      end
    end

    # Hard-delete a task's subtree from the live file in one checked transaction.
    # Follows apply_task_changeset!'s transaction shape (with_lock, snapshot,
    # preflight refusal, atomic write, post-write rollback, one journal entry,
    # reload). Deletion is never a repair route: an invalid file refuses. The
    # archive is never consulted or written — an archived-only id is not found,
    # and this is not an alias for CANCELLED.
    def delete_task!(command)
      unless command.is_a?(DeleteTask)
        return MutationResult.new(status: :invalid, errors: ["expected a Tasks::DeleteTask"])
      end

      with_lock do
        clear_rollback
        before = snapshot
        refusal = unsupported_schema_refusal
        return refusal if refusal
        current = nil
        begin
          unless command.id.is_a?(String) && !command.id.empty?
            return MutationResult.new(status: :invalid, errors: ["task id is required"])
          end
          if !command.expected_revision.nil? && revision_components(command.expected_revision).nil?
            return MutationResult.new(status: :invalid, errors: ["malformed expected_revision"])
          end

          # Check raw validity before parsing: deletion gets no repair mode, so
          # any preflight failure refuses outright and writes nothing.
          preflight = Check.check(@org)
          unless preflight.ok?
            return MutationResult.new(status: :store_invalid, errors: preflight.errors.map(&:last))
          end

          records = fresh_records(@org)
          existing_index = records.index { |record| record["id"] == command.id }
          # An archived-only id is absent from the live file: the archive is
          # read-only, so it is simply not found here.
          return MutationResult.new(status: :not_found) unless existing_index
          unless records[existing_index]["type"] == "task"
            return MutationResult.new(status: :invalid, errors: ["delete targets tasks"])
          end

          ri = existing_index
          current = build_edit_snapshot(records, ri)

          if command.expected_revision
            revision_error = delete_revision_error(current, command.expected_revision)
            return MutationResult.new(status: revision_error, snapshot: current) if revision_error
          end

          rj = subtree_end(records, ri)
          removed = records[ri...rj]
          removed_task_ids = removed.filter_map { |record| record["id"] if record["type"] == "task" }
          descendant_tasks = removed.drop(1).select { |record| record["type"] == "task" }

          unless command.cascade || descendant_tasks.empty?
            return MutationResult.new(
              status: :conflict, snapshot: current,
              summary: {
                descendants: descendant_tasks.length,
                open_descendants: descendant_tasks.count { |record| OPEN_STATES.include?(record["state"]) },
              }
            )
          end

          title = records[ri]["title"]
          working_records = duplicate_records(records)
          working_records[ri...rj] = []
          # Serialize before replacing the file so an encoding/JSON error is an
          # invalid result, never a half-removed subtree.
          Format.dump(working_records)
        rescue JSON::GeneratorError, EncodingError, ArgumentError => e
          return MutationResult.new(status: :invalid, snapshot: current, errors: [safe_patch_error(e)])
        end

        label = command.history_label || delete_history_label(title, removed_task_ids.length)
        begin
          write_records(@org, working_records)
          if (reason = post_write_failure)
            record_rollback(reason, stage: :validation)
            restore(before)
            return MutationResult.new(status: :store_invalid,
                                      snapshot: restored_edit_snapshot(command.id), errors: [reason],
                                      rolled_back: true,
                                      rollback_stage: @last_rollback_stage)
          end
          after = snapshot
          @journal.record(label: label, before: before, after: after)
          reload!
          MutationResult.new(
            status: :ok,
            store_revision: store_revision_for(after),
            touched_ids: removed_task_ids,
            summary: {
              removed: removed_task_ids.length,
              descendants: descendant_tasks.length,
              open_descendants: descendant_tasks.count { |record| OPEN_STATES.include?(record["state"]) },
            }
          )
        rescue StandardError => e
          record_rollback(safe_patch_error(e), stage: :write)
          restore(before)
          MutationResult.new(status: :unavailable, errors: [safe_patch_error(e)], rolled_back: true,
                             rollback_stage: @last_rollback_stage)
        end
      end
    end

    # Accept or decline one proposal in a single checked transaction. This is
    # intentionally stricter than an arbitrary state patch: intent endpoints
    # only target PROPOSED tasks and proposal trees are decided leaves-first.
    def decide_proposal!(command, today: Date.today)
      unless command.is_a?(ProposalDecision)
        return MutationResult.new(status: :invalid, errors: ["expected a Tasks::ProposalDecision"])
      end
      unless ProposalDecision::ACTIONS.include?(command.action)
        return MutationResult.new(status: :invalid, errors: ["proposal action must be approve or reject"])
      end

      with_lock do
        clear_rollback
        before = snapshot
        current = nil
        refusal = unsupported_schema_refusal
        return refusal if refusal
        begin
          preflight = Check.check(@org)
          unless preflight.ok?
            return MutationResult.new(status: :store_invalid, errors: preflight.errors.map(&:last))
          end
          unless command.id.is_a?(String) && !command.id.empty?
            return MutationResult.new(status: :invalid, errors: ["task id is required"])
          end
          if !command.expected_revision.nil? && revision_components(command.expected_revision).nil?
            return MutationResult.new(status: :invalid, errors: ["malformed expected_revision"])
          end

          records = fresh_records(@org)
          ri = locate_stable_index(records, command.id)
          return MutationResult.new(status: :not_found) unless ri
          current = build_edit_snapshot(records, ri)
          if command.expected_revision
            revision_error = changeset_revision_error(
              current,
              TaskChangeset.new(
                id: command.id, changes: { state: records[ri]["state"] },
                expected_revision: command.expected_revision
              )
            )
            return MutationResult.new(status: revision_error, snapshot: current) if revision_error
          end

          from = records[ri]["state"]
          unless PROPOSED_STATES.include?(from)
            return MutationResult.new(
              status: :invalid, snapshot: current,
              errors: ["task is #{from}, not PROPOSED"],
              summary: { action: command.action, from: from }
            )
          end

          rj = subtree_end(records, ri)
          proposed_descendants = records[(ri + 1)...rj].select do |record|
            record["type"] == "task" && PROPOSED_STATES.include?(record["state"])
          end
          unless proposed_descendants.empty?
            return MutationResult.new(
              status: :conflict, snapshot: current,
              errors: ["decide proposed descendants first"],
              summary: {
                action: command.action,
                proposed_descendant_ids: proposed_descendants.map { |record| record["id"] },
              }
            )
          end

          if command.notes && !command.notes.empty? && command.action != :reject
            return MutationResult.new(
              status: :invalid, snapshot: current,
              errors: ["notes are only allowed when rejecting a proposal"]
            )
          end
          note_text = proposal_note_text(command.notes)
          if command.notes && note_text.nil?
            return MutationResult.new(
              status: :invalid, snapshot: current,
              errors: ["reject notes must be valid UTF-8 text"]
            )
          end

          target = command.action == :approve ? "INBOX" : "CANCELLED"
          working_records = duplicate_records(records)
          applied = patch_state(
            working_records, ri, target, today: today,
            allow_proposed_ancestor: command.action == :approve
          )
          unless applied[:status] == :ok
            return MutationResult.new(status: applied[:status], snapshot: current,
                                      errors: applied[:errors] || [], summary: applied[:summary])
          end
          unless note_text.nil? || note_text.empty?
            working_records[ri]["body"] = append_body(working_records[ri]["body"], note_text)
          end
          Format.dump(working_records)
        rescue JSON::GeneratorError, EncodingError, ArgumentError => e
          return MutationResult.new(status: :invalid, snapshot: current,
                                    errors: [safe_patch_error(e)])
        end

        begin
          write_records(@org, working_records)
          if (reason = post_write_failure)
            record_rollback(reason, stage: :validation)
            restore(before)
            return MutationResult.new(status: :store_invalid,
                                      snapshot: restored_edit_snapshot(command.id),
                                      errors: [reason], rolled_back: true,
                                      rollback_stage: @last_rollback_stage)
          end
          after = snapshot
          label = "#{command.action} proposal: #{records[ri]["title"]}"
          @journal.record(label: label, before: before, after: after)
          reload!
          fresh_ri = locate_stable_index(@records, command.id)
          MutationResult.new(
            status: :ok,
            snapshot: fresh_ri && build_edit_snapshot(@records, fresh_ri),
            read_snapshot: @read_snapshot,
            store_revision: store_revision_for(after),
            touched_ids: [command.id],
            summary: { action: command.action, from: "PROPOSED", to: target }
          )
        rescue StandardError => e
          record_rollback(safe_patch_error(e), stage: :write)
          restore(before)
          MutationResult.new(status: :unavailable, snapshot: current,
                             errors: [safe_patch_error(e)], rolled_back: true,
                             rollback_stage: @last_rollback_stage)
        end
      end
    end

    # -- delegation ------------------------------------------------------------
    #
    # Five primitives over the optional `delegation` object (see
    # Tasks::Delegation). Each is one checked transaction with the same shape as
    # decide_proposal!: the mutation lock, a preflight Check, an optional `own`
    # revision precondition, an atomic write, post-write rollback, one journal
    # entry, and a reload. Refusals are typed MutationResults, never exceptions:
    # :invalid for a precondition the caller can fix, :conflict for a race it
    # lost (a held claim, a worker mismatch), :no_change for an idempotent
    # repeat that must not burn an undo slot.
    #
    # The single hard guarantee is single pickup: claim is a compare-and-set
    # from `ready` to `claimed` performed under the lock against freshly read
    # bytes, so two workers can never both believe they hold one task. Reads
    # never grant ownership.
    #
    # `coalesce_key` is the same journal seam editor writes use: two mutations
    # sharing one key (and one coalesce scope) collapse into a single undo step.
    # Tasks::Application composes a delegation with a second write that belongs
    # to the same user action — the WAITING default behind `delegate --to`, the
    # blocker note behind `release --note` — so the owner undoes one action
    # rather than half of one.

    # Delegate to a person (`kind: "human"` + email assignee) or to the agent
    # pool (`kind: "agent"` + mode), replacing any delegation already present.
    # Only accepted live tasks qualify; PROPOSED, closed, and archived tasks
    # refuse with an error naming the state. A live claim refuses outright —
    # the owner revokes explicitly with undelegate! rather than yanking a
    # worker's task out from under it. `work_ref` survives a replacement of the
    # same kind (a mode update keeps pointing at the same work) and is dropped
    # when the delegation changes kind.
    def delegate_task!(id, kind:, mode: nil, assignee: nil, expected_revision: nil,
                       coalesce_key: nil)
      delegation_mutation!(id, expected_revision: expected_revision,
                           coalesce_key: coalesce_key) do |rec|
        plan_delegate(rec, kind: kind, mode: mode, assignee: assignee)
      end
    end

    # Clear the marker: undelegate, or revoke a live claim. Revocation wins —
    # afterwards the stale worker's release/work_ref fail their worker match.
    # Allowed whatever the delegation's status and whatever the task's state
    # (clearing provenance from a closed task is an owner's prerogative);
    # an undelegated task is :no_change.
    def undelegate_task!(id, expected_revision: nil)
      delegation_mutation!(id, expected_revision: expected_revision,
                           allow_repair: true) { |rec| plan_undelegate(rec) }
    end

    # Atomic pickup: compare-and-set {kind agent, status ready} to
    # {status claimed, assignee: worker}. An already-claimed task returns
    # :conflict naming the current holder and its `at`, including when the
    # holder is this same worker — a claim is granted once, never re-granted.
    def claim_task!(id, worker:, expected_revision: nil)
      delegation_mutation!(id, expected_revision: expected_revision) do |rec|
        plan_claim(rec, worker: worker)
      end
    end

    # Hand a claim back: claimed → ready, dropping the assignee. A worker must
    # supply the id that matches the live claim; the owner passes force: true
    # (no worker id) to clear a stale claim without undelegating.
    def release_task!(id, worker: nil, force: false, expected_revision: nil,
                      coalesce_key: nil)
      delegation_mutation!(id, expected_revision: expected_revision,
                           coalesce_key: coalesce_key) do |rec|
        plan_release(rec, worker: worker, force: force)
      end
    end

    # Record where the work lives (ticket, PR, brief, session). One reference:
    # setting overwrites, and nil clears it. The owner (worker: nil) may always
    # set it; a worker only while its claim matches. Not a status transition,
    # so `at` is left alone.
    def set_work_ref!(id, work_ref, worker: nil, expected_revision: nil)
      delegation_mutation!(id, expected_revision: expected_revision) do |rec|
        plan_work_ref(rec, work_ref: work_ref, worker: worker)
      end
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

    # Create a new empty section in one checked transaction. With parent_id nil
    # the section is appended at end of file as a top-level list; with a parent
    # section id it is inserted as the LAST child of that section's subtree, which
    # the DFS pre-order invariant keeps valid. The id is minted like task ids
    # (unique across live + archive). An empty/first-run file is bootstrapped with
    # a meta record first. Returns the new section id, or false when the title is
    # blank or parent_id names no section.
    def create_section!(title:, parent_id: nil)
      title = utf8(title.to_s).strip
      return false if title.empty?

      with_history("create section: #{title}") { create_section_impl(title, parent_id) }
    end

    # Atomically create one project section, bootstrapping the top-level
    # Projects root in the same checked write when it is absent. Returning one
    # MutationResult prevents a failed child insertion from leaving a root and
    # a separate undo entry behind.
    def create_project!(title:)
      title = utf8(title.to_s).strip
      if title.empty?
        return MutationResult.new(status: :invalid, errors: ["title cannot be blank"],
                                  field_errors: { title: ["cannot be blank"] })
      end

      with_lock do
        clear_rollback
        before = snapshot
        refusal = unsupported_schema_refusal
        return refusal if refusal

        begin
          if (preflight = create_preflight_failure)
            return MutationResult.new(status: :store_invalid, errors: [preflight])
          end

          records = fresh_records(@org)
          records = [meta_record] if records.empty?
          root_index = records.index do |record|
            record["type"] == "section" && !record["parent"] &&
              record["title"].to_s.strip.casecmp?("Projects")
          end
          created_root = root_index.nil?
          ids = ids_of(records) + archived_ids
          if created_root
            root_id = gen_id(ids)
            records << { "type" => "section", "id" => root_id, "title" => "Projects" }
            root_index = records.length - 1
            ids << root_id
          else
            root_id = records[root_index]["id"]
          end

          duplicate = records.any? do |record|
            record["type"] == "section" && record["parent"] == root_id &&
              record["title"].to_s.strip.casecmp?(title)
          end
          if duplicate
            message = "a project or area named #{title.inspect} already exists"
            return MutationResult.new(status: :invalid, errors: [message],
                                      field_errors: { title: [message] })
          end

          project_id = gen_id(ids)
          insert_at = subtree_end(records, root_index)
          records.insert(insert_at, {
                           "type" => "section", "id" => project_id,
                           "title" => title, "parent" => root_id,
                         })
          Format.dump(records)
        rescue JSON::GeneratorError, EncodingError, ArgumentError => e
          return MutationResult.new(status: :invalid, errors: [safe_patch_error(e)])
        end

        begin
          write_records(@org, records)
          if (reason = post_write_failure)
            record_rollback(reason, stage: :validation)
            restore(before)
            return MutationResult.new(status: :store_invalid, errors: [reason],
                                      rolled_back: true,
                                      rollback_stage: @last_rollback_stage)
          end

          after = snapshot
          @journal.record(label: "create project: #{title}", before: before, after: after)
          reload!
          MutationResult.new(
            status: :ok, read_snapshot: @read_snapshot, store_revision: store_revision_for(after),
            touched_ids: created_root ? [project_id, root_id] : [project_id],
            summary: { created_id: project_id, root_id: root_id, created_root: created_root }
          )
        rescue StandardError => e
          record_rollback(safe_patch_error(e), stage: :write)
          restore(before)
          MutationResult.new(status: :unavailable, errors: [safe_patch_error(e)],
                             rolled_back: true,
                             rollback_stage: @last_rollback_stage)
        end
      end
    end

    # The record of the section matching `name`, resolved with capture's widening
    # tiers (find_section): exact top-level, exact any-level, substring top-level,
    # substring any-level — all case-insensitive. Lets a move destination reach a
    # nested project sub-section by name, not just a top-level heading. Returns
    # the record hash, or nil.
    def section_named(name)
      records = current_read_snapshot.live_records
      i = find_section(records, name.to_s)
      i && records[i]
    end

    # Retitle a section in one checked transaction. Returns the section id, or
    # false when the id names no section or the title is blank.
    def rename_section!(id:, to:)
      title = utf8(to.to_s).strip
      return false if title.empty?

      with_history("rename section: #{title}") { rename_section_impl(id, title) }
    end

    # Close every open descendant task of a section (DONE + today's closed date,
    # drops defer, retires recur — the close_open_descendants cascade). Returns
    # the count closed (0 is a clean no-op), or false when the id names no
    # section.
    def complete_project!(id:, today: Date.today)
      with_history("complete project: #{id}") { complete_project_impl(id, today) }
    end

    # Move a section's entire contiguous subtree to the archive, mirroring the
    # sweep's serialization: the root section drops its parent and gains today's
    # `archived` stamp. Open tasks do not block — blocking is caller policy —
    # but an undecided proposal is never archival material. Returns the moved
    # stable ids, :proposed_descendants, or false when the id names no section.
    def archive_project!(id:)
      with_history("archive project: #{id}") { archive_project_impl(id) }
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

    # Apply an atomic multi-field semantic change. TaskChangeset's revision is
    # Store-produced and semantic: the field baseline digest never includes a
    # line number or mtime, while location and lifecycle digests protect the
    # wider effects of a move or state change.
    def apply_changeset!(changeset, today: Date.today, temporal_context: nil)
      apply_task_changeset!(changeset, strict_revision: true, today: today,
                            temporal_context: temporal_context)
    end

    # Apply one field-owned semantic change. TaskPatch remains the adapter
    # convenience for existing CLI/TUI save-on-blur paths; it delegates all
    # mutation work to the same changeset transaction below, retaining its
    # established narrow expected-value conflict check.
    def patch_task!(patch, today: Date.today, temporal_context: nil)
      unless patch.respond_to?(:id) && patch.respond_to?(:field) &&
             patch.respond_to?(:value) && patch.respond_to?(:expected)
        return MutationResult.new(status: :invalid, errors: ["expected a Tasks::TaskPatch"])
      end

      changeset = patch.respond_to?(:to_changeset) ? patch.to_changeset : TaskChangeset.from_patch(patch)
      field = normalize_patch_field(patch.field)
      apply_task_changeset!(
        changeset,
        strict_revision: false,
        field_expectations: { field => patch.expected },
        today: today, temporal_context: temporal_context
      )
    end

    # Shared transaction for TaskChangeset and TaskPatch. All field changes are
    # first applied to a detached records copy; an invalid later field therefore
    # cannot leak a partial in-memory mutation into a file write or journal step.
    def apply_task_changeset!(changeset, strict_revision:, today:, field_expectations: nil,
                              temporal_context: nil)
      unless changeset.is_a?(TaskChangeset)
        return MutationResult.new(status: :invalid, errors: ["expected a Tasks::TaskChangeset"])
      end

      with_lock do
        clear_rollback
        before = snapshot
        current = nil
        repair = false
        refusal = unsupported_schema_refusal
        return refusal if refusal
        begin
          # Check raw validity before parsing/building: Format.parse assumes a
          # valid UTF-8 String, while Check deliberately contains bad bytes.
          preflight = Check.check(@org)
          unless preflight.ok?
            # Targeted repair: a field-owned patch (never a strict-revision
            # changeset, never a create) may fix its OWN invalid record, but
            # only when every preflight Check error is attributable to that one
            # record (see repair_scope?). A revision or conflict baseline built
            # over malformed data isn't trustworthy, so strict-revision callers
            # keep refusing an invalid file outright.
            repair = !strict_revision && repair_scope?(preflight, changeset.id)
            unless repair
              return MutationResult.new(status: :store_invalid,
                                        errors: preflight.errors.map(&:last))
            end
          end

          validation = validate_changeset(changeset)
          unless validation.empty?
            return MutationResult.new(status: :invalid, errors: validation.values.flatten,
                                      field_errors: validation)
          end

          records = fresh_records(@org)
          ri = locate_stable_index(records, changeset.id)
          return MutationResult.new(status: :not_found) unless ri

          current = build_edit_snapshot(records, ri)
          placement_targets = resolve_changeset_placement_targets(records, current, changeset)
          if placement_targets && placement_targets[:status] != :ok
            return MutationResult.new(
              status: placement_targets[:status], snapshot: current,
              errors: placement_targets[:errors] || [],
              field_errors: placement_targets[:field_errors] || {},
              summary: placement_targets[:summary]
            )
          end

          if strict_revision
            revision_error = changeset_revision_error(current, changeset)
            return MutationResult.new(status: revision_error, snapshot: current) if revision_error
          end

          # Repair mode's `current` snapshot is derived from malformed source,
          # so the ordinary conflict gates (confirmation, field expectations)
          # would compare live values against untrustworthy baselines. The
          # post-write Check is the real safety net here: it must pass
          # COMPLETELY or the write rolls back (see post_write_failure below).
          unless repair || confirmation_matches?(current, changeset.confirmation)
            return MutationResult.new(status: :conflict, snapshot: current)
          end

          if field_expectations && !repair
            field_expectations.each do |field, expected|
              actual = patch_expected_for(current, field)
              unless semantic_patch_equal?(field, actual, expected)
                return MutationResult.new(status: :conflict, snapshot: current)
              end
            end
          end

          original_records = Format.dump(records)
          working_records = duplicate_records(records)
          applied = apply_changeset_fields(
            working_records, changeset, today: today, temporal_context: temporal_context,
            placement_targets: placement_targets
          )
          if applied[:status] != :ok
            return MutationResult.new(status: applied[:status], snapshot: current,
                                      errors: applied[:errors] || [],
                                      field_errors: applied[:field_errors] || {},
                                      summary: applied[:summary])
          end
          proposed_records = Format.dump(working_records)
        rescue JSON::GeneratorError, EncodingError, ArgumentError => e
          return MutationResult.new(status: :invalid, snapshot: current,
                                    errors: [safe_patch_error(e)])
        end

        if proposed_records == original_records
          reload!
          return MutationResult.new(status: :no_change, snapshot: current,
                                    read_snapshot: @read_snapshot,
                                    store_revision: store_revision_for(before),
                                    summary: applied[:summary])
        end

        label = changeset.history_label || changeset_history_label(changeset, current)
        begin
          write_records(@org, working_records)
          if (reason = post_write_failure)
            record_rollback(reason, stage: :validation)
            restore(before)
            return MutationResult.new(status: :store_invalid,
                                      snapshot: restored_edit_snapshot(changeset.id),
                                      errors: [reason], rolled_back: true,
                                      rollback_stage: @last_rollback_stage)
          end
          after = snapshot
          @journal.record(label: label, before: before, after: after,
                          coalesce_key: changeset.coalesce_key, repair: repair)
          reload!
          fresh_ri = locate_stable_index(@records, changeset.id)
          MutationResult.new(
            status: :ok,
            snapshot: fresh_ri && build_edit_snapshot(@records, fresh_ri),
            read_snapshot: @read_snapshot,
            store_revision: store_revision_for(after),
            touched_ids: applied[:touched_ids],
            summary: applied[:summary]
          )
        rescue StandardError => e
          record_rollback(safe_patch_error(e), stage: :write)
          restore(before)
          MutationResult.new(status: :unavailable,
                             snapshot: restored_edit_snapshot(changeset.id),
                             errors: [safe_patch_error(e)], rolled_back: true,
                             rollback_stage: @last_rollback_stage)
        end
      end
    end
    private :apply_task_changeset!

    # Ensure the item carries a stable id, returning it. Idempotent: an item
    # that already has one is returned untouched (no write). Post-migration ids
    # always exist; this is the repair path for a record somehow missing one.
    def ensure_id!(item)
      return item.id if item.id
      with_history("id: #{item.title}") { ensure_id_impl(item) }
    end

    # Converge a readable-but-unwritable store in ONE pass, and write once.
    #
    # `ensure_id!` above, and Format's "dropped on the next write" comment for an
    # unknown key inside a temporal object, are both RECORD repairs. Every
    # mutation pre- or post-flights Check over the WHOLE file, so either repair
    # lands only when its record is the file's last remaining error. With two or
    # more instances the file never validates, every attempt refuses or rolls
    # back, and the store is readable but unrepairable except by hand
    # (td-d6ed92, td-2addce). This is the command that closes that loop: it fixes
    # every instance it knows about across the store, then writes — so the file
    # Check sees is already converged.
    #
    # It repairs the ARCHIVE as well as the live file, because `post_write_failure`
    # validates both: a live-only repair would leave every mutation still refusing
    # on account of the archive, which is the same dead end one file over.
    #
    # Two invariants, both load-bearing:
    #
    #   * It never writes a partially repaired file. The repaired records are
    #     re-Checked in memory first, and any remaining error refuses the whole
    #     pass with nothing written. Unparseable lines therefore always refuse —
    #     Check folds Format's parse errors in — so a write can never silently
    #     drop a line this binary could not read.
    #   * It never touches `updated`. See #write_records' `stamp:` argument.
    def repair!(dry_run: false)
      with_lock do
        if unsupported_schema?
          source, version = unsupported_schema_source
          return RepairResult.new(
            status: :unsupported_schema, fixes: [], written: false, dry_run: dry_run,
            blockers: [RepairBlocker.new(file: repair_file_name(source), line: 1,
                                         message: Check.unsupported_version_message(version))]
          )
        end

        plans = repair_plans
        fixes = plans.flat_map { |plan| plan[:fixes] }
        blockers = plans.flat_map { |plan| plan[:blockers] }
        if blockers.any?
          return RepairResult.new(status: :unrepairable, fixes: fixes, blockers: blockers,
                                  written: false, dry_run: dry_run)
        end
        if fixes.empty? || dry_run
          return RepairResult.new(status: :ok, fixes: fixes, blockers: [],
                                  written: false, dry_run: dry_run)
        end

        # `repair: true` marks the journal step the same way a targeted repair
        # marks its own, so `undo` restores the malformed bytes on request
        # instead of refusing to write a file that fails today's invariants.
        wrote = with_history("repair store", repair: true) do
          plans.each do |plan|
            next if plan[:fixes].empty?
            write_records(plan[:path], plan[:records], stamp: false)
          end
          reload!
          true
        end
        unless wrote
          return RepairResult.new(
            status: :unrepairable, fixes: fixes, written: false, dry_run: dry_run,
            blockers: [RepairBlocker.new(file: nil, line: 0,
                                         message: last_rollback || "validation failed after the repair")]
          )
        end

        RepairResult.new(status: :ok, fixes: fixes, blockers: [], written: true, dry_run: dry_run)
      end
    end

    # Items parsed from the archive file (source: :archive). Not cached — the
    # archive is read rarely (`list -x/-a`) and appended rarely.
    def archive_items
      current_read_snapshot(include_archive: true).archive_items
    end

    # Lightweight schema-version check for adapter mutations whose own
    # transaction has more specific invalid-store diagnostics. This reads only
    # meta versions; it does not replace the mutation's validation gate. True
    # when either file declares a schema version this binary cannot read — a
    # refusal, never an invitation to convert the file.
    def unsupported_schema? = !unsupported_schema_source.nil?

    # The diagnostic for a store this build cannot read, or nil when the store
    # is current: "unsupported meta version 1 (expected 2)", prefixed with
    # "archive: " when the skew is in the archive rather than the live file.
    #
    # Public because every surface owes the operator the same sentence, and the
    # only useful part of it — which version, in which file — is knowable only
    # here. A refusal that says merely "unsupported schema version" tells the
    # user to go find another build without telling them which one.
    def unsupported_schema_error
      source, version = unsupported_schema_source
      return nil if source.nil?

      message = unsupported_meta_message(version)
      source == :archive ? "archive: #{message}" : message
    end

    private

    # -- creation --------------------------------------------------------------

    # Empty/missing live files are an intentional first-run state: creation
    # bootstraps their meta and Inbox records. Any non-empty file (including an
    # archive) must already validate before a create command is allowed to
    # inspect or extend it.
    def create_preflight_failure
      [@org, (@archive if File.exist?(@archive))].compact.each do |path|
        next if path == @org && (!File.exist?(path) || File.zero?(path))

        result = Check.check(path)
        return result.errors.first&.last || "validation failed" unless result.ok?
      end
      nil
    end

    # [source, version] for the first file whose meta record declares a schema
    # version this binary does not implement, or nil when both files are current.
    # Only an Integer version counts: any other shape is ordinary invalid data
    # and belongs to Check, which reports it as such.
    #
    # This runs on EVERY command (the CLI gates its whole dispatch on it), so it
    # reads line 1 rather than parsing the file: the version header is the only
    # thing it may consult, and an archive can be large.
    #
    # The rescue is per-file and narrow on purpose. A blanket rescue around the
    # loop would let an unreadable live file suppress the archive half of the
    # gate and report the store "supported" — the gate would be skipped by
    # exactly the I/O trouble that should make it more cautious. What it means
    # to tolerate is a file this method cannot open or decode: that is Check's
    # story to tell, with a line number, not a version-skew refusal.
    def unsupported_schema_source
      [[:live, @org], [:archive, (@archive if File.exist?(@archive))]].each do |source, path|
        next if path.nil?

        version = declared_meta_skew(path)
        return [source, version] if version
      end
      nil
    end

    # The declared version of `path`'s meta record when it is one this build
    # cannot read, else nil. Unopenable, empty, or unparseable files are nil.
    def declared_meta_skew(path)
      return nil if File.zero?(path)

      first = File.open(path, "r", encoding: "UTF-8", &:gets)
      return nil if first.nil?

      Check.unsupported_version(Format.parse(first).records)
    rescue SystemCallError, IOError, EncodingError
      nil
    end

    def unsupported_meta?(records) = !Check.unsupported_version(records).nil?

    def unsupported_meta_message(version) = Check.unsupported_version_message(version)

    # The shared refusal every mutation returns for a store this binary cannot
    # read. It writes nothing and offers no conversion: a store at another
    # schema version needs the matching binary, not a rewrite by this one.
    def unsupported_schema_refusal
      message = unsupported_schema_error
      return nil if message.nil?

      MutationResult.new(status: :unsupported_schema, errors: [message])
    end

    def normalize_create_task(command, today:)
      errors = Hash.new { |fields, field| fields[field] = [] }
      title = normalize_create_text(command.title, :title, errors, required: true)
      priority = normalize_create_priority(command.priority, errors)
      tags = normalize_create_tags(command.tags, errors)
      deferred = normalize_create_deferred(command.deferred, errors)
      normalize_create_apply_host_context(command.apply_host_context, errors)
      tags << DEFER_TAG if deferred && !tags.include?(DEFER_TAG)
      scheduled = normalize_create_temporal(command.scheduled, :scheduled, errors)
      deadline = normalize_create_temporal(command.deadline, :deadline, errors)
      state = normalize_create_state(command.state, errors)
      project = normalize_create_project(command.project, errors)
      parent_id = normalize_create_parent_id(command.parent_id, errors)
      recurrence = normalize_create_recurrence(command.recurrence, errors)
      lead = normalize_create_lead(command.lead, errors)
      notes = normalize_create_notes(command, errors)

      if project && parent_id
        errors[:location] << "project and parent_id cannot both be supplied"
      end

      # Capturing with a recurrence has always meant "start repeating now" when
      # a date was omitted. Keep that behavior in the command, not the CLI, so
      # every transport gets one definition of a recurring create — but with a
      # LEAD that reading is wrong: today's anchor puts the window in the past,
      # so the task appears immediately and the schedule's own first occurrence
      # is never used. A lead therefore seeds the first occurrence instead,
      # which is what makes `--recur y:06-01 --lead 17d` mean "invisible until
      # May 15" with no further arguments.
      if recurrence && !deadline && !scheduled
        seed = lead && first_occurrence(recurrence, today: today)
        scheduled = TemporalValue.new(date: seed || today)
      end
      state ||= (scheduled || deadline ? "TODO" : "INBOX")
      if recurrence && (CLOSED_STATES + PROPOSED_STATES).include?(state)
        errors[:state] << "can't set recurrence on a #{state} task"
      end
      if recurrence
        # Completion rolls the deadline when both dates exist, so that is the
        # stamp the schedule has to be reachable from.
        reason = unreachable_recurrence(recurrence, (deadline || scheduled)&.date, today: today)
        errors[:recurrence] << reason if reason
      end

      if lead
        # The same five rules patch_lead enforces, stated against the values
        # this create is about to write (docs/plans/implemented/recurring-lead-time.md
        # §5). A create that would need an immediate repair is a create that
        # should have been refused.
        anchor = Lead.anchor_date(deadline&.date, scheduled&.date)
        if anchor.nil?
          errors[:lead] << "a lead time needs a date to hide before — " \
                           "add a deadline or an available-from date first"
        elsif deadline && scheduled
          errors[:lead] << lead_gate_conflict_message(lead)
        else
          gate = Lead.date_bound(anchor, lead)
          unless gate && gate.iso8601.match?(Check::DATE_RE)
            errors[:lead] << "a lead of #{Lead.humanize(lead)} would open before #{anchor.iso8601}, " \
                             "outside the four-digit years dates are stored with"
          end
        end
      end

      [
        {
          title: title, priority: priority, tags: tags, scheduled: scheduled,
          deadline: deadline, state: state, project: project, parent_id: parent_id,
          recurrence: recurrence, lead: lead, notes: notes,
        },
        errors,
      ]
    end

    def normalize_create_text(value, field, errors, required: false)
      if value.nil?
        errors[field] << "#{field} is required" if required
        return nil
      end
      unless value.is_a?(String)
        errors[field] << "#{field} must be text"
        return nil
      end

      text = utf8(value)
      unless text.valid_encoding?
        errors[field] << "#{field} must be valid UTF-8 text"
        return nil
      end
      text = text.strip if field == :title
      if required && text.empty?
        errors[field] << "#{field} cannot be blank"
        return nil
      end
      text
    end

    def normalize_create_priority(value, errors)
      return nil if value.nil?
      return value if Check::PRIORITIES.include?(value)

      errors[:priority] << "priority must be A, B, C, or nil"
      nil
    end

    def normalize_create_tags(value, errors)
      unless value.is_a?(Array)
        errors[:tags] << "tags must be a list of tags"
        return []
      end
      unless value.all? { |tag| tag.is_a?(String) }
        errors[:tags] << "tags must be a list of tags"
        return []
      end

      tags = value.map { |tag| utf8(tag) }
      errors[:tags] << "tags must be valid UTF-8 text" unless tags.all?(&:valid_encoding?)
      tags
    end

    def normalize_create_deferred(value, errors)
      return value if value == true || value == false

      errors[:deferred] << "deferred must be true or false"
      false
    end

    def normalize_create_apply_host_context(value, errors)
      return value if value == true || value == false

      errors[:apply_host_context] << "apply_host_context must be true or false"
      false
    end

    def normalize_create_temporal(value, field, errors)
      return nil if value.nil? || value == ""
      return value if value.is_a?(TemporalValue)
      return TemporalValue.new(date: value) if value.is_a?(Date) || value.is_a?(String)

      errors[field] << "#{field} must be a temporal value or nil"
      nil
    rescue ArgumentError, Date::Error => e
      errors[field] << "#{field} #{e.message}"
      nil
    end

    def normalize_create_state(value, errors)
      return nil if value.nil?
      return value if Check::STATES.include?(value)

      errors[:state] << "invalid task state"
      nil
    end

    def normalize_create_project(value, errors)
      return nil if value.nil?

      project = normalize_create_text(value, :project, errors)
      errors[:project] << "project cannot be blank" if project&.empty?
      project&.empty? ? nil : project
    end

    def normalize_create_parent_id(value, errors)
      return nil if value.nil?
      unless value.is_a?(String) && Check::ID_RE.match?(value)
        errors[:parent_id] << "parent_id must be a stable task id"
        return nil
      end

      value
    end

    def normalize_create_recurrence(value, errors)
      return nil if value.nil?
      return value if value.is_a?(String) && Recur.cookie?(value)

      errors[:recurrence] << "invalid recurrence cookie"
      nil
    end

    # The date a schedule would first fire on, or nil when it cannot be
    # projected (an unreachable calendar rule); the caller then falls back to
    # today and the ordinary satisfiability guard reports the real problem.
    def first_occurrence(recurrence, today:)
      Recur.next_date(recurrence, from: today, today: today)
    rescue ArgumentError, Date::Error
      nil
    end

    def normalize_create_lead(value, errors)
      return nil if value.nil?
      return value if Lead.span?(value)

      errors[:lead] << "invalid lead time (expected a span like 3w, 2d, 1m, 1y)"
      nil
    end

    def normalize_create_notes(command, errors)
      if !command.body.nil? && !command.notes.nil?
        errors[:body] << "body and notes cannot both be supplied"
        return []
      end

      supplied = command.notes.nil? ? command.body : command.notes
      return [] if supplied.nil?
      supplied = supplied.split("\n", -1) if supplied.is_a?(String)
      unless supplied.is_a?(Array) && supplied.all? { |note| note.is_a?(String) }
        errors[:body] << "initial notes must be text or an ordered list of text"
        return []
      end

      notes = supplied.map { |note| utf8(note) }
      errors[:body] << "initial notes must be valid UTF-8 text" unless notes.all?(&:valid_encoding?)
      notes
    end

    def plan_create_task(records, attributes, today:)
      if attributes[:parent_id]
        pi = records.index { |record| record["id"] == attributes[:parent_id] }
        return { status: :not_found } unless pi
        unless %w[task section].include?(records[pi]["type"])
          return { status: :invalid, errors: ["parent_id must identify a task or section"] }
        end
        if records[pi]["type"] == "task" &&
           PROPOSED_STATES.include?(records[pi]["state"]) &&
           !PROPOSED_STATES.include?(attributes[:state])
          return { status: :invalid, errors: ["accepted work cannot be created under a proposed task"] }
        end

        # A section parent files the task directly beneath the heading (depth 1),
        # so only a task parent can push past the nesting cap.
        if records[pi]["type"] == "task"
          by_id = records.to_h { |record| [record["id"], record] }
          if task_depth(by_id, records[pi]) + 1 > @max_depth
            return { status: :too_deep,
                     errors: ["would exceed max depth #{@max_depth} (max_depth config / TASKS_MAX_DEPTH)"] }
          end
        end
        parent_id = records[pi]["id"]
        # Append at the end of the parent's subtree — after any existing task
        # and section children — which the DFS pre-order invariant keeps valid.
        insert_at = subtree_end(records, pi)
      elsif records.empty?
        records = [meta_record,
                   { "type" => "section", "id" => gen_id(archived_ids),
                     "title" => (attributes[:project] || "Inbox") }]
        si = records.length - 1
        parent_id = records[si]["id"]
        insert_at = subtree_end(records, si)
      else
        si = find_section(records, attributes[:project] || "Inbox")
        return { status: :invalid, errors: ["capture project does not exist"] } unless si

        parent_id = records[si]["id"]
        insert_at = subtree_end(records, si)
      end

      id = gen_id(ids_of(records) + archived_ids)
      rec = { "type" => "task", "id" => id, "parent" => parent_id,
              "state" => attributes[:state], "title" => attributes[:title] }
      rec["priority"] = attributes[:priority] if attributes[:priority]
      rec["tags"] = attributes[:tags] unless attributes[:tags].empty?
      write_temporal(rec, "scheduled", attributes[:scheduled]) if attributes[:scheduled]
      write_temporal(rec, "deadline", attributes[:deadline]) if attributes[:deadline]
      rec["recur"] = attributes[:recurrence] if attributes[:recurrence]
      rec["lead"] = attributes[:lead] if attributes[:lead]
      rec["body"] = (["Captured [#{today}]."] + attributes[:notes]).join("\n")

      records[insert_at, 0] = [rec]
      { status: :ok, records: records, id: id, parent_id: parent_id }
    end

    # -- reading ---------------------------------------------------------------

    # The cached Store-facing convenience reads all come from one snapshot. A
    # caller that needs a stable multi-step read should keep the public
    # #read_snapshot result instead; this cache only preserves the existing
    # Store surface and its reload-on-live-change behavior.
    #
    # Returns the snapshot this call built or found, never a re-read of the
    # ivar: a concurrent reader may replace @read_snapshot (last publish wins),
    # but each caller must get a snapshot satisfying its own archive request.
    def current_read_snapshot(include_archive: false)
      snapshot = @read_snapshot
      return snapshot unless read_snapshot_stale?(snapshot, include_archive)

      with_lock do
        # Re-check under the lock: another thread may have just reloaded.
        snapshot = @read_snapshot
        if read_snapshot_stale?(snapshot, include_archive)
          snapshot = publish_read_snapshot(read_snapshot(include_archive: include_archive))
        end
        snapshot
      end
    end

    def read_snapshot_stale?(snapshot, include_archive)
      return true if snapshot.nil? || changed?

      include_archive && (!snapshot.archive_loaded? || archive_changed?)
    end

    # Install a freshly built snapshot as the Store-wide read cache. Must run
    # under the lock so a mutation's own locked reads of @records can never
    # interleave with another thread's publication.
    def publish_read_snapshot(snapshot)
      @read_snapshot = snapshot
      @stat = snapshot.live_stat
      @archive_stat = snapshot.archive_stat if snapshot.archive_loaded?
      @records = snapshot.live_records
      @cache = snapshot.items
      @tree = snapshot.tree
      @nodes_by_line = snapshot.nodes_by_line
      snapshot
    end

    # The staleness key for a file: [mtime, inode, size] — the same triple the
    # read cache keys on, so two out-of-band writes within one coarse mtime tick
    # (which bare mtime can't tell apart) still register as a change. nil when
    # the file is absent.
    # Public class-level form so a holder of a ReadSnapshot (whose live_stat
    # uses this exact triple) can test its own staleness against the file
    # without borrowing a Store instance's cache state.
    def self.stat_key(path)
      st = File.stat(path)
      [st.mtime, st.ino, st.size]
    rescue Errno::ENOENT
      nil
    end

    def stat_key(path)
      self.class.stat_key(path)
    end

    def archive_changed?
      stat_key(@archive) != @archive_stat
    end

    # A snapshot captures bytes, parsed records, and its staleness key from one
    # file descriptor. API-grade reads additionally validate that exact parse;
    # ordinary CLI/TUI snapshots avoid paying for a structural Check they do not
    # consume. If Atomic.write installs a newer inode after this open, the later
    # stat comparison notices it rather than claiming old bytes are current.
    # Missing archive files are an empty optional history; the live file is
    # required by checked reads.
    def capture_read_source(path, optional: false, validate: false)
      File.open(path, "r", encoding: "UTF-8") do |file|
        stat = file.stat
        raw = file.read
        if raw.valid_encoding?
          parsed = Format.parse(raw)
          check = Check.check_parsed(parsed) if validate
          records = parsed.records
        else
          records = []
          check = Check::Result.new([[0, "file is not valid UTF-8"]], []) if validate
        end
        {
          raw: raw.freeze,
          records: records,
          stat: [stat.mtime, stat.ino, stat.size].freeze,
          check: check,
        }.freeze
      end
    rescue Errno::ENOENT
      return empty_read_source(validate: validate) if optional

      {
        raw: nil,
        records: [],
        stat: nil,
        check: validate ? Check::Result.new([[0, "file not found"]], []) : nil,
      }.freeze
    end

    def empty_read_source(validate: false)
      {
        raw: nil,
        records: [],
        stat: nil,
        check: validate ? Check::Result.new([], []) : nil,
      }.freeze
    end

    def build_read_snapshot(live, archive, include_archive:)
      live_records = live[:records]
      archive_records = include_archive ? archive[:records] : []
      ReadSnapshot.new(
        live_records: live_records, live_stat: live[:stat],
        archive_records: archive_records,
        archive_stat: include_archive ? archive[:stat] : nil,
        archive_loaded: include_archive, item_builder: method(:build_item),
        task_revisions: {
          live: task_revisions(live_records),
          archive: include_archive ? task_revisions(archive_records) : {},
        },
        link_shorthands: @link_shorthands, link_systems: @link_systems
      )
    end

    def annotated_check_entries(entries, source)
      entries.map do |line, message|
        { source: source, line: line, message: message }
      end
    end

    def store_revision_for_contents(live, archive)
      digest = Digest::SHA256.new
      [live, archive].each do |content|
        if content.nil?
          digest << [-1].pack("q>")
        else
          bytes = content.b
          digest << [bytes.bytesize].pack("q>") << bytes
        end
      end
      "s1.#{digest.hexdigest}"
    end

    def store_revision_for(snapshot)
      store_revision_for_contents(snapshot[:org], snapshot[:archive])
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
        scheduled_value: TemporalValue.from_record(rec, :scheduled),
        deadline_value: TemporalValue.from_record(rec, :deadline),
        recur: rec["recur"], lead: rec["lead"], lead_skip: rec["lead_skip"],
        id: rec["id"]&.to_s, closed: to_date(rec["closed"]),
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

    # Whether a preflight failure is repairable by a patch that targets `id`:
    # true only when EVERY Check error lies on the single record that patch will
    # rewrite. Raw-safety comes first — an invalid-UTF-8 file, or any line that
    # isn't parseable JSON (Format.parse yields an error entry), keeps refusing
    # even when it is the targeted line, because Format.parse/Check can't reason
    # about bytes they would misparse. With the file parseable, locate the target
    # by stable id and require each error's line to equal the target's line; an
    # error anywhere else means the fix wouldn't leave the file fully clean, so
    # we refuse exactly as before and the CLI shows the "already invalid" hint.
    #
    # Line-number attribution is the weak part, and it is worth being honest
    # about what actually protects this. Check reports the file's meta problems
    # against line 1 — "missing meta record on line 1", a wrong `type`, a
    # version this build cannot read. When the file HAS a meta record those
    # errors cannot collide with a task, because a task never sits on line 1.
    # When it does not, the first task IS on line 1, the meta error is
    # attributed to it, and "every error is on my record" becomes true of an
    # error the patch cannot fix. The write then runs and the post-write Check
    # rolls it back — no data is lost, but the caller gets "file failed
    # validation after the edit — run `tasks check`" for a file that was
    # already invalid before the edit, which points at the wrong thing.
    #
    # So the rollback is the safety net, not the invariant. The line below is
    # the invariant: a repairable target never sits on line 1, which makes
    # "line 1 belongs to meta, every other line belongs to a record" true rather
    # than merely usual. A store with no meta record needs `tasks check`, not a
    # field patch that would be rolled back after writing.
    def repair_scope?(preflight, id)
      return false unless id.is_a?(String) && !id.empty?
      raw = File.read(@org, encoding: "UTF-8")
      return false unless raw.valid_encoding?
      parsed = Format.parse(raw)
      return false unless parsed.errors.empty?
      target = parsed.records.find { |record| record["type"] == "task" && record["id"] == id }
      return false unless target
      return false if target["line"] == 1

      preflight.errors.all? { |line, _| line == target["line"] }
    rescue Errno::ENOENT, SystemCallError, IOError
      false
    end

    def normalize_patch_field(field)
      field = field.to_sym
      field == :recur ? :recurrence : field
    rescue NoMethodError
      field
    end

    # Composite commands are not editor fields: tag_delta owns a tag-set delta,
    # activate owns the availability pair, and date_clear owns coupled dates.
    def patch_field?(field)
      EditSnapshot::FIELDS.include?(field) || %i[tag_delta activate date_clear].include?(field)
    end

    def patch_expected_for(snapshot, field)
      case field
      when :tag_delta then snapshot.metadata.fetch(:tag_sequence)
      when :date_clear then snapshot.metadata.fetch(:date_state)
      else snapshot.expected_for(field)
      end
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
      values = edit_values(rec, tags: tags, contexts: contexts, ordinary_tags: ordinary_tags)
      scheduled_value = TemporalValue.from_record(rec, :scheduled)
      deadline_value = TemporalValue.from_record(rec, :deadline)
      EditSnapshot.new(
        id: rec["id"], title: values[:title], priority: values[:priority],
        deferred: values[:deferred], scheduled: values[:scheduled],
        deadline: values[:deadline], scheduled_value: scheduled_value,
        deadline_value: deadline_value, recurrence: values[:recurrence],
        lead: values[:lead], lead_skip: rec["lead_skip"],
        contexts: contexts, tags: ordinary_tags, body: values[:body],
        parent: rec["parent"], state: rec["state"], closed: to_date(rec["closed"]),
        baselines: values,
        fingerprints: {
          location: location_fingerprint(records, ri),
          state: lifecycle_fingerprint(records, ri),
        },
        revision: task_revision(values, records, ri),
        metadata: {
          line: rec["line"],
          tag_sequence: tags,
          date_state: {
            scheduled: temporal_expectation(scheduled_value),
            deadline: temporal_expectation(deadline_value), recurrence: values[:recurrence],
          },
          parent_type: parent && parent["type"],
          parent_title: parent && parent["title"],
          subtree_ids: records[ri...subtree_end(records, ri)].filter_map { |record| record["id"] },
        }
      )
    end

    def edit_values(rec, tags: semantic_tags(rec), contexts: nil, ordinary_tags: nil)
      contexts ||= tags.select { |tag| tag.start_with?("@") }
      ordinary_tags ||= tags.reject { |tag| tag.start_with?("@") || tag == DEFER_TAG }
      {
        title: rec["title"],
        priority: rec["priority"],
        deferred: tags.include?(DEFER_TAG),
        scheduled: to_date(rec["scheduled"]),
        deadline: to_date(rec["deadline"]),
        recurrence: rec["recur"],
        lead: rec["lead"],
        contexts: contexts,
        tags: ordinary_tags,
        body: rec["body"].is_a?(String) ? rec["body"] : "",
        location: rec["parent"],
        state: rec["state"],
      }
    end

    def temporal_expectation(value)
      value&.all_day? ? value.date : value
    end

    # `siblings_by_parent` is a bulk-computation index (see task_revisions); it
    # must yield the exact id list the inline scan produces, or the same task
    # would carry different revisions depending on which path built it.
    def location_fingerprint(records, ri, siblings_by_parent: nil)
      rec = records[ri]
      rj = subtree_end(records, ri)
      structural = records[ri...rj].map do |record|
        [record["type"], record["id"], record["parent"]]
      end
      siblings = if siblings_by_parent
                   siblings_by_parent.fetch(rec["parent"], [])
                 else
                   records.filter_map do |record|
                     record["id"] if record["parent"] == rec["parent"]
                   end
                 end
      semantic_digest([rec["parent"], siblings, structural])
    end

    def lifecycle_fingerprint(records, ri)
      rj = subtree_end(records, ri)
      owned = records[ri...rj].filter_map do |record|
        next unless record["type"] == "task"
        tags = semantic_tags(record)
        [record["id"], record["parent"], record["state"], record["closed"],
         record["scheduled"], record["scheduled_time"],
         record["deadline"], record["deadline_time"], record["recur"],
         tags.include?(DEFER_TAG)]
      end
      semantic_digest(owned)
    end

    def semantic_digest(value)
      Digest::SHA256.hexdigest(JSON.generate(value))
    end

    # Revision strings stay opaque at the application boundary, but keeping
    # their three semantic components separate lets Store ignore a sibling list
    # change for a title-only update while still guarding moves and cascades.
    # Date values are normalized before hashing so equivalent Store snapshots
    # never depend on Ruby object identity or JSONL serialization details.
    def task_revision(values, records, ri, siblings_by_parent: nil)
      rec = records[ri]
      own_fields = REVISION_OWN_FIELDS.map { |field| [field, revision_value(values[field])] }
      # Time metadata is part of the task's own semantic value: a stale
      # zone/time edit must fail exactly like a stale date edit. Normalized
      # stored objects only — never derived instants, so a tzdata update
      # cannot invalidate revisions.
      own_fields << [:scheduled_time, revision_value(rec["scheduled_time"])]
      own_fields << [:deadline_time, revision_value(rec["deadline_time"])]
      # Delegation is an own-field change (ADR-0007), so an HTTP If-Match claim
      # gets its compare-and-set from the same revision component every other
      # field edit uses — and a stale editor cannot overwrite a fresh claim.
      own_fields << [:delegation, revision_value(rec[Delegation::FIELD])]
      own = semantic_digest(own_fields)
      location = location_fingerprint(records, ri, siblings_by_parent: siblings_by_parent)
      lifecycle = lifecycle_fingerprint(records, ri)
      "v1.#{own}.#{location}.#{lifecycle}"
    end

    def task_revisions(records)
      # One sibling index for the whole pass: the per-task inline sibling scan
      # made every snapshot build quadratic in list size.
      siblings = sibling_ids_by_parent(records)
      records.each_with_index.each_with_object({}) do |(record, index), revisions|
        next unless record["type"] == "task" && record["id"]

        revisions[record["id"]] =
          task_revision(edit_values(record), records, index, siblings_by_parent: siblings)
      end
    end

    def sibling_ids_by_parent(records)
      records.each_with_object({}) do |record, map|
        id = record["id"]
        (map[record["parent"]] ||= []) << id unless id.nil?
      end
    end

    def revision_value(value)
      case value
      when Date
        value.iso8601
      when Hash
        value.keys.sort_by(&:to_s).map { |key| [key.to_s, revision_value(value[key])] }
      when Array
        value.map { |item| revision_value(item) }
      else
        value
      end
    end

    def revision_components(revision)
      return nil unless revision.is_a?(String)

      version, own, location, lifecycle = revision.split(".", -1)
      return nil unless version == "v1" && [own, location, lifecycle].all? { |part| /\A[0-9a-f]{64}\z/.match?(part) }

      { own: own, location: location, lifecycle: lifecycle }
    end

    # A cascading delete must be refused if the task, its siblings, or any
    # descendant changed since the revision was captured, so — unlike an
    # ordinary field edit — it compares ALL THREE revision components. The
    # supplied revision is already validated as parseable by the caller.
    def delete_revision_error(current, expected_revision)
      expected = revision_components(expected_revision)
      return :invalid unless expected

      actual = revision_components(current.revision)
      %i[own location lifecycle].any? { |part| actual.fetch(part) != expected.fetch(part) } ? :stale : nil
    end

    def delete_history_label(title, removed_count)
      return "delete: #{title}" if removed_count <= 1

      "delete #{removed_count} tasks: #{title}"
    end

    def changeset_revision_error(current, changeset)
      expected = revision_components(changeset.expected_revision)
      return :invalid unless expected

      actual = revision_components(current.revision)
      required = [:own]
      fields = changeset.ordered_fields
      if fields.include?(:location) && !changeset.changes[:location].is_a?(TaskPlacement)
        required << :location
      end
      required << :lifecycle if fields.include?(:state)
      required.uniq.any? { |part| actual.fetch(part) != expected.fetch(part) } ? :stale : nil
    end

    def validate_changeset(changeset)
      errors = Hash.new { |fields, field| fields[field] = [] }
      unless changeset.id.is_a?(String) && !changeset.id.empty?
        errors[:id] << "task id is required"
      end
      unless changeset.changes.is_a?(Hash) && !changeset.changes.empty?
        errors[:changes] << "changes must be a non-empty mapping"
        return errors
      end
      unless changeset.duplicate_fields.empty?
        errors[:changes] << "changes repeat #{changeset.duplicate_fields.map(&:inspect).join(", ")}"
      end

      fields = changeset.ordered_fields
      unknown = fields.reject { |field| patch_field?(field) }
      errors[:changes].concat(unknown.map { |field| "unknown editable field #{field.inspect}" }) unless unknown.empty?

      tag_fields = %i[contexts tags deferred]
      if fields.include?(:tag_delta) && !(fields & tag_fields).empty?
        errors[:changes] << "tag_delta cannot be combined with tag slice changes"
      end
      if fields.include?(:date_clear) && !(fields & %i[scheduled deadline]).empty?
        errors[:changes] << "date_clear cannot be combined with scheduled or deadline"
      end
      if fields.include?(:activate) && !(fields & %i[deferred scheduled]).empty?
        errors[:changes] << "activate cannot be combined with deferred or scheduled"
      end
      validate_changeset_location(changeset.changes[:location], errors) if fields.include?(:location)
      errors
    end

    def validate_changeset_location(location, errors)
      return if location.equal?(TaskChangeset::UNNEST)

      if location.is_a?(TaskPlacement)
        unless stable_task_id?(location.parent_id)
          errors[:parent_id] << "parent_id must be a stable id"
        end
        unless location.before_id.nil? || stable_task_id?(location.before_id)
          errors[:before_id] << "before_id must be a stable id or nil"
        end
        return
      end

      unless stable_task_id?(location)
        errors[:location] << "location must be a stable parent id, UNNEST, or Tasks::TaskPlacement"
      end
    end

    def stable_task_id?(value)
      value.is_a?(String) && value.valid_encoding? && value.encoding.ascii_compatible? &&
        Check::ID_RE.match?(value)
    rescue ArgumentError, Encoding::CompatibilityError
      false
    end

    def duplicate_records(records)
      JSON.parse(JSON.generate(records))
    end

    def apply_changeset_fields(records, changeset, today:, temporal_context: nil,
                               placement_targets: nil)
      touched_ids = []
      summaries = {}
      changeset.ordered_fields.each do |field|
        ri = locate_stable_index(records, changeset.id)
        return { status: :not_found } unless ri

        applied = apply_semantic_patch(
          records, ri, field, changeset.changes.fetch(field), force: changeset.force,
          today: today, temporal_context: temporal_context, placement_targets: placement_targets
        )
        return applied unless applied[:status] == :ok

        touched_ids.concat(applied[:touched_ids] || [])
        summaries[field] = applied[:summary] if applied[:summary]
      end

      # Recurrence and lead time are intents ABOUT a date, so a write that
      # clears the last date retires them too — judged after the WHOLE
      # changeset, never mid-flight, because a changeset that MOVES the anchor
      # (clear one date, set the other) passes through a momentary dateless
      # state and would otherwise lose a window the user was relocating.
      #
      # Only a date-owning field triggers it. `activate` also leaves a task
      # dateless, and it deliberately preserves recurrence: activation owns
      # availability, not the recurrence contract.
      if (changeset.ordered_fields & DATE_OWNING_FIELDS).any? &&
         (ri = locate_stable_index(records, changeset.id))
        clear_dateless_intent(records[ri])
      end

      fields = changeset.ordered_fields
      summary = if fields.length == 1
                  summaries[fields.first]
                else
                  { fields: fields, by_field: summaries }
                end
      { status: :ok, touched_ids: touched_ids.uniq, summary: summary }
    end

    def changeset_history_label(changeset, current)
      fields = changeset.ordered_fields
      return "edit #{fields.first}: #{current.title}" if fields.length == 1

      "edit #{fields.join(", ")}: #{current.title}"
    end

    def semantic_patch_equal?(field, actual, expected)
      case field
      when :scheduled, :deadline
        normalized = normalize_patch_date(expected)
        normalized != :invalid && actual == normalized
      when :contexts, :tags, :tag_delta
        actual == Array(expected)
      when :date_clear
        actual == expected
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
      return value if value.is_a?(TemporalValue)
      return value if value.is_a?(Date)
      return Date.iso8601(value) if value.is_a?(String)
      :invalid
    rescue ArgumentError, Date::Error
      :invalid
    end

    def write_temporal(record, key, value)
      record[key] = value.date.iso8601
      metadata = value.time_metadata
      metadata ? record["#{key}_time"] = metadata : record.delete("#{key}_time")
      record
    end

    def restored_edit_snapshot(id)
      records = @records || fresh_records(@org)
      ri = locate_stable_index(records, id)
      ri && build_edit_snapshot(records, ri)
    end

    def apply_semantic_patch(records, ri, field, value, force: false, today:, temporal_context: nil,
                             placement_targets: nil)
      case field
      when :title      then patch_title(records, ri, value)
      when :priority   then patch_priority(records, ri, value)
      when :deferred   then patch_deferred(records, ri, value)
      when :activate   then patch_activate(records, ri, value, today: today, temporal_context: temporal_context)
      when :scheduled  then patch_date(records, ri, value, :scheduled)
      when :deadline   then patch_date(records, ri, value, :deadline)
      when :date_clear then patch_date_clear(records, ri, value)
      when :recurrence then patch_recurrence(records, ri, value, today: today)
      when :lead       then patch_lead(records, ri, value)
      when :contexts   then patch_tag_slice(records, ri, value, :contexts)
      when :tags       then patch_tag_slice(records, ri, value, :tags)
      when :tag_delta  then patch_tag_delta(records, ri, value)
      when :body       then patch_body(records, ri, value)
      when :location   then patch_location(records, ri, value, force: force, placement_targets: placement_targets)
      when :state      then patch_state(records, ri, value, today: today, temporal_context: temporal_context)
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

    # Composite "available now" operation. Unlike generic date editing, this
    # intentionally preserves recurrence when a future scheduled date was its
    # only anchor: activation owns availability, not the recurrence contract.
    # A later completion will require the user to establish a new occurrence
    # date, but activation must never silently discard the cookie.
    def patch_activate(records, ri, value, today:, temporal_context: nil)
      return patch_invalid("activate must be true") unless value == true

      rec = records[ri]
      tags = semantic_tags(rec)
      tags.delete(DEFER_TAG)
      replace_optional(rec, "tags", tags)
      scheduled = TemporalValue.from_record(rec, :scheduled, validate: false)
      future = if scheduled&.local_time && temporal_context
                 scheduled.release_instant(temporal_context) > temporal_context.now
               else
                 scheduled && scheduled.date > today
               end
      # A lead task releases the CURRENT OCCURRENCE, stamped by its anchor date,
      # and keeps every date it has: the anchor is what the next window is
      # measured from, and the roll re-arms it.
      #
      # Only a LEAD task takes this path: for everything else, including a
      # recurring task with no lead, activation keeps its long-standing meaning
      # of clearing a future available-from date.
      anchor = Lead.anchor_date(to_date(rec["deadline"]), to_date(rec["scheduled"]))
      if anchor && Lead.span?(rec["lead"])
        rec["lead_skip"] = anchor.iso8601
        return patch_ok(rec)
      end
      if future
        rec.delete("scheduled")
        rec.delete("scheduled_time")
      end
      patch_ok(rec)
    end

    def patch_date(records, ri, value, kind)
      date = normalize_patch_date(value)
      return patch_invalid("#{kind} must be a date/time or nil") if date == :invalid
      rec = records[ri]
      key = kind.to_s
      if date
        # Rule 3: the lead owns the task's own timed gate. A lead task may not
        # end up carrying BOTH dates, from either direction — adding an
        # available-from date beside a deadline-anchored lead, or adding a
        # deadline to a scheduled-anchored one (which flips the anchor and
        # leaves the available-from date a second, silently ignored gate).
        other = kind == :scheduled ? "deadline" : "scheduled"
        if Lead.span?(rec["lead"]) && rec[other]
          return patch_invalid(lead_gate_conflict_message(rec["lead"]))
        end

        temporal = date.is_a?(TemporalValue) ? date : TemporalValue.new(date: date)
        write_temporal(rec, key, temporal)
        rec["state"] = "TODO" if rec["state"] == "INBOX"
      else
        rec.delete(key)
        rec.delete("#{key}_time")
      end
      clear_lead_skip(rec)
      patch_ok(rec)
    end

    # `undate` owns both date fields and their coupled recurrence cookie. Keep
    # that legacy CLI operation one checked write and one undo entry instead of
    # exposing an observable intermediate state between two single-date patches.
    def patch_date_clear(records, ri, value)
      kind = value.is_a?(String) || value.is_a?(Symbol) ? value.to_sym : value
      return patch_invalid("date clear kind must be deadline, scheduled, or nil") unless kind.nil? || %i[deadline scheduled].include?(kind)

      rec = records[ri]
      fields = kind ? [kind] : %i[scheduled deadline]
      return patch_invalid("no matching date stamp") unless fields.any? { |field| rec[field.to_s] }

      fields.each do |field|
        rec.delete(field.to_s)
        rec.delete("#{field}_time")
      end
      clear_lead_skip(rec)
      patch_ok(rec)
    end

    # A recurrence and a lead time are both intents ABOUT a date, so neither can
    # outlive the last one. Clearing the final anchor retires them in the same
    # changeset — one undo step, and never a stored value with nothing to
    # measure from (which Check would then have to report).
    def clear_dateless_intent(rec)
      return if rec["scheduled"] || rec["deadline"]

      rec.delete("recur")
      rec.delete("lead")
    end

    # Attach, replace, or clear the lead-time window. The five rules the plan
    # states (docs/plans/implemented/recurring-lead-time.md §5) all land here, so
    # every surface refuses the same shapes with the same words. Clearing is
    # always allowed — a refusal a user cannot undo is a trap.
    def patch_lead(records, ri, value)
      rec = records[ri]
      if value.nil? || value == :off
        rec.delete("lead")
        rec.delete("lead_skip")
        return patch_ok(rec)
      end

      # Rule 4: grammar. The canonical span is what reaches the store; friendly
      # phrasings are an adapter's job (Lead.parse_result), so a non-canonical
      # value here is a caller bug, not a user typo.
      unless Lead.span?(value)
        return patch_invalid("invalid lead time #{value.inspect} (expected a span like 3w, 2d, 1m, 1y)")
      end
      # Rule 1: a lead needs an anchor to measure back from. There is
      # deliberately no state rule here — a lead is an intent about a date, and
      # it is accepted wherever the date fields themselves are (a proposal
      # included), unlike recurrence, which `done` has to be able to roll.
      anchor = Lead.anchor_date(to_date(rec["deadline"]), to_date(rec["scheduled"]))
      unless anchor
        return patch_invalid("a lead time needs a date to hide before — " \
                             "add a deadline or an available-from date first")
      end
      # Rule 3: one own timed gate.
      if rec["deadline"] && rec["scheduled"]
        return patch_invalid(lead_gate_conflict_message(value))
      end
      # Rule 5: the derived gate must stay a storable date.
      gate = Lead.date_bound(anchor, value)
      unless gate && gate.iso8601.match?(Check::DATE_RE)
        return patch_invalid("a lead of #{Lead.humanize(value)} would open before " \
                             "#{anchor.iso8601}, outside the four-digit years dates are stored with")
      end

      rec["lead"] = value
      # A new window supersedes any occurrence a previous one released early.
      rec.delete("lead_skip")
      patch_ok(rec)
    end

    # Rule 3's one message, shared by the two writes that can create the
    # conflict (setting the lead, and setting an available-from date beside a
    # deadline-anchored one), so the user reads the same fix either way.
    def lead_gate_conflict_message(span)
      "a lead time of #{Lead.humanize(span)} hides this task before its date — " \
        "carrying a deadline AND an available-from date beside it would leave a " \
        "second, ignored gate. Clear one of them " \
        "(`tasks undate <ref> --kind scheduled`, or `tasks lead <ref> off`)."
    end

    # `lead_skip` releases ONE occurrence, identified by the anchor date it was
    # stamped with. Any write that moves or removes an anchor therefore retires
    # it; the derivation also compares the stamp against the current anchor, so
    # a stale stamp a foreign writer leaves behind still cannot release a
    # different occurrence.
    def clear_lead_skip(rec)
      rec.delete("lead_skip")
    end

    def patch_recurrence(records, ri, value, today:)
      rec = records[ri]
      if !value.nil? && value != :off && PROPOSED_STATES.include?(rec["state"])
        return patch_invalid("can't set recurrence on a PROPOSED task")
      end
      return patch_invalid("recurrence requires a scheduled date or deadline") unless rec["scheduled"] || rec["deadline"]
      if value.nil? || value == :off
        rec.delete("recur")
      else
        return patch_invalid("invalid recurrence cookie") unless value.is_a?(String) && Recur.cookie?(value)

        anchor = to_date(rec["deadline"]) || to_date(rec["scheduled"])
        if (reason = unreachable_recurrence(value, anchor, today: today))
          return patch_invalid(reason)
        end

        rec["recur"] = value
      end
      patch_ok(rec)
    end

    # A cookie can parse cleanly and still be unwritable, in two ways, both of
    # which leave a task nothing can ever complete:
    #
    #   unreachable — `2y:02:5fri` anchored in an odd year needs a February with
    #     five Fridays, and odd years are never leap, so the roll has no target
    #     and `done` refuses it.
    #   unstorable — `+9999y` (or `9999y:07-04`) rolls past the four-digit years
    #     a stored date is written with, so the roll would succeed and then fail
    #     the post-write check, rolling every completion back forever.
    #
    # So the write computes the one occurrence it would produce and refuses the
    # cookie up front, with the engine's own reason where there is one. Both
    # shapes can hit this: a calendar schedule can be unreachable, and either
    # shape can overshoot the storable range.
    def unreachable_recurrence(cookie, anchor, today:)
      return nil unless anchor.is_a?(Date)

      date = Recur.next_date(cookie, from: anchor, today: today)
      return nil if date.iso8601.match?(Check::DATE_RE)

      "recurrence would roll to #{date.iso8601}, outside the four-digit years dates are stored with"
    rescue ArgumentError, Date::Error => error
      error.message
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

    # The CLI's `tag` verb owns the whole ordered tag sequence: it may add and
    # remove contexts, plain tags, and the defer marker in one undoable write.
    # The editor keeps narrower context/tag slices, so this private patch field
    # preserves the CLI's historical order and atomic add/remove semantics
    # without weakening those field boundaries.
    def patch_tag_delta(records, ri, value)
      return patch_invalid("tag changes must contain add and remove lists") unless value.is_a?(Hash)

      add = value[:add] || value["add"]
      remove = value[:remove] || value["remove"]
      return patch_invalid("tag changes must contain add and remove lists") unless
        add.is_a?(Array) && remove.is_a?(Array) &&
        (add + remove).all? { |tag| tag.is_a?(String) }

      add = add.map { |tag| utf8(tag) }
      remove = remove.map { |tag| utf8(tag) }
      rec = records[ri]
      tags = semantic_tags(rec).reject { |tag| remove.include?(tag) }
      add.each do |tag|
        tags << tag unless tags.include?(tag)
      end
      replace_optional(rec, "tags", tags)
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

    # The single writer for the rollback pair. Every mutation entry point calls
    # #clear_rollback before it starts and #record_rollback on exactly one of
    # its two rollback arms, so the reason and the stage are always set together
    # or cleared together.
    def clear_rollback
      @last_rollback = nil
      @last_rollback_stage = nil
    end

    def record_rollback(reason, stage:)
      unless MutationResult::ROLLBACK_STAGES.include?(stage)
        raise ArgumentError, "unknown rollback stage #{stage.inspect}"
      end

      @last_rollback_stage = stage
      @last_rollback = reason
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

    def patch_location(records, ri, location, force: false, placement_targets: nil)
      if location.is_a?(TaskPlacement)
        return patch_placement(records, ri, location, targets: placement_targets)
      end

      parent_id = location
      rec = records[ri]
      parent_id = enclosing_section_id(records, rec) if parent_id.equal?(TaskChangeset::UNNEST)
      return patch_invalid("location must be a parent id") unless parent_id.is_a?(String)
      if !force && rec["parent"] == parent_id
        return patch_ok(rec, summary: { from: rec["parent"], to: parent_id, moved_ids: [] })
      end

      pi = records.index { |record| record["id"] == parent_id }
      return patch_invalid("location parent does not exist") unless pi
      return patch_invalid("location parent must be a section or task") unless %w[section task].include?(records[pi]["type"])
      if records[pi]["type"] == "task" &&
         PROPOSED_STATES.include?(records[pi]["state"]) &&
         !PROPOSED_STATES.include?(rec["state"])
        return patch_invalid("accepted work cannot be moved under a proposed task")
      end

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

    def resolve_changeset_placement_targets(records, current, changeset)
      location = changeset.changes[:location]
      return nil unless location.is_a?(TaskPlacement)

      resolve_placement_targets(records, location, from: current.parent_id)
    end

    def resolve_placement_targets(records, placement, from:)
      parent_id = placement.parent_id
      before_id = placement.before_id
      summary = { from: from, to: parent_id, before: before_id }
      pi = records.index do |record|
        record["id"] == parent_id && %w[section task].include?(record["type"])
      end
      ai = if before_id
             records.index { |record| record["id"] == before_id && record["type"] == "task" }
           end
      unless pi
        message = "parent_id does not identify a live task or section"
        return { status: :not_found, errors: [message], field_errors: { parent_id: [message] },
                 summary: summary }
      end
      if before_id && !ai
        message = "before_id does not identify a live task"
        return { status: :not_found, errors: [message], field_errors: { before_id: [message] },
                 summary: summary }
      end

      { status: :ok, parent_index: pi, before_index: ai }
    end

    def patch_placement(records, ri, placement, targets: nil)
      rec = records[ri]
      from = rec["parent"]
      parent_id = placement.parent_id
      before_id = placement.before_id
      summary = { from: from, to: parent_id, before: before_id }
      targets ||= resolve_placement_targets(records, placement, from: from)
      return targets unless targets[:status] == :ok

      pi = targets.fetch(:parent_index)
      ai = targets.fetch(:before_index)
      if records[pi]["type"] == "task" &&
         PROPOSED_STATES.include?(records[pi]["state"]) &&
         !PROPOSED_STATES.include?(rec["state"])
        return patch_invalid("accepted work cannot be moved under a proposed task")
      end

      rj = subtree_end(records, ri)
      if (pi >= ri && pi < rj) || (ai && ai >= ri && ai < rj)
        return { status: :cycle, summary: summary }
      end
      if ai && records[ai]["parent"] != parent_id
        return {
          status: :conflict,
          summary: summary.merge(current_parent_id: records[ai]["parent"]),
        }
      end

      by_id = records.to_h { |record| [record["id"], record] }
      if records[pi]["type"] == "task" &&
         task_depth(by_id, records[pi]) + subtree_height(records, ri) > @max_depth
        return { status: :too_deep, summary: summary }
      end

      moved_ids = records[ri...rj].filter_map do |record|
        record["id"] if record["type"] == "task"
      end
      subtree = records[ri...rj].map(&:dup)
      rest = records[0...ri] + records[rj..]
      new_pi = rest.index { |record| record["id"] == parent_id }
      insert_at = if before_id
                    rest.index { |record| record["id"] == before_id }
                  else
                    subtree_end(rest, new_pi)
                  end
      if placement_satisfied?(rec, parent_id, source_index: ri, insertion_index: insert_at)
        return patch_ok(rec, touched_ids: [], summary: summary.merge(moved_ids: []))
      end

      subtree[0]["parent"] = parent_id
      rest[insert_at, 0] = subtree
      records.replace(rest)
      patch_ok(subtree[0], touched_ids: moved_ids,
               summary: summary.merge(moved_ids: moved_ids))
    end

    # After removing the moving span, its old physical boundary is still
    # `source_index` in the detached array. The placement is satisfied only
    # when the freshly resolved insertion boundary is that exact slot. This
    # includes section subtrees between task children even though sections are
    # never valid moving resources or before-anchors themselves.
    def placement_satisfied?(rec, parent_id, source_index:, insertion_index:)
      rec["parent"] == parent_id && source_index == insertion_index
    end

    def enclosing_section_id(records, record)
      by_id = records.to_h { |candidate| [candidate["id"], candidate] }
      current = record
      while current && (parent = by_id[current["parent"]])
        return parent["id"] if parent["type"] == "section"

        current = parent
      end
      nil
    end

    def patch_state(records, ri, value, today:, temporal_context: nil,
                    allow_proposed_ancestor: false)
      return patch_invalid("invalid task state") unless Check::STATES.include?(value)
      rec = records[ri]
      from = rec["state"]
      if PROPOSED_STATES.include?(value) && Recur.cookie?(rec["recur"])
        return patch_invalid("remove recurrence before setting PROPOSED")
      end
      # Approval and delegation are independent owner decisions, and an
      # undecided proposal carries neither a claim nor an assignee.
      if PROPOSED_STATES.include?(value) && Delegation.object?(rec[Delegation::FIELD])
        return patch_invalid("undelegate before setting PROPOSED")
      end
      if PROPOSED_STATES.include?(value) && !PROPOSED_STATES.include?(from)
        rj = subtree_end(records, ri)
        accepted_descendant = records[(ri + 1)...rj].find do |descendant|
          descendant["type"] == "task" &&
            !PROPOSED_STATES.include?(descendant["state"])
        end
        if accepted_descendant
          return patch_invalid(
            "cannot set PROPOSED while accepted descendants remain"
          )
        end
      end
      if PROPOSED_STATES.include?(from) && value == "DONE"
        return patch_invalid("approve the proposal before completing it")
      end
      if !CLOSED_STATES.include?(value) &&
         !PROPOSED_STATES.include?(value) &&
         !allow_proposed_ancestor &&
         proposed_task_ancestor?(records, rec)
        return patch_invalid("accepted work cannot remain under a proposed task")
      end
      if value == "DONE" && Recur.cookie?(rec["recur"])
        result = advance_recurrence_records(records, ri, today: today,
                                            temporal_context: temporal_context)
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
        rec["closed"] ||= today.iso8601
        settle_delegation_on_close(rec)
        if value == "DONE"
          cascaded_ids = close_open_descendants(records, ri, today: today)
          touched_ids.concat(cascaded_ids)
        end
      elsif DONE_STATES.include?(from) && !DONE_STATES.include?(value)
        rec.delete("closed")
      end
      patch_ok(rec, touched_ids: touched_ids,
               summary: { from: from, to: value, recurrence_advanced: false,
                          cascaded_ids: cascaded_ids })
    end

    def proposed_task_ancestor?(records, record)
      by_id = records.each_with_object({}) do |candidate, index|
        index[candidate["id"]] = candidate if candidate["id"]
      end
      current = record
      while (parent = by_id[current["parent"]])
        return true if parent["type"] == "task" && PROPOSED_STATES.include?(parent["state"])

        current = parent
      end
      false
    end

    def advance_recurrence_records(records, ri, today:, temporal_context: nil)
      rec = records[ri]
      cookie = rec["recur"]
      return patch_invalid("invalid recurrence cookie") unless Recur.cookie?(cookie)
      deadline = to_date(rec["deadline"])
      scheduled = to_date(rec["scheduled"])
      return patch_invalid("recurrence requires a valid date") unless deadline || scheduled

      context = temporal_context || TemporalContext.new(
        now: Time.utc(today.year, today.month, today.day, 12), timezone: "Etc/UTC"
      )

      if deadline
        deadline_value = TemporalValue.from_record(rec, :deadline, validate: false)
        next_deadline = Recur.next_temporal_date(
          cookie, value: deadline_value, kind: :deadline, context: context
        ) do |candidate|
          !scheduled || temporal_candidate_valid?(
            rec, "scheduled", candidate + (scheduled - deadline), context
          )
        end
        if rec["scheduled"]
          return patch_invalid("recurrence requires a valid date") unless scheduled

          rec["scheduled"] = (scheduled + (next_deadline - deadline)).iso8601
        end
        rec["deadline"] = next_deadline.iso8601
      else
        scheduled_value = TemporalValue.from_record(rec, :scheduled, validate: false)
        next_scheduled = Recur.next_temporal_date(
          cookie, value: scheduled_value, kind: :scheduled, context: context
        )
        rec["scheduled"] = next_scheduled.iso8601
      end
      rec["tags"] = semantic_tags(rec) - [DEFER_TAG]
      replace_optional(rec, "tags", rec["tags"])
      # The roll moved the anchor, so any occurrence released early is history
      # and the lead window re-arms against the new one.
      clear_lead_skip(rec)
      rec["body"] = append_body(rec["body"], "- Did [#{today}].")
      roll_delegation_forward(rec)
      patch_ok(rec)
    rescue ArgumentError, Timezones::Error => error
      patch_invalid(error.message)
    end

    def temporal_candidate_valid?(record, field, date, temporal_context)
      metadata = record["#{field}_time"]
      return true unless metadata
      value = TemporalValue.new(date: date, local_time: metadata["local"],
                                timezone: metadata["timezone"], fold: metadata.fetch("fold", 0),
                                validate: false)
      zone_context = temporal_context || TemporalContext.new(now: Time.utc(date.year, date.month, date.day),
                                                             timezone: "Etc/UTC")
      value.instant(zone_context)
      true
    rescue ArgumentError, Timezones::Error
      false
    end

    # -- delegation ------------------------------------------------------------

    # The shared transaction behind every delegation primitive. The block gets
    # the target record from a DETACHED copy and returns a plan hash:
    # {status: :ok, label:, summary:, no_change: false} to write, or a typed
    # refusal ({status: :invalid | :conflict, errors:, summary:}) that writes
    # nothing. Because the block runs under the mutation lock against records
    # read fresh inside it, a compare-and-set the block performs is atomic
    # across processes.
    def delegation_mutation!(id, expected_revision:, coalesce_key: nil, allow_repair: false)
      with_lock do
        clear_rollback
        before = snapshot
        current = nil
        repair = false
        refusal = unsupported_schema_refusal
        return refusal if refusal
        begin
          preflight = Check.check(@org)
          unless preflight.ok?
            # Targeted repair, exactly as apply_task_changeset! grants it to a
            # field-owned patch: undelegate OWNS the delegation field, so it may
            # strip a malformed marker from its own record — the one a version
            # skew or a foreign writer can leave behind. Only when every
            # preflight error is attributable to that record (repair_scope?),
            # and never under an If-Match, whose baseline was built over the
            # malformed bytes. Every other delegation operation keeps refusing.
            repair = allow_repair && expected_revision.nil? && repair_scope?(preflight, id)
            unless repair
              return MutationResult.new(status: :store_invalid, errors: preflight.errors.map(&:last))
            end
          end
          unless id.is_a?(String) && !id.empty?
            return MutationResult.new(status: :invalid, errors: ["task id is required"])
          end
          if !expected_revision.nil? && revision_components(expected_revision).nil?
            return MutationResult.new(status: :invalid, errors: ["malformed expected_revision"])
          end

          records = fresh_records(@org)
          ri = locate_stable_index(records, id)
          return MutationResult.new(status: :not_found) unless ri
          current = build_edit_snapshot(records, ri)
          # A delegation change is an `own`-field change (ADR-0007), so an
          # If-Match claim is guarded by exactly the component that carries it.
          if expected_revision &&
             revision_components(current.revision)[:own] != revision_components(expected_revision)[:own]
            return MutationResult.new(status: :stale, snapshot: current)
          end

          working_records = duplicate_records(records)
          planned = yield(working_records[ri])
          unless planned[:status] == :ok
            return MutationResult.new(status: planned[:status], snapshot: current,
                                      errors: planned[:errors] || [], summary: planned[:summary])
          end
          if planned[:no_change]
            return MutationResult.new(status: :no_change, snapshot: current, summary: planned[:summary])
          end
          # Never write a marker the schema would reject: Check runs post-write
          # and would roll the whole file back, which is a far worse diagnostic
          # than the shape error itself.
          if working_records[ri].key?(Delegation::FIELD)
            shape = Delegation.errors(working_records[ri][Delegation::FIELD])
            return MutationResult.new(status: :invalid, snapshot: current, errors: shape) unless shape.empty?
          end
          Format.dump(working_records)
        rescue JSON::GeneratorError, EncodingError, ArgumentError => e
          return MutationResult.new(status: :invalid, snapshot: current, errors: [safe_patch_error(e)])
        end

        begin
          write_records(@org, working_records)
          if (reason = post_write_failure)
            record_rollback(reason, stage: :validation)
            restore(before)
            return MutationResult.new(status: :store_invalid, snapshot: restored_edit_snapshot(id),
                                      errors: [reason], rolled_back: true,
                                      rollback_stage: @last_rollback_stage)
          end
          after = snapshot
          @journal.record(label: planned[:label], before: before, after: after,
                          coalesce_key: coalesce_key, repair: repair)
          reload!
          fresh_ri = locate_stable_index(@records, id)
          MutationResult.new(
            status: :ok,
            snapshot: fresh_ri && build_edit_snapshot(@records, fresh_ri),
            read_snapshot: @read_snapshot,
            store_revision: store_revision_for(after),
            touched_ids: [id],
            summary: planned[:summary]
          )
        rescue StandardError => e
          record_rollback(safe_patch_error(e), stage: :write)
          restore(before)
          MutationResult.new(status: :unavailable, snapshot: current,
                             errors: [safe_patch_error(e)], rolled_back: true,
                             rollback_stage: @last_rollback_stage)
        end
      end
    end

    def delegation_now = Delegation.stamp(@now.call)

    def delegation_refusal(status, message, summary = nil)
      { status: status, errors: [message], summary: summary }
    end

    # Only accepted live work is delegable: a proposal is an undecided
    # suggestion, a closed task's marker is inert provenance, and an archived
    # record is history. Each refusal names the state it is refusing.
    def delegation_ineligible(rec, verb)
      state = rec["archived"] ? "archived" : rec["state"]
      return nil if !rec["archived"] && OPEN_STATES.include?(rec["state"])

      delegation_refusal(:invalid, "task is #{state}; only accepted live tasks can be #{verb}")
    end

    def delegation_held(rec, action, message)
      existing = rec[Delegation::FIELD]
      delegation_refusal(:conflict, message,
                         { action: action, holder: existing["assignee"], at: existing["at"] })
    end

    def plan_delegate(rec, kind:, mode:, assignee:)
      kind = kind.to_s
      mode = mode.nil? ? nil : utf8(mode.to_s).strip
      assignee = assignee.nil? ? nil : utf8(assignee.to_s).strip
      invalid = delegate_input_error(kind, mode, assignee)
      return delegation_refusal(:invalid, invalid) if invalid
      ineligible = delegation_ineligible(rec, "delegated")
      return ineligible if ineligible

      existing = rec[Delegation::FIELD]
      if Delegation.claimed?(existing)
        return delegation_held(rec, :delegate,
                               "already claimed by #{existing["assignee"]} at #{existing["at"]}; " \
                               "undelegate to revoke the claim first")
      end

      # A mode update or a new assignee of the same kind still points at the
      # same work; a human <-> agent replacement is a different delegation.
      retained = existing["work_ref"] if Delegation.object?(existing) && existing["kind"] == kind
      candidate = Delegation.ordered({
        "kind" => kind, "mode" => mode, "assignee" => assignee, "at" => delegation_now,
        "status" => kind == "human" ? Delegation::DELEGATED : Delegation::READY,
        "work_ref" => retained,
      })
      summary = { action: :delegate, delegation: candidate, previous: existing }
      if delegation_settled?(existing, candidate)
        return { status: :ok, no_change: true,
                 summary: summary.merge(delegation: existing) }
      end

      rec[Delegation::FIELD] = candidate
      label = kind == "human" ? "delegate → #{assignee}: #{rec["title"]}" : "delegate #{mode}: #{rec["title"]}"
      { status: :ok, label: label, summary: summary }
    end

    def delegate_input_error(kind, mode, assignee)
      unless Delegation::KINDS.include?(kind)
        return "delegation kind #{kind.inspect} must be #{Delegation::KINDS.join(" or ")}"
      end
      if kind == "human"
        return "a human delegation has no mode" if mode
        unless Delegation.email?(assignee)
          return "assignee #{assignee.inspect} must be an email address " \
                 "(local@domain.tld, no whitespace or control characters, " \
                 "at most #{Delegation::ASSIGNEE_LIMIT} chars)"
        end
      else
        unless Delegation::MODES.include?(mode)
          return "mode #{mode.inspect} must be one of #{Delegation::MODES.join("/")}"
        end
        return "an agent delegation is claimed by a worker, not assigned" if assignee
      end
      nil
    end

    # Two delegations describe the same state when only the transition stamp
    # differs — re-delegating at the current mode must not burn an undo slot.
    def delegation_settled?(existing, candidate)
      return false unless Delegation.object?(existing)

      existing.reject { |key, _| key == "at" } == candidate.reject { |key, _| key == "at" }
    end

    # Keyed on the field's PRESENCE, not on its shape: undelegate is the repair
    # route for a marker some other writer left malformed (a claimed marker with
    # no assignee, say), so it must be able to strip a value it would never have
    # written itself. An explicit JSON null is the one exception — Check and
    # Format both read it as absent, so removing it would write identical bytes.
    def plan_undelegate(rec)
      existing = rec[Delegation::FIELD]
      if existing.nil?
        return { status: :ok, no_change: true, summary: { action: :undelegate, previous: nil } }
      end

      rec.delete(Delegation::FIELD)
      { status: :ok, label: "undelegate: #{rec["title"]}",
        summary: { action: :undelegate, previous: existing } }
    end

    def plan_claim(rec, worker:)
      worker = worker.nil? ? nil : utf8(worker.to_s).strip
      unless Delegation.worker?(worker)
        return delegation_refusal(:invalid, "worker id #{worker.inspect} must be non-empty, " \
                                            "whitespace-free, free of control characters, " \
                                            "and at most #{Delegation::ASSIGNEE_LIMIT} chars")
      end
      ineligible = delegation_ineligible(rec, "claimed")
      return ineligible if ineligible

      existing = rec[Delegation::FIELD]
      unless Delegation.agent?(existing)
        return delegation_refusal(:invalid, "task is not delegated to the agent pool")
      end
      unless Delegation.ready?(existing)
        return delegation_held(rec, :claim,
                               "already claimed by #{existing["assignee"]} at #{existing["at"]}")
      end

      claimed = Delegation.ordered(existing.merge("status" => Delegation::CLAIMED,
                                                  "assignee" => worker, "at" => delegation_now))
      rec[Delegation::FIELD] = claimed
      { status: :ok, label: "claim: #{rec["title"]}",
        summary: { action: :claim, worker: worker, delegation: claimed, previous: existing } }
    end

    def plan_release(rec, worker:, force:)
      worker = worker.nil? ? nil : utf8(worker.to_s).strip
      existing = rec[Delegation::FIELD]
      unless Delegation.claimed?(existing)
        return delegation_refusal(:invalid, "task is not claimed")
      end
      ineligible = delegation_ineligible(rec, "released")
      return ineligible if ineligible
      unless force || worker == existing["assignee"]
        return delegation_held(rec, :release,
                               "claim is held by #{existing["assignee"]}, not #{worker.inspect}")
      end

      released = Delegation.ordered(existing.merge("status" => Delegation::READY,
                                                   "assignee" => nil, "at" => delegation_now))
      rec[Delegation::FIELD] = released
      { status: :ok, label: "release: #{rec["title"]}",
        summary: { action: :release, released_from: existing["assignee"], forced: !!force,
                   delegation: released, previous: existing } }
    end

    def plan_work_ref(rec, work_ref:, worker:)
      existing = rec[Delegation::FIELD]
      return delegation_refusal(:invalid, "task is not delegated") unless Delegation.object?(existing)

      unless worker.nil?
        held_by = utf8(worker.to_s).strip
        unless Delegation.claimed?(existing) && existing["assignee"] == held_by
          return delegation_held(rec, :work_ref,
                                 "a work reference from a worker requires a matching claim")
        end
      end

      clear = work_ref.nil? || work_ref == :off
      reference = clear ? nil : utf8(work_ref.to_s).strip
      unless clear
        problems = Delegation.work_ref_errors("work_ref" => reference)
        return delegation_refusal(:invalid, problems.first) unless problems.empty?
      end

      candidate = Delegation.ordered(existing.merge("work_ref" => reference))
      summary = { action: :work_ref, work_ref: reference, delegation: candidate, previous: existing }
      return { status: :ok, no_change: true, summary: summary } if candidate == existing

      rec[Delegation::FIELD] = candidate
      label = clear ? "clear work ref: #{rec["title"]}" : "work ref → #{reference}: #{rec["title"]}"
      { status: :ok, label: label, summary: summary }
    end

    # Completing a recurring task rolls it forward instead of closing it, and
    # the occurrence that appears is NEW work: the claim and the work reference
    # belong to the cycle that just finished, so carrying them over would hand
    # the next occurrence to a worker who never picked it up — invisible to
    # `--agent-ready` (it looks claimed) and unpickupable by anyone else.
    # The standing intent survives: the agent mode, or the person the task is
    # delegated to, with a fresh transition stamp. Nothing that says the work
    # already started does.
    def roll_delegation_forward(rec)
      existing = rec[Delegation::FIELD]
      return unless Delegation.object?(existing)
      # A marker of no recognized kind cannot describe intent to carry over;
      # a fresh occurrence is the right place to drop it.
      return rec.delete(Delegation::FIELD) unless Delegation.agent?(existing) || Delegation.human?(existing)

      human = Delegation.human?(existing)
      rec[Delegation::FIELD] = Delegation.ordered(
        existing.merge("status" => human ? Delegation::DELEGATED : Delegation::READY,
                       "assignee" => human ? existing["assignee"] : nil,
                       "at" => delegation_now, "work_ref" => nil)
      )
    end

    # Closing a task that was merely QUEUED for an agent clears the marker —
    # nothing happened yet, so there is no provenance to keep. A live claim or a
    # human delegation is retained verbatim: who held the task and where the
    # work lives is exactly what should survive into the archive.
    def settle_delegation_on_close(rec)
      rec.delete(Delegation::FIELD) if Delegation.ready?(rec[Delegation::FIELD])
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
        id = @id_source ? @id_source.call : SecureRandom.hex(4)
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
    # stands). Returns the touched records' stable IDs, in file order.
    def close_open_descendants(records, ri, today: Date.today)
      rj = subtree_end(records, ri)
      closed_on = today.iso8601
      records[(ri + 1)...rj].each_with_object([]) do |rec, touched|
        next unless rec["type"] == "task" && OPEN_STATES.include?(rec["state"])
        rec["state"] = "DONE"
        rec["closed"] = closed_on
        rec["tags"] = (rec["tags"] || []) - [DEFER_TAG]
        rec.delete("recur")
        settle_delegation_on_close(rec)
        touched << rec["id"]
      end
    end

    def section_index(records, id)
      records.index { |record| record["type"] == "section" && record["id"] == id }
    end

    def create_section_impl(title, parent_id)
      records = fresh_records(@org)
      records = [meta_record] if records.empty?
      if parent_id.nil?
        insert_at = records.length
      else
        pi = section_index(records, parent_id) or return false
        insert_at = subtree_end(records, pi)
      end
      id = gen_id(ids_of(records) + archived_ids)
      rec = { "type" => "section", "id" => id, "title" => title }
      rec["parent"] = parent_id if parent_id
      records[insert_at, 0] = [rec]
      write_records(@org, records)
      reload!
      id
    end

    def rename_section_impl(id, title)
      records = fresh_records(@org)
      ri = section_index(records, id) or return false
      records[ri]["title"] = title
      write_records(@org, records)
      reload!
      id
    end

    def complete_project_impl(id, today)
      records = fresh_records(@org)
      ri = section_index(records, id) or return false
      closed = close_open_descendants(records, ri, today: today)
      return 0 if closed.empty?

      write_records(@org, records)
      reload!
      closed.length
    end

    # Splice the section's subtree out of the live file and append it to the
    # archive, archive-first so an interruption can only leave retry-safe
    # duplicates, never a lost subtree (the sweep's ordering and safety gates).
    def archive_project_impl(id)
      records = fresh_records(@org)
      ri = section_index(records, id) or return false
      rj = subtree_end(records, ri)
      moved = records[ri...rj].map(&:dup)
      if moved.any? { |record| record["type"] == "task" && PROPOSED_STATES.include?(record["state"]) }
        return :proposed_descendants
      end
      moved[0].delete("parent")
      moved[0]["archived"] = Date.today.iso8601
      kept = records[0...ri] + records[rj..]

      arch = File.exist?(@archive) ? fresh_records(@archive) : []
      arch = [meta_record] if arch.empty?
      retry_state, = archive_retry_state(arch, moved)
      return false if retry_state == :conflict

      if retry_state == :new
        arch.concat(moved)
        write_records(@archive, arch)
      end
      persisted_ids = ids_of(fresh_records(@archive)).to_set
      missing_ids = ids_of(moved).reject { |mid| persisted_ids.include?(mid) }
      raise "archive write omitted moved ids: #{missing_ids.join(", ")}" unless missing_ids.empty?

      write_records(@org, kept)
      reload!
      ids_of(moved)
    end

    # Index of the section matching `name`, resolving in widening tiers so a
    # captured task lands in the most specific match: exact top-level, then
    # exact any-level, then substring top-level, then substring any-level (all
    # case-insensitive, file order within a tier). The top-level tiers preserve
    # the historical resolution; the any-level tiers let capture reach a nested
    # project sub-section by name. Returns the record index, or nil.
    def find_section(records, name)
      want = name.strip.downcase
      all = records.each_index.select { |i| records[i]["type"] == "section" }
      top = all.select { |i| !records[i]["parent"] }
      exact = ->(pool) { pool.find { |i| records[i]["title"].to_s.downcase == want } }
      substr = ->(pool) { pool.find { |i| records[i]["title"].to_s.downcase.include?(want) } }
      exact.call(top) || exact.call(all) || substr.call(top) || substr.call(all)
    end

    # -- write plumbing --------------------------------------------------------

    # `stamp: false` writes the records with their `updated` values exactly as
    # they were read. The one caller is #repair!, and the reason is semantic: a
    # repair asserts nothing about the task's content, so stamping it would
    # (a) falsify "when this task last changed", (b) hand the repairing device an
    # undeserved win in the last-write-wins merge — which, for a dropped unknown
    # temporal key, means overwriting the newer binary that understood the field
    # with the copy that just discarded it — and (c) for a just-minted id, be
    # indistinguishable from a task created now, since stamp_changed_tasks!
    # indexes originals by id and a fresh id is in no index (td-d6ed92).
    def write_records(path, records, stamp: true)
      stamp_changed_tasks!(fresh_records(path), records) if stamp
      Atomic.write(path, Format.dump(records))
    end

    def stamp_changed_tasks!(original_records, proposed_records)
      original_by_id = original_records.each_with_object({}) do |record, by_id|
        by_id[record["id"]] = record if record["id"]
      end
      changed_ids = proposed_records.each_with_object(Set.new) do |record, ids|
        next false unless record["type"] == "task"

        original = original_by_id[record["id"]]
        ids << record["id"] if original.nil? || stamp_semantics(original) != stamp_semantics(record)
      end
      stamp = UpdateStamp.format(@now.call, @device) unless changed_ids.empty?

      proposed_records.each do |record|
        next unless record["type"] == "task"

        if changed_ids.include?(record["id"])
          record["updated"] = stamp
        elsif (original = original_by_id[record["id"]])
          original.key?("updated") ? record["updated"] = original["updated"] : record.delete("updated")
        end
      end
    end

    def stamp_semantics(record)
      Format.dump_record(record.reject { |key, _| key == "line" || key == "updated" })
    end

    def meta_record = { "type" => "meta", "version" => Format::VERSION }

    # Append `line` to an existing body string (or start one).
    def append_body(body, line)
      body.nil? || body.empty? ? line : "#{body}\n#{line}"
    end

    # Join reject notes the same way propose joins repeatable `--note` values.
    # Returns nil when any note fails UTF-8 recovery (invalid → typed error).
    def proposal_note_text(notes)
      return "" if notes.nil? || notes.empty?

      pieces = notes.map { |note| utf8(note) }
      return nil unless pieces.all?(&:valid_encoding?)

      pieces.join("\n")
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
      # `flock` is not reentrant across separately opened descriptors, but a
      # few locked operations legitimately call another locked read in the
      # same Ruby execution context (for example restore -> reload!). An
      # execution context is both the Thread and Fiber: a Store can be shared
      # by threads, and a yielded owner Fiber must not let another Fiber
      # bypass the sidecar flock. It cannot wait on that flock either: a Fiber
      # that blocks its thread's scheduler prevents the owner from resuming to
      # release it, so reject that contention explicitly.
      #
      # Known limit: this guard is per Store INSTANCE. Two Stores on the same
      # file in one thread (e.g. a locked mutation calling code that builds a
      # fresh Store via StoreFactory) would deadlock in flock with no
      # diagnostic — flock excludes across fds within one process. No such
      # nesting exists today; a fiber-scheduler server (Falcon/async) would
      # need a process-wide registry keyed on lock_path instead.
      owner = [Thread.current, Fiber.current]
      # Snapshot @lock_owner once per test: reading it twice (`@lock_owner &&
      # @lock_owner.first...`) leaves an interrupt checkpoint between the reads
      # where the releasing thread can nil it, turning a should-block contender
      # into a NoMethodError on nil.
      holder = @lock_owner
      return yield if holder == owner

      if holder && holder.first.equal?(Thread.current)
        raise CrossFiberLockError,
              "Store lock is held by another Fiber on this thread; resume the owner before locking"
      end

      File.open(lock_path, File::RDWR | File::CREAT, 0o644) do |f|
        f.flock(File::LOCK_EX)
        @lock_owner = owner
        begin
          yield
        ensure
          @lock_owner = nil
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
        return [:unsupported_schema] if unsupported_schema?

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
          # A step marked `repair` is the exception: it recorded a deliberate
          # targeted repair whose `before` was the malformed record the user asked
          # to fix, so undo must faithfully restore those invalid bytes rather
          # than refuse (the automatic ensure_id! repair is never so marked, so
          # its undo stays gated).
          if step[:target][:org] && !step[:repair] && !Check.check(@org).ok?
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
    # `repair: true` flags the journal step as one whose BEFORE-state is invalid
    # bytes the user deliberately asked to fix, so `undo` restores them instead
    # of refusing (see history_step). Default false: an ordinary mutation's undo
    # stays gated on the restored file validating.
    def with_history(label, coalesce_key: nil, repair: false)
      with_lock do
        clear_rollback
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
            record_rollback(reason, stage: :validation)
            restore(before)
            return result.is_a?(Integer) ? 0 : false
          end
          @journal.record(label: label, before: before, after: after,
                          coalesce_key: coalesce_key, repair: repair)
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

    # Move every fully closed DONE/CANCELLED task subtree to the archive file.
    # The archive is written first, then the live file: interruption can leave
    # retry-safe duplicates across the two files, but can never silently lose a
    # task. A retry converges only when every stable ID has one canonically
    # equal archived copy; partial or mismatched overlap refuses safely. Returns
    # the count of roots swept, or ArchiveRefusal when a safety gate blocks it.
    def archive_swept_impl(expected_preview)
      return ArchiveRefusal.new(reason: :unsupported_schema) if unsupported_schema?

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
      # not conflict solely because today's proposed archive/update stamps have
      # advanced. Moving into the archive is a write for every task in the
      # subtree, so the durable archive copy also owns each task's `updated`.
      expected["archived"] = actual["archived"] if expected["archived"] && actual["archived"]
      expected["updated"] = actual["updated"] if actual["updated"]
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
            record["type"] == "task" && !CLOSED_STATES.include?(record["state"])
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

    # -- store repair ----------------------------------------------------------

    # The keys a temporal object may carry. Deliberately the same set
    # Check.check_temporal_time subtracts and Format::NESTED_KEY_ORDER declares:
    # a repair that dropped a different set than Check refuses would either fail
    # to converge or destroy a field Check was happy with.
    TEMPORAL_KEYS = %w[local timezone fold].freeze
    private_constant :TEMPORAL_KEYS

    def repair_file_name(source) = source == :archive ? File.basename(@archive) : File.basename(@org)

    # Plan the repair of every file in the store. Ids are minted from ONE pool
    # spanning both files, so a repair can never invent an id that collides with
    # a swept task (the cross-file duplicate `check --all-files` refuses).
    def repair_plans
      taken = Set.new
      targets = [[@org, File.basename(@org)]]
      targets << [@archive, File.basename(@archive)] if File.exist?(@archive)
      parsed = targets.filter_map do |path, name|
        raw = begin
          File.read(path, encoding: "UTF-8")
        rescue Errno::ENOENT
          nil
        end
        next nil if raw.nil?

        unless raw.valid_encoding?
          next { path: path, file: name, records: [], fixes: [],
                 blockers: [RepairBlocker.new(file: name, line: 0, message: "file is not valid UTF-8")] }
        end
        result = Format.parse(raw)
        result.records.each { |record| taken << record["id"] if record["id"].is_a?(String) }
        { path: path, file: name, parsed: result }
      end

      parsed.map { |plan| plan.key?(:parsed) ? repair_plan(plan, taken) : plan }
    end

    # Apply the known repairs to one file's records, then re-Check the result in
    # memory. Whatever Check still reports is a blocker: a defect this command
    # does not know how to converge, reported rather than written over.
    def repair_plan(plan, taken)
      records = plan[:parsed].records
      fixes = apply_known_repairs!(records, plan[:file], taken)
      after = Check.check_parsed(Format::Result.new(records, plan[:parsed].errors))
      blockers = after.errors.map do |line, message|
        RepairBlocker.new(file: plan[:file], line: line, message: message)
      end
      { path: plan[:path], file: plan[:file], records: records, fixes: fixes, blockers: blockers }
    end

    # The two known members of the class. Both are repairs the codebase already
    # documents and neither can reach the file today:
    #
    #   * a record with no id — `ensure_id!` mints one, but only for the record
    #     it was asked about. An id-less record can never be a parent (Check
    #     resolves `parent` against ids it has already seen), so minting one
    #     cannot invalidate a reference. A MALFORMED id is deliberately left
    #     alone: children may point at it, and reminting would orphan them.
    #   * an unknown key inside `scheduled_time`/`deadline_time` — the drop
    #     `Format::NESTED_FORWARD_COMPAT` calls "the repair path, not data loss".
    #     Dropped explicitly rather than left to `Format.dump_record`, so the
    #     in-memory re-Check below sees the repaired state and the fix is
    #     enumerable in the report.
    def apply_known_repairs!(records, file, taken)
      fixes = []
      records.each do |record|
        next if record["type"] == "meta"
        line = record["line"]
        id = record["id"]
        if id.nil? || (id.is_a?(String) && id.empty?)
          minted = gen_id(taken)
          taken << minted
          record["id"] = minted
          fixes << RepairFix.new(file: file, line: line, kind: :minted_id,
                                 message: "record missing id", id: minted)
        end
        %w[scheduled_time deadline_time].each do |key|
          value = record[key]
          next unless value.is_a?(Hash)

          unknown = value.keys.map(&:to_s) - TEMPORAL_KEYS
          next if unknown.empty?

          unknown.each { |unknown_key| value.delete(unknown_key) }
          fixes << RepairFix.new(file: file, line: line, kind: :dropped_temporal_keys,
                                 message: "#{key} has unknown keys: #{unknown.join(", ")}")
        end
      end
      fixes
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
