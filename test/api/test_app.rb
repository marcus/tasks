# frozen_string_literal: true

require "json"
require "openapi_first"
require "rack/mock"
require "rack/request"
require "rack/response"
require "stringio"
require "tmpdir"

require_relative "../test_helper"
require "tasks/api/app"

class TestApiApp < Minitest::Test
  ROOT = File.expand_path("../..", __dir__)
  CONTRACT = File.join(ROOT, "docs/api/openapi.yaml")
  HOST = "127.0.0.1:4747"

  def setup
    @dir = Dir.mktmpdir("tasks-api-app")
    @org = File.join(@dir, "tasks.jsonl")
    @archive = File.join(@dir, "archive.jsonl")
    File.write(@org, api_fixture)
    File.write(@archive, archive_fixture)
    @log = StringIO.new
    @app = Tasks::Api::App.build(
      paths: Tasks::Config.for_dir(@dir), port: 4747, logger: @log
    )
    @request = Rack::MockRequest.new(@app)
    @definition = OpenapiFirst.load(CONTRACT)
  end

  def teardown
    FileUtils.remove_entry(@dir) if File.directory?(@dir)
  end

  def test_health_readiness_meta_and_sections
    health = get("/healthz")
    assert_response health, 200, { "status" => "ok" }

    ready = get("/readyz")
    assert_response ready, 200, { "status" => "ready" }

    meta = get("/api/v1/meta")
    assert_equal 200, meta.status
    payload = JSON.parse(meta.body)
    assert_equal "loopback", payload.dig("data", "server_mode")
    assert_equal %w[INBOX TODO NEXT WAITING DONE CANCELLED], payload.dig("data", "states")
    assert_equal 4, payload.dig("data", "max_depth")
    assert_equal %w[archive_sweep events redo undo], payload.dig("data", "capabilities").keys.sort
    assert_equal quote(payload.dig("meta", "store_revision")), meta["etag"]
    refute_match(/#{Regexp.escape(@dir)}/, meta.body)

    sections = get("/api/v1/sections")
    assert_equal 200, sections.status
    assert_equal [FIX[:inbox], FIX[:work], FIX[:home]], JSON.parse(sections.body).fetch("data").map { |row| row.fetch("id") }

    [health, ready, meta, sections].each { |response| assert_contract_response(response) }
  end

  def test_health_does_not_touch_store_and_readiness_refuses_invalid_store
    File.write(@org, "{not-json\n")

    assert_equal 200, get("/healthz").status
    response = get("/readyz")
    assert_error response, 503, "store_invalid"
    refute_match(/#{Regexp.escape(@org)}/, response.body)
    assert_contract_response(response)
  end

  def test_list_supports_every_documented_filter_and_rejects_unknown_queries
    cases = {
      "scope=done" => [FIX[:old]],
      "scope=all&state=DONE" => [FIX[:old], FIX[:pr]],
      "context=computer" => [FIX[:flight], FIX[:pr], FIX[:eval]],
      "tag=important" => [FIX[:flight], FIX[:pr], FIX[:eval]],
      "priority=B" => [FIX[:pr]],
      "scope=all&text=plants" => [FIX[:plants]],
      "text=Some%20note&body=true" => [FIX[:travel]],
      "deferred=true" => [FIX[:plants]],
      "available=false" => [FIX[:plants]],
      "available=true" => [FIX[:garden], FIX[:flight], FIX[:pr], "bbbb0001", "bbbb0002", FIX[:eval], FIX[:travel]],
      "recurring=true" => [FIX[:flight]],
    }
    cases.each do |query, expected|
      response = get("/api/v1/tasks?#{query}")
      assert_equal 200, response.status, query
      ids = JSON.parse(response.body).fetch("data").map { |row| row.fetch("id") }
      assert_equal expected, ids, query
      assert_contract_response(response)
    end

    assert_error get("/api/v1/tasks?unknown=true"), 422, "validation_failed"
    assert_error get("/api/v1/tasks?body=1"), 422, "validation_failed"
    assert_error get("/api/v1/tasks?scope=done&available=false"), 422, "validation_failed"
    assert_error get("/api/v1/tasks?tag=%40desk"), 422, "validation_failed"
  end

  def test_task_representation_and_source_exact_lookup
    live = get("/api/v1/tasks/#{FIX[:pr]}")
    assert_equal 200, live.status
    task = JSON.parse(live.body).fetch("data")
    expected_keys = %w[
      archived availability_blocker_id availability_reason available body child_count closed contexts
      deadline deferred depth descendant_count id links parent_id priority project recurrence revision
      scheduled section_id source state tags title
    ]
    assert_equal expected_keys, task.keys.sort
    assert_equal "live", task.fetch("source")
    assert_equal ["important"], task.fetch("tags")
    refute task.key?("line")
    refute task.key?("headline")
    assert_equal live["etag"], quote(task.fetch("revision"))

    archived = get("/api/v1/tasks/#{FIX[:pr]}?source=archive")
    archived_task = JSON.parse(archived.body).fetch("data")
    assert_equal "Archived duplicate", archived_task.fetch("title")
    assert_equal true, archived_task.fetch("archived")
    assert_equal "Archive", archived_task.fetch("project")

    assert_error get("/api/v1/tasks/deadbeef"), 404, "not_found"
    assert_error get("/api/v1/tasks/NOT-AN-ID"), 400, "malformed_request"
    assert_error get("/api/v1/tasks/#{FIX[:pr]}?source=both"), 422, "validation_failed"
    [live, archived].each { |response| assert_contract_response(response) }
  end

  def test_create_update_noop_and_delete_round_trip
    created = json_request(
      "POST", "/api/v1/tasks",
      title: "API-created task", priority: "B", tags: ["api"], contexts: ["@desk"],
      deferred: false, scheduled: "2026-07-20", deadline: nil, state: "NEXT",
      project: "Work", parent_id: nil, recurrence: "weekly", body: ["one", "two"]
    )
    assert_equal 201, created.status
    resource = JSON.parse(created.body).fetch("data")
    id = resource.fetch("id")
    assert_match(%r{\A/api/v1/tasks/[0-9a-f]{8}\z}, created["location"])
    assert_equal quote(resource.fetch("revision")), created["etag"]
    assert_equal ".+1w", resource.fetch("recurrence")
    assert_equal ["@desk"], resource.fetch("contexts")
    assert_equal ["api"], resource.fetch("tags")
    assert_equal ["Captured [2026-07-15].", "one", "two"], resource.fetch("body")
    assert_contract_response(created)

    updated = json_request(
      "PATCH", "/api/v1/tasks/#{id}",
      { title: "API-updated task", priority: "A", contexts: ["@home"], tags: ["changed"] },
      { "HTTP_IF_MATCH" => created["etag"] }
    )
    assert_equal 200, updated.status
    updated_resource = JSON.parse(updated.body).fetch("data")
    assert_equal "API-updated task", updated_resource.fetch("title")
    assert_equal ["@home"], updated_resource.fetch("contexts")
    refute_equal created["etag"], updated["etag"]
    assert_contract_response(updated)

    noop = json_request(
      "PATCH", "/api/v1/tasks/#{id}", { title: "API-updated task" },
      { "HTTP_IF_MATCH" => updated["etag"] }
    )
    assert_equal 200, noop.status
    assert_equal updated["etag"], noop["etag"]

    store = Tasks::Store.new(org: @org, archive: @archive)
    assert_equal [:ok, "edit title, priority, contexts, tags: API-created task"], store.undo!
    assert_equal "API-created task", store.items.find { |item| item.id == id }.title,
                 "no-op PATCH must not add a journal entry"

    current = get("/api/v1/tasks/#{id}")
    deleted = request("DELETE", "/api/v1/tasks/#{id}", "HTTP_IF_MATCH" => current["etag"])
    assert_equal 204, deleted.status
    assert_equal "", deleted.body
    refute deleted["content-type"]
    assert_contract_response(deleted)
  end

  def test_parent_null_unnests_and_tree_counts_are_derived
    parent = get("/api/v1/tasks/#{FIX[:pr]}")
    parent_resource = JSON.parse(parent.body).fetch("data")
    assert_equal 1, parent_resource.fetch("child_count")
    assert_equal 2, parent_resource.fetch("descendant_count")

    child = get("/api/v1/tasks/bbbb0001")
    moved = json_request(
      "PATCH", "/api/v1/tasks/bbbb0001", { parent_id: nil },
      { "HTTP_IF_MATCH" => child["etag"] }
    )
    resource = JSON.parse(moved.body).fetch("data")
    assert_equal FIX[:work], resource.fetch("parent_id")
    assert_equal 0, resource.fetch("depth")
  end

  def test_preconditions_stale_current_and_delete_cascade_conflict
    missing = json_request("PATCH", "/api/v1/tasks/#{FIX[:pr]}", { title: "x" })
    assert_error missing, 428, "missing_precondition"

    loaded = get("/api/v1/tasks/#{FIX[:pr]}")
    store = Tasks::Store.new(org: @org, archive: @archive)
    snapshot = store.edit_snapshot(FIX[:pr])
    result = store.apply_changeset!(Tasks::TaskChangeset.from(snapshot, changes: { title: "CLI won" }))
    assert result.ok?

    stale = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:pr]}", { title: "API loses" },
      { "HTTP_IF_MATCH" => loaded["etag"] }
    )
    assert_error stale, 412, "stale_revision"
    assert_equal "CLI won", JSON.parse(stale.body).dig("error", "details", "current", "title")
    assert_equal stale["etag"], quote(JSON.parse(stale.body).dig("error", "details", "current", "revision"))
    assert_contract_response(stale)

    current = get("/api/v1/tasks/#{FIX[:pr]}")
    conflict = request("DELETE", "/api/v1/tasks/#{FIX[:pr]}", "HTTP_IF_MATCH" => current["etag"])
    assert_error conflict, 409, "conflict"
    assert_equal 2, JSON.parse(conflict.body).dig("error", "details", "descendants")

    cascaded = request(
      "DELETE", "/api/v1/tasks/#{FIX[:pr]}?cascade=true",
      "HTTP_IF_MATCH" => current["etag"]
    )
    assert_equal 204, cascaded.status
    assert Tasks::Check.check(@org).ok?
    assert_nil Tasks::Store.new(org: @org, archive: @archive).items.find { |item| item.id == FIX[:pr] }
  end

  def test_transport_validation_body_limit_host_origin_and_forwarded_headers
    assert_error request("POST", "/api/v1/tasks", input: "{}", "CONTENT_TYPE" => "text/plain"), 415, "unsupported_media_type"
    assert_error request("POST", "/api/v1/tasks", input: "{", "CONTENT_TYPE" => "application/json"), 400, "malformed_request"
    assert_error json_request("POST", "/api/v1/tasks", title: "ok", unknown: true), 422, "validation_failed"
    assert_error json_request("POST", "/api/v1/tasks", title: "ok", contexts: ["desk"]), 422, "validation_failed"
    assert_error json_request("PATCH", "/api/v1/tasks/#{FIX[:pr]}", {}, { "HTTP_IF_MATCH" => get("/api/v1/tasks/#{FIX[:pr]}")["etag"] }), 422, "validation_failed"

    huge = JSON.generate(title: "x" * Tasks::Api::App::BODY_LIMIT)
    assert_error request("POST", "/api/v1/tasks", input: huge, "CONTENT_TYPE" => "application/json"), 413, "payload_too_large"

    bad_host = request("GET", "/healthz", "HTTP_HOST" => "evil.example")
    assert_error bad_host, 400, "malformed_request"
    forwarded = request("GET", "/healthz", "HTTP_X_FORWARDED_HOST" => HOST)
    assert_error forwarded, 400, "malformed_request"
    origin = json_request("POST", "/api/v1/tasks", { title: "blocked" }, { "HTTP_ORIGIN" => "https://evil.example" })
    assert_error origin, 403, "forbidden_origin"
    allowed = json_request("POST", "/api/v1/tasks", { title: "allowed" }, { "HTTP_ORIGIN" => "http://#{HOST}" })
    assert_equal 201, allowed.status

    [bad_host, forwarded, origin, allowed].each { |response| refute_equal "*", response["access-control-allow-origin"] }
  end

  def test_unexpected_failures_and_logs_are_safe
    failure = Class.new do
      def read_status_result
        raise "secret title at /private/tasks.jsonl?token=abc"
      end
    end.new
    log = StringIO.new
    app = Tasks::Api::App.new(application: failure, port: 4747, logger: log)
    response = Rack::MockRequest.new(app).get("/readyz", "HTTP_HOST" => HOST)

    assert_error response, 503, "unavailable"
    refute_match(/secret|private|token|backtrace/i, response.body)
    refute_match(/secret|private|token|tasks\.jsonl/i, log.string)
    entry = JSON.parse(log.string)
    assert_equal %w[duration_ms event method request_id route status], entry.keys.sort
    assert_equal "/readyz", entry.fetch("route")
  end

  private

  def api_fixture
    records = FIXTURE_RECORDS.map(&:dup)
    records.find { |record| record["id"] == FIX[:flight] }["recur"] = ".+1w"
    records.find { |record| record["id"] == FIX[:plants] }["tags"] = %w[@home defer]
    pr_index = records.index { |record| record["id"] == FIX[:pr] }
    records.insert(
      pr_index + 1,
      { "type" => "task", "id" => "bbbb0001", "parent" => FIX[:pr], "state" => "TODO", "title" => "Child" },
      { "type" => "task", "id" => "bbbb0002", "parent" => "bbbb0001", "state" => "TODO", "title" => "Grandchild" }
    )
    Tasks::Format.dump(records)
  end

  def archive_fixture
    Tasks::Format.dump([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "cccc0001", "title" => "Archive" },
      { "type" => "task", "id" => FIX[:pr], "parent" => "cccc0001", "state" => "DONE",
        "title" => "Archived duplicate", "closed" => "2026-07-01" },
    ])
  end

  def get(path, env = {}) = request("GET", path, env)

  def json_request(method, path, body = nil, env = nil, **keyword_body)
    if body.nil?
      body = keyword_body
      env ||= {}
    end
    request(
      method, path,
      { "CONTENT_TYPE" => "application/json", input: JSON.generate(body) }.merge(env || {})
    )
  end

  def request(method, path, env = {})
    env = { "HTTP_HOST" => HOST }.merge(env)
    response = @request.request(method, path, env)
    response.instance_variable_set(:@tasks_contract_method, method)
    response.instance_variable_set(:@tasks_contract_path, path)
    response
  end

  def assert_response(response, status, payload)
    assert_equal status, response.status
    assert_equal payload, JSON.parse(response.body)
    assert_equal "application/json", response["content-type"]
    assert_match(/\Areq_[0-9a-f]+\z/, response["x-request-id"])
  end

  def assert_error(response, status, code)
    assert_equal status, response.status, response.body
    payload = JSON.parse(response.body)
    assert_equal code, payload.dig("error", "code")
    assert_kind_of String, payload.dig("error", "message")
    assert_kind_of Hash, payload.dig("error", "details")
    assert_match(/\Areq_[0-9a-f]+\z/, payload.dig("error", "request_id"))
    refute_match(/#{Regexp.escape(@dir)}/, response.body) if @dir
  end

  def assert_contract_response(response)
    method = response.instance_variable_get(:@tasks_contract_method)
    path = response.instance_variable_get(:@tasks_contract_path)
    contract_path = path.sub(%r{\A/api/v1}, "")
    request_env = { method: method }
    if %w[POST PATCH].include?(method)
      request_env[:input] = JSON.generate(method == "POST" ? { title: "Contract task" } : { title: "Contract update" })
      request_env["CONTENT_TYPE"] = "application/json"
    end
    request_env["HTTP_IF_MATCH"] = '"v1.opaque"' if %w[PATCH DELETE].include?(method)
    env = Rack::MockRequest.env_for(contract_path, request_env)
    rack_request = Rack::Request.new(env)
    validated_request = @definition.validate_request(rack_request)
    assert validated_request.valid?, "#{method} #{path} request: #{validated_request.error&.message}"
    rack_response = Rack::Response.new(
      response.body.empty? ? [] : [response.body], response.status, response.headers
    )
    validated = @definition.validate_response(rack_request, rack_response)
    assert validated&.valid?, "#{method} #{path}: #{validated&.error&.message || "not matched"}"
  end

  def quote(value) = %Q("#{value}")
end
