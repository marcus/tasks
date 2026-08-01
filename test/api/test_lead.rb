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

# HTTP adapter coverage for lead time: the field on both write paths, the
# read-only trio on every task payload, and CLI/API parity on the refusals.
# The model itself is proven in test_lead.rb; what is asserted here is
# transport — which spelling is stored and echoed, which reason reaches the
# client, and which status carries it.
class TestApiLead < Minitest::Test
  ROOT = File.expand_path("../..", __dir__)
  CONTRACT = File.join(ROOT, "docs/api/openapi.yaml")
  HOST = "127.0.0.1:4747"

  def setup
    @dir = Dir.mktmpdir("tasks-api-lead")
    @org = File.join(@dir, "tasks.jsonl")
    @archive = File.join(@dir, "archive.jsonl")
    File.write(@org, Tasks::Format.dump(FIXTURE_RECORDS))
    @log = StringIO.new
    @app = Tasks::Api::App.build(paths: Tasks::Config.for_dir(@dir), port: 4747, logger: @log)
    @request = Rack::MockRequest.new(@app)
    @definition = OpenapiFirst.load(CONTRACT)
  end

  def teardown
    FileUtils.remove_entry(@dir) if File.directory?(@dir)
  end

  # -- writes ----------------------------------------------------------------

  def test_create_accepts_a_span_or_a_phrase_and_echoes_the_canonical_value
    canonical = json_request("POST", "/api/v1/tasks",
                             title: "Renew the passport", deadline: "2026-11-01", lead: "3w")
    assert_equal 201, canonical.status, canonical.body
    resource = JSON.parse(canonical.body).fetch("data")
    assert_equal "3w", resource.fetch("lead")
    assert_equal "3 weeks", resource.fetch("lead_human")
    assert_equal "2026-10-11", resource.fetch("lead_opens")
    assert_contract_response(canonical)

    phrased = json_request("POST", "/api/v1/tasks",
                           title: "Renew the license", deadline: "2026-11-01", lead: "3 weeks")
    assert_equal 201, phrased.status, phrased.body
    assert_equal "3w", JSON.parse(phrased.body).dig("data", "lead")

    stored = Tasks::Format.parse(File.read(@org, encoding: "UTF-8")).records
                          .select { |record| ["Renew the passport", "Renew the license"].include?(record["title"]) }
    assert_equal %w[3w 3w], stored.map { |record| record["lead"] }
    assert Tasks::Check.check(@org).ok?
  end

  def test_patch_sets_replaces_and_clears_the_window
    current = get("/api/v1/tasks/#{FIX[:flight]}")
    set = json_request("PATCH", "/api/v1/tasks/#{FIX[:flight]}",
                       { lead: "a week" }, { "HTTP_IF_MATCH" => current["etag"] })
    assert_equal 200, set.status, set.body
    assert_equal "1w", JSON.parse(set.body).dig("data", "lead")
    assert_equal "2026-06-25", JSON.parse(set.body).dig("data", "lead_opens")
    assert_equal "1w", record_for(@org, title: "Book flight in Concur").fetch("lead")
    assert_contract_response(set)

    cleared = json_request("PATCH", "/api/v1/tasks/#{FIX[:flight]}",
                           { lead: "off" }, { "HTTP_IF_MATCH" => set["etag"] })
    assert_equal 200, cleared.status, cleared.body
    assert_nil JSON.parse(cleared.body).dig("data", "lead")
    assert_nil JSON.parse(cleared.body).dig("data", "lead_human")
    assert_nil JSON.parse(cleared.body).dig("data", "lead_opens")
    refute record_for(@org, title: "Book flight in Concur").key?("lead")

    current = get("/api/v1/tasks/#{FIX[:flight]}")
    nulled = json_request("PATCH", "/api/v1/tasks/#{FIX[:flight]}",
                          { lead: "2d" }, { "HTTP_IF_MATCH" => current["etag"] })
    assert_equal 200, nulled.status
    explicit_null = json_request("PATCH", "/api/v1/tasks/#{FIX[:flight]}",
                                 { lead: nil }, { "HTTP_IF_MATCH" => nulled["etag"] })
    assert_equal 200, explicit_null.status, explicit_null.body
    assert_nil JSON.parse(explicit_null.body).dig("data", "lead")
    assert Tasks::Check.check(@org).ok?
  end

  # -- reads -----------------------------------------------------------------

  def test_availability_reflects_the_derived_gate_with_no_new_reason
    json_request("POST", "/api/v1/tasks",
                 title: "Renew the passport", deadline: "2036-11-01", lead: "3w")
    row = collection_row("Renew the passport")

    assert_equal false, row.fetch("available")
    assert_equal "scheduled", row.fetch("availability_reason"), "no new availability reason"
    assert_match(/\A2036-10-11T/, row.fetch("available_at"))
    assert_nil row.fetch("scheduled"), "a deadline-anchored lead stores no available-from date"
    assert_equal "2036-10-11", row.fetch("lead_opens")
  end

  def test_available_false_already_selects_lead_gated_tasks
    json_request("POST", "/api/v1/tasks",
                 title: "Renew the passport", deadline: "2036-11-01", lead: "3w")

    hidden = JSON.parse(get("/api/v1/tasks?available=false").body).fetch("data")
    assert_includes hidden.map { |task| task["title"] }, "Renew the passport"

    visible = JSON.parse(get("/api/v1/tasks").body).fetch("data")
    refute_includes visible.map { |task| task["title"] }, "Renew the passport"
  end

  def test_lead_skip_never_reaches_the_wire
    json_request("POST", "/api/v1/tasks",
                 title: "Quarterly filing", scheduled: "2036-04-20", recurrence: "+3m", lead: "1w")
    id = record_for(@org, title: "Quarterly filing").fetch("id")
    current = get("/api/v1/tasks/#{id}")
    json_request("PATCH", "/api/v1/tasks/#{id}", { deferred: false },
                 { "HTTP_IF_MATCH" => current["etag"] })

    # Release this occurrence through the store, then prove the stamp is
    # internal: it is on disk, and absent from the resource.
    system_activate(id)
    assert_equal "2036-04-20", record_for(@org, title: "Quarterly filing").fetch("lead_skip")

    resource = JSON.parse(get("/api/v1/tasks/#{id}").body).fetch("data")
    refute resource.key?("lead_skip")
    assert_equal true, resource.fetch("available"), "the released occurrence is available"
    assert_equal "1w", resource.fetch("lead"), "the window itself survives"
  end

  # -- refusals (CLI/API parity) ---------------------------------------------

  def test_refusals_carry_the_same_reasons_the_cli_gives
    dateless = json_request("POST", "/api/v1/tasks", title: "No date at all", lead: "3w")
    assert_error dateless, 422, "validation_failed"
    assert_match(/needs a date to hide before/,
                 JSON.parse(dateless.body).dig("error", "details", "fields", "lead").first)

    both = json_request("POST", "/api/v1/tasks", title: "Two gates",
                        deadline: "2026-11-01", scheduled: "2026-10-01", lead: "3w")
    assert_error both, 422, "validation_failed"
    assert_match(/second, ignored gate/,
                 JSON.parse(both.body).dig("error", "details", "fields", "lead").first)

    junk = json_request("POST", "/api/v1/tasks", title: "Junk span",
                        deadline: "2026-11-01", lead: "soonish")
    assert_error junk, 422, "validation_failed"
    assert_match(/unrecognized lead time/,
                 JSON.parse(junk.body).dig("error", "details", "fields", "lead").first)

    hours = json_request("POST", "/api/v1/tasks", title: "Hour lead",
                         deadline: "2026-11-01", lead: "5h")
    assert_error hours, 422, "validation_failed"
    assert_match(/isn't supported yet/,
                 JSON.parse(hours.body).dig("error", "details", "fields", "lead").first)

    typed = json_request("POST", "/api/v1/tasks", title: "Wrong type",
                         deadline: "2026-11-01", lead: 3)
    assert_error typed, 422, "validation_failed"
    assert_equal ["must be text or null"],
                 JSON.parse(typed.body).dig("error", "details", "fields", "lead")
  end

  def test_create_refuses_off_and_a_scheduled_date_beside_a_deadline_anchored_lead
    off = json_request("POST", "/api/v1/tasks", title: "Nothing to clear",
                       deadline: "2026-11-01", lead: "off")
    assert_error off, 422, "validation_failed"
    assert_equal ['must name a span, not "off"'],
                 JSON.parse(off.body).dig("error", "details", "fields", "lead")

    json_request("POST", "/api/v1/tasks", title: "Renew the passport",
                 deadline: "2026-11-01", lead: "3w")
    id = record_for(@org, title: "Renew the passport").fetch("id")
    current = get("/api/v1/tasks/#{id}")
    second_gate = json_request("PATCH", "/api/v1/tasks/#{id}",
                               { scheduled: "2026-10-01" }, { "HTTP_IF_MATCH" => current["etag"] })
    assert_equal 422, second_gate.status, second_gate.body
    assert_match(/second, ignored gate/, second_gate.body)
  end

  private

  # Activation is a composite store operation with no HTTP verb of its own;
  # the API's job here is only to keep the stamp it writes off the wire.
  def system_activate(id)
    store = Tasks::Store.new(org: @org, archive: @archive)
    snapshot = store.edit_snapshot(id)
    result = store.apply_changeset!(Tasks::TaskChangeset.from(snapshot, changes: { activate: true }))
    assert result.ok?, result.errors.join(", ")
  end

  def collection_row(title)
    JSON.parse(get("/api/v1/tasks?available=false").body).fetch("data")
        .find { |task| task["title"] == title } or raise "no row for #{title}"
  end

  def get(path) = request("GET", path)

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
    contract = {
      method: method, path: path,
      input: (env[:input] || env["input"])&.dup,
      content_type: env["CONTENT_TYPE"],
      if_match: env["HTTP_IF_MATCH"],
    }
    response = @request.request(method, path, env)
    response.instance_variable_set(:@tasks_contract_request, contract)
    response
  end

  def assert_error(response, status, code)
    assert_equal status, response.status, response.body
    payload = JSON.parse(response.body)
    assert_equal code, payload.dig("error", "code")
    refute_match(/#{Regexp.escape(@dir)}/, response.body)
  end

  def assert_contract_response(response)
    data, rack_request = contract_request_for(response)
    rack_response = Rack::Response.new(
      response.body.empty? ? [] : [response.body], response.status, response.headers
    )
    validated = @definition.validate_response(rack_request, rack_response)
    assert validated&.valid?,
           "#{data[:method]} #{data[:path]}: #{validated&.error&.message || "not matched"}"
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
end
