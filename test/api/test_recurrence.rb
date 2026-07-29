# frozen_string_literal: true

require "json"
require "minitest/mock"
require "openapi_first"
require "rack/mock"
require "rack/request"
require "rack/response"
require "stringio"
require "tmpdir"

require_relative "../test_helper"
require "tasks/api/app"

# HTTP adapter coverage for calendar recurrence: both input syntaxes on the
# write paths, the humanized rendering on every task payload, and the taskless
# explain endpoint. The grammar itself is proven in test_recur.rb and the roll
# semantics in the store tests; what is asserted here is transport — which
# spelling is stored and echoed, which reason reaches the client, and which
# status carries it.
class TestApiRecurrence < Minitest::Test
  ROOT = File.expand_path("../..", __dir__)
  CONTRACT = File.join(ROOT, "docs/api/openapi.yaml")
  HOST = "127.0.0.1:4747"
  EXPLAIN = "/api/v1/recurrence/explain"

  # A schedule that parses but can never fire: five Fridays need a leap
  # February, and a four-year parity anchored on a non-leap year only ever
  # reaches non-leap years. The anchor is a fixture date, so this stays true
  # whatever year the suite runs in.
  NEVER_FIRES = "4y:02:5fri"

  def setup
    @dir = Dir.mktmpdir("tasks-api-recurrence")
    @org = File.join(@dir, "tasks.jsonl")
    @archive = File.join(@dir, "archive.jsonl")
    File.write(@org, recurring_fixture)
    @log = StringIO.new
    @app = Tasks::Api::App.build(paths: Tasks::Config.for_dir(@dir), port: 4747, logger: @log)
    @request = Rack::MockRequest.new(@app)
    @definition = OpenapiFirst.load(CONTRACT)
  end

  def teardown
    FileUtils.remove_entry(@dir) if File.directory?(@dir)
  end

  # -- writes ----------------------------------------------------------------

  def test_create_accepts_both_input_syntaxes_and_echoes_the_canonical_schedule
    phrased = json_request(
      "POST", "/api/v1/tasks",
      title: "Standup notes", scheduled: "2026-08-03", recurrence: "every mon wed fri"
    )
    assert_equal 201, phrased.status, phrased.body
    resource = JSON.parse(phrased.body).fetch("data")
    assert_equal "w:mon,wed,fri", resource.fetch("recurrence")
    assert_equal "every Mon, Wed, Fri", resource.fetch("recurrence_human")
    assert_contract_response(phrased)

    # The canonical grammar and the phrase that means it store identically.
    canonical = json_request(
      "POST", "/api/v1/tasks",
      title: "Standup notes, said the short way", scheduled: "2026-08-03",
      recurrence: "w:mon,wed,fri"
    )
    assert_equal 201, canonical.status, canonical.body
    assert_equal "w:mon,wed,fri", JSON.parse(canonical.body).dig("data", "recurrence")

    # Interval input keeps working, unchanged, alongside the calendar shapes.
    interval = json_request(
      "POST", "/api/v1/tasks",
      title: "Weekly review", scheduled: "2026-08-03", recurrence: "weekly"
    )
    assert_equal 201, interval.status, interval.body
    assert_equal ".+1w", JSON.parse(interval.body).dig("data", "recurrence")
    assert_equal "every week from completion",
                 JSON.parse(interval.body).dig("data", "recurrence_human")
    assert_contract_response(interval)

    created_titles = ["Standup notes", "Standup notes, said the short way", "Weekly review"]
    stored = Tasks::Format.parse(File.read(@org, encoding: "UTF-8")).records
                          .select { |record| created_titles.include?(record["title"]) }
    assert_equal ["w:mon,wed,fri", "w:mon,wed,fri", ".+1w"], stored.map { |record| record["recur"] }
    assert Tasks::Check.check(@org).ok?
  end

  def test_patch_accepts_phrases_rewrites_canonically_and_clears_with_off
    current = get("/api/v1/tasks/#{FIX[:eval]}")
    monthly = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:eval]}",
      { recurrence: "the 15th of the month" }, { "HTTP_IF_MATCH" => current["etag"] }
    )
    assert_equal 200, monthly.status, monthly.body
    resource = JSON.parse(monthly.body).fetch("data")
    assert_equal "m:15", resource.fetch("recurrence")
    assert_equal "monthly on the 15th", resource.fetch("recurrence_human")
    assert_equal "m:15", record_for(@org, title: "Midyear self-eval").fetch("recur")
    assert_contract_response(monthly)

    ordinal = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:eval]}",
      { recurrence: "2nd tuesday" }, { "HTTP_IF_MATCH" => monthly["etag"] }
    )
    assert_equal 200, ordinal.status, ordinal.body
    assert_equal "m:2tue", JSON.parse(ordinal.body).dig("data", "recurrence")
    assert_equal "monthly on the 2nd Tuesday",
                 JSON.parse(ordinal.body).dig("data", "recurrence_human")

    cleared = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:eval]}",
      { recurrence: "off" }, { "HTTP_IF_MATCH" => ordinal["etag"] }
    )
    assert_equal 200, cleared.status, cleared.body
    assert_nil JSON.parse(cleared.body).dig("data", "recurrence")
    assert_nil JSON.parse(cleared.body).dig("data", "recurrence_human")
    refute record_for(@org, title: "Midyear self-eval").key?("recur")
    assert Tasks::Check.check(@org).ok?
  end

  # A rejection has to say which part of the phrase failed; "must be a valid
  # recurrence interval" told an agent nothing it could act on.
  def test_parse_rejections_carry_the_parsers_own_reason
    unknown = json_request(
      "POST", "/api/v1/tasks",
      title: "Nonsense schedule", scheduled: "2026-08-03", recurrence: "every blursday"
    )
    assert_error unknown, 422, "validation_failed"
    assert_equal ['unrecognized schedule: "every blursday"'],
                 JSON.parse(unknown.body).dig("error", "details", "fields", "recurrence")

    current = get("/api/v1/tasks/#{FIX[:eval]}")
    prefixed = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:eval]}",
      { recurrence: ".+w:mon" }, { "HTTP_IF_MATCH" => current["etag"] }
    )
    assert_error prefixed, 422, "validation_failed"
    reason = JSON.parse(prefixed.body).dig("error", "details", "fields", "recurrence").first
    assert_match(/is an interval prefix/, reason)
    assert_match(/use "\+" to advance one occurrence at a time/, reason)

    # `off` clears a schedule; on create there is nothing to clear, and saying
    # so beats echoing a parser reason that does not exist for this input.
    off_on_create = json_request(
      "POST", "/api/v1/tasks", title: "Nothing to clear", recurrence: "off"
    )
    assert_error off_on_create, 422, "validation_failed"
    assert_equal ['must name a schedule, not "off"'],
                 JSON.parse(off_on_create.body).dig("error", "details", "fields", "recurrence")

    assert_equal FIXTURE_RECORDS.length,
                 Tasks::Format.parse(File.read(@org, encoding: "UTF-8")).records.length,
                 "a rejected schedule must not write anything"
  end

  # Whether a schedule ever fires depends on the task's own date, so the refusal
  # comes from the store's write-time guard rather than the parser — and its
  # reason has to reach the client through the same 422 shape.
  def test_unsatisfiable_schedules_surface_the_stores_reason
    created = json_request(
      "POST", "/api/v1/tasks",
      title: "Impossible cadence", scheduled: "2026-08-01", recurrence: NEVER_FIRES
    )
    assert_error created, 422, "validation_failed"
    reason = JSON.parse(created.body).dig("error", "details", "fields", "recurrence").first
    assert_match(/may never fire for this anchor/, reason)

    current = get("/api/v1/tasks/#{FIX[:eval]}")
    patched = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:eval]}",
      { recurrence: NEVER_FIRES }, { "HTTP_IF_MATCH" => current["etag"] }
    )
    assert_error patched, 422, "validation_failed"
    assert_match(/may never fire for this anchor/,
                 JSON.parse(patched.body).dig("error", "message"))
    refute record_for(@org, title: "Midyear self-eval").key?("recur")

    # The other unwritable shape: a schedule that rolls past the four-digit
    # years a stored date is written with.
    unstorable = json_request(
      "PATCH", "/api/v1/tasks/#{FIX[:eval]}",
      { recurrence: "9999y:07-04" }, { "HTTP_IF_MATCH" => current["etag"] }
    )
    assert_error unstorable, 422, "validation_failed"
    assert_match(/outside the four-digit years/,
                 JSON.parse(unstorable.body).dig("error", "message"))
    assert Tasks::Check.check(@org).ok?
  end

  # -- representation --------------------------------------------------------

  def test_task_payloads_carry_the_humanized_schedule_on_rows_and_resources
    listed = get("/api/v1/tasks?recurring=true")
    assert_equal 200, listed.status
    rows = JSON.parse(listed.body).fetch("data")
    assert_equal [FIX[:flight]], rows.map { |row| row.fetch("id") }
    assert_equal "w:mon,wed,fri", rows.first.fetch("recurrence")
    assert_equal "every Mon, Wed, Fri", rows.first.fetch("recurrence_human")
    assert_contract_response(listed)

    open_rows = JSON.parse(get("/api/v1/tasks").body).fetch("data")
    assert open_rows.all? { |row| row.key?("recurrence_human") },
           "recurrence_human is part of the row shape, not just the resource"
    plain = open_rows.find { |row| row.fetch("id") == FIX[:pr] }
    assert_nil plain.fetch("recurrence_human")

    resource = JSON.parse(get("/api/v1/tasks/#{FIX[:flight]}").body).fetch("data")
    assert_equal "every Mon, Wed, Fri", resource.fetch("recurrence_human")
  end

  # -- explain ---------------------------------------------------------------

  def test_explain_projects_an_understood_schedule
    response = get("#{EXPLAIN}?input=every+mon+wed+fri")
    assert_equal 200, response.status, response.body
    payload = JSON.parse(response.body)
    data = payload.fetch("data")
    assert_equal "every mon wed fri", data.fetch("input")
    assert_equal "w:mon,wed,fri", data.fetch("canonical")
    assert_equal "every Mon, Wed, Fri", data.fetch("human")
    assert_equal 5, data.fetch("next").length
    data.fetch("next").each { |date| assert_match(/\A\d{4}-\d{2}-\d{2}\z/, date) }
    assert_equal data.fetch("next"), data.fetch("next").sort
    assert_operator Date.iso8601(data.fetch("next").first), :>, store_today
    refute data.key?("error")
    refute payload.key?("meta"), "explain reads no store, so it reports no store revision"
    assert_contract_response(response)

    # The canonical grammar explains to the same answer as its phrase.
    assert_equal data.fetch("canonical"),
                 JSON.parse(get("#{EXPLAIN}?input=w%3Amon%2Cwed%2Cfri").body).dig("data", "canonical")

    interval = JSON.parse(get("#{EXPLAIN}?input=every+2+weeks").body).fetch("data")
    assert_equal ".+2w", interval.fetch("canonical")
    assert_equal "every 2 weeks from completion", interval.fetch("human")

    cleared = get("#{EXPLAIN}?input=off")
    assert_equal 200, cleared.status
    assert_nil JSON.parse(cleared.body).dig("data", "canonical")
    assert_equal "no recurrence", JSON.parse(cleared.body).dig("data", "human")
    assert_empty JSON.parse(cleared.body).dig("data", "next")
    assert_contract_response(cleared)
  end

  def test_explain_count_defaults_clamps_and_rejects_non_integers
    assert_equal 5, JSON.parse(get("#{EXPLAIN}?input=every+monday").body).dig("data", "next").length

    counted = get("#{EXPLAIN}?input=every+monday&count=2")
    assert_equal 2, JSON.parse(counted.body).dig("data", "next").length
    assert_contract_response(counted)

    assert_empty JSON.parse(get("#{EXPLAIN}?input=every+monday&count=0").body).dig("data", "next")
    # Out of range clamps to the engine's own ceiling rather than 422-ing.
    assert_equal 50, JSON.parse(get("#{EXPLAIN}?input=every+monday&count=999").body).dig("data", "next").length
    assert_empty JSON.parse(get("#{EXPLAIN}?input=every+monday&count=-3").body).dig("data", "next")

    assert_error get("#{EXPLAIN}?input=every+monday&count=lots"), 422, "validation_failed"
    assert_equal ["must be an integer"],
                 JSON.parse(get("#{EXPLAIN}?input=every+monday&count=2.5").body)
                     .dig("error", "details", "fields", "count")
  end

  def test_explain_reports_a_schedule_that_can_never_fire
    # Pin the clock: whether this schedule fires is a property of the anchor,
    # and the anchor here is today.
    pinned = Tasks::TemporalContext.new(now: Time.utc(2026, 7, 28, 12), timezone: "Etc/UTC")
    response = Tasks::TemporalContext.stub(:capture, pinned) do
      get("#{EXPLAIN}?input=#{Rack::Utils.escape(NEVER_FIRES)}")
    end

    assert_equal 200, response.status, response.body
    data = JSON.parse(response.body).fetch("data")
    assert_equal NEVER_FIRES, data.fetch("input")
    assert_equal NEVER_FIRES, data.fetch("canonical")
    assert_equal "every 4 years on the 5th Friday of February", data.fetch("human")
    assert_empty data.fetch("next")
    assert_match(/may never fire for this anchor/, data.fetch("error"))
    assert_contract_response(response)
  end

  def test_explain_reports_the_parser_reason_for_input_that_is_not_a_schedule
    response = get("#{EXPLAIN}?input=every+blursday")
    assert_equal 200, response.status, response.body
    data = JSON.parse(response.body).fetch("data")
    assert_equal({ "input" => "every blursday", "error" => 'unrecognized schedule: "every blursday"' }, data)
    assert_contract_response(response)

    prefixed = get("#{EXPLAIN}?input=.%2Bw%3Amon")
    assert_equal 200, prefixed.status
    assert_match(/is an interval prefix/, JSON.parse(prefixed.body).dig("data", "error"))
    assert_contract_response(prefixed)
  end

  def test_explain_rejects_a_malformed_request_rather_than_answering_it
    missing = get(EXPLAIN)
    assert_error missing, 422, "validation_failed"
    assert_equal ["is required"], JSON.parse(missing.body).dig("error", "details", "fields", "input")
    assert_contract_request missing, valid: false

    blank = get("#{EXPLAIN}?input=+++")
    assert_error blank, 422, "validation_failed"
    assert_equal ["must be non-empty text"],
                 JSON.parse(blank.body).dig("error", "details", "fields", "input")

    unknown = get("#{EXPLAIN}?input=every+monday&from=2026-08-01")
    assert_error unknown, 422, "validation_failed"
    assert_equal ["unknown query field from"],
                 JSON.parse(unknown.body).dig("error", "details", "fields", "query")

    assert_equal 404, get("/api/v1/recurrence").status
    assert_equal 404, @request.post(EXPLAIN, "HTTP_HOST" => HOST).status
  end

  # The endpoint is the one read that needs nothing but the clock, so it keeps
  # answering when the store cannot be read at all.
  def test_explain_needs_no_readable_store
    File.write(@org, "{not-json\n")

    assert_error get("/api/v1/tasks"), 503, "store_invalid"
    response = get("#{EXPLAIN}?input=every+monday")
    assert_equal 200, response.status, response.body
    assert_equal "w:mon", JSON.parse(response.body).dig("data", "canonical")
  end

  private

  def recurring_fixture
    records = FIXTURE_RECORDS.map(&:dup)
    records.find { |record| record["id"] == FIX[:flight] }["recur"] = "w:mon,wed,fri"
    Tasks::Format.dump(records)
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
    assert_kind_of Hash, payload.dig("error", "details")
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

  def assert_contract_request(response, valid:)
    data, rack_request = contract_request_for(response)
    validated = @definition.validate_request(rack_request)
    if valid
      assert validated.valid?, "#{data[:method]} #{data[:path]} request: #{validated.error&.message}"
    else
      refute validated.valid?, "#{data[:method]} #{data[:path]} unexpectedly matched the contract"
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

  def store_today = Tasks::TemporalContext.capture(timezone: Tasks::Config.for_dir(@dir).timezone).local_date
end
