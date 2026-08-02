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
  CASES = File.join(ROOT, "porting", "runners", "cases", "phase1.jsonl")

  # Derived from the case list rather than written down, because a magic number
  # here turns "someone added a conformance case" — the thing this whole corpus
  # wants more of — into four unrelated test failures. What is worth asserting
  # is that the baseline covers the case list exactly, and that is what the
  # comparison below does.
  CASE_COUNT = File.readlines(CASES).map(&:strip)
                   .count { |l| !l.empty? && !l.start_with?("#") }

  # Seeding is a subprocess and the baseline is one file per case, so each
  # seeded set is built once for the whole class rather than per test.
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

  # Implementation paths dirty in the worktree right now, spelled exactly as
  # porting/evidence/capture spells them in provenance.
  def implementation_dirty_now
    `git -C #{ROOT.inspect} status --porcelain 2>/dev/null`
      .lines.map { |l| l[3..].to_s.strip }.reject(&:empty?)
      .select { |p| p.start_with?("bin/", "lib/") }
  end

  # The one finding a seed is supposed to produce, located by field prefix.
  def finding_on(report, case_id, field_prefix)
    gate_findings(report).find { |f| f["case_id"] == case_id && f["field"].start_with?(field_prefix) }
  end

  # --- the baseline is a usable oracle at all -------------------------------

  def test_baseline_exists_and_is_self_consistent
    assert File.directory?(BASELINE), "no committed baseline at #{BASELINE}"
    set = baseline_set
    assert_equal CASE_COUNT, set.size,
                 "the baseline must carry one observation per case in #{File.basename(CASES)}"

    report = Conformance::Comparator.new(baseline: set, candidate: set).run
    assert_empty gate_findings(report),
                 "the baseline must match itself; if it does not, every seeded result below is noise"
    assert_equal 0, report.dig("gate", "exit_status")
  end

  def test_provenance_records_a_clean_implementation_tree
    prov = JSON.parse(File.read(File.join(ROOT, "porting", "evidence", "phase1", "provenance.json")))
    assert_match(/\A[0-9a-f]{40}\z/, prov.dig("repo", "commit"))

    # A baseline taken against uncommitted bin/ or lib/ changes cannot be
    # reproduced from its commit, so a COMMITTED baseline must be a clean one.
    # Mid-flight that is not yet true and cannot be: a change to the CLI has to
    # be captured before it can be committed. So the assertion follows the
    # worktree — in a clean checkout (which is what gets committed, and what CI
    # runs) the baseline must be clean; while the implementation is dirty, the
    # provenance must at least say so out loud rather than claim otherwise.
    if implementation_dirty_now.empty?
      assert prov.dig("repo", "implementation_clean"),
             "the committed baseline was captured against a dirty bin/ or lib/ tree. " \
             "Re-run porting/evidence/capture now that the tree is clean and commit the result."
    else
      refute prov.dig("repo", "implementation_clean"),
             "bin/ or lib/ is dirty, so the baseline cannot honestly claim a clean implementation tree"
      assert_equal implementation_dirty_now.sort, Array(prov.dig("repo", "implementation_paths_dirty")).sort,
                   "provenance must name exactly the implementation paths that were dirty at capture; " \
                   "if these differ, the baseline predates the current edits — re-run porting/evidence/capture"
    end
    assert_equal CASE_COUNT, prov.dig("inputs", "case_count")
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

  # Rollback, the case the harness exists for. The seeded candidate takes the
  # other internal path — never wrote where Ruby wrote and reverted — and prints
  # BYTE-IDENTICAL stderr, which is what used to make it undetectable. Exit
  # status, deltas and store bytes are identical too, so if the label does not
  # catch it, nothing does.
  def test_detects_a_rollback_that_differs_in_nothing_but_the_label
    report = report_for(self.class.seeded("rollback"))
    case_id = "cli-capture-readonly-rollback"

    f = finding_on(report, case_id, "files.rolled_back")
    refute_nil f, "a wrote-and-reverted vs never-wrote difference must be reported on the label"
    assert_equal "go_defect", f["class"]
    assert_equal "files", f["dimension"]
    assert_equal true, f["baseline"]
    assert_equal false, f["candidate"]

    assert_nil finding_on(report, case_id, "process.stderr"),
               "the seed leaves stderr untouched on purpose: the detection must come from the label"
    assert_nil finding_on(report, case_id, "process.exit_status")
    assert_empty gate_findings(report).select { |x| x["dimension"] == "files" && x["field"] != "files.rolled_back" },
                 "nothing on the filesystem distinguishes wrote-and-reverted from never-wrote"
  end

  # The baseline labels exactly one case, and says nothing about the rest rather
  # than guessing false — a read reports no rollback flag at all.
  def test_the_baseline_labels_the_rollback_case_and_only_that_case
    labelled = baseline_set.by_case.values.reject { |o| o.dig("files", "rolled_back").nil? }
    assert_equal ["cli-capture-readonly-rollback"], labelled.map { |o| o["case_id"] }
    assert_equal true, labelled.first.dig("files", "rolled_back")
    assert_empty labelled.first.dig("files", "deltas"),
                 "the point of the label: the write was reverted, so the filesystem shows nothing"

    report = Conformance::Comparator.new(baseline: baseline_set, candidate: baseline_set).run
    assert_equal CASE_COUNT - 1, report.dig("summary", "rollback_unlabelled_cases"),
                 "the report must count the cases carrying no label, not leave it to be discovered"
  end

  # true and false are two Ruby classes and one JSON type. Reporting the flip as
  # a type change would describe a real difference in a way that reads as a
  # harness bug — and the rollback label is a boolean.
  def test_a_flipped_boolean_is_a_value_difference_not_a_type_difference
    diff = Conformance::Diffs.json_diff({ "rolled_back" => true }, { "rolled_back" => false })
    assert_equal 1, diff.length
    assert_equal "value", diff.first["reason"]
    assert_equal [true, false], [diff.first["baseline"], diff.first["candidate"]]
  end

  # --- classification, not just detection -----------------------------------

  def test_unpaired_case_is_missing_oracle_coverage
    dir = Dir.mktmpdir("compare-unpaired")
    FileUtils.cp(Dir[File.join(BASELINE, "*.json")].sort.first(3), dir)
    report = report_for(dir)
    unpaired = report["cases"].select { |c| c["status"] == "unpaired" }
    assert_equal CASE_COUNT - 3, unpaired.length
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
  def test_audit_reports_every_gate_class_as_exercised
    out, _err, status = Open3.capture3(RbConfig.ruby, AUDIT, "--json", BASELINE)
    audit = JSON.parse(out)
    assert status.success?, "a complete corpus must exit 0: #{out}"
    assert_empty audit["gaps"]

    rollback = audit["classes"].find { |c| c["class"] == "rollback" }
    assert rollback["exercised"]
    assert_equal ["cli-capture-readonly-rollback"], rollback["cases"],
                 "one narrow case, and the audit must keep naming which one"

    exits = audit["classes"].find { |c| c["class"] == "exit_status" }
    assert_equal [0, 1, 2], exits["distinct_values"],
                 "the corpus must present the 1-vs-2 distinction, not just 'both nonzero'"
  end

  # The audit's whole job is to refuse to be satisfied by a corpus that never
  # asks the question, so removing the cases that ask it must reopen the gap.
  def test_audit_reopens_the_gaps_when_the_cases_that_close_them_are_removed
    dir = Dir.mktmpdir("audit-narrowed")
    Dir[File.join(BASELINE, "*.json")].each do |f|
      next if File.basename(f, ".json") =~ /\A(cli-capture-readonly-rollback|cli-done-(no-match|ambiguous)-ref)\z/

      FileUtils.cp(f, dir)
    end
    out, _err, status = Open3.capture3(RbConfig.ruby, AUDIT, "--json", dir)
    audit = JSON.parse(out)
    refute status.success?, "an incomplete corpus must exit nonzero"
    assert_equal %w[exit_status rollback].sort, audit["gaps"].sort
  ensure
    FileUtils.remove_entry(dir) if dir && File.directory?(dir)
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
