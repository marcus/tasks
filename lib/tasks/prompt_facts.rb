# frozen_string_literal: true

require "socket"

module Tasks
  # Registry of short, labeled facts injected into an agent's system prompt
  # under a "Current environment" heading. Each fact is toggled with a
  # `prompt.<name> = on|off` config key; presentation order follows REGISTRY.
  #
  # Providers that raise or return blank are omitted silently so a flaky
  # future source (weather, …) never aborts an agent run.
  module PromptFacts
    FACT_NAME = /\A[a-z][a-z0-9_-]*\z/

    REGISTRY = {
      "datetime" => {
        default: true,
        render: ->(clock:, **) { format_datetime(clock.call) }
      },
      "hostname" => {
        default: true,
        render: ->(hostname:, **) { hostname.call }
      }
    }.freeze

    module_function

    # Effective on/off map for every registered fact. `overrides` comes from
    # config (`prompt.*`); absent keys keep the registry default.
    def resolve(overrides = {})
      REGISTRY.each_with_object({}) do |(name, spec), map|
        map[name] = overrides.key?(name) ? !!overrides[name] : !!spec[:default]
      end
    end

    # Render the "Current environment" block, or nil when nothing enabled /
    # every provider fails. `enabled` is the resolved name→bool map.
    def render(enabled, clock: -> { Time.now }, hostname: -> { Socket.gethostname })
      lines = []
      REGISTRY.each do |name, spec|
        next unless enabled[name]

        value = safe_value(spec[:render], clock: clock, hostname: hostname)
        next if value.nil? || value.to_s.strip.empty?

        lines << "- #{name}: #{value.to_s.strip}"
      end
      return nil if lines.empty?

      "Current environment:\n#{lines.join("\n")}"
    end

    def format_datetime(time)
      time.strftime("%Y-%m-%d %a %H:%M %Z")
    end

    def parse_toggle(value)
      case value.to_s.strip.downcase
      when "on", "true", "1"  then true
      when "off", "false", "0" then false
      end
    end

    def safe_value(renderer, clock:, hostname:)
      renderer.call(clock: clock, hostname: hostname)
    rescue StandardError
      nil
    end
    private_class_method :safe_value
  end
end
