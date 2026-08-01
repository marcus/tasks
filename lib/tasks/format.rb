# frozen_string_literal: true

require "json"
require_relative "delegation"

module Tasks
  # The JSONL store's serialization layer, and the SOLE owner of the on-disk
  # schema: which keys exist, the order they appear in, and the omission rules.
  # It knows nothing about *meaning* — dates stay "YYYY-MM-DD" strings, tags
  # stay arrays, recur stays a raw cookie. Turning those into Date objects is
  # the Store's job; validating them against the schema is Check's job. Format
  # only does shape: canonical order, empty-field omission, line numbering.
  #
  # One record per line, `JSON.generate`'d (compact, UTF-8, non-ASCII left
  # unescaped so git diffs stay readable). Parsing is lenient: a malformed or
  # non-object line never raises — it becomes an (line, message) error entry
  # and is skipped, so a single bad line can't take down the whole store.
  module Format
    # Bumped when the on-disk schema changes incompatibly. Lives on the `meta`
    # record (line 1): {"type":"meta","version":1}.
    VERSION = 2

    # Canonical field order for a serialized record. Any key present on a record
    # but absent here is emitted *after* these, in the hash's own insertion
    # order — so a future writer can add fields without this layer knowing them
    # (forward-compat), and an older reader round-trips them untouched.
    KEY_ORDER = %w[
      type id parent state priority title tags scheduled scheduled_time
      deadline deadline_time recur lead lead_skip delegation
      closed archived body updated
    ].freeze

    # Nested objects with a fixed key order of their own. Emission follows the
    # same rules one level down — declared order first, absent entries omitted
    # — so a delegation or a time value produces one canonical line whichever
    # writer built it.
    NESTED_KEY_ORDER = {
      "scheduled_time" => %w[local timezone fold].freeze,
      "deadline_time" => %w[local timezone fold].freeze,
      Delegation::FIELD => Delegation::KEY_ORDER,
    }.freeze

    # Nested objects whose *unknown* keys round-trip instead of being dropped —
    # the same forward-compat rule the top level has always had. `delegation` is
    # in: a field a newer binary added must survive a write by this one, and
    # Check only warns about it. The temporal `*_time` objects are deliberately
    # out: Check treats an unknown key there as a hard error, so dropping it on
    # the next write is the repair path, not data loss.
    NESTED_FORWARD_COMPAT = [Delegation::FIELD].freeze

    # The physical 1-based line number `parse` stamps onto each record so the
    # Store can resolve `L<n>` refs and reselect in the TUI. It is bookkeeping,
    # never part of the schema — `dump_record` drops it so it never serializes.
    LINE_KEY = "line"

    module_function

    # Serialize one record hash to a single JSON line (no trailing newline).
    # Keys emit in KEY_ORDER first, then any unknown keys in insertion order.
    # nil, "", and [] are omitted (an absent field, not a present-but-empty
    # one). The out-of-band "line" marker never serializes.
    def dump_record(record)
      record = stringify(record)
      ordered = {}
      KEY_ORDER.each do |k|
        next unless record.key?(k)
        value = nested_object(k, record[k])
        ordered[k] = value unless omit?(value)
      end
      record.each do |k, v|
        next if k == LINE_KEY || KEY_ORDER.include?(k) || omit?(v)
        ordered[k] = v
      end
      JSON.generate(ordered)
    end

    # Serialize a list of records to full file text: one record per line, with a
    # trailing newline at EOF. An empty list yields an empty string.
    def dump(records)
      return "" if records.empty?
      +records.map { |r| dump_record(r) }.join("\n") << "\n"
    end

    Result = Struct.new(:records, :errors) do
      def ok? = errors.empty?
    end

    # Parse full file text into a Result of records + (line, message) errors.
    # Lenient and total: never raises. A line that isn't valid JSON, or that
    # parses to something other than an object, becomes an error tuple (1-based
    # line number) and is skipped; every other record still parses. Each record
    # carries its physical line number under LINE_KEY.
    def parse(text)
      records = []
      errors = []
      # Tolerate a leading UTF-8 BOM (some editors prepend U+FEFF): strip it so
      # line 1 parses as JSON instead of folding in as an opaque "invalid JSON".
      text = text.sub(/\A﻿/, "")
      text.each_line.with_index(1) do |line, no|
        stripped = line.chomp
        if stripped.strip.empty?
          errors << [no, "blank line"]
          next
        end
        begin
          value = JSON.parse(stripped)
        rescue JSON::ParserError => e
          errors << [no, "invalid JSON: #{e.message.lines.first.strip}"]
          next
        end
        unless value.is_a?(Hash)
          errors << [no, "expected a JSON object, got #{value.class}"]
          next
        end
        value[LINE_KEY] = no
        records << value
      end
      Result.new(records, errors)
    end

    # A value that represents an absent field — omitted from serialization.
    def omit?(value)
      value.nil? || (value.respond_to?(:empty?) && value.empty?)
    end

    # Records may reach us with symbol keys (a hand-built hash) or string keys
    # (a parsed record); the schema speaks strings, so normalize the top level.
    def stringify(record)
      record.each_key.all? { |k| k.is_a?(String) } ? record : record.transform_keys(&:to_s)
    end

    # Order one nested object's keys, dropping absent entries. A value with no
    # declared nested order — or one that isn't an object at all — passes
    # through untouched, so a malformed record still serializes for Check to
    # report rather than raising here. For a forward-compat field, keys this
    # binary does not know follow the declared ones in their own order, exactly
    # as dump_record does at the top level.
    def nested_object(key, value)
      order = NESTED_KEY_ORDER[key]
      return value unless order && value.is_a?(Hash)
      source = stringify(value)
      ordered = {}
      order.each do |child|
        ordered[child] = source[child] if source.key?(child) && !omit?(source[child])
      end
      return ordered unless NESTED_FORWARD_COMPAT.include?(key)

      source.each do |child, child_value|
        next if order.include?(child) || omit?(child_value)
        ordered[child] = child_value
      end
      ordered
    end
  end
end
