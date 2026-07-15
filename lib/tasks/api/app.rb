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
        title priority tags contexts deferred scheduled deadline state project parent_id recurrence body
      ].freeze
      PATCH_FIELDS = %w[
        title priority body contexts tags deferred scheduled deadline recurrence parent_id state
      ].freeze
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
          application: Application.new(store_factory: factory),
          port: port, max_depth: paths.max_depth,
          urgent_days: paths.urgent_days, logger: logger
        )
      end

      def initialize(application:, port: 4747, max_depth: Tree::DEFAULT_MAX_DEPTH,
                     urgent_days: Quadrants::DEFAULT_URGENT_DAYS, logger: $stderr,
                     request_id_generator: nil, clock: nil)
        @application = application
        @port = Integer(port)
        @max_depth = Integer(max_depth)
        @urgent_days = Integer(urgent_days)
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
          capabilities: { undo: true, redo: true, archive_sweep: true, events: false },
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
        query_params(request, [])
        body = json_body(request)
        reject_unknown_fields!(body, CREATE_FIELDS)
        validate_create_body!(body)
        recurrence = normalize_recurrence(body["recurrence"], allow_off: false)
        attributes = {
          title: body["title"], priority: body["priority"],
          tags: Array(body["tags"]) + Array(body["contexts"]),
          deferred: body.fetch("deferred", false),
          scheduled: body["scheduled"], deadline: body["deadline"], state: body["state"],
          project: body["project"], parent_id: body["parent_id"], recurrence: recurrence,
          body: normalize_body(body["body"]),
        }
        ensure_store_ready!(request_id)
        result = application.create_task(
          attributes,
          context: OperationContext.new(operation_id: request_id, source: :api)
        )
        mutation_failure!(result, request_id, parent_id: body["parent_id"])
        id = result.touched_ids.fetch(0)
        resource_result = application.task_result_from_mutation(result, id)
        view = resource_result.data
        [
          201,
          { "location" => "/api/v1/tasks/#{id}", "etag" => etag(view.revision) },
          Representation.success(Representation.task(view), resource_result.store_revision),
        ]
      end

      def update_task(request, id, request_id)
        query_params(request, [])
        expected_revision = if_match!(request)
        body = json_body(request)
        reject_unknown_fields!(body, PATCH_FIELDS)
        validation!(changes: ["must contain at least one field"]) if body.empty?
        validate_patch_body!(body)
        changes = normalize_patch_changes(body, id, request_id)
        ensure_store_ready!(request_id)
        result = application.update_task(
          id, changes, expected_revision: expected_revision,
          context: OperationContext.new(operation_id: request_id, source: :api)
        )
        mutation_failure!(result, request_id, id: id)
        resource_result = application.task_result_from_mutation(result, id)
        view = resource_result.data
        [
          200,
          { "etag" => etag(view.revision) },
          Representation.success(Representation.task(view), resource_result.store_revision),
        ]
      end

      def delete_task(request, id, request_id)
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

      def normalize_patch_changes(body, id, request_id)
        changes = body.transform_keys(&:to_sym)
        changes[:body] = normalize_body(body["body"]) if body.key?("body")
        if body.key?("recurrence")
          changes[:recurrence] = normalize_recurrence(body["recurrence"], allow_off: true)
        end
        if body.key?("parent_id")
          parent_id = body["parent_id"]
          if parent_id.nil?
            current = application.get_task_result(id, source: :live)
            read_failure!(current, request_id) unless current.ok?
            parent_id = current.data.section_id
          end
          changes.delete(:parent_id)
          changes[:location] = parent_id
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

      def validate_patch_body!(body) = validate_common_body!(body, create: false)

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

      def mutation_failure!(result, request_id, parent_id: nil, id: nil)
        return if result.ok?

        case result.status
        when :not_found
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
          message = if result.summary.is_a?(Hash) && result.summary[:descendants].to_i.positive?
                      "The task has descendants; retry with cascade=true to delete them."
                    else
                      Errors.message(:conflict)
                    end
          raise HttpError.new(409, :conflict, message, details: result.summary || {})
        when :cycle, :too_deep
          details = result.summary || {}
          details = details.merge(max_depth: @max_depth) if result.too_deep?
          raise HttpError.new(409, result.status, Errors.message(result.status), details: details)
        when :store_invalid, :unavailable
          raise HttpError.new(503, result.status, Errors.message(result.status))
        else
          raise HttpError.new(503, :unavailable, Errors.message(:unavailable))
        end
      end

      def read_failure!(result, _request_id)
        case result.status
        when :not_found
          raise HttpError.new(404, :not_found, Errors.message(:not_found))
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
        raw = request.body.read(BODY_LIMIT + 1)
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
        return path if %w[/healthz /readyz /api/v1/meta /api/v1/sections /api/v1/tasks].include?(path)
        return "/api/v1/tasks/{id}" if path.match?(%r{\A/api/v1/tasks/[^/]+\z})

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
