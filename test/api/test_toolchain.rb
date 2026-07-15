# frozen_string_literal: true

require "json"
require "minitest/autorun"
require "net/http"
require "openapi_first"
require "rack/mock"
require "rack/request"
require "rack/response"
require "socket"
require "tempfile"
require "timeout"
require "yaml"

class TestApiToolchain < Minitest::Test
  ROOT = File.expand_path("../..", __dir__)
  CONTRACT = File.join(ROOT, "docs/api/openapi.yaml")
  RACK_FIXTURE = File.join(__dir__, "fixtures/toolchain.ru")
  METHODS = %w[get post patch delete].freeze

  def setup
    @definition = OpenapiFirst.load(CONTRACT)
    @document = YAML.safe_load_file(CONTRACT, aliases: true)
  end

  def test_locked_toolchain_loads_the_openapi_31_contract
    assert_equal "3.1.0", @definition["openapi"]
    assert_equal "Tasks local HTTP API", @definition.title
    assert_operator @definition.routes.count, :>=, 10
  end

  def test_every_embedded_request_body_example_validates
    checked = []
    each_operation do |path, method, operation|
      content = resolve(operation["requestBody"])&.dig("content", "application/json")
      next unless content

      examples(content).each do |name, value|
        request = rack_request(path, method, body: value)
        validated = @definition.validate_request(request)
        assert validated.valid?, request_failure(path, method, name, validated)
        checked << "#{method.upcase} #{path} #{name}"
      end
    end

    assert_operator checked.length, :>=, 8
  end

  def test_every_embedded_response_example_validates
    checked = []
    each_operation do |path, method, operation|
      operation.fetch("responses", {}).each do |status, response_ref|
        response = resolve(response_ref)
        content = response&.dig("content", "application/json")
        next unless content

        examples(content).each do |name, value|
          request = rack_request(path, method)
          rack_response = Rack::Response.new(
            [JSON.generate(value)], Integer(status), response_headers(response)
          )
          validated = @definition.validate_response(request, rack_response)
          assert validated&.valid?, response_failure(path, method, status, name, validated)
          checked << "#{method.upcase} #{path} #{status} #{name}"
        end
      end
    end

    assert_operator checked.length, :>=, 20
  end

  def test_nullable_unions_local_references_and_unknown_body_fields_are_enforced
    valid = rack_request(
      "/tasks", "post",
      body: { "title" => "Contract proof", "priority" => nil, "deadline" => nil }
    )
    assert @definition.validate_request(valid).valid?

    unknown = rack_request(
      "/tasks", "post",
      body: { "title" => "Contract proof", "unexpected" => true }
    )
    result = @definition.validate_request(unknown)
    refute result.valid?
    assert_match(/unexpected/, result.error.message)
  end

  def test_task_placement_schema_accepts_stable_anchors_and_rejects_ambiguous_shapes
    placement = @document.dig("components", "schemas", "TaskPlacement")
    assert_equal ["parent_id"], placement.fetch("required")
    assert_equal false, placement.fetch("additionalProperties")

    valid = [
      { "placement" => { "parent_id" => "9f8e7d6c" } },
      { "placement" => { "parent_id" => "9f8e7d6c", "before_id" => nil } },
      { "placement" => { "parent_id" => "9f8e7d6c", "before_id" => "4d5e6f7a" } },
    ]
    valid.each do |body|
      result = @definition.validate_request(rack_request("/tasks/3c4d5e6f", "patch", body: body))
      assert result.valid?, "expected valid placement #{body.inspect}: #{result.error&.message}"
    end

    invalid = [
      { "placement" => nil },
      { "placement" => {} },
      { "placement" => { "parent_id" => "9f8e7d6c", "after_id" => "4d5e6f7a" } },
      { "parent_id" => "9f8e7d6c", "placement" => { "parent_id" => "9f8e7d6c" } },
    ]
    invalid.each do |body|
      result = @definition.validate_request(rack_request("/tasks/3c4d5e6f", "patch", body: body))
      refute result.valid?, "expected invalid placement #{body.inspect}"
    end
  end

  def test_patch_contract_freezes_placement_examples_statuses_and_revision_scope
    patch = @document.dig("paths", "/tasks/{id}", "patch")
    description = patch.fetch("description")
    assert_includes description, "Placement compares\nthe `own` component"
    assert_includes description, "Legacy\n`parent_id` keeps its existing `own` plus `location` comparison"

    example_names = patch.dig("requestBody", "content", "application/json", "examples").keys
    assert_includes example_names, "place_before"
    assert_includes example_names, "place_at_end"
    %w[404 409 412 422 428].each { |status| assert patch.fetch("responses").key?(status), status }
  end

  def test_unknown_query_fields_require_an_explicit_adapter_guard
    request = rack_request("/tasks?unexpected=true", "get")

    assert @definition.validate_request(request).valid?,
           "openapi_first intentionally validates documented parameters but does not reject extras; " \
           "the Rack adapter must compare raw and documented query keys"
  end

  def test_real_puma_boots_the_rack_lint_config
    port = available_port
    log = Tempfile.new("tasks-puma-toolchain")
    pid = Process.spawn(
      { "RACK_ENV" => "test" },
      "bundle", "exec", "puma", RACK_FIXTURE,
      "--bind", "tcp://127.0.0.1:#{port}", "--threads", "0:2",
      chdir: ROOT, out: log, err: log
    )

    response = wait_for_response(port)
    assert_equal "200", response.code
    assert_equal({ "ok" => true }, JSON.parse(response.body))
  ensure
    stop_process(pid) if pid
    log&.close!
  end

  private

  def each_operation
    @document.fetch("paths").each do |path, path_item|
      METHODS.each do |method|
        operation = path_item[method]
        yield path, method, operation if operation
      end
    end
  end

  def examples(content)
    content.fetch("examples", {}).to_h do |name, example|
      [name, resolve(example).fetch("value")]
    end
  end

  def resolve(object)
    return object unless object.is_a?(Hash) && object["$ref"]

    object["$ref"].delete_prefix("#/").split("/").reduce(@document) do |value, token|
      value.fetch(token.gsub("~1", "/").gsub("~0", "~"))
    end
  end

  def rack_request(path, method, body: nil)
    concrete = path.gsub("{id}", "3c4d5e6f").gsub("{name}", "agenda")
    env = {
      "CONTENT_TYPE" => (body || %w[post patch].include?(method)) ? "application/json" : nil,
      "HTTP_IF_MATCH" => %w[patch delete].include?(method) ? '"v1.opaque"' : nil,
    }.compact
    input = body.nil? ? "" : JSON.generate(body)
    Rack::Request.new(
      Rack::MockRequest.env_for(concrete, env.merge(method: method.upcase, input: input))
    )
  end

  def response_headers(response)
    headers = { "content-type" => "application/json" }
    response.fetch("headers", {}).each_key do |name|
      headers[name.downcase] = case name.downcase
                               when "etag" then '"v1.opaque"'
                               when "location" then "/api/v1/tasks/3c4d5e6f"
                               else "example"
                               end
    end
    headers
  end

  def request_failure(path, method, name, validated)
    "#{method.upcase} #{path} request example #{name}: #{validated.error&.message}"
  end

  def response_failure(path, method, status, name, validated)
    "#{method.upcase} #{path} response example #{status}/#{name}: #{validated&.error&.message || "not matched"}"
  end

  def available_port
    server = TCPServer.new("127.0.0.1", 0)
    server.addr[1]
  ensure
    server&.close
  end

  def wait_for_response(port)
    Timeout.timeout(10) do
      loop do
        begin
          return Net::HTTP.get_response(URI("http://127.0.0.1:#{port}/"))
        rescue Errno::ECONNREFUSED, Errno::EHOSTUNREACH
          sleep 0.05
        end
      end
    end
  end

  def stop_process(pid)
    Process.kill("TERM", pid)
    Timeout.timeout(5) { Process.wait(pid) }
  rescue Errno::ESRCH, Errno::ECHILD
    nil
  rescue Timeout::Error
    Process.kill("KILL", pid)
    Process.wait(pid)
  end
end
