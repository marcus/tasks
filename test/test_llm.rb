# frozen_string_literal: true

require_relative "test_helper"
require "tmpdir"
require "llm/registry"

# The adapter layer: one agent protocol, config-assembled registry, and the two
# built-in harnesses. No test spawns a real `claude`/`hermes` or hits a live
# model server — command construction and resolution are pure, and availability
# probes are pointed at a dead port so they fail fast.
class TestLLM < Minitest::Test
  A = LLM::Agent

  # -- ClaudeCli adapter -----------------------------------------------------

  def test_claude_command_includes_model_and_permissions_flag
    agent = A::ClaudeCli.new(root: "/tmp")
    cmd = agent.command("do the thing", model: "opus")
    assert_equal %w[claude -p], cmd[0, 2]
    assert_includes cmd, "do the thing"
    assert_equal "opus", cmd[cmd.index("--model") + 1]
    assert_includes cmd, "--dangerously-skip-permissions"
    refute_includes cmd, "--append-system-prompt" # no system context given
  end

  def test_claude_command_appends_system_prompt_when_present
    agent = A::ClaudeCli.new(root: "/tmp", system: "conventions here")
    cmd = agent.command("x", model: "sonnet")
    assert_equal "conventions here", cmd[cmd.index("--append-system-prompt") + 1]
  end

  def test_command_override_changes_binary
    agent = A::ClaudeCli.new(root: "/tmp", command: "/opt/claude")
    assert_equal "/opt/claude", agent.command("x", model: "sonnet").first
  end

  # -- Hermes adapter --------------------------------------------------------

  def test_hermes_stream_uses_chat_query_and_prepends_system
    agent = A::Hermes.new(root: "/tmp", system: "SYS")
    cmd = agent.command("hello", model: "gemma4:e4b", stream: true)
    assert_equal %w[hermes chat -q], cmd[0, 3]
    assert_equal "SYS\n\nhello", cmd[3] # no --append-system-prompt flag exists
    assert_equal "gemma4:e4b", cmd[cmd.index("--model") + 1]
    assert_equal "ollama-launch", cmd[cmd.index("--provider") + 1]
    assert_includes cmd, "--yolo"           # required for headless edits
    assert_includes cmd, "--accept-hooks"
  end

  def test_hermes_sync_uses_oneshot
    agent = A::Hermes.new(root: "/tmp")
    cmd = agent.command("hello", model: "gemma4:e4b", stream: false)
    assert_equal %w[hermes -z], cmd[0, 2]
    assert_equal "hello", cmd[2]
  end

  def test_hermes_inference_provider_omitted_when_blank
    agent = A::Hermes.new(root: "/tmp", inference_provider: "")
    refute_includes agent.command("x", model: "m"), "--provider"
  end

  def test_hermes_available_false_when_model_endpoint_down
    # a real binary on PATH but Ollama unreachable → still unavailable
    agent = A::Hermes.new(root: "/tmp", command: "ruby", ollama_url: "http://127.0.0.1:1")
    refute agent.available?
  end

  # -- availability (PATH probe) ---------------------------------------------

  def test_available_false_for_missing_binary
    refute A::ClaudeCli.new(root: "/tmp", command: "definitely-not-a-real-binary-xyz").available?
  end

  def test_available_true_for_binary_on_path
    assert A::ClaudeCli.new(root: "/tmp", command: "ruby").available?
  end

  # -- Config parsing --------------------------------------------------------

  def with_config(body)
    Dir.mktmpdir do |dir|
      path = File.join(dir, "config")
      File.write(path, body)
      yield LLM::Config.load(path: path)
    end
  end

  def test_config_reads_default_provider_and_model
    with_config("llm_provider = hermes\nllm_model = gemma4:e4b\n") do |c|
      assert_equal "hermes", c.provider
      assert_equal "gemma4:e4b", c.model
    end
  end

  def test_config_reads_provider_model_lists_and_settings
    body = <<~CFG
      hermes_models = gemma4:e4b, gemma4:12b-mlx , qwen3:4b
      hermes_command = /opt/hermes
      hermes_provider = my-ollama
      ollama_url = http://127.0.0.1:9999
    CFG
    with_config(body) do |c|
      h = c.provider_settings("hermes")
      assert_equal %w[gemma4:e4b gemma4:12b-mlx qwen3:4b], h[:models]
      assert_equal "/opt/hermes", h[:command]
      assert_equal "my-ollama", h[:inference_provider]
      assert_equal "http://127.0.0.1:9999", h[:ollama_url]
    end
  end

  def test_config_ignores_unknown_keys_and_missing_file
    with_config("nonsense = 1\ndir = /whatever\n") do |c|
      assert_nil c.provider
      assert_nil c.model
    end
    c = LLM::Config.load(path: "/no/such/file")
    assert_nil c.provider
  end

  # -- Registry + resolution -------------------------------------------------

  def empty_config = LLM::Config.new(provider: nil, model: nil, providers: {})

  def test_registry_defaults
    reg = LLM.registry(empty_config)
    assert_equal %w[claude-cli hermes], reg.keys
    assert_equal A::ClaudeCli, reg["claude-cli"].adapter
    assert_equal %w[sonnet opus haiku], reg["claude-cli"].models
  end

  def test_entries_put_default_first_and_dedupe
    entries = LLM.entries(empty_config)
    assert_equal "claude-cli:sonnet", entries.first.to_s
    assert_equal entries.map(&:to_s), entries.map(&:to_s).uniq
    assert_includes entries.map(&:to_s), "hermes:gemma4:e4b"
  end

  def test_config_moves_default_entry_to_front
    cfg = LLM::Config.new(provider: "hermes", model: "gemma4:e4b", providers: {})
    assert_equal "hermes:gemma4:e4b", LLM.entries(cfg).first.to_s
  end

  def test_config_model_list_override_flows_into_entries
    cfg = LLM::Config.new(provider: nil, model: nil,
                          providers: { "hermes" => { models: %w[qwen3:4b] } })
    hermes = LLM.entries(cfg).map(&:to_s).select { |e| e.start_with?("hermes:") }
    assert_equal %w[hermes:qwen3:4b], hermes
  end

  def test_default_entry_precedence_explicit_over_config
    cfg = LLM::Config.new(provider: "claude-cli", model: "haiku", providers: {})
    # explicit args win over config
    assert_equal "hermes:gemma4:e4b",
                 LLM.default_entry(provider: "hermes", config: cfg).to_s
    # a model not in the list is still honored (flexibility to run any model)
    assert_equal "claude-cli:sonnet-5",
                 LLM.default_entry(model: "sonnet-5", config: cfg).to_s
  end

  def test_default_entry_falls_back_to_first_provider_and_model
    assert_equal "claude-cli:sonnet", LLM.default_entry(config: empty_config).to_s
  end

  def test_build_returns_configured_adapter_with_settings
    cfg = LLM::Config.new(provider: nil, model: nil,
                          providers: { "hermes" => { command: "/opt/hermes",
                                                     inference_provider: "x" } })
    agent = LLM.build(LLM::Entry.new(provider: "hermes", model: "gemma4:e4b"),
                      root: "/tmp", config: cfg)
    assert_instance_of A::Hermes, agent
    assert_equal "/opt/hermes", agent.command("hi", model: "m").first
    assert_equal "x", agent.command("hi", model: "m")[agent.command("hi", model: "m").index("--provider") + 1]
  end

  def test_build_raises_on_unknown_provider
    assert_raises(ArgumentError) do
      LLM.build(LLM::Entry.new(provider: "nope", model: "m"), root: "/tmp", config: empty_config)
    end
  end
end
