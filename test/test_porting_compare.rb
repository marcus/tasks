# frozen_string_literal: true

require_relative "test_helper"
require "json"
require "open3"
require "rbconfig"
require "base64"
require "digest"

require_relative "../porting/compare/lib/comparator"
require_relative "../porting/compare/lib/report"

# porting/compare diffs two observation sets and classifies every difference.
# It is the thing that decides whether the Go port is right, so the properties
# worth defending are the ones a broken comparator would quietly violate:
#
#   * it detects each of the five mismatch classes the Phase 1 gate names, ON
#     THE RIGHT FIELD and with the RIGHT CLASSIFICATION — "something differs" is
#     not a result anyone can act on;
#   * it stays SILENT where the spec says to stay silent (stdout JSON key
#     order), because an over-strict comparator is as useless as a blind one and
#     fails in a way that looks like diligence;
#   * it honours the deliberate asymmetry in porting/specs/errors.md — the same
#     reordering that is invisible on stdout is a hard failure in store bytes;
#   * every normalization it applies is one of the four in
#     porting/specs/determinism.md, and it normalizes PATHS without ever
#     rewriting BYTES.
#
# The seeded mismatches come from porting/compare/seed, which perturbs the
# committed Ruby baseline. Using the real baseline rather than hand-written
# fixtures matters: a hand-written observation can accidentally be shaped to fit
# the comparator, and then the test passes because both sides are wrong.
class TestPortingCompare < Minitest::Test
  ROOT = File.expand_path("..", __dir__)
  BASELINE = File.join(ROOT, "porting", "evidence", "phase1", "ruby")
  SEED = File.join(ROOT, "porting", "compare", "seed")
  COMPARE = File.join(ROOT, "porting", "compare", "compare")
  AUDIT = File.join(ROOT, "porting", "compare", "audit")

  # Seeding is a subprocess and the baseline is 27 files, so each seeded set is
  # built once for the whole class rather than per test.
  def self.seeded(name)
    @seeded ||= {}
    @seeded[name] ||= begin
      dir = Dir.mktmpdir("compare-seed-#{name}")
      at_exit { FileUtils.remove_entry(dir) if File.directory?(dir) }
      out, err, status = Open3.capture3(RbConfig.ruby, SEED, "--mismatch", name, BASELINE, dir)
      raise "seed #{name} failed: #{err}#{out}" unless status.success?

      dir
    end
  end

  def baseline_set = Conformance::ObservationSet.load(BASELINE)

  def report_for(candidate_dir, **kwargs)
    Conformance::Comparator.new(
      baseline: baseline_set,
      candidate: Conformance::ObservationSet.load(candidate_dir),
      **kwargs
    ).run
  end

  def gate_findings(report)
    report["cases"].flat_map { |c| c["findings"] }.select { |f| f["severity"] == "gate" }
  end

  # The one finding a seed is supposed to produce, located by field prefix.
  def finding_on(report, case_id, field_prefix)
    gate_findings(report).find { |f| f["case_id"] == case_id && f["field"].start_with?(field_prefix) }
  end

  # --- the baseline is a usable oracle at all -------------------------------

  def test_baseline_exists_and_is_self_consistent
    assert File.directory?(BASELINE), "no committed baseline at #{BASELINE}"
    set = baseline_set
    assert_equal 27, set.size, "the Phase 1 corpus is 27 cases"

    report = Conformance::Comparator.new(baseline: set, candidate: set).run
    assert_empty gate_findings(report),
                 "the baseline must match itself; if it does not, every seeded result below is noise"
    assert_equal 0, report.dig("gate", "exit_status")
  end

  def test_provenance_records_a_clean_implementation_tree
    prov = JSON.parse(File.read(File.join(ROOT, "porting", "evidence", "phase1", "provenance.json")))
    assert_match(/\A[0-9a-f]{40}\z/, prov.dig("repo", "commit"))
    assert prov.dig("repo", "implementation_clean"),
           "a baseline captured against uncommitted bin/ or lib/ changes cannot be reproduced"
    assert_equal 27, prov.dig("inputs", "case_count")
    %w[case_list_sha256 fixture_corpus_sha256 runner_sha256 probe_sha256 schema_sha256].each do |k|
      assert_match(/\A[0-9a-f]{64}\z/, prov.dig("inputs", k), "provenance is missing #{k}")
    end
  end

  # --- the five seeded mismatch classes -------------------------------------

  def test_detects_exit_status_collapse
    report = report_for(self.class.seeded("exit-status"))
    f = finding_on(report, "cli-undo-after-capture", "process.exit_status")
    refute_nil f, "a 1 -> 2 exit status change must be reported"
    assert_equal "go_defect", f["class"]
    assert_equal "cli", f["dimension"]
    assert_equal 1, f["baseline"]
    assert_equal 2, f["candidate"]
    assert_equal 1, report.dig("gate", "exit_status")
  end

  # Both sides are nonzero here, which is exactly the collapse errors.md warns
  # about: a comparator that treated "both failed" as agreement would pass.
  def test_exit_status_comparison_is_not_satisfied_by_both_nonzero
    a = JSON.parse(File.read(File.join(BASELINE, "cli-undo-after-capture.json")))
    assert_equal 1, a.dig("process", "exit_status")
    b = JSON.parse(File.read(File.join(self.class.seeded("exit-status"), "cli-undo-after-capture.json")))
    assert_equal 2, b.dig("process", "exit_status")
    assert b.dig("process", "exit_status").positive? && a.dig("process", "exit_status").positive?
  end

  def test_detects_omitted_json_key
    report = report_for(self.class.seeded("json-output"))
    f = finding_on(report, "cli-list-json-small-gtd", "process.stdout (json)")
    refute_nil f, "an omitted stdout JSON key must be reported"
    assert_equal "go_defect", f["class"]
    diff = f.dig("detail", "differences")
    assert diff.any? { |d| d["path"] == "$[0].tags" && d["reason"] == "key_only_in_baseline" },
           "the report must name the missing key and say which side dropped it: #{diff.inspect}"
  end

  # The negative control. errors.md: object key order inside a JSON payload
  # printed to stdout is NOT compared, because stdout JSON is consumed by
  # parsers. Reporting it would be over-strictness, which is the other way to
  # make the harness useless.
  def test_stdout_json_key_order_is_not_a_finding
    report = report_for(self.class.seeded("json-key-order"))
    assert_empty gate_findings(report),
                 "stdout JSON key order is presentation; reporting it makes every real finding harder to see"
    assert_equal 0, report.dig("gate", "exit_status")
  end

  def test_detects_reordered_keys_in_store_bytes
    report = report_for(self.class.seeded("store-bytes"))
    f = finding_on(report, "cli-capture-small-gtd", "files.after[tasks.jsonl].sha256")
    refute_nil f, "a reordered key inside the JSONL store must be reported"
    assert_equal "go_defect", f["class"]
    assert_equal "files", f["dimension"]
    assert_match(/byte for byte, INCLUDING key order/, f["rule"])
    assert_equal f.dig("detail", "line_diff", "baseline_lines"), f.dig("detail", "line_diff", "candidate_lines"),
                 "the store changed by reordering, not by adding or removing a record"
    line = f.dig("detail", "line_diff", "lines").first
    assert_equal JSON.parse(line["baseline"]), JSON.parse(line["candidate"]),
                 "the seeded defect must be a pure reordering: identical parsed data, different bytes"
  end

  # The asymmetry, asserted as one statement rather than two: the SAME kind of
  # reordering is silent on stdout and fatal in the store.
  def test_key_order_asymmetry_between_stdout_and_store
    stdout_report = report_for(self.class.seeded("json-key-order"))
    store_report = report_for(self.class.seeded("store-bytes"))
    assert_empty gate_findings(stdout_report), "stdout JSON: key order is not compared"
    refute_empty gate_findings(store_report), "JSONL store: key order is a byte contract"
  end

  def test_detects_one_character_revision_token_change
    report = report_for(self.class.seeded("revision-token"))
    f = finding_on(report, "cli-list-stale-revision", "revisions.resources[")
    refute_nil f, "a one-character revision token change must be reported"
    assert_equal "go_defect", f["class"]
    assert_equal "revisions", f["dimension"]
    diff = f.dig("detail", "differences")
    assert diff.any? { |d| d["path"] == "$.revision" },
           "the report must name the token field, not just say the resource differs"
    base, cand = diff.first.values_at("baseline", "candidate")
    assert_equal base.length, cand.length
    assert_equal 1, base.chars.zip(cand.chars).count { |x, y| x != y },
                 "the seed must be an off-by-one token, not a wholesale replacement"
  end

  # Rollback. The honest result: it IS detected, but only as a stderr byte
  # difference, and it is NOT labelled as a rollback, because files.rolled_back
  # is null on both sides. Both halves are asserted so a future change that
  # silently drops either one fails here.
  def test_detects_rollback_diagnostic_only_through_stderr_bytes
    report = report_for(self.class.seeded("rollback"))
    f = finding_on(report, "cli-capture-torn-file", "process.stderr")
    refute_nil f, "a wrote-and-reverted vs never-wrote diagnostic difference must be reported"
    assert_equal "go_defect", f["class"]
    assert_equal "cli", f["dimension"]

    assert_nil finding_on(report, "cli-capture-torn-file", "files.rolled_back"),
               "files.rolled_back is null on both sides today, so it cannot be the thing that detected this"

    case_row = report["cases"].find { |c| c["case_id"] == "cli-capture-torn-file" }
    assert_equal ["cli"], case_row["findings"].map { |x| x["dimension"] }.uniq,
                 "nothing on the filesystem distinguishes wrote-and-reverted from never-wrote: " \
                 "exit status, deltas and store bytes are all identical"
    assert report.dig("summary", "rollback_unlabelled_cases").positive?,
           "the report must say out loud that rollback is unlabelled, not leave it to be discovered"
  end

  # --- classification, not just detection -----------------------------------

  def test_unpaired_case_is_missing_oracle_coverage
    dir = Dir.mktmpdir("compare-unpaired")
    FileUtils.cp(Dir[File.join(BASELINE, "*.json")].sort.first(3), dir)
    report = report_for(dir)
    unpaired = report["cases"].select { |c| c["status"] == "unpaired" }
    assert_equal 24, unpaired.length
    assert_equal ["missing_oracle_coverage"], unpaired.flat_map { |c| c["findings"] }.map { |f| f["class"] }.uniq
  ensure
    FileUtils.remove_entry(dir) if dir && File.directory?(dir)
  end

  def test_environment_difference_is_advisory_and_forces_a_rerun_note
    dir = mutate_case("cli-list-small-gtd") { |obs| obs["environment"]["tzdb_version"] = "tzdata-2099a" }
    report = report_for(dir)
    env = report["cases"].flat_map { |c| c["findings"] }.find { |f| f["field"] == "environment.tzdb_version" }
    refute_nil env
    assert_equal "nondeterminism", env["class"]
    assert_equal "advisory", env["severity"], "environment can never fail a case on its own"
    assert_equal 0, report.dig("gate", "exit_status")
  ensure
    FileUtils.remove_entry(dir) if dir && File.directory?(dir)
  end

  # errors.md: a run whose two sides disagree in environment AND elsewhere must
  # be re-run with the environments matched before the difference is classified.
  # The finding still fails the gate; what is withheld is the attribution.
  def test_environment_difference_marks_other_findings_for_rerun
    dir = mutate_case("cli-list-small-gtd") do |obs|
      obs["environment"]["platform"] = "x86_64-linux"
      obs["process"]["exit_status"] = 1
    end
    report = report_for(dir)
    f = finding_on(report, "cli-list-small-gtd", "process.exit_status")
    refute_nil f
    assert f["requires_rerun"], "an exit-status difference alongside an environment difference is not yet attributable"
    assert report.dig("summary", "requires_rerun")
    assert_equal 1, report.dig("gate", "exit_status"), "it is still a gate failure"
  ensure
    FileUtils.remove_entry(dir) if dir && File.directory?(dir)
  end

  def test_metrics_can_neither_fail_nor_pass_a_case
    dir = mutate_case("cli-list-small-gtd") do |obs|
      obs["metrics"]["wall_ms"] = 999_999
      obs["metrics"]["bytes_written"] = 123_456
    end
    report = report_for(dir)
    assert_empty gate_findings(report), "metrics must never be able to fail a conformance case"
    row = report["cases"].find { |c| c["case_id"] == "cli-list-small-gtd" }["advisory_observations"]
    refute_empty row, "…and must still be recorded, so a reader can see the number"
  ensure
    FileUtils.remove_entry(dir) if dir && File.directory?(dir)
  end

  def test_a_recorded_disposition_downgrades_a_finding_but_never_hides_it
    dispositions = Conformance::Dispositions.new(
      [{ "case_id" => "cli-undo-after-capture", "field" => "process.exit_status",
         "class" => "intentional_difference", "severity" => "accepted",
         "record" => "porting/intentional-differences.md#example" }]
    )
    report = Conformance::Comparator.new(
      baseline: baseline_set,
      candidate: Conformance::ObservationSet.load(self.class.seeded("exit-status")),
      dispositions: dispositions
    ).run

    f = report["cases"].flat_map { |c| c["findings"] }.find { |x| x["field"] == "process.exit_status" }
    refute_nil f, "an accepted difference is still reported — it is a known exception, not a silenced class"
    assert_equal "intentional_difference", f["class"]
    assert_equal "accepted", f["severity"]
    assert_equal 0, report.dig("gate", "exit_status")
  end

  def test_disposition_without_a_record_reference_is_refused
    path = File.join(Dir.mktmpdir("dispositions"), "d.jsonl")
    File.write(path, "#{JSON.generate({ "case_id" => "x", "field" => "y", "class" => "go_defect" })}\n")
    assert_raises(ArgumentError) { Conformance::Dispositions.load(path) }
  end

  # --- normalization: exactly four, and paths not bytes ----------------------

  def test_normalizes_the_four_documented_values_and_says_so_in_the_report
    report = report_for(BASELINE)
    fields = report["normalizations"].map { |n| n["field"] }
    assert_equal 4, fields.length, "determinism.md lists four normalizations; this must not grow silently"
    report["normalizations"].each do |n|
      refute_empty n["why_unobservable"].to_s,
                   "every normalization must carry the reason a user cannot observe it"
    end
  end

  def test_journal_key_is_normalized_in_paths_but_never_inside_bytes
    obs = JSON.parse(File.read(File.join(BASELINE, "cli-capture-small-gtd.json")))
    normalized, = Conformance::Normalize.observation(obs)

    index_path = normalized.dig("journal", "index", "path")
    assert_includes index_path, Conformance::Normalize::JOURNAL_KEY
    refute_match(/journal\/[0-9a-f]{16}\//, index_path)

    # The bytes are untouched: rewriting them before digesting is exactly the
    # move determinism.md refuses.
    assert_equal obs.dig("journal", "index", "content_base64"),
                 normalized.dig("journal", "index", "content_base64")
    assert_equal obs.dig("journal", "index", "sha256"), normalized.dig("journal", "index", "sha256")
  end

  def test_copy_root_is_normalized_in_streams_but_not_in_file_contents
    obs = JSON.parse(File.read(File.join(BASELINE, "cli-capture-small-gtd.json")))
    normalized, spellings = Conformance::Normalize.observation(obs)

    refute_empty spellings
    assert_equal Conformance::Normalize::COPY_ROOT, normalized.dig("fixture", "copy_root")

    index_bytes = Base64.decode64(normalized.dig("journal", "index", "content_base64"))
    assert_includes index_bytes, "/tasks-conformance/cli-capture-small-gtd/tasks.jsonl",
                    "the journal index's `org` path stays inside the compared bytes; the cause is removed " \
                    "by running both sides at the same path, not by rewriting the bytes"
  end

  # --- the same-absolute-path requirement ------------------------------------

  def test_differing_copy_roots_are_a_harness_error_not_a_silent_exclusion
    dir = mutate_case("cli-capture-small-gtd") do |obs|
      obs["fixture"]["copy_root"] = "/tmp/somewhere-else/cli-capture-small-gtd"
    end
    report = report_for(dir)
    f = finding_on(report, "cli-capture-small-gtd", "fixture.copy_root")
    refute_nil f, "a cross-path comparison must be refused loudly, not quietly de-scoped"
    assert_equal "harness_error", f["class"]
    assert_empty report["exclusions"], "nothing is excluded unless --cross-path was asked for"
  ensure
    FileUtils.remove_entry(dir) if dir && File.directory?(dir)
  end

  def test_cross_path_excludes_the_journal_index_and_records_the_exclusion
    dir = mutate_case("cli-capture-small-gtd") do |obs|
      obs["fixture"]["copy_root"] = "/tmp/somewhere-else/cli-capture-small-gtd"
    end
    report = report_for(dir, cross_path: true)
    excluded = report["exclusions"].map { |e| e["field"] }
    assert_includes excluded, "journal.index"
    report["exclusions"].each { |e| refute_empty e["reason"].to_s }
  ensure
    FileUtils.remove_entry(dir) if dir && File.directory?(dir)
  end

  # --- coverage audit --------------------------------------------------------

  # The audit is what keeps a green comparison honest: it names the classes the
  # corpus cannot exercise, so a PASS on those classes is never mistaken for
  # proof.
  def test_audit_reports_the_rollback_and_exit_status_two_gaps
    out, _err, status = Open3.capture3(RbConfig.ruby, AUDIT, "--json", BASELINE)
    audit = JSON.parse(out)
    refute status.success?, "an incomplete corpus must exit nonzero"
    assert_includes audit["gaps"], "rollback"
    assert_includes audit["gaps"], "exit_status"

    rollback = audit["classes"].find { |c| c["class"] == "rollback" }
    refute rollback["exercised"], "no Phase 1 case produces a labelled rollback"

    exits = audit["classes"].find { |c| c["class"] == "exit_status" }
    assert_equal [0, 1], exits["distinct_values"], "no case exits 2, so the 1-vs-2 collapse is untested by the corpus"
  end

  # --- the executable is usable as a gate ------------------------------------

  def test_compare_executable_exit_status_is_the_gate
    _out, _err, ok = Open3.capture3(RbConfig.ruby, COMPARE, "--json", BASELINE, BASELINE)
    assert_equal 0, ok.exitstatus

    _out, _err, bad = Open3.capture3(RbConfig.ruby, COMPARE, "--json", BASELINE, self.class.seeded("exit-status"))
    assert_equal 1, bad.exitstatus

    _out, _err, usage = Open3.capture3(RbConfig.ruby, COMPARE, BASELINE)
    assert_equal 2, usage.exitstatus
  end

  def test_text_report_never_changes_the_verdict
    report = report_for(self.class.seeded("store-bytes"))
    text = Conformance::Report.text(report)
    assert_includes text, "GATE FAIL"
    assert_includes text, "cli-capture-small-gtd"
    assert_equal 1, report.dig("gate", "exit_status")
  end

  private

  # A copy of the baseline with one case edited. Used for the differences the
  # seed catalog deliberately does not carry, because they are properties of the
  # comparator rather than modelled port defects.
  def mutate_case(case_id)
    dir = Dir.mktmpdir("compare-mutate")
    Dir[File.join(BASELINE, "*.json")].each { |f| FileUtils.cp(f, dir) }
    path = File.join(dir, "#{case_id}.json")
    obs = JSON.parse(File.read(path))
    yield obs
    File.write(path, "#{JSON.pretty_generate(obs)}\n")
    dir
  end
end
