# frozen_string_literal: true

require_relative "atomic"
require_relative "jsonl_merge"

module Tasks
  module MergeDriverCommand
    module_function

    def run(args, stdout: $stdout, stderr: $stderr)
      unless args.length == 4
        stderr.puts "usage: tasks merge-driver <base> <ours> <theirs> <pathname>"
        return 2
      end

      base_path, ours_path, theirs_path, pathname = args
      result = JsonlMerge.merge(
        base_text: read_utf8(base_path),
        ours_text: read_utf8(ours_path),
        theirs_text: read_utf8(theirs_path)
      )
      # A refusal writes NOTHING to the merge file and returns nonzero, which is
      # what leaves the working file byte-for-byte at its pre-merge content.
      #
      # That follows from Git's contract, so it is worth stating: Git hands the
      # driver three TEMP files and copies whatever %A holds back over the
      # working file when the driver returns — on failure exactly as on success
      # (that is how a text driver's conflict markers reach the working tree).
      # Git seeds %A with the ours blob and has already checked that same blob
      # into the working file before the driver runs, in `merge`, `rebase`,
      # `cherry-pick`, and `checkout -m` alike. So an untouched %A means the
      # copy-back is a byte-for-byte no-op, and the path is still left UU with a
      # nonzero exit: nothing is resolved, nothing is silently rewritten.
      #
      # The corollary is the rule for anyone changing this method (the Go port
      # included): never write %A before the merged result is known to be valid.
      # A write-then-validate ordering would leave a rejected merge's output in
      # the working file, and returning nonzero afterwards could not take it
      # back. JsonlMerge builds and validates entirely in memory, and only a
      # result that passed carries `text`, so the single write below is reached
      # only by a merge that is already known good.
      unless result.ok?
        append_log(pathname, result.log_lines(pathname: pathname), stderr: stderr)
        stderr.puts "tasks JSONL merge failed: #{result.error}"
        return 1
      end

      Atomic.write(ours_path, result.text)
      append_log(pathname, result.log_lines(pathname: pathname), stderr: stderr)
      stdout.puts "merged #{pathname}" if ENV["TASKS_MERGE_VERBOSE"] == "1"
      0
    rescue SystemCallError, IOError => error
      append_log(pathname, ["merge #{pathname}: failed", "  error: #{error.message}"], stderr: stderr) if pathname
      stderr.puts "tasks JSONL merge failed: #{error.message}"
      1
    end

    def read_utf8(path)
      File.binread(path).force_encoding(Encoding::UTF_8)
    end

    def append_log(pathname, lines, stderr:)
      real_path = File.expand_path(pathname)
      log_path = File.join(File.dirname(real_path), ".tasks-merge.log")
      File.open(log_path, "a", encoding: "UTF-8") do |log|
        lines.each { |line| log.puts(line) }
      end
    rescue SystemCallError, IOError => error
      stderr.puts "tasks JSONL merge warning: could not write audit log: #{error.message}"
    end
  end
end
