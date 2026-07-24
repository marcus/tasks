# frozen_string_literal: true

require "fileutils"
require "json"
require_relative "../tasks/atomic"
require_relative "../tasks/config"

module Tui
  # Persists small bits of TUI state across runs — the active view, collapsed
  # set, panel mode/offset, and context filters — so the TUI reopens where you
  # left it. Deliberately dumb: a flat, versioned JSON hash at
  # $XDG_STATE_HOME/tasks/tui.json. UI preference, not task data — so one file
  # per user (not per org file), and losing it costs nothing but a default.
  #
  # Every read tolerates a missing/corrupt/foreign-version file by returning
  # {} — session state must never be able to keep the TUI from starting.
  module Session
    VERSION = 1

    module_function

    def path(env: ENV)
      File.join(Tasks::Config.xdg_base("XDG_STATE_HOME", ".local", "state", env: env),
                "tasks", "tui.json")
    end

    # The saved state as a symbol-keyed hash ({} when absent/unusable).
    def load(env: ENV)
      data = JSON.parse(File.read(path(env: env), encoding: "UTF-8"), symbolize_names: true)
      return {} unless data.is_a?(Hash) && data[:version] == VERSION
      data.except(:version)
    rescue Errno::ENOENT, JSON::ParserError
      {}
    end

    # Overwrite the saved state. Values should be JSON scalars (strings —
    # symbols come back from load as strings inside values, so callers store
    # strings). Best-effort: a read-only state dir must not crash TUI exit.
    def save(state, env: ENV)
      # The version stamp is ours alone — a state key named "version" would
      # emit a duplicate JSON key and poison every future load.
      state = state.reject { |k, _| k.to_s == "version" }
      file = path(env: env)
      FileUtils.mkdir_p(File.dirname(file))
      Tasks::Atomic.write(file, JSON.pretty_generate({ version: VERSION }.merge(state)))
      true
    rescue SystemCallError
      false
    end
  end
end
