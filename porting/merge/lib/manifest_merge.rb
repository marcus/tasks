# frozen_string_literal: true

require "json"
require "set"

module Porting
  # Deterministic, record-aware three-way merge for porting/manifest.jsonl,
  # keyed on the slice `id` rather than line position.
  #
  # This is deliberately a smaller cousin of Tasks::JsonlMerge
  # (lib/tasks/jsonl_merge.rb), not a copy of it. tasks.jsonl records carry a
  # tree (`parent`), an `updated` last-writer-wins stamp, and special-cased
  # fields (tags, body, delegation, state/closed) that its merge resolves with
  # bespoke rules. Manifest records are flat and have no `updated` stamp at
  # all, so there is no arbiter to break a tie when both sides genuinely
  # changed the same field to different values. Where tasks.jsonl's merge would
  # reach for last-write-wins, this one has nothing to reach for — so it
  # refuses instead. That is the one semantic difference worth stating plainly:
  # THIS MERGE NEVER GUESSES. A real same-field conflict fails the whole file,
  # the same way an unparseable side or an invalid merged result does.
  module ManifestMerge
    Result = Struct.new(:text, :events, :error, keyword_init: true) do
      def ok? = error.nil?

      def log_lines(pathname: nil)
        heading = "merge #{pathname || "porting/manifest.jsonl"}: #{ok? ? "ok" : "failed"}"
        return [heading, "  error: #{error}"] unless ok?

        ["#{heading} (#{events.length} decisions)", *events.map { |event| ManifestMerge.format_event(event) }]
      end
    end

    MergeError = Class.new(StandardError)

    Entry = Struct.new(:id, :raw, :obj, keyword_init: true)

    module_function

    def merge(base_text:, ours_text:, theirs_text:)
      base = parse_side("base", base_text, allow_empty: true)
      ours = parse_side("ours", ours_text)
      theirs = parse_side("theirs", theirs_text)
      events = []

      base_by_id = index_by_id(base)
      ours_by_id = index_by_id(ours)
      theirs_by_id = index_by_id(theirs)

      order = ours.map(&:id) + theirs.map(&:id).reject { |id| ours_by_id.key?(id) }.uniq

      kept_ids = []
      lines = order.filter_map do |id|
        line = resolve_record(id, base_by_id[id], ours_by_id[id], theirs_by_id[id], events)
        kept_ids << id if line
        line
      end

      text = lines.join
      validate_output!(text, kept_ids)

      Result.new(text: text, events: events.freeze, error: nil)
    rescue MergeError, EncodingError, JSON::ParserError => error
      Result.new(text: nil, events: [].freeze, error: error.message)
    end

    # Every side must be valid UTF-8 JSONL: one JSON object per non-blank line,
    # each carrying a unique string `id`. There is no schema-version header in
    # this file (unlike tasks.jsonl's meta record) to gate on, so this is the
    # whole of what "parseable" means here.
    def parse_side(label, text, allow_empty: false)
      utf8 = text.dup.force_encoding(Encoding::UTF_8)
      raise MergeError, "#{label} is not valid UTF-8" unless utf8.valid_encoding?
      return [] if allow_empty && utf8.strip.empty?

      seen = Set.new
      utf8.each_line.with_index(1).filter_map do |line, lineno|
        next nil if line.strip.empty?

        obj = begin
          JSON.parse(line)
        rescue JSON::ParserError => error
          raise MergeError, "#{label} line #{lineno}: #{error.message}"
        end
        unless obj.is_a?(Hash)
          raise MergeError, "#{label} line #{lineno}: record is not a JSON object"
        end
        id = obj["id"]
        unless id.is_a?(String) && !id.empty?
          raise MergeError, "#{label} line #{lineno}: record has no string `id`"
        end
        raise MergeError, "#{label} line #{lineno}: duplicate id `#{id}`" unless seen.add?(id)

        Entry.new(id: id, raw: line, obj: obj)
      end
    end

    def index_by_id(entries)
      entries.each_with_object({}) { |entry, index| index[entry.id] = entry }
    end

    # One record, three possible histories (present/absent on base/ours/theirs).
    # Returns the raw line to emit, byte for byte, wherever a side's line can be
    # taken unchanged — a record this method did not have to merge is never
    # reserialized. Returns nil for an honored deletion.
    def resolve_record(id, base, ours, theirs, events)
      if base.nil?
        return added_record(id, ours, theirs, events)
      end

      if ours.nil? && theirs.nil?
        events << { id: id, decision: :deleted }
        return nil
      end
      if ours.nil?
        if theirs.obj == base.obj
          events << { id: id, decision: :deleted_by_ours }
          return nil
        end
        events << { id: id, decision: :kept_theirs_edit_over_ours_delete }
        return theirs.raw
      end
      if theirs.nil?
        if ours.obj == base.obj
          events << { id: id, decision: :deleted_by_theirs }
          return nil
        end
        events << { id: id, decision: :kept_ours_edit_over_theirs_delete }
        return ours.raw
      end

      return keep(id, ours, events, :unchanged) if ours.obj == theirs.obj
      return keep(id, theirs, events, :took_theirs_only_change) if ours.obj == base.obj
      return keep(id, ours, events, :took_ours_only_change) if theirs.obj == base.obj

      merge_fields(id, base, ours, theirs, events)
    end

    def added_record(id, ours, theirs, events)
      return nil unless ours || theirs

      if ours && theirs
        return keep(id, ours, events, :added_both_identical) if ours.obj == theirs.obj

        return merge_fields(id, nil, ours, theirs, events, decision: :merged_concurrent_add)
      end

      side, tag = ours ? [ours, :added_ours] : [theirs, :added_theirs]
      keep(id, side, events, tag)
    end

    def keep(id, entry, events, decision)
      events << { id: id, decision: decision }
      entry.raw
    end

    # Both sides changed the record relative to base, and they disagree about
    # its bytes. Resolve field by field with the ordinary 3-way rule: a field
    # only one side touched takes that side; a field neither touched (or both
    # touched to the same value) is uncontested. A field BOTH sides touched to
    # DIFFERENT values has no tiebreaker available -- manifest records carry no
    # `updated` stamp for a last-write-wins call -- so it is a genuine conflict
    # and fails the whole merge (see the module comment).
    #
    # `status` gets no special case beyond this: base=not_started, one side
    # advances it, the other leaves it alone, is exactly "a field only one side
    # touched" and already resolves to the advanced value below. Two sides
    # advancing `status` to two DIFFERENT non-trivial values falls through to
    # the same field-conflict rule as any other field, and correctly refuses --
    # guessing which advancement is real is exactly the silent data loss this
    # driver exists to prevent.
    def merge_fields(id, base, ours, theirs, events, decision: :merged_fields)
      base_obj = base&.obj || {}
      keys = ordered_union(ours.obj.keys, theirs.obj.keys, base_obj.keys)
      merged = {}

      keys.each do |key|
        base_value = base_obj[key]
        ours_value = ours.obj[key]
        theirs_value = theirs.obj[key]

        merged[key] =
          if ours_value == theirs_value
            ours_value
          elsif ours_value == base_value
            theirs_value
          elsif theirs_value == base_value
            ours_value
          else
            raise MergeError, "id `#{id}`: field `#{key}` changed on both sides to different values " \
                               "(ours=#{ours_value.inspect}, theirs=#{theirs_value.inspect})"
          end
      end

      events << { id: id, decision: decision }
      "#{JSON.generate(merged)}\n"
    end

    def ordered_union(*lists)
      seen = Set.new
      lists.flatten.each_with_object([]) { |key, keys| keys << key if seen.add?(key) }
    end

    # A last check that the merge produced what it claims to: valid JSONL, one
    # object per line, unique ids, and exactly the ids the resolution loop
    # decided to keep -- never fewer (a silent drop) and never more (a
    # duplicate slipped in by a merge bug).
    def validate_output!(text, candidate_ids)
      seen = Set.new
      text.each_line.with_index(1) do |line, lineno|
        obj = begin
          JSON.parse(line)
        rescue JSON::ParserError => error
          raise MergeError, "merged output line #{lineno} is invalid: #{error.message}"
        end
        id = obj["id"]
        raise MergeError, "merged output line #{lineno} has no string `id`" unless id.is_a?(String)
        raise MergeError, "merged output has duplicate id `#{id}`" unless seen.add?(id)
      end

      expected = candidate_ids.uniq
      unexpected = seen.to_a - expected
      not_emitted = expected - seen.to_a
      return if unexpected.empty? && not_emitted.empty?

      raise MergeError, "merged output id set does not match the resolved set " \
                         "(unexpected: #{unexpected.join(", ")}; not emitted: #{not_emitted.join(", ")})"
    end

    def format_event(event)
      "  #{event[:id]} #{event[:decision]}"
    end
  end
end
