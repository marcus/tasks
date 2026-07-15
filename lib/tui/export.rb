# frozen_string_literal: true

require_relative "store"

module Tui
  # Plain-text/markdown renderings of a task for yanking out of the TUI.
  module Export
    module_function

    # A reference just specific enough to paste into the Claude prompt.
    def reference(item) = item.title

    # The whole task as pasteable markdown. `notes` is the item's prose lines
    # (already filtered) from Store#body.
    def markdown(item, notes)
      md = ["## #{item.title}", ""]
      md << "- state: #{item.state}"
      md << "- priority: #{item.priority}" if item.priority
      md << "- deadline: #{item.deadline.iso8601}"   if item.deadline
      md << "- available from: #{item.scheduled.iso8601}" if item.scheduled
      md << "- on hold: yes" if item.deferred?
      if item.respond_to?(:available?) && !item.available?
        reason = item.availability_reason.to_s.tr("_", " ")
        md << "- availability: #{reason}#{item.availability_blocker_id ? " via #{item.availability_blocker_id}" : ""}"
      end
      md << "- closed: #{item.closed.iso8601}"       if item.closed
      ctx  = item.contexts
      tags = item.tags - ctx
      md << "- contexts: #{ctx.join(" ")}"  unless ctx.empty?
      md << "- tags: #{tags.join(", ")}"    unless tags.empty?

      notes = notes.map(&:strip).reject(&:empty?)
      unless notes.empty?
        md << ""
        md.concat(notes)
      end
      md.join("\n") + "\n"
    end
  end
end
