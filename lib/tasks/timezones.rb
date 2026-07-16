# frozen_string_literal: true

require "date"
require "tzinfo"

module Tasks
  module Timezones
    FALLBACK = "Etc/UTC"

    class Error < ArgumentError; end
    class NonexistentLocalTime < Error; end

    module_function

    def get(identifier)
      id = identifier.to_s.strip
      raise Error, "time zone is required" if id.empty?
      unless id.include?("/") || id == "UTC"
        raise Error, "#{id.inspect} is not an IANA time-zone identifier"
      end
      TZInfo::Timezone.get(id)
    rescue TZInfo::InvalidTimezoneIdentifier
      raise Error, "unknown IANA time zone #{id.inspect}"
    end

    def resolve(identifier) = identifier.is_a?(TZInfo::Timezone) ? identifier : get(identifier)

    def utc_for(date, local, zone, fold: 0)
      hour, minute = local.split(":", 2).map(&:to_i)
      wall = Time.utc(date.year, date.month, date.day, hour, minute)
      periods = resolve(zone).periods_for_local(wall)
      if periods.empty?
        next_local = first_valid_local_after(date, local, zone)
        hint = next_local ? "; first valid time is #{next_local}" : ""
        raise NonexistentLocalTime, "#{date} #{local} does not exist in #{resolve(zone).identifier}#{hint}"
      end

      instants = periods.map { |period| wall - period.observed_utc_offset }.sort
      (fold.to_i == 1 ? instants.last : instants.first).utc
    end

    def earliest_on(date, zone)
      0.upto(1_439) do |minute|
        local = format("%02d:%02d", minute / 60, minute % 60)
        return utc_for(date, local, zone)
      rescue NonexistentLocalTime
        next
      end
      raise NonexistentLocalTime, "calendar date #{date} does not exist in #{resolve(zone).identifier}"
    end

    def local_time(utc, zone) = resolve(zone).utc_to_local(utc.utc)

    def ambiguous?(date, local, zone)
      hour, minute = local.split(":", 2).map(&:to_i)
      resolve(zone).periods_for_local(Time.utc(date.year, date.month, date.day, hour, minute)).length > 1
    end

    def first_valid_local_after(date, local, zone)
      hour, minute = local.split(":", 2).map(&:to_i)
      start = hour * 60 + minute
      (start + 1).upto(1_439) do |candidate|
        wall = Time.utc(date.year, date.month, date.day, candidate / 60, candidate % 60)
        return format("%02d:%02d", candidate / 60, candidate % 60) unless resolve(zone).periods_for_local(wall).empty?
      end
      nil
    end

    def detect(env: ENV, localtime: "/etc/localtime")
      if env["TZ"] && !env["TZ"].empty?
        return [get(env["TZ"]).identifier, "TZ env", false]
      end
      if File.symlink?(localtime)
        target = File.realpath(localtime)
        marker = "/zoneinfo/"
        if (index = target.index(marker))
          id = target[(index + marker.length)..]
          return [get(id).identifier, "host /etc/localtime", false]
        end
      end
      [FALLBACK, "UTC fallback", true]
    rescue Error, SystemCallError
      [FALLBACK, "UTC fallback", true]
    end

    def tzdb_version
      TZInfo::DataSource.get.to_s
    end
  end
end
