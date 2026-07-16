# frozen_string_literal: true

require "securerandom"

require_relative "store"
require_relative "application_read_result"
require_relative "create_task"
require_relative "delete_task"
require_relative "operation_context"
require_relative "task_changeset"
require_relative "task_patch"
require_relative "task_queries"
require_relative "temporal_context"

module Tasks
  # One coherent, immutable live read for adapters that need both the legacy
  # Items used for presentation and their canonical TaskViews. The latter is
  # the public data contract; Items and the tree are deliberately retained as
  # adapter-only presentation inputs while the TUI's outliner is migrated.
  #
  # This is not a Store wrapper. It is created from a single ReadSnapshot and
  # exposes no mutable Store or persistence operation.
  class TaskReadModel
    attr_reader :items, :tree, :tasks, :temporal_context

    def initialize(snapshot, today: Date.today, temporal_context: nil)
      @snapshot = snapshot
      @queries = TaskQueries.new(snapshot, today: today, temporal_context: temporal_context)
      @temporal_context = @queries.temporal_context
      @items = snapshot.items
      @tree = snapshot.tree
      @tasks = items.map { |item| @queries.task(item) }.freeze
      @tasks_by_id = tasks.each_with_object({}) { |task, index| index[task.id] = task if task.id }.freeze
      freeze
    end

    # True when the live file no longer matches the snapshot this model was
    # built from. Long-lived presenters must gate refreshes on this rather
    # than on a mutation Store's #changed?: any read that lets that Store
    # self-reload consumes its mtime signal, and a model kept until the next
    # #changed? tick would then stay stale forever.
    def stale?(live_path)
      Store.stat_key(live_path) != @snapshot.live_stat
    end

    # Canonical resource for a presentation Item or stable id. The Item path
    # lets adapters keep their existing renderer without ever asking Store for
    # a body, links, or ancestry after the read has been captured.
    def task_for(item_or_id)
      return @tasks_by_id[item_or_id.to_s] if item_or_id.is_a?(String) || item_or_id.is_a?(Symbol)

      item_or_id && @queries.task(item_or_id)
    end

    def node_for(item) = @snapshot.node_for(item)

    # Named results remain canonical TaskQueryResults. Keeping this on the
    # model means a multi-read adapter can ask for an individual view without
    # parsing a second Store snapshot.
    def view_tasks(name, today: @queries.today, urgent_days: Quadrants::DEFAULT_URGENT_DAYS)
      @queries.view(name, today: today, urgent_days: urgent_days)
    end
  end

  # Builds a fresh Store for every application operation. Store maintains
  # convenient read caches for interactive clients, so keeping one instance in
  # a long-lived HTTP or CLI application object would let request-local reads
  # leak into later calls. The factory owns only immutable construction
  # settings; each #call returns a new mutable Store.
  class StoreFactory
    def initialize(org:, archive:, journal_dir: nil, undo_limit: Store::UNDO_LIMIT,
                   links: {}, link_systems: {}, max_depth: Tree::DEFAULT_MAX_DEPTH,
                   now: -> { Time.now.utc }, device: nil)
      @org = frozen_text(org)
      @archive = frozen_text(archive)
      @journal_dir = journal_dir && frozen_text(journal_dir)
      @undo_limit = Integer(undo_limit)
      @links = immutable_copy(links)
      @link_systems = immutable_copy(link_systems)
      @max_depth = Integer(max_depth)
      @now = now
      @device = UpdateStamp.slug(device || UpdateStamp.device).freeze
      # Every Store built by one factory represents one adapter/application
      # lifetime. Sharing this private scope preserves coalesced editor writes
      # while each operation still receives its own mutable Store instance.
      @coalesce_scope = SecureRandom.hex(16).freeze
      freeze
    end

    def call
      Store.new(org: org, archive: archive, journal_dir: journal_dir,
                undo_limit: undo_limit, links: links, link_systems: link_systems,
                max_depth: max_depth, coalesce_scope: coalesce_scope,
                now: now, device: device)
    end

    private

    attr_reader :org, :archive, :journal_dir, :undo_limit, :links, :link_systems, :max_depth,
                :coalesce_scope, :now, :device

    def frozen_text(value)
      value.to_s.dup.freeze
    end

    def immutable_copy(value)
      case value
      when Hash
        value.each_with_object({}) { |(key, child), copy| copy[immutable_copy(key)] = immutable_copy(child) }.freeze
      when Array
        value.map { |child| immutable_copy(child) }.freeze
      when String
        value.dup.freeze
      else
        value.freeze
      end
    end
  end

  # Persistence-neutral read facade shared by the CLI, TUI, and future HTTP
  # adapter. It accepts typed Ruby inputs and returns immutable query/view
  # objects. Adapter concerns such as ARGV, terminal rendering, Rack request
  # objects, and HTTP status mapping deliberately remain outside this class.
  class Application
    def initialize(store_factory:, temporal_context_factory: nil)
      unless store_factory.respond_to?(:call)
        raise ArgumentError, "store_factory must respond to #call"
      end

      @store_factory = store_factory
      @temporal_context_factory = temporal_context_factory
      freeze
    end

    def list_tasks(filter, today: Date.today, context: nil)
      validate_operation_context(context)
      unless filter.is_a?(TaskFilter)
        raise ArgumentError, "filter must be a Tasks::TaskFilter"
      end

      queries(include_archive: filter.include_archive?, today: today, operation_context: context).list(filter)
    end

    # The named selections are kept here so adapters do not each recreate
    # agenda/next/inbox/quadrant semantics. The return value retains the legacy
    # Items for presentation while exposing canonical immutable TaskViews.
    def view_tasks(name, today: Date.today, urgent_days: Quadrants::DEFAULT_URGENT_DAYS, context: nil)
      validate_operation_context(context)
      queries(today: today, operation_context: context).view(name, urgent_days: urgent_days)
    end

    # Stable IDs are the application boundary; fuzzy title and L<line>
    # resolution are CLI-only conveniences. A missing id is an ordinary nil
    # result so a later HTTP adapter can map it to its own not-found response.
    def get_task(id, include_archive: false, today: Date.today, context: nil)
      validate_operation_context(context)
      queries(include_archive: include_archive, today: today, operation_context: context)
        .find(id, include_archive: include_archive)
    end

    def list_sections
      queries.sections
    end

    # Projects and areas rolled up over their open tasks. Kept here so the CLI
    # and HTTP adapters share one definition of what a project is and how it is
    # ordered. Returns an array of ProjectView.
    def list_projects(today: Date.today, context: nil)
      validate_operation_context(context)
      queries(today: today, operation_context: context).projects
    end

    # A single ProjectView for a project or area section id, or nil so a later
    # HTTP adapter maps the absence to its own not-found response.
    def get_project(id, today: Date.today, context: nil)
      validate_operation_context(context)
      queries(today: today, operation_context: context).project_view(id)
    end

    # API-grade reads return canonical data plus the global live+archive
    # revision produced by the exact checked snapshot behind that data. The
    # existing direct query methods stay compatible for CLI/TUI callers.
    def list_tasks_result(filter, today: Date.today)
      unless filter.is_a?(TaskFilter)
        raise ArgumentError, "filter must be a Tasks::TaskFilter"
      end

      checked_query(today: today) { |query| query.list(filter) }
    end

    def get_task_result(id, source: :live, today: Date.today)
      checked_query(today: today) do |query|
        query.find(id, source: source)
      end
    end

    def list_sections_result(today: Date.today)
      checked_query(today: today) { |query| query.sections }
    end

    # API-grade project reads: the ProjectView list, and a single ProjectView
    # mapped to not_found when the id is not a project or area. Both carry the
    # checked snapshot's global revision.
    def list_projects_result(today: Date.today, context: nil)
      validate_operation_context(context)
      checked_query(today: today, operation_context: context) { |query| query.projects }
    end

    def project_result(id, today: Date.today, context: nil)
      validate_operation_context(context)
      checked_query(today: today, operation_context: context) { |query| query.project_view(id) }
    end

    # Safe foundation for /meta and readiness. Transport/config capabilities
    # can be added by the adapter; store health and its change token stay here.
    def read_status_result(today: Date.today)
      checked_query(today: today) { {}.freeze }
    end

    # A single live read for presentation adapters. It deliberately receives a
    # new Store just like every other Application query, so the TUI cannot
    # retain Store's mutable read cache between paints or external writes.
    def read_tasks(today: Date.today, context: nil)
      validate_operation_context(context)
      TaskReadModel.new(store_factory.call.read_snapshot, today: today,
                        temporal_context: context_for(today: today, operation_context: context))
    end

    # Field-scoped snapshot for adapters that preserve save-on-blur or
    # single-field CLI conflict behavior. It is an immutable typed value, not
    # an escape hatch to a Store instance.
    def edit_snapshot(id)
      store_factory.call.edit_snapshot(id)
    end

    # Schema deployment is an operator action, but TUI and CLI entry points use
    # the same checked Store migration rather than editing either JSONL file.
    def migrate_schema(dry_run: false)
      store_factory.call.migrate_schema!(dry_run: dry_run)
    end

    # Typed creation seam. Hash attributes are accepted for adapter convenience
    # but immediately become an immutable CreateTask before Store takes the
    # lock, so CLI, TUI, and a future HTTP adapter share one create transaction.
    def create_task(command_or_attributes, context: nil, today: Date.today)
      validate_operation_context(context)
      command = case command_or_attributes
                when CreateTask
                  command_or_attributes
                when Hash
                  CreateTask.new(**command_or_attributes.transform_keys(&:to_sym))
                else
                  raise ArgumentError, "create_task expects a Tasks::CreateTask or attributes mapping"
                end
      store_factory.call.create_task!(command, today: operation_today(today, context))
    end

    # Typed command seam for transports that need an atomic multi-field update.
    # CLI/TUI mutation routing stays untouched in Phase 3a; this gives the
    # future HTTP adapter and command tests the same Store transaction directly.
    def update_task(id_or_changeset, changes = nil, expected_revision: nil, context: nil,
                    coalesce_key: nil, confirmation: nil, history_label: nil, force: false,
                    today: Date.today)
      validate_operation_context(context)
      changeset = if id_or_changeset.is_a?(TaskChangeset)
                    raise ArgumentError, "changes are not accepted with a TaskChangeset" unless changes.nil?
                    raise ArgumentError, "expected_revision is not accepted with a TaskChangeset" unless expected_revision.nil?

                    id_or_changeset
                  else
                    TaskChangeset.new(
                      id: id_or_changeset, changes: changes, expected_revision: expected_revision,
                      coalesce_key: coalesce_key, confirmation: confirmation,
                      history_label: history_label, force: force
                    )
                  end
      temporal = context_for(today: today, operation_context: context)
      store_factory.call.apply_changeset!(changeset, today: temporal.local_date,
                                          temporal_context: temporal)
    end

    # A single-field command keeps TaskPatch's field-owned expectation while
    # sharing the Store transaction used by TaskChangeset. This lets existing
    # adapters migrate behind the application boundary without turning an
    # unrelated concurrent edit into a whole-task conflict.
    def patch_task(patch, context: nil, today: Date.today)
      validate_operation_context(context)
      unless patch.is_a?(TaskPatch)
        raise ArgumentError, "patch_task expects a Tasks::TaskPatch"
      end

      temporal = context_for(today: today, operation_context: context)
      store_factory.call.patch_task!(patch, today: temporal.local_date,
                                     temporal_context: temporal)
    end

    # Typed deletion seam. An undoable hard delete of one live task; a task with
    # descendants is refused unless cascade is true. expected_revision is
    # optional — nil skips the concurrency check (CLI convenience), a supplied
    # value guards the whole subtree. Accepts a prebuilt DeleteTask or an id.
    def delete_task(id_or_command, cascade: false, expected_revision: nil,
                    context: nil, history_label: nil)
      validate_operation_context(context)
      command = if id_or_command.is_a?(DeleteTask)
                  unless cascade == false && expected_revision.nil? && history_label.nil?
                    raise ArgumentError, "options are not accepted with a DeleteTask"
                  end

                  id_or_command
                else
                  DeleteTask.new(id: id_or_command, cascade: cascade,
                                 expected_revision: expected_revision, history_label: history_label)
                end
      store_factory.call.delete_task!(command)
    end

    # Project mutations mapped to the shared MutationResult vocabulary so the CLI
    # and HTTP adapters render one outcome set. Rename validates a non-blank
    # title (:invalid) and reports a missing section as :not_found. Complete
    # returns the closed count in the summary (0 is a clean :ok). Archive returns
    # the moved stable ids.
    # Create a new empty project: a section under the top-level "Projects" root.
    # When no root exists yet (an empty or rootless store) it is created first —
    # top-level, appended at end — then the project beneath it, so an agent is
    # never stranded. Rejects a blank title (:invalid) and a title that
    # duplicates an existing project or area (:invalid — the project-ref candidate
    # set, so a duplicate would make later refs ambiguous). touched_ids is
    # [new_id], or [new_id, root_id] when the root was auto-created.
    def create_project(title:, today: Date.today)
      title = title.to_s.strip
      if title.empty?
        return MutationResult.new(status: :invalid, errors: ["title cannot be blank"],
                                  field_errors: { title: ["cannot be blank"] })
      end
      store = store_factory.call
      return migration_required_mutation if store.checked_read_snapshot.migration_required?
      if list_projects(today: today).any? { |view| view.title.to_s.strip.casecmp?(title) }
        message = "a project or area named #{title.inspect} already exists"
        return MutationResult.new(status: :invalid, errors: [message],
                                  field_errors: { title: [message] })
      end

      root_id = project_root_id(store)
      created_root = root_id.nil?
      if created_root
        root_id = store.create_section!(title: "Projects")
        return create_section_failure(store) unless root_id
      end
      new_id = store.create_section!(title: title, parent_id: root_id)
      return create_section_failure(store) unless new_id

      MutationResult.new(
        status: :ok, touched_ids: created_root ? [new_id, root_id] : [new_id],
        summary: { created_id: new_id, root_id: root_id, created_root: created_root }
      )
    end

    def rename_project(id, title:)
      title = title.to_s.strip
      if title.empty?
        return MutationResult.new(status: :invalid, errors: ["title cannot be blank"])
      end

      store = store_factory.call
      return migration_required_mutation if store.checked_read_snapshot.migration_required?
      touched = store.rename_section!(id: id, to: title)
      touched ? MutationResult.new(status: :ok, touched_ids: [touched]) : MutationResult.new(status: :not_found)
    end

    def complete_project(id, today: Date.today)
      store = store_factory.call
      return migration_required_mutation if store.checked_read_snapshot.migration_required?
      closed = store.complete_project!(id: id, today: today)
      return MutationResult.new(status: :not_found) unless closed

      # complete_project! returns 0 both for a genuine no-op (already fully
      # closed) and for a post-write validation rollback. Only the latter sets
      # last_rollback, so a rolled-back write maps to the same :store_invalid
      # failure other mutations produce rather than masquerading as clean.
      if closed == 0 && store.last_rollback
        return MutationResult.new(status: :store_invalid, errors: [store.last_rollback])
      end

      MutationResult.new(status: :ok, summary: { closed: closed })
    end

    def archive_project(id)
      store = store_factory.call
      return migration_required_mutation if store.checked_read_snapshot.migration_required?
      moved = store.archive_project!(id: id)
      return MutationResult.new(status: :not_found) unless moved

      MutationResult.new(status: :ok, touched_ids: moved, summary: { archived: moved.length })
    end

    # Rebuild a canonical task resource from the immutable post-mutation
    # snapshot carried by MutationResult. This keeps an HTTP response and its
    # global revision tied to the exact same write instead of racing a second
    # Store read after the lock is released.
    def task_result_from_mutation(result, id, today: Date.today, temporal_context: nil)
      unless result.is_a?(MutationResult)
        raise ArgumentError, "result must be a Tasks::MutationResult"
      end
      unless result.ok? && result.read_snapshot && result.store_revision
        raise ArgumentError, "mutation result has no coherent task snapshot"
      end

      task = TaskQueries.new(result.read_snapshot, today: today,
                             temporal_context: temporal_context || context_for(today: today)).find(id, source: :live)
      ApplicationReadResult.new(
        status: task ? :ok : :not_found,
        data: task,
        store_revision: result.store_revision
      )
    end

    private

    attr_reader :store_factory, :temporal_context_factory

    # The id of the top-level "Projects" root section (case-insensitive, same
    # rule TaskQueries#projects uses), read from the given store's live snapshot,
    # or nil when no such root exists yet.
    def project_root_id(store)
      store.read_snapshot.live_records.find do |record|
        record["type"] == "section" && !record["parent"] &&
          record["title"].to_s.strip.downcase == "projects"
      end&.fetch("id")
    end

    # A create_section! that returned false after a non-blank title and a valid
    # parent can only be a post-write validation rollback, mapped like the other
    # project mutations (see complete_project).
    def create_section_failure(store)
      MutationResult.new(status: :store_invalid,
                         errors: [store.last_rollback || "section creation failed"])
    end

    def queries(include_archive: false, today: Date.today, operation_context: nil)
      TaskQueries.new(store_factory.call.read_snapshot(include_archive: include_archive), today: today,
                      temporal_context: context_for(today: today, operation_context: operation_context))
    end

    def checked_query(today:, operation_context: nil)
      checked = store_factory.call.checked_read_snapshot
      unless checked.ok?
        return ApplicationReadResult.new(
          status: checked.status, store_revision: checked.store_revision,
          errors: checked.errors, warnings: checked.warnings
        )
      end

      data = yield TaskQueries.new(checked.snapshot, today: today,
                                   temporal_context: context_for(today: today, operation_context: operation_context))
      ApplicationReadResult.new(
        status: data.nil? ? :not_found : :ok,
        data: data,
        store_revision: checked.store_revision,
        warnings: checked.warnings
      )
    end

    def validate_operation_context(context)
      return if context.nil?
      return if defined?(OperationContext) && context.is_a?(OperationContext)

      raise ArgumentError, "context must be a Tasks::OperationContext"
    end

    def context_for(today:, operation_context: nil)
      return operation_context.temporal_context if operation_context&.respond_to?(:temporal_context) && operation_context.temporal_context
      return temporal_context_factory.call if temporal_context_factory
      TemporalContext.new(now: Time.utc(today.year, today.month, today.day, 12), timezone: "Etc/UTC")
    end

    def migration_required_mutation
      MutationResult.new(status: :migration_required, errors: ["run `tasks migrate`"])
    end

    def operation_today(fallback, operation_context)
      context_for(today: fallback, operation_context: operation_context).local_date
    end
  end
end
