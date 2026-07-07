# frozen_string_literal: true

require_relative "test_helper"
require "tasks/config"
require "json"
require "open3"

# Tasks::Config path resolution — the layer that lets task data live outside
# the repo. Every test passes an explicit env hash; none may read real ENV.
class TestConfig < Minitest::Test
  BIN = File.expand_path("../bin/tasks", __dir__)

  # Env for hermetic CLI runs: clears any real TASKS_* overrides and points
  # the config-file lookup at a sandbox XDG dir.
  def clean_env(xdg)
    { "TASKS_ORG" => nil, "TASKS_ARCHIVE" => nil, "TASKS_DIR" => nil,
      "TASKS_URGENT_DAYS" => nil, "TASKS_THEME" => nil, "NO_COLOR" => nil,
      "XDG_CONFIG_HOME" => xdg }
  end

  def resolve(env: {}, default: "/repo")
    # Route the config file into a throwaway XDG dir unless the test sets one
    env = { "XDG_CONFIG_HOME" => @xdg }.merge(env)
    Tasks::Config.resolve(default_dir: default, env: env)
  end

  def setup
    @xdg = Dir.mktmpdir
  end

  def teardown
    FileUtils.remove_entry(@xdg)
  end

  def write_config(text)
    dir = File.join(@xdg, "tasks")
    FileUtils.mkdir_p(dir)
    File.write(File.join(dir, "config"), text)
  end

  # -- resolution precedence --------------------------------------------------

  def test_defaults_to_default_dir
    paths = resolve
    assert_equal "/repo/gtd.org", paths.org
    assert_equal "/repo/archive.org", paths.archive
    assert_equal({ org: "default", archive: "default", urgent_days: "default", theme: "default" },
                 paths.sources)
  end

  def test_tasks_dir_env_points_both_files
    paths = resolve(env: { "TASKS_DIR" => "/data" })
    assert_equal "/data/gtd.org", paths.org
    assert_equal "/data/archive.org", paths.archive
    assert_equal "TASKS_DIR env", paths.sources[:org]
  end

  def test_per_file_env_beats_tasks_dir
    paths = resolve(env: { "TASKS_DIR" => "/data", "TASKS_ORG" => "/elsewhere/my.org" })
    assert_equal "/elsewhere/my.org", paths.org
    assert_equal "TASKS_ORG env", paths.sources[:org]
    assert_equal "/data/archive.org", paths.archive # archive still follows TASKS_DIR
  end

  def test_config_file_dir_key
    write_config("dir = /from-file\n")
    paths = resolve
    assert_equal "/from-file/gtd.org", paths.org
    assert_equal "/from-file/archive.org", paths.archive
    assert_equal "config file", paths.sources[:org]
  end

  def test_config_file_per_file_keys_beat_dir_key
    write_config(<<~CONF)
      dir = /from-file
      org = /special/tasks.org
    CONF
    paths = resolve
    assert_equal "/special/tasks.org", paths.org
    assert_equal "/from-file/archive.org", paths.archive
  end

  def test_env_beats_config_file
    write_config("dir = /from-file\n")
    paths = resolve(env: { "TASKS_DIR" => "/from-env" })
    assert_equal "/from-env/gtd.org", paths.org
  end

  def test_empty_env_values_are_ignored
    paths = resolve(env: { "TASKS_DIR" => "", "TASKS_ORG" => "" })
    assert_equal "/repo/gtd.org", paths.org
  end

  # -- config file parsing ----------------------------------------------------

  def test_config_file_ignores_comments_blanks_and_unknown_keys
    write_config(<<~CONF)
      # where my tasks live
      dir = /data

      color = always
    CONF
    paths = resolve
    assert_equal "/data/gtd.org", paths.org
  end

  def test_config_file_expands_tilde
    write_config("dir = ~/tasks\n")
    paths = resolve
    assert_equal File.join(Dir.home, "tasks", "gtd.org"), paths.org
  end

  def test_missing_config_file_is_fine
    paths = resolve
    assert_equal "/repo/gtd.org", paths.org
    refute File.file?(paths.config_file)
  end

  # -- urgent_days (the quadrants urgency window) -----------------------------

  def test_urgent_days_defaults_to_three
    paths = resolve
    assert_equal 3, paths.urgent_days
    assert_equal "default", paths.sources[:urgent_days]
  end

  def test_urgent_days_from_config_file
    write_config("urgent_days = 7\n")
    paths = resolve
    assert_equal 7, paths.urgent_days
    assert_equal "config file", paths.sources[:urgent_days]
  end

  def test_urgent_days_env_beats_config_file
    write_config("urgent_days = 7\n")
    paths = resolve(env: { "TASKS_URGENT_DAYS" => "14" })
    assert_equal 14, paths.urgent_days
    assert_equal "TASKS_URGENT_DAYS env", paths.sources[:urgent_days]
  end

  def test_urgent_days_invalid_config_value_falls_back_to_default
    write_config("urgent_days = soon\n")
    assert_equal 3, resolve.urgent_days
  end

  def test_urgent_days_invalid_env_falls_back_to_config
    write_config("urgent_days = 9\n")
    # a negative (or unparseable) env value is ignored, not fatal
    assert_equal 9, resolve(env: { "TASKS_URGENT_DAYS" => "-2" }).urgent_days
  end

  def test_urgent_days_empty_env_is_ignored
    write_config("urgent_days = 9\n")
    assert_equal 9, resolve(env: { "TASKS_URGENT_DAYS" => "" }).urgent_days
  end

  def test_for_dir_uses_default_urgent_days
    assert_equal 3, Tasks::Config.for_dir("/sandbox").urgent_days
  end

  # -- theme + colors (TUI appearance) ----------------------------------------

  def test_theme_defaults_with_no_colors
    paths = resolve
    assert_equal "default", paths.theme
    assert_equal({}, paths.colors)
  end

  def test_theme_and_colors_from_config_file
    write_config(<<~CONF)
      theme = mono
      color.accent = magenta
      color.link = underline #88aaff
    CONF
    paths = resolve
    assert_equal "mono", paths.theme
    assert_equal "config file", paths.sources[:theme]
    assert_equal({ "accent" => "magenta", "link" => "underline #88aaff" }, paths.colors)
  end

  def test_generated_theme_name_from_config_file
    write_config("theme = dracula\n")
    paths = resolve
    assert_equal "dracula", paths.theme
    assert_equal "config file", paths.sources[:theme]
  end

  def test_tasks_theme_env_beats_config_file
    write_config("theme = mono\n")
    paths = resolve(env: { "TASKS_THEME" => "default" })
    assert_equal "default", paths.theme
    assert_equal "TASKS_THEME env", paths.sources[:theme]
  end

  def test_no_color_env_selects_mono_unless_theme_is_explicit
    assert_equal "mono", resolve(env: { "NO_COLOR" => "1" }).theme
    write_config("theme = default\n")
    assert_equal "default", resolve(env: { "NO_COLOR" => "1" }).theme
  end

  def test_bare_color_dot_key_is_ignored
    write_config("color. = red\n")
    assert_equal({}, resolve.colors)
  end

  # -- for_dir (test sandboxing) ---------------------------------------------

  def test_for_dir_pins_both_files_ignoring_env_and_config
    write_config("dir = /from-file\n")
    paths = Tasks::Config.for_dir("/sandbox")
    assert_equal "/sandbox/gtd.org", paths.org
    assert_equal "/sandbox/archive.org", paths.archive
  end

  # -- CLI: tasks config, and end-to-end resolution ---------------------------

  def test_cli_config_reports_paths_and_sources
    Dir.mktmpdir do |dir|
      env = clean_env(@xdg).merge("TASKS_DIR" => dir)
      File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)
      out, _err, st = Open3.capture3(env, "ruby", BIN, "config", "--json")
      assert st.success?
      j = JSON.parse(out)
      assert_equal File.join(dir, "gtd.org"), j["org"]
      assert_equal "TASKS_DIR env", j["sources"]["org"]
      assert_equal 3, j["urgent_days"]
      assert_equal "default", j["sources"]["urgent_days"]
      assert_equal File.join(@xdg, "tasks", "config"), j["config_file"]
      assert_equal false, j["config_file_exists"]
    end
  end

  def test_cli_reads_tasks_from_config_file_dir
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)
      write_config("dir = #{dir}\n")
      out, _err, st = Open3.capture3(clean_env(@xdg), "ruby", BIN, "list", "-a", "--json")
      assert st.success?
      titles = JSON.parse(out).map { |i| i["title"] }
      assert_includes titles, "Water the plants"
    end
  end

  def test_cli_mutation_lands_in_configured_dir
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)
      write_config("dir = #{dir}\n")
      _out, _err, st = Open3.capture3(clean_env(@xdg), "ruby", BIN, "done", "Water the plants")
      assert st.success?
      org = File.join(dir, "gtd.org")
      assert_match(/DONE Water the plants/, File.read(org))
      assert Tasks::Check.check(org).ok?
    end
  end
end
