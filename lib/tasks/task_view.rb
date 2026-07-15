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
                :availability_blocker_id

    def initialize(id:, state:, priority:, title:, tags:, scheduled:, deadline:,
                   recur:, closed:, source:, body:, links:, headline:, parent_id:,
                   ancestor_ids:, child_ids:, section_id:, section_title:, project:,
                   availability:, revision: nil)
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
