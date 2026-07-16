# frozen_string_literal: true

require "date"
require "json"
require "securerandom"

require "rack/request"
require "rack/utils"

require_relative "../application"
require_relative "../config"
require_relative "../recur"
require_relative "errors"
require_relative "representation"

module Tasks
  module Api
    class App
      BODY_LIMIT = 64 * 1024
      TASK_ID = /\A[0-9a-f]{8}\z/
      LIST_QUERY_KEYS = %w[scope state context tag priority text body deferred available recurring].freeze
      TASK_QUERY_KEYS = %w[source].freeze
      DELETE_QUERY_KEYS = %w[cascade].freeze
      CREATE_FIELDS = %w[
        title priority tags contexts deferred scheduled scheduled_time deadline deadline_time
        state project parent_id recurrence body
      ].freeze
      PATCH_FIELDS = %w[
        title priority body contexts tags deferred scheduled scheduled_time deadline deadline_time
        recurrence parent_id placement state
      ].freeze
      PLACEMENT_FIELDS = %w[parent_id before_id].freeze
      FORWARDED_HEADERS = %w[
        HTTP_FORWARDED HTTP_X_FORWARDED_HOST HTTP_X_FORWARDED_PROTO HTTP_X_FORWARDED_PORT
      ].freeze

      def self.build(paths:, port: 4747, logger: $stderr)
        factory = StoreFactory.new(
          org: paths.org, archive: paths.archive,
          links: paths.links, link_systems: paths.link_systems,
          max_depth: paths.max_depth
        )
        new(
          application: Application.new(
            store_factory: factory,
            temporal_context_factory: -> {
              TemporalContext.capture(timezone: paths.timezone, time_format: paths.time_format)
            }
          ),
          port: port, max_depth: paths.max_depth,
          urgent_days: paths.urgent_days, logger: logger,
          timezone: paths.timezone, time_format: paths.time_format
        )
      end

      def initialize(application:, port: 4747, max_depth: Tree::DEFAULT_MAX_DEPTH,
                     urgent_days: Quadrants::DEFAULT_URGENT_DAYS, logger: $stderr,
                     request_id_generator: nil, clock: nil,
                     timezone: "Etc/UTC", time_format: 12)
        @application = application
        @port = Integer(port)
        @max_depth = Integer(max_depth)
        @urgent_days = Integer(urgent_days)
        @timezone = timezone
        @time_format = Integer(time_format)
        @logger = logger
        @request_id_generator = request_id_generator || -> { "req_#{SecureRandom.hex(8)}" }
        @clock = clock || -> { Process.clock_gettime(Process::CLOCK_MONOTONIC) }
        @allowed_hosts = ["127.0.0.1:#{@port}", "localhost:#{@port}"].freeze
        @allowed_origins = @allowed_hosts.map { |host| "http://#{host}" }.freeze
      end

      def call(env)
        request_id = @request_id_generator.call.to_s
        started = @clock.call
        request = Rack::Request.new(env)
        route = route_name(request)
        status = 500

        begin
          enforce_host!(env)
          enforce_origin!(request)
          status, headers, body = dispatch(request, request_id)
        rescue HttpError => error
          status = error.status
          headers = error.headers
          body = Representation.error(error.code, error.message, request_id, error.details)
        rescue Timezones::Error => error
          status = 503
          headers = {}
          body = Representation.error(
            :store_invalid,
            "A floating task time is invalid in the configured time zone.",
            request_id,
            { temporal_error: error.message }
          )
        rescue StandardError
          status = 503
          headers = {}
          body = Representation.error(
            :unavailable, Errors.message(:unavailable), request_id
          )
        ensure
          log_request(request.request_method, route, status, request_id, started)
        end

        rack_response(status, headers, body, request_id)
      end

      private

      attr_reader :application

      def dispatch(request, request_id)
        method = request.request_method
        path = request.path_info
        return health(request) if method == "GET" && path == "/healthz"
        return readiness(request, request_id) if method == "GET" && path == "/readyz"
        return meta(request, request_id) if method == "GET" && path == "/api/v1/meta"
        return sections(request, request_id) if method == "GET" && path == "/api/v1/sections"
        return list_tasks(request, request_id) if method == "GET" && path == "/api/v1/tasks"
        return create_task(request, request_id) if method == "POST" && path == "/api/v1/tasks"

        match = path.match(%r{\A/api/v1/tasks/([^/]+)\z})
        if match
          id = valid_task_id!(match[1])
          return get_task(request, id, request_id) if method == "GET"
          return update_task(request, id, request_id) if method == "PATCH"
          return delete_task(request, id, request_id) if method == "DELETE"
        end

        return list_projects(request, request_id) if method == "GET" && path == "/api/v1/projects"
        return create_project(request, request_id) if method == "POST" && path == "/api/v1/projects"

        project = path.match(%r{\A/api/v1/projects/([^/]+?)(/complete|/archive)?\z})
        if project
          id = valid_task_id!(project[1])
          case project[2]
          when nil
            return get_project(request, id, request_id) if method == "GET"
            return rename_project(request, id, request_id) if method == "PATCH"
          when "/complete"
            return complete_project(request, id, request_id) if method == "POST"
          when "/archive"
            return archive_project(request, id, request_id) if method == "POST"
          end
        end

        raise HttpError.new(404, :not_found, Errors.message(:not_found))
      end

      def health(request)
        query_params(request, [])
        [200, {}, { status: "ok" }]
      end

      def readiness(request, request_id)
        query_params(request, [])
        result = application.read_status_result
        return [200, {}, { status: "ready" }] if result.ok?

        read_failure!(result, request_id)
      end

      def meta(request, request_id)
        query_params(request, [])
        result = application.read_status_result
        read_failure!(result, request_id) unless result.ok?
        data = {
          api_version: "v1", server_mode: "loopback",
          states: TaskFilter::STATE_ORDER,
          open_states: Store::OPEN_STATES,
          closed_states: Store::DONE_STATES,
          priorities: Check::PRIORITIES,
          max_depth: @max_depth,
          urgent_days: @urgent_days,
          timezone: @timezone,
          time_format: @time_format,
          tzdb_version: Timezones.tzdb_version,
          temporal_precision: "minute",
          # Capabilities advertise what THIS server routes, not the store's
          # abilities. `projects` is true because the project routes below are
          # dispatched; undo/redo/archive_sweep flip to true when the Phase 3
          # manager endpoints (/history, /archive-sweeps) are dispatched.
          capabilities: { projects: true, undo: false, redo: false, archive_sweep: false, events: false },
        }
        [200, { "etag" => etag(result.store_revision) }, Representation.success(data, result.store_revision)]
      end

      def sections(request, request_id)
        query_params(request, [])
        result = application.list_sections_result
        read_failure!(result, request_id) unless result.ok?
        data = result.data.map { |view| Representation.section(view) }
        [200, {}, Representation.success(data, result.store_revision)]
      end

      def list_tasks(request, request_id)
        query = query_params(request, LIST_QUERY_KEYS)
        scope = query.fetch("scope", "open")
        allowed_scopes = TaskFilter::SCOPES.map(&:to_s)
        validation!(scope: ["must be open, done, archived, or all"]) unless allowed_scopes.include?(scope)

        state = query["state"]
        validation!(state: ["must be a documented task state"]) if state && !TaskFilter::STATE_ORDER.include?(state)
        priority = query["priority"]
        validation!(priority: ["must be A, B, or C"]) if priority && !Check::PRIORITIES.include?(priority)

        context = query["context"]
        context = "@#{context}" if context && !context.start_with?("@")
        validation!(context: ["must name a context"]) if context == "@"
        tag = query["tag"]
        validation!(tag: ["must be an ordinary tag"]) if tag&.start_with?("@") || tag == Store::DEFER_TAG

        body = boolean_query(query, "body", default: false)
        deferred = boolean_query(query, "deferred", default: false)
        recurring = boolean_query(query, "recurring", default: false)
        available = boolean_query(query, "available", default: nil)
        if available == false && scope != "open"
          validation!(available: ["false is only valid with scope=open"])
        end

        filter = TaskFilter.new(
          scope: scope, state: state, priority: priority,
          contexts: context ? [context] : [], tags: tag ? [tag] : [],
          text: query["text"] ? [query["text"]] : [], body_search: body,
          someday_only: deferred, unavailable_only: available == false,
          recurring_only: recurring
        )
        result = application.list_tasks_result(filter)
        read_failure!(result, request_id) unless result.ok?
        tasks = result.data.tasks
        tasks = tasks.select(&:available?) if available == true
        data = tasks.map { |view| Representation.task(view) }
        [200, {}, Representation.success(data, result.store_revision)]
      rescue Timezones::Error
        raise
      rescue ArgumentError => error
        validation!(query: [safe_argument_message(error)])
      end

      def get_task(request, id, request_id)
        query = query_params(request, TASK_QUERY_KEYS)
        source = query.fetch("source", "live")
        validation!(source: ["must be live or archive"]) unless %w[live archive].include?(source)
        result = application.get_task_result(id, source: source.to_sym)
        read_failure!(result, request_id) unless result.ok?
        view = result.data
        [
          200,
          { "etag" => etag(view.revision) },
          Representation.success(Representation.task(view), result.store_revision),
        ]
      end

      def create_task(request, request_id)
        temporal = request_temporal_context
        query_params(request, [])
        body = json_body(request)
        reject_unknown_fields!(body, CREATE_FIELDS)
        validate_create_body!(body)
        recurrence = normalize_recurrence(body["recurrence"], allow_off: false)
        attributes = {
          title: body["title"], priority: body["priority"],
          tags: Array(body["tags"]) + Array(body["contexts"]),
          deferred: body.fetch("deferred", false),
          scheduled: temporal_input(body, "scheduled", context: temporal),
          deadline: temporal_input(body, "deadline", context: temporal), state: body["state"],
          project: body["project"], parent_id: body["parent_id"], recurrence: recurrence,
          body: normalize_body(body["body"]),
        }
        ensure_store_ready!(request_id)
        result = application.create_task(
          attributes,
          context: OperationContext.new(operation_id: request_id, source: :api,
                                        temporal_context: temporal)
        )
        mutation_failure!(result, request_id, parent_id: body["parent_id"])
        id = result.touched_ids.fetch(0)
        resource_result = application.task_result_from_mutation(result, id, temporal_context: temporal)
        view = resource_result.data
        [
          201,
          { "location" => "/api/v1/tasks/#{id}", "etag" => etag(view.revision) },
          Representation.success(Representation.task(view), resource_result.store_revision),
        ]
      end

      def update_task(request, id, request_id)
        temporal = request_temporal_context
        query_params(request, [])
        expected_revision = if_match!(request)
        body = json_body(request)
        reject_unknown_fields!(body, PATCH_FIELDS)
        validation!(changes: ["must contain at least one field"]) if body.empty?
        current = application.get_task_result(id, source: :live)
        if current.status == :not_found
          raise HttpError.new(404, :not_found, "No live task with that id.",
                              details: { field: "id", id: id })
        end
        read_failure!(current, request_id) unless current.ok?
        validate_patch_body!(body, current: current.data)
        changes = normalize_patch_changes(body, current: current.data, context: temporal)
        ensure_store_ready!(request_id)
        result = application.update_task(
          id, changes, expected_revision: expected_revision,
          context: OperationContext.new(operation_id: request_id, source: :api,
                                        temporal_context: temporal)
        )
        mutation_failure!(result, request_id, id: id, placement: changes[:location])
        resource_result = application.task_result_from_mutation(result, id, temporal_context: temporal)
        view = resource_result.data
        [
          200,
          { "etag" => etag(view.revision) },
          Representation.success(Representation.task(view), resource_result.store_revision),
        ]
      end

      def delete_task(request, id, request_id)
        reject_delete_body!(request)
        expected_revision = if_match!(request)
        query = query_params(request, DELETE_QUERY_KEYS)
        cascade = boolean_query(query, "cascade", default: false)
        ensure_store_ready!(request_id)
        result = application.delete_task(
          id, cascade: cascade, expected_revision: expected_revision,
          context: OperationContext.new(operation_id: request_id, source: :api)
        )
        mutation_failure!(result, request_id, id: id)
        [204, {}, nil]
      end

      # Projects and areas are rolled-up sections. Reads mirror the CLI's
      # `projects`/`project show`; mutations map the shared MutationResult
      # vocabulary. Project mutations carry no per-resource revision (the domain
      # exposes none for a section retitle/cascade/sweep), so — unlike task
      # writes — no If-Match precondition is required; the parity difference is
      # documented in the OpenAPI contract.
      def list_projects(request, request_id)
        query_params(request, [])
        result = application.list_projects_result
        read_failure!(result, request_id) unless result.ok?
        data = result.data.map { |view| Representation.project(view) }
        [200, { "etag" => etag(result.store_revision) }, Representation.success(data, result.store_revision)]
      end

      # Create a new empty project section under the top-level "Projects" root
      # (bootstrapped when absent). A blank/missing title is 422 at the adapter;
      # a duplicate title (an existing project or area) is the domain's :invalid,
      # mapped to 422 by project_mutation_failure!. Responds 201 with the new
      # project resource re-read from the committed snapshot.
      def create_project(request, request_id)
        query_params(request, [])
        body = json_body(request)
        reject_unknown_fields!(body, %w[title])
        validation!(title: ["is required"]) unless body.key?("title")
        unless body["title"].is_a?(String) && !body["title"].strip.empty?
          validation!(title: ["must be non-empty text"])
        end
        ensure_store_ready!(request_id)
        result = application.create_project(title: body["title"])
        project_mutation_failure!(result, nil)
        id = result.summary.fetch(:created_id)
        read = application.project_result(id)
        read_failure!(read, request_id) unless read.ok?
        [
          201,
          { "location" => "/api/v1/projects/#{id}", "etag" => etag(read.store_revision) },
          Representation.success(Representation.project(read.data), read.store_revision),
        ]
      end

      def get_project(request, id, request_id)
        query_params(request, [])
        result = application.project_result(id)
        read_failure!(result, request_id) unless result.ok?
        [
          200,
          { "etag" => etag(result.store_revision) },
          Representation.success(Representation.project(result.data), result.store_revision),
        ]
      end

      def rename_project(request, id, request_id)
        query_params(request, [])
        body = json_body(request)
        reject_unknown_fields!(body, %w[title])
        validation!(title: ["is required"]) unless body.key?("title")
        unless body["title"].is_a?(String) && !body["title"].strip.empty?
          validation!(title: ["must be non-empty text"])
        end
        ensure_store_ready!(request_id)
        # Pre-validate like archive: a non-project/area id (Inbox, the "Projects"
        # root, a done-pile section, a task) is 404 before any write, so the
        # store's mechanical section retitle never mutates a non-project section.
        before = application.project_result(id)
        read_failure!(before, request_id) unless before.ok?
        result = application.rename_project(id, title: body["title"])
        project_mutation_failure!(result, id)
        title = body["title"].strip
        project_after_mutation(id, request_id) { renamed_project_view(before.data, title) }
      end

      def complete_project(request, id, request_id)
        query_params(request, [])
        reject_action_body!(request)
        ensure_store_ready!(request_id)
        # Pre-validate like archive: a non-project/area id is 404 before any
        # write, so completing Inbox, the "Projects" root, or a done-pile section
        # is impossible rather than closing its tasks and then 404-ing.
        before = application.project_result(id)
        read_failure!(before, request_id) unless before.ok?
        result = application.complete_project(id)
        project_mutation_failure!(result, id)
        project_after_mutation(id, request_id) { completed_project_view(before.data) }
      end

      def archive_project(request, id, request_id)
        query = query_params(request, %w[force])
        force = boolean_query(query, "force", default: false)
        reject_action_body!(request)
        ensure_store_ready!(request_id)
        # Mirror the CLI refusal: an archive that would carry live open work
        # needs an explicit force. Deferred/held tasks are still open work, so
        # they block too (parity with complete's cascade, which closes them).
        # The store sweep is mechanical, so this policy lives in the adapter
        # (see also the CLI's `project archive --force`).
        view = application.project_result(id)
        read_failure!(view, request_id) unless view.ok?
        open_count = view.data.open_count
        held_count = view.data.held_count
        if (open_count + held_count).positive? && !force
          raise HttpError.new(
            409, :conflict,
            "The project still has open tasks; retry with force=true to archive them.",
            details: { open_count: open_count, held_count: held_count }
          )
        end
        result = application.archive_project(id)
        project_mutation_failure!(result, id)
        status = application.read_status_result
        data = {
          id: id,
          archived: result.summary ? result.summary[:archived] : result.touched_ids.length,
          moved_ids: result.touched_ids,
        }
        [200, { "etag" => etag(status.store_revision) }, Representation.success(data, status.store_revision)]
      end

      # Shape a successful complete/rename response. Prefer the post-mutation
      # re-read: a project stays in the read model, so its refreshed counts come
      # from a coherent checked snapshot. When the section no longer surfaces
      # there — a completed area drops out (no open work), a rename moves an area
      # out of scope (e.g. retitled "Inbox") — the write already committed, so we
      # must NOT 404. The block yields a ProjectView synthesized from the
      # pre-read with the post-state applied, paired with the current revision.
      def project_after_mutation(id, request_id)
        read = application.project_result(id)
        return project_response(read.data, read.store_revision) if read.ok?

        status = application.read_status_result
        read_failure!(status, request_id) unless status.ok?
        project_response(yield, status.store_revision)
      end

      def project_response(view, revision)
        [
          200,
          { "etag" => etag(revision) },
          Representation.success(Representation.project(view), revision),
        ]
      end

      # The pre-read project as it stands after a completing cascade: every open
      # task (deferred included) is closed, so the rollups are zero and it is
      # stuck. Consistent with the Project schema.
      def completed_project_view(view)
        ProjectView.new(
          id: view.id, title: view.title, parent_id: view.parent_id, kind: view.kind,
          line: view.line, open_count: 0, next_count: 0, next_date: nil, stuck: true,
          body: view.body, task_ids: [], held_count: 0
        )
      end

      # The pre-read project with only its title replaced — the rollups are
      # unchanged by a retitle.
      def renamed_project_view(view, title)
        ProjectView.new(
          id: view.id, title: title, parent_id: view.parent_id, kind: view.kind,
          line: view.line, open_count: view.open_count, next_count: view.next_count,
          next_date: view.next_date, stuck: view.stuck, body: view.body,
          task_ids: view.task_ids, held_count: view.held_count
        )
      end

      def project_mutation_failure!(result, id)
        return if result.ok?

        case result.status
        when :not_found
          raise HttpError.new(404, :not_found, "No project with that id.", details: { id: id })
        when :invalid
          details = result.field_errors.empty? ? {} : { fields: result.field_errors }
          raise HttpError.new(422, :validation_failed, Errors.message(:validation_failed), details: details)
        when :store_invalid, :unavailable
          raise HttpError.new(503, result.status, Errors.message(result.status))
        else
          raise HttpError.new(503, :unavailable, Errors.message(:unavailable))
        end
      end

      # A parameterless action POST (complete/archive) accepts no request body;
      # a non-empty one is rejected the same way DELETE rejects a body.
      def reject_action_body!(request)
        reject_delete_body!(request, subject: "Project action requests")
      end

      def normalize_patch_changes(body, current:, context:)
        changes = body.transform_keys(&:to_sym)
        %w[scheduled deadline].each do |field|
          next unless body.key?(field) || body.key?("#{field}_time")
          changes.delete("#{field}_time".to_sym)
          changes[field.to_sym] = temporal_patch_value(body, field, current: current, context: context)
        end
        changes[:body] = normalize_body(body["body"]) if body.key?("body")
        if body.key?("recurrence")
          changes[:recurrence] = normalize_recurrence(body["recurrence"], allow_off: true)
        end
        if body.key?("parent_id")
          changes.delete(:parent_id)
          # Store resolves nil to the enclosing section under the mutation
          # lock, so a concurrent ancestor move cannot reuse a stale section
          # observed by a separate adapter read.
          changes[:location] = body["parent_id"].nil? ? TaskChangeset::UNNEST : body["parent_id"]
        end
        if body.key?("placement")
          placement = body.fetch("placement")
          changes.delete(:placement)
          changes[:location] = TaskPlacement.new(
            parent_id: placement.fetch("parent_id"),
            before_id: placement["before_id"]
          )
        end
        changes
      end

      def validate_create_body!(body)
        validation!(title: ["is required"]) unless body.key?("title")
        validate_common_body!(body, create: true)
        if body["project"] && body["parent_id"]
          validation!(location: ["project and parent_id cannot both be supplied"])
        end
      end

      def validate_patch_body!(body, current:)
        validate_common_body!(body, create: false)
        %w[scheduled deadline].each do |field|
          next unless body["#{field}_time"] && !body.key?(field)
          validation!("#{field}_time" => ["requires #{field}"]) unless current.public_send(field)
        end
        if body.key?("placement") && body.key?("parent_id")
          validation!(
            placement: ["cannot be combined with parent_id"],
            parent_id: ["cannot be combined with placement"]
          )
        end
        validate_placement!(body["placement"]) if body.key?("placement")
      end

      def validate_placement!(placement)
        unless placement.is_a?(Hash)
          validation!(placement: ["must be an object"])
        end

        unknown = placement.keys - PLACEMENT_FIELDS
        unless unknown.empty?
          validation!(placement: ["must contain only parent_id and before_id"])
        end
        unless placement.key?("parent_id")
          validation!("placement.parent_id" => ["is required"])
        end

        parent_id = placement["parent_id"]
        unless parent_id.is_a?(String) && parent_id.match?(TASK_ID)
          validation!("placement.parent_id" => ["must be a stable task or section id"])
        end
        before_id = placement["before_id"]
        unless before_id.nil? || (before_id.is_a?(String) && before_id.match?(TASK_ID))
          validation!("placement.before_id" => ["must be a stable task id or null"])
        end
      end

      def validate_common_body!(body, create:)
        if body.key?("title") && (!body["title"].is_a?(String) || body["title"].strip.empty?)
          validation!(title: ["must be non-empty text"])
        end
        if body.key?("priority") && !body["priority"].nil? && !Check::PRIORITIES.include?(body["priority"])
          validation!(priority: ["must be A, B, C, or null"])
        end
        if body.key?("state") && !TaskFilter::STATE_ORDER.include?(body["state"])
          validation!(state: ["must be a documented task state"])
        end
        %w[deferred].each do |field|
          validation!(field => ["must be true or false"]) if body.key?(field) && ![true, false].include?(body[field])
        end
        %w[scheduled deadline].each do |field|
          validate_iso_date!(field, body[field]) if body.key?(field)
          validate_time_input!("#{field}_time", body["#{field}_time"]) if body.key?("#{field}_time")
          if create && body.key?("#{field}_time") && !body.key?(field)
            validation!("#{field}_time" => ["requires #{field}"])
          end
          if body[field].nil? && body.key?(field) && body["#{field}_time"]
            validation!("#{field}_time" => ["cannot be set when #{field} is null"])
          end
        end
        %w[tags contexts].each do |field|
          next unless body.key?(field)
          value = body[field]
          validation!(field => ["must be a list of text values"]) unless value.is_a?(Array) && value.all? { |item| item.is_a?(String) }
        end
        if body.key?("contexts") && body["contexts"].any? { |tag| !tag.start_with?("@") || tag.length == 1 }
          validation!(contexts: ["each context must start with @"])
        end
        if body.key?("tags") && body["tags"].any? { |tag| tag.empty? || tag.start_with?("@") || tag == Store::DEFER_TAG }
          validation!(tags: ["must contain ordinary tags only"])
        end
        if body.key?("parent_id") && !body["parent_id"].nil? && !body["parent_id"].to_s.match?(TASK_ID)
          validation!(parent_id: ["must be a stable task id or null"])
        end
        if create && body.key?("project") && !body["project"].nil? && !body["project"].is_a?(String)
          validation!(project: ["must be text or null"])
        end
        if body.key?("body")
          value = body["body"]
          valid = value.nil? || value.is_a?(String) || (value.is_a?(Array) && value.all? { |line| line.is_a?(String) })
          validation!(body: ["must be text, a list of text lines, or null"]) unless valid
          validation!(body: ["null is not valid for PATCH body"]) if !create && value.nil?
        end
        if body.key?("recurrence") && !body["recurrence"].nil? && !body["recurrence"].is_a?(String)
          validation!(recurrence: ["must be text or null"])
        end
      end

      def validate_iso_date!(field, value)
        return if value.nil?
        unless value.is_a?(String) && value.match?(/\A\d{4}-\d{2}-\d{2}\z/)
          validation!(field => ["must be an ISO YYYY-MM-DD date or null"])
        end
        Date.iso8601(value)
      rescue Date::Error
        validation!(field => ["must be a real calendar date"])
      end

      def validate_time_input!(field, value)
        return if value.nil?
        validation!(field => ["must be an object or null"]) unless value.is_a?(Hash)
        unknown = value.keys - %w[local timezone fold]
        validation!(field => ["contains unknown fields: #{unknown.join(", ")}"]) unless unknown.empty?
        validation!("#{field}.local" => ["is required"]) unless value.key?("local")
        unless value["local"].is_a?(String) && TemporalValue::LOCAL_RE.match?(value["local"])
          validation!("#{field}.local" => ["must use HH:MM minute precision"])
        end
        if value.key?("timezone") && !value["timezone"].nil?
          begin
            Timezones.get(value["timezone"])
          rescue Timezones::Error => e
            validation!("#{field}.timezone" => [e.message])
          end
        end
        if value.key?("fold") && ![0, 1].include?(value["fold"])
          validation!("#{field}.fold" => ["must be 0 or 1"])
        end
      end

      def temporal_input(body, field, context:)
        date = body[field]
        return nil if date.nil?
        time = body["#{field}_time"]
        return TemporalValue.new(date: date) unless time
        TemporalValue.new(date: date, local_time: time.fetch("local"),
                          timezone: time["timezone"], fold: time.fetch("fold", 0)).tap do |value|
          value.instant(context) if value.floating?
        end
      rescue ArgumentError, Timezones::Error => e
        validation!("#{field}_time" => [e.message])
      end

      def temporal_patch_value(body, field, current:, context:)
        date_key = body.key?(field)
        time_key = body.key?("#{field}_time")
        date = date_key ? body[field] : current.public_send(field)&.iso8601
        return nil if date.nil?
        if time_key
          time = body["#{field}_time"]
          return TemporalValue.new(date: date) if time.nil?
          return temporal_input({ field => date, "#{field}_time" => time }, field, context: context)
        end
        existing = current.public_send("#{field}_value")
        begin
          value = existing ? existing.with_date(Date.iso8601(date)) : TemporalValue.new(date: date)
          value.instant(context) if value.floating?
          value
        rescue ArgumentError, Timezones::Error => e
          # Moving the date under preserved time metadata can land the kept
          # local time in a DST gap — a client-input problem, not a store one.
          validation!(field => [e.message])
        end
      end

      def request_temporal_context
        TemporalContext.capture(timezone: @timezone, time_format: @time_format)
      end

      def normalize_recurrence(value, allow_off:)
        return nil if value.nil?
        parsed = Recur.parse_interval(value)
        return nil if allow_off && parsed == :off
        validation!(recurrence: ["must be a valid recurrence interval"]) unless parsed.is_a?(String)
        parsed
      end

      def normalize_body(value)
        return nil if value.nil?
        value.is_a?(Array) ? value.join("\n") : value
      end

      def mutation_failure!(result, request_id, parent_id: nil, id: nil, placement: nil)
        return if result.ok?

        case result.status
        when :not_found
          if placement.is_a?(TaskPlacement)
            field = placement_missing_field(result)
            details = placement_details(placement)
            if field
              value_key = field == "placement.parent_id" ? :parent_id : :before_id
              details = { field: field, value_key => details.fetch(value_key) }
              message = "#{field} does not identify a live #{value_key == :parent_id ? "task or section" : "task"}."
            else
              details = { field: "id", id: id }
              message = "No live task with that id."
            end
            raise HttpError.new(404, :not_found, message, details: details)
          end
          details = parent_id ? { parent_id: parent_id } : {}
          message = parent_id ? "parent_id does not identify a live task." : Errors.message(:not_found)
          raise HttpError.new(404, :not_found, message, details: details)
        when :stale
          current = id && application.get_task_result(id, source: :live)
          details = {}
          headers = {}
          if current&.ok?
            details[:current] = Representation.task(current.data)
            headers["etag"] = etag(current.data.revision)
          end
          raise HttpError.new(412, :stale_revision, Errors.message(:stale_revision),
                              details: details, headers: headers)
        when :invalid
          details = result.field_errors.empty? ? {} : { fields: result.field_errors }
          raise HttpError.new(422, :validation_failed, Errors.message(:validation_failed), details: details)
        when :conflict
          if placement.is_a?(TaskPlacement)
            raise HttpError.new(
              409, :conflict,
              "The placement anchor is no longer a direct child of the requested parent.",
              details: placement_details(placement, result.summary, include_current_parent: true)
            )
          end
          message = if result.summary.is_a?(Hash) && result.summary[:descendants].to_i.positive?
                      "The task has descendants; retry with cascade=true to delete them."
                    else
                      Errors.message(:conflict)
                    end
          raise HttpError.new(409, :conflict, message, details: result.summary || {})
        when :cycle, :too_deep
          details = if placement.is_a?(TaskPlacement)
                      placement_details(placement, result.summary)
                    else
                      result.summary || {}
                    end
          details = details.merge(max_depth: @max_depth) if result.too_deep?
          message = if placement.is_a?(TaskPlacement) && result.cycle?
                      "The placement parent or anchor cannot be the moving task or its descendant."
                    elsif placement.is_a?(TaskPlacement) && result.too_deep?
                      "The placement would exceed max_depth."
                    else
                      Errors.message(result.status)
                    end
          raise HttpError.new(409, result.status, message, details: details)
        when :store_invalid, :unavailable
          raise HttpError.new(503, result.status, Errors.message(result.status))
        else
          raise HttpError.new(503, :unavailable, Errors.message(:unavailable))
        end
      end

      def placement_missing_field(result)
        fields = result.field_errors.keys.map(&:to_s)
        return "placement.parent_id" if fields.include?("parent_id")
        return "placement.before_id" if fields.include?("before_id")

        nil
      end

      def placement_details(placement, summary = nil, include_current_parent: false)
        details = { parent_id: placement.parent_id }
        details[:before_id] = placement.before_id if placement.before_id
        if include_current_parent && summary.is_a?(Hash) && summary.key?(:current_parent_id)
          details[:current_parent_id] = summary[:current_parent_id]
        end
        details
      end

      def read_failure!(result, _request_id)
        case result.status
        when :not_found
          raise HttpError.new(404, :not_found, Errors.message(:not_found))
        when :migration_required
          raise HttpError.new(
            409, :schema_migration_required, Errors.message(:schema_migration_required),
            details: { current_version: 1, required_version: Format::VERSION, command: "tasks migrate" }
          )
        when :store_invalid, :unavailable
          raise HttpError.new(503, result.status, Errors.message(result.status))
        else
          raise HttpError.new(503, :unavailable, Errors.message(:unavailable))
        end
      end

      def ensure_store_ready!(request_id)
        result = application.read_status_result
        read_failure!(result, request_id) unless result.ok?
      end

      def json_body(request)
        unless request.media_type == "application/json"
          raise HttpError.new(415, :unsupported_media_type, Errors.message(:unsupported_media_type))
        end
        length = request.content_length
        begin
          length = Integer(length, 10) if length
        rescue ArgumentError
          raise HttpError.new(400, :malformed_request, "Content-Length is malformed.")
        end
        if length && length > BODY_LIMIT
          raise HttpError.new(413, :payload_too_large, Errors.message(:payload_too_large))
        end
        input = request.body
        unless input
          raise HttpError.new(400, :malformed_request, "The request body is not valid JSON.")
        end

        raw = input.read(BODY_LIMIT + 1)
        if raw.bytesize > BODY_LIMIT
          raise HttpError.new(413, :payload_too_large, Errors.message(:payload_too_large))
        end
        parsed = JSON.parse(raw)
        unless parsed.is_a?(Hash)
          raise HttpError.new(400, :malformed_request, "The request body must be a JSON object.")
        end
        parsed
      rescue JSON::ParserError
        raise HttpError.new(400, :malformed_request, "The request body is not valid JSON.")
      end

      def reject_delete_body!(request, subject: "DELETE requests")
        length = request.content_length
        begin
          length = Integer(length, 10) if length
        rescue ArgumentError
          raise HttpError.new(400, :malformed_request, "Content-Length is malformed.")
        end
        if length && length > BODY_LIMIT
          raise HttpError.new(413, :payload_too_large, Errors.message(:payload_too_large))
        end

        input = request.body
        return unless input

        # IO#read(n) returns nil at EOF, so an empty body reads as nil, not "".
        raw = input.read(BODY_LIMIT + 1) || ""
        if raw.bytesize > BODY_LIMIT
          raise HttpError.new(413, :payload_too_large, Errors.message(:payload_too_large))
        end
        return if raw.empty?

        unless request.media_type == "application/json"
          raise HttpError.new(415, :unsupported_media_type, Errors.message(:unsupported_media_type))
        end
        raise HttpError.new(400, :malformed_request, "#{subject} do not accept a body.")
      end

      def reject_unknown_fields!(body, allowed)
        unknown = body.keys - allowed
        return if unknown.empty?

        validation!(unknown: unknown.map { |field| "unknown request field #{field}" })
      end

      def query_params(request, allowed)
        parsed = Rack::Utils.parse_query(request.query_string)
        unknown = parsed.keys - allowed
        unless unknown.empty?
          validation!(query: unknown.map { |field| "unknown query field #{field}" })
        end
        duplicate = parsed.find { |_key, value| value.is_a?(Array) }
        if duplicate
          raise HttpError.new(400, :malformed_request, "Query parameters may be supplied only once.")
        end
        parsed
      rescue ArgumentError
        raise HttpError.new(400, :malformed_request, "The query string is malformed.")
      end

      def boolean_query(query, key, default:)
        return default unless query.key?(key)
        return true if query[key] == "true"
        return false if query[key] == "false"

        validation!(key => ["must be true or false"])
      end

      def if_match!(request)
        raw = request.get_header("HTTP_IF_MATCH")
        unless raw && !raw.empty?
          raise HttpError.new(428, :missing_precondition, Errors.message(:missing_precondition))
        end
        match = raw.match(/\A"([^"\\]+)"\z/)
        unless match
          raise HttpError.new(422, :validation_failed, "If-Match is not a well-formed task revision.")
        end
        match[1]
      end

      def valid_task_id!(value)
        return value if value.match?(TASK_ID)

        raise HttpError.new(400, :malformed_request, "The task id is malformed.")
      end

      def validation!(fields)
        raise HttpError.new(
          422, :validation_failed, Errors.message(:validation_failed), details: { fields: fields }
        )
      end

      def enforce_host!(env)
        forwarded = FORWARDED_HEADERS.any? { |name| env.key?(name) && !env[name].to_s.empty? }
        if forwarded
          raise HttpError.new(400, :malformed_request, "Forwarded host headers are not accepted.")
        end
        host = env["HTTP_HOST"].to_s.downcase
        unless @allowed_hosts.include?(host)
          raise HttpError.new(400, :malformed_request, "The Host header is not allowed.")
        end
      end

      def enforce_origin!(request)
        return unless %w[POST PATCH DELETE].include?(request.request_method)
        origin = request.get_header("HTTP_ORIGIN")
        return if origin.nil? || origin.empty?
        return if @allowed_origins.include?(origin)

        raise HttpError.new(403, :forbidden_origin, Errors.message(:forbidden_origin))
      end

      def etag(revision) = %Q("#{revision}")

      def rack_response(status, headers, body, request_id)
        response_headers = {
          "content-type" => "application/json",
          "x-request-id" => request_id,
          "cache-control" => "no-store",
        }.merge(headers)
        if body.nil?
          response_headers.delete("content-type")
          response_headers["content-length"] = "0"
          return [status, response_headers, []]
        end
        json = JSON.generate(body)
        response_headers["content-length"] = json.bytesize.to_s
        [status, response_headers, [json]]
      end

      def route_name(request)
        path = request.path_info
        return path if %w[/healthz /readyz /api/v1/meta /api/v1/sections /api/v1/tasks /api/v1/projects].include?(path)
        return "/api/v1/tasks/{id}" if path.match?(%r{\A/api/v1/tasks/[^/]+\z})
        return "/api/v1/projects/{id}/complete" if path.match?(%r{\A/api/v1/projects/[^/]+/complete\z})
        return "/api/v1/projects/{id}/archive" if path.match?(%r{\A/api/v1/projects/[^/]+/archive\z})
        return "/api/v1/projects/{id}" if path.match?(%r{\A/api/v1/projects/[^/]+\z})

        "unmatched"
      end

      def log_request(method, route, status, request_id, started)
        duration_ms = ((@clock.call - started) * 1000).round(2)
        payload = {
          event: "http_request", request_id: request_id, method: method,
          route: route, status: status, duration_ms: duration_ms,
        }
        @logger.puts(JSON.generate(payload)) if @logger
      rescue StandardError
        nil
      end

      def safe_argument_message(error)
        text = error.message.to_s
        text.match?(/[\\\/]/) ? "invalid query value" : text
      end
    end
  end
end
