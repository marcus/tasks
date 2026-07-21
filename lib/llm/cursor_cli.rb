# frozen_string_literal: true

require_relative "agent"

module LLM
  class Agent
    # Cursor's local `agent` CLI, run headlessly in print mode. The CLI has no
    # system-prompt flag, so the shared TASK_AGENT.md context is prepended to the
    # user's request, as it is for Hermes.
    #
    # Text output contains the final assistant message rather than structured
    # tool progress. Both the TUI and synchronous CLI therefore use the same
    # invocation and inherit Agent's raw output pumping and process-group
    # cancellation unchanged.
    #
    # Verified against Cursor Agent 2026.07.09-a3815c0. This is an external CLI
    # contract; re-check `agent --help` when upgrading.
    class CursorCli < Agent
      def self.default_command = "agent"

      def command(prompt, model:, stream: true)
        full = @system ? "#{@system}\n\n#{prompt}" : prompt
        args = [@command, "-p", "--force", "--output-format", "text"]
        args += ["--model", model] unless model.to_s.empty?
        args << full
      end
    end
  end
end
