# frozen_string_literal: true

require_relative "store"
require_relative "../tasks/lead"

module Tui
  # Plain-text/markdown renderings of a task for yanking out of the TUI.
  module Export
    module_function

    # Stable id for pasting into the agent prompt. Exact ids stay unambiguous
    # across duplicate titles and later retitles.
    def reference(item) = item.id

    # The whole task as pasteable markdown. `notes` is the item's prose lines
    # (already filtered) from Store#body.
    def markdown(item, notes)
      md = ["## #{item.title}", ""]
      md << "- state: #{item.state}"
      md << "- priority: #{item.priority}" if item.priority
      md << "- deadline: #{temporal_text(item, :deadline)}" if item.deadline
      md << "- available from: #{temporal_text(item, :scheduled)}" if item.scheduled
      md << "- lead time: #{Tasks::Lead.display(item.lead, lead_anchor(item))}" if lead?(item)
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

    def lead?(item)
      item.respond_to?(:lead) && Tasks::Lead.span?(item.lead)
    end

    def lead_anchor(item)
      return item.deadline_value || item.scheduled_value if item.respond_to?(:deadline_value)

      item.deadline || item.scheduled
    end

    def temporal_text(item, field)
      value = item.respond_to?("#{field}_value") && item.public_send("#{field}_value")
      return item.public_send(field).iso8601 unless value&.local_time

      zone = value.timezone ? " [#{value.timezone}]" : ""
      fold = value.fold == 1 ? " fold=later" : ""
      "#{value.date.iso8601} #{value.local_time}#{zone}#{fold}"
    end
  end
end
