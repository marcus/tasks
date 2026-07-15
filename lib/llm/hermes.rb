# frozen_string_literal: true

require_relative "agent"
require "net/http"
require "uri"

module LLM
  class Agent
    # The Hermes agent (Nous Research) — an agentic CLI installed locally. It
    # runs tools and edits files autonomously, so it fits the Agent protocol as
    # just another spawn: a different binary and flags, the same contract.
    #
    # Hermes reads its model/endpoint from ~/.hermes/config.yaml. We pass -m and
    # --provider per-invocation so the switcher's selected model wins without
    # mutating the user's global Hermes config. Point it at a local Ollama model
    # via Hermes' own config (an "ollama-launch"/custom provider whose base_url
    # is http://127.0.0.1:11434/v1); this adapter only names the model+provider.
    #
    # Verified against Hermes v0.17.0 (2026-06). The CLI is an external contract,
    # not a stable API — re-check `hermes --help` when upgrading. Notably:
    #   -z / --oneshot PROMPT   one-shot, prints ONLY the final answer (sync CLI)
    #   chat -q / --query PROMPT single non-interactive query, streams the
    #                           transcript incl. tool previews (TUI); add -Q to
    #                           silence it, which we deliberately do NOT.
    #   -m / --model, --provider  model + inference provider overrides
    #   --yolo                  bypass approval prompts — required headless, the
    #                           analogue of claude's --dangerously-skip-permissions
    #   --accept-hooks          auto-approve config.yaml shell hooks (no TTY)
    # Start/pump/cancel (incl. process-group TERM) are inherited from Agent.
    class Hermes < Agent
      DEFAULT_OLLAMA_URL = "http://127.0.0.1:11434"
      # Hermes' conventional local-Ollama provider name; overridable via config,
      # or set empty to fall back to Hermes' own default provider.
      DEFAULT_INFERENCE_PROVIDER = "ollama-launch"

      def self.default_command = "hermes"

      def initialize(root:, system: nil, command: nil,
                     ollama_url: DEFAULT_OLLAMA_URL,
                     inference_provider: DEFAULT_INFERENCE_PROVIDER, **_opts)
        super(root: root, system: system, command: command)
        @ollama_url = ollama_url.to_s.empty? ? DEFAULT_OLLAMA_URL : ollama_url
        @inference_provider = inference_provider.to_s.strip
      end

      # Hermes has no --append-system-prompt, so prepend our context (TASK_AGENT.md +
      # file locations) to the prompt text. Hermes may also auto-inject any AGENTS.md
      # it finds in cwd; the data dir should not carry one — our injected copy is
      # authoritative about the contract and absolute file locations.
      def command(prompt, model:, stream: true)
        full = @system ? "#{@system}\n\n#{prompt}" : prompt
        args = stream ? [@command, "chat", "-q", full] : [@command, "-z", full]
        args += ["--model", model] unless model.to_s.empty?
        args += ["--provider", @inference_provider] unless @inference_provider.empty?
        args += ["--yolo", "--accept-hooks"]
        args
      end

      # Installed AND the model endpoint answers — an installed Hermes pointed at
      # a dead Ollama is still a dead end, so surface it as unavailable.
      def available?
        super && ollama_up?
      end

      private

      # Short timeouts: this runs synchronously from the TUI's submit path, so a
      # dead endpoint must fail fast rather than stall the event loop. /api/tags
      # answers instantly when Ollama is up (it just lists local models).
      def ollama_up?
        uri = URI.join(@ollama_url, "/api/tags")
        Net::HTTP.start(uri.host, uri.port, open_timeout: 0.5, read_timeout: 0.5) do |http|
          http.get(uri.request_uri).is_a?(Net::HTTPSuccess)
        end
      rescue StandardError
        false
      end
    end
  end
end
