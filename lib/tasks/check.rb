# frozen_string_literal: true

require "date"
require "set"
require_relative "format"

module Tasks
  # Structural linter for a tasks.jsonl (or archive.jsonl) file. Format keeps a
  # line from ever crashing the store; Check catches the *semantic* breakage a
  # bad edit (human or LLM) could introduce — an unresolved parent, a duplicate
  # id, an out-of-DFS-order record, a bad state — early and precisely.
  #
  # Errors are things the tooling would misparse or that break invariants the
  # store relies on. Warnings are hazards (e.g. duplicate open titles break
  # fuzzy refs). The Result shape (errors/warnings as (line, message) tuples,
  # to_h) matches the old org linter so cmd_check and with_history don't change.
  module Check
    STATES      = %w[INBOX TODO NEXT WAITING DONE CANCELLED].freeze
    OPEN_STATES = %w[INBOX TODO NEXT WAITING].freeze
    DONE_STATES = %w[DONE CANCELLED].freeze
    PRIORITIES  = %w[A B C].freeze
    TYPES       = %w[meta section task].freeze

    ID_RE    = /\A[0-9a-f]{8}\z/
    DATE_RE  = /\A\d{4}-\d{2}-\d{2}\z/
    # An org repeater cookie: +1w, ++2d, .+1m (see Tasks::Recur). A positive
    # count only — ++0d would never terminate a catch-up roll.
    RECUR_RE = /\A(?:\.\+|\+\+|\+)[1-9]\d*[dwmy]\z/

    # Fields a section record must not carry (task-only semantics). `archived`
    # is allowed — a swept subtree root can be a section.
    SECTION_FORBIDDEN = %w[state priority scheduled deadline recur closed tags].freeze
    # Every key the schema knows (plus the out-of-band line stamp / meta version).
    KNOWN_KEYS = (Format::KEY_ORDER + %w[line version]).to_set

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
      return Result.new([[0, "file not found: #{path}"]], []) unless File.exist?(path)

      raw = File.read(path, encoding: "UTF-8")
      check_text(raw)
    end

    # Validate bytes already captured by a caller. Store's API-grade read path
    # uses this while holding its sidecar lock so validation and the canonical
    # resources are derived from the same read, rather than checking a path and
    # reopening it afterward.
    def check_text(raw)
      return Result.new([[0, "file is not valid UTF-8"]], []) unless raw.valid_encoding?

      check_parsed(Format.parse(raw))
    end

    # Validate one Format parse result without reparsing or rereading. This is
    # public for Store's coherent read capture; callers should normally prefer
    # #check or #check_text.
    def check_parsed(parsed)
      errors = parsed.errors.dup # unparseable / blank lines fold in as errors
      warnings = []
      records = parsed.records

      check_meta(records, errors)

      seen = Set.new                 # ids seen so far (for parent resolution)
      dup_ids = Hash.new { |h, k| h[k] = [] }
      open_titles = Hash.new { |h, k| h[k] = [] }
      stack = []                     # ids on the current DFS path

      records.each do |r|
        line = r["line"]
        type = r["type"]

        # meta is only valid on line 1 (checked above); a later one is an error.
        if type == "meta"
          errors << [line, "unexpected meta record (only valid on line 1)"] unless line == 1
          next
        end
        unless TYPES.include?(type)
          errors << [line, "unknown record type #{type.inspect}"]
          next
        end

        check_id(r, line, errors, dup_ids)
        check_keys(r, line, warnings)
        stack = check_parent(r, line, seen, stack, errors)

        if type == "task"
          check_task(r, line, errors)
          open_titles[r["title"].to_s.downcase] << line if OPEN_STATES.include?(r["state"])
        else
          check_section(r, line, errors)
        end

        seen << r["id"] if r["id"]
      end

      dup_ids.each do |id, lines|
        next if lines.size < 2
        errors << [lines.last, "duplicate id #{id.inspect} (lines #{lines.join(", ")}) — id refs will be wrong"]
      end
      open_titles.each do |title, lines|
        next if lines.size < 2
        warnings << [lines.last, "duplicate open title #{title.inspect} (lines #{lines.join(", ")}) — fuzzy refs will be ambiguous"]
      end

      Result.new(errors.sort_by(&:first), warnings.sort_by(&:first))
    end

    # Line 1 must be a well-formed meta record at the current schema version.
    def check_meta(records, errors)
      meta = records.find { |r| r["line"] == 1 }
      if meta.nil?
        errors << [1, "missing meta record on line 1"] unless errors.any? { |l, _| l == 1 }
      elsif meta["type"] != "meta"
        errors << [1, "line 1 must be a meta record ({\"type\":\"meta\",\"version\":#{Format::VERSION}})"]
      elsif !meta["version"].is_a?(Integer) || meta["version"] != Format::VERSION
        # A non-Integer version (e.g. the float 1.0, which `1.0 == 1` would
        # otherwise wave through) is unsupported, not just a wrong number.
        errors << [1, "unsupported meta version #{meta["version"].inspect} (expected #{Format::VERSION})"]
      end
    end

    # Check must never raise on malformed raw JSON — a non-String id (e.g. the
    # integer 12345678) would blow up `id !~ ID_RE`, and since with_history runs
    # Check AFTER writing, that raise would bypass the rollback. Type-guard first,
    # then emit an error tuple describing the real problem.
    def check_id(r, line, errors, dup_ids)
      id = r["id"]
      if id.nil? || id == ""
        errors << [line, "record missing id"]
      elsif !id.is_a?(String) || id !~ ID_RE
        errors << [line, "malformed id #{id.inspect} (expected 8 hex chars)"]
      else
        dup_ids[id] << line
      end
    end

    # Parent must resolve to an EARLIER record, and the record must sit in DFS
    # pre-order (its parent is the top of the open ancestor stack). Returns the
    # updated stack.
    def check_parent(r, line, seen, stack, errors)
      parent = r["parent"]
      id = r["id"]
      if parent.nil?
        return [id]
      end
      unless seen.include?(parent)
        errors << [line, "parent #{parent.inspect} does not resolve to an earlier record"]
        return stack
      end
      stack = stack.dup
      stack.pop while stack.any? && stack.last != parent
      if stack.last == parent
        stack.push(id)
      else
        errors << [line, "record #{id.inspect} breaks DFS pre-order (parent #{parent.inspect} is not an open ancestor)"]
      end
      stack
    end

    def check_keys(r, line, warnings)
      r.each_key do |k|
        warnings << [line, "unknown key #{k.inspect}"] unless KNOWN_KEYS.include?(k)
      end
    end

    def check_task(r, line, errors)
      unless STATES.include?(r["state"])
        errors << [line, "invalid state #{r["state"].inspect} (expected #{STATES.join("/")})"]
      end
      if r["priority"] && !PRIORITIES.include?(r["priority"])
        errors << [line, "invalid priority #{r["priority"].inspect} (expected A, B, or C)"]
      end
      title = r["title"]
      if title.nil? || (title.is_a?(String) && title.strip.empty?)
        errors << [line, "task has no title"]
      elsif !title.is_a?(String)
        errors << [line, "title must be a string"]
      end
      %w[scheduled deadline closed].each { |k| check_date(r, k, line, errors) }
      check_date(r, "archived", line, errors)
      # Guard the type before the regex: a non-String recur (e.g. an integer)
      # would raise on `!~`, and Check must report — never crash — on bad data.
      if (rc = r["recur"]) && (!rc.is_a?(String) || rc !~ RECUR_RE)
        errors << [line, "invalid recur cookie #{rc.inspect} (expected e.g. .+1w, ++1m, +2d)"]
      end
      if r["closed"] && OPEN_STATES.include?(r["state"])
        errors << [line, "closed date on an open task (#{r["state"]})"]
      end
      if r["tags"] && !r["tags"].is_a?(Array)
        errors << [line, "tags must be an array"]
      elsif r["tags"].is_a?(Array) && r["tags"].any? { |t| !t.is_a?(String) }
        errors << [line, "tags must all be strings"]
      end
    end

    def check_section(r, line, errors)
      if r["title"].nil? || r["title"].to_s.strip.empty?
        errors << [line, "section has no title"]
      end
      SECTION_FORBIDDEN.each do |k|
        errors << [line, "section must not carry #{k.inspect}"] if r[k]
      end
      check_date(r, "archived", line, errors)
    end

    def check_date(r, key, line, errors)
      v = r[key]
      return unless v
      unless v.is_a?(String) && v =~ DATE_RE
        errors << [line, "#{key} #{v.inspect} is not a YYYY-MM-DD date"]
        return
      end
      y, m, d = v.split("-").map(&:to_i)
      errors << [line, "#{key} #{v} is not a real date"] unless Date.valid_date?(y, m, d)
    end
  end
end
