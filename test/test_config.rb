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
    { "TASKS_FILE" => nil, "TASKS_ARCHIVE" => nil, "TASKS_DIR" => nil,
      "TASKS_URGENT_DAYS" => nil, "TASKS_MAX_DEPTH" => nil,
      "TASKS_THEME" => nil, "TASKS_TIMEZONE" => nil, "TASKS_TIME_FORMAT" => nil,
      "TZ" => nil, "NO_COLOR" => nil, "XDG_CONFIG_HOME" => xdg }
  end

  def resolve(env: {}, default: "/repo", hostname: -> { "test-host.local" })
    # Route the config file into a throwaway XDG dir unless the test sets one
    env = { "XDG_CONFIG_HOME" => @xdg, "TZ" => "Etc/UTC" }.merge(env)
    Tasks::Config.resolve(default_dir: default, env: env, hostname: hostname)
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
    assert_equal "/repo/tasks.jsonl", paths.org
    assert_equal "/repo/archive.jsonl", paths.archive
    assert_equal "/repo/agent-memory.md", paths.memory
    assert_equal({ org: "default", archive: "default", memory: "beside tasks.jsonl",
                   urgent_days: "default", max_depth: "default", theme: "default",
                   timezone: "TZ env", time_format: "default" }, paths.sources)
  end

  def test_tasks_dir_env_points_both_files
    paths = resolve(env: { "TASKS_DIR" => "/data" })
    assert_equal "/data/tasks.jsonl", paths.org
    assert_equal "/data/archive.jsonl", paths.archive
    assert_equal "TASKS_DIR env", paths.sources[:org]
  end

  def test_per_file_env_beats_tasks_dir
    paths = resolve(env: { "TASKS_DIR" => "/data", "TASKS_FILE" => "/elsewhere/my.jsonl" })
    assert_equal "/elsewhere/my.jsonl", paths.org
    assert_equal "TASKS_FILE env", paths.sources[:org]
    assert_equal "/data/archive.jsonl", paths.archive # archive still follows TASKS_DIR
  end

  def test_config_file_dir_key
    write_config("dir = /from-file\n")
    paths = resolve
    assert_equal "/from-file/tasks.jsonl", paths.org
    assert_equal "/from-file/archive.jsonl", paths.archive
    assert_equal "config file", paths.sources[:org]
  end

  def test_config_file_per_file_keys_beat_dir_key
    write_config(<<~CONF)
      dir = /from-file
      file = /special/tasks.jsonl
    CONF
    paths = resolve
    assert_equal "/special/tasks.jsonl", paths.org
    assert_equal "/from-file/archive.jsonl", paths.archive
  end

  def test_env_beats_config_file
    write_config("dir = /from-file\n")
    paths = resolve(env: { "TASKS_DIR" => "/from-env" })
    assert_equal "/from-env/tasks.jsonl", paths.org
  end

  def test_empty_env_values_are_ignored
    paths = resolve(env: { "TASKS_DIR" => "", "TASKS_FILE" => "" })
    assert_equal "/repo/tasks.jsonl", paths.org
  end

  # -- memory sidecar (agent-memory.md resolution) ----------------------------

  def test_memory_defaults_beside_resolved_tasks_file
    paths = resolve
    assert_equal "/repo/agent-memory.md", paths.memory
    assert_equal "beside tasks.jsonl", paths.sources[:memory]
  end

  def test_memory_follows_tasks_dir
    paths = resolve(env: { "TASKS_DIR" => "/data" })
    assert_equal "/data/agent-memory.md", paths.memory
  end

  def test_memory_follows_the_final_tasks_file_override_not_the_base_dir
    # A TASKS_FILE override must drag the sibling memory with it, even though
    # the archive still resolves from TASKS_DIR.
    paths = resolve(env: { "TASKS_DIR" => "/data", "TASKS_FILE" => "/elsewhere/my.jsonl" })
    assert_equal "/elsewhere/agent-memory.md", paths.memory
    assert_equal "/data/archive.jsonl", paths.archive
  end

  def test_memory_config_key_beats_sibling_default
    write_config("memory = /notes/defaults.md\n")
    paths = resolve
    assert_equal "/notes/defaults.md", paths.memory
    assert_equal "config file", paths.sources[:memory]
  end

  def test_memory_config_key_expands_tilde
    write_config("memory = ~/defaults.md\n")
    assert_equal File.join(Dir.home, "defaults.md"), resolve.memory
  end

  def test_tasks_memory_env_beats_config_key_and_sibling
    write_config("memory = /notes/defaults.md\n")
    paths = resolve(env: { "TASKS_MEMORY" => "/override/mem.md" })
    assert_equal "/override/mem.md", paths.memory
    assert_equal "TASKS_MEMORY env", paths.sources[:memory]
  end

  def test_tasks_memory_empty_env_is_ignored
    write_config("memory = /notes/defaults.md\n")
    assert_equal "/notes/defaults.md", resolve(env: { "TASKS_MEMORY" => "" }).memory
  end

  def test_for_dir_pins_memory_beside_the_sandbox
    paths = Tasks::Config.for_dir("/sandbox")
    assert_equal "/sandbox/agent-memory.md", paths.memory
    assert_equal "pinned", paths.sources[:memory]
  end

  # -- config file parsing ----------------------------------------------------

  def test_config_file_ignores_comments_blanks_and_unknown_keys
    write_config(<<~CONF)
      # where my tasks live
      dir = /data

      color = always
    CONF
    paths = resolve
    assert_equal "/data/tasks.jsonl", paths.org
  end

  def test_config_file_expands_tilde
    write_config("dir = ~/tasks\n")
    paths = resolve
    assert_equal File.join(Dir.home, "tasks", "tasks.jsonl"), paths.org
  end

  def test_missing_config_file_is_fine
    paths = resolve
    assert_equal "/repo/tasks.jsonl", paths.org
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

  # -- max_depth (the task-nesting depth cap) ---------------------------------

  def test_max_depth_defaults_to_four
    paths = resolve
    assert_equal 4, paths.max_depth
    assert_equal "default", paths.sources[:max_depth]
  end

  def test_max_depth_from_config_file
    write_config("max_depth = 6\n")
    paths = resolve
    assert_equal 6, paths.max_depth
    assert_equal "config file", paths.sources[:max_depth]
  end

  def test_max_depth_env_beats_config_file
    write_config("max_depth = 6\n")
    paths = resolve(env: { "TASKS_MAX_DEPTH" => "2" })
    assert_equal 2, paths.max_depth
    assert_equal "TASKS_MAX_DEPTH env", paths.sources[:max_depth]
  end

  def test_max_depth_zero_falls_back_to_default
    write_config("max_depth = 0\n")
    assert_equal 4, resolve.max_depth
  end

  def test_max_depth_negative_falls_back_to_default
    write_config("max_depth = -1\n")
    assert_equal 4, resolve.max_depth
  end

  def test_max_depth_non_numeric_falls_back_to_default
    write_config("max_depth = deep\n")
    assert_equal 4, resolve.max_depth
  end

  def test_max_depth_invalid_env_falls_back_to_config
    write_config("max_depth = 5\n")
    # a below-1 (or unparseable) env value is ignored, not fatal
    assert_equal 5, resolve(env: { "TASKS_MAX_DEPTH" => "0" }).max_depth
  end

  def test_max_depth_empty_env_is_ignored
    write_config("max_depth = 5\n")
    assert_equal 5, resolve(env: { "TASKS_MAX_DEPTH" => "" }).max_depth
  end

  def test_for_dir_uses_default_max_depth
    assert_equal 4, Tasks::Config.for_dir("/sandbox").max_depth
  end

  # -- temporal settings -----------------------------------------------------

  def test_timezone_resolution_precedence_and_time_format
    write_config("timezone = Europe/London\ntime_format = 24\n")
    configured = resolve(env: { "TZ" => "Asia/Tokyo" })
    assert_equal "Europe/London", configured.timezone
    assert_equal "config file", configured.sources[:timezone]
    assert_equal 24, configured.time_format

    overridden = resolve(env: { "TASKS_TIMEZONE" => "America/New_York",
                                "TASKS_TIME_FORMAT" => "12" })
    assert_equal "America/New_York", overridden.timezone
    assert_equal "TASKS_TIMEZONE env", overridden.sources[:timezone]
    assert_equal 12, overridden.time_format
  end

  def test_invalid_timezone_env_falls_through_to_config_zone_with_a_warning
    write_config("timezone = Europe/London\n")
    configured = nil
    _out, err = capture_io do
      configured = resolve(env: { "TASKS_TIMEZONE" => "Bogus/NotAZone" })
    end
    assert_equal "Europe/London", configured.timezone
    assert_equal "config file", configured.sources[:timezone]
    assert_match(/ignoring invalid time zone "Bogus\/NotAZone"/, err)
  end

  def test_timezone_uses_tz_and_detector_reports_utc_fallback
    assert_equal "Asia/Tokyo", resolve(env: { "TZ" => "Asia/Tokyo" }).timezone
    zone, source, warning = Tasks::Timezones.detect(env: {}, localtime: "/missing/localtime")
    assert_equal "Etc/UTC", zone
    assert_equal "UTC fallback", source
    assert warning
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

  # -- prompt facts (agent Current environment block) -------------------------

  def test_prompt_facts_default_datetime_and_hostname_on
    paths = resolve
    assert_equal({ "datetime" => true, "hostname" => true }, paths.prompt_facts)
  end

  def test_prompt_facts_from_config_file
    write_config(<<~CONF)
      prompt.datetime = off
      prompt.hostname = on
    CONF
    paths = resolve
    assert_equal({ "datetime" => false, "hostname" => true }, paths.prompt_facts)
  end

  def test_prompt_facts_unknown_name_ignored_at_resolve
    write_config("prompt.weather = on\nprompt.datetime = off\n")
    paths = resolve
    refute paths.prompt_facts.key?("weather")
    assert_equal false, paths.prompt_facts["datetime"]
    assert_equal true, paths.prompt_facts["hostname"]
  end

  def test_prompt_facts_invalid_toggle_falls_through_to_default
    write_config("prompt.datetime = maybe\n")
    assert_equal true, resolve.prompt_facts["datetime"]
  end

  def test_prompt_facts_bare_dot_key_ignored
    write_config("prompt. = on\n")
    assert_equal({ "datetime" => true, "hostname" => true }, resolve.prompt_facts)
  end

  def test_for_dir_uses_default_prompt_facts
    assert_equal({ "datetime" => true, "hostname" => true },
                 Tasks::Config.for_dir("/sandbox").prompt_facts)
  end

  # -- host-specific creation context ----------------------------------------

  def test_host_context_prefers_full_hostname_then_short_label
    write_config(<<~CONF)
      host_context.home-mac = home
      host_context.home-mac.local = @specific
    CONF

    full = resolve(hostname: -> { "HOME-MAC.LOCAL" })
    assert_equal "HOME-MAC.LOCAL", full.hostname
    assert_equal "@specific", full.host_context
    assert_equal "host_context.home-mac.local", full.host_context_source

    short = resolve(hostname: -> { "home-mac.example" })
    assert_equal "@home", short.host_context
    assert_equal "host_context.home-mac", short.host_context_source
  end

  def test_host_context_ignores_unmatched_and_malformed_rows
    write_config(<<~CONF)
      host_context.bad host = @bad
      host_context.bare = @
      host_context.office = work desk
      host_context.home = @home
    CONF

    paths = resolve(hostname: -> { "elsewhere.local" })
    assert_equal "elsewhere.local", paths.hostname
    assert_nil paths.host_context
    assert_nil paths.host_context_source
    assert_equal({ "home" => "@home" }, paths.host_contexts)
  end

  def test_for_dir_has_no_host_context_and_does_not_call_hostname
    paths = Tasks::Config.for_dir("/sandbox")
    assert_nil paths.hostname
    assert_nil paths.host_context
    assert_nil paths.host_context_source
    assert_equal({}, paths.host_contexts)
  end

  def test_cli_config_reports_prompt_facts
    Dir.mktmpdir do |dir|
      write_config("prompt.hostname = off\n")
      env = clean_env(@xdg).merge("TASKS_DIR" => dir)
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE)
      out, _err, st = Open3.capture3(env, "ruby", BIN, "config", "--json")
      assert st.success?
      j = JSON.parse(out)
      assert_equal({ "datetime" => true, "hostname" => false }, j["prompt_facts"])
    end
  end

  def test_cli_config_reports_resolved_host_context
    Dir.mktmpdir do |dir|
      hostname = Socket.gethostname
      write_config("host_context.#{hostname.downcase} = home\n")
      env = clean_env(@xdg).merge("TASKS_DIR" => dir)
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE)
      out, _err, st = Open3.capture3(env, "ruby", BIN, "config", "--json")
      assert st.success?
      j = JSON.parse(out)
      assert_equal hostname, j["hostname"]
      assert_equal "@home", j["host_context"]
      assert_equal "host_context.#{hostname.downcase}", j["host_context_source"]
    end
  end

  # -- for_dir (test sandboxing) ---------------------------------------------

  def test_for_dir_pins_both_files_ignoring_env_and_config
    write_config("dir = /from-file\n")
    paths = Tasks::Config.for_dir("/sandbox")
    assert_equal "/sandbox/tasks.jsonl", paths.org
    assert_equal "/sandbox/archive.jsonl", paths.archive
  end

  # -- CLI: tasks config, and end-to-end resolution ---------------------------

  def test_cli_config_reports_paths_and_sources
    Dir.mktmpdir do |dir|
      env = clean_env(@xdg).merge("TASKS_DIR" => dir)
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE)
      out, _err, st = Open3.capture3(env, "ruby", BIN, "config", "--json")
      assert st.success?
      j = JSON.parse(out)
      assert_equal File.join(dir, "tasks.jsonl"), j["org"]
      assert_equal "TASKS_DIR env", j["sources"]["org"]
      assert_equal 3, j["urgent_days"]
      assert_equal "default", j["sources"]["urgent_days"]
      assert_equal 4, j["max_depth"]
      assert_equal "default", j["sources"]["max_depth"]
      assert_equal File.join(dir, "agent-memory.md"), j["memory"]
      assert_equal "beside tasks.jsonl", j["sources"]["memory"]
      assert_equal false, j["memory_exists"]
      assert_equal File.join(@xdg, "tasks", "config"), j["config_file"]
      assert_equal false, j["config_file_exists"]
    end
  end

  def test_cli_config_reports_memory_from_tasks_file_sibling_and_existence
    Dir.mktmpdir do |dir|
      tasks_file = File.join(dir, "tasks.jsonl")
      File.write(tasks_file, FIXTURE)
      File.write(File.join(dir, "agent-memory.md"), "# defaults\n")
      env = clean_env(@xdg).merge("TASKS_FILE" => tasks_file,
                                  "TASKS_ARCHIVE" => File.join(dir, "archive.jsonl"))
      out, _err, st = Open3.capture3(env, "ruby", BIN, "config", "--json")
      assert st.success?
      j = JSON.parse(out)
      assert_equal File.join(dir, "agent-memory.md"), j["memory"]
      assert_equal "beside tasks.jsonl", j["sources"]["memory"]
      assert_equal true, j["memory_exists"]
    end
  end

  def test_cli_reads_tasks_from_config_file_dir
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE)
      write_config("dir = #{dir}\n")
      out, _err, st = Open3.capture3(clean_env(@xdg), "ruby", BIN, "list", "-a", "--json")
      assert st.success?
      titles = JSON.parse(out).map { |i| i["title"] }
      assert_includes titles, "Water the plants"
    end
  end

  def test_cli_mutation_lands_in_configured_dir
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE)
      write_config("dir = #{dir}\n")
      _out, _err, st = Open3.capture3(clean_env(@xdg), "ruby", BIN, "done", "Water the plants")
      assert st.success?
      org = File.join(dir, "tasks.jsonl")
      assert_equal "DONE", record_for(org, title: "Water the plants")["state"]
      assert Tasks::Check.check(org).ok?
    end
  end
end
