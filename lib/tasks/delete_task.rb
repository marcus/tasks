# frozen_string_literal: true

module Tasks
  # Immutable, transport-neutral request to hard-delete one live task. Store owns
  # the transaction, the descendant guard, and the revision check; this command
  # only captures the requested target and options without retaining mutable
  # caller input.
  #
  # `expected_revision` is an opaque Store-produced value: callers carry it
  # forward from an EditSnapshot rather than construct it from a file coordinate
  # or a wall-clock timestamp. It is optional — a nil revision skips the
  # optimistic-concurrency check (the CLI convenience), while a supplied value
  # is checked against all three revision components so a cascading delete is
  # refused when any descendant, sibling, or the task itself changed.
  #
  # `cascade` opts into removing a task that still has descendants; without it a
  # task with a non-empty subtree is refused. Deleting never reparents children.
  class DeleteTask
    attr_reader :id, :cascade, :expected_revision, :history_label

    alias target_id id

    def initialize(id: nil, target_id: nil, cascade: false,
                   expected_revision: nil, history_label: nil)
      @id = immutable(id || target_id)
      @cascade = cascade == true
      @expected_revision = immutable(expected_revision)
      @history_label = immutable(history_label)
      freeze
    end

    private

    def immutable(value)
      value.is_a?(String) ? value.dup.freeze : value.freeze
    end
  end
end
