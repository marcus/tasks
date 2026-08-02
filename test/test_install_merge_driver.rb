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
      assert_equal "#{TASKS_BIN} merge-driver %O %A %B %P",
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

  # Drive a real `git merge` whose driver refuses, and report what the user is
  # left holding. The refusal contract is all three at once: nonzero exit, the
  # path still UU, and the working file byte-for-byte at its pre-merge content —
  # Git copies %A back over the working file even when the driver fails, so a
  # driver that wrote anything on the way out would show up here as changed
  # bytes while the exit status still looked correct.
  def assert_git_merge_refuses_without_touching_working_file(ours_text, theirs_text)
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
      before = File.binread(tasks_path)

      _out, merge_stderr, merge_status = Open3.capture3("git", "-C", repo, "merge", "--no-edit", "theirs")
      porcelain, = Open3.capture3("git", "-C", repo, "status", "--porcelain", "tasks.jsonl")

      refute merge_status.success?, "git merge should fail: #{merge_stderr}"
      assert_includes porcelain, "UU tasks.jsonl"
      assert_equal before, File.binread(tasks_path),
                   "refusal changed the working file instead of leaving it at its pre-merge content"
      merge_stderr
    end
  end

  def test_refused_merge_leaves_working_file_untouched_when_a_side_is_another_schema_version
    ours = Tasks::Format.dump(records { |r| r[2]["title"] = "Book Sixt car" })
    theirs = Tasks::Format.dump(records { |r| r[0] = { "type" => "meta", "version" => 1 } })

    assert_includes assert_git_merge_refuses_without_touching_working_file(ours, theirs), "schema v1"
  end

  def test_refused_merge_leaves_working_file_untouched_when_a_side_is_unparseable
    ours = Tasks::Format.dump(records { |r| r[2]["title"] = "Book Sixt car" })
    theirs = "#{Tasks::Format.dump(BASE_RECORDS).lines.first}{not json at all}\n"

    assert_includes assert_git_merge_refuses_without_touching_working_file(ours, theirs), "cannot be parsed"
  end

  def test_refused_merge_leaves_working_file_untouched_when_the_merged_result_is_invalid
    ours = Tasks::Format.dump(records { |r| r[3]["parent"] = "30000002" })
    theirs = Tasks::Format.dump(records do |r|
      moved = r.delete_at(2)
      moved["parent"] = "30000003"
      r.insert(3, moved)
    end)

    assert_includes assert_git_merge_refuses_without_touching_working_file(ours, theirs), "cyclic parents"
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
