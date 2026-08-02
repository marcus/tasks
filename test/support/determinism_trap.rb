# frozen_string_literal: true

# determinism_trap.rb — loaded into a `bin/tasks` child via RUBYOPT=-r, it
# records every read of a nondeterministic source and writes one TSV line per
# call to $TASKS_TRAP_LOG.
#
# This is the only mechanism that turns `invocation.pins[].applied` from a claim
# into a proof. `applied` is computed by asking whether `Tasks::Determinism`
# RESOLVED a value — not whether every call site USED it — so a single method
# with a `today: Date.today` default parameter, or a second hostname consumer
# that never consults the module, produces wall-clock-dependent output while the
# observation cheerfully records `applied: true`. That is not hypothetical: it is
# how an unpinned `SecureRandom.hex(8)` reached durable journal bytes and how
# `TASKS_PIN_HOSTNAME` failed to reach the update stamp's device slug.
#
# Deliberately a separate file rather than a heredoc inside the test: it is
# loaded by `ruby -r`, so it must be requirable on its own, and a reader
# debugging a trap hit wants a real file with real line numbers.
#
# It patches the *source* methods, not the seam, so a call that bypasses
# `Tasks::Determinism` is exactly what it catches.

require "date"
require "time"
require "socket"
require "securerandom"

module DeterminismTrap
  LOG = ENV.fetch("TASKS_TRAP_LOG")

  module_function

  # The first frame outside this file: the call site that actually wanted the
  # nondeterministic value. Recorded rather than asserted on here, so the test
  # owns the policy and this file owns only the observation.
  def record(kind)
    site = caller.find { |line| !line.include?("determinism_trap.rb") } || "?"
    File.open(LOG, "a") { |f| f.puts("#{kind}\t#{site}") }
  end
end

class << Date
  alias_method :__trap_today, :today
  def today(...)
    DeterminismTrap.record("Date.today")
    __trap_today(...)
  end
end

class << Time
  alias_method :__trap_now, :now
  def now(...)
    DeterminismTrap.record("Time.now")
    __trap_now(...)
  end
end

class << Socket
  alias_method :__trap_gethostname, :gethostname
  def gethostname(...)
    DeterminismTrap.record("Socket.gethostname")
    __trap_gethostname(...)
  end
end

module SecureRandom
  class << self
    alias_method :__trap_hex, :hex
    def hex(...)
      DeterminismTrap.record("SecureRandom.hex")
      __trap_hex(...)
    end

    alias_method :__trap_uuid, :uuid
    def uuid(...)
      DeterminismTrap.record("SecureRandom.uuid")
      __trap_uuid(...)
    end
  end
end
