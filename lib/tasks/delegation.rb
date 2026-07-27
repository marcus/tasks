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
    # `at` is the UTC timestamp of the last status transition, spelled like the
    # timestamp half of an UpdateStamp so the two never drift apart.
    AT_RE = /\A\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\z/

    module_function

    # The canonical UTC spelling of `at` for a Time.
    def stamp(time) = time.utc.strftime("%Y-%m-%dT%H:%M:%SZ")

    # A copy of `value` in KEY_ORDER with absent entries dropped. Store builds
    # every object through this so an in-memory delegation already matches the
    # bytes Format will write.
    def ordered(value)
      return value unless value.is_a?(Hash)

      KEY_ORDER.each_with_object({}) do |key, copy|
        child = value[key]
        next if child.nil? || (child.respond_to?(:empty?) && child.empty?)

        copy[key] = child
      end
    end

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

    # An identifier, not an address to send to: contains "@", carries no
    # whitespace, and stays within the shared bound.
    def email?(value)
      value.is_a?(String) && value.include?("@") && !value.match?(/\s/) &&
        !value.empty? && value.length <= ASSIGNEE_LIMIT
    end

    def worker?(value)
      value.is_a?(String) && !value.empty? && !value.match?(/\s/) &&
        value.length <= ASSIGNEE_LIMIT
    end

    def valid?(value) = errors(value).empty?

    # Every invariant violation in `value`, as plain messages a caller can
    # stamp with a line number (Check) or report as a refusal (Store). An empty
    # array means the object satisfies the whole contract.
    def errors(value)
      return ["delegation must be an object"] unless value.is_a?(Hash)
      return ["delegation must not be empty"] if value.empty?

      messages = []
      unknown = value.keys.map(&:to_s) - KEY_ORDER
      messages << "delegation has unknown keys: #{unknown.sort.join(", ")}" unless unknown.empty?
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
                    "(contains @, no whitespace, at most #{ASSIGNEE_LIMIT} chars)"
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
                      "(non-empty, no whitespace, at most #{ASSIGNEE_LIMIT} chars)"
        end
      else
        messages << "delegation.status #{value["status"].inspect} must be " \
                    "#{AGENT_STATUSES.join(" or ")} for an agent delegation"
      end
      messages
    end

    # One reference to where the work lives. Additional links belong in the task
    # body, so this stays a single non-empty line.
    def work_ref_errors(value)
      reference = value["work_ref"]
      return ["delegation.work_ref must be a non-empty string"] unless reference.is_a?(String) &&
                                                                       !reference.strip.empty?
      return ["delegation.work_ref must be a single line"] if reference.match?(/[\r\n]/)

      []
    end
  end
end
