# frozen_string_literal: true

require "json"

module Conformance
  # One typed difference. Typed first, formatted second: every dimension
  # produces these, and the report layer never sees anything else.
  #
  # `klass` is the playbook's step-6 classification. A comparator that only says
  # "differs" is not actionable, so every finding must carry one of these and
  # the rule that justifies it.
  class Finding
    # The five classes from docs/plans/active/language-porting-playbook.md § 6.
    GO_DEFECT = "go_defect"
    LEGACY_RUBY_RULE = "legacy_ruby_rule"
    NONDETERMINISM = "nondeterminism"
    INTENTIONAL_DIFFERENCE = "intentional_difference"
    MISSING_ORACLE_COVERAGE = "missing_oracle_coverage"

    # A sixth, deliberately added and stated out loud: the comparison itself is
    # not trustworthy (the two sides did not run the same case, a pin was
    # dropped, a stream was truncated before it could be compared). It is not a
    # verdict about the port; it is a refusal to render one. Folding it into
    # go_defect would blame the port for a harness fault, and folding it into
    # missing_oracle_coverage would make a broken run look like a gap in the
    # corpus. It fails the gate.
    HARNESS_ERROR = "harness_error"

    CLASSES = [GO_DEFECT, LEGACY_RUBY_RULE, NONDETERMINISM,
               INTENTIONAL_DIFFERENCE, MISSING_ORACLE_COVERAGE, HARNESS_ERROR].freeze

    # gate     — fails the run. The default for every real difference.
    # accepted — a difference Marcus has recorded in
    #            porting/intentional-differences.md; reported, does not fail.
    # advisory — never fails and never passes anything: environment and metrics.
    SEVERITIES = %w[gate accepted advisory].freeze

    attr_reader :case_id, :dimension, :field, :klass, :severity,
                :baseline, :candidate, :detail, :rule
    attr_accessor :requires_rerun

    def initialize(case_id:, dimension:, field:, klass:, rule:,
                   baseline: nil, candidate: nil, detail: nil, severity: "gate")
      raise ArgumentError, "unknown class #{klass}" unless CLASSES.include?(klass)
      raise ArgumentError, "unknown severity #{severity}" unless SEVERITIES.include?(severity)

      @case_id = case_id
      @dimension = dimension
      @field = field
      @klass = klass
      @rule = rule
      @baseline = baseline
      @candidate = candidate
      @detail = detail
      @severity = severity
      @requires_rerun = false
    end

    def gate? = severity == "gate"

    def to_h
      h = {
        "case_id" => case_id,
        "dimension" => dimension,
        "field" => field,
        "class" => klass,
        "severity" => severity,
        "rule" => rule,
        "baseline" => baseline,
        "candidate" => candidate
      }
      h["detail"] = detail if detail
      h["requires_rerun"] = true if requires_rerun
      h
    end

    # Values land in a JSON report and in a terminal, so an embedded 500 KiB
    # base64 blob has to be summarised. Summarising the DISPLAY is not
    # normalising the COMPARISON: the comparison already happened, over the full
    # value, before this is called.
    MAX_RENDER = 220

    def self.render(value)
      case value
      when String
        return value if value.length <= MAX_RENDER

        "#{value[0, MAX_RENDER]}… (#{value.length} chars, sha256:#{Digest::SHA256.hexdigest(value)[0, 16]})"
      when Hash, Array
        json = JSON.generate(value)
        return value if json.length <= MAX_RENDER

        "#{json[0, MAX_RENDER]}… (#{json.length} chars)"
      else
        value
      end
    end
  end
end
