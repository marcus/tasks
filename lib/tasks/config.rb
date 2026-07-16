# frozen_string_literal: true

require_relative "quadrants"
require_relative "tree"
require_relative "prompt_facts"
require_relative "timezones"

module Tasks
  # Resolves where tasks.jsonl and archive.jsonl live, so the task data can sit
  # outside this repo. Precedence, highest first:
  #
  #   1. TASKS_FILE / TASKS_ARCHIVE env vars (per-file overrides; tests use these)
  #   2. TASKS_DIR env var (a directory holding tasks.jsonl + archive.jsonl)
  #   3. config file at $XDG_CONFIG_HOME/tasks/config (default ~/.config/tasks/
  #      config), `key = value` lines with keys dir / file / archive /
  #      urgent_days / max_depth / theme / color.<slot> / prompt.<fact>
  #   4. default_dir — the repo root, matching the original layout
  #
  # Every consumer (CLI, TUI) goes through Config.resolve so they can never
  # disagree about which files are live.
  #
  # Besides the file paths, resolve also carries `urgent_days` — the deadline
  # window that marks a task "urgent" in the Covey quadrants (env
  # TASKS_URGENT_DAYS > config `urgent_days` > Quadrants::DEFAULT_URGENT_DAYS).
  module Config
    PATH_KEYS = %w[dir file archive memory].freeze

    # Link settings live in two dotted namespaces (see Tasks::Links):
    #   link.<name>   = <url template with %s>  — shorthand: `jira:OPS-1` in a
    #                   body expands through the template
    #   system.<name> = <host>                  — classify that host as <name>
    #                   (self-hosted Jira/GitLab the built-ins can't know)
    # Names are constrained so a stray `foo.bar = x` line can't inject a
    # shorthand that then matches prose.
    LINK_NAME = /\A[a-z][a-z0-9_-]*\z/

    Paths = Struct.new(:org, :archive, :memory, :urgent_days, :max_depth, :theme, :colors,
                       :timezone, :time_format, :timezone_fallback_warning,
                       :links, :link_systems, :prompt_facts,
                       :sources, :config_file, keyword_init: true) do
      # Context block appended to an agent's system prompt so a headless harness
      # finds the CLI and the task files even when they live outside the repo.
      # Provider-agnostic — every backend (Claude CLI, Hermes, …) uses it.
      # The memory sidecar path is always listed (even when the file does not
      # exist yet) so an agent can create or edit it without guessing.
      def agent_context(cli_root:)
        <<~CTX
          File locations for this run (absolute; use these, not relative paths):
          - tasks CLI: #{File.join(cli_root, "bin", "tasks")}
          - tasks.jsonl: #{org}
          - archive.jsonl: #{archive}
          - agent-memory.md: #{memory}
        CTX
      end
    end

    # Paths pinned to one directory, ignoring env and config file — for
    # sandboxes (tests) that must never touch the user's real task files.
    def self.for_dir(dir)
      Paths.new(
        org: File.join(dir, "tasks.jsonl"), archive: File.join(dir, "archive.jsonl"),
        memory: File.join(dir, "agent-memory.md"),
        urgent_days: Quadrants::DEFAULT_URGENT_DAYS,
        max_depth: Tree::DEFAULT_MAX_DEPTH,
        theme: "default", colors: {},
        timezone: Timezones::FALLBACK, time_format: 12, timezone_fallback_warning: false,
        links: {}, link_systems: {},
        prompt_facts: PromptFacts.resolve,
        sources: { org: "pinned", archive: "pinned", memory: "pinned", urgent_days: "default",
                   max_depth: "default", theme: "default", timezone: "pinned", time_format: "default" },
        config_file: config_file
      )
    end

    def self.resolve(default_dir:, env: ENV)
      file = config_file(env)
      conf = parse_file(file)

      dir, dir_source =
        if env["TASKS_DIR"] && !env["TASKS_DIR"].empty?
          [env["TASKS_DIR"], "TASKS_DIR env"]
        elsif conf["dir"]
          [conf["dir"], "config file"]
        else
          [default_dir, "default"]
        end

      org,     org_source     = pick("tasks.jsonl",   "TASKS_FILE",    dir, dir_source, conf["file"],    env)
      archive, archive_source = pick("archive.jsonl", "TASKS_ARCHIVE", dir, dir_source, conf["archive"], env)
      # Memory derives from the FINAL org path, not the base dir: a TASKS_FILE
      # override must select its sibling agent-memory.md even when the dir/
      # archive come from elsewhere. That's why it can't reuse `pick`, which
      # defaults from the directory rather than the resolved file.
      memory, memory_source = pick_memory(org, conf["memory"], env)
      urgent_days, urgent_source = pick_urgent_days(conf, env)
      max_depth,   max_depth_source = pick_max_depth(conf, env)
      theme, theme_source = pick_theme(conf, env)
      timezone, timezone_source, timezone_warning = pick_timezone(conf, env)
      time_format, time_format_source = pick_time_format(conf, env)

      Paths.new(
        org: org, archive: archive, memory: memory,
        urgent_days: urgent_days, max_depth: max_depth,
        theme: theme, colors: conf["colors"] || {},
        timezone: timezone, time_format: time_format, timezone_fallback_warning: timezone_warning,
        links: conf.fetch(:links, {}), link_systems: conf.fetch(:link_systems, {}),
        prompt_facts: PromptFacts.resolve(conf.fetch(:prompt_facts, {})),
        sources: { org: org_source, archive: archive_source, memory: memory_source,
                   urgent_days: urgent_source, max_depth: max_depth_source, theme: theme_source,
                   timezone: timezone_source, time_format: time_format_source },
        config_file: file
      )
    end

    # Resolve the agent-memory.md sidecar: TASKS_MEMORY env beats a config
    # `memory` key beats agent-memory.md beside the resolved tasks.jsonl. The
    # config value already carries `~`/relative expansion (memory is a PATH_KEY).
    def self.pick_memory(org, file_value, env)
      if env["TASKS_MEMORY"] && !env["TASKS_MEMORY"].empty?
        [File.expand_path(env["TASKS_MEMORY"]), "TASKS_MEMORY env"]
      elsif file_value
        [file_value, "config file"]
      else
        [File.expand_path(File.join(File.dirname(org), "agent-memory.md")), "beside tasks.jsonl"]
      end
    end
    private_class_method :pick_memory

    # TUI theme name. Env beats config file; NO_COLOR (when nothing explicit
    # is set) selects the attribute-only theme. Tui::Theme validates the name
    # and falls back to the default look for anything it doesn't know.
    def self.pick_theme(conf, env)
      if env["TASKS_THEME"] && !env["TASKS_THEME"].empty?
        [env["TASKS_THEME"], "TASKS_THEME env"]
      elsif conf["theme"]
        [conf["theme"], "config file"]
      elsif env["NO_COLOR"] && !env["NO_COLOR"].empty?
        ["mono", "NO_COLOR env"]
      else
        ["default", "default"]
      end
    end
    private_class_method :pick_theme

    # Each source falls through independently on an invalid zone: a typo'd
    # TASKS_TIMEZONE must not silently skip past a valid config-file zone.
    def self.pick_timezone(conf, env)
      candidates = [
        [env["TASKS_TIMEZONE"], "TASKS_TIMEZONE env"],
        [conf["timezone"], "config file"],
      ]
      candidates.each do |value, source|
        next if value.nil? || value.empty?

        begin
          return [Timezones.get(value).identifier, source, false]
        rescue Timezones::Error
          warn "tasks: ignoring invalid time zone #{value.inspect} from #{source}"
        end
      end
      Timezones.detect(env: env)
    end
    private_class_method :pick_timezone

    def self.pick_time_format(conf, env)
      raw = env["TASKS_TIME_FORMAT"]
      return [raw.to_i, "TASKS_TIME_FORMAT env"] if %w[12 24].include?(raw)
      return [conf["time_format"], "config file"] if conf.key?("time_format")
      [12, "default"]
    end
    private_class_method :pick_time_format

    # Deadline window (in days) for the "urgent" axis. Env beats config file
    # beats the built-in default; an empty or non-integer value is ignored so
    # a typo falls back rather than crashing.
    def self.pick_urgent_days(conf, env)
      env_val = env["TASKS_URGENT_DAYS"]
      if env_val && !env_val.empty? && (n = parse_days(env_val))
        [n, "TASKS_URGENT_DAYS env"]
      elsif conf.key?("urgent_days")
        [conf["urgent_days"], "config file"]
      else
        [Quadrants::DEFAULT_URGENT_DAYS, "default"]
      end
    end
    private_class_method :pick_urgent_days

    # Parse a non-negative integer day count, or nil if the value is unusable.
    def self.parse_days(str)
      n = Integer(str, 10)
      n.negative? ? nil : n
    rescue ArgumentError, TypeError
      nil
    end
    private_class_method :parse_days

    # Task-nesting depth cap. Env beats config file beats the built-in default;
    # a value below 1 (0 would forbid all tasks) or non-integer is ignored so a
    # typo falls through to the next source rather than crashing.
    def self.pick_max_depth(conf, env)
      env_val = env["TASKS_MAX_DEPTH"]
      if env_val && !env_val.empty? && (n = parse_depth(env_val))
        [n, "TASKS_MAX_DEPTH env"]
      elsif conf.key?("max_depth")
        [conf["max_depth"], "config file"]
      else
        [Tree::DEFAULT_MAX_DEPTH, "default"]
      end
    end
    private_class_method :pick_max_depth

    # Parse a positive integer (≥ 1) depth, or nil if the value is unusable.
    def self.parse_depth(str)
      n = Integer(str, 10)
      n < 1 ? nil : n
    rescue ArgumentError, TypeError
      nil
    end
    private_class_method :parse_depth

    def self.pick(basename, env_key, dir, dir_source, file_value, env)
      if env[env_key] && !env[env_key].empty?
        [File.expand_path(env[env_key]), "#{env_key} env"]
      elsif file_value
        [file_value, "config file"]
      else
        [File.expand_path(File.join(dir, basename)), dir_source]
      end
    end
    private_class_method :pick

    def self.config_file(env = ENV)
      File.join(xdg_base("XDG_CONFIG_HOME", ".config", env: env), "tasks", "config")
    end

    # Resolve an XDG base directory, falling back to ~/<default...> when the env
    # var is unset or empty, and expanding either form to an absolute path — a
    # relative XDG value would otherwise resolve against each process's cwd, so
    # two invocations from different directories would disagree on where state
    # lives. Shared by every "where does tasks state live" decision.
    def self.xdg_base(env_key, *default, env: ENV)
      base = env[env_key]
      base = File.join(Dir.home, *default) if base.nil? || base.empty?
      File.expand_path(base)
    end

    # `key = value` per line; `#` comments and blanks ignored; unknown keys
    # ignored (forward compatibility). Path keys (dir/file/archive) expand `~`
    # and relative paths; scalar keys (urgent_days, max_depth) parse as integers
    # and an invalid value is dropped so the caller falls back to its default.
    # TUI appearance: `theme = <name>` and `color.<slot> = <spec>` lines are
    # collected verbatim (specs under conf["colors"]) — Tui::Theme owns
    # validation so the vocabulary lives in one place.
    # Dotted link keys (`link.jira = …`, `system.gitlab = …`) collect into the
    # :links / :link_systems maps (symbol keys so they can't collide with the
    # flat string keys above). `prompt.<fact> = on|off` toggles agent prompt
    # facts (see PromptFacts); unknown names are kept for forward compatibility
    # but only registered facts affect the resolved map.
    def self.parse_file(path)
      return {} unless File.file?(path)

      File.readlines(path, encoding: "UTF-8").each_with_object({}) do |line, conf|
        line = line.strip
        next if line.empty? || line.start_with?("#")

        key, _, value = line.partition("=")
        key = key.strip
        # Strip an inline comment: whitespace + "#" ends the value. Requiring
        # the whitespace keeps a "#" INSIDE a value intact (a URL anchor like
        # https://host/page#sec has no space before its #). color.* specs are
        # exempt — a hex token (`bold #ff8800`) legitimately follows a space,
        # so color lines can't carry inline comments.
        value = value.strip
        value = value.sub(/\s#.*\z/, "").strip unless key.start_with?("color.")
        next if value.empty?

        if PATH_KEYS.include?(key)
          conf[key] = File.expand_path(value)
        elsif key == "urgent_days" && (n = parse_days(value))
          conf[key] = n
        elsif key == "max_depth" && (n = parse_depth(value))
          conf[key] = n
        elsif key == "theme"
          conf[key] = value
        elsif key == "timezone"
          begin
            conf[key] = Timezones.get(value).identifier
          rescue Timezones::Error
            next
          end
        elsif key == "time_format" && %w[12 24].include?(value)
          conf[key] = value.to_i
        elsif key.start_with?("color.") && key.length > 6
          (conf["colors"] ||= {})[key.delete_prefix("color.")] = value
        elsif (m = key.match(/\Alink\.(.+)\z/)) && m[1].match?(LINK_NAME)
          (conf[:links] ||= {})[m[1]] = value
        elsif (m = key.match(/\Asystem\.(.+)\z/)) && m[1].match?(LINK_NAME)
          (conf[:link_systems] ||= {})[m[1]] = value
        elsif (m = key.match(/\Aprompt\.(.+)\z/)) && m[1].match?(PromptFacts::FACT_NAME)
          toggle = PromptFacts.parse_toggle(value)
          (conf[:prompt_facts] ||= {})[m[1]] = toggle unless toggle.nil?
        end
      end
    end
  end
end
