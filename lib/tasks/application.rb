# frozen_string_literal: true

require_relative "store"
require_relative "create_task"
require_relative "operation_context"
require_relative "task_changeset"
require_relative "task_queries"

module Tasks
  # One coherent, immutable live read for adapters that need both the legacy
  # Items used for presentation and their canonical TaskViews. The latter is
  # the public data contract; Items and the tree are deliberately retained as
  # adapter-only presentation inputs while the TUI's outliner is migrated.
  #
  # This is not a Store wrapper. It is created from a single ReadSnapshot and
  # exposes no mutable Store or persistence operation.
  class TaskReadModel
    attr_reader :items, :tree, :tasks

    def initialize(snapshot)
      @snapshot = snapshot
      @queries = TaskQueries.new(snapshot)
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
    def view_tasks(name, today: Date.today, urgent_days: Quadrants::DEFAULT_URGENT_DAYS)
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
                   links: {}, link_systems: {}, max_depth: Tree::DEFAULT_MAX_DEPTH)
      @org = frozen_text(org)
      @archive = frozen_text(archive)
      @journal_dir = journal_dir && frozen_text(journal_dir)
      @undo_limit = Integer(undo_limit)
      @links = immutable_copy(links)
      @link_systems = immutable_copy(link_systems)
      @max_depth = Integer(max_depth)
      freeze
    end

    def call
      Store.new(org: org, archive: archive, journal_dir: journal_dir,
                undo_limit: undo_limit, links: links, link_systems: link_systems,
                max_depth: max_depth)
    end

    private

    attr_reader :org, :archive, :journal_dir, :undo_limit, :links, :link_systems, :max_depth

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
    def initialize(store_factory:)
      unless store_factory.respond_to?(:call)
        raise ArgumentError, "store_factory must respond to #call"
      end

      @store_factory = store_factory
      freeze
    end

    def list_tasks(filter)
      unless filter.is_a?(TaskFilter)
        raise ArgumentError, "filter must be a Tasks::TaskFilter"
      end

      queries(include_archive: filter.include_archive?).list(filter)
    end

    # The named selections are kept here so adapters do not each recreate
    # agenda/next/inbox/quadrant semantics. The return value retains the legacy
    # Items for presentation while exposing canonical immutable TaskViews.
    def view_tasks(name, today: Date.today, urgent_days: Quadrants::DEFAULT_URGENT_DAYS)
      queries.view(name, today: today, urgent_days: urgent_days)
    end

    # Stable IDs are the application boundary; fuzzy title and L<line>
    # resolution are CLI-only conveniences. A missing id is an ordinary nil
    # result so a later HTTP adapter can map it to its own not-found response.
    def get_task(id, include_archive: false)
      queries(include_archive: include_archive).find(id, include_archive: include_archive)
    end

    def list_sections
      queries.sections
    end

    # A single live read for presentation adapters. It deliberately receives a
    # new Store just like every other Application query, so the TUI cannot
    # retain Store's mutable read cache between paints or external writes.
    def read_tasks
      TaskReadModel.new(store_factory.call.read_snapshot)
    end

    # Typed creation seam. Hash attributes are accepted for adapter convenience
    # but immediately become an immutable CreateTask before Store takes the
    # lock, so CLI, TUI, and a future HTTP adapter share one create transaction.
    def create_task(command_or_attributes, context: nil)
      validate_operation_context(context)
      command = case command_or_attributes
                when CreateTask
                  command_or_attributes
                when Hash
                  CreateTask.new(**command_or_attributes.transform_keys(&:to_sym))
                else
                  raise ArgumentError, "create_task expects a Tasks::CreateTask or attributes mapping"
                end
      store_factory.call.create_task!(command)
    end

    # Typed command seam for transports that need an atomic multi-field update.
    # CLI/TUI mutation routing stays untouched in Phase 3a; this gives the
    # future HTTP adapter and command tests the same Store transaction directly.
    def update_task(id_or_changeset, changes = nil, expected_revision: nil, context: nil,
                    coalesce_key: nil, confirmation: nil, history_label: nil, force: false)
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
      store_factory.call.apply_changeset!(changeset)
    end

    private

    attr_reader :store_factory

    def queries(include_archive: false)
      TaskQueries.new(store_factory.call.read_snapshot(include_archive: include_archive))
    end

    def validate_operation_context(context)
      return if context.nil?
      return if defined?(OperationContext) && context.is_a?(OperationContext)

      raise ArgumentError, "context must be a Tasks::OperationContext"
    end
  end
end
