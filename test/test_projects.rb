# frozen_string_literal: true

require_relative "test_helper"
require "tasks/application"
require "tasks/task_queries"

# Phase 1 domain coverage for the Projects feature: the ProjectView read model,
# the three section/project Store mutations, capture into nested sections, and
# the Application parity seam. All reads pin `today` so availability-driven
# rollups stay deterministic.
class TestProjects < Minitest::Test
  TODAY = Date.new(2026, 7, 20)

  def with_projects_store
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, PROJECTS_FIXTURE)
      yield Tasks::Store.new(org: org, archive: archive), org, archive
    end
  end

  def queries(store)
    Tasks::TaskQueries.new(store.read_snapshot, today: TODAY)
  end

  def with_projects_application
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, PROJECTS_FIXTURE)
      yield org, archive, Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive)
      )
    end
  end

  # -- queries ---------------------------------------------------------------

  def test_projects_lists_projects_before_areas_ordered_by_date_then_title
    with_projects_store do |store, _org, _archive|
      views = queries(store).projects

      assert_equal %w[project project project area], views.map(&:kind)
      # Site launch carries the soonest date, so it sorts ahead of the two
      # dateless projects, which then order by title; the area follows all.
      assert_equal [PFIX[:site], PFIX[:empty], PFIX[:reno], PFIX[:tasks]], views.map(&:id)
    end
  end

  def test_project_view_rolls_up_open_non_deferred_descendants_across_depth
    with_projects_store do |store, _org, _archive|
      site = queries(store).project_view(PFIX[:site])

      assert_equal "project", site.kind
      assert_equal "Site launch", site.title
      assert_equal PFIX[:projects], site.parent_id
      assert_equal "Goal: ship the personal site.", site.body
      # NEXT + TODO(deadline) + the nested sub-section task; the deferred task
      # is excluded even though it is open.
      assert_equal [PFIX[:site_next], PFIX[:site_todo], PFIX[:site_sub_task]], site.task_ids
      assert_equal 3, site.open_count
      assert_equal 1, site.next_count
      assert_equal Date.new(2026, 7, 25), site.next_date
      refute site.stuck
      refute_includes site.to_h.keys, :line
    end
  end

  def test_stuck_flags_projects_without_an_open_next_including_empty_ones
    with_projects_store do |store, _org, _archive|
      by_id = queries(store).projects.to_h { |view| [view.id, view] }

      reno = by_id[PFIX[:reno]]
      assert reno.stuck
      assert_equal 1, reno.open_count
      assert_equal 0, reno.next_count

      empty = by_id[PFIX[:empty]]
      assert empty.stuck
      assert_equal 0, empty.open_count
      assert_nil empty.next_date
      assert_empty empty.task_ids
    end
  end

  def test_held_count_rolls_up_open_deferred_descendants
    with_projects_store do |store, _org, _archive|
      by_id = queries(store).projects.to_h { |view| [view.id, view] }

      # Site launch excludes its one deferred TODO from the rollup, counting it
      # as held instead; a project with no parked work reports zero.
      assert_equal 1, by_id[PFIX[:site]].held_count
      assert_equal 0, by_id[PFIX[:reno]].held_count
      assert_equal 0, by_id[PFIX[:tasks]].held_count
    end
  end

  def test_held_count_includes_inherited_hold
    # A live task under a deferred parent task is itself effectively held, so it
    # counts toward held_count, not open_count.
    fixture = Tasks::Format.dump([
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "eeee0001", "title" => "Projects" },
      { "type" => "section", "id" => "eeee0002", "parent" => "eeee0001", "title" => "Blocked" },
      { "type" => "task", "id" => "eeee0003", "parent" => "eeee0002", "state" => "TODO",
        "title" => "Parked parent", "tags" => %w[defer] },
      { "type" => "task", "id" => "eeee0004", "parent" => "eeee0003", "state" => "TODO",
        "title" => "Inherits the hold" },
    ])
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, fixture)
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      view = Tasks::TaskQueries.new(store.read_snapshot, today: TODAY).project_view("eeee0002")
      assert_equal 0, view.open_count
      assert_equal 2, view.held_count, "the deferred parent and its inheriting child both count as held"
    end
  end

  def test_area_is_an_open_top_level_section_outside_projects
    with_projects_store do |store, _org, _archive|
      tasks = queries(store).project_view(PFIX[:tasks])

      assert_equal "area", tasks.kind
      assert_nil tasks.parent_id
      assert_equal [PFIX[:tasks_next], PFIX[:tasks_todo]], tasks.task_ids
      assert_equal 1, tasks.next_count
      refute tasks.stuck
    end
  end

  def test_projects_excludes_inbox_projects_root_nested_and_done_only_sections
    with_projects_store do |store, _org, _archive|
      query = queries(store)
      ids = query.projects.map(&:id)

      refute_includes ids, PFIX[:inbox], "Inbox never lists as an area"
      refute_includes ids, PFIX[:projects], "the Projects heading itself never lists"
      refute_includes ids, PFIX[:site_sub], "a nested sub-section rolls up, never lists"
      refute_includes ids, PFIX[:donepile], "a done-only section is not an open area"

      assert_nil query.project_view(PFIX[:inbox])
      assert_nil query.project_view(PFIX[:projects])
      assert_nil query.project_view(PFIX[:site_sub])
      assert_nil query.project_view(PFIX[:donepile])
      assert_nil query.project_view(PFIX[:site_next]), "a task id is not a project"
      assert_nil query.project_view("ffffffff")
    end
  end

  # -- rename_section! -------------------------------------------------------

  def test_rename_section_retitles_and_round_trips_through_undo
    with_projects_store do |store, org, _archive|
      assert_equal PFIX[:site], store.rename_section!(id: PFIX[:site], to: "  Site launch v2  ")
      assert_equal "Site launch v2", record_for(org, title: "Site launch v2")["title"]
      assert_nil record_for(org, title: "Site launch")
      assert Tasks::Check.check(org).ok?

      assert_equal [:ok, "rename section: Site launch v2"], store.undo!
      assert_equal PFIX[:site], record_for(org, title: "Site launch")["id"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_rename_section_rejects_blank_titles_and_missing_ids
    with_projects_store do |store, org, _archive|
      assert_equal false, store.rename_section!(id: PFIX[:site], to: "   ")
      assert_equal false, store.rename_section!(id: "ffffffff", to: "Ghost")
      assert_equal "Site launch", record_for(org, title: "Site launch")["title"]
      assert_equal [:empty], store.undo!, "a refusal writes nothing and burns no history"
    end
  end

  # -- complete_project! -----------------------------------------------------

  def test_complete_project_closes_open_descendants_dropping_defer_and_recur
    with_projects_store do |store, org, _archive|
      count = store.complete_project!(id: PFIX[:site], today: TODAY)
      assert_equal 4, count, "NEXT, TODO, nested task, and the deferred task all close"

      recur = record_for(org, title: "Pick a static-site generator")
      assert_equal "DONE", recur["state"]
      assert_equal TODAY.iso8601, recur["closed"]
      refute recur.key?("recur"), "a cascaded recurring task is retired, not advanced"

      deferred = record_for(org, title: "Someday: custom domain")
      assert_equal "DONE", deferred["state"]
      refute_includes Array(deferred["tags"]), "defer"
      assert_equal TODAY.iso8601, deferred["closed"]
      assert Tasks::Check.check(org).ok?

      assert_equal [:ok, "complete project: #{PFIX[:site]}"], store.undo!
      assert_equal "NEXT", record_for(org, title: "Pick a static-site generator")["state"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_complete_project_is_a_clean_zero_when_nothing_is_open
    with_projects_store do |store, org, _archive|
      assert_equal 0, store.complete_project!(id: PFIX[:empty], today: TODAY)
      assert_equal [:empty], store.undo!, "closing nothing records no history"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_complete_project_reports_a_missing_section
    with_projects_store do |store, _org, _archive|
      assert_equal false, store.complete_project!(id: "ffffffff", today: TODAY)
    end
  end

  # -- archive_project! ------------------------------------------------------

  def test_archive_project_moves_the_subtree_and_undo_deletes_a_fresh_archive
    with_projects_store do |store, org, archive|
      refute File.exist?(archive)

      moved = store.archive_project!(id: PFIX[:site])
      assert_equal [PFIX[:site], PFIX[:site_next], PFIX[:site_todo],
                    PFIX[:site_sub], PFIX[:site_sub_task], PFIX[:site_deferred]], moved

      assert_nil record_for(org, title: "Site launch"), "swept out of the live file"
      root = record_for(archive, title: "Site launch")
      refute root.key?("parent"), "a swept section root loses its parent"
      assert_equal Date.today.iso8601, root["archived"]
      # An open task moves too — blocking is caller policy, Store is mechanical.
      assert_equal "NEXT", record_for(archive, title: "Pick a static-site generator")["state"]
      assert Tasks::Check.check(org).ok?
      assert Tasks::Check.check(archive).ok?

      assert_equal [:ok, "archive project: #{PFIX[:site]}"], store.undo!
      refute File.exist?(archive), "undo removes the archive file it created"
      assert_equal PFIX[:site], record_for(org, title: "Site launch")["id"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_archive_project_reports_a_missing_section
    with_projects_store do |store, _org, archive|
      assert_equal false, store.archive_project!(id: "ffffffff")
      refute File.exist?(archive)
    end
  end

  # -- create_section! -------------------------------------------------------

  def test_create_section_appends_a_top_level_list_at_end_of_file
    with_projects_store do |store, org, _archive|
      id = store.create_section!(title: "  Reading  ")
      rec = Tasks::Format.parse(File.read(org)).records.last
      assert_equal id, rec["id"]
      assert_equal "section", rec["type"]
      assert_equal "Reading", rec["title"], "the title is trimmed"
      refute rec.key?("parent"), "a top-level list carries no parent"
      assert Tasks::Check.check(org).ok?

      assert_equal [:ok, "create section: Reading"], store.undo!
      assert_nil record_for(org, title: "Reading")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_create_section_inserts_as_last_child_past_a_nested_subtree
    with_projects_store do |store, org, _archive|
      id = store.create_section!(title: "Launch assets", parent_id: PFIX[:site])
      records = Tasks::Format.parse(File.read(org)).records
      created = records.index { |r| r["id"] == id }
      assert_equal PFIX[:site], records[created]["parent"]
      # Every record of the Site launch subtree — including the nested
      # Copywriting sub-section and its task — precedes the new last child.
      [PFIX[:site], PFIX[:site_next], PFIX[:site_todo], PFIX[:site_sub],
       PFIX[:site_sub_task], PFIX[:site_deferred]].each do |sid|
        assert created > records.index { |r| r["id"] == sid }
      end
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_create_section_rejects_a_blank_title_and_a_missing_parent
    with_projects_store do |store, org, _archive|
      assert_equal false, store.create_section!(title: "   ")
      assert_equal false, store.create_section!(title: "Orphan", parent_id: "ffffffff")
      assert_nil record_for(org, title: "Orphan")
      assert_equal [:empty], store.undo!, "a refusal writes nothing and burns no history"
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- capture into sections -------------------------------------------------

  def test_capture_resolves_a_nested_section_by_name
    with_projects_store do |store, org, _archive|
      result = store.create_task!(Tasks::CreateTask.new(title: "Draft the FAQ", project: "Copywriting"))

      assert_equal :ok, result.status
      assert_equal PFIX[:site_sub], result.summary[:parent_id]
      assert_equal PFIX[:site_sub], record_for(org, title: "Draft the FAQ")["parent"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_capture_accepts_a_section_id_as_parent_and_appends_in_subtree
    with_projects_store do |store, org, _archive|
      result = store.create_task!(Tasks::CreateTask.new(title: "Under the heading", parent_id: PFIX[:site_sub]))

      assert_equal :ok, result.status
      assert_equal PFIX[:site_sub], result.summary[:parent_id]

      records = Tasks::Format.parse(File.read(org)).records
      section = records.index { |r| r["id"] == PFIX[:site_sub] }
      existing = records.index { |r| r["id"] == PFIX[:site_sub_task] }
      created = records.index { |r| r["title"] == "Under the heading" }
      assert created > existing, "a new section child appends after the existing subtree"
      assert created > section
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- application seam ------------------------------------------------------

  def test_application_list_and_get_project
    with_projects_application do |_org, _archive, app|
      views = app.list_projects(today: TODAY)
      assert_equal [PFIX[:site], PFIX[:empty], PFIX[:reno], PFIX[:tasks]], views.map(&:id)

      assert_equal "Site launch", app.get_project(PFIX[:site], today: TODAY).title
      assert_nil app.get_project(PFIX[:inbox], today: TODAY)
    end
  end

  def test_application_checked_project_results_carry_revision_and_not_found
    with_projects_application do |_org, _archive, app|
      listed = app.list_projects_result(today: TODAY)
      assert listed.ok?
      assert_match(/\As1\./, listed.store_revision)
      assert_equal [PFIX[:site], PFIX[:empty], PFIX[:reno], PFIX[:tasks]], listed.data.map(&:id)

      one = app.project_result(PFIX[:tasks], today: TODAY)
      assert one.ok?
      assert_equal "area", one.data.kind

      missing = app.project_result(PFIX[:donepile], today: TODAY)
      assert missing.not_found?
      assert_nil missing.data
      assert_match(/\As1\./, missing.store_revision)
    end
  end

  def test_application_rename_project_maps_outcomes
    with_projects_application do |org, _archive, app|
      ok = app.rename_project(PFIX[:reno], title: "Kitchen reno")
      assert_equal :ok, ok.status
      assert_equal [PFIX[:reno]], ok.touched_ids
      assert_equal "Kitchen reno", record_for(org, title: "Kitchen reno")["title"]

      blank = app.rename_project(PFIX[:reno], title: "   ")
      assert_equal :invalid, blank.status

      missing = app.rename_project("ffffffff", title: "Ghost")
      assert_equal :not_found, missing.status
    end
  end

  def test_application_create_project_files_under_the_existing_root
    with_projects_application do |org, _archive, app|
      result = app.create_project(title: "  Mid-year Reviews  ", today: TODAY)
      assert_equal :ok, result.status
      new_id = result.summary[:created_id]
      refute result.summary[:created_root], "the root already existed"
      assert_equal [new_id], result.touched_ids

      rec = record_for(org, title: "Mid-year Reviews")
      assert_equal PFIX[:projects], rec["parent"], "filed under the Projects root"
      assert_equal new_id, rec["id"]
      assert Tasks::Check.check(org).ok?
      assert_includes app.list_projects(today: TODAY).map(&:id), new_id,
                      "a fresh empty project lists immediately"
    end
  end

  def test_application_create_project_bootstraps_a_missing_root
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, dump_fixture([
        { "type" => "meta", "version" => 2 },
        { "type" => "section", "id" => "eeee0001", "title" => "Inbox" },
      ]))
      app = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive)
      )
      result = app.create_project(title: "Reviews", today: TODAY)
      assert_equal :ok, result.status
      assert result.summary[:created_root], "no root existed, so one was created"

      root = record_for(org, title: "Projects")
      project = record_for(org, title: "Reviews")
      refute root.key?("parent"), "the auto-created root is top-level"
      assert_equal root["id"], project["parent"]
      assert_equal [result.summary[:created_id], root["id"]], result.touched_ids,
                   "touched carries the new project and the auto-created root"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_application_create_project_rejects_blank_and_duplicate_titles
    with_projects_application do |org, _archive, app|
      assert_equal :invalid, app.create_project(title: "   ", today: TODAY).status
      # "Site launch" is an existing project; "Tasks" is an existing area —
      # both are in the project-ref candidate set, so both are duplicates.
      assert_equal :invalid, app.create_project(title: "site launch", today: TODAY).status
      assert_equal :invalid, app.create_project(title: "Tasks", today: TODAY).status
      assert_equal PROJECTS_FIXTURE, File.read(org), "no rejected create writes anything"
    end
  end

  def test_application_complete_project_reports_closed_count
    with_projects_application do |_org, _archive, app|
      done = app.complete_project(PFIX[:site], today: TODAY)
      assert_equal :ok, done.status
      assert_equal 4, done.summary[:closed]

      empty = app.complete_project(PFIX[:empty], today: TODAY)
      assert_equal :ok, empty.status
      assert_equal 0, empty.summary[:closed]

      missing = app.complete_project("ffffffff", today: TODAY)
      assert_equal :not_found, missing.status
    end
  end

  # A post-write validation rollback returns 0 closed from the Store, the same
  # value as a clean no-op. The application must not render that as :ok — it
  # maps to the :store_invalid failure other mutations produce, so the "run
  # `tasks check`" hint reaches the adapter. Mirrors the store's own rollback
  # idiom: an integer id readers survive but any post-write Check rejects.
  def test_complete_project_maps_a_post_write_rollback_to_store_invalid
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, dump_fixture([
        { "type" => "meta", "version" => 2 },
        { "type" => "section", "id" => "aaaa0001", "title" => "Projects" },
        { "type" => "section", "id" => "aaaa0002", "parent" => "aaaa0001", "title" => "Ship it" },
        { "type" => "task", "id" => "aaaa0003", "parent" => "aaaa0002", "state" => "NEXT",
          "title" => "Do the thing" },
        { "type" => "task", "id" => 12345678, "parent" => "aaaa0002", "state" => "NEXT",
          "title" => "Bad id sibling" },
      ]))
      before = File.read(org)
      app = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive)
      )

      result = app.complete_project("aaaa0002", today: TODAY)
      assert_equal :store_invalid, result.status
      assert_equal before, File.read(org), "the rolled-back write leaves bytes untouched"
    end
  end

  def test_application_archive_project_returns_moved_ids
    with_projects_application do |_org, _archive, app|
      done = app.archive_project(PFIX[:reno])
      assert_equal :ok, done.status
      assert_equal [PFIX[:reno], PFIX[:reno_todo]], done.touched_ids
      assert_equal 2, done.summary[:archived]

      missing = app.archive_project("ffffffff")
      assert_equal :not_found, missing.status
    end
  end
end
