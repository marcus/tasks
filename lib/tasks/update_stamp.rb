# frozen_string_literal: true

require "socket"
require "time"

module Tasks
  # Value object helpers for the per-record last-write stamp carried in JSONL.
  # Stamps stay as one sortable token on disk so the timestamp and device
  # tiebreaker cannot drift apart.
  module UpdateStamp
    VALUE_RE = /\A(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z)#([a-z0-9]+)\z/

    module_function

    def valid?(value)
      return false unless value.is_a?(String) && (match = VALUE_RE.match(value))

      Time.iso8601(match[1]).utc?
    rescue ArgumentError
      false
    end

    def key(value)
      return nil unless valid?(value)

      timestamp, device = value.split("#", 2)
      [timestamp, device]
    end

    def compare(left, right)
      left_key = key(left)
      right_key = key(right)
      return 0 if left_key.nil? && right_key.nil?
      return -1 if left_key.nil?
      return 1 if right_key.nil?

      left_key <=> right_key
    end

    def max(left, right)
      compare(left, right).negative? ? right : left
    end

    def format(time, device)
      normalized_device = slug(device)
      raise ArgumentError, "device slug is empty" if normalized_device.empty?
      raise ArgumentError, "clock must return a Time" unless time.respond_to?(:utc)

      "#{time.utc.strftime("%Y-%m-%dT%H:%M:%SZ")}##{normalized_device}"
    end

    # Hostnames commonly look like Marcus-MBP.local. The first alphanumeric
    # token is stable and short ("marcus") while still allowing an explicit
    # TASKS_DEVICE such as "home2" to disambiguate similar machines.
    def slug(value)
      value.to_s.downcase.split(".", 2).first.to_s.scan(/[a-z0-9]+/).first || "device"
    end

    def device(env: ENV, hostname: Socket.gethostname)
      override = env["TASKS_DEVICE"].to_s
      slug(override.strip.empty? ? hostname : override)
    end
  end
end
