# frozen_string_literal: true

require_relative "quadrants"

module Tasks
  # Resolves where tasks.jsonl and archive.jsonl live, so the task data can sit
  # outside this repo. Precedence, highest first:
  #
  #   1. TASKS_FILE / TASKS_ARCHIVE env vars (per-file overrides; tests use these)
  #   2. TASKS_DIR env var (a directory holding tasks.jsonl + archive.jsonl)
  #   3. config file at $XDG_CONFIG_HOME/tasks/config (default ~/.config/tasks/
  #      config), `key = value` lines with keys dir / file / archive / urgent_days
  #   4. default_dir — the repo root, matching the original layout
  #
  # Every consumer (CLI, TUI) goes through Config.resolve so they can never
  # disagree about which files are live.
  #
  # Besides the file paths, resolve also carries `urgent_days` — the deadline
  # window that marks a task "urgent" in the Covey quadrants (env
  # TASKS_URGENT_DAYS > config `urgent_days` > Quadrants::DEFAULT_URGENT_DAYS).
  module Config
    PATH_KEYS = %w[dir file archive].freeze

    # Link settings live in two dotted namespaces (see Tasks::Links):
    #   link.<name>   = <url template with %s>  — shorthand: `jira:OPS-1` in a
    #                   body expands through the template
    #   system.<name> = <host>                  — classify that host as <name>
    #                   (self-hosted Jira/GitLab the built-ins can't know)
    # Names are constrained so a stray `foo.bar = x` line can't inject a
    # shorthand that then matches prose.
    LINK_NAME = /\A[a-z][a-z0-9_-]*\z/

    Paths = Struct.new(:org, :archive, :urgent_days, :links, :link_systems,
                       :sources, :config_file, keyword_init: true) do
      # Context block appended to an agent's system prompt so a headless harness
      # finds the CLI and the task files even when they live outside the repo.
      # Provider-agnostic — every backend (Claude CLI, Hermes, …) uses it.
      def agent_context(cli_root:)
        <<~CTX
          File locations for this run (absolute; use these, not relative paths):
          - tasks CLI: #{File.join(cli_root, "bin", "tasks")}
          - tasks.jsonl: #{org}
          - archive.jsonl: #{archive}
        CTX
      end
      # Deprecated alias kept for one release; prefer #agent_context.
      alias_method :claude_context, :agent_context
    end

    # Paths pinned to one directory, ignoring env and config file — for
    # sandboxes (tests) that must never touch the user's real task files.
    def self.for_dir(dir)
      Paths.new(
        org: File.join(dir, "tasks.jsonl"), archive: File.join(dir, "archive.jsonl"),
        urgent_days: Quadrants::DEFAULT_URGENT_DAYS,
        links: {}, link_systems: {},
        sources: { org: "pinned", archive: "pinned", urgent_days: "default" },
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
      urgent_days, urgent_source = pick_urgent_days(conf, env)

      Paths.new(
        org: org, archive: archive,
        urgent_days: urgent_days,
        links: conf.fetch(:links, {}), link_systems: conf.fetch(:link_systems, {}),
        sources: { org: org_source, archive: archive_source, urgent_days: urgent_source },
        config_file: file
      )
    end

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
    # and relative paths; scalar keys (urgent_days) parse as integers and an
    # invalid value is dropped so the caller falls back to its default.
    # Dotted link keys (`link.jira = …`, `system.gitlab = …`) collect into the
    # :links / :link_systems maps (symbol keys so they can't collide with the
    # flat string keys above).
    def self.parse_file(path)
      return {} unless File.file?(path)

      File.readlines(path, encoding: "UTF-8").each_with_object({}) do |line, conf|
        line = line.strip
        next if line.empty? || line.start_with?("#")

        key, _, value = line.partition("=")
        key = key.strip
        # Strip an inline comment: whitespace + "#" ends the value. Requiring
        # the whitespace keeps a "#" INSIDE a value intact (a URL anchor like
        # https://host/page#sec has no space before its #).
        value = value.strip.sub(/\s#.*\z/, "").strip
        next if value.empty?

        if PATH_KEYS.include?(key)
          conf[key] = File.expand_path(value)
        elsif key == "urgent_days" && (n = parse_days(value))
          conf[key] = n
        elsif (m = key.match(/\Alink\.(.+)\z/)) && m[1].match?(LINK_NAME)
          (conf[:links] ||= {})[m[1]] = value
        elsif (m = key.match(/\Asystem\.(.+)\z/)) && m[1].match?(LINK_NAME)
          (conf[:link_systems] ||= {})[m[1]] = value
        end
      end
    end
  end
end
