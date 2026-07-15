# frozen_string_literal: true

require "date"
require "digest"
require "securerandom"
require "set"
require_relative "atomic"
require_relative "check"
require_relative "create_task"
require_relative "delete_task"
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
    OPEN_STATES = %w[INBOX TODO NEXT WAITING].freeze
    DONE_STATES = %w[DONE CANCELLED].freeze
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
    def initialize(org:, archive:, journal_dir: nil, undo_limit: UNDO_LIMIT, coalesce_scope: nil,
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
          task_revisions: {
            live: task_revisions(live_records),
            archive: include_archive ? task_revisions(archive_records) : {},
          },
          link_shorthands: @link_shorthands, link_systems: @link_systems
        )
      end
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

    # Returns [:ok, label] | [:empty] | [:conflict, label]
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
        @last_rollback = nil
        before = snapshot
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
            @last_rollback = reason
            restore(before)
            return MutationResult.new(status: :store_invalid, errors: [reason])
          end

          after = snapshot
          @journal.record(label: "capture: #{attributes[:title]}", before: before, after: after)
          reload!
          ri = locate_stable_index(@records, planned[:id])
          MutationResult.new(
            status: :ok,
            snapshot: ri && build_edit_snapshot(@records, ri),
            read_snapshot: @read_snapshot,
            touched_ids: [planned[:id]],
            summary: { parent_id: planned[:parent_id], inserted_id: planned[:id] }
          )
        rescue StandardError => e
          @last_rollback = safe_patch_error(e)
          restore(before)
          MutationResult.new(status: :unavailable, errors: [safe_patch_error(e)])
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
        @last_rollback = nil
        before = snapshot
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
            @last_rollback = reason
            restore(before)
            return MutationResult.new(status: :store_invalid,
                                      snapshot: restored_edit_snapshot(command.id), errors: [reason])
          end
          after = snapshot
          @journal.record(label: label, before: before, after: after)
          reload!
          MutationResult.new(
            status: :ok,
            touched_ids: removed_task_ids,
            summary: {
              removed: removed_task_ids.length,
              descendants: descendant_tasks.length,
              open_descendants: descendant_tasks.count { |record| OPEN_STATES.include?(record["state"]) },
            }
          )
        rescue StandardError => e
          @last_rollback = safe_patch_error(e)
          restore(before)
          MutationResult.new(status: :unavailable, errors: [safe_patch_error(e)])
        end
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
    def apply_changeset!(changeset, today: Date.today)
      apply_task_changeset!(changeset, strict_revision: true, today: today)
    end

    # Apply one field-owned semantic change. TaskPatch remains the adapter
    # convenience for existing CLI/TUI save-on-blur paths; it delegates all
    # mutation work to the same changeset transaction below, retaining its
    # established narrow expected-value conflict check.
    def patch_task!(patch, today: Date.today)
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
        today: today
      )
    end

    # Shared transaction for TaskChangeset and TaskPatch. All field changes are
    # first applied to a detached records copy; an invalid later field therefore
    # cannot leak a partial in-memory mutation into a file write or journal step.
    def apply_task_changeset!(changeset, strict_revision:, today:, field_expectations: nil)
      unless changeset.is_a?(TaskChangeset)
        return MutationResult.new(status: :invalid, errors: ["expected a Tasks::TaskChangeset"])
      end

      with_lock do
        @last_rollback = nil
        before = snapshot
        current = nil
        repair = false
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
          applied = apply_changeset_fields(working_records, changeset, today: today)
          if applied[:status] != :ok
            return MutationResult.new(status: applied[:status], snapshot: current,
                                      errors: applied[:errors] || [], summary: applied[:summary])
          end
          proposed_records = Format.dump(working_records)
        rescue JSON::GeneratorError, EncodingError, ArgumentError => e
          return MutationResult.new(status: :invalid, snapshot: current,
                                    errors: [safe_patch_error(e)])
        end

        if proposed_records == original_records
          reload!
          return MutationResult.new(status: :no_change, snapshot: current,
                                    read_snapshot: @read_snapshot, summary: applied[:summary])
        end

        label = changeset.history_label || changeset_history_label(changeset, current)
        begin
          write_records(@org, working_records)
          if (reason = post_write_failure)
            @last_rollback = reason
            restore(before)
            return MutationResult.new(status: :store_invalid,
                                      snapshot: restored_edit_snapshot(changeset.id),
                                      errors: [reason])
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
            touched_ids: applied[:touched_ids],
            summary: applied[:summary]
          )
        rescue StandardError => e
          @last_rollback = safe_patch_error(e)
          restore(before)
          MutationResult.new(status: :unavailable,
                             snapshot: restored_edit_snapshot(changeset.id),
                             errors: [safe_patch_error(e)])
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

    # Items parsed from the archive file (source: :archive). Not cached — the
    # archive is read rarely (`list -x/-a`) and appended rarely.
    def archive_items
      current_read_snapshot(include_archive: true).archive_items
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

    def normalize_create_task(command, today:)
      errors = Hash.new { |fields, field| fields[field] = [] }
      title = normalize_create_text(command.title, :title, errors, required: true)
      priority = normalize_create_priority(command.priority, errors)
      tags = normalize_create_tags(command.tags, errors)
      scheduled = normalize_create_date(command.scheduled, :scheduled, errors)
      deadline = normalize_create_date(command.deadline, :deadline, errors)
      state = normalize_create_state(command.state, errors)
      project = normalize_create_project(command.project, errors)
      parent_id = normalize_create_parent_id(command.parent_id, errors)
      recurrence = normalize_create_recurrence(command.recurrence, errors)
      notes = normalize_create_notes(command, errors)

      if project && parent_id
        errors[:location] << "project and parent_id cannot both be supplied"
      end

      # Capturing with a recurrence has always meant "start repeating now" when
      # a date was omitted. Keep that behavior in the command, not the CLI, so
      # every transport gets one definition of a recurring create.
      scheduled ||= today if recurrence && !deadline
      state ||= (scheduled || deadline ? "TODO" : "INBOX")
      errors[:state] << "can't set recurrence on a #{state} task" if recurrence && DONE_STATES.include?(state)

      [
        {
          title: title, priority: priority, tags: tags, scheduled: scheduled,
          deadline: deadline, state: state, project: project, parent_id: parent_id,
          recurrence: recurrence, notes: notes,
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

    def normalize_create_date(value, field, errors)
      return nil if value.nil? || value == ""
      return value if value.is_a?(Date)
      return Date.iso8601(value) if value.is_a?(String)

      errors[field] << "#{field} must be a date or nil"
      nil
    rescue ArgumentError, Date::Error
      errors[field] << "#{field} must be a date or nil"
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
        return { status: :invalid, errors: ["parent_id must identify a task"] } unless records[pi]["type"] == "task"

        by_id = records.to_h { |record| [record["id"], record] }
        if task_depth(by_id, records[pi]) + 1 > @max_depth
          return { status: :too_deep,
                   errors: ["would exceed max depth #{@max_depth} (max_depth config / TASKS_MAX_DEPTH)"] }
        end
        parent_id = records[pi]["id"]
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
      rec["scheduled"] = attributes[:scheduled].iso8601 if attributes[:scheduled]
      rec["deadline"] = attributes[:deadline].iso8601 if attributes[:deadline]
      rec["recur"] = attributes[:recurrence] if attributes[:recurrence]
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

    # Whether a preflight failure is repairable by a patch that targets `id`:
    # true only when EVERY Check error lies on the single record that patch will
    # rewrite. Raw-safety comes first — an invalid-UTF-8 file, or any line that
    # isn't parseable JSON (Format.parse yields an error entry), keeps refusing
    # even when it is the targeted line, because Format.parse/Check can't reason
    # about bytes they would misparse. With the file parseable, locate the target
    # by stable id and require each error's line to equal the target's line; an
    # error anywhere else means the fix wouldn't leave the file fully clean, so
    # we refuse exactly as before and the CLI shows the "already invalid" hint.
    def repair_scope?(preflight, id)
      return false unless id.is_a?(String) && !id.empty?
      raw = File.read(@org, encoding: "UTF-8")
      return false unless raw.valid_encoding?
      parsed = Format.parse(raw)
      return false unless parsed.errors.empty?
      target = parsed.records.find { |record| record["type"] == "task" && record["id"] == id }
      return false unless target
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
        revision: task_revision(values, records, ri),
        metadata: {
          line: rec["line"],
          tag_sequence: tags,
          date_state: {
            scheduled: values[:scheduled], deadline: values[:deadline], recurrence: values[:recurrence],
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
        contexts: contexts,
        tags: ordinary_tags,
        body: rec["body"].is_a?(String) ? rec["body"] : "",
        location: rec["parent"],
        state: rec["state"],
      }
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
         record["scheduled"], record["deadline"], record["recur"],
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
      own = semantic_digest(EditSnapshot::FIELDS.map { |field| [field, revision_value(values[field])] })
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
      required << :location if fields.include?(:location)
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
      errors
    end

    def duplicate_records(records)
      JSON.parse(JSON.generate(records))
    end

    def apply_changeset_fields(records, changeset, today:)
      touched_ids = []
      summaries = {}
      changeset.ordered_fields.each do |field|
        ri = locate_stable_index(records, changeset.id)
        return { status: :not_found } unless ri

        applied = apply_semantic_patch(
          records, ri, field, changeset.changes.fetch(field), force: changeset.force, today: today
        )
        return applied unless applied[:status] == :ok

        touched_ids.concat(applied[:touched_ids] || [])
        summaries[field] = applied[:summary] if applied[:summary]
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

    def apply_semantic_patch(records, ri, field, value, force: false, today:)
      case field
      when :title      then patch_title(records, ri, value)
      when :priority   then patch_priority(records, ri, value)
      when :deferred   then patch_deferred(records, ri, value)
      when :activate   then patch_activate(records, ri, value, today: today)
      when :scheduled  then patch_date(records, ri, value, :scheduled)
      when :deadline   then patch_date(records, ri, value, :deadline)
      when :date_clear then patch_date_clear(records, ri, value)
      when :recurrence then patch_recurrence(records, ri, value)
      when :contexts   then patch_tag_slice(records, ri, value, :contexts)
      when :tags       then patch_tag_slice(records, ri, value, :tags)
      when :tag_delta  then patch_tag_delta(records, ri, value)
      when :body       then patch_body(records, ri, value)
      when :location   then patch_location(records, ri, value, force: force)
      when :state      then patch_state(records, ri, value, today: today)
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
    def patch_activate(records, ri, value, today:)
      return patch_invalid("activate must be true") unless value == true

      rec = records[ri]
      tags = semantic_tags(rec)
      tags.delete(DEFER_TAG)
      replace_optional(rec, "tags", tags)
      scheduled = to_date(rec["scheduled"])
      rec.delete("scheduled") if scheduled && scheduled > today
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

    # `undate` owns both date fields and their coupled recurrence cookie. Keep
    # that legacy CLI operation one checked write and one undo entry instead of
    # exposing an observable intermediate state between two single-date patches.
    def patch_date_clear(records, ri, value)
      kind = value.is_a?(String) || value.is_a?(Symbol) ? value.to_sym : value
      return patch_invalid("date clear kind must be deadline, scheduled, or nil") unless kind.nil? || %i[deadline scheduled].include?(kind)

      rec = records[ri]
      fields = kind ? [kind] : %i[scheduled deadline]
      return patch_invalid("no matching date stamp") unless fields.any? { |field| rec[field.to_s] }

      fields.each { |field| rec.delete(field.to_s) }
      rec.delete("recur") unless rec["scheduled"] || rec["deadline"]
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

    def patch_state(records, ri, value, today:)
      return patch_invalid("invalid task state") unless Check::STATES.include?(value)
      rec = records[ri]
      from = rec["state"]
      if value == "DONE" && Recur.cookie?(rec["recur"])
        result = advance_recurrence_records(records, ri, today: today)
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

    def advance_recurrence_records(records, ri, today:)
      rec = records[ri]
      cookie = rec["recur"]
      return patch_invalid("invalid recurrence cookie") unless Recur.cookie?(cookie)
      deadline = to_date(rec["deadline"])
      scheduled = to_date(rec["scheduled"])
      return patch_invalid("recurrence requires a valid date") unless deadline || scheduled

      if deadline
        next_deadline = Recur.next_date(cookie, from: deadline, today: today)
        if rec["scheduled"]
          return patch_invalid("recurrence requires a valid date") unless scheduled

          rec["scheduled"] = (scheduled + (next_deadline - deadline)).iso8601
        end
        rec["deadline"] = next_deadline.iso8601
      else
        rec["scheduled"] = Recur.next_date(cookie, from: scheduled, today: today).iso8601
      end
      rec["tags"] = semantic_tags(rec) - [DEFER_TAG]
      replace_optional(rec, "tags", rec["tags"])
      rec["body"] = append_body(rec["body"], "- Did [#{today}].")
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
        touched << rec["id"]
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
