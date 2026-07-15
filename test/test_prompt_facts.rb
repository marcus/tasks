# frozen_string_literal: true

require_relative "test_helper"
require "tasks/prompt_facts"
require "time"

class TestPromptFacts < Minitest::Test
  def test_resolve_defaults_datetime_and_hostname_on
    assert_equal({ "datetime" => true, "hostname" => true }, Tasks::PromptFacts.resolve)
  end

  def test_resolve_honors_overrides
    map = Tasks::PromptFacts.resolve("datetime" => false, "hostname" => true)
    assert_equal({ "datetime" => false, "hostname" => true }, map)
  end

  def test_resolve_ignores_unknown_override_keys
    map = Tasks::PromptFacts.resolve("weather" => true, "datetime" => false)
    refute map.key?("weather")
    assert_equal false, map["datetime"]
  end

  def test_format_datetime_is_agent_friendly
    t = Time.new(2026, 7, 15, 8, 41, 0)
    out = Tasks::PromptFacts.format_datetime(t)
    assert_match(/\A2026-07-15 Wed 08:41 /, out)
    refute_empty out.split.last # timezone abbrev present
  end

  def test_render_includes_enabled_facts
    enabled = { "datetime" => true, "hostname" => true }
    block = Tasks::PromptFacts.render(
      enabled,
      clock: -> { Time.new(2026, 7, 15, 8, 41, 0) },
      hostname: -> { "test-host.local" }
    )
    assert_match(/\ACurrent environment:\n/, block)
    assert_includes block, "- datetime: 2026-07-15 Wed 08:41"
    assert_includes block, "- hostname: test-host.local"
    # datetime before hostname (registry order)
    assert_operator block.index("datetime"), :<, block.index("hostname")
  end

  def test_render_omits_disabled_facts
    enabled = { "datetime" => false, "hostname" => true }
    block = Tasks::PromptFacts.render(
      enabled,
      clock: -> { raise "should not run" },
      hostname: -> { "only-host" }
    )
    assert_equal "Current environment:\n- hostname: only-host", block
  end

  def test_render_nil_when_all_off
    assert_nil Tasks::PromptFacts.render({ "datetime" => false, "hostname" => false })
  end

  def test_provider_exception_omits_that_line_only
    enabled = { "datetime" => true, "hostname" => true }
    block = Tasks::PromptFacts.render(
      enabled,
      clock: -> { raise "boom" },
      hostname: -> { "survives" }
    )
    assert_equal "Current environment:\n- hostname: survives", block
  end

  def test_blank_provider_value_omits_that_line
    enabled = { "datetime" => true, "hostname" => true }
    block = Tasks::PromptFacts.render(
      enabled,
      clock: -> { Time.new(2026, 7, 15, 8, 41, 0) },
      hostname: -> { "  " }
    )
    refute_includes block, "hostname"
    assert_includes block, "datetime"
  end

  def test_parse_toggle
    assert_equal true, Tasks::PromptFacts.parse_toggle("on")
    assert_equal true, Tasks::PromptFacts.parse_toggle("TRUE")
    assert_equal true, Tasks::PromptFacts.parse_toggle("1")
    assert_equal false, Tasks::PromptFacts.parse_toggle("off")
    assert_equal false, Tasks::PromptFacts.parse_toggle("False")
    assert_equal false, Tasks::PromptFacts.parse_toggle("0")
    assert_nil Tasks::PromptFacts.parse_toggle("maybe")
    assert_nil Tasks::PromptFacts.parse_toggle("")
  end
end
