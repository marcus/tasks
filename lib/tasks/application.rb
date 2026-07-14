# frozen_string_literal: true

require_relative "store"
require_relative "task_queries"

module Tasks
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

    private

    attr_reader :store_factory

    def queries(include_archive: false)
      TaskQueries.new(store_factory.call.read_snapshot(include_archive: include_archive))
    end
  end
end
