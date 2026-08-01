# frozen_string_literal: true

require "base64"
require "digest"
require "json"

require_relative "normalize"
require_relative "finding"
require_relative "diffs"
require_relative "dimensions/cli"
require_relative "dimensions/files"
require_relative "dimensions/journal"
require_relative "dimensions/revisions"
require_relative "dimensions/performance"
require_relative "dimensions/http"

module Conformance
  # A set of observations, loaded from a directory of <case_id>.json or from a
  # JSONL stream.
  class ObservationSet
    attr_reader :source, :by_case

    def self.load(path)
      if File.directory?(path)
        records = Dir[File.join(path, "*.json")].sort.map { |f| JSON.parse(File.read(f)) }
      else
        records = File.readlines(path).map(&:strip).reject(&:empty?).map { |l| JSON.parse(l) }
      end
      new(path, records)
    end

    def initialize(source, records)
      @source = source
      @by_case = {}
      records.each do |r|
        id = r["case_id"]
        raise ArgumentError, "duplicate case_id #{id.inspect} in #{source}" if @by_case.key?(id)

        @by_case[id] = r
      end
    end

    def case_ids = by_case.keys.sort
    def size = by_case.size

    # Provenance the report echoes back, so a reader knows what was compared.
    def provenance
      impls = by_case.values.map { |o| o["implementation"] }.compact.uniq
      {
        "source" => source,
        "cases" => size,
        "implementations" => impls,
        "environments" => by_case.values.map { |o| o["environment"] }.compact.uniq
      }
    end
  end

  # The per-case comparison context. Dimensions talk only to this: they add
  # typed findings, they never format and they never decide the exit status.
  class CaseContext
    attr_reader :case_id, :a, :b, :findings, :observations, :exclusions, :options

    def initialize(case_id:, baseline:, candidate:, options:)
      @case_id = case_id
      @a = baseline
      @b = candidate
      @options = options
      @findings = []
      @observations = []
      @exclusions = []
      @rollback_unlabelled = false
      @stream_cache = {}
    end

    def cross_path? = options[:cross_path]
    def rollback_unlabelled? = @rollback_unlabelled
    def note_rollback_unlabelled = (@rollback_unlabelled = true)

    def add(dimension, field, klass, rule, baseline: nil, candidate: nil, detail: nil, severity: "gate")
      disposition = options[:dispositions]&.for(case_id, field)
      if disposition
        klass = disposition["class"]
        severity = disposition["severity"] || "accepted"
        detail = { "disposition" => disposition, "original_detail" => detail }
      end
      @findings << Finding.new(case_id: case_id, dimension: dimension, field: field, klass: klass,
                               rule: rule, baseline: baseline, candidate: candidate,
                               detail: detail, severity: severity)
    end

    def equal!(dimension, field, va, vb, klass:, rule:, severity: "gate")
      return if va == vb

      # Structured values get a path-level difference list, because two 200-char
      # revision tokens that differ in their last character are indistinguishable
      # when rendered side by side. The comparison already happened over the full
      # values; this only makes the answer readable.
      detail = nil
      if (va.is_a?(Hash) && vb.is_a?(Hash)) || (va.is_a?(Array) && vb.is_a?(Array))
        diff = Diffs.json_diff(va, vb)
        detail = { "differences" => diff } unless diff.empty?
      end

      add(dimension, field, klass, rule,
          baseline: Finding.render(va), candidate: Finding.render(vb),
          detail: detail, severity: severity)
    end

    # Advisory, never a finding: recorded so a reader can see the number.
    def observe(dimension, field, value)
      @observations << { "dimension" => dimension, "field" => field, "value" => value }
    end

    def exclude!(field, reason)
      @exclusions << { "case_id" => case_id, "field" => field, "reason" => reason }
    end

    # Decoded, copy-root-normalized stream bytes, or nil when the capture was
    # truncated and therefore must not be used for equality.
    def stream_bytes(obs, which)
      @stream_cache[[obs.object_id, which]] ||= begin
        s = obs.dig("process", which)
        if s.nil? || s["truncated_at_bytes"]
          [nil]
        else
          raw = Base64.decode64(s["bytes_base64"].to_s)
          [Normalize.rewrite_copy_root_bytes(raw, options[:spellings])]
        end
      end
      @stream_cache[[obs.object_id, which]].first
    end
  end

  # Machine-readable index into porting/intentional-differences.md. A difference
  # recorded in that file but not here keeps getting re-reported; an entry here
  # with no section there is a difference-hiding machine. Both directions are
  # review failures — see porting/intentional-differences.md § The record.
  class Dispositions
    def self.load(path)
      return new([]) unless path && File.exist?(path)

      entries = File.readlines(path).map(&:strip)
                    .reject { |l| l.empty? || l.start_with?("#") }
                    .map { |l| JSON.parse(l) }
      entries.each do |e|
        %w[case_id field class record].each do |k|
          raise ArgumentError, "disposition missing #{k}: #{e.inspect}" unless e[k]
        end
        unless Finding::CLASSES.include?(e["class"])
          raise ArgumentError, "disposition has unknown class #{e["class"].inspect}"
        end
      end
      new(entries)
    end

    def initialize(entries) = @entries = entries
    def empty? = @entries.empty?
    def to_a = @entries

    def for(case_id, field)
      @entries.find { |e| e["case_id"] == case_id && e["field"] == field }
    end
  end

  # The comparator proper.
  class Comparator
    DIMENSIONS = [Dimensions::Cli, Dimensions::Files, Dimensions::Journal,
                  Dimensions::Revisions, Dimensions::Performance, Dimensions::Http].freeze

    REPORT_VERSION = 1

    def initialize(baseline:, candidate:, dispositions: Dispositions.new([]),
                   cross_path: false, only: nil)
      @baseline = baseline
      @candidate = candidate
      @dispositions = dispositions
      @cross_path = cross_path
      @only = only
    end

    def run
      cases = []
      ids = (@baseline.case_ids | @candidate.case_ids).sort
      ids &= Array(@only) if @only && !@only.empty?

      ids.each { |id| cases << compare_case(id) }

      report(cases)
    end

    private

    def compare_case(id)
      a_raw = @baseline.by_case[id]
      b_raw = @candidate.by_case[id]

      # Unpaired. The playbook's fifth class: the oracle does not cover what the
      # candidate produced, or the candidate did not produce what the oracle
      # covers. Either way the answer is a case, not a verdict.
      if a_raw.nil? || b_raw.nil?
        f = Finding.new(case_id: id, dimension: "cli", field: "case_id",
                        klass: Finding::MISSING_ORACLE_COVERAGE,
                        rule: "playbook § 6 — a case observed on only one side has no oracle to compare against",
                        baseline: a_raw ? "observed" : "absent",
                        candidate: b_raw ? "observed" : "absent",
                        detail: "unpaired case")
        return { "case_id" => id, "status" => "unpaired", "findings" => [f],
                 "observations" => [], "exclusions" => [], "rollback_unlabelled" => false }
      end

      a, spell_a = Normalize.observation(a_raw)
      b, spell_b = Normalize.observation(b_raw)
      spellings = (spell_a | spell_b).sort_by { |s| -s.length }

      ctx = CaseContext.new(case_id: id, baseline: a, candidate: b,
                            options: { cross_path: @cross_path, dispositions: @dispositions,
                                       spellings: spellings })

      # Copy roots that differ mean the two sides ran at different absolute
      # paths. The journal index's `org` field then differs by construction and
      # a byte comparison of it is meaningless. runners/README.md
      # § "The same-absolute-path requirement": a cross-path comparison "must
      # exclude journal.index and say so in its report; a silent exclusion is a
      # defect." So it is never silent and never automatic — it must be asked
      # for.
      if spell_a.first != spell_b.first
        if @cross_path
          ctx.exclude!("fixture.copy_root",
                       "baseline ran at #{spell_a.first.inspect}, candidate at #{spell_b.first.inspect}")
        else
          ctx.add("cli", "fixture.copy_root", Finding::HARNESS_ERROR,
                  "runners/README.md § The same-absolute-path requirement — both implementations must run " \
                  "against copies at the same absolute path, because the journal index records that path " \
                  "inside bytes the harness digests. Re-run with the same --work, or pass --cross-path to " \
                  "compare with journal.index excluded and the exclusion reported.",
                  baseline: spell_a.first, candidate: spell_b.first)
        end
      end

      env_mismatch = environment(ctx)
      DIMENSIONS.each { |d| d.compare(ctx) }

      # errors.md § "What is not compared at all": a run whose two sides
      # disagree in `environment` AND also elsewhere "must be re-run with the
      # environments matched before the difference is classified". Marking every
      # other finding rather than suppressing it: the finding is still real and
      # still fails the gate; what is withheld is the confident attribution.
      if env_mismatch
        ctx.findings.each { |f| f.requires_rerun = true unless f.field.start_with?("environment.") }
      end

      status = ctx.findings.any?(&:gate?) ? "mismatch" : "match"
      { "case_id" => id, "status" => status, "findings" => ctx.findings,
        "observations" => ctx.observations, "exclusions" => ctx.exclusions,
        "rollback_unlabelled" => ctx.rollback_unlabelled? }
    end

    # environment.* is recorded, never an assertion: "a conformance run whose
    # two sides disagree in environment and agree everywhere else is fine".
    # Classified nondeterminism ("add the pin, do not normalize the output") at
    # advisory severity, so it cannot fail the gate on its own.
    def environment(ctx)
      ea = ctx.a["environment"] || {}
      eb = ctx.b["environment"] || {}
      mismatch = false
      (ea.keys | eb.keys).sort.each do |k|
        next if ea[k] == eb[k]

        mismatch = true
        ctx.add("cli", "environment.#{k}", Finding::NONDETERMINISM,
                "errors.md § What is not compared at all — environment is recorded so a difference elsewhere " \
                "can be attributed to a tzdb release or a platform, never itself an assertion. " \
                "determinism.md § Not pinnable: recorded instead.",
                baseline: ea[k], candidate: eb[k], severity: "advisory")
      end
      mismatch
    end

    def report(cases)
      findings = cases.flat_map { |c| c["findings"] }
      gate = findings.select(&:gate?)

      by_class = Hash.new(0)
      findings.each { |f| by_class[f.klass] += 1 }
      by_dimension = Hash.new(0)
      gate.each { |f| by_dimension[f.dimension] += 1 }

      {
        "report_version" => REPORT_VERSION,
        "baseline" => @baseline.provenance,
        "candidate" => @candidate.provenance,
        "normalizations" => normalizations_applied,
        "summary" => {
          "cases" => cases.length,
          "matched" => cases.count { |c| c["status"] == "match" },
          "mismatched" => cases.count { |c| c["status"] == "mismatch" },
          "unpaired" => cases.count { |c| c["status"] == "unpaired" },
          "findings" => findings.length,
          "gate_findings" => gate.length,
          "by_class" => by_class,
          "gate_findings_by_dimension" => by_dimension,
          "requires_rerun" => findings.any?(&:requires_rerun),
          "rollback_unlabelled_cases" => cases.count { |c| c["rollback_unlabelled"] }
        },
        "exclusions" => cases.flat_map { |c| c["exclusions"] },
        "dispositions" => @dispositions.to_a,
        "cases" => cases.map do |c|
          {
            "case_id" => c["case_id"],
            "status" => c["status"],
            "findings" => c["findings"].map(&:to_h),
            "advisory_observations" => c["observations"]
          }
        end,
        "gate" => { "passed" => gate.empty?, "exit_status" => gate.empty? ? 0 : 1 }
      }
    end

    # Echoed into every report. A reader must be able to see what was hidden
    # without reading the source.
    def normalizations_applied
      [
        { "field" => "observation_id", "to" => Normalize::OBSERVATION_ID,
          "why_unobservable" => "minted by the harness after the invocation exited; never written to the " \
                                "store, the journal, stdout or stderr, and named by no command or setting" },
        { "field" => "fixture.copy_root, invocation.env[].value, invocation.pins[].value, " \
                     "process.stdout/stderr bytes",
          "to" => Normalize::COPY_ROOT,
          "why_unobservable" => "the prefix is the harness's choice of working directory, echoed back by the " \
                                "implementation; every path INSIDE the copy stays compared" },
        { "field" => "journal directory key in files.*[].path and journal.index.path",
          "to" => Normalize::JOURNAL_KEY,
          "why_unobservable" => "a private cache key under XDG_STATE_HOME: no command prints it, no setting " \
                                "names it. Applied to paths only, never to bytes." },
        { "field" => "metrics.*", "to" => "advisory-only (never gate)",
          "why_unobservable" => "performance is a separate gate with its own budgets; errors.md forbids " \
                                "metrics from failing OR passing a conformance case" }
      ]
    end
  end
end
