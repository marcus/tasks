# frozen_string_literal: true

require "shellwords"

module Tasks
  # Builds the post-run "what did the agent change?" git diff for `tasks -p`.
  # Extracted out of bin/tasks so the decision (which files to diff, and how to
  # handle a relocated memory sidecar) can be exercised against a real sandbox
  # repo without driving an actual agent.
  #
  # The memory sidecar (agent-memory.md) normally sits beside tasks.jsonl, so it
  # is diffed right alongside the task files. But TASKS_MEMORY or the config
  # `memory` key can put it outside the task-data repo's work tree, where
  # `git -C data_dir diff` cannot see it. In that case it is dropped from the
  # diff and, when it exists (so the agent could have edited it), flagged with a
  # one-line notice rather than silently omitted.
  module AgentDiff
    # diff:   the captured `git diff` text for the in-repo targets (may be empty).
    # notice: the path of an out-of-repo memory sidecar to flag, or nil.
    Result = Struct.new(:diff, :notice, keyword_init: true)

    module_function

    # Returns a Result, or nil when data_dir is not a git work tree (the diff is
    # only meaningful for a git-backed task set). color forces ANSI color in the
    # captured diff — callers pass $stdout.tty? so an interactive run stays
    # colored while a piped/redirected one stays plain.
    def compute(data_dir:, org:, archive:, memory:, color: false)
      return nil unless git_work_tree?(data_dir)

      targets = [org, archive]
      notice = nil
      if memory
        if in_same_repo?(data_dir, memory)
          targets << memory
        elsif File.exist?(memory)
          notice = memory
        end
      end

      Result.new(diff: capture_diff(data_dir, targets, color: color), notice: notice)
    end

    def git_work_tree?(dir)
      system("git", "-C", dir, "rev-parse", "--is-inside-work-tree",
             out: File::NULL, err: File::NULL)
    end

    # True when path resolves inside data_dir's git work tree — the same repo, so
    # `git -C data_dir diff` can show it. A path in no repo, or in a different
    # (e.g. nested or sibling) repo, is outside.
    def in_same_repo?(data_dir, path)
      dir = File.directory?(path) ? path : File.dirname(path)
      return false unless File.directory?(dir)

      top = toplevel(data_dir)
      !top.nil? && top == toplevel(dir)
    end

    def toplevel(dir)
      out = `git -C #{dir.shellescape} rev-parse --show-toplevel 2>/dev/null`.strip
      out.empty? ? nil : out
    end

    def capture_diff(data_dir, targets, color:)
      spec = targets.map(&:shellescape).join(" ")
      color_flag = color ? "always" : "never"
      diff = `git -C #{data_dir.shellescape} --no-pager diff --color=#{color_flag} -- #{spec}`
      # Plain `git diff` only sees tracked files, but the first "remember ..."
      # request CREATES agent-memory.md (and a fresh task set may have uncommitted
      # task files too). Surface those as new-file diffs so the create path is
      # never silently absent from the audit.
      untracked(data_dir, targets).each do |path|
        diff += `git -C #{data_dir.shellescape} --no-pager diff --no-index --color=#{color_flag} -- /dev/null #{path.shellescape}`
      end
      diff
    end

    def untracked(data_dir, targets)
      targets.select do |path|
        File.file?(path) &&
          !system("git", "-C", data_dir, "ls-files", "--error-unmatch", path,
                  out: File::NULL, err: File::NULL)
      end
    end
  end
end
