# frozen_string_literal: true

require "shellwords"

module Tasks
  # Opens a URL in the user's browser — the one place that knows how, shared by
  # the CLI (`tasks open`) and the TUI (`o`). TASKS_OPENER overrides the
  # platform launcher (also how tests observe opens without a browser); it is
  # shell-split, so `TASKS_OPENER="open -a Safari"` works.
  module Opener
    module_function

    # The launcher argv (the URL is appended by open_url).
    def command(env: ENV)
      override = env["TASKS_OPENER"]
      return Shellwords.split(override) if override && !override.empty?
      case RUBY_PLATFORM
      when /darwin/ then ["open"]
      else               ["xdg-open"]
      end
    end

    # Launch detached; returns true if the launcher could be spawned. Output is
    # discarded — a TUI must not have a browser's stderr scribbled over it.
    # Any spawn failure (missing launcher, not executable, …) returns false
    # rather than raising: a bad TASKS_OPENER must not crash the TUI on `o`.
    def open_url(url, env: ENV)
      pid = Process.spawn(*command(env: env), url,
                          out: File::NULL, err: File::NULL)
      Process.detach(pid)
      true
    rescue SystemCallError, ArgumentError
      false
    end
  end
end
