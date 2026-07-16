# frozen_string_literal: true

require "json"
require "net/http"
require "openapi_first"
require "open3"
require "rack/mock"
require "rack/request"
require "rack/response"
require "rbconfig"
require "socket"
require "tempfile"
require "timeout"
require "tmpdir"

require_relative "../test_helper"

class TestApiBlackBox < Minitest::Test
  ROOT = File.expand_path("../..", __dir__)
  API_BIN = File.join(ROOT, "bin/tasks-api")
  TASKS_BIN = File.join(ROOT, "bin/tasks")
  CONTRACT = File.join(ROOT, "docs/api/openapi.yaml")

  def setup
    @dir = Dir.mktmpdir("tasks-api-black-box")
    @org = File.join(@dir, "tasks.jsonl")
    @archive = File.join(@dir, "archive.jsonl")
    @state = File.join(@dir, "state")
    File.write(@org, FIXTURE)
    @env = {
      "TASKS_FILE" => @org,
      "TASKS_ARCHIVE" => @archive,
      "XDG_STATE_HOME" => @state,
    }
    @port = available_port
    @log = Tempfile.new("tasks-api-puma")
    @definition = OpenapiFirst.load(CONTRACT)
    @pid = Process.spawn(
      @env, API_BIN, "--port", @port.to_s,
      chdir: ROOT, out: @log, err: @log
    )
    wait_for_server
  end

  def teardown
    stop_server
    @log&.close!
    FileUtils.remove_entry(@dir) if @dir && File.directory?(@dir)
  end

  def test_real_entrypoint_starts_and_stops_cleanly
    response = http("GET", "/healthz")
    assert_equal "200", response.code
    assert_equal({ "status" => "ok" }, JSON.parse(response.body))
    assert process_alive?(@pid)

    status = stop_server("INT")
    assert status.success?, server_log
  end

  def test_cli_capture_racing_api_creates_preserves_every_successful_write
    api_titles = []
    cli_titles = []
    8.times do |index|
      ready = Queue.new
      go = Queue.new
      api_title = "API race #{index}"
      cli_title = "CLI race #{index}"
      api_thread = Thread.new do
        ready << true
        go.pop
        http("POST", "/api/v1/tasks", json: { title: api_title })
      end
      cli_thread = Thread.new do
        ready << true
        go.pop
        run_cli("capture", cli_title)
      end
      2.times { ready.pop }
      2.times { go << true }
      api_response = api_thread.value
      cli_result = cli_thread.value
      if api_response.code == "201"
        api_titles << api_title
      else
        flunk "API race failed: #{api_response.code} #{api_response.body}"
      end
      assert cli_result.fetch(:status).success?, cli_result.fetch(:stderr)
      cli_titles << cli_title
    end

    titles = Tasks::Store.new(org: @org, archive: @archive).items.map(&:title)
    assert_empty (api_titles + cli_titles) - titles
    assert Tasks::Check.check(@org).ok?
  end

  def test_cli_change_makes_loaded_http_etag_stale
    loaded = http("GET", "/api/v1/tasks/#{FIX[:pr]}")
    old_etag = loaded["etag"]
    cli = run_cli("retitle", "Review PR backlog", "CLI renamed")
    assert cli.fetch(:status).success?, cli.fetch(:stderr)

    stale = http(
      "PATCH", "/api/v1/tasks/#{FIX[:pr]}",
      json: { title: "API overwrite" }, headers: { "If-Match" => old_etag }
    )
    assert_equal "412", stale.code
    payload = JSON.parse(stale.body)
    assert_equal "stale_revision", payload.dig("error", "code")
    assert_equal "CLI renamed", payload.dig("error", "details", "current", "title")
    assert_equal "CLI renamed", Tasks::Store.new(org: @org, archive: @archive).items.find { |item| item.id == FIX[:pr] }.title
  end

  def test_ordered_placements_survive_cli_churn_and_match_stable_id_cli_bytes
    child_capture = run_cli("capture", "Placement subtree child", "--under", FIX[:plants])
    assert child_capture.fetch(:status).success?, child_capture.fetch(:stderr)
    child_id = task_id_for_title("Placement subtree child")
    grandchild_capture = run_cli("capture", "Placement subtree grandchild", "--under", child_id)
    assert grandchild_capture.fetch(:status).success?, grandchild_capture.fetch(:stderr)
    grandchild_id = task_id_for_title("Placement subtree grandchild")

    collection = http("GET", "/api/v1/tasks?scope=all")
    assert_contract_exchange(collection)
    loaded = JSON.parse(collection.body).fetch("data").to_h do |task|
      [task.fetch("id"), task]
    end

    capture = run_cli("capture", "Cross-process sibling", "--project", "Work")
    assert capture.fetch(:status).success?, capture.fetch(:stderr)
    reorder = run_cli("move", FIX[:eval], "--before", FIX[:flight])
    assert reorder.fetch(:status).success?, reorder.fetch(:stderr)
    before_placements = File.binread(@org)

    cli_dir = Dir.mktmpdir("tasks-placement-cli-parity")
    cli_org = File.join(cli_dir, "tasks.jsonl")
    cli_archive = File.join(cli_dir, "archive.jsonl")
    File.binwrite(cli_org, before_placements)

    same_parent = http(
      "PATCH", "/api/v1/tasks/#{FIX[:travel]}",
      json: { placement: { parent_id: FIX[:work], before_id: FIX[:pr] } },
      headers: { "If-Match" => quote(loaded.fetch(FIX[:travel]).fetch("revision")) }
    )
    assert_equal "200", same_parent.code, same_parent.body
    assert_contract_exchange(same_parent)
    assert_resource_etag(same_parent)
    after_same_parent = File.binread(@org)
    same_parent_revision = JSON.parse(same_parent.body).dig("meta", "store_revision")

    cross_parent = http(
      "PATCH", "/api/v1/tasks/#{FIX[:plants]}",
      json: { placement: { parent_id: FIX[:work], before_id: FIX[:flight] } },
      headers: { "If-Match" => quote(loaded.fetch(FIX[:plants]).fetch("revision")) }
    )
    assert_equal "200", cross_parent.code, cross_parent.body
    assert_contract_exchange(cross_parent)
    assert_resource_etag(cross_parent)
    after_both = File.binread(@org)
    final_payload = JSON.parse(cross_parent.body)
    final_revision = final_payload.dig("meta", "store_revision")
    assert_equal FIX[:work], final_payload.dig("data", "section_id")
    assert_nil final_payload.dig("data", "parent_id")
    assert_equal 2, final_payload.dig("data", "descendant_count")
    assert_equal final_revision, store_revision
    refute_equal same_parent_revision, final_revision

    records = Tasks::Format.parse(File.read(@org, encoding: "UTF-8")).records
    plants_index = records.index { |record| record["id"] == FIX[:plants] }
    assert_equal [FIX[:plants], child_id, grandchild_id],
                 records[plants_index, 3].map { |record| record.fetch("id") }
    assert_equal FIX[:plants], records.find { |record| record["id"] == child_id }.fetch("parent")
    assert_equal child_id, records.find { |record| record["id"] == grandchild_id }.fetch("parent")

    cli_same_parent = run_cli_at(
      cli_org, cli_archive, File.join(cli_dir, "state"),
      "move", FIX[:travel], "--before", FIX[:pr]
    )
    assert cli_same_parent.fetch(:status).success?, cli_same_parent.fetch(:stderr)
    cli_cross_parent = run_cli_at(
      cli_org, cli_archive, File.join(cli_dir, "state"),
      "move", FIX[:plants], "--before", FIX[:flight]
    )
    assert cli_cross_parent.fetch(:status).success?, cli_cross_parent.fetch(:stderr)
    assert_equal without_update_stamps(after_both, FIX[:travel], FIX[:plants]),
                 without_update_stamps(File.binread(cli_org), FIX[:travel], FIX[:plants]),
                 "equivalent stable-id API and CLI placements must serialize identically apart from wall-clock stamps"

    assert_equal [FIX[:eval], FIX[:plants], FIX[:flight], FIX[:travel], FIX[:pr], FIX[:old]],
                 direct_child_ids(FIX[:work]).reject { |id| id == task_id_for_title("Cross-process sibling") }
    assert Tasks::Check.check(@org).ok?

    undo_cross = run_cli("undo")
    assert undo_cross.fetch(:status).success?, undo_cross.fetch(:stderr)
    assert_equal after_same_parent, File.binread(@org)
    assert_equal same_parent_revision, store_revision

    undo_same = run_cli("undo")
    assert undo_same.fetch(:status).success?, undo_same.fetch(:stderr)
    assert_equal before_placements, File.binread(@org)
    undone_resource = http("GET", "/api/v1/tasks/#{FIX[:plants]}")
    assert_contract_exchange(undone_resource)
    assert_resource_etag(undone_resource)
    assert_equal FIX[:home], JSON.parse(undone_resource.body).dig("data", "section_id")
    undone_child = http("GET", "/api/v1/tasks/#{child_id}")
    assert_contract_exchange(undone_child)
    assert_resource_etag(undone_child)
    assert_equal FIX[:plants], JSON.parse(undone_child.body).dig("data", "parent_id")
    undone_grandchild = http("GET", "/api/v1/tasks/#{grandchild_id}")
    assert_contract_exchange(undone_grandchild)
    assert_resource_etag(undone_grandchild)
    assert_equal child_id, JSON.parse(undone_grandchild.body).dig("data", "parent_id")

    redo_same = run_cli("redo")
    assert redo_same.fetch(:status).success?, redo_same.fetch(:stderr)
    assert_equal after_same_parent, File.binread(@org)
    assert_equal same_parent_revision, store_revision
    redo_cross = run_cli("redo")
    assert redo_cross.fetch(:status).success?, redo_cross.fetch(:stderr)
    assert_equal after_both, File.binread(@org)
    assert_equal final_revision, store_revision
    redone_resource = http("GET", "/api/v1/tasks/#{FIX[:plants]}")
    assert_contract_exchange(redone_resource)
    assert_resource_etag(redone_resource)
    assert_equal FIX[:work], JSON.parse(redone_resource.body).dig("data", "section_id")
    redone_child = http("GET", "/api/v1/tasks/#{child_id}")
    assert_contract_exchange(redone_child)
    assert_resource_etag(redone_child)
    assert_equal FIX[:plants], JSON.parse(redone_child.body).dig("data", "parent_id")
    redone_grandchild = http("GET", "/api/v1/tasks/#{grandchild_id}")
    assert_contract_exchange(redone_grandchild)
    assert_resource_etag(redone_grandchild)
    assert_equal child_id, JSON.parse(redone_grandchild.body).dig("data", "parent_id")
  ensure
    FileUtils.remove_entry(cli_dir) if cli_dir && File.directory?(cli_dir)
  end

  def test_placement_distinguishes_stale_own_edit_missing_anchor_and_moved_anchor
    collection = http("GET", "/api/v1/tasks?scope=all")
    assert_contract_exchange(collection)
    loaded = JSON.parse(collection.body).fetch("data").to_h do |task|
      [task.fetch("id"), task]
    end

    edit = run_cli("retitle", FIX[:flight], "CLI changed flight")
    assert edit.fetch(:status).success?, edit.fetch(:stderr)
    stale = http(
      "PATCH", "/api/v1/tasks/#{FIX[:flight]}",
      json: { placement: { parent_id: FIX[:home] } },
      headers: { "If-Match" => quote(loaded.fetch(FIX[:flight]).fetch("revision")) }
    )
    assert_api_error stale, "412", "stale_revision"
    assert_contract_exchange(stale)

    delete = run_cli("delete", FIX[:eval])
    assert delete.fetch(:status).success?, delete.fetch(:stderr)
    missing = http(
      "PATCH", "/api/v1/tasks/#{FIX[:pr]}",
      json: { placement: { parent_id: FIX[:work], before_id: FIX[:eval] } },
      headers: { "If-Match" => quote(loaded.fetch(FIX[:pr]).fetch("revision")) }
    )
    assert_api_error missing, "404", "not_found"
    assert_equal "placement.before_id", JSON.parse(missing.body).dig("error", "details", "field")
    assert_contract_exchange(missing)

    move_anchor = run_cli("move", FIX[:travel], "Home")
    assert move_anchor.fetch(:status).success?, move_anchor.fetch(:stderr)
    moved = http(
      "PATCH", "/api/v1/tasks/#{FIX[:pr]}",
      json: { placement: { parent_id: FIX[:work], before_id: FIX[:travel] } },
      headers: { "If-Match" => quote(loaded.fetch(FIX[:pr]).fetch("revision")) }
    )
    assert_api_error moved, "409", "conflict"
    assert_equal FIX[:home], JSON.parse(moved.body).dig("error", "details", "current_parent_id")
    assert_contract_exchange(moved)
    assert Tasks::Check.check(@org).ok?
  end

  def test_api_mutation_is_undone_by_fresh_cli_process_to_exact_bytes_and_resource
    before_bytes = File.binread(@org)
    before = http("GET", "/api/v1/tasks/#{FIX[:pr]}")
    updated = http(
      "PATCH", "/api/v1/tasks/#{FIX[:pr]}",
      json: { title: "Changed through HTTP", priority: "A" },
      headers: { "If-Match" => before["etag"] }
    )
    assert_equal "200", updated.code, updated.body
    refute_equal before_bytes, File.binread(@org)

    undo = run_cli("undo")
    assert undo.fetch(:status).success?, undo.fetch(:stderr)
    assert_equal before_bytes, File.binread(@org)
    restored = http("GET", "/api/v1/tasks/#{FIX[:pr]}")
    assert_equal JSON.parse(before.body).fetch("data"), JSON.parse(restored.body).fetch("data")
  end

  def test_temporal_cli_and_api_writes_are_mutually_visible_and_cli_undoable
    cli_due = run_cli("due", FIX[:pr], "2026-07-20 5pm", "--timezone", "Europe/London", "--json")
    assert cli_due.fetch(:status).success?, cli_due.fetch(:stderr)

    loaded = http("GET", "/api/v1/tasks/#{FIX[:pr]}")
    assert_contract_exchange(loaded)
    resource = JSON.parse(loaded.body).fetch("data")
    assert_equal "2026-07-20", resource.fetch("deadline")
    assert_equal "Europe/London", resource.dig("deadline_time", "timezone")
    assert_equal "2026-07-20T16:00:00Z", resource.dig("deadline_time", "instant")

    before_api = File.binread(@org)
    updated = http(
      "PATCH", "/api/v1/tasks/#{FIX[:pr]}",
      json: {
        scheduled: "2026-07-21",
        scheduled_time: { local: "09:30", timezone: "America/New_York", fold: 0 },
      },
      headers: { "If-Match" => loaded["etag"] }
    )
    assert_equal "200", updated.code, updated.body
    assert_contract_exchange(updated)

    shown = run_cli("show", FIX[:pr], "--json")
    assert shown.fetch(:status).success?, shown.fetch(:stderr)
    cli_resource = JSON.parse(shown.fetch(:stdout))
    assert_equal "America/New_York", cli_resource.dig("scheduled_time", "timezone")
    assert_equal "2026-07-21T13:30:00Z", cli_resource.dig("scheduled_time", "instant")

    undo = run_cli("undo")
    assert undo.fetch(:status).success?, undo.fetch(:stderr)
    assert_equal before_api, File.binread(@org)
    restored = JSON.parse(http("GET", "/api/v1/tasks/#{FIX[:pr]}").body).fetch("data")
    assert_nil restored.fetch("scheduled")
    assert_nil restored.fetch("scheduled_time")
  end

  def test_live_and_archive_external_changes_advance_refresh_token_and_reads_are_fresh
    first_revision = store_revision
    capture = run_cli("capture", "Fresh CLI task")
    assert capture.fetch(:status).success?, capture.fetch(:stderr)
    second_revision = store_revision
    refute_equal first_revision, second_revision
    list = http("GET", "/api/v1/tasks?text=Fresh%20CLI%20task")
    assert_equal ["Fresh CLI task"], JSON.parse(list.body).fetch("data").map { |task| task.fetch("title") }

    File.write(@archive, Tasks::Format.dump([
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "dddd0001", "title" => "Archive" },
      { "type" => "task", "id" => "dddd0002", "parent" => "dddd0001", "state" => "DONE",
        "title" => "External archive task", "closed" => "2026-07-01" },
    ]))
    third_revision = store_revision
    refute_equal second_revision, third_revision
    archived = http("GET", "/api/v1/tasks/dddd0002?source=archive")
    assert_equal "200", archived.code
    assert_equal "External archive task", JSON.parse(archived.body).dig("data", "title")
  end

  def test_invalid_external_edit_refuses_reads_and_mutations_without_overwrite_or_path_leak
    invalid = File.binread(@org).sub('"parent":"aaaa0003"', '"parent":"ffffffff"')
    File.binwrite(@org, invalid)

    read = http("GET", "/api/v1/tasks")
    assert_safe_store_refusal(read)
    mutation = http("POST", "/api/v1/tasks", json: { title: "Must not land" })
    assert_safe_store_refusal(mutation)
    assert_equal invalid, File.binread(@org)
    refute_includes server_log, @dir
  end

  private

  def http(method, path, json: nil, headers: {})
    request_class = Net::HTTP.const_get(method.capitalize)
    request = request_class.new(path)
    headers.each { |name, value| request[name] = value }
    if json
      request["Content-Type"] = "application/json"
      request.body = JSON.generate(json)
    end
    response = Net::HTTP.start("127.0.0.1", @port) { |client| client.request(request) }
    response.instance_variable_set(
      :@tasks_contract_request,
      { method: method, path: path, json: json, headers: headers }
    )
    response
  end

  def store_revision
    response = http("GET", "/api/v1/meta")
    assert_equal "200", response.code, response.body
    JSON.parse(response.body).dig("meta", "store_revision")
  end

  def run_cli(*args)
    stdout, stderr, status = Open3.capture3(@env, RbConfig.ruby, TASKS_BIN, *args, chdir: ROOT)
    { stdout: stdout, stderr: stderr, status: status }
  end

  def run_cli_at(org, archive, state, *args)
    env = { "TASKS_FILE" => org, "TASKS_ARCHIVE" => archive, "XDG_STATE_HOME" => state }
    stdout, stderr, status = Open3.capture3(env, RbConfig.ruby, TASKS_BIN, *args, chdir: ROOT)
    { stdout: stdout, stderr: stderr, status: status }
  end

  def direct_child_ids(parent_id)
    Tasks::Format.parse(File.read(@org, encoding: "UTF-8")).records.filter_map do |record|
      record["id"] if record["type"] == "task" && record["parent"] == parent_id
    end
  end

  def task_id_for_title(title)
    Tasks::Format.parse(File.read(@org, encoding: "UTF-8")).records
      .find { |record| record["title"] == title }
      .fetch("id")
  end

  def without_update_stamps(bytes, *ids)
    records = Tasks::Format.parse(bytes).records.map do |record|
      next record unless ids.include?(record["id"])

      record.except("updated", "line")
    end
    Tasks::Format.dump(records.map { |record| record.except("line") })
  end

  def assert_api_error(response, status, code)
    assert_equal status, response.code, response.body
    assert_equal code, JSON.parse(response.body).dig("error", "code")
  end

  def assert_resource_etag(response)
    assert_equal quote(JSON.parse(response.body).dig("data", "revision")), response["etag"]
  end

  def assert_contract_exchange(response)
    request_data = response.instance_variable_get(:@tasks_contract_request)
    path = request_data.fetch(:path).sub(%r{\A/api/v1}, "")
    json = request_data[:json]
    headers = request_data.fetch(:headers)
    env = { method: request_data.fetch(:method) }
    unless json.nil?
      env[:input] = JSON.generate(json)
      env["CONTENT_TYPE"] = "application/json"
    end
    env["HTTP_IF_MATCH"] = headers["If-Match"] if headers["If-Match"]
    rack_request = Rack::Request.new(Rack::MockRequest.env_for(path, env))

    validated_request = @definition.validate_request(rack_request)
    assert validated_request.valid?,
           "#{request_data[:method]} #{request_data[:path]} request: #{validated_request.error&.message}"

    rack_response = Rack::Response.new(
      response.body.empty? ? [] : [response.body], response.code.to_i,
      response.each_header.to_h
    )
    validated_response = @definition.validate_response(rack_request, rack_response)
    assert validated_response&.valid?,
           "#{request_data[:method]} #{request_data[:path]} response: " \
           "#{validated_response&.error&.message || "not matched"}"
  end

  def quote(value) = %Q("#{value}")

  def assert_safe_store_refusal(response)
    assert_equal "503", response.code
    payload = JSON.parse(response.body)
    assert_equal "store_invalid", payload.dig("error", "code")
    refute_match(/#{Regexp.escape(@dir)}|backtrace|exception/i, response.body)
  end

  def available_port
    server = TCPServer.new("127.0.0.1", 0)
    server.addr[1]
  ensure
    server&.close
  end

  def wait_for_server
    Timeout.timeout(10) do
      loop do
        begin
          return if http("GET", "/healthz").code == "200"
        rescue Errno::ECONNREFUSED, Errno::EHOSTUNREACH
          raise "tasks-api exited during startup\n#{server_log}" unless process_alive?(@pid)
          sleep 0.05
        end
      end
    end
  end

  def stop_server(signal = "TERM")
    return @stop_status unless @pid

    Process.kill(signal, @pid)
    Timeout.timeout(5) { _, @stop_status = Process.wait2(@pid) }
    @pid = nil
    @stop_status
  rescue Errno::ESRCH, Errno::ECHILD
    @pid = nil
    @stop_status
  rescue Timeout::Error
    Process.kill("KILL", @pid)
    _, @stop_status = Process.wait2(@pid)
    @pid = nil
    @stop_status
  end

  def process_alive?(pid)
    Process.kill(0, pid)
    true
  rescue Errno::ESRCH
    false
  end

  def server_log
    @log.flush
    File.read(@log.path, encoding: "UTF-8")
  end
end
