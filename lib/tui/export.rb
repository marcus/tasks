# frozen_string_literal: true

require_relative "store"

module Tui
  # Plain-text/markdown renderings of a task for yanking out of the TUI.
  module Export
    module_function

    # A reference just specific enough to paste into the Claude prompt.
    def reference(item) = item.title

    # The whole task as pasteable markdown.
    def markdown(item, block)
      md = ["## #{item.title}", ""]
      md << "- state: #{item.state}"
      md << "- priority: #{item.priority}" if item.priority
      md << "- deadline: #{item.deadline.iso8601}"   if item.deadline
      md << "- scheduled: #{item.scheduled.iso8601}" if item.scheduled
      ctx  = item.contexts
      tags = item.tags - ctx
      md << "- contexts: #{ctx.join(" ")}"  unless ctx.empty?
      md << "- tags: #{tags.join(", ")}"    unless tags.empty?

      notes = block.drop(1)
                   .reject { |l| l =~ /^\s*(SCHEDULED|DEADLINE):/ }
                   .map(&:strip).reject(&:empty?)
      unless notes.empty?
        md << ""
        md.concat(notes)
      end
      md.join("\n") + "\n"
    end
  end
end
