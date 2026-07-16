# frozen_string_literal: true

require "date"
require_relative "timezones"

module Tasks
  class TemporalValue
    LOCAL_RE = /\A(?:[01]\d|2[0-3]):[0-5]\d\z/

    attr_reader :date, :local_time, :timezone, :fold

    def initialize(date:, local_time: nil, timezone: nil, fold: 0, validate: true)
      @date = date.is_a?(Date) ? date : Date.iso8601(date.to_s)
      @local_time = local_time&.to_s
      @timezone = timezone&.to_s
      @fold = Integer(fold || 0)
      raise ArgumentError, "local time must use HH:MM minute precision" if @local_time && !LOCAL_RE.match?(@local_time)
      raise ArgumentError, "time zone requires a local time" if @timezone && !@local_time
      raise ArgumentError, "fold must be 0 or 1" unless [0, 1].include?(@fold)
      Timezones.get(@timezone) if @timezone
      resolved_instant(Timezones.get(@timezone)) if validate && fixed?
      freeze
    rescue Date::Error
      raise ArgumentError, "date must be a real YYYY-MM-DD date"
    end

    def all_day? = local_time.nil?
    def floating? = !all_day? && timezone.nil?
    def fixed? = !timezone.nil?

    def effective_zone(context)
      fixed? ? Timezones.get(timezone) : context.timezone
    end

    def instant(context)
      return Timezones.earliest_on(date, context.timezone) if all_day?
      resolved_instant(effective_zone(context))
    end
    alias release_instant instant

    def due_boundary(context)
      all_day? ? Timezones.earliest_on(date + 1, context.timezone) : instant(context)
    end

    def overdue?(context) = context.now > due_boundary(context)
    def released?(context) = context.now >= release_instant(context)

    def projected(context)
      return { date: date, local: nil, timezone: nil } if all_day?
      local = Timezones.local_time(instant(context), context.timezone)
      { date: local.to_date, local: format("%02d:%02d", local.hour, local.min), timezone: context.timezone_id }
    end

    def time_metadata
      return nil if all_day?
      { "local" => local_time }.tap do |h|
        h["timezone"] = timezone if timezone
        h["fold"] = 1 if fold == 1
      end.freeze
    end

    def api_time(context)
      return nil if all_day?
      {
        local: local_time, timezone: timezone, fold: fold,
        effective_timezone: effective_zone(context).identifier,
        instant: instant(context).iso8601,
      }
    end

    def shift(days)
      self.class.new(date: date + days, local_time: local_time, timezone: timezone, fold: fold)
    end

    def with_date(new_date) = self.class.new(date: new_date, local_time: local_time, timezone: timezone, fold: fold)

    def ==(other)
      other.is_a?(TemporalValue) && [date, local_time, timezone, fold] ==
        [other.date, other.local_time, other.timezone, other.fold]
    end
    alias eql? ==
    def hash = [date, local_time, timezone, fold].hash

    def self.from_record(record, field, validate: false)
      raw_date = record[field.to_s]
      return nil unless raw_date
      time = record["#{field}_time"]
      new(date: raw_date, local_time: time&.fetch("local", nil),
          timezone: time&.fetch("timezone", nil), fold: time&.fetch("fold", 0), validate: validate)
    rescue ArgumentError, KeyError, TypeError
      new(date: raw_date, validate: false)
    rescue Date::Error
      nil
    end

    private

    def resolved_instant(zone) = Timezones.utc_for(date, local_time, zone, fold: fold)
  end
end
