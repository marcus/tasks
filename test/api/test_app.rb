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
    # Capabilities advertise only what App#dispatch routes. `projects` is true
    # (the project routes are dispatched); undo/redo/archive_sweep/events stay
    # false until their endpoints are implemented; flipping any of those to true
    # without routing it (see App#meta) must fail here.
    assert_equal(
      { "projects" => true, "undo" => false, "redo" => false, "archive_sweep" => false, "events" => false },
      payload.dig("data", "capabilities")
    )
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

  def test_readiness_reports_a_typed_schema_migration_requirement
    records = Tasks::Format.parse(File.read(@org)).records.map(&:dup)
    records.first["version"] = 1
    File.write(@org, records.map { |record| JSON.generate(record) }.join("\n") + "\n")

    response = get("/readyz")
    assert_error response, 409, "schema_migration_required"
    details = JSON.parse(response.body).dig("error", "details")
    assert_equal 1, details.fetch("current_version")
    assert_equal 2, details.fetch("required_version")
    assert_equal "tasks migrate", details.fetch("command")
    assert_contract_response(response)

    tasks = get("/api/v1/tasks")
    assert_error tasks, 409, "schema_migration_required"
    assert_contract_response(tasks)
  end

  def test_list_supports_every_documented_filter_and_rejects_unknown_queries
    cases = {
      "scope=done" => [FIX[:old]],
      "scope=all&state=DONE" => [FIX[:old], FIX[:pr], "dddd0001"],
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
      archived availability_blocker_id availability_reason available available_at body child_count closed contexts
      deadline deadline_time deferred depth descendant_count id links parent_id priority project recurrence revision
      scheduled scheduled_time section_id source state tags title
    ]
    assert_equal expected_keys, task.keys.sort
    assert_equal "live", task.fetch("source")
    assert_nil task.fetch("parent_id"), "top-level tasks expose no task parent"
    assert_equal FIX[:work], task.fetch("section_id")
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
    assert_nil resource.fetch("parent_id")
    assert_equal ["Captured [#{Date.today.iso8601}].", "one", "two"], resource.fetch("body")
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
    child_resource = JSON.parse(child.body).fetch("data")
    assert_equal FIX[:pr], child_resource.fetch("parent_id")
    assert_equal 1, child_resource.fetch("depth")
    moved = json_request(
      "PATCH", "/api/v1/tasks/bbbb0001", { parent_id: nil },
      { "HTTP_IF_MATCH" => child["etag"] }
    )
    resource = JSON.parse(moved.body).fetch("data")
    assert_nil resource.fetch("parent_id")
    assert_equal FIX[:work], resource.fetch("section_id")
    assert_equal 0, resource.fetch("depth")

    # The child's ETag deliberately does not change when only an ancestor is
    # moved. A later unnest must therefore resolve the CURRENT enclosing
    # section under the Store lock, not reuse the adapter's earlier read.
    grandchild = get("/api/v1/tasks/bbbb0002")
    old_etag = grandchild["etag"]
    ancestor_snapshot = Tasks::Store.new(org: @org, archive: @archive).edit_snapshot("bbbb0001")
    ancestor_move = Tasks::Store.new(org: @org, archive: @archive).apply_changeset!(
      Tasks::TaskChangeset.from(ancestor_snapshot, changes: { location: FIX[:home] })
    )
    assert ancestor_move.ok?
    assert_equal old_etag, get("/api/v1/tasks/bbbb0002")["etag"]
    current_unnest = json_request(
      "PATCH", "/api/v1/tasks/bbbb0002", { parent_id: nil },
      { "HTTP_IF_MATCH" => old_etag }
    )
    assert_equal 200, current_unnest.status, current_unnest.body
    current_resource = JSON.parse(current_unnest.body).fetch("data")
    assert_nil current_resource.fetch("parent_id")
    assert_equal FIX[:home], current_resource.fetch("section_id")
  end

  def test_ordered_placement_accepts_before_null_and_omitted_anchors_from_one_fetch
    eval_loaded = get("/api/v1/tasks/#{FIX[:eval]}")
    travel_loaded = get("/api/v1/tasks/#{FIX[:travel]}")

    first = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:travel]}",
      { placement: { parent_id: FIX[:work], before_id: FIX[:flight] } },
      { "HTTP_IF_MATCH" => travel_loaded["etag"] }
    )
    assert_equal 200, first.status, first.body
    first_payload = JSON.parse(first.body)
    assert_equal FIX[:work], first_payload.dig("data", "section_id")
    assert_nil first_payload.dig("data", "parent_id")
    assert_equal first["etag"], quote(first_payload.dig("data", "revision"))
    assert_match(/\As1\.[0-9a-f]{64}\z/, first_payload.dig("meta", "store_revision"))
    assert_equal [FIX[:travel], FIX[:flight], FIX[:pr], FIX[:eval], FIX[:old]], work_task_ids
    assert_contract_response(first)

    # The first reorder churned every Work sibling's location fingerprint, but
    # an anchor-relative placement fetched before that churn still compares only
    # the moving task's own component.
    appended_with_null = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:eval]}",
      { placement: { parent_id: FIX[:work], before_id: nil } },
      { "HTTP_IF_MATCH" => eval_loaded["etag"] }
    )
    assert_equal 200, appended_with_null.status, appended_with_null.body
    assert_equal [FIX[:travel], FIX[:flight], FIX[:pr], FIX[:old], FIX[:eval]], work_task_ids
    assert_contract_response(appended_with_null)

    # Reusing the same pre-drag ETag is accepted again. Omitting before_id is
    # also append, and the already-satisfied placement is a successful no-op.
    appended_omitted = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:eval]}",
      { placement: { parent_id: FIX[:work] } },
      { "HTTP_IF_MATCH" => eval_loaded["etag"] }
    )
    assert_equal 200, appended_omitted.status, appended_omitted.body
    assert_equal appended_with_null["etag"], appended_omitted["etag"]
    assert_equal [FIX[:travel], FIX[:flight], FIX[:pr], FIX[:old], FIX[:eval]], work_task_ids
    assert_contract_response(appended_omitted)
    assert Tasks::Check.check(@org).ok?
  end

  def test_ordered_placement_moves_a_whole_subtree_across_sections_before_an_anchor
    loaded = get("/api/v1/tasks/#{FIX[:pr]}")
    moved = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:pr]}",
      { placement: { parent_id: FIX[:home], before_id: FIX[:plants] } },
      { "HTTP_IF_MATCH" => loaded["etag"] }
    )

    assert_equal 200, moved.status, moved.body
    payload = JSON.parse(moved.body)
    assert_nil payload.dig("data", "parent_id")
    assert_equal FIX[:home], payload.dig("data", "section_id")
    assert_equal 0, payload.dig("data", "depth")
    assert_equal moved["etag"], quote(payload.dig("data", "revision"))
    assert_match(/\As1\.[0-9a-f]{64}\z/, payload.dig("meta", "store_revision"))

    records = Tasks::Format.parse(File.read(@org, encoding: "UTF-8")).records
    home_index = records.index { |record| record["id"] == FIX[:home] }
    assert_equal [FIX[:home], FIX[:pr], "bbbb0001", "bbbb0002", FIX[:plants]],
                 records[home_index, 5].map { |record| record["id"] }
    assert_equal FIX[:pr], records.find { |record| record["id"] == "bbbb0001" }.fetch("parent")
    assert_equal "bbbb0001", records.find { |record| record["id"] == "bbbb0002" }.fetch("parent")
    assert_contract_response(moved)
    assert Tasks::Check.check(@org).ok?
  end

  def test_placement_rejects_malformed_and_mutually_exclusive_inputs
    current = get("/api/v1/tasks/#{FIX[:flight]}")
    cases = [
      [{ placement: nil }, "placement"],
      [{ placement: [] }, "placement"],
      [{ placement: {} }, "placement.parent_id"],
      [{ placement: { parent_id: nil } }, "placement.parent_id"],
      [{ placement: { parent_id: FIX[:work], before_id: "short" } }, "placement.before_id"],
      [{ placement: { parent_id: FIX[:work], after_id: FIX[:eval] } }, "placement"],
    ]
    cases.each do |body, field|
      response = json_request(
        "PATCH", "/api/v1/tasks/#{FIX[:flight]}", body,
        { "HTTP_IF_MATCH" => current["etag"] }
      )
      assert_error response, 422, "validation_failed"
      assert JSON.parse(response.body).dig("error", "details", "fields").key?(field), body.inspect
      assert_contract_request response, valid: false
    end

    exclusive = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:flight]}",
      { parent_id: FIX[:home], placement: { parent_id: FIX[:work] } },
      { "HTTP_IF_MATCH" => current["etag"] }
    )
    assert_error exclusive, 422, "validation_failed"
    exclusive_error = JSON.parse(exclusive.body).fetch("error")
    assert_equal "One or more fields are invalid.", exclusive_error.fetch("message")
    assert_equal %w[parent_id placement], exclusive_error.dig("details", "fields").keys.sort
    assert_contract_request exclusive, valid: false

    missing_precondition = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:flight]}",
      { placement: { parent_id: FIX[:work], before_id: FIX[:eval] } }
    )
    assert_error missing_precondition, 428, "missing_precondition"
  end

  def test_placement_maps_missing_subject_parent_and_anchor_with_safe_fields
    current = get("/api/v1/tasks/#{FIX[:flight]}")
    cases = [
      ["deadbeef", { parent_id: FIX[:work] }, "id", "deadbeef"],
      [FIX[:flight], { parent_id: "deadbeef" }, "placement.parent_id", "deadbeef"],
      [FIX[:flight], { parent_id: FIX[:work], before_id: "deadbeef" }, "placement.before_id", "deadbeef"],
      [FIX[:flight], { parent_id: FIX[:work], before_id: "dddd0001" }, "placement.before_id", "dddd0001"],
    ]
    cases.each do |id, placement, field, value|
      response = json_request(
        "PATCH", "/api/v1/tasks/#{id}", { placement: placement },
        { "HTTP_IF_MATCH" => current["etag"] }
      )
      assert_error response, 404, "not_found"
      details = JSON.parse(response.body).dig("error", "details")
      assert_equal field, details.fetch("field")
      assert_equal value, details.fetch(field == "id" ? "id" : field.split(".").last)
      assert_contract_response(response)
    end
  end

  def test_placement_maps_cycle_anchor_conflict_and_depth_refusals
    pr = get("/api/v1/tasks/#{FIX[:pr]}")
    parent_cycle = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:pr]}",
      { placement: { parent_id: "bbbb0001" } },
      { "HTTP_IF_MATCH" => pr["etag"] }
    )
    assert_error parent_cycle, 409, "cycle"
    assert_equal "bbbb0001", JSON.parse(parent_cycle.body).dig("error", "details", "parent_id")
    assert_contract_response(parent_cycle)

    anchor_cycle = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:pr]}",
      { placement: { parent_id: FIX[:work], before_id: "bbbb0002" } },
      { "HTTP_IF_MATCH" => pr["etag"] }
    )
    assert_error anchor_cycle, 409, "cycle"
    anchor_error = JSON.parse(anchor_cycle.body).fetch("error")
    assert_equal "bbbb0002", anchor_error.dig("details", "before_id")
    assert_equal "The placement parent or anchor cannot be the moving task or its descendant.",
                 anchor_error.fetch("message")
    assert_contract_response(anchor_cycle)

    flight = get("/api/v1/tasks/#{FIX[:flight]}")
    conflict = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:flight]}",
      { placement: { parent_id: FIX[:home], before_id: FIX[:eval] } },
      { "HTTP_IF_MATCH" => flight["etag"] }
    )
    assert_error conflict, 409, "conflict"
    assert_equal(
      { "parent_id" => FIX[:home], "before_id" => FIX[:eval], "current_parent_id" => FIX[:work] },
      JSON.parse(conflict.body).dig("error", "details")
    )
    assert_contract_response(conflict)

    add_deep_destination
    too_deep = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:pr]}",
      { placement: { parent_id: "dddd0003" } },
      { "HTTP_IF_MATCH" => pr["etag"] }
    )
    assert_error too_deep, 409, "too_deep"
    assert_equal 4, JSON.parse(too_deep.body).dig("error", "details", "max_depth")
    assert_contract_response(too_deep)
    assert Tasks::Check.check(@org).ok?
  end

  def test_placement_stales_on_own_edit_while_legacy_parent_move_still_guards_location
    flight = get("/api/v1/tasks/#{FIX[:flight]}")
    store = Tasks::Store.new(org: @org, archive: @archive)
    snapshot = store.edit_snapshot(FIX[:flight])
    edited = store.apply_changeset!(Tasks::TaskChangeset.from(snapshot, changes: { title: "CLI edited" }))
    assert edited.ok?

    missing_parent = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:flight]}",
      { placement: { parent_id: "deadbeef" } },
      { "HTTP_IF_MATCH" => flight["etag"] }
    )
    assert_error missing_parent, 404, "not_found"
    assert_equal(
      { "field" => "placement.parent_id", "parent_id" => "deadbeef" },
      JSON.parse(missing_parent.body).dig("error", "details")
    )
    assert_contract_response(missing_parent)

    missing_anchor = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:flight]}",
      { placement: { parent_id: FIX[:home], before_id: "deadbeef" } },
      { "HTTP_IF_MATCH" => flight["etag"] }
    )
    assert_error missing_anchor, 404, "not_found"
    assert_equal(
      { "field" => "placement.before_id", "before_id" => "deadbeef" },
      JSON.parse(missing_anchor.body).dig("error", "details")
    )
    assert_contract_response(missing_anchor)

    stale_placement = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:flight]}",
      { placement: { parent_id: FIX[:home] } },
      { "HTTP_IF_MATCH" => flight["etag"] }
    )
    assert_error stale_placement, 412, "stale_revision"
    assert_equal "CLI edited", JSON.parse(stale_placement.body).dig("error", "details", "current", "title")
    assert_contract_response(stale_placement)

    eval_loaded = get("/api/v1/tasks/#{FIX[:eval]}")
    travel_loaded = get("/api/v1/tasks/#{FIX[:travel]}")
    churn = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:travel]}",
      { placement: { parent_id: FIX[:work], before_id: FIX[:eval] } },
      { "HTTP_IF_MATCH" => travel_loaded["etag"] }
    )
    assert_equal 200, churn.status, churn.body

    legacy = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:eval]}", { parent_id: FIX[:home] },
      { "HTTP_IF_MATCH" => eval_loaded["etag"] }
    )
    assert_error legacy, 412, "stale_revision"
    assert_contract_response(legacy)
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
    unsupported = request("POST", "/api/v1/tasks", input: "{}", "CONTENT_TYPE" => "text/plain")
    assert_error unsupported, 415, "unsupported_media_type"
    assert_contract_request unsupported, valid: false
    malformed = request("POST", "/api/v1/tasks", input: "{", "CONTENT_TYPE" => "application/json")
    assert_error malformed, 400, "malformed_request"
    assert_contract_request malformed, valid: false
    unknown = json_request("POST", "/api/v1/tasks", title: "ok", unknown: true)
    assert_error unknown, 422, "validation_failed"
    assert_contract_request unknown, valid: false
    assert_error json_request("POST", "/api/v1/tasks", title: "ok", contexts: ["desk"]), 422, "validation_failed"
    empty_patch = json_request("PATCH", "/api/v1/tasks/#{FIX[:pr]}", {}, { "HTTP_IF_MATCH" => get("/api/v1/tasks/#{FIX[:pr]}")["etag"] })
    assert_error empty_patch, 422, "validation_failed"
    assert_contract_request empty_patch, valid: false

    huge = JSON.generate(title: "x" * Tasks::Api::App::BODY_LIMIT)
    huge_response = request("POST", "/api/v1/tasks", input: huge, "CONTENT_TYPE" => "application/json")
    assert_error huge_response, 413, "payload_too_large"
    assert_contract_response huge_response

    current = get("/api/v1/tasks/#{FIX[:pr]}")
    delete_media = request(
      "DELETE", "/api/v1/tasks/#{FIX[:pr]}",
      input: "not json", "CONTENT_TYPE" => "text/plain", "HTTP_IF_MATCH" => current["etag"]
    )
    assert_error delete_media, 415, "unsupported_media_type"
    assert_contract_response delete_media
    delete_huge = request(
      "DELETE", "/api/v1/tasks/#{FIX[:pr]}",
      input: "x" * (Tasks::Api::App::BODY_LIMIT + 1),
      "CONTENT_TYPE" => "application/json", "HTTP_IF_MATCH" => current["etag"]
    )
    assert_error delete_huge, 413, "payload_too_large"
    assert_contract_response delete_huge
    assert Tasks::Store.new(org: @org, archive: @archive).items.any? { |item| item.id == FIX[:pr] }

    bad_host = request("GET", "/healthz", "HTTP_HOST" => "evil.example")
    assert_error bad_host, 400, "malformed_request"
    forwarded = request("GET", "/healthz", "HTTP_X_FORWARDED_HOST" => HOST)
    assert_error forwarded, 400, "malformed_request"
    origin = json_request("POST", "/api/v1/tasks", { title: "blocked" }, { "HTTP_ORIGIN" => "https://evil.example" })
    assert_error origin, 403, "forbidden_origin"
    assert_contract_response origin
    allowed = json_request("POST", "/api/v1/tasks", { title: "allowed" }, { "HTTP_ORIGIN" => "http://#{HOST}" })
    assert_equal 201, allowed.status
    assert_contract_response allowed

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

  def test_temporal_create_and_patch_pair_semantics
    created = json_request(
      "POST", "/api/v1/tasks",
      { title: "Customer call", project: "Work", deadline: "2026-07-20",
        deadline_time: { local: "17:00", timezone: "Europe/London", fold: 0 } }
    )
    assert_equal 201, created.status, created.body
    task = JSON.parse(created.body).fetch("data")
    assert_equal "2026-07-20T16:00:00Z", task.dig("deadline_time", "instant")
    assert_equal "Europe/London", task.dig("deadline_time", "effective_timezone")
    assert_contract_request created, valid: true
    assert_contract_response created

    id = task.fetch("id")
    moved = json_request(
      "PATCH", "/api/v1/tasks/#{id}", { deadline: "2026-07-21" },
      { "HTTP_IF_MATCH" => created["etag"] }
    )
    assert_equal 200, moved.status, moved.body
    moved_task = JSON.parse(moved.body).fetch("data")
    assert_equal "2026-07-21", moved_task.fetch("deadline")
    assert_equal "17:00", moved_task.dig("deadline_time", "local"), "date-only PATCH preserves time intent"

    all_day = json_request(
      "PATCH", "/api/v1/tasks/#{id}", { deadline_time: nil },
      { "HTTP_IF_MATCH" => moved["etag"] }
    )
    assert_equal 200, all_day.status, all_day.body
    assert_nil JSON.parse(all_day.body).fetch("data").fetch("deadline_time")

    invalid = json_request(
      "PATCH", "/api/v1/tasks/#{id}",
      { deadline: nil, deadline_time: { local: "09:00" } },
      { "HTTP_IF_MATCH" => all_day["etag"] }
    )
    assert_error invalid, 422, "validation_failed"

    orphan = json_request(
      "PATCH", "/api/v1/tasks/#{id}", { scheduled_time: { local: "09:00" } },
      { "HTTP_IF_MATCH" => all_day["etag"] }
    )
    assert_error orphan, 422, "validation_failed"
  end

  def test_temporal_api_rejects_derived_fields_unknown_zones_and_dst_gaps
    current = get("/api/v1/tasks/#{FIX[:flight]}")
    bad_values = [
      { deadline_time: { local: "09:00", instant: "2026-07-20T09:00:00Z" } },
      { deadline_time: { local: "09:00", timezone: "PST" } },
      { deadline: "2026-03-08", deadline_time:
        { local: "02:30", timezone: "America/Los_Angeles" } },
    ]
    bad_values.each do |body|
      response = json_request(
        "PATCH", "/api/v1/tasks/#{FIX[:flight]}", body,
        { "HTTP_IF_MATCH" => current["etag"] }
      )
      assert_error response, 422, "validation_failed"
    end
  end

  def test_patching_only_the_date_onto_a_dst_gap_is_a_field_error_not_a_server_error
    current = get("/api/v1/tasks/#{FIX[:flight]}")
    seeded = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:flight]}",
      { deadline: "2026-03-01",
        deadline_time: { local: "02:30", timezone: "America/Los_Angeles" } },
      { "HTTP_IF_MATCH" => current["etag"] }
    )
    assert_equal 200, seeded.status

    fresh = get("/api/v1/tasks/#{FIX[:flight]}")
    # Moving only the date preserves 02:30 America/Los_Angeles, which does not
    # exist on the spring-forward date — a client-input 422, never a 503.
    response = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:flight]}", { deadline: "2026-03-08" },
      { "HTTP_IF_MATCH" => fresh["etag"] }
    )
    assert_error response, 422, "validation_failed"
  end

  def test_api_returns_a_safe_error_when_configured_zone_creates_a_floating_gap
    records = Tasks::Format.parse(File.read(@org)).records
    task = records.find { |record| record["id"] == FIX[:flight] }
    task["deadline"] = "2026-03-08"
    task["deadline_time"] = { "local" => "02:30" }
    File.write(@org, Tasks::Format.dump(records))
    application = Tasks::Application.new(
      store_factory: Tasks::StoreFactory.new(org: @org, archive: @archive),
      temporal_context_factory: -> {
        Tasks::TemporalContext.new(
          now: Time.utc(2026, 3, 8, 9), timezone: "America/Los_Angeles"
        )
      }
    )
    app = Tasks::Api::App.new(
      application: application, timezone: "America/Los_Angeles", logger: StringIO.new
    )

    response = Rack::MockRequest.new(app).get(
      "/api/v1/tasks", "HTTP_HOST" => HOST
    )

    assert_error response, 503, "store_invalid"
    assert_match(/first valid time is 03:00/, response.body)
    refute_match(/lib\/tasks|tasks\.jsonl/, response.body)
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
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "cccc0001", "title" => "Archive" },
      { "type" => "task", "id" => FIX[:pr], "parent" => "cccc0001", "state" => "DONE",
        "title" => "Archived duplicate", "closed" => "2026-07-01" },
      { "type" => "task", "id" => "dddd0001", "parent" => "cccc0001", "state" => "DONE",
        "title" => "Archived anchor", "closed" => "2026-07-02" },
    ])
  end

  def work_task_ids
    Tasks::Format.parse(File.read(@org, encoding: "UTF-8")).records.filter_map do |record|
      record["id"] if record["type"] == "task" && record["parent"] == FIX[:work]
    end
  end

  def add_deep_destination
    records = Tasks::Format.parse(File.read(@org, encoding: "UTF-8")).records
    records.concat([
      { "type" => "task", "id" => "dddd0002", "parent" => FIX[:plants], "state" => "TODO",
        "title" => "Deep child" },
      { "type" => "task", "id" => "dddd0003", "parent" => "dddd0002", "state" => "TODO",
        "title" => "Deep grandchild" },
    ])
    File.write(@org, Tasks::Format.dump(records))
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
    contract_request = {
      method: method,
      path: path,
      input: (env[:input] || env["input"])&.dup,
      content_type: env["CONTENT_TYPE"],
      if_match: env["HTTP_IF_MATCH"],
    }
    response = @request.request(method, path, env)
    response.instance_variable_set(:@tasks_contract_request, contract_request)
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
    request_data, rack_request = contract_request_for(response)
    assert_contract_request response, valid: true
    rack_response = Rack::Response.new(
      response.body.empty? ? [] : [response.body], response.status, response.headers
    )
    validated = @definition.validate_response(rack_request, rack_response)
    assert validated&.valid?, "#{request_data[:method]} #{request_data[:path]}: #{validated&.error&.message || "not matched"}"
  end

  def assert_contract_request(response, valid:)
    request_data, rack_request = contract_request_for(response)
    validated = @definition.validate_request(rack_request)
    if valid
      assert validated.valid?, "#{request_data[:method]} #{request_data[:path]} request: #{validated.error&.message}"
    else
      refute validated.valid?, "#{request_data[:method]} #{request_data[:path]} unexpectedly matched the contract"
    end
  end

  def contract_request_for(response)
    data = response.instance_variable_get(:@tasks_contract_request)
    path = data.fetch(:path).sub(%r{\A/api/v1}, "")
    env = { method: data.fetch(:method) }
    env[:input] = data[:input] unless data[:input].nil?
    env["CONTENT_TYPE"] = data[:content_type] if data[:content_type]
    env["HTTP_IF_MATCH"] = data[:if_match] if data[:if_match]
    [data, Rack::Request.new(Rack::MockRequest.env_for(path, env))]
  end

  def quote(value) = %Q("#{value}")
end
