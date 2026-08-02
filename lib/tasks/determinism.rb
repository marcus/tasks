# frozen_string_literal: true

require "securerandom"
require "socket"
require "time"
require_relative "timezones"

module Tasks
  # The one place that turns harness *pins* into the injectable values the
  # library already accepts. It exists for the Go-port conformance harness
  # (`porting/`), where two runs of the same command must produce byte-identical
  # files, and for nothing else.
  #
  # Design rules, deliberately narrow:
  #
  #   * This module is an **adapter-boundary** concern, like Config. Only
  #     `bin/tasks`, `bin/tasks-api`, `bin/tasks-tui`, and Config itself read it.
  #     Store, Journal, Application and the domain stay env-free and keep taking
  #     plain injected values.
  #   * Every accessor returns `nil` when its pin is unset, and every caller
  #     falls back to the constructor default it used before. Unpinned behavior
  #     is therefore bit-for-bit what it was.
  #   * It configures nothing else. It is not a second config system: it cannot
  #     select files, change formatting, or alter semantics. If a pin would do
  #     any of those, it does not belong here.
  #
  # Pins are documented in `porting/specs/determinism.md`; that file and this
  # one must be changed together.
  module Determinism
    # Instant every clock reads. RFC3339 / ISO8601 with an offset (e.g.
    # "2026-03-14T15:09:26Z"). Unset: the real clock.
    NOW = "TASKS_PIN_NOW"

    # Task-id mint sequence. Comma-separated tokens; each is either eight hex
    # characters or the literal "seq" (equivalent to "00000000"). Ids are minted
    # in order; after the last token the sequence continues by incrementing it as
    # a 32-bit counter. Unset: SecureRandom.
    IDS = "TASKS_PIN_IDS"

    # The journal's coalescing scope — a per-process random token that is
    # persisted into journal index.json and therefore observable in journal
    # bytes. Unset: SecureRandom.hex(16).
    COALESCE_SCOPE = "TASKS_PIN_COALESCE_SCOPE"

    # The per-operation coalescing KEY minted for each delegation operation
    # (delegate / undelegate / claim / release). Like the scope, it is persisted
    # into journal index.json and is therefore observable in journal bytes —
    # unlike the scope, a fresh one is minted per operation rather than per
    # process, so it needs a sequence rather than a single value. Same spelling
    # rules as IDS but sixteen hex characters wide. Unset: SecureRandom.hex(8).
    DELEGATION_KEYS = "TASKS_PIN_DELEGATION_KEYS"

    # Host name used for host-context selection in Config. Unset:
    # Socket.gethostname. (The *device* half of update stamps has its own
    # long-standing setting, TASKS_DEVICE; this module does not duplicate it.)
    HOSTNAME = "TASKS_PIN_HOSTNAME"

    # Terminal geometry for full-screen clients. Standard POSIX names, honored
    # only when the real terminal size would otherwise be queried. Unset: the
    # tty's own winsize, then 24x80.
    LINES = "LINES"
    COLUMNS = "COLUMNS"

    # Pre-existing settings the harness also pins; listed so `report` can show
    # the complete applied set without inventing new names for them.
    DEVICE = "TASKS_DEVICE"
    TZ = "TZ"
    LANG = "LANG"
    LC_ALL = "LC_ALL"

    KEYS = [NOW, IDS, COALESCE_SCOPE, DELEGATION_KEYS, HOSTNAME, LINES, COLUMNS,
            DEVICE, TZ, LANG, LC_ALL].freeze

    # A monotonic, reproducible hex mint. Stateful on purpose: one CLI
    # invocation can perform several mutations, and each must draw the *next*
    # token rather than repeat the first. Store's existing collision loop still
    # applies to ids, so an id already present in the store or archive is
    # skipped exactly as before.
    #
    # `width` is the token length in hex characters — 8 for task/section ids,
    # 16 for delegation coalescing keys — and `name` is only used in messages,
    # so a malformed pin names the pin the operator actually set.
    class IdSequence
      SPAN = 1 << 32

      attr_reader :width

      def initialize(spec, width: 8, name: IDS)
        @width = Integer(width)
        @name = name
        @token = /\A[0-9a-f]{#{@width}}\z/
        @span = 16**@width
        @queue = parse(spec)
        raise ArgumentError, "#{@name} is empty" if @queue.empty?

        @counter = nil
      end

      def call
        if (token = @queue.shift)
          @counter = token.to_i(16)
          return token
        end
        @counter = (@counter + 1) % @span
        format("%0#{@width}x", @counter)
      end

      private

      def parse(spec)
        spec.to_s.split(",").map do |raw|
          token = raw.strip.downcase
          token = "0" * @width if token == "seq"
          unless @token.match?(token)
            raise ArgumentError,
                  "#{@name} token must be #{@width} hex characters or \"seq\", got #{raw.inspect}"
          end

          token
        end
      end
    end

    module_function

    # Frozen UTC instant, or nil. Raises on an unparseable pin: a harness that
    # silently fell back to the wall clock would produce a green run that proves
    # nothing.
    def now(env: ENV)
      raw = env[NOW].to_s.strip
      return nil if raw.empty?

      Time.iso8601(raw).utc.freeze
    rescue ArgumentError
      raise ArgumentError, "#{NOW} must be an ISO8601 instant with an offset, got #{raw.inspect}"
    end

    # Clock lambda in the shape Store/StoreFactory/TemporalContext already take.
    def clock(env: ENV)
      instant = now(env: env)
      instant && -> { instant }
    end

    # Process-wide id mint. Memoized because `bin/tasks` builds more than one
    # Store per invocation and they must share one sequence.
    def id_source(env: ENV)
      spec = env[IDS].to_s.strip
      return nil if spec.empty?
      return @id_source if defined?(@id_source) && @id_source_spec == spec

      @id_source_spec = spec
      @id_source = IdSequence.new(spec)
    end

    def coalesce_scope(env: ENV)
      value = env[COALESCE_SCOPE].to_s.strip
      value.empty? ? nil : value
    end

    # Process-wide mint for delegation coalescing keys. Memoized for the same
    # reason id_source is: one invocation may build more than one Application
    # and they must draw from one sequence rather than each restart it.
    def delegation_key_source(env: ENV)
      spec = env[DELEGATION_KEYS].to_s.strip
      return nil if spec.empty?
      return @delegation_key_source if defined?(@delegation_key_source) && @delegation_key_spec == spec

      @delegation_key_spec = spec
      @delegation_key_source = IdSequence.new(spec, width: 16, name: DELEGATION_KEYS)
    end

    # Callable, never nil: Config.resolve wants a hostname provider and the
    # unpinned provider is exactly the one it defaulted to before.
    def hostname(env: ENV)
      pinned = env[HOSTNAME].to_s.strip
      pinned.empty? ? -> { Socket.gethostname } : -> { pinned }
    end

    # [rows, columns] when both are pinned to positive integers, else nil so the
    # caller queries the real terminal.
    def winsize(env: ENV)
      rows = positive_integer(env[LINES])
      columns = positive_integer(env[COLUMNS])
      rows && columns ? [rows, columns] : nil
    end

    def positive_integer(value)
      number = Integer(value.to_s.strip, 10)
      number.positive? ? number : nil
    rescue ArgumentError, TypeError
      nil
    end

    # Everything an observation should record about how this process was pinned,
    # plus the host facts a pin cannot control but a comparison must know. Keys
    # with unset pins are present with a null value: "no pin was applied" is a
    # fact worth recording, and omission would make it indistinguishable from a
    # harness that forgot to look.
    def report(env: ENV)
      {
        "pins" => KEYS.to_h { |key| [key, env[key].nil? ? nil : env[key].to_s] },
        "tzdb_version" => Timezones.tzdb_version,
        "ruby_version" => RUBY_VERSION,
        "ruby_platform" => RUBY_PLATFORM,
        "default_external" => Encoding.default_external.to_s,
        "default_internal" => Encoding.default_internal&.to_s
      }
    end

    # Test-only: drop the memoized sequences.
    def reset!
      remove_instance_variable(:@id_source) if defined?(@id_source)
      remove_instance_variable(:@id_source_spec) if defined?(@id_source_spec)
      remove_instance_variable(:@delegation_key_source) if defined?(@delegation_key_source)
      remove_instance_variable(:@delegation_key_spec) if defined?(@delegation_key_spec)
      nil
    end
  end
end
