# frozen_string_literal: true

module Tasks
  # Resolves where gtd.org and archive.org live, so the task data can sit
  # outside this repo. Precedence, highest first:
  #
  #   1. TASKS_ORG / TASKS_ARCHIVE env vars (per-file overrides; tests use these)
  #   2. TASKS_DIR env var (a directory holding gtd.org + archive.org)
  #   3. config file at $XDG_CONFIG_HOME/tasks/config (default ~/.config/tasks/
  #      config), `key = value` lines with keys dir / org / archive
  #   4. default_dir — the repo root, matching the original layout
  #
  # Every consumer (CLI, TUI) goes through Config.resolve so they can never
  # disagree about which files are live.
  module Config
    Paths = Struct.new(:org, :archive, :sources, :config_file, keyword_init: true) do
      # Context block appended to Claude's system prompt so a headless agent
      # finds the CLI and the task files even when they live outside the repo.
      def claude_context(cli_root:)
        <<~CTX
          File locations for this run (absolute; use these, not relative paths):
          - tasks CLI: #{File.join(cli_root, "bin", "tasks")}
          - gtd.org: #{org}
          - archive.org: #{archive}
        CTX
      end
    end

    # Paths pinned to one directory, ignoring env and config file — for
    # sandboxes (tests) that must never touch the user's real task files.
    def self.for_dir(dir)
      Paths.new(
        org: File.join(dir, "gtd.org"), archive: File.join(dir, "archive.org"),
        sources: { org: "pinned", archive: "pinned" },
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

      org,     org_source     = pick("gtd.org",     "TASKS_ORG",     dir, dir_source, conf["org"],     env)
      archive, archive_source = pick("archive.org", "TASKS_ARCHIVE", dir, dir_source, conf["archive"], env)

      Paths.new(
        org: org, archive: archive,
        sources: { org: org_source, archive: archive_source },
        config_file: file
      )
    end

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
      base = env["XDG_CONFIG_HOME"]
      base = File.join(Dir.home, ".config") if base.nil? || base.empty?
      File.join(base, "tasks", "config")
    end

    # `key = value` per line; `#` comments and blanks ignored; unknown keys
    # ignored (forward compatibility). Values expand `~` and relative paths.
    def self.parse_file(path)
      return {} unless File.file?(path)

      File.readlines(path, encoding: "UTF-8").each_with_object({}) do |line, conf|
        line = line.strip
        next if line.empty? || line.start_with?("#")

        key, _, value = line.partition("=")
        key = key.strip
        value = value.strip
        next unless %w[dir org archive].include?(key) && !value.empty?

        conf[key] = File.expand_path(value)
      end
    end
  end
end
