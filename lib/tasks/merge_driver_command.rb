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
