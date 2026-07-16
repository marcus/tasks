# frozen_string_literal: true

require_relative "test_helper"
require "json"
require "open3"

# CLI adapter coverage for the Projects feature (`projects` / `project <verb>`).
# Every case shells out to bin/tasks in a sandbox seeded with PROJECTS_FIXTURE,
# so it exercises real dispatch, ref resolution, exit codes, and output — the
# ProjectView rollups and Store mutations themselves live in test_projects.rb.
class TestCliProjects < Minitest::Test
  BIN = File.expand_path("../bin/tasks", __dir__)

  # Run bin/tasks against a PROJECTS_FIXTURE sandbox; yields [org, out, err, st].
  def run_cli(*args, content: PROJECTS_FIXTURE)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, content)
      env = { "TASKS_FILE" => org, "TASKS_ARCHIVE" => archive }
      out, err, st = Open3.capture3(env, "ruby", BIN, *args)
      yield org, out.force_encoding("UTF-8"), err.force_encoding("UTF-8"), st, archive
    end
  end

  # -- projects (list) -------------------------------------------------------

  def test_projects_text_lists_projects_then_areas_with_counts_and_stuck
    run_cli("projects") do |_org, out, _err, st|
      assert st.success?
      assert_match(/Projects/, out)
      assert_match(/Areas/, out)
      assert_match(/Site launch\s+3 open · 1 next · next 7\/25/, out)
      assert_match(/Empty project\s+0 open · 0 next\s+\(stuck\)/, out)
      assert_match(/Stuck reno\s+1 open · 0 next\s+\(stuck\)/, out)
      assert_match(/Tasks\s+2 open · 1 next/, out)
      # Projects sort ahead of areas, dated project ahead of dateless ones.
      assert_operator out.index("Site launch"), :<, out.index("Empty project")
      assert_operator out.index("Empty project"), :<, out.index("Stuck reno")
      assert_operator out.index("Projects"), :<, out.index("Areas")
    end
  end

  def test_projects_json_is_an_array_of_project_objects
    run_cli("projects", "--json") do |_org, out, _err, st|
      assert st.success?
      rows = JSON.parse(out)
      assert_equal [PFIX[:site], PFIX[:empty], PFIX[:reno], PFIX[:tasks]], rows.map { |r| r["id"] }
      assert_equal %w[project project project area], rows.map { |r| r["kind"] }
      site = rows.first
      assert_equal 3, site["open_count"]
      assert_equal "2026-07-25", site["next_date"]
      assert_equal [PFIX[:site_next], PFIX[:site_todo], PFIX[:site_sub_task]], site["task_ids"]
      # nil-valued keys are omitted (an area has no parent_id / next_date / body).
      refute rows.last.key?("parent_id")
      refute rows.last.key?("next_date")
    end
  end

  def test_projects_rejects_unknown_flag
    run_cli("projects", "--bogus") do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/unknown flag: --bogus/, err)
    end
  end

  # -- project show ----------------------------------------------------------

  def test_project_show_text_and_json
    run_cli("project", "show", "Site launch") do |_org, out, _err, st|
      assert st.success?
      assert_match(/Site launch\s+\[project\]/, out)
      assert_match(/id:\s+#{PFIX[:site]}/, out)
      assert_match(/3 open · 1 next · next 7\/25/, out)
      assert_match(/Goal: ship the personal site\./, out)
    end
    run_cli("project", "show", PFIX[:tasks], "--json") do |_org, out, _err, st|
      assert st.success?
      view = JSON.parse(out)
      assert_equal "area", view["kind"]
      assert_equal PFIX[:tasks], view["id"]
    end
  end

  # -- ref resolution failures (exit 2) --------------------------------------

  def test_show_no_match_exits_2
    run_cli("project", "show", "nonesuch") do |_org, _out, err, st|
      assert_equal 2, st.exitstatus
      assert_match(/no match: nonesuch/, err)
    end
  end

  def test_show_ambiguous_lists_candidates_exit_2
    # "s" appears in several project/area titles (Site launch, Tasks, Stuck reno).
    run_cli("project", "show", "s") do |_org, _out, err, st|
      assert_equal 2, st.exitstatus
      assert_match(/ambiguous: s/, err)
      assert_match(/L\d+: Site launch/, err)
      assert_match(/L\d+: Stuck reno/, err)
    end
  end

  # -- project rename --------------------------------------------------------

  def test_project_rename_retitles_the_section
    run_cli("project", "rename", "Stuck reno", "Kitchen reno") do |org, out, _err, st|
      assert st.success?
      assert_match(/renamed "Stuck reno" → "Kitchen reno"/, out)
      assert_equal "Kitchen reno", record_for(org, title: "Kitchen reno")["title"]
      assert_nil record_for(org, title: "Stuck reno")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_project_rename_dry_run_writes_nothing
    run_cli("project", "rename", "Stuck reno", "Kitchen reno", "--dry-run") do |org, out, _err, st|
      assert st.success?
      assert_match(/would rename "Stuck reno" → "Kitchen reno"/, out)
      assert_equal PROJECTS_FIXTURE, File.read(org)
    end
  end

  def test_project_rename_json_emits_the_updated_project
    run_cli("project", "rename", PFIX[:reno], "Kitchen reno", "--json") do |_org, out, _err, st|
      assert st.success?
      view = JSON.parse(out)
      assert_equal "Kitchen reno", view["title"]
      assert_equal PFIX[:reno], view["id"]
    end
  end

  # -- project complete ------------------------------------------------------

  def test_project_complete_closes_open_tasks_and_prints_headlines
    run_cli("project", "complete", "Site launch") do |org, out, _err, st|
      assert st.success?
      assert_match(/completed "Site launch" \(closed 4\)/, out)
      # every touched task's new DONE headline prints
      assert_match(/DONE Pick a static-site generator/, out)
      assert_match(/DONE Someday: custom domain/, out)
      assert_equal "DONE", record_for(org, title: "Pick a static-site generator")["state"]
      assert_equal "DONE", record_for(org, title: "Someday: custom domain")["state"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_project_done_synonym_completes
    run_cli("project", "done", "Stuck reno") do |org, out, _err, st|
      assert st.success?
      assert_match(/completed "Stuck reno" \(closed 1\)/, out)
      assert_equal "DONE", record_for(org, title: "Measure the kitchen")["state"]
    end
  end

  def test_project_complete_dry_run_writes_nothing
    run_cli("project", "complete", "Site launch", "--dry-run") do |org, out, _err, st|
      assert st.success?
      assert_match(/would complete "Site launch": close 4 open tasks/, out)
      assert_equal PROJECTS_FIXTURE, File.read(org)
    end
  end

  def test_project_complete_json_reports_touched
    run_cli("project", "complete", "Stuck reno", "--json") do |_org, out, _err, st|
      assert st.success?
      payload = JSON.parse(out)
      assert_equal [PFIX[:reno_todo]], payload["touched"].map { |t| t["id"] }
      assert_equal "DONE", payload["touched"].first["state"]
    end
  end

  # -- project archive -------------------------------------------------------

  def test_project_archive_refuses_while_open_tasks_remain
    # Site launch has 3 rolled-up open tasks plus 1 deferred/held one; all four
    # block the sweep, and the message calls out the deferred count.
    run_cli("project", "archive", "Site launch") do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/refusing to archive "Site launch": 4 open tasks \(1 deferred\) remain/, err)
      # nothing moved
      assert_equal PROJECTS_FIXTURE, File.read(org)
    end
  end

  # A project whose only open work is deferred must still refuse without --force
  # (parity with the API and with complete's cascade, which closes held tasks).
  DEFERRED_ONLY_FIXTURE = Tasks::Format.dump([
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "dddd0001", "title" => "Projects" },
    { "type" => "section", "id" => "dddd0002", "parent" => "dddd0001", "title" => "Parked" },
    { "type" => "task", "id" => "dddd0003", "parent" => "dddd0002", "state" => "TODO",
      "title" => "Someday: revisit", "tags" => %w[defer] },
  ])

  def test_project_archive_refuses_a_deferred_only_project_without_force
    run_cli("project", "archive", "Parked", content: DEFERRED_ONLY_FIXTURE) do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/refusing to archive "Parked": 1 open task \(1 deferred\) remain/, err)
      assert_equal DEFERRED_ONLY_FIXTURE, File.read(org)
    end
  end

  def test_project_archive_force_sweeps_a_deferred_only_project
    run_cli("project", "archive", "Parked", "--force", content: DEFERRED_ONLY_FIXTURE) do |org, out, _err, st, archive|
      assert st.success?
      assert_match(/archived "Parked"/, out)
      assert_nil record_for(org, title: "Parked")
      assert_equal "Someday: revisit", record_for(archive, title: "Someday: revisit")["title"]
      assert Tasks::Check.check(org).ok?
      assert Tasks::Check.check(archive).ok?
    end
  end

  def test_project_archive_force_sweeps_the_subtree
    run_cli("project", "archive", "Site launch", "--force") do |org, out, _err, st, archive|
      assert st.success?
      assert_match(/archived "Site launch" — 6 records moved to archive.jsonl/, out)
      assert_nil record_for(org, title: "Site launch")
      assert_equal "Site launch", record_for(archive, title: "Site launch")["title"]
      assert Tasks::Check.check(org).ok?
      assert Tasks::Check.check(archive).ok?
    end
  end

  def test_project_archive_empty_project_needs_no_force
    run_cli("project", "archive", "Empty project") do |org, out, _err, st|
      assert st.success?
      assert_match(/archived "Empty project" — 1 record moved/, out)
      assert_nil record_for(org, title: "Empty project")
    end
  end

  def test_project_archive_dry_run_writes_nothing
    run_cli("project", "archive", "Empty project", "--dry-run") do |org, out, _err, st|
      assert st.success?
      assert_match(/would archive "Empty project" and its subtree/, out)
      assert_equal PROJECTS_FIXTURE, File.read(org)
    end
  end

  def test_project_archive_json_lists_moved_ids
    run_cli("project", "archive", "Empty project", "--json") do |_org, out, _err, st|
      assert st.success?
      payload = JSON.parse(out)
      assert_equal 1, payload["archived"]
      assert_equal [PFIX[:empty]], payload["moved_ids"]
    end
  end

  # -- project create --------------------------------------------------------

  def test_project_create_makes_an_empty_project_under_the_root
    run_cli("project", "create", "Mid-year Reviews") do |org, out, _err, st|
      assert st.success?
      assert_match(/created "Mid-year Reviews"/, out)
      rec = record_for(org, title: "Mid-year Reviews")
      assert_equal "section", rec["type"]
      assert_equal PFIX[:projects], rec["parent"], "filed under the Projects root"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_project_create_json_emits_the_new_project
    run_cli("project", "create", "Mid-year Reviews", "--json") do |_org, out, _err, st|
      assert st.success?
      payload = JSON.parse(out)
      assert_equal "Mid-year Reviews", payload["title"]
      assert_equal "project", payload["kind"]
      assert_equal 0, payload["open_count"]
      assert_equal true, payload["stuck"]
    end
  end

  def test_project_create_dry_run_writes_nothing
    run_cli("project", "create", "Mid-year Reviews", "--dry-run") do |org, out, _err, st|
      assert st.success?
      assert_match(/would create project "Mid-year Reviews"/, out)
      assert_equal PROJECTS_FIXTURE, File.read(org)
    end
  end

  def test_project_create_duplicate_title_exits_1
    run_cli("project", "create", "Site launch") do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/already exists/, err)
      assert_equal PROJECTS_FIXTURE, File.read(org)
    end
  end

  def test_project_create_blank_title_aborts
    run_cli("project", "create", "   ") do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(%r{usage: tasks project create}, err)
      assert_equal PROJECTS_FIXTURE, File.read(org)
    end
  end

  # The exact transcript scenario: create a brand-new project, then move a task
  # into it by name — the positional section destination now reaches a nested
  # project under the "Projects" root, which was previously impossible.
  def test_create_then_move_a_task_into_the_new_project
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, PROJECTS_FIXTURE)
      env = { "TASKS_FILE" => org, "TASKS_ARCHIVE" => archive }

      _o, _e, st = Open3.capture3(env, "ruby", BIN, "project", "create", "Mid-year Reviews")
      assert st.success?
      new_id = record_for(org, title: "Mid-year Reviews")["id"]

      out, err, st = Open3.capture3(env, "ruby", BIN, "move", "File expenses", "Mid-year Reviews")
      assert st.success?, "#{out}#{err}"
      assert_equal new_id, record_for(org, title: "File expenses")["parent"],
                   "the task lands under the new nested project"
      assert Tasks::Check.check(org).ok?

      listing, = Open3.capture3(env, "ruby", BIN, "projects")
      assert_match(%r{Mid-year Reviews\s+1 open}, listing.force_encoding("UTF-8"))
    end
  end

  # -- dispatch --------------------------------------------------------------

  def test_unknown_project_verb_aborts
    run_cli("project", "frobnicate", "x") do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/unknown project command: "frobnicate"/, err)
    end
  end
end
