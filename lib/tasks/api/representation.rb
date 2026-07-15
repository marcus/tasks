# frozen_string_literal: true

require_relative "../store"

module Tasks
  module Api
    module Representation
      module_function

      def task(view)
        {
          id: view.id,
          revision: view.revision,
          source: view.source.to_s,
          parent_id: view.parent_id,
          section_id: view.section_id,
          depth: view.ancestor_ids.count { |id| id != view.section_id },
          state: view.state,
          priority: view.priority,
          title: view.title,
          contexts: view.contexts,
          tags: view.tags.reject { |tag| tag.start_with?("@") || tag == Store::DEFER_TAG },
          deferred: view.deferred?,
          scheduled: view.scheduled&.iso8601,
          deadline: view.deadline&.iso8601,
          available: view.available?,
          availability_reason: view.availability_reason.to_s,
          availability_blocker_id: view.availability_blocker_id,
          recurrence: view.recur,
          body: view.body,
          closed: view.closed&.iso8601,
          archived: view.source == :archive,
          project: view.project || view.section_title,
          child_count: view.child_ids.length,
          descendant_count: view.descendant_count,
          links: view.links.map { |link| { system: link.system, url: link.url, label: link.label } },
        }
      end

      def section(view)
        { id: view.id, title: view.title, parent_id: view.parent_id }
      end

      def success(data, store_revision)
        { data: data, meta: { store_revision: store_revision } }
      end

      def error(code, message, request_id, details = {})
        { error: { code: code.to_s, message: message, details: details, request_id: request_id } }
      end
    end
  end
end
