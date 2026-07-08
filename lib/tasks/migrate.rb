# frozen_string_literal: true

require "date"
require "securerandom"
require "set"
require_relative "atomic"
require_relative "format"

module Tasks
  # One-shot org→JSONL converter, invoked as `tasks migrate`. This file is
  # deliberately SELF-CONTAINED: it carries its own copy of the org grammar, the
  # star-count tree builder, and the legacy org lint, because the store/check/tree
  # it was cloned from are rewritten for JSONL in the next stage — migrate must
  # keep parsing the *old* format after they stop understanding it. It is deleted
  # once the real data repo is cut over (git history keeps it).
  #
  # It never touches the live store. It reads gtd.org (+ optional archive.org),
  # emits tasks.jsonl (+ archive.jsonl) beside them, validates the output against
  # the same DFS/parent invariants the future Check will enforce, and prints a
  # summary of what it carried and what it dropped.
  module Migrate
    # -- copied org grammar (kept in lockstep with store.rb at clone time) -----
    STATES       = %w[INBOX TODO NEXT WAITING DONE CANCELLED].freeze
    OPEN_STATES  = %w[INBOX TODO NEXT WAITING].freeze
    DONE_STATES  = %w[DONE CANCELLED].freeze

    HEADLINE = /^\*+\s+(INBOX|TODO|NEXT|WAITING|DONE|CANCELLED)\s+(?:\[#([ABC])\]\s+)?(.*?)\s*(:[\w@:]+:)?\s*$/
    REPEATER = /(?:\.\+|\+\+|\+)[1-9]\d*[dwmy]/
    STAMP    = /(SCHEDULED|DEADLINE):\s*<(\d{4}-\d{2}-\d{2})(?:[^>]*?\s(#{REPEATER}))?[^>]*>/
    CLOSED_STAMP = /^\s*CLOSED:\s*\[(\d{4}-\d{2}-\d{2})\]/
    ID_LINE      = /^\s*:ID:\s+(\S+)\s*$/i
    DRAWER_START = /^\s*:PROPERTIES:\s*$/i
    DRAWER_END   = /^\s*:END:\s*$/i
    PLANNING     = /^\s*(?:SCHEDULED|DEADLINE|CLOSED):/

    # An `# Archived <date>` sweep separator in archive.org; the date it stamps
    # onto every top-level (root) node in the chunk that follows it.
    ARCHIVE_SEP  = /^# Archived (\d{4}-\d{2}-\d{2})/

    # Legacy-org lint grammar (copied from check.rb).
    TASK_HEADLINE = /^\*+\s+(?:#{STATES.join("|")})\s+(?:\[#[ABC]\]\s+)?(.*?)\s*(:[\w@:]+:)?\s*$/
    METADATA      = /^\s+(SCHEDULED|DEADLINE|CLOSED):/

    # A parsed org task headline plus its planning metadata (mirrors the store's
    # Item, minus the display-only members migrate never needs).
    Item = Struct.new(:state, :priority, :title, :tags, :scheduled, :deadline,
                      :recur, :closed, :id, :line, keyword_init: true)

    # A headline node in the star-count tree: its own body lines and its
    # children. `item` nil marks a non-task heading (a GTD list or project) —
    # those become `section` records.
    Node = Struct.new(:title, :line, :level, :item, :body, :children,
                      keyword_init: true)

    # Bad CLI arguments — surfaced as a usage message, not a backtrace.
    class UsageError < StandardError; end

    # Mints unique 8-hex ids, excluding every id already seen across both org
    # files, so a freshly minted section id can never collide with a preserved
    # task :ID: (even one already swept into the archive). Counts how many it
    # minted for the summary. (Same loop as the store's ensure_id_impl.)
    class Minter
      attr_reader :minted

      def initialize(taken)
        @taken = taken.to_set
        @minted = 0
      end

      def mint
        loop do
          id = SecureRandom.hex(4)
          next if @taken.include?(id)
          @taken << id
          @minted += 1
          return id
        end
      end
    end

    # Running tallies for the closing summary + the lossy-conversion notes.
    Summary = Struct.new(:sections, :tasks, :archived, :bodies, :dropped_hash,
                         :dual, :dropped_closed, keyword_init: true) do
      def self.blank
        new(sections: 0, tasks: 0, archived: 0, bodies: 0, dropped_hash: 0,
            dual: [], dropped_closed: [])
      end
    end

    module_function

    # Convert the org files under the source dir to JSONL. Returns true on
    # success, false (with a message on `err`) on any refusal or failure.
    def run(argv, default_dir:, out: $stdout, err: $stderr)
      opts = parse_args(argv)
      src_dir = opts[:from] || default_dir
      gtd     = File.join(src_dir, "gtd.org")
      archive = File.join(src_dir, "archive.org")
      unless File.exist?(gtd)
        err.puts "migrate: no gtd.org found in #{src_dir}"
        return false
      end

      tasks_out   = File.join(src_dir, "tasks.jsonl")
      archive_out = File.join(src_dir, "archive.jsonl")
      unless opts[:force]
        clashes = [tasks_out, archive_out].select { |f| File.exist?(f) }
        unless clashes.empty?
          err.puts "migrate: refusing to overwrite #{clashes.map { |f| File.basename(f) }.join(", ")} (pass --force)"
          return false
        end
      end

      # Step 1 — legacy org lint. Warnings are informational; any error aborts.
      errors, warnings = lint_files([gtd, archive].select { |f| File.exist?(f) })
      warnings.each { |f, ln, m| out.puts "warn  #{File.basename(f)}:#{ln}: #{m}" }
      unless errors.empty?
        errors.each { |f, ln, m| err.puts "error #{File.basename(f)}:#{ln}: #{m}" }
        err.puts "migrate: aborting — fix the #{errors.size} org error(s) above first."
        return false
      end

      # Ids are minted excluding EVERY existing :ID: across both files.
      taken = existing_ids(gtd)
      taken |= existing_ids(archive) if File.exist?(archive)
      minter  = Minter.new(taken)
      summary = Summary.blank

      # Step 2/3 — walk both files into records.
      live_records = convert_live(gtd, minter, summary)
      arch_records = File.exist?(archive) ? convert_archive(archive, minter, summary) : []

      live_out = [meta_record, *live_records]
      arch_out = arch_records.empty? ? nil : [meta_record, *arch_records]

      # Step 4 (pre-write half) — internal sanity pass. This should be
      # impossible to trip given a correct walk; if it does, fail loudly rather
      # than write a broken store.
      problems = validate(live_out)
      problems.concat(validate(arch_out)) if arch_out
      problems.concat(cross_file_dups(live_out, arch_out))
      unless problems.empty?
        err.puts "migrate: internal validation failed (this is a bug):"
        problems.each { |p| err.puts "  #{p}" }
        return false
      end

      if opts[:dry_run]
        print_summary(out, summary, minter, tasks_out, archive_out, dry: true)
        preview(out, tasks_out, live_out)
        preview(out, archive_out, arch_out) if arch_out
        return true
      end

      Atomic.write(tasks_out, Format.dump(live_out))
      Atomic.write(archive_out, Format.dump(arch_out)) if arch_out
      print_summary(out, summary, minter, tasks_out, archive_out, dry: false)
      true
    rescue UsageError => e
      err.puts "migrate: #{e.message}"
      err.puts "usage: tasks migrate [--from <dir>] [--dry-run] [--force]"
      false
    end

    def parse_args(argv)
      opts = { from: nil, dry_run: false, force: false }
      argv = argv.dup
      while (a = argv.shift)
        case a
        when "--from"    then opts[:from] = argv.shift or raise UsageError, "missing value for --from"
        when "--dry-run" then opts[:dry_run] = true
        when "--force"   then opts[:force] = true
        else raise UsageError, "unknown argument: #{a}"
        end
      end
      opts
    end

    def meta_record = { "type" => "meta", "version" => Format::VERSION }

    # -- record emission -------------------------------------------------------

    def convert_live(path, minter, summary)
      lines = read_lines(path)
      roots = build_tree(lines, parse_lines(lines))
      records = []
      roots.each { |n| emit(n, nil, nil, minter, summary, records) }
      records
    end

    # Archive.org is a sequence of `# Archived <date>` chunks (with an optional
    # pre-first-separator block). Each chunk parses like gtd.org; its top-level
    # nodes become roots stamped with the chunk's date (the pre-separator block
    # gets none), and their descendants keep internal parents.
    def convert_archive(path, minter, summary)
      records = []
      archive_chunks(read_lines(path)).each do |date, clines|
        roots = build_tree(clines, parse_lines(clines))
        roots.each { |n| emit(n, nil, date, minter, summary, records) }
      end
      records
    end

    # Split archive lines into [date_or_nil, lines] chunks on the separators.
    def archive_chunks(lines)
      chunks = []
      date = nil
      buf = []
      lines.each do |l|
        if (m = l.match(ARCHIVE_SEP))
          chunks << [date, buf] unless buf.empty?
          date = m[1]
          buf = []
        else
          buf << l
        end
      end
      chunks << [date, buf] unless buf.empty?
      chunks
    end

    # Emit a node and its subtree, pre-order. `archived_date` stamps THIS node
    # only (roots of an archive chunk); children never inherit it.
    def emit(node, parent_id, archived_date, minter, summary, records)
      id = node.item&.id || minter.mint
      records << build_record(node, id, parent_id, archived_date, summary)
      node.children.each { |c| emit(c, id, nil, minter, summary, records) }
    end

    def build_record(node, id, parent_id, archived_date, summary)
      body = body_text(node.body, summary)
      rec =
        if node.item
          summary.tasks += 1
          task_record(node, id, parent_id, summary)
        else
          summary.sections += 1
          { "type" => "section", "id" => id }.tap do |r|
            r["parent"] = parent_id if parent_id
            r["title"] = node.title
          end
        end
      if archived_date
        rec["archived"] = archived_date
        summary.archived += 1
      end
      unless body.empty?
        rec["body"] = body
        summary.bodies += 1
      end
      rec
    end

    def task_record(node, id, parent_id, summary)
      item = node.item
      rec = { "type" => "task", "id" => id }
      rec["parent"]   = parent_id if parent_id
      rec["state"]    = item.state
      rec["priority"] = item.priority if item.priority
      rec["title"]    = item.title
      rec["tags"]     = item.tags unless item.tags.empty?
      rec["scheduled"] = item.scheduled.iso8601 if item.scheduled
      rec["deadline"]  = item.deadline.iso8601 if item.deadline
      recur = recur_for(node, item, summary)
      rec["recur"] = recur if recur
      if item.closed
        if OPEN_STATES.include?(item.state)
          # CLOSED on an open task is meaningless in the new schema (Check will
          # error on it) — drop it and report the loss.
          summary.dropped_closed << item.title
        else
          rec["closed"] = item.closed.iso8601
        end
      end
      rec
    end

    # The recurrence cookie, matching parse_lines' DEADLINE-over-SCHEDULED
    # precedence (item.recur already encodes it). When BOTH stamps carry a
    # cookie, the SCHEDULED one is discarded — report it.
    def recur_for(node, item, summary)
      cookies = {}
      node.body.each do |l|
        next unless (m = l.match(STAMP)) && m[3]
        cookies[m[1]] ||= m[3]
      end
      if cookies["DEADLINE"] && cookies["SCHEDULED"]
        summary.dual << [item.title, cookies["DEADLINE"], cookies["SCHEDULED"]]
      end
      item.recur
    end

    # A node's own prose, mapped to the schema `body` string: drawer machinery,
    # planning stamps, and `#`-comment lines removed (the same filter every org
    # surface applied via Store.prose — the `#` lines were invisible to it, so
    # migration drops them and counts them), blank edges trimmed, then dedented
    # by the common leading whitespace of the remaining non-blank lines and
    # joined with "\n". Empty prose yields "" (the caller omits the key).
    def body_text(raw, summary)
      kept = []
      strip_drawer(raw).each do |l|
        if l =~ PLANNING
          next
        elsif l.start_with?("#")
          summary.dropped_hash += 1
        else
          kept << l.chomp
        end
      end
      kept.shift while kept.any? && kept.first.strip.empty?
      kept.pop   while kept.any? && kept.last.strip.empty?
      return "" if kept.empty?
      dedent(kept).join("\n")
    end

    def dedent(lines)
      non_blank = lines.reject { |l| l.strip.empty? }
      return lines.map { |l| l.strip.empty? ? "" : l } if non_blank.empty?
      common = non_blank.map { |l| l[/\A[ \t]*/] }.reduce do |a, b|
        n = 0
        n += 1 while n < a.length && n < b.length && a[n] == b[n]
        a[0, n]
      end
      lines.map do |l|
        next "" if l.strip.empty?
        common.empty? || !l.start_with?(common) ? l : l[common.length..]
      end
    end

    # -- org parsing (copied from store.rb / tree.rb) --------------------------

    def read_lines(path)
      File.readlines(path, encoding: "UTF-8")
    rescue Errno::ENOENT
      []
    end

    def parse_lines(all_lines)
      items = []
      current = nil
      in_drawer = false
      all_lines.each.with_index(1) do |line, lineno|
        if (m = line.match(HEADLINE))
          current = Item.new(
            state: m[1], priority: m[2], title: m[3].strip,
            tags: (m[4] || "").split(":").reject(&:empty?), line: lineno
          )
          items << current
          in_drawer = false
        elsif line =~ /^\*+\s/
          current = nil
          in_drawer = false
        elsif line =~ DRAWER_START
          in_drawer = true
        elsif line =~ DRAWER_END
          in_drawer = false
        elsif current && (s = line.match(STAMP))
          begin
            d = Date.parse(s[2])
            current.scheduled = d if s[1] == "SCHEDULED"
            current.deadline  = d if s[1] == "DEADLINE"
            current.recur = s[3] if s[3] && (s[1] == "DEADLINE" || current.recur.nil?)
          rescue Date::Error
            # impossible date — the lint already flagged it; leave nil.
          end
        elsif current && (cm = line.match(CLOSED_STAMP))
          begin
            current.closed = Date.parse(cm[1])
          rescue Date::Error
            # impossible date — leave nil.
          end
        elsif current && in_drawer && (idm = line.match(ID_LINE))
          current.id ||= idm[1]
        end
      end
      items
    end

    # Star-count tree over `lines`; each headline binds to the item parse_lines
    # produced for its line, and each non-headline line joins the innermost open
    # node's body (own-lines scope — a child headline starts a new node).
    def build_tree(lines, items)
      by_line = items.to_h { |i| [i.line, i] }
      roots = []
      stack = []
      lines.each_with_index do |line, idx|
        stars = line[/\A(\*+)\s/, 1]
        unless stars
          stack.last&.body&.push(line)
          next
        end
        item = by_line[idx + 1]
        node = Node.new(
          title: item ? item.title : line.sub(/\A\*+\s+/, "").strip,
          line: idx + 1, level: stars.length, item: item, body: [], children: []
        )
        stack.pop while stack.any? && stack.last.level >= node.level
        if (parent = stack.last)
          parent.children << node
        else
          roots << node
        end
        stack << node
      end
      roots
    end

    def strip_drawer(lines)
      in_drawer = false
      lines.reject do |l|
        if l =~ DRAWER_START
          in_drawer = true
        elsif in_drawer && l =~ DRAWER_END
          in_drawer = false
          true
        elsif in_drawer && l =~ /^\s*:[\w-]+:/
          true
        elsif in_drawer
          in_drawer = false
          false
        else
          false
        end
      end
    end

    # Every :ID: that owns a task (inside a PROPERTIES drawer under a task
    # headline). Same scoping as the store's id_index: a section/subtask/bare
    # :ID: doesn't count.
    def existing_ids(path)
      ids = Set.new
      in_task = false
      in_drawer = false
      read_lines(path).each do |line|
        if line.match?(HEADLINE)    then in_task = true;  in_drawer = false
        elsif line =~ /^\*+\s/      then in_task = false; in_drawer = false
        elsif line =~ DRAWER_START  then in_drawer = true
        elsif line =~ DRAWER_END    then in_drawer = false
        elsif in_task && in_drawer && (m = line.match(ID_LINE))
          ids << m[1]
        end
      end
      ids
    end

    # -- legacy org lint (copied from check.rb) --------------------------------

    def lint_files(paths)
      errors = []
      warnings = []
      paths.each do |path|
        e, w = lint(path)
        errors.concat(e.map { |ln, m| [path, ln, m] })
        warnings.concat(w.map { |ln, m| [path, ln, m] })
      end
      [errors, warnings]
    end

    def lint(path)
      errors = []
      warnings = []
      raw = File.read(path, encoding: "UTF-8")
      return [[[0, "file is not valid UTF-8"]], []] unless raw.valid_encoding?

      in_task = false
      in_drawer = false
      drawer_at = nil
      open_titles = Hash.new { |h, k| h[k] = [] }
      ids = Hash.new { |h, k| h[k] = [] }

      raw.each_line.with_index(1) do |line, no|
        if line =~ DRAWER_START
          in_drawer = true
          drawer_at = no
        elsif line =~ DRAWER_END
          in_drawer = false
        elsif in_task && in_drawer && (m = line.match(ID_LINE))
          ids[m[1]] << no
        end

        case line
        when /^(\*+)\s+(\S+)(.*)$/
          warnings << [drawer_at, "unterminated :PROPERTIES: drawer (missing :END:)"] if in_drawer
          in_drawer = false
          stars, first = Regexp.last_match(1), Regexp.last_match(2)
          if STATES.include?(first)
            lint_task_headline(line, no, errors)
            if (m = line.match(TASK_HEADLINE)) && OPEN_STATES.include?(first)
              open_titles[m[1].downcase] << no
            end
            in_task = true
          else
            if stars.length >= 2 && first =~ /\A[A-Z]{3,}\z/
              errors << [no, "unknown state keyword #{first.inspect} (typo? expected #{STATES.join("/")})"]
            end
            in_task = false
          end
        when METADATA
          kind = Regexp.last_match(1)
          if in_task
            lint_metadata(line, kind, no, errors)
          else
            errors << [no, "#{kind}: line with no task headline above it"]
          end
        when /^\s*(SCHEDULED|DEADLINE|CLOSED):/
          errors << [no, "#{Regexp.last_match(1)}: at top level (lost its task headline?)"] unless in_task
        end
      end

      warnings << [drawer_at, "unterminated :PROPERTIES: drawer (missing :END:)"] if in_drawer
      open_titles.each do |title, lns|
        next if lns.size < 2
        warnings << [lns.last, "duplicate open title #{title.inspect} (lines #{lns.join(", ")})"]
      end
      ids.each do |id, lns|
        next if lns.size < 2
        errors << [lns.last, "duplicate :ID: #{id.inspect} (lines #{lns.join(", ")})"]
      end
      [errors, warnings]
    end

    def lint_task_headline(line, no, errors)
      if line =~ /\[#([^\]]*)\]/ && !%w[A B C].include?(Regexp.last_match(1))
        errors << [no, "invalid priority cookie [##{Regexp.last_match(1)}] (expected [#A], [#B], or [#C])"]
      end
      errors << [no, "malformed task headline: #{line.strip.inspect}"] unless line.match?(TASK_HEADLINE)
    end

    def lint_metadata(line, kind, no, errors)
      if kind == "CLOSED"
        date = line[/CLOSED:\s*\[(\d{4}-\d{2}-\d{2})\]/, 1]
        errors << [no, "CLOSED: expects [YYYY-MM-DD]"] unless date
      else
        date = line[/#{kind}:\s*<(\d{4}-\d{2}-\d{2})/, 1]
        errors << [no, "#{kind}: expects <YYYY-MM-DD …>"] unless date
      end
      return unless date
      y, m, d = date.split("-").map(&:to_i)
      errors << [no, "#{kind}: #{date} is not a real date"] unless Date.valid_date?(y, m, d)
    end

    # -- internal validation (a private stand-in for stage 4's Check) ----------

    # Confirm the emitted records honor the invariants the JSONL store depends
    # on: unique ids, backward-resolving parents, DFS pre-order contiguity, and
    # well-formed states/dates/recur. Returns a list of problem strings (empty
    # when clean).
    def validate(records)
      problems = []
      seen = Set.new
      stack = [] # ids on the current DFS path
      records.each do |r|
        next if r["type"] == "meta"
        id = r["id"]
        problems << "record missing id: #{r.inspect}" if id.nil? || id.empty?
        problems << "duplicate id #{id}" if id && seen.include?(id)

        if r["type"] == "task"
          problems << "bad state #{r["state"].inspect}" unless STATES.include?(r["state"])
        elsif r["type"] != "section"
          problems << "unknown type #{r["type"].inspect}"
        end
        %w[scheduled deadline closed archived].each do |k|
          v = r[k]
          problems << "bad #{k} date #{v.inspect}" if v && !valid_iso?(v)
        end
        if (rc = r["recur"]) && rc !~ /\A#{REPEATER}\z/
          problems << "bad recur cookie #{rc.inspect}"
        end

        parent = r["parent"]
        if parent.nil?
          stack = [id]
        else
          problems << "parent #{parent} of #{id} not seen earlier" unless seen.include?(parent)
          stack.pop while stack.any? && stack.last != parent
          problems << "parent #{parent} of #{id} breaks DFS pre-order contiguity" unless stack.last == parent
          stack.push(id)
        end
        seen << id if id
      end
      problems
    end

    def cross_file_dups(live, arch)
      return [] unless arch
      ids = (live + arch).reject { |r| r["type"] == "meta" }.map { |r| r["id"] }.compact
      ids.group_by(&:itself).select { |_, v| v.size > 1 }.keys.map { |id| "id #{id} appears in both files" }
    end

    def valid_iso?(str)
      return false unless str =~ /\A\d{4}-\d{2}-\d{2}\z/
      y, m, d = str.split("-").map(&:to_i)
      Date.valid_date?(y, m, d)
    end

    # -- output ----------------------------------------------------------------

    def print_summary(out, summary, minter, tasks_out, archive_out, dry:)
      out.puts(dry ? "migrate --dry-run — nothing written" : "migrate complete")
      out.puts "  sections:        #{summary.sections}"
      out.puts "  tasks:           #{summary.tasks}"
      out.puts "  archived roots:  #{summary.archived}"
      out.puts "  ids minted:      #{minter.minted}"
      out.puts "  bodies carried:  #{summary.bodies}"
      out.puts "  #-lines dropped: #{summary.dropped_hash}"
      unless summary.dual.empty?
        out.puts "  discarded SCHEDULED cookies (DEADLINE kept):"
        summary.dual.each { |t, dl, sc| out.puts "    #{t}: kept #{dl}, discarded #{sc}" }
      end
      unless summary.dropped_closed.empty?
        out.puts "  dropped CLOSED on open tasks:"
        summary.dropped_closed.each { |t| out.puts "    #{t}" }
      end
      return if dry

      out.puts
      out.puts "wrote #{tasks_out}"
      out.puts "wrote #{archive_out}" if File.exist?(archive_out)
      out.puts
      out.puts "next steps:"
      out.puts "  1. eyeball the jsonl, then run `tasks check`"
      out.puts "  2. commit the jsonl files"
      out.puts "  3. `git rm gtd.org archive.org` once you're satisfied"
    end

    def preview(out, path, records)
      out.puts
      out.puts "--- #{File.basename(path)} (first 10 lines) ---"
      Format.dump(records).each_line.first(10).each { |l| out.print l }
    end
  end
end
