# frozen_string_literal: true

require_relative "../tasks/config"

module LLM
  # LLM settings read from the same flat `key = value` file the task paths use
  # (~/.config/tasks/config). Every key is optional; unknown keys are ignored
  # (forward-compatible), so old configs keep working and a headless CLI that
  # never touches the LLM pays nothing. Recognized keys:
  #
  #   llm_provider = hermes                 default harness for `-p` and the TUI
  #   llm_model    = gemma4:e4b             default model within that provider
  #   <provider>_models  = a, b, c          override a provider's model list
  #   <provider>_command = /path/to/binary  override the binary a provider spawns
  #   hermes_provider = ollama-launch       Hermes inference provider (--provider)
  #   ollama_url  = http://127.0.0.1:11434  endpoint for Hermes' availability probe
  #
  # `<provider>` is any registry key, e.g. `claude-cli_models` or `hermes_models`.
  Config = Struct.new(:provider, :model, :providers, keyword_init: true) do
    # Per-provider overrides ({ models:, command:, ollama_url:, ... }), or {}.
    def provider_settings(name) = providers[name] || {}
  end

  def Config.load(env: ENV, path: nil)
    path ||= Tasks::Config.config_file(env)
    raw = read_raw(path)

    providers = Hash.new { |h, k| h[k] = {} }
    raw.each do |key, val|
      if (name = key[/\A(.+)_models\z/, 1])
        providers[name][:models] = split_csv(val)
      elsif (name = key[/\A(.+)_command\z/, 1])
        providers[name][:command] = val
      end
    end
    providers["hermes"][:inference_provider] = raw["hermes_provider"] if raw.key?("hermes_provider")
    providers["hermes"][:ollama_url] = raw["ollama_url"] if raw.key?("ollama_url")

    new(provider: presence(raw["llm_provider"]),
        model: presence(raw["llm_model"]),
        providers: providers)
  end

  def Config.read_raw(path)
    return {} unless File.file?(path)

    File.readlines(path, encoding: "UTF-8").each_with_object({}) do |line, h|
      line = line.strip
      next if line.empty? || line.start_with?("#")

      key, _, val = line.partition("=")
      key = key.strip
      val = val.strip
      h[key] = val unless val.empty?
    end
  end

  def Config.split_csv(val) = val.split(",").map(&:strip).reject(&:empty?)

  def Config.presence(str) = str.to_s.strip.empty? ? nil : str.strip
end
