# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "rbconfig"
require "json"

# porting/manifest-issues projects porting/manifest.jsonl into td. Its one
# non-negotiable property is idempotency: the fleet runs it whenever the
# manifest changes, and a second run must create nothing, update nothing, and
# touch no dependency edge. Proving that against the real td database would
# mean writing to it, so td is replaced here by a recording double that speaks
# the same six invocations the script uses — which also proves the script uses
# only those six.
class TestManifestIssues < Minitest::Test
  SCRIPT = File.expand_path("../porting/manifest-issues", __dir__)
  MANIFEST = File.expand_path("../porting/manifest.jsonl", __dir__)
  CAMPAIGNS = File.expand_path("../porting/campaigns.jsonl", __dir__)

  # A stand-in `td`: a tiny issue database in one JSON file, plus a log of every
  # invocation, so a test can assert on what was *not* run as well as on state.
  FAKE_TD = <<~'RUBY'
    #!/usr/bin/env ruby
    require "json"
    state_path = ENV.fetch("FAKE_TD_STATE")
    log_path = ENV.fetch("FAKE_TD_LOG")
    state = File.exist?(state_path) ? JSON.parse(File.read(state_path)) : { "issues" => {}, "seq" => 0 }
    args = ARGV.dup
    args.shift(2) if args[0] == "--work-dir"
    File.open(log_path, "a") { |f| f.puts(args.join(" ")) }

    def flag(args, name)
      i = args.index(name)
      i ? args[i + 1] : nil
    end

    save = lambda { File.write(state_path, JSON.generate(state)) }

    case args[0]
    when "list"
      label = flag(args, "--labels")
      matching = state["issues"].values.select { |i| i["labels"].include?(label) }
      puts JSON.generate(matching)
    when "show"
      issue = state["issues"][args[1]]
      abort "no such issue #{args[1]}" unless issue
      puts JSON.generate(issue)
    when "create"
      state["seq"] += 1
      id = format("td-f%05d", state["seq"])
      issue = {
        "id" => id,
        "title" => flag(args, "--title"),
        "type" => flag(args, "--type"),
        "priority" => flag(args, "--priority"),
        "points" => flag(args, "--points").to_i,
        "labels" => flag(args, "--labels").to_s.split(","),
        "parent_id" => flag(args, "--parent").to_s,
        "description" => File.read(flag(args, "--description-file")),
        "acceptance" => File.read(flag(args, "--acceptance-file")),
        "status" => "open",
        "dependencies" => [],
      }
      state["issues"][id] = issue
      save.call
      puts JSON.generate({ "action" => "created", "id" => id, "issue" => issue })
    when "update"
      issue = state["issues"][args[1]]
      abort "no such issue #{args[1]}" unless issue
      issue["title"] = flag(args, "--title") if args.include?("--title")
      issue["priority"] = flag(args, "--priority") if args.include?("--priority")
      issue["points"] = flag(args, "--points").to_i if args.include?("--points")
      issue["labels"] = flag(args, "--labels").to_s.split(",") if args.include?("--labels")
      issue["parent_id"] = flag(args, "--parent") if args.include?("--parent")
      issue["description"] = File.read(flag(args, "--description-file")) if args.include?("--description-file")
      issue["acceptance"] = File.read(flag(args, "--acceptance-file")) if args.include?("--acceptance-file")
      save.call
      puts JSON.generate({ "action" => "updated", "id" => issue["id"] })
    when "dep"
      if args[1] == "add" || args[1] == "rm"
        issue = state["issues"][args[2]]
        abort "no such issue #{args[2]}" unless issue
        if args[1] == "add"
          issue["dependencies"] |= [args[3]]
        else
          issue["dependencies"] -= [args[3]]
        end
        save.call
        puts "#{args[1]} ok"
      else
        issue = state["issues"][args[1]]
        abort "no such issue #{args[1]}" unless issue
        puts JSON.generate({ "dependencies" => issue["dependencies"], "issue" => issue })
      end
    else
      abort "fake td does not implement #{args[0]}"
    end
  RUBY

  MUTATING = /\A(create|update|dep (add|rm))\b/

  def with_fake_td
    Dir.mktmpdir("fake-td") do |dir|
      bin = File.join(dir, "td")
      File.write(bin, FAKE_TD)
      File.chmod(0o755, bin)
      @state = File.join(dir, "state.json")
      @log = File.join(dir, "log.txt")
      yield bin
    end
  end

  def run_script(bin, *args)
    env = { "TD_BIN" => bin, "FAKE_TD_STATE" => @state, "FAKE_TD_LOG" => @log }
    stdout, stderr, status = Open3.capture3(env, RbConfig.ruby, SCRIPT, *args)
    [stdout, stderr, status]
  end

  # Invocations the fake saw since the last call, so a run's side effects can be
  # asserted independently of the ones before it.
  def drain_log
    lines = File.exist?(@log) ? File.readlines(@log, chomp: true) : []
    File.write(@log, "")
    lines
  end

  def state_issues
    File.exist?(@state) ? JSON.parse(File.read(@state))["issues"] : {}
  end

  def manifest_records = File.readlines(MANIFEST, chomp: true).reject(&:empty?).map { |l| JSON.parse(l) }
  def campaign_records = File.readlines(CAMPAIGNS, chomp: true).reject(&:empty?).map { |l| JSON.parse(l) }

  def sync(bin)
    stdout, stderr, status = run_script(bin, "sync", "--json")
    assert status.success?, "sync failed: #{stderr}#{stdout}"
    JSON.parse(stdout)
  end

  def test_manifest_validates_against_the_repository
    stdout, stderr, status = Open3.capture3(RbConfig.ruby, SCRIPT, "validate")
    assert status.success?, "validate failed: #{stderr}#{stdout}"
    assert_includes stdout, "every source path and oracle test resolves"
  end

  def test_plan_writes_nothing
    with_fake_td do |bin|
      stdout, stderr, status = run_script(bin, "plan")
      assert status.success?, stderr
      assert_includes stdout, "CREATE"
      assert_empty state_issues, "a dry run created issues"
      assert_empty drain_log.grep(MUTATING), "a dry run ran a mutating td command"
    end
  end

  def test_second_run_is_a_no_op
    with_fake_td do |bin|
      slices = manifest_records
      campaigns = campaign_records

      first = sync(bin)
      assert_equal slices.size + campaigns.size, first["summary"]["create"]
      assert_equal 0, first["summary"].fetch("update", 0)
      assert_equal slices.size + campaigns.size, state_issues.size
      refute_empty drain_log.grep(MUTATING)

      second = sync(bin)
      assert_equal slices.size + campaigns.size, second["summary"]["skip"]
      assert_equal 0, second["summary"].fetch("create", 0)
      assert_equal 0, second["summary"].fetch("update", 0)
      assert_equal 0, second["summary"].fetch("dep", 0)
      assert_equal 0, second["summary"].fetch("orphan", 0)
      assert_empty drain_log.grep(MUTATING), "the second run wrote to td"
      assert_equal slices.size + campaigns.size, state_issues.size, "the second run created duplicates"
    end
  end

  def test_every_slice_gets_its_issue_labels_parent_and_dependency_edges
    with_fake_td do |bin|
      sync(bin)
      issues = state_issues.values
      by_label = issues.to_h { |i| [i["labels"].find { |l| l.start_with?("slice:", "campaign:") }, i] }

      campaign_records.each do |campaign|
        epic = by_label.fetch("campaign:#{campaign["campaign"]}")
        assert_equal "epic", epic["type"]
      end

      manifest_records.each do |slice|
        issue = by_label.fetch("slice:#{slice["id"]}", nil)
        refute_nil issue, "no issue for slice #{slice["id"]}"
        assert_includes issue["labels"], "risk:#{slice["risk"]}"
        assert_includes issue["labels"], "porting"
        assert_equal by_label.fetch("campaign:#{slice["campaign"]}")["id"], issue["parent_id"]
        assert_operator issue["title"].length, :<=, 200

        wanted = slice["depends_on"].map { |d| by_label.fetch("slice:#{d}")["id"] }.sort
        assert_equal wanted, issue["dependencies"].sort, "wrong edges on #{slice["id"]}"
      end
    end
  end

  def test_a_drifted_issue_is_updated_and_then_settles
    with_fake_td do |bin|
      sync(bin)
      drain_log

      # Somebody retitled the issue and dropped its risk label in td. The
      # manifest is the source of truth, so the next sync puts both back.
      state = JSON.parse(File.read(@state))
      target = state["issues"].values.find { |i| i["labels"].include?("slice:format-parse") }
      target["title"] = "renamed by hand"
      target["labels"] -= ["risk:medium"]
      target["labels"] |= ["flaky"]
      File.write(@state, JSON.generate(state))

      second = sync(bin)
      assert_equal 1, second["summary"]["update"]
      assert_equal 0, second["summary"].fetch("create", 0)
      updated = second["actions"].find { |a| a["action"] == "update" }
      assert_equal "slice format-parse", updated["target"]

      restored = state_issues.values.find { |i| i["labels"].include?("slice:format-parse") }
      assert_includes restored["labels"], "risk:medium"
      assert_includes restored["labels"], "flaky", "a label the manifest does not own was dropped"
      refute_equal "renamed by hand", restored["title"]

      drain_log
      third = sync(bin)
      assert_equal 0, third["summary"].fetch("update", 0)
      assert_empty drain_log.grep(MUTATING)
    end
  end

  def test_progress_is_generated_from_the_manifest
    stdout, stderr, status = Open3.capture3(RbConfig.ruby, SCRIPT, "progress", "--json")
    assert status.success?, stderr
    report = JSON.parse(stdout)
    slices = manifest_records
    assert_equal slices.size, report["total"]
    assert_equal slices.map { |s| s["status"] }.tally, report["by_status"]
    slices.group_by { |s| s["campaign"] }.each do |number, group|
      assert_equal group.size, report["by_campaign"][number.to_s]["total"]
    end
  end

  def test_no_hand_written_percentage_ships_beside_the_manifest
    # Progress is generated. A literal percentage in the porting tree's prose
    # would be a second, rotting copy of it.
    offenders = Dir[File.expand_path("../porting/**/*.md", __dir__)].select do |path|
      File.read(path).match?(/\b\d{1,3}\s?%|\b\d+\s*(?:of|\/)\s*\d+\s+slices\b/)
    end
    assert_empty offenders, "hand-written progress found in #{offenders.join(", ")}"
  end
end
