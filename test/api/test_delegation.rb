# frozen_string_literal: true

require "json"
require "open3"
require "openapi_first"
require "rack/mock"
require "rack/request"
require "rack/response"
require "stringio"
require "tmpdir"

require_relative "../test_helper"
require "tasks/api/app"
require "tasks/task_queries"

# HTTP parity for task delegation (plan: agent-task-delegation, phase 3).
#
# The API is an adapter, so these tests care about three things the adapter
# owns: that the resource and the two read scopes say exactly what the CLI says,
# that each action maps to the right status code and precondition, and that a
# lost claim race is distinguishable from a stale ETag without parsing prose.
# Domain behavior itself (eligibility, the claim compare-and-set, worker
# matching, WAITING) is proven once in test_delegation.rb / test_application.rb.
class TestApiDelegation < Minitest::Test
  ROOT = File.expand_path("../..", __dir__)
  CONTRACT = File.join(ROOT, "docs/api/openapi.yaml")
  TASKS_BIN = File.join(ROOT, "bin/tasks")
  HOST = "127.0.0.1:4747"
  WORKER_A = "claude-code/claude-fable-5/aaaa1111"
  WORKER_B = "claude-code/claude-opus-5/bbbb2222"

  def setup
    @dir = Dir.mktmpdir("tasks-api-delegation")
    @org = File.join(@dir, "tasks.jsonl")
    @archive = File.join(@dir, "archive.jsonl")
    File.write(@org, FIXTURE)
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

  # -- representation --------------------------------------------------------

  def test_task_resource_carries_the_stored_delegation_verbatim
    plain = get("/api/v1/tasks/#{FIX[:garden]}")
    assert_equal 200, plain.status
    assert_nil JSON.parse(plain.body).dig("data", "delegation"),
               "an undelegated task reports null, not an empty object"
    assert_contract_response(plain)

    delegate(FIX[:pr], kind: "agent", mode: "research")
    claim(FIX[:pr], worker: WORKER_A)
    work_ref(FIX[:pr], work_ref: "https://example.com/brief", worker: WORKER_A)

    response = get("/api/v1/tasks/#{FIX[:pr]}")
    delegation = JSON.parse(response.body).dig("data", "delegation")
    # Fixed emission order, absent keys omitted entirely — the same bytes the
    # store writes, so a client diffing two responses sees one stable shape.
    assert_equal %w[kind mode status assignee at work_ref], delegation.keys
    assert_equal "agent", delegation.fetch("kind")
    assert_equal "research", delegation.fetch("mode")
    assert_equal "claimed", delegation.fetch("status")
    assert_equal WORKER_A, delegation.fetch("assignee")
    assert_match(/\A\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\z/, delegation.fetch("at"))
    assert_equal "https://example.com/brief", delegation.fetch("work_ref")
    assert_equal delegation, stored_delegation(FIX[:pr])
    assert_contract_response(response)

    human = get("/api/v1/tasks/#{FIX[:travel]}")
    delegate(FIX[:travel], kind: "human", assignee: "pat@example.com")
    human = JSON.parse(get("/api/v1/tasks/#{FIX[:travel]}").body).dig("data", "delegation")
    assert_equal %w[kind status assignee at], human.keys, "mode is omitted for a human hand-off"
  end

  def test_resource_and_scopes_match_the_cli_json_byte_for_byte
    delegate(FIX[:pr], kind: "agent", mode: "implement")
    delegate(FIX[:plants], kind: "agent", mode: "refine")
    delegate(FIX[:travel], kind: "human", assignee: "pat@example.com")
    claim(FIX[:plants], worker: WORKER_B)

    api = JSON.parse(get("/api/v1/tasks/#{FIX[:plants]}").body).dig("data", "delegation")
    cli = JSON.parse(cli!("show", FIX[:plants], "--json")).fetch("delegation")
    assert_equal cli, api, "`tasks show --json` and the task resource spell one delegation"

    %w[delegated agent_ready].each do |scope|
      flag = scope == "delegated" ? "--delegated" : "--agent-ready"
      api_ids = list_ids("/api/v1/tasks?scope=#{scope}")
      cli_ids = JSON.parse(cli!("list", flag, "--json")).map { |row| row.fetch("id") }
      assert_equal cli_ids, api_ids, "scope=#{scope} mirrors `tasks list #{flag}`"
    end
  end

  def test_metadata_publishes_the_delegation_vocabularies
    response = get("/api/v1/meta")
    data = JSON.parse(response.body).fetch("data")
    assert_equal %w[human agent], data.fetch("delegation_kinds")
    assert_equal %w[refine research implement], data.fetch("delegation_modes")
    assert_equal %w[delegated ready claimed], data.fetch("delegation_statuses")
    # The vocabularies are the domain's, not a second copy maintained here.
    assert_equal Tasks::Delegation::KINDS, data.fetch("delegation_kinds")
    assert_equal Tasks::Delegation::MODES, data.fetch("delegation_modes")
    assert_equal Tasks::Delegation::STATUSES, data.fetch("delegation_statuses")
    assert_contract_response(response)
  end

  # -- read scopes -----------------------------------------------------------

  def test_delegation_scopes_select_and_rank_like_the_cli_filters
    delegate(FIX[:pr], kind: "agent", mode: "implement")       # priority B
    delegate(FIX[:garden], kind: "agent", mode: "refine")      # no priority, first in file
    delegate(FIX[:plants], kind: "agent", mode: "research")    # no priority, last in file
    delegate(FIX[:travel], kind: "human", assignee: "pat@example.com")

    ready = get("/api/v1/tasks?scope=agent_ready")
    assert_equal 200, ready.status
    # Priority first, then the soonest date, then canonical file order — so the
    # B-priority task outranks two unprioritised tasks that precede it on disk.
    assert_equal [FIX[:pr], FIX[:garden], FIX[:plants]],
                 JSON.parse(ready.body).fetch("data").map { |row| row.fetch("id") }
    assert_contract_response(ready)

    delegated = get("/api/v1/tasks?scope=delegated")
    assert_equal 200, delegated.status
    assert_equal [FIX[:garden], FIX[:pr], FIX[:travel], FIX[:plants]],
                 JSON.parse(delegated.body).fetch("data").map { |row| row.fetch("id") },
                 "every marker, human included, in canonical order"
    assert_contract_response(delegated)

    # A claim removes the task from the claimable queue but not from the
    # owner's hand-off list: a list read never grants ownership, and the owner
    # still needs to see who is holding what.
    claim(FIX[:pr], worker: WORKER_A)
    refute_includes list_ids("/api/v1/tasks?scope=agent_ready"), FIX[:pr]
    assert_includes list_ids("/api/v1/tasks?scope=delegated"), FIX[:pr]

    # Ordinary filters still compose with both scopes.
    assert_equal [FIX[:plants]], list_ids("/api/v1/tasks?scope=agent_ready&context=home")
    assert_equal [FIX[:pr]], list_ids("/api/v1/tasks?scope=delegated&priority=B")
    assert_equal [], list_ids("/api/v1/tasks?scope=agent_ready&priority=A")

    assert_error get("/api/v1/tasks?scope=delegated&available=false"), 422, "validation_failed"
    assert_error get("/api/v1/tasks?scope=claimed"), 422, "validation_failed"
  end

  def test_agent_ready_excludes_human_proposed_closed_and_unavailable_work
    proposed = create_task(title: "Proposed idea", state: "PROPOSED")
    deferred = create_task(title: "Held work", deferred: true)
    delegate(FIX[:travel], kind: "human", assignee: "pat@example.com")
    delegate(deferred.fetch("id"), kind: "agent", mode: "research")
    delegate(FIX[:pr], kind: "agent", mode: "research")

    # A PROPOSED task refuses delegation outright, so it can never reach the
    # queue by any route.
    refusal = json_request(
      "POST", "/api/v1/tasks/#{proposed.fetch("id")}/delegate",
      { kind: "agent", mode: "research" }, if_match(proposed.fetch("id"))
    )
    assert_error refusal, 422, "validation_failed"

    assert_equal [FIX[:pr]], list_ids("/api/v1/tasks?scope=agent_ready")
    # Both scopes are refinements of the default open-live collection, so an
    # effectively unavailable task is out of both — exactly what `tasks list
    # --delegated` does. Its marker is still readable on the task itself.
    delegated_ids = list_ids("/api/v1/tasks?scope=delegated")
    assert_equal [FIX[:pr], FIX[:travel]], delegated_ids
    assert_equal JSON.parse(cli!("list", "--delegated", "--json")).map { |row| row.fetch("id") },
                 delegated_ids
    assert_equal "research",
                 JSON.parse(get("/api/v1/tasks/#{deferred.fetch("id")}").body)
                     .dig("data", "delegation", "mode")

    # Closing a claimed task keeps the marker as provenance, and the queue
    # drops it because it is no longer live open work.
    claim(FIX[:pr], worker: WORKER_A)
    closed = patch_task(FIX[:pr], state: "DONE")
    assert_equal "claimed", closed.dig("delegation", "status")
    assert_equal WORKER_A, closed.dig("delegation", "assignee")
    assert_equal [], list_ids("/api/v1/tasks?scope=agent_ready")
  end

  # -- actions ---------------------------------------------------------------

  def test_human_delegation_moves_the_task_to_waiting_unless_keep_state
    response = json_request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/delegate",
      { kind: "human", assignee: "pat@example.com" }, if_match(FIX[:pr])
    )
    assert_equal 200, response.status, response.body
    task = JSON.parse(response.body).fetch("data")
    assert_equal "WAITING", task.fetch("state")
    assert_equal({ "kind" => "human", "status" => "delegated",
                   "assignee" => "pat@example.com", "at" => task.dig("delegation", "at") },
                 task.fetch("delegation"))
    assert_equal quote(task.fetch("revision")), response["etag"]
    assert_contract_response(response)

    kept = json_request(
      "POST", "/api/v1/tasks/#{FIX[:eval]}/delegate",
      { kind: "human", assignee: "sam@example.com", keep_state: true }, if_match(FIX[:eval])
    )
    assert_equal 200, kept.status, kept.body
    assert_equal "TODO", JSON.parse(kept.body).dig("data", "state")
    assert_contract_response(kept)

    # Undelegating leaves the lifecycle state alone; the owner decides.
    cleared = request("POST", "/api/v1/tasks/#{FIX[:pr]}/undelegate", if_match(FIX[:pr]))
    assert_equal 200, cleared.status, cleared.body
    assert_nil JSON.parse(cleared.body).dig("data", "delegation")
    assert_equal "WAITING", JSON.parse(cleared.body).dig("data", "state")
    assert_contract_response(cleared)
  end

  def test_agent_round_trip_returns_the_canonical_resource_and_a_fresh_etag
    delegated = json_request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/delegate",
      { kind: "agent", mode: "research" }, if_match(FIX[:pr])
    )
    assert_equal 200, delegated.status, delegated.body
    task = JSON.parse(delegated.body).fetch("data")
    assert_equal "NEXT", task.fetch("state"), "agent delegation never moves lifecycle state"
    assert_equal "ready", task.dig("delegation", "status")
    refute task.fetch("delegation").key?("assignee"), "a ready marker names no worker"
    assert_contract_response(delegated)

    # A mode update on ready work is allowed and replaces the mode in place.
    remoded = json_request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/delegate",
      { kind: "agent", mode: "implement" }, { "HTTP_IF_MATCH" => delegated["etag"] }
    )
    assert_equal 200, remoded.status, remoded.body
    assert_equal "implement", JSON.parse(remoded.body).dig("data", "delegation", "mode")

    claimed = json_request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/claim",
      { worker: WORKER_A }, { "HTTP_IF_MATCH" => remoded["etag"] }
    )
    assert_equal 200, claimed.status, claimed.body
    claimed_task = JSON.parse(claimed.body).fetch("data")
    assert_equal "claimed", claimed_task.dig("delegation", "status")
    assert_equal WORKER_A, claimed_task.dig("delegation", "assignee")
    # The whole resource, so a worker claims and reads the authority it works
    # under in one request.
    assert_equal "implement", claimed_task.dig("delegation", "mode")
    assert_equal "Review PR backlog", claimed_task.fetch("title")
    assert_equal quote(claimed_task.fetch("revision")), claimed["etag"]
    refute_equal remoded["etag"], claimed["etag"], "a claim advances the task's own revision"
    assert_contract_response(claimed)

    released = json_request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/release",
      { worker: WORKER_A, note: "Blocked: needs the vendor key." },
      { "HTTP_IF_MATCH" => claimed["etag"] }
    )
    assert_equal 200, released.status, released.body
    released_task = JSON.parse(released.body).fetch("data")
    assert_equal "ready", released_task.dig("delegation", "status")
    refute released_task.fetch("delegation").key?("assignee")
    assert_includes released_task.fetch("body"), "Blocked: needs the vendor key."
    assert_contract_response(released)

    # Undelegate is idempotent: the repeat writes nothing and still answers with
    # the current resource rather than a bare 204 or a 404.
    first = request("POST", "/api/v1/tasks/#{FIX[:pr]}/undelegate", if_match(FIX[:pr]))
    assert_equal 200, first.status, first.body
    repeat = request("POST", "/api/v1/tasks/#{FIX[:pr]}/undelegate", if_match(FIX[:pr]))
    assert_equal 200, repeat.status, repeat.body
    assert_nil JSON.parse(repeat.body).dig("data", "delegation")
    assert_equal quote(JSON.parse(repeat.body).dig("data", "revision")), repeat["etag"]
    assert_contract_response(repeat)
  end

  def test_owner_force_release_and_revocation_defeat_a_stale_worker
    delegate(FIX[:pr], kind: "agent", mode: "research")
    claim(FIX[:pr], worker: WORKER_A)

    forced = json_request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/release", { force: true }, if_match(FIX[:pr])
    )
    assert_equal 200, forced.status, forced.body
    assert_equal "ready", JSON.parse(forced.body).dig("data", "delegation", "status")
    assert_contract_response(forced)

    claim(FIX[:pr], worker: WORKER_B)
    request("POST", "/api/v1/tasks/#{FIX[:pr]}/undelegate", if_match(FIX[:pr]))

    # Revocation wins: the stale worker's next worker-matched write fails its
    # precondition rather than resurrecting the marker.
    stale_worker = json_request(
      "PUT", "/api/v1/tasks/#{FIX[:pr]}/work_ref",
      { work_ref: "https://example.com/late", worker: WORKER_B }, if_match(FIX[:pr])
    )
    assert_error stale_worker, 422, "validation_failed"
    assert_match(/not delegated/, JSON.parse(stale_worker.body).dig("error", "message"))
    assert_contract_response(stale_worker)
  end

  def test_work_ref_is_replaced_cleared_and_gated_on_a_matching_claim
    delegate(FIX[:pr], kind: "agent", mode: "implement")
    claim(FIX[:pr], worker: WORKER_A)

    set = json_request(
      "PUT", "/api/v1/tasks/#{FIX[:pr]}/work_ref",
      { work_ref: "https://github.com/acme/x/pull/42", worker: WORKER_A }, if_match(FIX[:pr])
    )
    assert_equal 200, set.status, set.body
    task = JSON.parse(set.body).fetch("data")
    assert_equal "https://github.com/acme/x/pull/42", task.dig("delegation", "work_ref")
    assert_equal quote(task.fetch("revision")), set["etag"]
    assert_contract_response(set)

    # One reference: the owner may always overwrite it.
    replaced = json_request(
      "PUT", "/api/v1/tasks/#{FIX[:pr]}/work_ref",
      { work_ref: "https://github.com/acme/x/pull/43" }, if_match(FIX[:pr])
    )
    assert_equal "https://github.com/acme/x/pull/43",
                 JSON.parse(replaced.body).dig("data", "delegation", "work_ref")

    # A worker whose id does not match the live claim is a lost race, not a
    # validation problem.
    intruder = json_request(
      "PUT", "/api/v1/tasks/#{FIX[:pr]}/work_ref",
      { work_ref: "https://example.com/theirs", worker: WORKER_B }, if_match(FIX[:pr])
    )
    assert_error intruder, 409, "claim_conflict"
    assert_equal WORKER_A, JSON.parse(intruder.body).dig("error", "details", "holder")
    assert_contract_response(intruder)

    cleared = json_request(
      "PUT", "/api/v1/tasks/#{FIX[:pr]}/work_ref", { work_ref: nil }, if_match(FIX[:pr])
    )
    assert_equal 200, cleared.status, cleared.body
    refute JSON.parse(cleared.body).dig("data", "delegation").key?("work_ref")
    assert_contract_response(cleared)

    # An idempotent repeat writes nothing and still returns the resource.
    repeat = json_request(
      "PUT", "/api/v1/tasks/#{FIX[:pr]}/work_ref", { work_ref: nil }, if_match(FIX[:pr])
    )
    assert_equal 200, repeat.status, repeat.body
    assert_equal quote(JSON.parse(repeat.body).dig("data", "revision")), repeat["etag"]

    undelegated = json_request(
      "PUT", "/api/v1/tasks/#{FIX[:eval]}/work_ref",
      { work_ref: "https://example.com/nope" }, if_match(FIX[:eval])
    )
    assert_error undelegated, 422, "validation_failed"
    assert_contract_response(undelegated)
  end

  # -- concurrency -----------------------------------------------------------

  def test_lost_claim_race_is_409_while_a_stale_precondition_is_412
    delegate(FIX[:pr], kind: "agent", mode: "research")
    shared = get("/api/v1/tasks/#{FIX[:pr]}")["etag"]

    winner = json_request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/claim", { worker: WORKER_A },
      { "HTTP_IF_MATCH" => shared }
    )
    assert_equal 200, winner.status, winner.body
    at = JSON.parse(winner.body).dig("data", "delegation", "at")

    # Same starting ETag: the loser's view of the task is out of date, so the
    # precondition is what fails. Refetch and decide again.
    stale = json_request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/claim", { worker: WORKER_B },
      { "HTTP_IF_MATCH" => shared }
    )
    assert_error stale, 412, "stale_revision"
    assert_equal "claimed",
                 JSON.parse(stale.body).dig("error", "details", "current", "delegation", "status")
    assert_equal winner["etag"], stale["etag"]
    assert_contract_response(stale)

    # Current ETag, but the task is simply taken: a distinct, machine-readable
    # conflict naming the holder and when it took the task.
    lost = json_request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/claim", { worker: WORKER_B }, if_match(FIX[:pr])
    )
    assert_error lost, 409, "claim_conflict"
    details = JSON.parse(lost.body).dig("error", "details")
    assert_equal "claim", details.fetch("action")
    assert_equal WORKER_A, details.fetch("holder")
    assert_equal at, details.fetch("at")
    assert_equal FIX[:pr], details.fetch("id")
    assert_contract_response(lost)

    # A claim is granted once and never re-granted, even to its own holder.
    again = json_request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/claim", { worker: WORKER_A }, if_match(FIX[:pr])
    )
    assert_error again, 409, "claim_conflict"

    # And the marker is untouched by either loser.
    assert_equal WORKER_A, stored_delegation(FIX[:pr]).fetch("assignee")
  end

  def test_every_action_requires_a_well_formed_if_match
    delegate(FIX[:pr], kind: "agent", mode: "research")
    stale = get("/api/v1/tasks/#{FIX[:pr]}")["etag"]
    claim(FIX[:pr], worker: WORKER_A)

    delegation_actions.each do |method, path, body|
      missing = body ? json_request(method, path, body) : request(method, path)
      assert_error missing, 428, "missing_precondition"
      assert_contract_request missing, valid: false

      malformed = if body
                    json_request(method, path, body, { "HTTP_IF_MATCH" => "not-an-etag" })
                  else
                    request(method, path, "HTTP_IF_MATCH" => "not-an-etag")
                  end
      assert_error malformed, 422, "validation_failed"

      outdated = if body
                   json_request(method, path, body, { "HTTP_IF_MATCH" => stale })
                 else
                   request(method, path, "HTTP_IF_MATCH" => stale)
                 end
      assert_error outdated, 412, "stale_revision"
      assert_contract_response(outdated)
    end
  end

  # -- refusals and validation ----------------------------------------------

  def test_delegation_refuses_proposed_and_closed_tasks
    proposed = create_task(title: "Maybe worth doing", state: "PROPOSED").fetch("id")

    refused = json_request(
      "POST", "/api/v1/tasks/#{proposed}/delegate",
      { kind: "agent", mode: "research" }, if_match(proposed)
    )
    assert_error refused, 422, "validation_failed"
    assert_match(/task is PROPOSED/, JSON.parse(refused.body).dig("error", "message"))
    assert_contract_response(refused)

    closed = json_request(
      "POST", "/api/v1/tasks/#{FIX[:old]}/delegate",
      { kind: "human", assignee: "pat@example.com" }, if_match(FIX[:old])
    )
    assert_error closed, 422, "validation_failed"
    assert_match(/task is DONE/, JSON.parse(closed.body).dig("error", "message"))
    assert_contract_response(closed)

    missing = json_request(
      "POST", "/api/v1/tasks/00000000/delegate",
      { kind: "agent", mode: "research" }, if_match(FIX[:pr])
    )
    assert_error missing, 404, "not_found"
    assert_contract_response(missing)

    assert_nil stored_delegation(proposed)
    assert_nil stored_delegation(FIX[:old])
  end

  def test_action_bodies_are_validated_before_anything_is_written
    delegate(FIX[:plants], kind: "agent", mode: "research")

    # Cross-field rules belong to the domain and are reported with the message
    # that names them.
    semantic = {
      { kind: "human", assignee: "pat at example.com" } => /must be an email address/,
      { kind: "human", mode: "research", assignee: "pat@example.com" } => /has no mode/,
      { kind: "agent" } => %r{refine/research/implement},
      { kind: "agent", mode: "research", assignee: "pat@example.com" } => /claimed by a worker/,
    }
    semantic.each do |body, message|
      response = json_request("POST", "/api/v1/tasks/#{FIX[:pr]}/delegate", body, if_match(FIX[:pr]))
      assert_error response, 422, "validation_failed"
      assert_match message, JSON.parse(response.body).dig("error", "message"), body.inspect
      assert_contract_response(response)
    end

    # Wrong-typed or unknown fields are the adapter's own refusals.
    adapter = [
      ["POST", "/api/v1/tasks/#{FIX[:pr]}/delegate", { kind: "robot" }],
      ["POST", "/api/v1/tasks/#{FIX[:pr]}/delegate", {}],
      ["POST", "/api/v1/tasks/#{FIX[:pr]}/delegate", { kind: "human", assignee: "pat@example.com", keep_state: "yes" }],
      ["POST", "/api/v1/tasks/#{FIX[:pr]}/delegate", { kind: "agent", mode: "research", urgency: "high" }],
      ["POST", "/api/v1/tasks/#{FIX[:plants]}/claim", {}],
      ["POST", "/api/v1/tasks/#{FIX[:plants]}/claim", { worker: "   " }],
      ["POST", "/api/v1/tasks/#{FIX[:plants]}/claim", { worker: WORKER_A, force: true }],
      ["POST", "/api/v1/tasks/#{FIX[:plants]}/release", {}],
      ["POST", "/api/v1/tasks/#{FIX[:plants]}/release", { force: "yes" }],
      ["PUT", "/api/v1/tasks/#{FIX[:plants]}/work_ref", {}],
      ["PUT", "/api/v1/tasks/#{FIX[:plants]}/work_ref", { work_ref: "" }],
      ["PUT", "/api/v1/tasks/#{FIX[:plants]}/work_ref", { work_ref: "one\ntwo" }],
    ]
    adapter.each do |method, path, body|
      id = path.split("/")[4]
      response = json_request(method, path, body, if_match(id))
      assert_error response, 422, "validation_failed", context: [method, path, body].inspect
    end

    # An invalid mode is refused by the published contract as well as the server.
    bad_mode = json_request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/delegate",
      { kind: "agent", mode: "supervise" }, if_match(FIX[:pr])
    )
    assert_error bad_mode, 422, "validation_failed"
    assert_contract_response(bad_mode, request_valid: false)

    # An undelegate carries no body at all.
    bodied = json_request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/undelegate", { worker: WORKER_A }, if_match(FIX[:pr])
    )
    assert_error bodied, 400, "malformed_request"

    assert_nil stored_delegation(FIX[:pr]), "no refused request wrote a marker"
    assert_equal "ready", stored_delegation(FIX[:plants]).fetch("status")
  end

  # -- transport policy ------------------------------------------------------

  def test_delegation_endpoints_keep_the_host_and_origin_mutation_policy
    delegate(FIX[:pr], kind: "agent", mode: "research")

    delegation_actions.each do |method, path, body|
      env = { "HTTP_ORIGIN" => "https://evil.example" }.merge(if_match(FIX[:pr]))
      blocked = body ? json_request(method, path, body, env) : request(method, path, env)
      assert_error blocked, 403, "forbidden_origin", context: "#{method} #{path}"
      assert_contract_response(blocked)

      host = { "HTTP_HOST" => "evil.example" }.merge(if_match(FIX[:pr]))
      bad_host = body ? json_request(method, path, body, host) : request(method, path, host)
      assert_error bad_host, 400, "malformed_request", context: "#{method} #{path}"

      forwarded = { "HTTP_X_FORWARDED_HOST" => HOST }.merge(if_match(FIX[:pr]))
      proxied = body ? json_request(method, path, body, forwarded) : request(method, path, forwarded)
      assert_error proxied, 400, "malformed_request", context: "#{method} #{path}"
    end

    # The allowed loopback origin still passes, so the policy is a filter and
    # not a blanket refusal of the new verbs.
    allowed = json_request(
      "PUT", "/api/v1/tasks/#{FIX[:pr]}/work_ref",
      { work_ref: "https://example.com/brief" },
      { "HTTP_ORIGIN" => "http://#{HOST}" }.merge(if_match(FIX[:pr]))
    )
    assert_equal 200, allowed.status, allowed.body

    assert_equal "ready", stored_delegation(FIX[:pr]).fetch("status")
  end

  def test_unrouted_delegation_verbs_and_media_types_are_refused
    delegate(FIX[:pr], kind: "agent", mode: "research")

    assert_error request("GET", "/api/v1/tasks/#{FIX[:pr]}/claim"), 404, "not_found"
    assert_error request("POST", "/api/v1/tasks/#{FIX[:pr]}/work_ref", if_match(FIX[:pr])),
                 404, "not_found"
    assert_error request("PUT", "/api/v1/tasks/#{FIX[:pr]}/delegate", if_match(FIX[:pr])),
                 404, "not_found"
    assert_error request("POST", "/api/v1/tasks/nope/claim"), 400, "malformed_request"

    text = request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/claim",
      { "CONTENT_TYPE" => "text/plain", input: "worker=x" }.merge(if_match(FIX[:pr]))
    )
    assert_error text, 415, "unsupported_media_type"

    broken = request(
      "POST", "/api/v1/tasks/#{FIX[:pr]}/claim",
      { "CONTENT_TYPE" => "application/json", input: "{" }.merge(if_match(FIX[:pr]))
    )
    assert_error broken, 400, "malformed_request"

    assert_error get("/api/v1/tasks?scope=agent_ready&unknown=1"), 422, "validation_failed"
  end

  private

  # Every mutating delegation route, with a body that would succeed against a
  # ready, unclaimed task. Used by the precondition and transport-policy tests
  # so a route added later cannot skip either gate.
  def delegation_actions
    [
      ["POST", "/api/v1/tasks/#{FIX[:pr]}/delegate", { kind: "agent", mode: "implement" }],
      ["POST", "/api/v1/tasks/#{FIX[:pr]}/undelegate", nil],
      ["POST", "/api/v1/tasks/#{FIX[:pr]}/claim", { worker: WORKER_B }],
      ["POST", "/api/v1/tasks/#{FIX[:pr]}/release", { force: true }],
      ["PUT", "/api/v1/tasks/#{FIX[:pr]}/work_ref", { work_ref: "https://example.com/x" }],
    ]
  end

  def delegate(id, **body)
    response = json_request("POST", "/api/v1/tasks/#{id}/delegate", body, if_match(id))
    assert_equal 200, response.status, response.body
    JSON.parse(response.body).fetch("data")
  end

  def claim(id, worker:)
    response = json_request("POST", "/api/v1/tasks/#{id}/claim", { worker: worker }, if_match(id))
    assert_equal 200, response.status, response.body
    JSON.parse(response.body).fetch("data")
  end

  def work_ref(id, **body)
    response = json_request("PUT", "/api/v1/tasks/#{id}/work_ref", body, if_match(id))
    assert_equal 200, response.status, response.body
    JSON.parse(response.body).fetch("data")
  end

  def create_task(**attributes)
    response = json_request("POST", "/api/v1/tasks", attributes)
    assert_equal 201, response.status, response.body
    JSON.parse(response.body).fetch("data")
  end

  def patch_task(id, **changes)
    response = json_request("PATCH", "/api/v1/tasks/#{id}", changes, if_match(id))
    assert_equal 200, response.status, response.body
    JSON.parse(response.body).fetch("data")
  end

  def if_match(id) = { "HTTP_IF_MATCH" => get("/api/v1/tasks/#{id}")["etag"] }

  def list_ids(path)
    response = get(path)
    assert_equal 200, response.status, response.body
    JSON.parse(response.body).fetch("data").map { |row| row.fetch("id") }
  end

  # The delegation object as it sits on disk, so representation assertions are
  # anchored to the record rather than to another read of the same code.
  def stored_delegation(id)
    record = Tasks::Format.parse(File.read(@org, encoding: "UTF-8")).records.find do |candidate|
      candidate["id"] == id
    end
    record && record["delegation"]
  end

  # The real CLI over the same store, for cross-adapter parity.
  def cli!(*argv)
    env = {
      "TASKS_FILE" => @org, "TASKS_ARCHIVE" => @archive,
      "XDG_STATE_HOME" => File.join(@dir, "state"),
      "XDG_CONFIG_HOME" => File.join(@dir, "config"),
    }
    out, err, status = Open3.capture3(env, TASKS_BIN, *argv, chdir: ROOT)
    assert status.success?, "tasks #{argv.join(" ")} failed: #{err}"
    out
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

  def assert_error(response, status, code, context: nil)
    assert_equal status, response.status, [context, response.body].compact.join(" ")
    payload = JSON.parse(response.body)
    assert_equal code, payload.dig("error", "code"), context
    assert_kind_of String, payload.dig("error", "message")
    assert_kind_of Hash, payload.dig("error", "details")
    assert_match(/\Areq_[0-9a-f]+\z/, payload.dig("error", "request_id"))
    refute_match(/#{Regexp.escape(@dir)}/, response.body)
  end

  # `request_valid: false` is for the cases where the published contract itself
  # rejects the request (an off-vocabulary mode); the response must still be a
  # documented one.
  def assert_contract_response(response, request_valid: true)
    request_data, rack_request = contract_request_for(response)
    assert_contract_request response, valid: request_valid
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
