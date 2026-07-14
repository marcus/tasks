# frozen_string_literal: true

module Tasks
  # Immutable, Store-produced view of every field the task editor may change.
  # `baselines` are the field-owned semantic values used for ordinary conflict
  # checks; `fingerprints` guard operations whose effects span a subtree.
  class EditSnapshot
    FIELDS = %i[
      title priority deferred scheduled deadline recurrence contexts tags body
      location state
    ].freeze

    attr_reader :id, :title, :priority, :deferred, :scheduled, :deadline,
                :recurrence, :contexts, :tags, :body, :parent, :state, :closed,
                :baselines, :fingerprints, :metadata, :revision

    alias recur recurrence
    alias parent_id parent

    def initialize(id:, title:, priority:, deferred:, scheduled:, deadline:,
                   recurrence:, contexts:, tags:, body:, parent:, state:, closed:,
                   baselines:, fingerprints:, revision:, metadata: {})
      @id = immutable(id)
      @title = immutable(title)
      @priority = immutable(priority)
      @deferred = deferred
      @scheduled = scheduled
      @deadline = deadline
      @recurrence = immutable(recurrence)
      @contexts = immutable(contexts)
      @tags = immutable(tags)
      @body = immutable(body)
      @parent = immutable(parent)
      @state = immutable(state)
      @closed = closed
      @baselines = immutable(baselines)
      @fingerprints = immutable(fingerprints)
      @revision = immutable(revision)
      @metadata = immutable(metadata)
      freeze
    end

    def baseline(field) = @baselines.fetch(normalize_field(field))

    # Location and state own effects wider than one scalar field, so their
    # expected value is an affected-structure fingerprint.
    def expected_for(field)
      field = normalize_field(field)
      @fingerprints.fetch(field) { @baselines.fetch(field) }
    end
    alias expected expected_for

    def [](field)
      field = normalize_field(field)
      return parent if field == :location
      return recurrence if field == :recurrence
      public_send(field)
    end

    private

    def normalize_field(field)
      field = field.to_sym
      field == :recur ? :recurrence : field
    end

    def immutable(value)
      case value
      when Hash
        value.each_with_object({}) { |(key, item), copy| copy[key] = immutable(item) }.freeze
      when Array
        value.map { |item| immutable(item) }.freeze
      when String
        value.dup.freeze
      else
        value.freeze
      end
    end
  end
end
