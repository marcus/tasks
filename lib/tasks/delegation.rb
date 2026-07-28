# frozen_string_literal: true

require "time"

module Tasks
  # The optional `delegation` object: its vocabulary, its fixed nested key
  # order, and the shape invariants every writer must satisfy. Format emits
  # from KEY_ORDER, Check lints parsed records with #errors, and Store enforces
  # the same rules before it writes — one definition, three enforcement points.
  #
  # Absent means "not delegated": there is no empty or neutral delegation value,
  # which is why every mutation either installs a complete object or removes the
  # key outright.
  module Delegation
    FIELD = "delegation"

    # Fixed emission order. Absent keys are omitted so one-line diffs stay
    # readable and two writers can never produce different bytes for one state.
    KEY_ORDER = %w[kind mode status assignee at work_ref].freeze

    KINDS = %w[human agent].freeze
    # Agent authority. Widened only by the owner — a worker never promotes its
    # own mode (see TASK_AGENT.md).
    MODES = %w[refine research implement].freeze

    DELEGATED = "delegated" # human, awaiting the person
    READY = "ready"         # agent pool, unclaimed
    CLAIMED = "claimed"     # agent pool, held by one worker
    AGENT_STATUSES = [READY, CLAIMED].freeze
    STATUSES = ([DELEGATED] + AGENT_STATUSES).freeze

    # A worker id is a free-form token (recommended `<harness>/<model>/<session>`);
    # only non-empty, whitespace-free, and bounded are guaranteed. The bound
    # applies to human assignees too — an identifier, not a mail integration.
    ASSIGNEE_LIMIT = 200
    # One reference to where the work lives, not a document: bounded so a
    # pathological paste cannot push a 100 kB field onto one JSONL line.
    WORK_REF_LIMIT = 500
    # `at` is the UTC timestamp of the last status transition, spelled like the
    # timestamp half of an UpdateStamp so the two never drift apart.
    AT_RE = /\A\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\z/

    # C0 controls, DEL, and the C1 block. None of them belong in an identifier
    # or a reference, and every one of them is a rendering hazard: a worker id
    # carrying `\e[2K\e[1A` rewrites the terminal of the agent that *lost* a
    # claim race (the conflict line names the holder), and any non-SGR escape
    # desynchronizes the TUI's cell arithmetic, since Ansi.vislen strips SGR
    # only. Refuse the bytes at the schema boundary rather than sanitizing at
    # each of the four surfaces that render them.
    CONTROL_RE = /[\u0000-\u001F\u007F-\u009F]/
    # Ruby's `\s` is ASCII-only, so NBSP (U+00A0), the line/paragraph separators
    # (U+2028/U+2029), and the ideographic space would all satisfy a "no
    # whitespace" identifier rule while still looking like a break on screen.
    # The POSIX class is Unicode-aware and covers them.
    WHITESPACE_RE = /[[:space:]]/
    # Anything that would split one reference across two rendered lines.
    LINE_BREAK_RE = /[\r\n\u2028\u2029]/

    # A non-empty local part, exactly one `@`, and a dotted domain whose labels
    # are all non-empty. Deliberately far short of RFC 5322: the job is to
    # refuse `@work` — muscle memory from the TUI's `@` context filter, and one
    # keystroke away from silently moving a task to WAITING — not to police
    # addresses. Whitespace and control characters are already excluded by
    # #identifier?, so the character classes only have to exclude `@` and `.`.
    EMAIL_RE = /\A[^@]+@[^@.]+(?:\.[^@.]+)+\z/

    module_function

    # The canonical UTC spelling of `at` for a Time.
    def stamp(time) = time.utc.strftime("%Y-%m-%dT%H:%M:%SZ")

    # A copy of `value` in KEY_ORDER with absent entries dropped, then any keys
    # this binary does not know in their own order — the same rule Format
    # applies, so an in-memory delegation already matches the bytes that will be
    # written. Keeping the unknown tail is what lets a claim or release rewrite
    # a record from a newer binary without silently deleting its new field.
    def ordered(value)
      return value unless value.is_a?(Hash)

      copy = {}
      KEY_ORDER.each do |key|
        child = value[key]
        next if absent?(child)

        copy[key] = child
      end
      value.each do |key, child|
        name = key.to_s
        next if KEY_ORDER.include?(name) || absent?(child)

        copy[name] = child
      end
      copy
    end

    def absent?(child) = child.nil? || (child.respond_to?(:empty?) && child.empty?)

    def object?(value) = value.is_a?(Hash) && !value.empty?
    def agent?(value) = object?(value) && value["kind"] == "agent"
    def human?(value) = object?(value) && value["kind"] == "human"
    def ready?(value) = agent?(value) && value["status"] == READY
    def claimed?(value) = agent?(value) && value["status"] == CLAIMED

    # Shape, then reality: Time.iso8601 rolls an impossible day over (Feb 31
    # becomes Mar 3), so require the parsed instant to render back to the exact
    # bytes it came from.
    def timestamp?(value)
      return false unless value.is_a?(String) && AT_RE.match?(value)

      parsed = Time.iso8601(value)
      parsed.utc? && stamp(parsed) == value
    rescue ArgumentError
      false
    end

    # The hygiene both identity fields share: real UTF-8 (argv can carry
    # arbitrary bytes, and matching against invalid text raises), non-empty,
    # bounded, no whitespace in any script, and no control or escape bytes.
    def identifier?(value)
      value.is_a?(String) && value.valid_encoding? && !value.empty? &&
        value.length <= ASSIGNEE_LIMIT &&
        !CONTROL_RE.match?(value) && !WHITESPACE_RE.match?(value)
    end

    # An identifier, not an address to send to — but still address-*shaped*, so
    # a stray `@work` cannot become a person the task is now waiting on.
    def email?(value) = identifier?(value) && EMAIL_RE.match?(value)

    def worker?(value) = identifier?(value)

    def valid?(value) = errors(value).empty?

    # Nested keys this binary does not know, sorted. Deliberately *not* part of
    # #errors: forward compatibility is the schema's posture at the top level
    # (Format round-trips unknown keys, Check only warns), and it must not
    # invert one level down. A `delegation.lease_until` from a newer binary
    # would otherwise fail `check`, make JsonlMerge refuse a whole merge, and —
    # because the post-write Check validates every line — block every write
    # store-wide until a patch happened to land on that one record and drop it.
    def unknown_keys(value)
      return [] unless value.is_a?(Hash)

      (value.keys.map(&:to_s) - KEY_ORDER).sort
    end

    # Every invariant violation in `value`, as plain messages a caller can
    # stamp with a line number (Check) or report as a refusal (Store). An empty
    # array means the object satisfies the whole contract.
    def errors(value)
      return ["delegation must be an object"] unless value.is_a?(Hash)
      return ["delegation must not be empty"] if value.empty?

      messages = []
      messages.concat(kind_errors(value))
      unless timestamp?(value["at"])
        messages << "delegation.at #{value["at"].inspect} is not a UTC timestamp (YYYY-MM-DDTHH:MM:SSZ)"
      end
      messages.concat(work_ref_errors(value)) if value.key?("work_ref")
      messages
    end

    # kind decides which of the other fields are required, forbidden, or
    # constrained, so a bad kind stops the cascade rather than inventing three
    # follow-on errors about fields whose rules are unknown.
    def kind_errors(value)
      case value["kind"]
      when "human" then human_errors(value)
      when "agent" then agent_errors(value)
      else ["delegation.kind #{value["kind"].inspect} must be #{KINDS.join(" or ")}"]
      end
    end

    def human_errors(value)
      messages = []
      messages << "delegation.mode is not allowed for a human delegation" if value.key?("mode")
      unless value["status"] == DELEGATED
        messages << "delegation.status #{value["status"].inspect} must be #{DELEGATED.inspect} for a human delegation"
      end
      unless email?(value["assignee"])
        messages << "delegation.assignee #{value["assignee"].inspect} must be an email address " \
                    "(local@domain.tld, no whitespace or control characters, " \
                    "at most #{ASSIGNEE_LIMIT} chars)"
      end
      messages
    end

    def agent_errors(value)
      messages = []
      unless MODES.include?(value["mode"])
        messages << "delegation.mode #{value["mode"].inspect} must be one of #{MODES.join("/")}"
      end
      case value["status"]
      when READY
        messages << "delegation.assignee is not allowed while #{READY}" if value.key?("assignee")
      when CLAIMED
        unless worker?(value["assignee"])
          messages << "delegation.assignee #{value["assignee"].inspect} must be a worker id " \
                      "(non-empty, no whitespace or control characters, " \
                      "at most #{ASSIGNEE_LIMIT} chars)"
        end
      else
        messages << "delegation.status #{value["status"].inspect} must be " \
                    "#{AGENT_STATUSES.join(" or ")} for an agent delegation"
      end
      messages
    end

    # One reference to where the work lives. Additional links belong in the task
    # body, so this stays a single non-empty, bounded, control-free line — it is
    # rendered raw by `show`, `list --delegated`, and the TUI detail panel, and
    # it shares a JSONL line with the whole record.
    def work_ref_errors(value)
      reference = value["work_ref"]
      unless reference.is_a?(String) && reference.valid_encoding? && !reference.strip.empty?
        return ["delegation.work_ref must be a non-empty string"]
      end
      return ["delegation.work_ref must be a single line"] if reference.match?(LINE_BREAK_RE)
      return ["delegation.work_ref must not contain control characters"] if CONTROL_RE.match?(reference)

      if reference.length > WORK_REF_LIMIT
        return ["delegation.work_ref must be at most #{WORK_REF_LIMIT} characters " \
                "(got #{reference.length})"]
      end

      []
    end
  end
end
