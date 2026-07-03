# frozen_string_literal: true

require_relative "claude_cli"
require_relative "hermes"
require_relative "config"

module LLM
  # A single (provider, model) choice — what the TUI switcher cycles and the CLI
  # resolves a default for. Because every backend is an agent, there is no
  # `kind`/`transport` branch here: an Entry only names which harness + model.
  Entry = Struct.new(:provider, :model, keyword_init: true) do
    def to_s = "#{provider}:#{model}"
    def ==(other) = other.is_a?(Entry) && provider == other.provider && model == other.model
    alias_method :eql?, :==
    def hash = [provider, model].hash
  end

  # Maps a provider name to its adapter class + model list, assembled from
  # built-in defaults overlaid with the user's config. Adding a harness is a
  # two-line change: an adapter class + a DEFAULTS entry. Config can then tune a
  # provider's model list or binary without any code change — the growth path as
  # the harness/model landscape shifts.
  module Registry
    Spec = Struct.new(:provider, :adapter, :models, :transport, :settings, keyword_init: true)

    # `transport` is informational (for optional-dependency handling), never a
    # call-site branch. `settings` become adapter constructor kwargs.
    # The overall default is the first provider's first model — claude-cli:sonnet
    # — unchanged, because no local model is fast/reliable enough to default to
    # (see eval/llm/results-2026-07-02.md). Within Hermes, the default model is
    # qwen3.6:35b-a3b: the one local model that reliably drove the CLI in the
    # eval (0 corruptions, all 8 task dimensions), replacing gemma4:e4b, which
    # derailed. gemma4:e4b is kept as a lighter/faster fallback in the switcher.
    DEFAULTS = {
      "claude-cli" => { adapter: Agent::ClaudeCli, transport: :cli,
                        models: %w[sonnet opus haiku], settings: {} },
      "hermes"     => { adapter: Agent::Hermes,     transport: :cli,
                        models: %w[qwen3.6:35b-a3b gemma4:e4b], settings: {} },
    }.freeze

    def self.build(config = Config.load)
      DEFAULTS.each_with_object({}) do |(name, base), reg|
        over = config.provider_settings(name)
        models = over[:models]&.any? ? over[:models] : base[:models]
        settings = base[:settings].dup
        settings[:command] = over[:command] if over[:command]
        settings[:ollama_url] = over[:ollama_url] if over[:ollama_url]
        settings[:inference_provider] = over[:inference_provider] if over[:inference_provider]
        reg[name] = Spec.new(provider: name, adapter: base[:adapter],
                             transport: base[:transport], models: models, settings: settings)
      end
    end
  end

  module_function

  # provider name => Registry::Spec, built from config.
  def registry(config = Config.load) = Registry.build(config)

  # Flat, ordered (provider, model) list for the switcher. The resolved default
  # is moved to the front so cycling starts there — out of the box that's
  # claude-cli:sonnet, so nothing changes for current users.
  def entries(config = Config.load)
    reg = registry(config)
    all = reg.values.flat_map { |s| s.models.map { |m| Entry.new(provider: s.provider, model: m) } }
    all.unshift(default_entry(config: config, reg: reg))
    all.uniq # Entry#eql?/#hash dedupe; unshifted default keeps front position
  end

  # The starting (provider, model). Explicit args (e.g. CLI --provider/--model)
  # win, then config, then the first registered provider / its first model. A
  # model given explicitly is honored even if not in the provider's list, so a
  # user can run any model their harness supports without editing config.
  # config.model only applies when the provider wasn't explicitly overridden —
  # it's paired with config.provider, so `--provider hermes` alone resolves to
  # hermes's own default model, not a claude tier left in config.
  def default_entry(provider: nil, model: nil, config: Config.load, reg: nil)
    reg ||= registry(config)
    explicit = nonblank(provider)
    # An explicit provider (a CLI --provider flag) that isn't registered is a
    # user error — reject it rather than silently running the default backend.
    # A stale config provider falls back quietly so a typo can't brick the tool.
    if explicit && !reg.key?(explicit)
      raise ArgumentError, "unknown LLM provider: #{explicit.inspect} (known: #{reg.keys.join(", ")})"
    end

    pname = explicit || valid_key(config.provider, reg) || reg.keys.first
    spec = reg.fetch(pname)
    mname = nonblank(model)
    mname ||= nonblank(config.model) if explicit.nil? # config.model pairs with config.provider
    mname ||= spec.models.first
    Entry.new(provider: pname, model: mname)
  end

  def valid_key(name, reg)
    (k = nonblank(name)) && reg.key?(k) ? k : nil
  end

  def nonblank(str) = (s = str.to_s.strip).empty? ? nil : s

  # Instantiate the adapter for an entry, ready to #start / #run_sync. The model
  # rides along at call time; construction only needs root + system + settings.
  def build(entry, root:, system: nil, config: Config.load)
    spec = registry(config).fetch(entry.provider) do
      raise ArgumentError, "unknown LLM provider: #{entry.provider.inspect}"
    end
    spec.adapter.new(root: root, system: system, **spec.settings)
  end
end
