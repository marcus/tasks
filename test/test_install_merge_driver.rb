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

  def test_real_git_merge_invokes_driver_and_resolves_same_line_divergence
    Dir.mktmpdir do |repo|
      git(repo, "init", "-q")
      git(repo, "config", "user.name", "Merge Test")
      git(repo, "config", "user.email", "merge-test@example.com")
      File.write(File.join(repo, ".gitattributes"),
                 "tasks.jsonl merge=tasksjsonl\narchive.jsonl merge=tasksjsonl\n")
      File.write(File.join(repo, ".gitignore"), ".tasks-merge.log\n")
      base = [
        { "type" => "meta", "version" => 1 },
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
