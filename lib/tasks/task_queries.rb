# frozen_string_literal: true

require "date"
require_relative "quadrants"
require_relative "recur"
require_relative "store"
require_relative "task_view"

module Tasks
  # Typed selection inputs shared by CLI, TUI, and the forthcoming application
  # facade. `parse_cli` deliberately contains only the legacy list syntax;
  # adapters decide how to report an ArgumentError to their own users.
  class TaskFilter
    SCOPES = %i[open done archived all].freeze
    STATE_ORDER = %w[INBOX TODO NEXT WAITING DONE CANCELLED].freeze
    Parsed = Struct.new(:filter, :json, keyword_init: true) do
      def initialize(**)
        super
        freeze
      end
    end

    attr_reader :scope, :deferred_only, :recurring_only, :body_search, :contexts,
                :tags, :priority, :text

    def initialize(scope: :open, deferred_only: false, recurring_only: false,
                   body_search: false, contexts: [], tags: [], priority: nil, text: [])
      @scope = scope.to_s.to_sym
      raise ArgumentError, "unknown task scope: #{scope}" unless SCOPES.include?(@scope)

      @deferred_only = !!deferred_only
      @recurring_only = !!recurring_only
      @body_search = !!body_search
      @contexts = frozen_strings(contexts)
      @tags = frozen_strings(tags)
      @priority = priority&.to_s&.upcase
      raise ArgumentError, "priority must be A, B, C, or none" if @priority && !%w[A B C].include?(@priority)

      @priority&.freeze
      @text = frozen_strings(text)
      freeze
    end

    def self.parse_cli(args)
      scope = :open
      json = false
      deferred_only = recurring_only = body_search = false
      contexts = []
      tags = []
      priority = nil
      text = []

      args.each do |arg|
        case arg
        when "--open", "-o" then scope = :open
        when "--done", "-d" then scope = :done
        when "--archived", "-x" then scope = :archived
        when "--all", "-a" then scope = :all
        when "--deferred", "-D" then deferred_only = true
        when "--recurring", "-R" then recurring_only = true
        when "--body", "-b" then body_search = true
        when "--json" then json = true
        when /\A-([ABC])\z/ then priority = Regexp.last_match(1)
        when /\A@/ then contexts << arg
        when /\A\+(.+)/ then tags << Regexp.last_match(1)
        when /\A\// then text << arg[1..]
        when /\A-/ then raise ArgumentError, "unknown flag: #{arg}"
        else text << arg
        end
      end

      Parsed.new(
        filter: new(scope: scope, deferred_only: deferred_only, recurring_only: recurring_only,
                    body_search: body_search, contexts: contexts, tags: tags,
                    priority: priority, text: text),
        json: json
      )
    end

    def include_archive? = %i[archived all].include?(scope)

    def states
      case scope
      when :open then Store::OPEN_STATES
      when :done then Store::DONE_STATES
      else STATE_ORDER
      end
    end

    def text_query = text.join(" ").downcase

    private

    def frozen_strings(values)
      Array(values).map { |value| value.to_s.dup.freeze }.freeze
    end
  end

  # A query keeps canonical resources beside the immutable source Item used by
  # legacy adapters. The item never appears in TaskView#to_h, keeping physical
  # lines out of reusable resources while preserving exact CLI presentation.
  class TaskQueryResult
    Entry = Struct.new(:item, :task, :metadata, keyword_init: true) do
      def initialize(**)
        super
        self.metadata = (metadata || {}).freeze
        freeze
      end
    end

    attr_reader :name, :filter, :entries

    def initialize(name:, entries:, filter: nil)
      @name = name.to_sym
      @filter = filter
      @entries = entries.freeze
      freeze
    end

    def tasks = entries.map(&:task).freeze
    def items = entries.map(&:item).freeze

    def task_for(item)
      entries.find { |entry| same_item?(entry.item, item) }&.task
    end

    def metadata_for(item)
      entries.find { |entry| same_item?(entry.item, item) }&.metadata || {}
    end

    private

    def same_item?(left, right)
      left.equal?(right) || (left.id && right.id && left.id == right.id && left.source == right.source) ||
        (left.line == right.line && left.source == right.source && left.title == right.title)
    end
  end

  # Builds stable, immutable read representations from one Store::ReadSnapshot.
  # There is intentionally no Store factory or command API here: that belongs
  # to Phase 2b's Tasks::Application facade.
  class TaskQueries
    NAMED_VIEWS = %i[agenda next quadrants inbox].freeze

    attr_reader :snapshot

    def initialize(snapshot)
      @snapshot = snapshot
      @records_by_source_and_id = records_by_source_and_id
      @records_by_source_and_line = records_by_source_and_line
      @task_views = {}
    end

    def list(filter)
      items = source_items(filter).select { |item| filter_match?(item, filter) }
      result(:list, items, filter: filter)
    end

    def view(name, today: Date.today, urgent_days: Quadrants::DEFAULT_URGENT_DAYS)
      name = name.to_sym
      raise ArgumentError, "unknown task view: #{name}" unless NAMED_VIEWS.include?(name)

      items = snapshot.items.select do |item|
        case name
        when :agenda then item.open? && !item.deferred? && (item.deadline || item.scheduled)
        when :next then item.state == "NEXT" && !item.deferred?
        when :quadrants then item.open? && !item.deferred?
        when :inbox then item.state == "INBOX" && !item.deferred?
        end
      end
      items = sort_named(items, name)
      result(name, items) do |item|
        name == :quadrants ? { quadrant: Quadrants.of(item, today: today, urgent_days: urgent_days) } : {}
      end
    end

    def task(item)
      task_view(current_item_for(item) || item)
    end

    def find(id, include_archive: false)
      id = id.to_s
      items = include_archive ? snapshot.items + snapshot.archive_items : snapshot.items
      item = items.find { |candidate| candidate.id == id }
      item && task_view(item)
    end

    def sections
      snapshot.live_records.select { |record| record["type"] == "section" }.map do |record|
        section_view(record)
      end.freeze
    end

    private

    def result(name, items, filter: nil)
      entries = items.map do |item|
        metadata = block_given? ? yield(item) : {}
        TaskQueryResult::Entry.new(item: item, task: task_view(item), metadata: metadata)
      end
      TaskQueryResult.new(name: name, entries: entries, filter: filter)
    end

    def source_items(filter)
      case filter.scope
      when :archived then snapshot.archive_items
      when :all then snapshot.items + snapshot.archive_items
      else snapshot.items
      end
    end

    def filter_match?(item, filter)
      filter.states.include?(item.state) &&
        (filter.scope != :done || item.source == :live) &&
        deferred_match?(item, filter) &&
        (!filter.recurring_only || item.recurring?) &&
        (filter.priority.nil? || item.priority == filter.priority) &&
        filter.contexts.all? { |context| item.tags.include?(context) } &&
        filter.tags.all? { |tag| item.tags.include?(tag) } &&
        text_match?(item, filter)
    end

    def deferred_match?(item, filter)
      return item.deferred? if filter.deferred_only
      return !item.deferred? if filter.scope == :open

      true
    end

    def text_match?(item, filter)
      query = filter.text_query
      return true if query.empty?
      return true if item.title.to_s.downcase.include?(query)

      filter.body_search && snapshot.body(item).join.downcase.include?(query)
    end

    def sort_named(items, name)
      case name
      when :agenda then items.sort_by { |item| [item.deadline || item.scheduled, item.priority || "Z"] }
      when :next then items.sort_by { |item| item.priority || "Z" }
      else items
      end
    end

    def task_view(item)
      key = [item.source, item.id || item.line, item.title]
      @task_views[key] ||= begin
        record = record_for(item)
        node = snapshot.node_for(item)
        section = section_for(node)
        TaskView.new(
          id: item.id, state: item.state, priority: item.priority, title: item.title,
          tags: item.tags, scheduled: item.scheduled, deadline: item.deadline,
          recur: item.recur, closed: item.closed, source: item.source,
          body: snapshot.body(item), links: snapshot.links(item), headline: headline_for(item),
          parent_id: record && record["parent"], ancestor_ids: ancestor_ids(node),
          child_ids: child_ids(node), section_id: section && section["id"],
          section_title: section && section["title"], project: node&.open_project&.title
        )
      end
    end

    def current_item_for(item)
      items = item.source == :archive ? snapshot.archive_items : snapshot.items
      return items.find { |candidate| candidate.id == item.id } if item.id

      items.find { |candidate| candidate.line == item.line && candidate.title == item.title }
    end

    def section_view(record)
      node = snapshot.tree.flat_map { |root| root.each.to_a }.find { |candidate| candidate.line == record["line"] }
      children = node ? node.children : []
      SectionView.new(
        id: record["id"], title: record["title"], parent_id: record["parent"],
        child_section_ids: children.filter(&:section?).filter_map { |child| record_at_line(:live, child.line)&.fetch("id", nil) },
        task_ids: children.filter(&:task?).filter_map { |child| child.item&.id }
      )
    end

    def headline_for(item)
      text = +"#{item.state} "
      text << "[##{item.priority}] " if item.priority
      text << item.title.to_s
      text << " :#{item.tags.join(":")}:" unless item.tags.empty?
      text
    end

    def ancestor_ids(node)
      ancestors = []
      current = node&.parent
      while current
        id = record_at_line(:live, current.line)&.fetch("id", nil)
        ancestors << id if id
        current = current.parent
      end
      ancestors.reverse
    end

    def child_ids(node)
      return [] unless node

      node.children.filter(&:task?).filter_map { |child| child.item&.id }
    end

    def section_for(node)
      current = node
      current = current.parent while current && current.task?
      current && record_at_line(:live, current.line)
    end

    def record_for(item)
      records = @records_by_source_and_id[item.source]
      return records[item.id] if item.id && records.key?(item.id)

      record_at_line(item.source, item.line)
    end

    def record_at_line(source, line)
      @records_by_source_and_line[source][line]
    end

    def records_by_source_and_id
      { live: index_records(snapshot.live_records), archive: index_records(snapshot.archive_records) }
    end

    def records_by_source_and_line
      {
        live: snapshot.live_records.to_h { |record| [record["line"], record] },
        archive: snapshot.archive_records.to_h { |record| [record["line"], record] },
      }
    end

    def index_records(records)
      records.each_with_object({}) do |record, index|
        id = record["id"]
        index[id] = record if id
      end
    end
  end
end
