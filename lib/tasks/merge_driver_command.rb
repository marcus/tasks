# frozen_string_literal: true

require_relative "atomic"
require_relative "jsonl_merge"

module Tasks
  module MergeDriverCommand
    module_function

    DEFAULT_MARKER_SIZE = 7
    MINIMUM_MARKER_SIZE = 7

    def run(args, stdout: $stdout, stderr: $stderr)
      unless (4..7).cover?(args.length)
        stderr.puts "usage: tasks merge-driver <base> <ours> <theirs> <pathname> " \
                    "[marker-size] [ours-label] [theirs-label]"
        return 2
      end

      base_path, ours_path, theirs_path, pathname, marker_size, ours_label, theirs_label = args
      result = JsonlMerge.merge(
        base_text: read_utf8(base_path),
        ours_text: read_utf8(ours_path),
        theirs_text: read_utf8(theirs_path)
      )
      # A refusal writes a CONFLICTED file — both sides fenced by the ordinary
      # `<<<<<<< / ======= / >>>>>>>` markers — and returns nonzero.
      #
      # Git's contract is what makes that the right shape: Git hands the driver
      # three TEMP files and copies whatever %A holds back over the working file
      # when the driver returns, on failure exactly as on success (that is how a
      # text driver's markers reach the working tree). Git seeds %A with the
      # ours blob and has already checked that same blob into the working file
      # before the driver runs, in `merge`, `rebase`, `cherry-pick`, and
      # `checkout -m` alike. So leaving %A untouched does NOT mean "wrote
      # nothing": it means the working file is left as ours' full content, valid
      # JSONL, no markers anywhere — a file `tasks check` passes and the reflex
      # `git add tasks.jsonl` stages, silently discarding all of theirs. That is
      # the failure this branch exists to prevent (td-7b9c01).
      #
      # So the driver writes what Git's own text driver would: ours verbatim,
      # theirs verbatim, both fenced. Three properties follow, and all three are
      # the point — the path stays UU with a nonzero exit, `tasks check` fails
      # on the marker lines (they are not JSON), and neither side has been
      # summarized or dropped, so the resolution is a hand edit away.
      #
      # The bytes are copied raw from the two merge-stage temp files, including
      # a side that is not valid JSONL or not valid UTF-8 at all, which is why
      # this writes binary and not through Atomic/UTF-8. It writes %A, the
      # driver's own temp file — never %P, the working path — so no clean/smudge
      # filter is re-applied on the way through.
      #
      # The rule for anyone changing this method (the Go port included): never
      # write a MERGED result to %A before it is known valid. A
      # write-then-validate ordering would leave a rejected merge's output in
      # the working file looking clean, and returning nonzero afterwards could
      # not take it back. JsonlMerge builds and validates entirely in memory and
      # only a result that passed carries `text`, so the merged write below is
      # reached only by a merge that is already known good; what a refusal
      # writes is conflict markers, which resolve nothing.
      unless result.ok?
        write_conflict(
          ours_path, theirs_path,
          error: result.error,
          marker_size: marker_size,
          ours_label: ours_label,
          theirs_label: theirs_label,
          stderr: stderr
        )
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

    # The conflicted file Git copies back over the working path on a refusal.
    #
    # Shape is deliberately the conventional one — every editor, `git diff
    # --check`, and every human already recognizes it, and it is what the plain
    # text driver would have produced had this path not been claimed by a custom
    # driver:
    #
    #   <<<<<<< HEAD (tasks JSONL merge failed: <why>)
    #   …ours, byte for byte…
    #   =======
    #   …theirs, byte for byte…
    #   >>>>>>> other-branch
    #
    # The reason rides on the opening marker line, where Git itself puts a
    # free-form label. It does NOT get its own line and does not touch the
    # `=======` line: everything strictly between the fences is one side's
    # original bytes, so either side is recoverable from the file with no
    # un-mangling, which is the whole point of not summarizing them.
    #
    # Labels come from Git's %X/%Y when the configured driver command passes
    # them (Git >= 2.44-ish; "HEAD" and the branch/commit being merged). An
    # install predating that command string passes only four arguments, so the
    # labels fall back to "ours"/"theirs" and the file is just as readable.
    # Likewise %L, Git's conflict-marker size, honored when supplied so a repo
    # that widened its markers keeps them wide.
    def write_conflict(ours_path, theirs_path, error:, marker_size:, ours_label:, theirs_label:, stderr:)
      size = resolved_marker_size(marker_size)
      ours = label_or(ours_label, "ours")
      theirs = label_or(theirs_label, "theirs")
      reason = error.to_s.gsub(/\s+/, " ").strip

      # Every part is forced to binary before joining: one side may not be valid
      # UTF-8, and concatenating that with a UTF-8 marker line would raise.
      File.binwrite(ours_path, [
        "#{"<" * size} #{ours} (tasks JSONL merge failed: #{reason})\n".b,
        terminated(File.binread(ours_path)),
        "#{"=" * size}\n".b,
        terminated(File.binread(theirs_path)),
        "#{">" * size} #{theirs}\n".b,
      ].join)
    rescue SystemCallError, IOError => write_error
      # A refusal that cannot write its markers must still be a refusal: report
      # it and let the caller exit nonzero with the original diagnostic intact.
      stderr.puts "tasks JSONL merge warning: could not write conflict markers: #{write_error.message}"
    end

    def resolved_marker_size(supplied)
      text = supplied.to_s.strip
      return DEFAULT_MARKER_SIZE unless text.match?(/\A\d+\z/)

      [text.to_i, MINIMUM_MARKER_SIZE].max
    end

    def label_or(label, fallback)
      value = label.to_s.gsub(/\s+/, " ").strip
      value.empty? ? fallback : value
    end

    # A side missing its final newline would otherwise run into the next marker
    # and stop being recoverable; one added byte is the smallest fix and matches
    # what Git's own driver does with an incomplete final line.
    def terminated(bytes)
      return bytes if bytes.empty? || bytes.end_with?("\n")

      bytes + "\n".b
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
