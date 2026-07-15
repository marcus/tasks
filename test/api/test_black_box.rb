# frozen_string_literal: true

require "json"
require "net/http"
require "open3"
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

  def test_live_and_archive_external_changes_advance_refresh_token_and_reads_are_fresh
    first_revision = store_revision
    capture = run_cli("capture", "Fresh CLI task")
    assert capture.fetch(:status).success?, capture.fetch(:stderr)
    second_revision = store_revision
    refute_equal first_revision, second_revision
    list = http("GET", "/api/v1/tasks?text=Fresh%20CLI%20task")
    assert_equal ["Fresh CLI task"], JSON.parse(list.body).fetch("data").map { |task| task.fetch("title") }

    File.write(@archive, Tasks::Format.dump([
      { "type" => "meta", "version" => 1 },
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
    Net::HTTP.start("127.0.0.1", @port) { |client| client.request(request) }
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
