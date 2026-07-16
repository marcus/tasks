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

# HTTP adapter coverage for the Projects routes. The app is seeded with
# PROJECTS_FIXTURE so the ProjectView rollups match test_projects.rb, and every
# response is checked against the OpenAPI contract. Semantics stay in parity
# with the CLI (test_cli_projects.rb); only the transport differs — strict ids,
# no fuzzy refs, JSON envelopes, status codes.
class TestApiProjects < Minitest::Test
  ROOT = File.expand_path("../..", __dir__)
  CONTRACT = File.join(ROOT, "docs/api/openapi.yaml")
  HOST = "127.0.0.1:4747"
  ORIGIN = "http://127.0.0.1:4747"

  # A project whose only open task is deferred: open_count 0, held_count 1.
  DEFERRED_ONLY_FIXTURE = Tasks::Format.dump([
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "dddd0001", "title" => "Projects" },
    { "type" => "section", "id" => "dddd0002", "parent" => "dddd0001", "title" => "Parked" },
    { "type" => "task", "id" => "dddd0003", "parent" => "dddd0002", "state" => "TODO",
      "title" => "Someday: revisit", "tags" => %w[defer] },
  ])

  def setup
    @dir = Dir.mktmpdir("tasks-api-projects")
    @org = File.join(@dir, "tasks.jsonl")
    @archive = File.join(@dir, "archive.jsonl")
    File.write(@org, PROJECTS_FIXTURE)
    @log = StringIO.new
    @app = Tasks::Api::App.build(paths: Tasks::Config.for_dir(@dir), port: 4747, logger: @log)
    @request = Rack::MockRequest.new(@app)
    @definition = OpenapiFirst.load(CONTRACT)
  end

  def teardown
    FileUtils.remove_entry(@dir) if File.directory?(@dir)
  end

  # -- reads -----------------------------------------------------------------

  def test_list_projects_returns_projects_then_areas_with_revision
    response = get("/api/v1/projects")
    assert_equal 200, response.status
    payload = JSON.parse(response.body)
    assert_equal [PFIX[:site], PFIX[:empty], PFIX[:reno], PFIX[:tasks]],
                 payload["data"].map { |p| p["id"] }
    assert_equal %w[project project project area], payload["data"].map { |p| p["kind"] }
    site = payload["data"].first
    assert_equal 3, site["open_count"]
    assert_equal "2026-07-25", site["next_date"]
    assert_equal [PFIX[:site_next], PFIX[:site_todo], PFIX[:site_sub_task]], site["task_ids"]
    # An area emits explicit nulls (strict schema, unlike the CLI's lean shape).
    area = payload["data"].last
    assert_nil area["parent_id"]
    assert_nil area["next_date"]
    assert_nil area["body"]
    assert_equal quote(payload.dig("meta", "store_revision")), response["etag"]
    refute_match(/#{Regexp.escape(@dir)}/, response.body)
    assert_contract_response(response)
  end

  def test_get_one_project
    response = get("/api/v1/projects/#{PFIX[:site]}")
    assert_equal 200, response.status
    assert_equal "Site launch", JSON.parse(response.body).dig("data", "title")
    assert_contract_response(response)
  end

  def test_get_non_project_id_is_404
    # A done-only section, the Projects heading, Inbox, and a task id are all
    # 404 — none is a project or an area with open work.
    [PFIX[:donepile], PFIX[:projects], PFIX[:inbox], PFIX[:site_next]].each do |id|
      response = get("/api/v1/projects/#{id}")
      assert_error response, 404, "not_found"
      assert_contract_response(response)
    end
  end

  def test_malformed_project_id_is_400
    response = get("/api/v1/projects/not-hex")
    assert_error response, 400, "malformed_request"
  end

  # -- create (POST) ---------------------------------------------------------

  def test_create_project_returns_201_with_the_new_project
    response = json("POST", "/api/v1/projects", { "title" => "Mid-year Reviews" })
    assert_equal 201, response.status
    payload = JSON.parse(response.body)
    assert_equal "Mid-year Reviews", payload.dig("data", "title")
    assert_equal "project", payload.dig("data", "kind")
    assert_equal 0, payload.dig("data", "open_count")
    id = payload.dig("data", "id")
    assert_equal "/api/v1/projects/#{id}", response["location"]
    assert_equal PFIX[:projects], record("Mid-year Reviews")["parent"]
    assert Tasks::Check.check(@org).ok?
    assert_contract_response(response)
  end

  def test_create_project_blank_title_is_422
    response = json("POST", "/api/v1/projects", { "title" => "   " })
    assert_error response, 422, "validation_failed"
    assert_equal PROJECTS_FIXTURE, File.read(@org)
  end

  def test_create_project_missing_title_is_422
    response = json("POST", "/api/v1/projects", {})
    assert_error response, 422, "validation_failed"
  end

  def test_create_project_duplicate_title_is_422
    # "Site launch" is an existing project; the domain rejects the duplicate.
    response = json("POST", "/api/v1/projects", { "title" => "site launch" })
    assert_error response, 422, "validation_failed"
    assert_equal PROJECTS_FIXTURE, File.read(@org)
  end

  def test_create_project_unknown_field_is_422
    response = json("POST", "/api/v1/projects", { "title" => "X", "colour" => "red" })
    assert_error response, 422, "validation_failed"
  end

  def test_create_project_rejects_a_foreign_origin
    response = json("POST", "/api/v1/projects", { "title" => "X" },
                    "HTTP_ORIGIN" => "http://evil.example")
    assert_error response, 403, "forbidden_origin"
  end

  # -- rename (PATCH) --------------------------------------------------------

  def test_rename_project_updates_title
    response = json("PATCH", "/api/v1/projects/#{PFIX[:reno]}", { "title" => "Kitchen reno" })
    assert_equal 200, response.status
    assert_equal "Kitchen reno", JSON.parse(response.body).dig("data", "title")
    assert_equal "Kitchen reno", record("Kitchen reno")["title"]
    assert_contract_response(response)
  end

  def test_rename_project_blank_title_is_422
    response = json("PATCH", "/api/v1/projects/#{PFIX[:reno]}", { "title" => "   " })
    assert_error response, 422, "validation_failed"
  end

  def test_rename_project_unknown_field_is_422
    response = json("PATCH", "/api/v1/projects/#{PFIX[:reno]}", { "title" => "X", "colour" => "red" })
    assert_error response, 422, "validation_failed"
  end

  def test_rename_missing_project_is_404
    response = json("PATCH", "/api/v1/projects/ffffffff", { "title" => "Ghost" })
    assert_error response, 404, "not_found"
  end

  def test_rename_area_out_of_scope_returns_200_with_new_title
    # Retitling the "Tasks" area to "Inbox" moves it out of the read model; the
    # write committed, so the response is a synthesized 200, not a 404.
    response = json("PATCH", "/api/v1/projects/#{PFIX[:tasks]}", { "title" => "Inbox" })
    assert_equal 200, response.status
    assert_equal "Inbox", JSON.parse(response.body).dig("data", "title")
    assert_nil record("Tasks"), "the section was retitled out of scope"
    assert Tasks::Check.check(@org).ok?
    assert_contract_response(response)
  end

  def test_rename_on_inbox_or_projects_root_is_404_and_writes_nothing
    [PFIX[:inbox], PFIX[:projects]].each do |id|
      response = json("PATCH", "/api/v1/projects/#{id}", { "title" => "Renamed" })
      assert_error response, 404, "not_found"
      assert_equal PROJECTS_FIXTURE, File.read(@org), "no write on a non-project rename"
    end
  end

  def test_rename_requires_no_if_match
    # Project mutations carry no per-resource revision, so — unlike task PATCH —
    # a missing If-Match is not 428.
    response = json("PATCH", "/api/v1/projects/#{PFIX[:reno]}", { "title" => "Kitchen reno" })
    assert_equal 200, response.status
  end

  # -- complete (POST) -------------------------------------------------------

  def test_complete_project_closes_open_tasks
    response = json("POST", "/api/v1/projects/#{PFIX[:site]}/complete", nil)
    assert_equal 200, response.status
    data = JSON.parse(response.body)["data"]
    assert_equal 0, data["open_count"]
    assert_empty data["task_ids"]
    assert_equal "DONE", record("Pick a static-site generator")["state"]
    assert_equal "DONE", record("Someday: custom domain")["state"]
    assert Tasks::Check.check(@org).ok?
    assert_contract_response(response)
  end

  def test_complete_missing_project_is_404
    response = json("POST", "/api/v1/projects/ffffffff/complete", nil)
    assert_error response, 404, "not_found"
  end

  def test_complete_area_closes_its_tasks_and_returns_zero_open
    # An area drops out of the read model once its open work is closed; the
    # completed 200 is synthesized from the pre-read, never a post-write 404.
    response = json("POST", "/api/v1/projects/#{PFIX[:tasks]}/complete", nil)
    assert_equal 200, response.status
    data = JSON.parse(response.body)["data"]
    assert_equal 0, data["open_count"]
    assert_equal 0, data["held_count"]
    assert data["stuck"]
    assert_empty data["task_ids"]
    assert_equal "DONE", record("Reply to the vendor")["state"]
    assert_equal "DONE", record("File expenses")["state"]
    assert Tasks::Check.check(@org).ok?
    assert_contract_response(response)
  end

  def test_complete_on_inbox_or_projects_root_is_404_and_writes_nothing
    # Neither is a project or area; each must 404 before any cascade runs.
    [PFIX[:inbox], PFIX[:projects]].each do |id|
      response = json("POST", "/api/v1/projects/#{id}/complete", nil)
      assert_error response, 404, "not_found"
      assert_equal PROJECTS_FIXTURE, File.read(@org), "no write on a non-project complete"
    end
  end

  def test_complete_rejects_a_body
    response = json("POST", "/api/v1/projects/#{PFIX[:site]}/complete", { "unexpected" => true })
    assert_error response, 400, "malformed_request"
  end

  # -- archive (POST) --------------------------------------------------------

  def test_archive_refuses_while_open_tasks_remain
    response = post("/api/v1/projects/#{PFIX[:site]}/archive")
    assert_error response, 409, "conflict"
    assert_equal 3, JSON.parse(response.body).dig("error", "details", "open_count")
    assert_equal PROJECTS_FIXTURE, File.read(@org)
    assert_contract_response(response)
  end

  def test_archive_force_sweeps_the_subtree
    response = post("/api/v1/projects/#{PFIX[:site]}/archive?force=true")
    assert_equal 200, response.status
    data = JSON.parse(response.body)["data"]
    assert_equal PFIX[:site], data["id"]
    assert_equal 6, data["archived"]
    assert_equal PFIX[:site], data["moved_ids"].first
    assert_nil record("Site launch")
    assert_contract_response(response)
  end

  def test_archive_refuses_a_deferred_only_project_then_force_sweeps
    # A project whose only open work is deferred has open_count 0 but held_count
    # 1; that still blocks archive without force (parity with the CLI and with
    # complete's cascade, which closes held tasks).
    rebuild_app(DEFERRED_ONLY_FIXTURE)
    refused = post("/api/v1/projects/dddd0002/archive")
    assert_error refused, 409, "conflict"
    details = JSON.parse(refused.body).dig("error", "details")
    assert_equal 0, details["open_count"]
    assert_equal 1, details["held_count"]
    assert_equal DEFERRED_ONLY_FIXTURE, File.read(@org), "a refused archive writes nothing"
    assert_contract_response(refused)

    forced = post("/api/v1/projects/dddd0002/archive?force=true")
    assert_equal 200, forced.status
    assert_nil record("Parked")
    assert Tasks::Check.check(@org).ok?
    assert_contract_response(forced)
  end

  def test_archive_empty_project_needs_no_force
    response = post("/api/v1/projects/#{PFIX[:empty]}/archive")
    assert_equal 200, response.status
    assert_equal 1, JSON.parse(response.body).dig("data", "archived")
    assert_contract_response(response)
  end

  def test_archive_rejects_unknown_query
    response = post("/api/v1/projects/#{PFIX[:empty]}/archive?bogus=1")
    assert_error response, 422, "validation_failed"
  end

  # -- origin enforcement (parity with task mutations) -----------------------

  def test_project_mutation_rejects_a_foreign_origin
    response = json("PATCH", "/api/v1/projects/#{PFIX[:reno]}", { "title" => "X" },
                    "HTTP_ORIGIN" => "http://evil.example")
    assert_error response, 403, "forbidden_origin"
  end

  private

  # Reseed the sandbox with a custom fixture and rebuild the app around it.
  def rebuild_app(content)
    File.write(@org, content)
    @app = Tasks::Api::App.build(paths: Tasks::Config.for_dir(@dir), port: 4747, logger: @log)
    @request = Rack::MockRequest.new(@app)
  end

  def get(path) = request("GET", path)

  def post(path, env = {}) = request("POST", path, { "HTTP_ORIGIN" => ORIGIN }.merge(env))

  def json(method, path, body, env = {})
    input = body.nil? ? "" : JSON.generate(body)
    request(method, path, {
      "CONTENT_TYPE" => "application/json", "HTTP_ORIGIN" => ORIGIN, input: input
    }.merge(env))
  end

  def request(method, path, env = {})
    env = { "HTTP_HOST" => HOST }.merge(env)
    contract = {
      method: method, path: path,
      input: (env[:input] || env["input"])&.dup,
      content_type: env["CONTENT_TYPE"],
    }
    response = @request.request(method, path, env)
    response.instance_variable_set(:@tasks_contract_request, contract)
    response
  end

  def record(title) = record_for(@org, title: title)

  def assert_error(response, status, code)
    assert_equal status, response.status, response.body
    payload = JSON.parse(response.body)
    assert_equal code, payload.dig("error", "code")
    assert_kind_of Hash, payload.dig("error", "details")
    refute_match(/#{Regexp.escape(@dir)}/, response.body)
  end

  def assert_contract_response(response)
    data = response.instance_variable_get(:@tasks_contract_request)
    path = data.fetch(:path).sub(%r{\A/api/v1}, "")
    env = { method: data.fetch(:method) }
    env[:input] = data[:input] unless data[:input].nil?
    env["CONTENT_TYPE"] = data[:content_type] if data[:content_type]
    rack_request = Rack::Request.new(Rack::MockRequest.env_for(path, env))
    rack_response = Rack::Response.new(
      response.body.empty? ? [] : [response.body], response.status, response.headers
    )
    validated = @definition.validate_response(rack_request, rack_response)
    assert validated&.valid?, "#{data[:method]} #{data[:path]}: #{validated&.error&.message || "not matched"}"
  end

  def quote(value) = %Q("#{value}")
end
