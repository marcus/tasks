# frozen_string_literal: true

require_relative "agent"

module LLM
  class Agent
    # The local `claude` CLI, run headless with -p. This is the original
    # Tui::Claude spawn logic moved onto the Agent protocol with zero behavior
    # change. `claude -p --output-format text` streams the assistant's text the
    # same way whether or not we want a transcript, so the `stream:` hint is a
    # no-op here — it only matters for harnesses with distinct one-shot vs.
    # transcript modes (see Hermes).
    class ClaudeCli < Agent
      def self.default_command = "claude"

      def command(prompt, model:, stream: true)
        # --dangerously-skip-permissions: a headless run can't answer permission
        # prompts, and the whole point is letting the agent edit gtd.org freely.
        args = [@command, "-p", prompt, "--model", model,
                "--output-format", "text", "--dangerously-skip-permissions"]
        args += ["--append-system-prompt", @system] if @system
        args
      end
    end
  end
end
