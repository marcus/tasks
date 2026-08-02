# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "rbconfig"
require "json"
require "socket"
require_relative "../lib/tasks/update_stamp"

# The pins are only worth what their weakest call site is worth.
#
# `porting/specs/determinism.md` § "Verifying a pin actually took effect" offers
# `invocation.pins[].applied` as the defence against a pin that was set and
# silently ignored. It is a weaker defence than it reads: the probe computes
# `applied` by asking whether `Tasks::Determinism` RESOLVED a value, not whether
# every consumer USED it. `Tasks::Application` has some thirty methods with a
# `today: Date.today` default parameter; one adapter call site that forgets to
# pass `today:` yields wall-clock output with `applied: true` recorded beside it.
#
# Two real defects had exactly that shape and neither was caught by a pin
# report: an unpinned `SecureRandom.hex(8)` persisted into journal `index.json`
# on every delegation, and `UpdateStamp.device` reading the real
# `Socket.gethostname` straight past `TASKS_PIN_HOSTNAME`.
#
# So this test does not ask the process what it thinks it did. It intercepts
# `Date.today`, `Time.now`, `Socket.gethostname`, and `SecureRandom.hex/uuid` at
# the source (test/support/determinism_trap.rb, loaded via `RUBYOPT=-r`), runs
# the fully-pinned commands the conformance corpus runs, and fails on any read
# the pins were supposed to have removed.
#
# It is a structural check, not a spot check: adding a command to COMMANDS is the
# whole cost of covering a new code path.
class TestPortingDeterminismSeams < Minitest::Test
  CLI = File.expand_path("../bin/tasks", __dir__)
  TRAP = File.expand_path("support/determinism_trap.rb", __dir__)
  FIXTURE = File.expand_path("../porting/fixtures/valid/small-gtd/store", __dir__)

  # The pin set the runner uses, verbatim in intent: if these two drift apart the
  # test stops describing the harness. `porting/runners/ruby/run` is the source
  # of truth; the values are irrelevant, the coverage is not.
  PINS = {
    "TZ" => "UTC",
    "TASKS_TIMEZONE" => "UTC",
    "LANG" => "en_US.UTF-8",
    "LC_ALL" => "en_US.UTF-8",
    "TASKS_DEVICE" => "fixture",
    "TASKS_PIN_NOW" => "2026-03-14T15:09:26Z",
    "TASKS_PIN_IDS" => "bbbb0001",
    "TASKS_PIN_COALESCE_SCOPE" => "pinned-scope",
    "TASKS_PIN_DELEGATION_KEYS" => "cccc000000000001",
    "TASKS_PIN_HOSTNAME" => "fixture-host",
    "LINES" => "40",
    "COLUMNS" => "100"
  }.freeze

  # One read and four writes, chosen to cover every seam a pin exists for: the
  # clock (all of them), minted ids (capture), the journal's coalescing scope
  # (any mutation), its per-operation coalescing key (delegate), and the update
  # stamp's device slug (any mutation).
  COMMANDS = [
    %w[list],
    %w[agenda],
    ["capture", "seam probe", "--json"],
    ["done", "Skim the release notes", "--json"],
    %w[delegate 1a2b3c02 implement --json]
  ].freeze

  # `Time.now` inside tzinfo's own class bodies is a load-time artifact of the
  # zone library and has nothing to do with the invocation's output. Everything
  # under bin/ and lib/tasks/ is ours and is a finding.
  OURS = %r{(?:/bin/tasks|/lib/tasks/)}

  # The one documented exception: the CLI operation id. It is carried on
  # `Tasks::OperationContext` for future audit seams and is written to no store,
  # journal, or output — `determinism.md` § "Not pinned, because nothing
  # observable depends on it" says so in writing. Matched by enclosing method
  # rather than by line number so the allowance survives an edit above it.
  ALLOWED_RANDOM = /cli_operation_context/

  def setup
    @tmp = Dir.mktmpdir("determinism-seams")
  end

  def teardown
    FileUtils.remove_entry(@tmp) if @tmp && File.directory?(@tmp)
  end

  # --- helpers -------------------------------------------------------------

  # Run one command against a pristine copy under the full pin set, with the
  # trap loaded, and return the recorded reads as [kind, site] pairs.
  def trapped(argv)
    root = File.join(@tmp, "run-#{argv.join("-").gsub(/[^a-z0-9-]/i, "_")}")
    FileUtils.mkdir_p(root)
    assert system("cp", "-a", "#{FIXTURE}/.", "#{root}/"), "fixture copy failed"
    FileUtils.mkdir_p(File.join(root, ".config", "tasks"))
    FileUtils.mkdir_p(File.join(root, ".state"))
    log = File.join(root, "trap.log")

    env = PINS.merge(
      "PATH" => ENV["PATH"],
      "HOME" => root, "TASKS_DIR" => root,
      "XDG_CONFIG_HOME" => File.join(root, ".config"),
      "XDG_STATE_HOME" => File.join(root, ".state"),
      "TASKS_TRAP_LOG" => log,
      "RUBYOPT" => "-r#{TRAP}"
    )
    stdout, stderr, status = Open3.capture3(env, RbConfig.ruby, CLI, *argv,
                                            chdir: root, unsetenv_others: true)
    assert status.success?,
           "`tasks #{argv.join(" ")}` failed under the trap (#{status.exitstatus}): #{stderr}#{stdout}"
    return [] unless File.file?(log)

    File.readlines(log, chomp: true).map { |line| line.split("\t", 2) }
  end

  def ours(reads, kind)
    reads.select { |k, site| k == kind && OURS.match?(site.to_s) }
  end

  def describe(hits)
    hits.map { |kind, site| "  #{kind} at #{site}" }.join("\n")
  end

  # --- the seams -----------------------------------------------------------

  # The clock. `TASKS_PIN_NOW` is supposed to remove every wall-clock read from
  # the pinned path — both the instant and the calendar day, which are separate
  # seams (`Determinism.clock` and `today_local`) and fail separately.
  def test_no_pinned_command_reads_the_wall_clock
    COMMANDS.each do |argv|
      reads = trapped(argv)
      today = ours(reads, "Date.today")
      now = ours(reads, "Time.now")
      assert_empty today,
                   "`tasks #{argv.join(" ")}` read Date.today with TASKS_PIN_NOW set:\n#{describe(today)}"
      assert_empty now,
                   "`tasks #{argv.join(" ")}` read Time.now with TASKS_PIN_NOW set:\n#{describe(now)}"
    end
  end

  # The hostname, and both of its consumers. `Tasks::Config` selects a
  # `host_context.<hostname>` entry with it; `Tasks::UpdateStamp.device` derives
  # the device half of every update stamp from it when `TASKS_DEVICE` is unset.
  # One pin has to cover both, so the interesting case is the one where
  # `TASKS_DEVICE` is *not* set to hide the second consumer.
  def test_no_pinned_command_reads_the_real_hostname
    COMMANDS.each do |argv|
      hits = ours(trapped(argv), "Socket.gethostname")
      assert_empty hits,
                   "`tasks #{argv.join(" ")}` read the real hostname with TASKS_PIN_HOSTNAME set:\n#{describe(hits)}"
    end
  end

  # Randomness. Every mint that reaches durable bytes has a pin; the operation id
  # is the single documented exception and is allowlisted by name.
  def test_the_only_unpinned_randomness_is_the_operation_id
    COMMANDS.each do |argv|
      hits = trapped(argv).select { |k, site| k.start_with?("SecureRandom") && OURS.match?(site.to_s) }
      unexpected = hits.reject { |_, site| ALLOWED_RANDOM.match?(site.to_s) }
      assert_empty unexpected,
                   "`tasks #{argv.join(" ")}` minted randomness outside the documented " \
                   "exception; if it reaches store, journal, or output bytes it needs a pin " \
                   "in porting/specs/determinism.md:\n#{describe(unexpected)}"
    end
  end

  # The regression that motivated the whole file, stated directly rather than as
  # an absence: with TASKS_DEVICE deliberately unset, the update stamp's device
  # slug must come from the pin, not from the machine. Before the fix this wrote
  # the operator's real hostname into store bytes while the observation recorded
  # `TASKS_PIN_HOSTNAME applied: true`.
  def test_the_hostname_pin_reaches_the_update_stamp_device_slug
    root = File.join(@tmp, "device")
    FileUtils.mkdir_p(root)
    assert system("cp", "-a", "#{FIXTURE}/.", "#{root}/")
    FileUtils.mkdir_p(File.join(root, ".config", "tasks"))
    FileUtils.mkdir_p(File.join(root, ".state"))
    # A hostname whose slug cannot be confused with the TASKS_DEVICE default:
    # `slug("fixture-host")` is "fixture", which is exactly the device the pin
    # set would have produced anyway, so the pinned value has to be distinct for
    # the assertion to mean anything.
    env = PINS.merge("PATH" => ENV["PATH"], "HOME" => root, "TASKS_DIR" => root,
                     "XDG_CONFIG_HOME" => File.join(root, ".config"),
                     "XDG_STATE_HOME" => File.join(root, ".state"),
                     "TASKS_PIN_HOSTNAME" => "pinnedhost")
    env.delete("TASKS_DEVICE")
    _, stderr, status = Open3.capture3(env, RbConfig.ruby, CLI, "capture", "device probe",
                                       chdir: root, unsetenv_others: true)
    assert status.success?, stderr
    store = File.read(File.join(root, "tasks.jsonl"))
    assert_includes store, "Z#pinnedhost",
                    "the update stamp did not use the pinned hostname"
    real = Tasks::UpdateStamp.slug(Socket.gethostname)
    refute_includes store, "Z##{real}",
                    "the real hostname (#{real}) leaked into store bytes"
  end

  # A pinned delegation is byte-stable, journal included. The store agreed
  # before this pin existed; `index.json` did not, because the per-operation
  # coalescing key was 16 characters of fresh randomness on every run.
  def test_two_pinned_delegations_write_identical_journal_bytes
    digests = %w[a b].map do |name|
      root = File.join(@tmp, "delegate-#{name}")
      FileUtils.mkdir_p(root)
      assert system("cp", "-a", "#{FIXTURE}/.", "#{root}/")
      FileUtils.mkdir_p(File.join(root, ".config", "tasks"))
      FileUtils.mkdir_p(File.join(root, ".state"))
      env = PINS.merge("PATH" => ENV["PATH"], "HOME" => root, "TASKS_DIR" => root,
                       "XDG_CONFIG_HOME" => File.join(root, ".config"),
                       "XDG_STATE_HOME" => File.join(root, ".state"))
      _, stderr, status = Open3.capture3(env, RbConfig.ruby, CLI, "delegate", "1a2b3c02",
                                         "implement", "--json", chdir: root, unsetenv_others: true)
      assert status.success?, stderr
      indexes = Dir[File.join(root, ".state", "tasks", "journal", "*", "index.json")]
      assert_equal 1, indexes.size, "expected exactly one journal, got #{indexes.inspect}"
      index = indexes.first
      # The org path differs between the two copies by construction and is
      # compared elsewhere; the coalescing key is what this test is about.
      JSON.parse(File.read(index)).tap { |doc| doc.delete("org") }
    end
    assert_equal digests[0], digests[1],
                 "two fully-pinned delegations produced different journal index documents"
    key = digests[0]["states"].filter_map { |s| s["coalesce_key"] }.first
    assert_equal "delegation-delegate-cccc000000000001", key,
                 "the delegation coalescing key did not come from TASKS_PIN_DELEGATION_KEYS"
  end
end
