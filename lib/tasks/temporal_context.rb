# frozen_string_literal: true

require_relative "timezones"

module Tasks
  class TemporalContext
    attr_reader :now, :timezone, :time_format

    def initialize(now:, timezone:, time_format: 12)
      @now = now.utc.freeze
      @timezone = Timezones.resolve(timezone)
      @time_format = Integer(time_format)
      raise ArgumentError, "time format must be 12 or 24" unless [12, 24].include?(@time_format)
      freeze
    end

    def local_now = Timezones.local_time(now, timezone)
    def local_date = local_now.to_date
    def timezone_id = timezone.identifier

    def self.capture(timezone:, time_format: 12, clock: -> { Time.now.utc })
      new(now: clock.call, timezone: timezone, time_format: time_format)
    end
  end
end
