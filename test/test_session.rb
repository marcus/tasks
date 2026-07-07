# frozen_string_literal: true

require_relative "test_helper"
require "json"
require "tui/app"

# Coverage for TUI session persistence: the active view survives a restart
# (Tui::Session + App#restore_view/#save_session).
class TestSession < Minitest::Test
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
        assert_equal :agenda, app.instance_variable_get(:@view), "default on first run"

        app.send(:switch_view, 2) # Next
        app.send(:save_session)   # what run's ensure does on exit

        assert_equal :next, build_app(dir).instance_variable_get(:@view),
                     "a fresh App reopens on the saved view"
      end
    end
  end

  def test_unknown_saved_view_falls_back_to_agenda
    with_state_home do
      Tui::Session.save({ "view" => "themes-someday" })
      Dir.mktmpdir do |dir|
        File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)
        assert_equal :agenda, build_app(dir).instance_variable_get(:@view)
      end
    end
  end

  def test_non_string_saved_view_falls_back_not_crashes
    with_state_home do
      # A hand-edited state file with a non-string view must not crash startup.
      FileUtils.mkdir_p(File.dirname(Tui::Session.path))
      File.write(Tui::Session.path, JSON.generate(version: 1, view: 123))
      Dir.mktmpdir do |dir|
        File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)
        assert_equal :agenda, build_app(dir).instance_variable_get(:@view)
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
end
