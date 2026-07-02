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
      open_titles = Hash.new { |h, k| h[k] = [] }

      raw.each_line.with_index(1) do |line, no|
        case line
        when /^(\*+)\s+(\S+)(.*)$/
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

      open_titles.each do |title, lines|
        next if lines.size < 2
        warnings << [lines.last, "duplicate open title #{title.inspect} (lines #{lines.join(", ")}) — fuzzy refs will be ambiguous"]
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
