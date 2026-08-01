# frozen_string_literal: true

module Tasks
  module Api
    class HttpError < StandardError
      attr_reader :status, :code, :details, :headers

      def initialize(status, code, message, details: {}, headers: {})
        @status = Integer(status)
        @code = code.to_s.freeze
        @details = details.freeze
        @headers = headers.freeze
        super(message.to_s.freeze)
      end
    end

    module Errors
      MESSAGES = {
        malformed_request: "The request is malformed.",
        forbidden_origin: "This origin is not allowed to mutate tasks.",
        not_found: "No task with that id.",
        conflict: "The requested change conflicts with the current task.",
        # A lost delegation race, distinct from `stale_revision`: the client's
        # view of the task was current, and the claim is simply held by someone
        # else. `details` names the holder and when it took the task.
        claim_conflict: "The task's claim is held by another worker.",
        cycle: "A task cannot be moved under itself or a descendant.",
        too_deep: "Nesting the task would exceed the maximum depth.",
        stale_revision: "The task changed after it was loaded.",
        payload_too_large: "The request body is too large.",
        unsupported_media_type: "Request bodies must be application/json.",
        validation_failed: "One or more fields are invalid.",
        missing_precondition: "This write requires an If-Match header.",
        unsupported_schema_version: "The task store declares a schema version this build does not implement.",
        store_invalid: "The task list failed structural validation.",
        unavailable: "The task store is unavailable; retry.",
      }.freeze

      module_function

      def message(code) = MESSAGES.fetch(code.to_sym)
    end
  end
end
