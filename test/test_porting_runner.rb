# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "rbconfig"
require "json"
require "digest"

# The runner and the comparator must share ONE copy-root rewrite, so the test
# that proves `sha256_normalized` is that rewrite has to call it directly.
require_relative "../porting/compare/lib/normalize"

# porting/runners/ruby/run replays a scripted case list against copies of the
# fixture corpus and emits one observation per case. The properties worth
# defending here are the ones the whole conformance harness rests on:
#
#   * it never writes to a fixture, and never anywhere near a live store;
#   * an observation is schema-shaped, and says what actually happened —
#     including for a read, where "nothing changed" must be an assertion rather
#     than an omission;
#   * two runs are byte-identical under pins, because a harness that drifts
#     between runs cannot attribute a difference to the port.
#
# The full case list is exercised by porting/runners/cases/phase1.jsonl; these
# tests run the smallest lists that still prove each property, because every
# case is a real subprocess against a real copy.
class TestPortingRunner < Minitest::Test
  RUNNER = File.expand_path("../porting/runners/ruby/run", __dir__)
  PROBE = File.expand_path("../porting/runners/ruby/probe", __dir__)
  FIXTURES = File.expand_path("../porting/fixtures", __dir__)
  CASES = File.expand_path("../porting/runners/cases/phase1.jsonl", __dir__)
  SCHEMA = File.expand_path("../porting/specs/observations.schema.json", __dir__)

  READ_CASE = { "case_id" => "t-read", "fixture" => "valid/single-task", "argv" => ["list"] }.freeze
  MUTATION_CASE = { "case_id" => "t-mutate", "fixture" => "valid/small-gtd",
                    "argv" => ["capture", "runner test", "--json"] }.freeze

  def setup
    @tmp = Dir.mktmpdir("porting-runner-test")
  end

  def teardown
    FileUtils.remove_entry(@tmp) if @tmp && File.directory?(@tmp)
  end

  # --- helpers -------------------------------------------------------------

  def write_cases(*entries, name: "cases.jsonl")
    path = File.join(@tmp, name)
    File.write(path, entries.map { |e| JSON.generate(e) }.join("\n") + "\n")
    path
  end

  def run_runner(*args)
    Open3.capture3(RbConfig.ruby, RUNNER, *args)
  end

  # One run of the given cases into a fresh --out directory, returning the
  # parsed observations keyed by case id.
  def observe(*entries, extra: [], work: File.join(@tmp, "work"), out: nil)
    out ||= File.join(@tmp, "out-#{entries.map { |e| e["case_id"] }.join("-")}")
    list = write_cases(*entries, name: "cases-#{File.basename(out)}.jsonl")
    stdout, stderr, status = run_runner("--out", out, "--work", work, "--quiet", *extra, list)
    assert status.success?, "runner failed (#{status.exitstatus}): #{stderr}#{stdout}"
    entries.to_h do |entry|
      [entry["case_id"], JSON.parse(File.read(File.join(out, "#{entry["case_id"]}.json")))]
    end
  end

  def tree_digest(dir)
    files = Dir.glob("**/*", File::FNM_DOTMATCH, base: dir).reject { |p| p.end_with?(".", "..") }
    files.select { |p| File.file?(File.join(dir, p)) }.sort.map do |rel|
      "#{rel}:#{Digest::SHA256.file(File.join(dir, rel)).hexdigest}"
    end.join("\n")
  end

  # --- the case list -------------------------------------------------------

  def test_case_list_skips_comments_and_blank_lines
    path = File.join(@tmp, "commented.jsonl")
    File.write(path, ["# a comment", "", JSON.generate(READ_CASE), "   ", "# another"].join("\n"))
    stdout, stderr, status = run_runner("--dry-run", path)
    assert status.success?, stderr
    plan = JSON.parse(stdout)
    assert_equal ["t-read"], plan.map { |row| row["case_id"] }
  end

  def test_case_list_rejects_a_malformed_list
    {
      "unknown key" => READ_CASE.merge("argvv" => []),
      "missing case_id" => READ_CASE.reject { |k, _| k == "case_id" },
      "unknown fixture class" => READ_CASE.merge("fixture" => "nonesuch/thing"),
      "non-string argv" => READ_CASE.merge("argv" => [1]),
      "runner-owned env" => READ_CASE.merge("env" => { "TASKS_DIR" => "/elsewhere" }),
      "http surface" => READ_CASE.merge("surface" => "http"),
    }.each do |what, entry|
      _, stderr, status = run_runner("--dry-run", write_cases(entry, name: "bad.jsonl"))
      assert_equal 2, status.exitstatus, "#{what} was accepted"
      refute_empty stderr
    end
  end

  def test_case_list_rejects_duplicate_case_ids
    _, stderr, status = run_runner("--dry-run", write_cases(READ_CASE, READ_CASE, name: "dup.jsonl"))
    assert_equal 2, status.exitstatus
    assert_includes stderr, "duplicate case_id"
  end

  def test_shipped_case_list_covers_every_fixture_class
    stdout, stderr, status = run_runner("--dry-run", CASES)
    assert status.success?, stderr
    plan = JSON.parse(stdout)
    classes = plan.map { |row| row["fixture"].split("/").first }.uniq.sort
    assert_equal %w[adversarial compat malformed valid], classes
    plan.each do |row|
      assert File.directory?(File.join(FIXTURES, row["fixture"])), "missing fixture #{row["fixture"]}"
    end
  end

  # --- isolation -----------------------------------------------------------

  def test_a_mutating_case_leaves_the_fixture_untouched
    fixture = File.join(FIXTURES, "valid", "small-gtd")
    before = tree_digest(fixture)
    observed = observe(MUTATION_CASE)
    assert observed["t-mutate"]["files"]["mutated"], "the case did not mutate anything"
    assert_equal before, tree_digest(fixture), "the runner wrote to the fixture corpus"
  end

  def test_the_copy_is_removed_unless_kept
    work = File.join(@tmp, "work-clean")
    observe(READ_CASE, work: work)
    refute File.exist?(File.join(work, "t-read")), "the fixture copy outlived the case"

    kept = File.join(@tmp, "work-kept")
    observe(READ_CASE, work: kept, extra: ["--keep"], out: File.join(@tmp, "out-kept"))
    assert File.file?(File.join(kept, "t-read", "tasks.jsonl")), "--keep did not keep the copy"
  end

  def test_it_refuses_to_run_inside_the_live_store
    live = JSON.parse(Open3.capture2(RbConfig.ruby,
                                     File.expand_path("../bin/tasks", __dir__),
                                     "config", "--json").first)
    _, stderr, status = run_runner("--work", File.dirname(live.fetch("org")),
                                   "--out", File.join(@tmp, "never"), write_cases(READ_CASE))
    assert_equal 2, status.exitstatus
    assert_includes stderr, "overlaps the live store"
  end

  # --- the schema ----------------------------------------------------------

  # A full JSON Schema validation is the harness's own gate (a real validator
  # against porting/specs/observations.schema.json, outside this stdlib-only
  # suite). What is checked here is the part a regression would break first and
  # silently: the top-level key set, which the schema closes with
  # additionalProperties: false.
  def test_observations_carry_exactly_the_schema_top_level_keys
    schema = JSON.parse(File.read(SCHEMA))
    refute schema["additionalProperties"], "the schema stopped being closed at the root"
    observed = observe(READ_CASE, MUTATION_CASE)
    observed.each_value do |observation|
      missing = schema["required"] - observation.keys
      assert_empty missing, "observation is missing required key(s)"
      extra = observation.keys - schema["properties"].keys
      assert_empty extra, "observation carries key(s) the schema forbids"
    end
  end

  # --- what an observation says --------------------------------------------

  def test_a_read_asserts_that_it_changed_nothing
    observation = observe(READ_CASE).fetch("t-read")
    assert_equal 2, observation["schema_version"]
    assert_equal "t-read", observation["case_id"]
    assert_equal "ruby", observation.dig("implementation", "name")
    assert_equal "valid", observation.dig("fixture", "class")
    assert_equal 0, observation.dig("process", "exit_status")
    refute observation.dig("files", "mutated"), "a read reported a mutation"

    # A read is not delta-free: taking the store lock creates the sidecar, and
    # that creation is a real observable effect the port must reproduce.
    assert_equal [".tasks.jsonl.lock"], observation.dig("files", "deltas").map { |d| d["path"] }
    store = observation.dig("files", "after").find { |f| f["role"] == "store" }
    refute_nil store, "the store was not observed"
    assert_equal observation.dig("files", "before").find { |f| f["role"] == "store" }["sha256"],
                 store["sha256"]
    refute observation.dig("journal", "present"), "a read created a journal"
    assert_match(/\A\.state\/tasks\/journal\/[0-9a-f]{16}\/index\.json\z/,
                 observation.dig("journal", "index", "path"),
                 "an absent journal must still report where it would have been")
  end

  def test_a_mutation_reports_bytes_journal_and_touched_ids
    observation = observe(MUTATION_CASE).fetch("t-mutate")
    assert observation.dig("files", "mutated")
    deltas = observation.dig("files", "deltas").to_h { |d| [d["path"], d["kind"]] }
    assert_equal "modified", deltas["tasks.jsonl"]
    assert_equal "created", deltas[".tasks.jsonl.lock"]

    journal = observation["journal"]
    assert journal["present"]
    assert_equal 1, journal["version"]
    assert_equal 1, journal["cursor"]
    assert_equal 2, journal["states"].size
    assert_nil journal["states"].first["label"], "the baseline state must carry no label"
    assert_includes journal["states"].last["label"], "runner test"
    assert_equal 2, journal["blob_count"]

    # The id came from TASKS_PIN_IDS, and the CLI reported it itself.
    assert_equal ["bbbb0001"], observation.dig("revisions", "touched_ids")
    assert_operator observation.dig("metrics", "bytes_written"), :>, 0
  end

  # --- directories are entries too -----------------------------------------

  # Before `kind` existed, an effect that only created, removed or chmod'd a
  # directory produced an observation byte-identical to one that did nothing:
  # the walk recorded files only. These three tests pin the parts of the fix
  # that a later "tidy the walk" change would undo first.
  def test_directories_are_recorded_whether_or_not_they_changed
    observation = observe(READ_CASE).fetch("t-read")
    before = observation.dig("files", "before").to_h { |s| [s["path"], s] }

    # `.config/tasks` is created by the copy protocol and touched by nothing.
    # Recording only CHANGED directories would drop it, and then "no row" would
    # mean both "unchanged" and "this harness does not look at directories".
    row = before.fetch(".config/tasks")
    assert_equal "directory", row["kind"]
    assert_equal "0755", row["mode"]
    %w[sha256 size_bytes content_base64 line_count symlink_target].each do |field|
      assert_nil row[field], "a directory must not carry #{field}"
    end

    store = before.fetch("tasks.jsonl")
    assert_equal "file", store["kind"]
    refute_nil store["sha256"]
  end

  # The copy root is the one directory whose mode is a case-declared INPUT, and
  # before this it appeared nowhere in the observation: a case whose whole
  # subject is an unwritable store directory could not show what it made
  # unwritable.
  def test_the_copy_root_is_recorded_and_carries_the_declared_mode
    observation = observe(ROLLBACK_CASE.merge("case_id" => "t-root-mode")).fetch("t-root-mode")
    %w[before after].each do |side|
      root = observation.dig("files", side).find { |s| s["path"] == "." }
      refute_nil root, "the copy root is not recorded in files.#{side}"
      assert_equal "directory", root["kind"]
      assert_equal "0555", root["mode"],
                   "the case's copy_root_mode must be visible in files.#{side}; the runner " \
                   "restores a writable mode only AFTER the last observation"
    end
  end

  # A directory has no bytes, so mode is the whole of its observable state — and
  # a delta list that compared only digests reported a chmod as nothing having
  # happened. Driven directly against the walk rather than through a case,
  # because no product verb chmods a directory today; the point is that the rule
  # exists before something does.
  def test_a_mode_only_change_to_a_directory_is_a_modified_delta
    sandbox = File.join(@tmp, "delta-sandbox")
    FileUtils.mkdir_p(File.join(sandbox, "sub"))
    File.write(File.join(sandbox, "sub", "f"), "x")

    # `load`, not `require`: the runner is an extensionless executable, and its
    # entry point is guarded by $PROGRAM_NAME so loading it defines the module
    # without running a case.
    script = <<~RUBY
      require "json"
      load #{RUNNER.dump}
      root = #{sandbox.dump}
      before = PortingRunner::Tree.walk(root)
      File.chmod(0o700, File.join(root, "sub"))
      after = PortingRunner::Tree.walk(root)
      runner = PortingRunner::Runner.allocate
      puts JSON.generate(runner.send(:deltas_for, before, after))
    RUBY
    out, err, status = Open3.capture3(RbConfig.ruby, "-e", script)
    assert status.success?, err
    deltas = JSON.parse(out)
    assert_equal [{ "path" => "sub", "kind" => "modified",
                    "before_sha256" => nil, "after_sha256" => nil }], deltas,
                 "a chmod of a directory must be a delta; it is the only modification a " \
                 "directory can express"
  end

  # --- streams --------------------------------------------------------------

  # `stream.sha256` digests raw bytes, so a stream past the 256 KiB embed limit
  # that names the copy root could never compare equal across two copy roots —
  # and past that limit only the digest survives, so no downstream normalization
  # can fix it. sha256_normalized is that digest taken after the SAME copy-root
  # rewrite the comparator applies to short streams.
  def test_a_stream_naming_the_copy_root_carries_a_different_normalized_digest
    # `config --json` echoes the resolved absolute store paths, so its stdout
    # names the copy root; its stderr is empty and names nothing. One case
    # therefore exercises both directions.
    observation = observe(READ_CASE.merge("case_id" => "t-paths",
                                          "argv" => %w[config --json])).fetch("t-paths")
    spellings = Conformance::Normalize.copy_root_spellings(observation.dig("fixture", "copy_root"))

    stdout = observation.dig("process", "stdout")
    assert spellings.any? { |root| stdout["bytes_base64"].unpack1("m").include?(root) },
           "expected stdout to name the copy root"
    refute_equal stdout["sha256"], stdout["sha256_normalized"],
                 "the normalized digest must differ once the copy root is rewritten out; " \
                 "equal digests mean the rewrite was not applied before hashing"

    stderr = observation.dig("process", "stderr")
    refute spellings.any? { |root| stderr["bytes_base64"].unpack1("m").include?(root) }
    assert_equal stderr["sha256"], stderr["sha256_normalized"],
                 "a stream that does not name the copy root normalizes to itself"
  end

  # The runner must reuse the comparator's rewrite rather than carry a
  # lookalike: two implementations that drift silently reclassify a real
  # difference as a copy-root artifact, or the reverse.
  def test_the_normalized_digest_is_the_comparators_own_rewrite
    observation = observe(READ_CASE.merge("case_id" => "t-normalize")).fetch("t-normalize")
    spellings = Conformance::Normalize.copy_root_spellings(observation.dig("fixture", "copy_root"))
    %w[stdout stderr].each do |which|
      stream = observation.dig("process", which)
      bytes = stream["bytes_base64"].unpack1("m")
      expected = Digest::SHA256.hexdigest(
        Conformance::Normalize.rewrite_copy_root_bytes(bytes, spellings)
      )
      assert_equal expected, stream["sha256_normalized"], "#{which} normalized digest"
    end
  end

  # --- colour ---------------------------------------------------------------

  # Colour was an unpinned, unrecorded, unobserved input. It is now pinned (to
  # unset, which `unsetenv_others` makes real) and recorded — and still
  # unexercised, which is why the tty record matters: it makes the gap legible
  # instead of leaving a green run silent about it.
  def test_colour_inputs_are_pinned_unset_and_recorded
    observation = observe(READ_CASE.merge("case_id" => "t-colour")).fetch("t-colour")
    env = observation.dig("invocation", "env").to_h { |e| [e["name"], e["value"]] }
    %w[NO_COLOR TASKS_THEME COLORTERM TERM].each do |name|
      assert env.key?(name), "#{name} must be in the recorded floor, not merely absent"
      assert_nil env.fetch(name), "#{name} must be pinned to unset"
    end
    # Pinned to unset rather than to a value on purpose: NO_COLOR=1 resolves the
    # theme to "mono" and changes `tasks config --json`, so pinning it to a
    # value would mean the harness altering what it observes.
    pins = observation.dig("invocation", "pins").to_h { |p| [p["name"], p] }
    %w[NO_COLOR TASKS_THEME COLORTERM TERM].each do |name|
      refute pins.fetch(name)["applied"], "#{name} is unset, so it cannot be applied"
      assert_nil pins.fetch(name)["value"]
    end
  end

  def test_terminal_ness_is_recorded_because_it_is_the_colour_switch
    observation = observe(READ_CASE.merge("case_id" => "t-tty")).fetch("t-tty")
    assert_equal({ "stdin" => false, "stdout" => false, "stderr" => false },
                 observation.dig("invocation", "tty"),
                 "every stream is redirected to a file, and the observation must say so")
    refute_includes observation.dig("process", "stdout", "text").to_s, "\e[",
                    "no case reaches a tty, so no observation contains an ANSI escape"
  end

  # The test-only clock seam now resolves inside Tasks::Determinism like every
  # other pin, and the harness records it so "dominated by TASKS_PIN_NOW" is
  # evidence rather than an assertion in a comment.
  def test_the_test_today_sequence_is_pinned_and_reported
    observation = observe(READ_CASE.merge("case_id" => "t-seq")).fetch("t-seq")
    env = observation.dig("invocation", "env").to_h { |e| [e["name"], e["value"]] }
    assert env.key?("TASKS_TEST_TODAY_SEQUENCE")
    assert_nil env.fetch("TASKS_TEST_TODAY_SEQUENCE")

    dominated = observe(READ_CASE.merge("case_id" => "t-seq-dominated",
                                        "env" => { "TASKS_TEST_TODAY_SEQUENCE" => "2020-01-02" }))
                .fetch("t-seq-dominated")
    pin = dominated.dig("invocation", "pins").find { |p| p["name"] == "TASKS_TEST_TODAY_SEQUENCE" }
    assert_equal "2026-03-14T15:09:26Z", pin["value"],
                 "TASKS_PIN_NOW must still dominate: a 2020 instant here means the precedence " \
                 "inverted when the read moved into Tasks::Determinism"
  end

  # --- rollback ------------------------------------------------------------

  # The one product outcome the filesystem cannot show you. An unwritable copy
  # root with the lock sidecar already present makes the atomic write fail; the
  # implementation restores the previous bytes and reports that it did, and the
  # runner records the report rather than inferring anything.
  ROLLBACK_CASE = { "case_id" => "t-rollback", "fixture" => "adversarial/stale-lock-sidecar",
                    "argv" => ["capture", "must not be written", "--json"],
                    "copy_root_mode" => "0555" }.freeze

  def test_a_declared_copy_root_mode_produces_a_labelled_rollback
    observation = observe(ROLLBACK_CASE).fetch("t-rollback")

    assert_equal 1, observation.dig("process", "exit_status")
    assert_equal true, observation.dig("files", "rolled_back"),
                 "the CLI reported a write-then-revert and the runner must record it"
    # Everything else is indistinguishable from a mutation that never wrote —
    # which is the entire reason the label exists.
    refute observation.dig("files", "mutated")
    assert_empty observation.dig("files", "deltas")
    assert_includes observation.dig("process", "stdout", "text"), "\"rolled_back\":true"
  end

  def test_rolled_back_is_null_when_the_implementation_reported_nothing
    observed = observe(READ_CASE, MUTATION_CASE)
    assert_nil observed["t-read"].dig("files", "rolled_back"), "a read reports no rollback flag"
    assert_nil observed["t-mutate"].dig("files", "rolled_back"), "a success reports no rollback flag"
  end

  # The mode is applied to the copy root and to nothing under it, and it is
  # undone before cleanup — otherwise the next run could not empty the directory
  # and stale files would leak into its files.before.
  def test_a_restrictive_copy_root_mode_does_not_outlive_the_case
    work = File.join(@tmp, "work-mode")
    observe(ROLLBACK_CASE, work: work)
    refute File.exist?(File.join(work, "t-rollback")), "the fixture copy outlived the case"
  end

  def test_copy_root_mode_must_be_octal
    _, stderr, status = run_runner("--dry-run",
                                   write_cases(READ_CASE.merge("copy_root_mode" => "u+rwx"), name: "mode.jsonl"))
    assert_equal 2, status.exitstatus
    assert_includes stderr, "copy_root_mode"
  end

  def test_revisions_are_sourced_from_the_implementation
    observation = observe(MUTATION_CASE).fetch("t-mutate")
    assert_match(/\As1\.[0-9a-f]{64}\z/, observation.dig("revisions", "store"))
    resources = observation.dig("revisions", "resources")
    refute_empty resources
    resources.each do |resource|
      assert_match(/\Av1\.[0-9a-f]{64}\.[0-9a-f]{64}\.[0-9a-f]{64}\z/, resource["revision"])
      assert_equal "task", resource["kind"]
    end
    assert_equal resources.map { |r| r["id"] }.sort, resources.map { |r| r["id"] },
                 "resources must be sorted by id"
  end

  # The corpus recorded per-task tokens for this fixture independently of the
  # runner, which is the only cross-check available for the probe's answer.
  def test_probe_reproduces_the_recorded_revision_tokens
    recorded = JSON.parse(File.read(File.join(FIXTURES, "adversarial", "stale-revision",
                                              "revisions.json")))
    observation = observe({ "case_id" => "t-revisions", "fixture" => "adversarial/stale-revision",
                            "argv" => ["list"] }).fetch("t-revisions")
    tokens = observation.dig("revisions", "resources").to_h { |r| [r["id"], r["revision"]] }
    recorded.each do |row|
      assert_equal row["current_revision"], tokens[row["id"]],
                   "probe disagreed with the recorded token for #{row["id"]}"
    end
  end

  def test_every_pin_is_recorded_as_applied_and_the_env_records_unset_vars
    observation = observe(READ_CASE).fetch("t-read")
    pins = observation.dig("invocation", "pins").to_h { |p| [p["name"], p] }
    %w[TZ LANG LC_ALL TASKS_DEVICE TASKS_PIN_NOW TASKS_PIN_IDS TASKS_PIN_COALESCE_SCOPE
       TASKS_PIN_DELEGATION_KEYS TASKS_PIN_HOSTNAME LINES COLUMNS].each do |name|
      assert pins.fetch(name)["applied"], "pin #{name} was not applied"
    end
    assert_equal "2026-03-14T15:09:26Z", pins.fetch("TASKS_PIN_NOW")["value"]

    env = observation.dig("invocation", "env").to_h { |e| [e["name"], e["value"]] }
    assert_nil env.fetch("TASKS_FILE"), "TASKS_FILE must be recorded as unset, not omitted"
    assert_nil env.fetch("TASKS_ARCHIVE")
    assert env.fetch("XDG_CONFIG_HOME").end_with?("/.config")
    assert_equal env.fetch("TASKS_DIR"), observation.dig("fixture", "copy_root")
  end

  # A case may unset a pin; when it does, the pin must report itself unapplied
  # rather than silently keeping the runner's default.
  def test_a_case_can_unset_a_pin
    observation = observe(READ_CASE.merge("case_id" => "t-unpinned",
                                          "env" => { "TASKS_PIN_NOW" => nil }))
                  .fetch("t-unpinned")
    pin = observation.dig("invocation", "pins").find { |p| p["name"] == "TASKS_PIN_NOW" }
    refute pin["applied"]
    assert_nil pin["value"]
  end

  def test_a_malformed_fixture_captures_its_diagnostic
    observation = observe({ "case_id" => "t-malformed", "fixture" => "malformed/invalid-json",
                            "argv" => ["check"] }).fetch("t-malformed")
    assert_equal "malformed", observation.dig("fixture", "class")
    assert_equal 1, observation.dig("process", "exit_status")
    combined = observation.dig("process", "stdout", "text").to_s +
               observation.dig("process", "stderr", "text").to_s
    refute_empty combined, "the diagnostic was not captured"
    assert_equal Digest::SHA256.hexdigest(observation.dig("process", "stdout", "text")),
                 observation.dig("process", "stdout", "sha256")
    refute observation.dig("files", "mutated")
  end

  def test_stdin_is_attached_and_recorded
    with_payload = observe(READ_CASE.merge("case_id" => "t-stdin", "stdin" => "payload\n"))
                   .fetch("t-stdin")
    assert with_payload.dig("invocation", "stdin", "provided")
    assert_equal "payload\n",
                 with_payload.dig("invocation", "stdin", "bytes_base64").unpack1("m")

    without = observe(READ_CASE).fetch("t-read")
    refute without.dig("invocation", "stdin", "provided")
    assert_equal "", without.dig("invocation", "stdin", "bytes_base64")
  end

  # --- determinism ---------------------------------------------------------

  def test_two_pinned_runs_are_byte_identical
    work = File.join(@tmp, "work-identity")
    list = write_cases(READ_CASE, MUTATION_CASE, name: "identity.jsonl")
    outs = %w[a b].map do |name|
      out = File.join(@tmp, "identity-#{name}")
      _, stderr, status = run_runner("--out", out, "--work", work, "--quiet", "--pin-identity", list)
      assert status.success?, stderr
      out
    end
    %w[t-read t-mutate].each do |id|
      first, second = outs.map { |out| File.binread(File.join(out, "#{id}.json")) }
      assert_equal first, second, "#{id} was not byte-identical across two pinned runs"
    end
  end

  # Without --pin-identity exactly two fields may move, both of them harness
  # metadata. Anything else moving means the runner is leaking host state into
  # the observation.
  def test_only_observation_id_and_wall_ms_vary_between_unpinned_runs
    work = File.join(@tmp, "work-vary")
    list = write_cases(MUTATION_CASE, name: "vary.jsonl")
    runs = %w[a b].map do |name|
      out = File.join(@tmp, "vary-#{name}")
      _, stderr, status = run_runner("--out", out, "--work", work, "--quiet", list)
      assert status.success?, stderr
      JSON.parse(File.read(File.join(out, "t-mutate.json")))
    end
    differing = runs[0].keys.reject { |key| runs[0][key] == runs[1][key] }
    assert_equal %w[metrics observation_id], differing.sort
    assert_equal runs[0]["metrics"].reject { |k, _| k == "wall_ms" },
                 runs[1]["metrics"].reject { |k, _| k == "wall_ms" }
  end

  # --- roles, modes, and the recorded environment ---------------------------

  # `files[].role` comes from the paths the implementation resolved, not from a
  # table of filenames. valid/symlinked-store is the fixture that tells the two
  # apart: the store the user names is a link, the bytes are somewhere else.
  # A name table records the file carrying the store as "other", which voids the
  # schema's "the store and the archive were BOTH observed" guarantee and makes
  # the mutation invariant fail on a correct run.
  def test_a_symlinked_store_carries_the_store_role_on_both_spellings
    observation = observe({ "case_id" => "t-symlink", "fixture" => "valid/symlinked-store",
                            "argv" => ["capture", "through the link", "--json"] })
                  .fetch("t-symlink")
    after = observation.dig("files", "after")
    stores = after.select { |s| s["role"] == "store" }.map { |s| s["path"] }
    assert_equal %w[tasks.jsonl tasks.real.jsonl], stores.sort,
                 "both the link and its target must carry the store role"
    link = after.find { |s| s["path"] == "tasks.jsonl" }
    assert_equal "tasks.real.jsonl", link["symlink_target"]
    assert_nil link["sha256"], "a symlink is recorded by target, not by content"
    assert observation.dig("journal", "present"),
           "the journal key must be computed from the symlink-resolved store path"
    # The invariant that a name table breaks: mutated=true is cross-checked
    # against deltas whose role is store or archive.
    assert observation.dig("files", "mutated")
  end

  # Git records no permission bit but the executable one, so a fixture whose
  # subject is a restrictive mode has to declare it in perms.json and have the
  # runner apply it to the copy. Without this the "a chmod-600 store must not
  # widen to 644 across an atomic replacement" contract is untestable and `mode`
  # is a constant column in the baseline.
  def test_a_fixture_perms_manifest_is_applied_to_the_copy
    observation = observe({ "case_id" => "t-perms", "fixture" => "valid/restricted-mode-store",
                            "argv" => ["capture", "widen me", "--json"] }).fetch("t-perms")
    before = observation.dig("files", "before").find { |s| s["role"] == "store" }
    after = observation.dig("files", "after").find { |s| s["role"] == "store" }
    assert_equal "0600", before["mode"], "perms.json was not applied to the copy"
    assert_equal "0600", after["mode"],
                 "the store widened across the atomic replacement"
  end

  # A constant allowlist would let a case set a variable the product reads and
  # produce two observations with byte-identical invocation blocks and different
  # store bytes. The recorded set is the union of the floor and what was passed.
  def test_the_env_records_a_variable_the_case_set_outside_the_floor
    observation = observe(READ_CASE.merge("case_id" => "t-extra-env",
                                          "env" => { "TASKS_DATE_ORDER" => "dmy" }))
                  .fetch("t-extra-env")
    env = observation.dig("invocation", "env").to_h { |e| [e["name"], e["value"]] }
    assert_equal "dmy", env.fetch("TASKS_DATE_ORDER"),
                 "a variable the case set must be visible in the observation, not silent"
    assert_equal "/usr/bin:/bin:/usr/sbin:/sbin", env.fetch("PATH"),
                 "PATH is pinned, so it is recorded"
  end

  # umask is a per-process attribute, not a host fact: it moves `mode` on every
  # file the implementation creates, and mode is compared.
  def test_the_umask_is_pinned_not_inherited
    # Deliberately hostile: the runner is launched from a process whose umask is
    # 0077, which is what a differently-configured CI image looks like. The
    # observation must report the pin, and the modes must be the pinned ones —
    # if the runner merely recorded what it inherited, both would read 0600.
    previous = File.umask(0o077)
    observation = begin
      observe(MUTATION_CASE.merge("case_id" => "t-umask")).fetch("t-umask")
    ensure
      File.umask(previous)
    end
    assert_equal "0022", observation.dig("environment", "umask")
    # The journal index, because it is CREATED by the invocation: the store file
    # already existed and keeps its own mode across the atomic replacement, so it
    # would read 0644 either way and prove nothing.
    index = observation.dig("journal", "index")
    assert_equal "0644", index["mode"],
                 "the operator's umask reached the implementation"
  end

  # --- the probe -----------------------------------------------------------

  def test_probe_reports_an_unreadable_store_without_inventing_a_token
    dir = File.join(@tmp, "probe-store")
    FileUtils.mkdir_p(dir)
    File.write(File.join(dir, "tasks.jsonl"), "not jsonl at all\n")
    env = { "TASKS_DIR" => dir, "HOME" => dir, "XDG_CONFIG_HOME" => File.join(dir, ".config"),
            "XDG_STATE_HOME" => File.join(dir, ".state"), "TZ" => "UTC",
            "PATH" => "/usr/bin:/bin" }
    stdout, stderr, status = Open3.capture3(env, RbConfig.ruby, PROBE, dir, unsetenv_others: true)
    assert status.success?, stderr
    report = JSON.parse(stdout)
    assert_equal "store_invalid", report.dig("revisions", "status")
    assert_empty report.dig("revisions", "resources")
    assert_match(/\As1\./, report.dig("revisions", "store"),
                 "the store token is a digest of bytes and exists even for invalid bytes")
  end
end
