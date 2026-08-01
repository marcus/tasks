# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "rbconfig"
require "json"
require "fileutils"
require "tmpdir"

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
          # Real td refuses an edge that would close a cycle, and the generator
          # has to reorder edges without ever presenting one. A fake that
          # accepted anything would hide that.
          reaches = lambda do |from, target|
            seen = []
            queue = [from]
            until queue.empty?
              node = queue.shift
              next if seen.include?(node)
              seen << node
              return true if node == target
              queue.concat(state["issues"].fetch(node, {}).fetch("dependencies", []))
            end
            false
          end
          abort "cycle: #{args[3]} already depends on #{args[2]}" if reaches.call(args[3], args[2])
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

  # Slices whose corpus is still missing. Each projects a second issue — the
  # fixture gap — that the slice depends on, so `td ready` never offers a slice
  # that manifest.md forbids passing `characterizing`.
  def gated_records = manifest_records.reject { |s| s["fixtures_todo"].to_s.strip.empty? }
  def issue_count = manifest_records.size + campaign_records.size + gated_records.size

  # td issues indexed by the label that is their identity, and only that: a gate
  # issue carries `campaign:N` as membership, so a flat scan would let it
  # masquerade as its own epic.
  def by_identity
    state_issues.values.to_h do |issue|
      labels = issue["labels"]
      key = if labels.include?("porting-campaign") then labels.find { |l| l.start_with?("campaign:") }
            elsif labels.include?("porting-slice") then labels.find { |l| l.start_with?("slice:") }
            else labels.find { |l| l.start_with?("fixture-gate:") }
            end
      [key, issue]
    end
  end

  # Runs the script against a manifest that is not the tracked one.
  def with_manifest(slices)
    Dir.mktmpdir("manifest-alt") do |dir|
      path = File.join(dir, "manifest.jsonl")
      File.write(path, slices.map { |s| JSON.generate(s) }.join("\n") + "\n")
      yield({ "MANIFEST_ISSUES_MANIFEST" => path })
    end
  end

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

  # A whole-file oracle claim is unfalsifiable: the file exists, so validation
  # passes, and nobody can tell which of its tests are the slice's oracle and
  # which merely live nearby. test/test_config.rb has 70 tests; a slice claiming
  # the file claims all of them, including the ones proving something else.
  def test_a_whole_file_oracle_claim_is_rejected
    slices = manifest_records
    victim = slices.first
    victim["ruby_tests"] = ["test/test_config.rb"]

    with_manifest(slices) do |env|
      stdout, _stderr, status = Open3.capture3(env, RbConfig.ruby, SCRIPT, "validate")
      refute status.success?, "a bare test-file reference validated"
      assert_includes stdout, "whole-file claim"
      assert_includes stdout, victim["id"]
    end
  end

  # Existence is a weak check: a real test that proves another slice's behavior
  # passes it. The machine-detectable half of that is reachability — a test
  # driving a mutation verb owned by a slice that is not upstream cannot pass at
  # the referencing slice's position, however good the port is.
  def test_reach_flags_an_oracle_the_slice_cannot_run_and_accepts_an_explained_one
    slices = manifest_records
    victim = slices.find { |s| s["id"] == "format-parse" }
    # delete_task! is delete-task's, three campaigns downstream of format-parse.
    victim["ruby_tests"] += ["test/test_delete_task.rb#test_leaf_delete_removes_only_the_target_and_records_one_journal_entry"]

    with_manifest(slices) do |env|
      stdout, _stderr, status = Open3.capture3(env, RbConfig.ruby, SCRIPT, "reach", "--json")
      refute status.success?, "an unreachable oracle passed reach"
      row = JSON.parse(stdout)["reaches"].find { |r| r["slice"] == "format-parse" }
      refute_nil row
      assert_includes row["verbs"], "delete_task!"
      assert_includes row["owners"], "delete-task"
      refute row["explained"]
    end

    # Naming the test in oracle_gaps is the escape hatch, and it is the whole
    # point: the record says why the ref stays.
    victim["oracle_gaps"] += ["test_leaf_delete_removes_only_the_target_and_records_one_journal_entry " \
                              "reaches delete-task; kept deliberately for this test."]
    with_manifest(slices) do |env|
      _stdout, _stderr, status = Open3.capture3(env, RbConfig.ruby, SCRIPT, "reach")
      assert status.success?, "an explained reach still failed"
    end
  end

  # The committed manifest holds no unexplained reach.
  def test_the_manifest_has_no_unexplained_unreachable_oracles
    stdout, stderr, status = Open3.capture3(RbConfig.ruby, SCRIPT, "reach")
    assert status.success?, "unexplained unreachable oracles:\n#{stdout}#{stderr}"
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
      total = issue_count

      first = sync(bin)
      assert_equal total, first["summary"]["create"]
      assert_equal 0, first["summary"].fetch("update", 0)
      assert_equal total, state_issues.size
      refute_empty drain_log.grep(MUTATING)

      second = sync(bin)
      assert_equal total, second["summary"]["skip"]
      assert_equal 0, second["summary"].fetch("create", 0)
      assert_equal 0, second["summary"].fetch("update", 0)
      assert_equal 0, second["summary"].fetch("dep", 0)
      assert_equal 0, second["summary"].fetch("orphan", 0)
      assert_empty drain_log.grep(MUTATING), "the second run wrote to td"
      assert_equal total, state_issues.size, "the second run created duplicates"
    end
  end

  def test_every_slice_gets_its_issue_labels_parent_and_dependency_edges
    with_fake_td do |bin|
      sync(bin)
      by_label = by_identity

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

        wanted = slice["depends_on"].map { |d| by_label.fetch("slice:#{d}")["id"] }
        gate = by_label["fixture-gate:#{slice["id"]}"]
        wanted << gate["id"] if gate
        assert_equal wanted.sort, issue["dependencies"].sort, "wrong edges on #{slice["id"]}"
      end
    end
  end

  # `td ready` lists open issues whose dependencies are not met. A slice whose
  # `fixtures_todo` is non-null cannot pass `characterizing` (manifest.md), so
  # advertising it would send a claiming agent at work it must hand straight
  # back. The gap is projected as its own issue and the slice depends on it.
  def test_a_slice_with_a_missing_corpus_is_gated_behind_its_fixture_gap
    with_fake_td do |bin|
      sync(bin)
      by_label = by_identity

      refute_empty gated_records, "this test needs at least one slice with a fixtures_todo"

      gated_records.each do |slice|
        gate = by_label.fetch("fixture-gate:#{slice["id"]}", nil)
        refute_nil gate, "no fixture-gap issue for #{slice["id"]}"
        assert_includes gate["labels"], "porting-fixture-gate"
        refute_includes gate["labels"], "porting-slice", "a gap issue must not look like a slice"
        assert_includes gate["description"], slice["fixtures_todo"]
        assert_equal by_label.fetch("campaign:#{slice["campaign"]}")["id"], gate["parent_id"]

        issue = by_label.fetch("slice:#{slice["id"]}")
        assert_includes issue["dependencies"], gate["id"], "#{slice["id"]} is not gated on its gap"
      end

      manifest_records.each do |slice|
        next unless slice["fixtures_todo"].to_s.strip.empty?
        assert_nil by_label["fixture-gate:#{slice["id"]}"],
                   "#{slice["id"]} has its fixtures wired but still has a gap issue"
      end
    end
  end

  # The gate is temporary by construction: the moment a slice's corpus lands and
  # `fixtures_todo` goes null, the next sync drops the edge and reports the gap
  # issue as an orphan rather than closing it — it holds the record of the work.
  def test_closing_a_fixture_gap_removes_the_edge_and_orphans_the_gap_issue
    with_fake_td do |bin|
      sync(bin)
      slice = gated_records.first
      gate_id = by_identity.fetch("fixture-gate:#{slice["id"]}")["id"]
      drain_log

      cleared = manifest_records.map do |s|
        s = s.dup
        s["fixtures_todo"] = nil if s["id"] == slice["id"]
        JSON.generate(s)
      end

      Dir.mktmpdir("manifest-cleared") do |dir|
        path = File.join(dir, "manifest.jsonl")
        File.write(path, "#{cleared.join("\n")}\n")
        env = { "TD_BIN" => bin, "FAKE_TD_STATE" => @state, "FAKE_TD_LOG" => @log,
                "MANIFEST_ISSUES_MANIFEST" => path }
        stdout, stderr, status = Open3.capture3(env, RbConfig.ruby, SCRIPT, "sync", "--json")
        assert status.success?, "sync failed: #{stderr}#{stdout}"
        report = JSON.parse(stdout)

        edge = report["actions"].find { |a| a["action"] == "dep" && a["target"] == "slice #{slice["id"]}" }
        refute_nil edge, "no edge change for #{slice["id"]}"
        assert_includes edge["detail"], "-#{gate_id}"
        orphan = report["actions"].find { |a| a["action"] == "orphan" && a["target"] == "fixture-gate:#{slice["id"]}" }
        refute_nil orphan, "the stale gap issue was not reported"
        assert_equal gate_id, orphan["td_id"]
      end

      refute_includes state_issues.values.find { |i| i["labels"].include?("slice:#{slice["id"]}") }["dependencies"],
                      gate_id
      refute_nil state_issues[gate_id], "the gap issue was deleted rather than reported"
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

  # `source_paths` names the Ruby a slice ports; the behavior it must reproduce
  # is produced by that code plus everything it requires. Watching only the
  # former means a change to Recur::DAYS can turn a fixture red while `drift`
  # reports nothing — which is the drift rule failing silently, the one way it
  # must not fail.
  def test_drift_watches_the_transitive_require_closure_not_just_source_paths
    stdout, stderr, status = Open3.capture3(RbConfig.ruby, SCRIPT, "closure", "--json")
    assert status.success?, stderr
    rows = JSON.parse(stdout)["slices"].to_h { |r| [r["id"], r] }

    fields = rows.fetch("check-task-fields")
    assert_equal ["lib/tasks/check.rb"], fields["source_paths"]
    # check.rb validates the recur cookie through Recur and the `updated` value
    # through UpdateStamp. Neither is ported by this slice; both can break it.
    assert_includes fields["watched"], "lib/tasks/recur.rb"
    assert_includes fields["watched"], "lib/tasks/update_stamp.rb"
    assert_includes fields["watched"], "lib/tasks/lead.rb"

    rows.each_value do |row|
      assert_operator row["watched"].size, :>=, row["source_paths"].size
      assert_equal row["source_sha"], row["last_touch"],
                   "#{row["id"]} is pinned to something other than its closure's last-touch " \
                   "commit, so drift will fire on the next run"
    end

    # The closure is what makes coverage checkable at all: every lib/tasks file
    # outside the HTTP API is inside some slice's closure, and the API is the
    # honest exception (no campaign 2-4 slice ports it).
    covered = rows.values.flat_map { |r| r["watched"] }.uniq
    uncovered = Dir[File.expand_path("../lib/tasks/**/*.rb", __dir__)]
                .map { |p| p.delete_prefix("#{File.expand_path("..", __dir__)}/") } - covered
    assert_equal ["lib/tasks/api/app.rb", "lib/tasks/api/errors.rb",
                  "lib/tasks/api/representation.rb"], uncovered.sort
  end

  # Identity is a label. Two issues carrying one identity label is not a state
  # to resolve quietly: last-one-wins repoints every dependency edge at the
  # newcomer and orphans the issue an agent may already have claimed, then
  # converges and reports "nothing to do" over a permanently wrong graph.
  def test_a_duplicate_identity_label_stops_the_sync_instead_of_stealing_the_slice
    with_fake_td do |bin|
      sync(bin)
      state = JSON.parse(File.read(@state))
      original = state["issues"].values.find { |i| i["labels"].include?("slice:tree-build") }
      impostor = original.merge("id" => "td-dupe01", "title" => "a second tree-build")
      state["issues"]["td-dupe01"] = impostor
      File.write(@state, JSON.generate(state))
      drain_log

      stdout, _stderr, status = run_script(bin, "sync", "--json")
      refute status.success?, "sync accepted two issues carrying slice:tree-build"
      report = JSON.parse(stdout)
      refute report["ok"]
      assert_includes report["error"], "slice:tree-build"
      assert_includes report["error"], original["id"]
      assert_includes report["error"], "td-dupe01"
      assert_empty drain_log.grep(MUTATING), "a refused sync still wrote to td"
    end
  end

  # An edge is ours to remove if it points at an issue this script owns — a
  # retired slice's issue included — or at an id td no longer resolves at all.
  # Neither can ever be satisfied, and in a fleet whose ready-work query is a
  # dependency query, an unsatisfiable edge means permanently unstartable.
  def test_edges_to_retired_and_deleted_issues_are_removed
    with_fake_td do |bin|
      sync(bin)
      state = JSON.parse(File.read(@state))
      slice = state["issues"].values.find { |i| i["labels"].include?("slice:tree-build") }
      # What a slice rename leaves behind: the retired issue still carries the
      # fleet label, and a dependent still points at it.
      state["issues"]["td-retire"] = {
        "id" => "td-retire", "title" => "port tree-build (retired)", "type" => "task",
        "priority" => "P2", "points" => 3, "parent_id" => "",
        "labels" => %w[porting porting-slice slice:tree-build-old], "description" => "",
        "acceptance" => "", "status" => "open", "dependencies" => []
      }
      slice["dependencies"] |= ["td-retire", "td-deleted"]
      File.write(@state, JSON.generate(state))
      drain_log

      report = sync(bin)
      edge = report["actions"].find { |a| a["action"] == "dep" && a["target"] == "slice tree-build" }
      refute_nil edge, "the stale edges were not touched"
      assert_includes edge["detail"], "-td-retire"
      assert_includes edge["detail"], "-td-deleted", "an edge to a deleted issue was left in place"

      left = state_issues.values.find { |i| i["labels"].include?("slice:tree-build") }["dependencies"]
      refute_includes left, "td-retire"
      refute_includes left, "td-deleted"
      assert_includes report["actions"].map { |a| a["target"] }, "slice:tree-build-old"
      refute_nil state_issues["td-retire"], "the retired issue was deleted rather than reported"
    end
  end

  # Reversing an edge — B stops depending on A, A starts depending on B — is a
  # transient cycle if the add is attempted before the removal. td refuses such
  # an edge, so a generator that writes per slice reports success having quietly
  # not written one, and needs a second run to converge. Idempotency is not just
  # "the second run does nothing"; it is "the first run does everything".
  def test_reversing_a_dependency_converges_in_one_run
    with_fake_td do |bin|
      sync(bin)
      drain_log

      slices = manifest_records
      a = slices.find { |s| s["id"] == "update-stamp" }
      b = slices.find { |s| s["id"] == "create-basic" }
      # As seeded: create-basic depends on update-stamp. Flip it.
      b["depends_on"] -= ["update-stamp"]
      a["depends_on"] = (a["depends_on"] + ["create-basic"]).uniq

      with_manifest(slices) do |env|
        env = env.merge("TD_BIN" => bin, "FAKE_TD_STATE" => @state, "FAKE_TD_LOG" => @log)
        stdout, stderr, status = Open3.capture3(env, RbConfig.ruby, SCRIPT, "sync", "--json")
        assert status.success?, "sync failed: #{stderr}#{stdout}"

        by = by_identity
        assert_includes by.fetch("slice:update-stamp")["dependencies"],
                        by.fetch("slice:create-basic")["id"],
                        "the reversed edge was refused and not retried"
        refute_includes by.fetch("slice:create-basic")["dependencies"],
                        by.fetch("slice:update-stamp")["id"]

        drain_log
        _out, _err, again = Open3.capture3(env, RbConfig.ruby, SCRIPT, "sync", "--json")
        assert again.success?
        assert_empty drain_log.grep(MUTATING), "a second run still had edges to write"
      end
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
