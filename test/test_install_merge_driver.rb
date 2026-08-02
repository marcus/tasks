# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "rbconfig"
require "tasks/format"

class TestInstallMergeDriver < Minitest::Test
  INSTALLER = File.expand_path("../bin/install-merge-driver", __dir__)
  TASKS_BIN = File.expand_path("../bin/tasks", __dir__)

  def git(repo, *args)
    stdout, stderr, status = Open3.capture3("git", "-C", repo, *args)
    assert status.success?, "git #{args.join(" ")} failed: #{stderr}"
    stdout.strip
  end

  def test_installer_writes_repo_local_absolute_driver_config
    Dir.mktmpdir do |repo|
      git(repo, "init", "-q")
      File.write(File.join(repo, ".gitattributes"),
                 "tasks.jsonl merge=tasksjsonl\narchive.jsonl merge=tasksjsonl\n")

      stdout, stderr, status = Open3.capture3(RbConfig.ruby, INSTALLER, repo)

      assert status.success?, stderr
      assert_includes stdout, "installed tasksjsonl merge driver"
      assert_equal "tasks jsonl 3-way record merge", git(repo, "config", "--get", "merge.tasksjsonl.name")
      # %L/%X/%Y are unquoted on purpose: Git quotes them itself in the
      # rebase/cherry-pick path, and quoting them again is a shell syntax error
      # that skips the driver entirely.
      assert_equal "#{TASKS_BIN} merge-driver %O %A %B %P %L %X %Y",
                   git(repo, "config", "--get", "merge.tasksjsonl.driver")
    end
  end

  def test_installer_refuses_partial_attributes_registration
    Dir.mktmpdir do |repo|
      git(repo, "init", "-q")
      File.write(File.join(repo, ".gitattributes"), "tasks.jsonl merge=tasksjsonl\n")

      _stdout, stderr, status = Open3.capture3(RbConfig.ruby, INSTALLER, repo)

      refute status.success?
      assert_includes stderr, "must select merge=tasksjsonl for both"
      assert_includes stderr, "missing: archive.jsonl"
      configured, = Open3.capture3("git", "-C", repo, "config", "--get", "merge.tasksjsonl.driver")
      assert_empty configured
    end
  end

  BASE_RECORDS = [
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "30000001", "title" => "Work" },
    { "type" => "task", "id" => "30000002", "parent" => "30000001", "state" => "NEXT", "title" => "Book Sixt" },
    { "type" => "task", "id" => "30000003", "parent" => "30000001", "state" => "TODO", "title" => "Call PSE" },
  ].freeze

  def records(&edit)
    copied = BASE_RECORDS.map(&:dup)
    edit&.call(copied)
    copied
  end

  # `git rebase` and `git cherry-pick` reach the driver through Git's sequencer,
  # not the merge path, and Git pre-quotes the %X/%Y labels there while leaving
  # them bare under `merge`. A command string that quotes them again turns into a
  # shell syntax error, the driver never runs, and Git resolves tasks.jsonl with
  # its own line-based text merge — the one outcome this driver exists to
  # prevent, and one no `git merge` test can see. Hence all three operations.
  CONFLICTING_OPERATIONS = {
    "merge" => ->(_primary) { ["merge", "--no-edit", "theirs"] }.freeze,
    "rebase" => ->(primary) { ["rebase", "theirs", primary] }.freeze,
    "cherry-pick" => ->(_primary) { ["cherry-pick", "theirs"] }.freeze,
  }.freeze

  # Drive a real Git operation whose driver refuses, and assert what the user is
  # left holding. The refusal contract is four things at once: nonzero exit, the
  # path still UU, a working file carrying conflict markers, and both sides still
  # in it verbatim.
  #
  # Markers are the load-bearing part. Git copies %A over the working file even
  # when the driver fails, and it seeds %A with the ours blob, so a driver that
  # writes nothing leaves a clean, markerless, valid tasks.jsonl behind — one
  # `tasks check` passes and `git add` stages, silently dropping the whole other
  # side (td-7b9c01). Asserting the exit status alone would not catch that; the
  # `tasks check` assertion below is what makes the loss impossible to miss.
  def assert_git_operation_refuses_with_conflict_markers(ours_text, theirs_text, operation: "merge")
    Dir.mktmpdir do |repo|
      git(repo, "init", "-q")
      git(repo, "config", "user.name", "Merge Test")
      git(repo, "config", "user.email", "merge-test@example.com")
      File.write(File.join(repo, ".gitattributes"),
                 "tasks.jsonl merge=tasksjsonl\narchive.jsonl merge=tasksjsonl\n")
      File.write(File.join(repo, ".gitignore"), ".tasks-merge.log\n")
      tasks_path = File.join(repo, "tasks.jsonl")
      File.write(tasks_path, Tasks::Format.dump(BASE_RECORDS))
      git(repo, "add", ".gitattributes", ".gitignore", "tasks.jsonl")
      git(repo, "commit", "-q", "-m", "base")
      primary_branch = git(repo, "branch", "--show-current")
      _stdout, stderr, status = Open3.capture3(RbConfig.ruby, INSTALLER, repo)
      assert status.success?, stderr

      git(repo, "switch", "-q", "-c", "theirs")
      File.write(tasks_path, theirs_text)
      git(repo, "commit", "-q", "-am", "theirs")
      git(repo, "switch", "-q", primary_branch)
      File.write(tasks_path, ours_text)
      git(repo, "commit", "-q", "-am", "ours")

      argv = CONFLICTING_OPERATIONS.fetch(operation).call(primary_branch)
      _out, op_stderr, op_status = Open3.capture3("git", "-C", repo, *argv)
      porcelain, = Open3.capture3("git", "-C", repo, "status", "--porcelain", "tasks.jsonl")
      conflicted = File.binread(tasks_path)

      refute op_status.success?, "git #{operation} should fail: #{op_stderr}"
      assert_includes porcelain, "UU tasks.jsonl"
      assert_match(/^<{7} .*tasks JSONL merge failed: /, conflicted,
                   "git #{operation}: refusal left no conflict markers in the working file")
      assert_includes conflicted, "\n=======\n"
      assert_match(/^>{7} /, conflicted)
      # Which side lands in which fence flips between merge and rebase, so this
      # asserts only what matters: neither side was dropped or summarized.
      assert_includes conflicted, ours_text
      assert_includes conflicted, theirs_text
      assert_refuted_by_check(repo, tasks_path, operation)
      op_stderr
    end
  end

  # The property that turns a silent loss into a visible one: after a refusal the
  # file must NOT be something the toolchain calls fine.
  def assert_refuted_by_check(repo, tasks_path, operation)
    env = { "TASKS_FILE" => tasks_path,
            "TASKS_ARCHIVE" => File.join(repo, "archive.jsonl"),
            "XDG_CONFIG_HOME" => File.join(repo, ".xdg-test") }
    stdout, _stderr, status = Open3.capture3(env, RbConfig.ruby, TASKS_BIN, "check")

    refute status.success?, "git #{operation}: `tasks check` accepted a refused merge's working file"
    assert_includes stdout, "invalid JSON"
  end

  def test_refused_merge_conflicts_the_working_file_when_a_side_is_another_schema_version
    ours = Tasks::Format.dump(records { |r| r[2]["title"] = "Book Sixt car" })
    theirs = Tasks::Format.dump(records { |r| r[0] = { "type" => "meta", "version" => 1 } })

    assert_includes assert_git_operation_refuses_with_conflict_markers(ours, theirs), "schema v1"
  end

  def test_refused_merge_conflicts_the_working_file_when_a_side_is_unparseable
    ours = Tasks::Format.dump(records { |r| r[2]["title"] = "Book Sixt car" })
    theirs = "#{Tasks::Format.dump(BASE_RECORDS).lines.first}{not json at all}\n"

    assert_includes assert_git_operation_refuses_with_conflict_markers(ours, theirs), "cannot be parsed"
  end

  def test_refused_merge_conflicts_the_working_file_when_the_merged_result_is_invalid
    ours = Tasks::Format.dump(records { |r| r[3]["parent"] = "30000002" })
    theirs = Tasks::Format.dump(records do |r|
      moved = r.delete_at(2)
      moved["parent"] = "30000003"
      r.insert(3, moved)
    end)

    assert_includes assert_git_operation_refuses_with_conflict_markers(ours, theirs), "cyclic parents"
  end

  def test_refused_rebase_conflicts_the_working_file
    ours = Tasks::Format.dump(records { |r| r[2]["title"] = "Book Sixt car" })
    theirs = Tasks::Format.dump(records { |r| r[0] = { "type" => "meta", "version" => 1 } })

    assert_includes assert_git_operation_refuses_with_conflict_markers(ours, theirs, operation: "rebase"),
                    "schema v1"
  end

  def test_refused_cherry_pick_conflicts_the_working_file
    ours = Tasks::Format.dump(records { |r| r[2]["title"] = "Book Sixt car" })
    theirs = "#{Tasks::Format.dump(BASE_RECORDS).lines.first}{not json at all}\n"

    assert_includes assert_git_operation_refuses_with_conflict_markers(ours, theirs, operation: "cherry-pick"),
                    "cannot be parsed"
  end

  def test_real_git_merge_invokes_driver_and_resolves_same_line_divergence
    Dir.mktmpdir do |repo|
      git(repo, "init", "-q")
      git(repo, "config", "user.name", "Merge Test")
      git(repo, "config", "user.email", "merge-test@example.com")
      File.write(File.join(repo, ".gitattributes"),
                 "tasks.jsonl merge=tasksjsonl\narchive.jsonl merge=tasksjsonl\n")
      File.write(File.join(repo, ".gitignore"), ".tasks-merge.log\n")
      base = [
        { "type" => "meta", "version" => 2 },
        { "type" => "section", "id" => "30000001", "title" => "Work" },
        { "type" => "task", "id" => "30000002", "parent" => "30000001", "state" => "NEXT",
          "title" => "Book Sixt", "tags" => ["@computer"], "scheduled" => "2026-07-18" },
      ]
      tasks_path = File.join(repo, "tasks.jsonl")
      File.write(tasks_path, Tasks::Format.dump(base))
      git(repo, "add", ".gitattributes", ".gitignore", "tasks.jsonl")
      git(repo, "commit", "-q", "-m", "base")
      primary_branch = git(repo, "branch", "--show-current")

      _stdout, stderr, status = Open3.capture3(RbConfig.ruby, INSTALLER, repo)
      assert status.success?, stderr

      git(repo, "switch", "-q", "-c", "theirs")
      theirs = base.map(&:dup)
      theirs.last["scheduled"] = "2026-07-19"
      theirs.last["updated"] = "2026-07-16T11:00:00Z#work"
      File.write(tasks_path, Tasks::Format.dump(theirs))
      git(repo, "commit", "-q", "-am", "theirs reschedules")

      git(repo, "switch", "-q", primary_branch)
      ours = base.map(&:dup)
      ours.last["tags"] = %w[@computer travel]
      ours.last["updated"] = "2026-07-16T10:00:00Z#home"
      File.write(tasks_path, Tasks::Format.dump(ours))
      git(repo, "commit", "-q", "-am", "ours tags")

      _stdout, merge_stderr, merge_status = Open3.capture3("git", "-C", repo, "merge", "--no-edit", "theirs")

      assert merge_status.success?, merge_stderr
      merged = Tasks::Format.parse(File.read(tasks_path, encoding: "UTF-8")).records.last
      assert_equal %w[@computer travel], merged["tags"]
      assert_equal "2026-07-19", merged["scheduled"]
      assert_equal "2026-07-16T11:00:00Z#work", merged["updated"]
      refute_includes File.read(tasks_path), "<<<<<<<"
      assert_includes File.read(File.join(repo, ".tasks-merge.log")), "30000002 merged_fields"
    end
  end
end
