# frozen_string_literal: true

require_relative "test_helper"
require "tasks/determinism"
require "tasks/config"
require "tasks/store"
require "tasks/create_task"
require "tasks/task_patch"
require "tasks/application"
require "open3"

# Coverage for the conformance harness's determinism seams (lib/tasks/determinism.rb,
# porting/specs/determinism.md). Two halves that matter equally:
#
#   * pinned — the pin actually reaches the store/journal and two runs agree; and
#   * unpinned — the seam is invisible, because "adding a seam is in scope,
#     changing behavior is not" is only true if the default path is untouched.
class TestDeterminism < Minitest::Test
  D = Tasks::Determinism

  def setup
    D.reset!
  end

  def teardown
    D.reset!
  end

  # -- clock -------------------------------------------------------------------

  def test_now_is_nil_when_unpinned
    assert_nil D.now(env: {})
    assert_nil D.clock(env: {})
  end

  def test_now_parses_an_iso8601_instant_as_utc
    pinned = D.now(env: { "TASKS_PIN_NOW" => "2026-03-14T15:09:26Z" })
    assert_equal Time.utc(2026, 3, 14, 15, 9, 26), pinned
    assert pinned.utc?
  end

  def test_now_converts_an_offset_instant_to_utc
    assert_equal Time.utc(2026, 3, 14, 22, 9, 26),
                 D.now(env: { "TASKS_PIN_NOW" => "2026-03-14T15:09:26-07:00" })
  end

  def test_clock_returns_the_same_instant_every_call
    clock = D.clock(env: { "TASKS_PIN_NOW" => "2026-03-14T15:09:26Z" })
    assert_equal clock.call, clock.call
  end

  # A silent fallback to the wall clock would produce a run that looks
  # reproducible and is not — the harness's worst failure mode.
  def test_now_raises_rather_than_falling_back_to_the_wall_clock
    error = assert_raises(ArgumentError) { D.now(env: { "TASKS_PIN_NOW" => "yesterday" }) }
    assert_match(/TASKS_PIN_NOW/, error.message)
  end

  def test_blank_pin_is_treated_as_unset
    assert_nil D.now(env: { "TASKS_PIN_NOW" => "  " })
  end

  # -- id sequence -------------------------------------------------------------

  def test_id_source_is_nil_when_unpinned
    assert_nil D.id_source(env: {})
  end

  def test_id_source_mints_the_listed_ids_in_order
    source = D.id_source(env: { "TASKS_PIN_IDS" => "bbbb0001,bbbb0002" })
    assert_equal %w[bbbb0001 bbbb0002], [source.call, source.call]
  end

  # Running past the list must stay deterministic rather than fail mid-mutation:
  # a mutation that mints more ids than the case author listed is a bad guess,
  # not a reason to leave a half-written store.
  def test_id_source_continues_by_incrementing_the_last_token
    source = D.id_source(env: { "TASKS_PIN_IDS" => "bbbb0001" })
    assert_equal %w[bbbb0001 bbbb0002 bbbb0003], 3.times.map { source.call }
  end

  def test_seq_token_starts_at_zero
    source = D.id_source(env: { "TASKS_PIN_IDS" => "seq" })
    assert_equal %w[00000000 00000001], [source.call, source.call]
  end

  def test_id_source_wraps_at_32_bits
    source = D.id_source(env: { "TASKS_PIN_IDS" => "ffffffff" })
    assert_equal %w[ffffffff 00000000], [source.call, source.call]
  end

  def test_id_source_rejects_a_token_that_is_not_eight_hex
    assert_raises(ArgumentError) { D.id_source(env: { "TASKS_PIN_IDS" => "nope" }) }
    assert_raises(ArgumentError) { D.id_source(env: { "TASKS_PIN_IDS" => "bbb1" }) }
  end

  # One process may build several Stores (bin/tasks keeps a CLI Store and an
  # application StoreFactory); they must draw from one sequence, not restart it.
  def test_id_source_is_memoized_per_spec
    env = { "TASKS_PIN_IDS" => "bbbb0001" }
    first = D.id_source(env: env)
    assert_same first, D.id_source(env: env)
    first.call
    assert_equal "bbbb0002", D.id_source(env: env).call
  end

  def test_id_source_rebuilds_when_the_spec_changes
    D.id_source(env: { "TASKS_PIN_IDS" => "bbbb0001" }).call
    assert_equal "cccc0001", D.id_source(env: { "TASKS_PIN_IDS" => "cccc0001" }).call
  end

  # -- hostname, coalesce scope, winsize ---------------------------------------

  def test_hostname_defaults_to_the_real_hostname
    assert_equal Socket.gethostname, D.hostname(env: {}).call
  end

  def test_hostname_honors_the_pin
    assert_equal "fixture-host", D.hostname(env: { "TASKS_PIN_HOSTNAME" => "fixture-host" }).call
  end

  def test_coalesce_scope_pin
    assert_nil D.coalesce_scope(env: {})
    assert_equal "pinned-scope", D.coalesce_scope(env: { "TASKS_PIN_COALESCE_SCOPE" => "pinned-scope" })
  end

  # Half a geometry is not a geometry; one of the two would otherwise silently
  # pair with the real terminal's other dimension.
  def test_winsize_requires_both_dimensions
    assert_nil D.winsize(env: { "COLUMNS" => "100" })
    assert_nil D.winsize(env: { "LINES" => "40" })
    assert_equal [40, 100], D.winsize(env: { "LINES" => "40", "COLUMNS" => "100" })
  end

  def test_winsize_ignores_nonsense
    assert_nil D.winsize(env: { "LINES" => "0", "COLUMNS" => "100" })
    assert_nil D.winsize(env: { "LINES" => "wide", "COLUMNS" => "100" })
  end

  # -- report ------------------------------------------------------------------

  # An unset pin is recorded as null rather than omitted: "no pin was applied" is
  # a fact the observation must carry, and omission would make it look like the
  # harness never checked.
  def test_report_lists_every_pin_including_unset_ones
    report = D.report(env: { "TASKS_PIN_NOW" => "2026-03-14T15:09:26Z" })
    assert_equal Tasks::Determinism::KEYS.sort, report["pins"].keys.sort
    assert_equal "2026-03-14T15:09:26Z", report["pins"]["TASKS_PIN_NOW"]
    assert_nil report["pins"]["TASKS_PIN_IDS"]
    refute_empty report["tzdb_version"]
  end

  # -- the seam inside Store ---------------------------------------------------

  FIXTURE = <<~JSONL
    {"type":"meta","version":2}
    {"type":"section","id":"aaaa0001","title":"Inbox"}
  JSONL

  def with_store(id_source: nil)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, FIXTURE)
      yield Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"),
                             journal_dir: File.join(dir, "journal"),
                             now: -> { Time.utc(2026, 3, 14, 15, 9, 26) }, device: "fixture",
                             id_source: id_source), org
    end
  end

  def test_store_mints_random_ids_when_unpinned
    with_store do |store, _org|
      store.create_task!(Tasks::CreateTask.new(title: "first"))
      store.create_task!(Tasks::CreateTask.new(title: "second"))
      ids = store.items.map(&:id)
      assert_equal 2, ids.uniq.length
      ids.each { |id| assert_match(/\A[0-9a-f]{8}\z/, id) }
    end
  end

  def test_store_mints_pinned_ids
    source = D.id_source(env: { "TASKS_PIN_IDS" => "bbbb0001,bbbb0002" })
    with_store(id_source: source) do |store, _org|
      store.create_task!(Tasks::CreateTask.new(title: "first"))
      store.create_task!(Tasks::CreateTask.new(title: "second"))
      assert_equal %w[bbbb0001 bbbb0002], store.items.map(&:id)
    end
  end

  # The pin feeds gen_id's existing collision loop rather than bypassing it, so a
  # case author who lists an id already in the fixture gets the next one, not a
  # duplicate.
  def test_pinned_ids_still_skip_ids_already_taken
    source = D.id_source(env: { "TASKS_PIN_IDS" => "aaaa0001,bbbb0007" })
    with_store(id_source: source) do |store, _org|
      store.create_task!(Tasks::CreateTask.new(title: "first"))
      assert_equal %w[bbbb0007], store.items.map(&:id)
    end
  end

  # -- the seam inside StoreFactory --------------------------------------------

  def test_store_factory_defaults_to_a_random_coalesce_scope
    Dir.mktmpdir do |dir|
      args = { org: File.join(dir, "tasks.jsonl"), archive: File.join(dir, "archive.jsonl") }
      scopes = 2.times.map { Tasks::StoreFactory.new(**args).send(:coalesce_scope) }
      refute_equal scopes.first, scopes.last
      assert_match(/\A[0-9a-f]{32}\z/, scopes.first)
    end
  end

  def test_store_factory_threads_pins_into_every_store_it_builds
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, FIXTURE)
      factory = Tasks::StoreFactory.new(
        org: org, archive: File.join(dir, "archive.jsonl"), journal_dir: File.join(dir, "journal"),
        now: -> { Time.utc(2026, 3, 14, 15, 9, 26) }, device: "fixture",
        coalesce_scope: "pinned-scope",
        id_source: D.id_source(env: { "TASKS_PIN_IDS" => "bbbb0001,bbbb0002" })
      )
      first = factory.call
      first.create_task!(Tasks::CreateTask.new(title: "first"))
      second = factory.call
      second.create_task!(Tasks::CreateTask.new(title: "second"))
      assert_equal %w[bbbb0001 bbbb0002], second.items.map(&:id),
                   "a second Store from the same factory continues the sequence"
      # The scope is only persisted alongside a coalesce key, and that is exactly
      # where it matters: it is the token that decides whether a later process may
      # extend this undo step. Pinning it keeps journal bytes reproducible.
      second.patch_task!(Tasks::TaskPatch.new(
        id: "bbbb0002", field: :priority, value: "A", expected: nil, coalesce_key: "edit-session"
      ))
      index = JSON.parse(File.read(File.join(dir, "journal", "index.json")), symbolize_names: true)
      assert_equal "pinned-scope", index[:states].last[:coalesce_scope]
    end
  end

  # -- the seam inside Config --------------------------------------------------

  def test_config_resolve_honors_the_hostname_pin
    Dir.mktmpdir do |dir|
      paths = Tasks::Config.resolve(
        default_dir: dir,
        env: { "TASKS_DIR" => dir, "XDG_CONFIG_HOME" => File.join(dir, "config"),
               "TASKS_PIN_HOSTNAME" => "fixture-host.local" }
      )
      assert_equal "fixture-host.local", paths.hostname
    end
  end

  def test_config_resolve_still_takes_an_explicit_hostname_provider
    Dir.mktmpdir do |dir|
      paths = Tasks::Config.resolve(
        default_dir: dir,
        env: { "TASKS_DIR" => dir, "XDG_CONFIG_HOME" => File.join(dir, "config"),
               "TASKS_PIN_HOSTNAME" => "ignored" },
        hostname: -> { "explicit-host" }
      )
      assert_equal "explicit-host", paths.hostname
    end
  end

  # -- end to end: the acceptance criterion ------------------------------------

  BIN = File.expand_path("../bin/tasks", __dir__)

  PINS = {
    "TZ" => "America/Denver",
    "TASKS_DEVICE" => "fixture",
    "TASKS_PIN_NOW" => "2026-03-14T15:09:26Z",
    "TASKS_PIN_IDS" => "bbbb0001,bbbb0002,bbbb0003",
    "TASKS_PIN_COALESCE_SCOPE" => "pinned-scope",
    "TASKS_PIN_HOSTNAME" => "fixture-host"
  }.freeze

  def pinned_cli(dir, *args)
    env = PINS.merge(
      "TASKS_FILE" => File.join(dir, "tasks.jsonl"),
      "TASKS_ARCHIVE" => File.join(dir, "archive.jsonl"),
      "XDG_CONFIG_HOME" => File.join(dir, "config"),
      "XDG_STATE_HOME" => File.join(dir, "state")
    )
    out, err, status = Open3.capture3(env, "ruby", BIN, *args)
    [out.force_encoding("UTF-8"), err.force_encoding("UTF-8"), status]
  end

  # The story's acceptance criterion, at the size a unit test can carry: one
  # fixture, two copies, one pinned mutation each, identical store and journal
  # bytes. The full proof (including the lock sidecar and a hand-rolled
  # observation) lives in the story's evidence.
  def test_two_pinned_runs_produce_identical_stores_and_journals
    outputs = []
    stores = []
    journals = []

    2.times do
      Dir.mktmpdir("pinned-run") do |dir|
        FileUtils.mkdir_p(File.join(dir, "config", "tasks"))
        File.write(File.join(dir, "config", "tasks", "config"), "")
        File.write(File.join(dir, "tasks.jsonl"), FIXTURE)

        out, err, status = pinned_cli(dir, "capture", "add the ported behavior",
                                      "--priority", "A", "--tag", "port")
        assert status.success?, "capture failed: #{err}"
        outputs << out

        stores << File.binread(File.join(dir, "tasks.jsonl"))
        index = Dir[File.join(dir, "state", "tasks", "journal", "*", "index.json")].first
        refute_nil index, "the pinned run should have written a journal"
        # `org` records the store's canonical absolute path, which is a property
        # of where the copy lives rather than of the run. Everything else in the
        # index is compared verbatim — see porting/specs/determinism.md.
        journals << JSON.parse(File.read(index)).tap { |i| i.delete("org") }
      end
    end

    assert_equal stores.first, stores.last, "store bytes must be identical"
    assert_equal journals.first, journals.last, "journal state must be identical"
    assert_equal outputs.first, outputs.last, "stdout must be identical"
    assert_includes stores.first, '"id":"bbbb0001"'
    assert_includes stores.first, '"updated":"2026-03-14T15:09:26Z#fixture"'
  end

  # The other half of the contract: with nothing pinned the same command still
  # behaves as it always did — a fresh random id and a real timestamp.
  def test_an_unpinned_run_is_untouched
    Dir.mktmpdir("unpinned-run") do |dir|
      FileUtils.mkdir_p(File.join(dir, "config", "tasks"))
      File.write(File.join(dir, "config", "tasks", "config"), "")
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE)
      env = { "TASKS_FILE" => File.join(dir, "tasks.jsonl"),
              "TASKS_ARCHIVE" => File.join(dir, "archive.jsonl"),
              "XDG_CONFIG_HOME" => File.join(dir, "config"),
              "XDG_STATE_HOME" => File.join(dir, "state") }
      _out, err, status = Open3.capture3(env, "ruby", BIN, "capture", "unpinned")
      assert status.success?, err
      record = File.readlines(File.join(dir, "tasks.jsonl")).last
      assert_match(/"id":"[0-9a-f]{8}"/, record)
      refute_includes record, "bbbb0001"
      assert_match(/"updated":"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z#[a-z0-9]+"/, record)
    end
  end
end
