# frozen_string_literal: true

require_relative "test_helper"
require "json"
require "set"
require "tui/app"

# Coverage for TUI session persistence: the active view survives a restart
# (Tui::Session + App#restore_view/#save_session).
class TestSession < Minitest::Test
  def ui(app) = app.instance_variable_get(:@ui)

  # Run a block with XDG_STATE_HOME pinned to a fresh dir, so session state
  # written here can't leak into the suite-wide sandbox other tests share.
  def with_state_home
    Dir.mktmpdir do |dir|
      old = ENV["XDG_STATE_HOME"]
      ENV["XDG_STATE_HOME"] = dir
      yield dir
    ensure
      ENV["XDG_STATE_HOME"] = old
    end
  end

  # -- Session module ----------------------------------------------------------

  def test_round_trip
    with_state_home do |dir|
      assert Tui::Session.save({ "view" => "next" })
      assert_equal({ view: "next" }, Tui::Session.load)
      assert_path_exists File.join(dir, "tasks", "tui.json")
    end
  end

  def test_load_missing_file_is_empty
    with_state_home { assert_equal({}, Tui::Session.load) }
  end

  def test_load_corrupt_or_foreign_version_is_empty
    with_state_home do
      FileUtils.mkdir_p(File.dirname(Tui::Session.path))
      File.write(Tui::Session.path, "not json {")
      assert_equal({}, Tui::Session.load)

      File.write(Tui::Session.path, JSON.generate(version: 99, view: "next"))
      assert_equal({}, Tui::Session.load, "a future format is ignored, not misread")
    end
  end

  def test_save_reports_failure_on_unwritable_dir
    skip "chmod is advisory for root" if Process.uid.zero?
    with_state_home do |dir|
      FileUtils.mkdir_p(File.join(dir, "tasks"))
      File.chmod(0o500, File.join(dir, "tasks"))
      begin
        refute Tui::Session.save({ "view" => "next" }), "read-only state dir degrades, not raises"
      ensure
        File.chmod(0o700, File.join(dir, "tasks"))
      end
    end
  end

  # -- App integration ---------------------------------------------------------

  def build_app(dir)
    Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir),
                 llm_config: default_llm_config)
  end

  def test_view_persists_across_app_instances
    with_state_home do
      Dir.mktmpdir do |dir|
        File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)
        app = build_app(dir)
        assert_equal :agenda, ui(app).view, "default on first run"

        app.send(:switch_view, 2) # Next
        app.send(:save_session)   # what run's ensure does on exit

        assert_equal :next, ui(build_app(dir)).view,
                     "a fresh App reopens on the saved view"
      end
    end
  end


  def test_named_panel_mode_persists_across_app_instances
    with_state_home do
      Dir.mktmpdir do |dir|
        File.write(File.join(dir, "tasks.jsonl"), FIXTURE_ORG)
        app = build_app(dir)
        ui(app).panel_mode = :wide
        app.send(:save_session)
        assert_equal :wide, ui(build_app(dir)).panel_mode
      end
    end
  end

  def test_panel_column_offset_persists_across_app_instances
    with_state_home do
      Dir.mktmpdir do |dir|
        File.write(File.join(dir, "tasks.jsonl"), FIXTURE_ORG)
        app = build_app(dir)
        ui(app).panel_offset = 7
        app.send(:save_session)
        assert_equal 7, ui(build_app(dir)).panel_offset
      end
    end
  end

  def test_unknown_saved_view_falls_back_to_agenda
    with_state_home do
      Tui::Session.save({ "view" => "themes-someday" })
      Dir.mktmpdir do |dir|
        File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)
        assert_equal :agenda, ui(build_app(dir)).view
      end
    end
  end

  def test_non_string_saved_view_falls_back_not_crashes
    with_state_home do
      # A hand-edited state file with a non-string view must not crash startup.
      FileUtils.mkdir_p(File.dirname(Tui::Session.path))
      File.write(Tui::Session.path, JSON.generate(version: 2, view: 123))
      Dir.mktmpdir do |dir|
        File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)
        assert_equal :agenda, ui(build_app(dir)).view
      end
    end
  end

  def test_unknown_keys_survive_for_future_state
    with_state_home do
      # A future feature (e.g. theme) writes alongside view; load carries both.
      Tui::Session.save({ "view" => "inbox", "theme" => "solarized" })
      assert_equal({ view: "inbox", theme: "solarized" }, Tui::Session.load)
    end
  end

  # -- collapsed set (Stage 5) -------------------------------------------------

  def test_collapsed_round_trips
    with_state_home do
      assert Tui::Session.save({ "view" => "agenda", "collapsed" => %w[aaaa0002 aaaa0003] })
      assert_equal({ view: "agenda", collapsed: %w[aaaa0002 aaaa0003] }, Tui::Session.load)
    end
  end

  # A sandbox app on the shared fixture, so live task ids are known (FIX values).
  def app_on_fixture(dir)
    File.write(File.join(dir, "tasks.jsonl"), FIXTURE_ORG)
    build_app(dir)
  end

  def test_collapsed_persists_across_app_instances
    with_state_home do
      Dir.mktmpdir do |dir|
        app = app_on_fixture(dir)
        ui(app).collapsed = Set[FIX[:flight]]
        app.send(:save_session)
        assert_equal Set[FIX[:flight]], ui(app_on_fixture(dir)).collapsed
      end
    end
  end

  def test_save_session_prunes_stale_collapsed_ids
    with_state_home do
      Dir.mktmpdir do |dir|
        app = app_on_fixture(dir)
        # One live id (flight) and one that no longer exists in the file.
        ui(app).collapsed = Set[FIX[:flight], "deadbeef"]
        app.send(:save_session)
        assert_equal [FIX[:flight]], Tui::Session.load[:collapsed], "stale id pruned on save"
      end
    end
  end

  def test_legacy_session_without_collapsed_loads_empty
    with_state_home do
      Tui::Session.save({ "view" => "next" }) # no collapsed key at all
      Dir.mktmpdir do |dir|
        assert_empty ui(app_on_fixture(dir)).collapsed
      end
    end
  end

  def test_corrupt_collapsed_value_falls_back_to_empty
    with_state_home do
      FileUtils.mkdir_p(File.dirname(Tui::Session.path))
      # A string where an array belongs must degrade, not crash startup.
      File.write(Tui::Session.path, JSON.generate(version: 2, view: "agenda", collapsed: "oops"))
      Dir.mktmpdir do |dir|
        assert_empty ui(app_on_fixture(dir)).collapsed
      end
    end
  end

  # -- context filter ----------------------------------------------------------

  def test_context_filter_persists_across_app_instances
    with_state_home do
      Dir.mktmpdir do |dir|
        app = app_on_fixture(dir)
        ui(app).context_filter = "@home"
        app.send(:save_session)
        assert_equal "@home", ui(app_on_fixture(dir)).context_filter
      end
    end
  end

  def test_context_filter_normalizes_on_restore
    with_state_home do
      Tui::Session.save({ "view" => "next", "context_filter" => "work" })
      Dir.mktmpdir do |dir|
        assert_equal "@work", ui(app_on_fixture(dir)).context_filter
      end
    end
  end

  def test_save_session_prunes_stale_context_filter
    with_state_home do
      Dir.mktmpdir do |dir|
        app = app_on_fixture(dir)
        ui(app).context_filter = "@gone"
        app.send(:save_session)
        assert_nil Tui::Session.load[:context_filter], "stale context pruned on save"
      end
    end
  end

  def test_legacy_session_without_context_filter_loads_nil
    with_state_home do
      Tui::Session.save({ "view" => "next" })
      Dir.mktmpdir do |dir|
        assert_nil ui(app_on_fixture(dir)).context_filter
      end
    end
  end

  def test_corrupt_context_filter_falls_back_to_nil
    with_state_home do
      FileUtils.mkdir_p(File.dirname(Tui::Session.path))
      File.write(Tui::Session.path, JSON.generate(version: 2, view: "agenda", context_filter: 123))
      Dir.mktmpdir do |dir|
        assert_nil ui(app_on_fixture(dir)).context_filter
      end
    end
  end

  def test_clearing_context_filter_removes_it_from_session
    with_state_home do
      Dir.mktmpdir do |dir|
        app = app_on_fixture(dir)
        ui(app).context_filter = "@home"
        app.send(:save_session)
        assert_equal "@home", Tui::Session.load[:context_filter]

        ui(app).context_filter = nil
        app.send(:save_session)
        assert_nil Tui::Session.load[:context_filter]
      end
    end
  end
end
