# frozen_string_literal: true

require_relative "test_helper"
require "tasks/format"
require "tasks/migrate"
require "stringio"
require "open3"

# Golden org→JSONL migration tests. Minted ids are random, so records under a
# minted parent are asserted field-by-field on the parsed output; nodes whose
# whole line is deterministic (a preserved :ID: with an omitted/known parent)
# are asserted byte-exact.
class TestMigrate < Minitest::Test
  F = Tasks::Format
  M = Tasks::Migrate

  # Mirrors the plan's example: GTD lists, a project heading (section) with a
  # goal body, a task with all fields + a preserved :ID:, a sub-action under a
  # project, a sub-action under a TASK, a recurring task, a closed DONE task,
  # idless tasks (minted), tags-with-contexts, an indented multi-line body with
  # a dropped `#` comment, and prose directly under a section heading.
  COMPREHENSIVE = <<~ORG
    * Inbox
      Loose thoughts land here.
    ** INBOX random thought :@home:

    * Projects
    ** Launch the personal site
       Goal: site up by end of month.
    *** NEXT [#A] Pick a static-site generator :@computer:important:
        DEADLINE: <2026-07-20 Mon>
        :PROPERTIES:
        :ID: aaaa1111
        :END:
    *** TODO Write the about page :@computer:
        First body line.
    # dropped comment
        Second body line.

    * Home
    ** NEXT Water the plants :@home:
       SCHEDULED: <2026-07-08 Wed +1w>
       Weekly chore.
    *** NEXT Buy fertilizer :@errands:
    ** DONE [#C] Old finished thing :@computer:
       CLOSED: [2026-06-20]
  ORG

  def with_src(gtd:, archive: nil)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "gtd.org"), gtd)
      File.write(File.join(dir, "archive.org"), archive) if archive
      yield dir
    end
  end

  def migrate(dir, *args)
    out = StringIO.new
    err = StringIO.new
    ok = M.run(args, default_dir: dir, out: out, err: err)
    [ok, out.string, err.string]
  end

  def records(path)
    F.parse(File.read(path)).records
  end

  def rec(records, title)
    records.find { |r| r["title"] == title } or raise "no record titled #{title.inspect}"
  end

  def raw_line(path, needle)
    File.read(path).each_line.find { |l| l.include?(needle) }&.chomp or
      raise "no line containing #{needle.inspect}"
  end

  # -- comprehensive walk ----------------------------------------------------

  def test_comprehensive_structure_and_fields
    with_src(gtd: COMPREHENSIVE) do |dir|
      ok, _out, err = migrate(dir)
      assert ok, "migrate failed: #{err}"
      recs = records(File.join(dir, "tasks.jsonl"))

      # meta first, then DFS pre-order = file order.
      assert_equal({ "type" => "meta", "version" => 1 },
                   recs.first.reject { |k, _| k == "line" })
      assert_equal(
        ["Inbox", "random thought", "Projects", "Launch the personal site",
         "Pick a static-site generator", "Write the about page", "Home",
         "Water the plants", "Buy fertilizer", "Old finished thing"],
        recs.drop(1).map { |r| r["title"] }
      )

      inbox   = rec(recs, "Inbox")
      projects = rec(recs, "Projects")
      home    = rec(recs, "Home")
      launch  = rec(recs, "Launch the personal site")
      thought = rec(recs, "random thought")
      pick    = rec(recs, "Pick a static-site generator")
      about   = rec(recs, "Write the about page")
      water   = rec(recs, "Water the plants")
      fert    = rec(recs, "Buy fertilizer")
      old     = rec(recs, "Old finished thing")

      # sections: no parent for top-level lists; prose under a heading → body.
      assert_equal "section", inbox["type"]
      refute inbox.key?("parent")
      assert_equal "Loose thoughts land here.", inbox["body"]

      # project heading is a section under Projects, carrying its goal body.
      assert_equal "section", launch["type"]
      assert_equal projects["id"], launch["parent"]
      assert_equal "Goal: site up by end of month.", launch["body"]

      # task with all fields; parent = enclosing section; contexts inline in tags.
      assert_equal "task", pick["type"]
      assert_equal launch["id"], pick["parent"]
      assert_equal "NEXT", pick["state"]
      assert_equal "A", pick["priority"]
      assert_equal %w[@computer important], pick["tags"]
      assert_equal "2026-07-20", pick["deadline"]
      assert_equal "aaaa1111", pick["id"] # preserved verbatim

      # inbox task
      assert_equal inbox["id"], thought["parent"]
      assert_equal "INBOX", thought["state"]
      assert_equal ["@home"], thought["tags"]

      # indented multi-line body: dropped `#` line, dedented, "\n"-joined.
      assert_equal "First body line.\nSecond body line.", about["body"]

      # recurring task: cookie carried, scheduled kept, body dedented.
      assert_equal home["id"], water["parent"]
      assert_equal "2026-07-08", water["scheduled"]
      assert_equal "+1w", water["recur"]
      assert_equal "Weekly chore.", water["body"]

      # sub-action under a TASK → parent is the task id.
      assert_equal water["id"], fert["parent"]

      # closed DONE task keeps its CLOSED date.
      assert_equal "DONE", old["state"]
      assert_equal "2026-06-20", old["closed"]
      assert_equal home["id"], old["parent"]

      # every record carries a distinct 8-hex id.
      ids = recs.drop(1).map { |r| r["id"] }
      assert(ids.all? { |i| i =~ /\A[0-9a-f]{8}\z/ }, "ids not 8-hex: #{ids.inspect}")
      assert_equal ids.size, ids.uniq.size
    end
  end

  # Byte-exact line for the preserved-id task, with the (minted) section parent
  # pinned from the parsed output — locks key order, spacing, omission rules.
  def test_preserved_id_line_is_byte_exact
    with_src(gtd: COMPREHENSIVE) do |dir|
      assert migrate(dir).first
      path = File.join(dir, "tasks.jsonl")
      parent = rec(records(path), "Launch the personal site")["id"]
      expected =
        %({"type":"task","id":"aaaa1111","parent":"#{parent}",) +
        %("state":"NEXT","priority":"A","title":"Pick a static-site generator",) +
        %("tags":["@computer","important"],"deadline":"2026-07-20"})
      assert_equal expected, raw_line(path, '"id":"aaaa1111"')
    end
  end

  def test_summary_output
    with_src(gtd: COMPREHENSIVE) do |dir|
      _ok, out, _err = migrate(dir)
      assert_match(/sections:\s+4/, out)      # Inbox, Projects, Launch…, Home
      assert_match(/tasks:\s+6/, out)
      assert_match(/ids minted:\s+9/, out)    # all but the preserved aaaa1111
      assert_match(/#-lines dropped:\s+1/, out)
      assert_match(/bodies carried:\s+4/, out) # Inbox, Launch, about, water
      assert_match(/next steps:/, out)
      assert_match(/git rm gtd\.org archive\.org/, out)
    end
  end

  # -- archive ----------------------------------------------------------------

  ARCHIVE = <<~ORG
    ** DONE Pre-separator task :@work:
       CLOSED: [2026-05-01]

    # Archived 2026-06-01
    ** DONE First swept :@home:
       CLOSED: [2026-05-15]
    *** DONE First swept child
        :PROPERTIES:
        :ID: bbbb2222
        :END:

    # Archived 2026-07-01
    ** CANCELLED Second swept :@errands:
       CLOSED: [2026-06-20]
       :PROPERTIES:
       :ID: cccc3333
       :END:
  ORG

  def test_archive_separators_and_parent_dropping
    with_src(gtd: COMPREHENSIVE, archive: ARCHIVE) do |dir|
      ok, _out, err = migrate(dir)
      assert ok, "migrate failed: #{err}"
      recs = records(File.join(dir, "archive.jsonl"))

      pre    = rec(recs, "Pre-separator task")
      first  = rec(recs, "First swept")
      child  = rec(recs, "First swept child")
      second = rec(recs, "Second swept")

      # pre-separator block: root, no archived stamp, no parent.
      refute pre.key?("archived")
      refute pre.key?("parent")

      # roots stamped with their preceding separator date.
      assert_equal "2026-06-01", first["archived"]
      assert_equal "2026-07-01", second["archived"]
      refute first.key?("parent")

      # descendants keep internal parents and are NOT stamped.
      assert_equal first["id"], child["parent"]
      refute child.key?("archived")
      assert_equal "bbbb2222", child["id"] # preserved

      # a preserved-id root with an omitted parent is fully deterministic.
      path = File.join(dir, "archive.jsonl")
      expected = %({"type":"task","id":"cccc3333","state":"CANCELLED",) +
                 %("title":"Second swept","tags":["@errands"],) +
                 %("closed":"2026-06-20","archived":"2026-07-01"})
      assert_equal expected, raw_line(path, '"id":"cccc3333"')
    end
  end

  def test_missing_archive_is_fine
    with_src(gtd: COMPREHENSIVE) do |dir|
      ok, _out, _err = migrate(dir)
      assert ok
      assert File.exist?(File.join(dir, "tasks.jsonl"))
      refute File.exist?(File.join(dir, "archive.jsonl"))
    end
  end

  # -- edge cases -------------------------------------------------------------

  def test_dual_cookie_keeps_deadline_and_reports
    org = <<~ORG
      * Work
      ** NEXT Ship it :@computer:
         SCHEDULED: <2026-07-10 Fri +1w>
         DEADLINE: <2026-07-12 Sun +2d>
    ORG
    with_src(gtd: org) do |dir|
      _ok, out, _err = migrate(dir)
      ship = rec(records(File.join(dir, "tasks.jsonl")), "Ship it")
      assert_equal "+2d", ship["recur"] # DEADLINE cookie wins
      assert_match(/discarded SCHEDULED cookies/, out)
      assert_match(/Ship it: kept \+2d, discarded \+1w/, out)
    end
  end

  def test_closed_on_open_task_is_dropped_and_reported
    org = <<~ORG
      * Work
      ** TODO Not really done :@computer:
         CLOSED: [2026-06-20]
    ORG
    with_src(gtd: org) do |dir|
      _ok, out, _err = migrate(dir)
      task = rec(records(File.join(dir, "tasks.jsonl")), "Not really done")
      refute task.key?("closed")
      assert_match(/dropped CLOSED on open tasks/, out)
      assert_match(/Not really done/, out)
    end
  end

  # A child DONE task's CLOSED must not leak up to a DONE parent (the recurring
  # store-block-walk bug class: bind metadata to the immediate headline).
  def test_child_closed_does_not_leak_to_parent
    org = <<~ORG
      * Work
      ** DONE Parent task :@computer:
         CLOSED: [2026-06-01]
      *** DONE Child task
          CLOSED: [2026-06-20]
    ORG
    with_src(gtd: org) do |dir|
      assert migrate(dir).first
      recs = records(File.join(dir, "tasks.jsonl"))
      assert_equal "2026-06-01", rec(recs, "Parent task")["closed"]
      assert_equal "2026-06-20", rec(recs, "Child task")["closed"]
      assert_equal rec(recs, "Parent task")["id"], rec(recs, "Child task")["parent"]
    end
  end

  def test_dry_run_writes_nothing
    with_src(gtd: COMPREHENSIVE) do |dir|
      ok, out, _err = migrate(dir, "--dry-run")
      assert ok
      refute File.exist?(File.join(dir, "tasks.jsonl"))
      assert_match(/nothing written/, out)
      assert_match(/"type":"meta"/, out) # preview shows output lines
    end
  end

  def test_refuses_overwrite_without_force
    with_src(gtd: COMPREHENSIVE) do |dir|
      File.write(File.join(dir, "tasks.jsonl"), "STALE\n")
      ok, _out, err = migrate(dir)
      refute ok
      assert_match(/refusing to overwrite/, err)
      assert_equal "STALE\n", File.read(File.join(dir, "tasks.jsonl"))

      ok2, _out2, _err2 = migrate(dir, "--force")
      assert ok2
      refute_equal "STALE\n", File.read(File.join(dir, "tasks.jsonl"))
    end
  end

  def test_missing_gtd_errors
    Dir.mktmpdir do |dir|
      ok, _out, err = migrate(dir)
      refute ok
      assert_match(/no gtd\.org/, err)
    end
  end

  def test_from_flag_selects_source_dir
    with_src(gtd: COMPREHENSIVE) do |src|
      Dir.mktmpdir do |other|
        ok, _out, _err = migrate(other, "--from", src)
        assert ok
        assert File.exist?(File.join(src, "tasks.jsonl"))
        refute File.exist?(File.join(other, "tasks.jsonl"))
      end
    end
  end

  def test_lint_errors_abort_migration
    org = <<~ORG
      * Work
      SCHEDULED: <2026-07-10 Fri>
    ORG
    with_src(gtd: org) do |dir|
      ok, _out, err = migrate(dir)
      refute ok
      assert_match(/aborting/, err)
      refute File.exist?(File.join(dir, "tasks.jsonl"))
    end
  end

  # -- CLI end-to-end ---------------------------------------------------------

  BIN = File.expand_path("../bin/tasks", __dir__)

  def test_cli_migrate_exit_codes
    with_src(gtd: COMPREHENSIVE) do |dir|
      env = { "TASKS_DIR" => dir }
      out, _err, st = Open3.capture3(env, "ruby", BIN, "migrate")
      assert st.success?, "exit #{st.exitstatus}"
      assert File.exist?(File.join(dir, "tasks.jsonl"))
      assert_match(/migrate complete/, out)

      # second run without --force refuses → exit 1
      _out2, _err2, st2 = Open3.capture3(env, "ruby", BIN, "migrate")
      assert_equal 1, st2.exitstatus
    end
  end
end
