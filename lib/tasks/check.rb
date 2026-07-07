# frozen_string_literal: true

require "date"

module Tasks
  # Structural linter for gtd.org. The file is free-form text, so nothing
  # stops a bad edit (human or LLM) from quietly breaking parseability —
  # this catches mangling early and precisely.
  #
  # Errors are things the tooling would misparse or silently ignore.
  # Warnings are hazards (e.g. duplicate open titles break fuzzy refs).
  module Check
    STATES = %w[INBOX TODO NEXT WAITING DONE CANCELLED].freeze
    OPEN_STATES = %w[INBOX TODO NEXT WAITING].freeze

    TASK_HEADLINE = /^\*+\s+(?:#{STATES.join("|")})\s+(?:\[#[ABC]\]\s+)?(.*?)\s*(:[\w@:]+:)?\s*$/
    METADATA      = /^\s+(SCHEDULED|DEADLINE|CLOSED):/
    ID_LINE       = /^\s*:ID:\s+(\S+)\s*$/i
    DRAWER_START  = /^\s*:PROPERTIES:\s*$/i
    DRAWER_END    = /^\s*:END:\s*$/i

    Result = Struct.new(:errors, :warnings) do
      def ok? = errors.empty?
      def to_h
        {
          ok: ok?,
          errors:   errors.map   { |line, msg| { line: line, message: msg } },
          warnings: warnings.map { |line, msg| { line: line, message: msg } },
        }
      end
    end

    module_function

    def check(path)
      errors = []
      warnings = []
      return Result.new([[0, "file not found: #{path}"]], []) unless File.exist?(path)

      raw = File.read(path, encoding: "UTF-8")
      unless raw.valid_encoding?
        return Result.new([[0, "file is not valid UTF-8"]], [])
      end

      in_task = false
      in_drawer = false
      drawer_at = nil
      open_titles = Hash.new { |h, k| h[k] = [] }
      ids = Hash.new { |h, k| h[k] = [] }

      raw.each_line.with_index(1) do |line, no|
        # Only an :ID: inside a task's PROPERTIES drawer is a real task id — the
        # same scoping the store parses by (a section-heading or prose :ID: is
        # not a task handle, so it must not trip the uniqueness check).
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
          # A drawer still open at the next headline never got its :END:.
          warnings << [drawer_at, "unterminated :PROPERTIES: drawer (missing :END:)"] if in_drawer
          in_drawer = false
          stars, first = $1, $2
          if STATES.include?(first)
            check_task_headline(line, no, errors)
            if (m = line.match(TASK_HEADLINE)) && OPEN_STATES.include?(first)
              open_titles[m[1].downcase] << no
            end
            in_task = true
          else
            # A section heading — unless it looks like a typo'd state
            # keyword (all-caps token on a non-top-level headline).
            if stars.length >= 2 && first =~ /\A[A-Z]{3,}\z/
              errors << [no, "unknown state keyword #{first.inspect} (typo? expected #{STATES.join("/")})"]
            end
            in_task = false
          end
        when METADATA
          kind = $1
          if !in_task
            errors << [no, "#{kind}: line with no task headline above it"]
          else
            check_metadata(line, kind, no, errors)
          end
        when /^\s*(SCHEDULED|DEADLINE|CLOSED):/
          # metadata at column 0 — legal org but our parser expects it under
          # a task; catch the common paste error of losing the headline
          errors << [no, "#{$1}: at top level (lost its task headline?)"] unless in_task
        end
      end

      # A drawer still open at end of file also never closed.
      warnings << [drawer_at, "unterminated :PROPERTIES: drawer (missing :END:)"] if in_drawer

      open_titles.each do |title, lines|
        next if lines.size < 2
        warnings << [lines.last, "duplicate open title #{title.inspect} (lines #{lines.join(", ")}) — fuzzy refs will be ambiguous"]
      end

      # A stable :ID: must be unique — a collision silently points every id-based
      # ref and locate at whichever task the parser hits first.
      ids.each do |id, lines|
        next if lines.size < 2
        errors << [lines.last, "duplicate :ID: #{id.inspect} (lines #{lines.join(", ")}) — id refs will be wrong"]
      end

      Result.new(errors, warnings)
    end

    def check_task_headline(line, no, errors)
      if line =~ /\[#([^\]]*)\]/ && !%w[A B C].include?($1)
        errors << [no, "invalid priority cookie [##{$1}] (expected [#A], [#B], or [#C])"]
      end
      unless line.match?(TASK_HEADLINE)
        errors << [no, "malformed task headline: #{line.strip.inspect}"]
      end
    end

    def check_metadata(line, kind, no, errors)
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
  end
end
