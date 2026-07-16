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
          parent_id: view.parent_id == view.section_id ? nil : view.parent_id,
          section_id: view.section_id,
          depth: view.ancestor_ids.count { |id| id != view.section_id },
          state: view.state,
          priority: view.priority,
          title: view.title,
          contexts: view.contexts,
          tags: view.tags.reject { |tag| tag.start_with?("@") || tag == Store::DEFER_TAG },
          deferred: view.deferred?,
          scheduled: view.scheduled&.iso8601,
          scheduled_time: view.scheduled_time,
          deadline: view.deadline&.iso8601,
          deadline_time: view.deadline_time,
          available: view.available?,
          availability_reason: view.availability_reason.to_s,
          availability_blocker_id: view.availability_blocker_id,
          available_at: view.available_at,
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

      # A project/area resource rolled up over its open tasks. Unlike
      # ProjectView#to_h (which omits nil keys for the on-disk-lean CLI shape),
      # every field is present with an explicit null so the HTTP schema can be
      # strict. Physical `line` is never exposed.
      def project(view)
        {
          id: view.id,
          title: view.title,
          parent_id: view.parent_id,
          kind: view.kind,
          open_count: view.open_count,
          next_count: view.next_count,
          next_date: view.next_date&.iso8601,
          next_time: view.next_time,
          next_at: view.next_at&.iso8601,
          stuck: view.stuck,
          held_count: view.held_count,
          body: view.body,
          task_ids: view.task_ids,
        }
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
