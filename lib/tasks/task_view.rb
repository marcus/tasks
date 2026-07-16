# frozen_string_literal: true

require_relative "links"
require_relative "recur"
require_relative "store"

module Tasks
  # Persistence-neutral task resource. It intentionally omits physical JSONL
  # coordinates: adapters can keep line numbers as presentation details, while
  # reusable callers operate on stable ids and semantic fields only.
  class TaskView
    attr_reader :id, :state, :priority, :title, :tags, :contexts, :scheduled,
                :deadline, :recur, :closed, :source, :body, :links, :headline,
                :parent_id, :ancestor_ids, :child_ids, :section_id,
                :section_title, :project, :revision, :availability_reason,
                :availability_blocker_id, :descendant_count

    def initialize(id:, state:, priority:, title:, tags:, scheduled:, deadline:,
                   recur:, closed:, source:, body:, links:, headline:, parent_id:,
                   ancestor_ids:, child_ids:, section_id:, section_title:, project:,
                   availability:, revision: nil, descendant_count: 0)
      @id = frozen_text(id)
      @state = frozen_text(state)
      @priority = frozen_text(priority)
      @title = frozen_text(title)
      @tags = frozen_array(tags)
      @contexts = @tags.select { |tag| tag.start_with?("@") }.freeze
      @scheduled = scheduled&.freeze
      @deadline = deadline&.freeze
      @recur = frozen_text(recur)
      @closed = closed&.freeze
      @source = source&.to_sym
      @body = frozen_array(body)
      @links = frozen_links(links)
      @headline = frozen_text(headline)
      @parent_id = frozen_text(parent_id)
      @ancestor_ids = frozen_array(ancestor_ids)
      @child_ids = frozen_array(child_ids)
      @section_id = frozen_text(section_id)
      @section_title = frozen_text(section_title)
      @project = frozen_text(project)
      @revision = frozen_text(revision)
      @available = availability.available?
      @availability_reason = availability.reason
      @availability_blocker_id = frozen_text(availability.blocker_id)
      @descendant_count = Integer(descendant_count)
      freeze
    end

    def open? = Store::OPEN_STATES.include?(state)
    def deferred? = tags.include?(Store::DEFER_TAG)
    def recurring? = Recur.cookie?(recur)
    def available? = @available

    # The canonical JSON-ready representation. Dates are strings because this
    # object is also the future HTTP resource; the Ruby accessors retain Dates.
    def to_h
      {
        id: id, state: state, priority: priority, title: title, tags: tags,
        contexts: contexts, deferred: deferred?, scheduled: scheduled&.iso8601,
        deadline: deadline&.iso8601, available: available?,
        availability_reason: availability_reason.to_s,
        availability_blocker_id: availability_blocker_id,
        recur: recur, closed: closed&.iso8601, source: source,
        body: body, links: links.map(&:to_h), parent_id: parent_id,
        ancestor_ids: ancestor_ids, child_ids: child_ids, section_id: section_id,
        section_title: section_title, project: project, headline: headline,
        revision: revision,
      }
    end

    private

    def frozen_text(value)
      value.nil? ? nil : value.to_s.dup.freeze
    end

    def frozen_array(values)
      Array(values).map { |value| frozen_text(value) }.freeze
    end

    def frozen_links(values)
      Array(values).map do |link|
        Links::Link.new(
          url: frozen_text(link.url), label: frozen_text(link.label), system: frozen_text(link.system)
        ).freeze
      end.freeze
    end
  end

  # Canonical project/area resource: a section rolled up over its open,
  # non-deferred descendant tasks at any depth. `kind` distinguishes a project
  # (a section under the top-level "Projects" heading) from an area (any other
  # top-level list that currently holds open work). `stuck` flags a project or
  # area with no open NEXT action — including one with zero open tasks. `line`
  # is the physical coordinate for adapters; like TaskView it never appears in
  # #to_h, keeping reusable resources free of file positions. `next_date` is the
  # soonest deadline-or-scheduled date across the rolled-up open tasks, the key
  # the listing sorts on (nil sorts last). `held_count` counts open descendant
  # tasks excluded from the rollup because they are deferred/held (own or
  # inherited hold); the archive refusal treats them as open work too.
  class ProjectView
    KINDS = %w[project area].freeze

    attr_reader :id, :title, :parent_id, :kind, :line, :open_count, :next_count,
                :next_date, :stuck, :body, :task_ids, :held_count

    def initialize(id:, title:, parent_id:, kind:, line:, open_count:, next_count:,
                   next_date:, stuck:, body:, task_ids:, held_count: 0)
      @id = frozen_text(id)
      @title = frozen_text(title)
      @parent_id = frozen_text(parent_id)
      @kind = frozen_text(kind)
      raise ArgumentError, "unknown project kind: #{kind}" unless KINDS.include?(@kind)

      @line = line
      @open_count = Integer(open_count)
      @next_count = Integer(next_count)
      @next_date = next_date&.freeze
      @stuck = !!stuck
      @body = frozen_text(body)
      @task_ids = frozen_array(task_ids)
      @held_count = Integer(held_count)
      freeze
    end

    # Dates render as ISO strings because this is also the future HTTP resource;
    # nil-valued fields are omitted so the shape stays as lean as the on-disk
    # record. `held_count` is always an integer, so it is always present.
    # Physical `line` is intentionally absent.
    def to_h
      {
        id: id, title: title, parent_id: parent_id, kind: kind,
        open_count: open_count, next_count: next_count,
        next_date: next_date&.iso8601, stuck: stuck, held_count: held_count,
        body: body, task_ids: task_ids,
      }.reject { |_, value| value.nil? }
    end

    private

    def frozen_text(value)
      value.nil? ? nil : value.to_s.dup.freeze
    end

    def frozen_array(values)
      Array(values).map { |value| frozen_text(value) }.freeze
    end
  end

  # Canonical section resource for clients that need project headings without
  # inspecting raw records or rebuilding the task tree themselves.
  class SectionView
    attr_reader :id, :title, :parent_id, :child_section_ids, :task_ids

    def initialize(id:, title:, parent_id:, child_section_ids:, task_ids:)
      @id = frozen_text(id)
      @title = frozen_text(title)
      @parent_id = frozen_text(parent_id)
      @child_section_ids = frozen_array(child_section_ids)
      @task_ids = frozen_array(task_ids)
      freeze
    end

    def to_h
      {
        id: id, title: title, parent_id: parent_id,
        child_section_ids: child_section_ids, task_ids: task_ids,
      }
    end

    private

    def frozen_text(value)
      value.nil? ? nil : value.to_s.dup.freeze
    end

    def frozen_array(values)
      Array(values).map { |value| frozen_text(value) }.freeze
    end
  end
end
