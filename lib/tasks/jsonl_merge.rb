# frozen_string_literal: true

require "set"
require_relative "check"
require_relative "delegation"
require_relative "format"
require_relative "update_stamp"

module Tasks
  # Deterministic, record-aware three-way merge for tasks JSONL files. The
  # merge is field-level; ordering remains ours-first while the final DFS walk
  # guarantees parent-before-child structural validity.
  module JsonlMerge
    Result = Struct.new(:text, :events, :error, keyword_init: true) do
      def ok? = error.nil?

      def log_lines(pathname: nil)
        heading = "merge #{pathname || "tasks JSONL"}: #{ok? ? "ok" : "failed"}"
        return [heading, "  error: #{error}"] unless ok?

        ["#{heading} (#{events.length} decisions)", *events.map { |event| JsonlMerge.format_event(event) }]
      end
    end

    MergeError = Class.new(StandardError)
    EXCLUDED_FIELDS = Set["line", "updated"].freeze
    STATE_FIELDS = Set["state", "closed"].freeze
    # A date and its nested time metadata are one logical value; resolving them
    # independently could attach one side's clock/zone to the other side's date.
    TEMPORAL_PAIRS = { "scheduled" => "scheduled_time", "deadline" => "deadline_time" }.freeze
    # The delegation marker resolves as ONE object, never field by field: a
    # mixed record could name one worker's id beside another's claim stamp,
    # which is exactly the two-owner state the design forbids.
    SPECIAL_FIELDS = (EXCLUDED_FIELDS + STATE_FIELDS + Set["tags", "body", Delegation::FIELD] +
                      TEMPORAL_PAIRS.keys + TEMPORAL_PAIRS.values).freeze
    TERMINAL_STATES = Check::CLOSED_STATES.to_set.freeze

    module_function

    def merge(base_text:, ours_text:, theirs_text:)
      ours = parse_side("ours", ours_text)
      theirs = parse_side("theirs", theirs_text)
      base = parse_side("base", base_text, allow_empty: true)
      events = []
      base_by_id = index_by_id(base)
      ours_by_id = index_by_id(ours)
      theirs_by_id = index_by_id(theirs)
      ids = ordered_union(ours_by_id.keys, theirs_by_id.keys, base_by_id.keys)
      merged_by_id = {}

      ids.each do |id|
        resolved = resolve_record(
          id, base_by_id[id], ours_by_id[id], theirs_by_id[id], events
        )
        merged_by_id[id] = resolved if resolved
      end

      log_order_conflicts!(merged_by_id, base, ours, theirs, events)
      restore_required_ancestors!(merged_by_id, base_by_id, ours_by_id, theirs_by_id, events)

      records = order_records(merged_by_id, ours, theirs, base)
      text = Format.dump(records)
      validation = Check.check_text(text, version: Format::VERSION)
      unless validation.ok?
        details = validation.errors.map { |line, message| "line #{line}: #{message}" }.join("; ")
        raise MergeError, "merged output is invalid: #{details}"
      end

      Result.new(text: text, events: events.freeze, error: nil)
    rescue MergeError, EncodingError, JSON::ParserError => error
      Result.new(text: nil, events: [].freeze, error: error.message)
    end

    # Every side must declare the schema version this binary implements. There
    # is no cross-version merge: reconciling records whose meaning differs by
    # version is exactly the silent corruption a version header exists to stop.
    def parse_side(label, text, allow_empty: false)
      utf8 = text.dup.force_encoding(Encoding::UTF_8)
      raise MergeError, "#{label} is not valid UTF-8" unless utf8.valid_encoding?
      return [] if allow_empty && utf8.empty?

      parsed = Format.parse(utf8)
      unless parsed.ok?
        details = parsed.errors.map { |line, message| "line #{line}: #{message}" }.join("; ")
        raise MergeError, "#{label} cannot be parsed: #{details}"
      end
      version = declared_version(parsed.records)
      if version && version != Format::VERSION
        raise MergeError, "#{label} is schema v#{version}; this binary reads schema v#{Format::VERSION} only"
      end
      validation = Check.check_parsed(parsed, version: Format::VERSION)
      unless validation.ok?
        details = validation.errors.map { |line, message| "line #{line}: #{message}" }.join("; ")
        raise MergeError, "#{label} is invalid: #{details}"
      end

      parsed.records
    end

    def declared_version(records)
      meta = records.find { |record| record["line"] == 1 } || records.first
      version = meta && meta["type"] == "meta" ? meta["version"] : nil
      version.is_a?(Integer) ? version : nil
    end

    # Deleting a subtree on one side while the other edits a descendant keeps
    # the edited descendant by policy. Keep the minimal ancestor chain too, or
    # that safe resurrection would produce an invalid dangling parent.
    def restore_required_ancestors!(merged_by_id, base_by_id, ours_by_id, theirs_by_id, events)
      loop do
        missing = merged_by_id.each_value.filter_map do |record|
          parent = record["parent"]
          parent if parent && !merged_by_id.key?(parent)
        end.uniq
        break if missing.empty?

        restored = false
        missing.each do |id|
          source = ours_by_id[id] || theirs_by_id[id] || base_by_id[id]
          next unless source

          merged_by_id[id] = clean(source)
          events << { id: id, decision: :restored_ancestor_for_edited_descendant }
          restored = true
        end
        break unless restored
      end
    end

    def index_by_id(records)
      records.each_with_object({}) do |record, index|
        index[record["id"]] = record if record["id"]
      end
    end

    def log_order_conflicts!(merged_by_id, base, ours, theirs, events)
      indexes = [index_by_id(base), index_by_id(ours), index_by_id(theirs)]
      common_ids = indexes.map(&:keys).reduce { |common, ids| common & ids }
      stable_ids = common_ids.select do |id|
        parents = indexes.map { |index| index[id]["parent"] }
        parents.uniq.length == 1 && merged_by_id.key?(id)
      end
      parents = stable_ids.map { |id| indexes.first[id]["parent"] }.uniq

      parents.each do |parent|
        sequences = [base, ours, theirs].map do |records|
          records.filter_map do |record|
            id = record["id"]
            id if stable_ids.include?(id) && record["parent"] == parent
          end
        end
        base_order, ours_order, theirs_order = sequences
        next if base_order.length < 2
        next unless ours_order != base_order && theirs_order != base_order && ours_order != theirs_order

        events << { id: parent || "root", decision: :ours_ordering_conflict }
      end
    end

    def ordered_union(*lists)
      seen = Set.new
      lists.flatten.each_with_object([]) do |id, ids|
        ids << id if seen.add?(id)
      end
    end

    def resolve_record(id, base, ours, theirs, events)
      if base.nil?
        return added_record(id, ours, theirs, events)
      end

      if ours.nil? && theirs.nil?
        events << { id: id, decision: :deleted }
        return nil
      end
      if ours.nil?
        if record_equal?(theirs, base)
          events << { id: id, decision: :deleted_by_ours }
          return nil
        end
        events << { id: id, decision: :kept_theirs_edit_over_ours_delete }
        return clean(theirs)
      end
      if theirs.nil?
        if record_equal?(ours, base)
          events << { id: id, decision: :deleted_by_theirs }
          return nil
        end
        events << { id: id, decision: :kept_ours_edit_over_theirs_delete }
        return clean(ours)
      end
      return clean(ours) if record_equal?(ours, theirs)

      merge_record(id, base, ours, theirs, events)
    end

    def added_record(id, ours, theirs, events)
      if ours && theirs
        return clean(ours) if record_equal?(ours, theirs)

        merge_record(id, nil, ours, theirs, events, decision: :merged_concurrent_add)
      elsif ours
        events << { id: id, decision: :added_ours }
        clean(ours)
      elsif theirs
        events << { id: id, decision: :added_theirs }
        clean(theirs)
      end
    end

    def merge_record(id, base, ours, theirs, events, decision: :merged_fields)
      event = { id: id, decision: decision, conflicts: [], low_confidence: [] }
      merged = {}
      keys = ordered_union(clean(ours).keys, clean(theirs).keys, base ? clean(base).keys : [])

      (keys.to_set - SPECIAL_FIELDS).each do |field|
        value = merge_scalar(field, value_of(base, field), value_of(ours, field),
                             value_of(theirs, field), ours, theirs, event)
        assign(merged, field, value)
      end

      TEMPORAL_PAIRS.each do |date_field, time_field|
        next unless keys.include?(date_field) || keys.include?(time_field)

        merge_temporal_pair!(merged, date_field, time_field, base, ours, theirs, event)
      end

      if keys.include?("tags")
        assign(merged, "tags", merge_tags(value_of(base, "tags"), value_of(ours, "tags"),
                                            value_of(theirs, "tags")))
      end
      if keys.include?("body")
        body = merge_body(value_of(base, "body"), value_of(ours, "body"),
                          value_of(theirs, "body"), ours, theirs, event)
        assign(merged, "body", body)
      end

      merge_delegation!(merged, base, ours, theirs, event) if keys.include?(Delegation::FIELD)
      merge_state!(merged, base, ours, theirs, event) if keys.include?("state")
      # Runs after both, because the rule reads the resolved state and rewrites
      # the resolved delegation.
      settle_delegation_state!(merged, event)
      updated = UpdateStamp.max(value_of(ours, "updated"), value_of(theirs, "updated"))
      assign(merged, "updated", updated)

      events << event
      merged
    end

    def merge_scalar(field, base_value, ours_value, theirs_value, ours, theirs, event)
      return ours_value if ours_value == theirs_value
      return theirs_value if ours_value == base_value
      return ours_value if theirs_value == base_value

      event[:conflicts] << field
      winner = lww_side(ours, theirs, event, field)
      winner == :ours ? ours_value : theirs_value
    end

    # Resolve a date and its time metadata as one unit: the classic 3-way rules
    # apply to the [date, time] tuple, and a genuine conflict is decided once,
    # with the winning side supplying both halves.
    def merge_temporal_pair!(merged, date_field, time_field, base, ours, theirs, event)
      base_pair = [value_of(base, date_field), value_of(base, time_field)]
      ours_pair = [value_of(ours, date_field), value_of(ours, time_field)]
      theirs_pair = [value_of(theirs, date_field), value_of(theirs, time_field)]

      winner = if ours_pair == theirs_pair then ours_pair
               elsif ours_pair == base_pair then theirs_pair
               elsif theirs_pair == base_pair then ours_pair
               else
                 event[:conflicts] << date_field
                 lww_side(ours, theirs, event, date_field) == :ours ? ours_pair : theirs_pair
               end
      assign(merged, date_field, winner[0])
      assign(merged, time_field, winner[1])
    end

    def merge_tags(base_tags, ours_tags, theirs_tags)
      base = Array(base_tags)
      union = (Array(ours_tags) + Array(theirs_tags)).uniq
      retained_base = base.select { |tag| union.include?(tag) }
      retained_base + (union - retained_base).sort
    end

    def merge_body(base_value, ours_value, theirs_value, ours, theirs, event)
      return ours_value if ours_value == theirs_value
      return theirs_value if ours_value == base_value
      return ours_value if theirs_value == base_value

      if ours_value.is_a?(String) && theirs_value.is_a?(String)
        return theirs_value if theirs_value.start_with?(ours_value)
        return ours_value if ours_value.start_with?(theirs_value)
      end

      event[:conflicts] << "body"
      lww_side(ours, theirs, event, "body") == :ours ? ours_value : theirs_value
    end

    # Resolve the delegation marker as ONE atomic value: the merged record takes
    # exactly one side's whole object, so a claim can never be spliced together
    # from two devices.
    #
    # The winner is picked by a SINGLE total order over the two values, never by
    # asking which side is "ours" or which record was written last. That matters
    # because the merge is applied pairwise: an earlier rule that ordered claims
    # by `at` but everything else by record-level last-write-wins is
    # non-associative, and three devices could converge on DIFFERENT holders
    # depending on the order their clones happened to sync. A maximum over one
    # total order is associative and commutative, which is what makes "exactly
    # one worker holds a claim" hold across multi-device merges. The order is:
    #
    # - removal absorbs everything: if either side dropped a marker the base
    #   carried, the merge drops it. Owner revocation (undelegate) is the always
    #   -wins override, and the escape hatch for every rule below;
    # - a `claimed` marker beats any non-claimed one, so a live claim is never
    #   silently downgraded to `ready` (or replaced) by a concurrent edit that
    #   did not go through revocation;
    # - two claims: earlier `at`, then the lexicographically smaller assignee,
    #   then canonical bytes — first claim wins, and the loser discovers it at
    #   its next worker-matched operation;
    # - two non-claimed markers: later `at`, then canonical bytes — the most
    #   recent owner intent.
    def merge_delegation!(merged, base, ours, theirs, event)
      field = Delegation::FIELD
      base_value = value_of(base, field)
      ours_value = value_of(ours, field)
      theirs_value = value_of(theirs, field)
      return assign(merged, field, ours_value) if ours_value == theirs_value

      removed = !base_value.nil? && (ours_value.nil? || theirs_value.nil?)
      winner = removed ? nil : delegation_max(ours_value, theirs_value)
      # A change only one side made is reported as a conflict only when the
      # order OVERRULES it — a released claim the other device still holds, say.
      # Confirming a one-sided edit is the ordinary field rule, and silent.
      other = ours_value == base_value ? theirs_value : ours_value
      if [ours_value, theirs_value].include?(base_value) && winner == other
        return assign(merged, field, winner)
      end

      event[:conflicts] << field
      event[:delegation] = { reason: removed ? :removal_wins : delegation_reason(ours_value, theirs_value),
                             holder: winner && winner["assignee"] }
      assign(merged, field, winner)
    end

    # The greater of two markers under the total order documented above. `nil`
    # (a side with no marker, with none in the base to have removed) ranks below
    # every object, so a concurrent add still wins over "absent".
    def delegation_max(left, right)
      return left if right.nil?
      return right if left.nil?

      left_claimed = Delegation.claimed?(left)
      return left_claimed ? left : right if left_claimed != Delegation.claimed?(right)

      by_at = left["at"].to_s <=> right["at"].to_s
      # A claim is ranked by when it was taken (first wins); any other marker by
      # when the owner last stated it (most recent wins).
      return (left_claimed ? by_at.negative? : by_at.positive?) ? left : right unless by_at.zero?

      if left_claimed
        by_assignee = left["assignee"].to_s <=> right["assignee"].to_s
        return by_assignee.negative? ? left : right unless by_assignee.zero?
      end
      # Canonical bytes make the order total: two objects that tie on every
      # meaningful key still resolve the same way on both devices.
      delegation_bytes(left) <= delegation_bytes(right) ? left : right
    end

    def delegation_bytes(value) = JSON.generate(Delegation.ordered(value))

    def delegation_reason(ours_value, theirs_value)
      ours_claimed = Delegation.claimed?(ours_value)
      return :claim_holds unless ours_claimed == Delegation.claimed?(theirs_value)

      ours_claimed ? :earlier_claim : :later_intent
    end

    # Reconcile the resolved marker with the rest of the resolved record,
    # because the parts were decided independently and only some combinations
    # are legal:
    #
    # - only a task can carry a delegation; a record whose `type` resolved to
    #   something else drops it (defensive — no CLI or API operation turns a
    #   delegated task into a section — but the alternative is aborting the
    #   whole merge, which would block device sync until a hand repair);
    # - closed keeps a claim or a human delegation verbatim (who did it, and
    #   where), but drops a marker still merely `ready` — nothing happened, the
    #   same normalization a local close performs;
    # - a task the other side turned back into a proposal carries no delegation
    #   at all.
    #
    # Without this the merge could emit a record Check rejects, failing the
    # whole merge over a pair of individually legal sides.
    def settle_delegation_state!(merged, event)
      delegation = merged[Delegation::FIELD]
      return unless Delegation.object?(delegation)

      reason = if merged["type"] != "task" then :cleared_on_non_task
               elsif Check::PROPOSED_STATES.include?(merged["state"]) then :cleared_on_proposal
               elsif TERMINAL_STATES.include?(merged["state"]) && Delegation.ready?(delegation)
                 :cleared_on_close
               end
      return unless reason

      merged.delete(Delegation::FIELD)
      prior = event[:delegation]
      event[:delegation] = prior ? prior.merge(cleared: reason) : { reason: reason }
    end

    def merge_state!(merged, base, ours, theirs, event)
      base_state = value_of(base, "state")
      ours_state = value_of(ours, "state")
      theirs_state = value_of(theirs, "state")
      state, winner = resolve_state(base_state, ours_state, theirs_state, ours, theirs, event)
      assign(merged, "state", state)

      closed = if winner == :ours
                 value_of(ours, "closed")
               elsif winner == :theirs
                 value_of(theirs, "closed")
               else
                 merge_scalar("closed", value_of(base, "closed"), value_of(ours, "closed"),
                              value_of(theirs, "closed"), ours, theirs, event)
               end
      closed = nil unless TERMINAL_STATES.include?(state)
      assign(merged, "closed", closed)
    end

    def resolve_state(base_state, ours_state, theirs_state, ours, theirs, event)
      return [ours_state, :both] if ours_state == theirs_state
      return [theirs_state, :theirs] if ours_state == base_state
      return [ours_state, :ours] if theirs_state == base_state

      ours_terminal = TERMINAL_STATES.include?(ours_state)
      theirs_terminal = TERMINAL_STATES.include?(theirs_state)
      if ours_terminal != theirs_terminal
        event[:conflicts] << "state"
        return ours_terminal ? [ours_state, :ours] : [theirs_state, :theirs]
      end

      event[:conflicts] << "state"
      winner = lww_side(ours, theirs, event, "state")
      [winner == :ours ? ours_state : theirs_state, winner]
    end

    def lww_side(ours, theirs, event, field)
      ours_stamp = value_of(ours, "updated")
      theirs_stamp = value_of(theirs, "updated")
      comparison = UpdateStamp.compare(ours_stamp, theirs_stamp)
      return :ours if comparison.positive?
      return :theirs if comparison.negative?

      if ours_stamp.nil? && theirs_stamp.nil?
        event[:low_confidence] << field
        return :ours
      end

      # Equal valid stamps can occur after a common prior merge. Break that tie
      # by the complete record bytes so swapping ours/theirs stays commutative.
      Format.dump_record(clean(ours)) >= Format.dump_record(clean(theirs)) ? :ours : :theirs
    end

    def order_records(merged_by_id, ours, theirs, base)
      meta = clean(ours.find { |record| record["type"] == "meta" } ||
                   theirs.find { |record| record["type"] == "meta" } ||
                   base.find { |record| record["type"] == "meta" })
      ours_rank = ranks(ours)
      theirs_rank = ranks(theirs)
      base_rank = ranks(base)
      children = Hash.new { |hash, parent| hash[parent] = [] }
      merged_by_id.each_value { |record| children[record["parent"]] << record }
      children.each_value do |siblings|
        siblings.sort_by! do |record|
          id = record["id"]
          if ours_rank.key?(id)
            [0, ours_rank[id]]
          elsif theirs_rank.key?(id)
            [1, theirs_rank[id]]
          else
            [2, base_rank.fetch(id, Float::INFINITY)]
          end
        end
      end

      ordered = [meta]
      visit = lambda do |record|
        ordered << record
        children[record["id"]].each { |child| visit.call(child) }
      end
      children[nil].each { |record| visit.call(record) }

      missing = merged_by_id.keys - ordered.filter_map { |record| record["id"] }
      raise MergeError, "merged records have missing or cyclic parents: #{missing.join(", ")}" unless missing.empty?

      ordered
    end

    def ranks(records)
      records.each_with_index.each_with_object({}) do |(record, index), by_id|
        by_id[record["id"]] = index if record["id"]
      end
    end

    def clean(record)
      return nil unless record

      record.reject { |key, _| key == "line" }
    end

    def record_equal?(left, right) = clean(left) == clean(right)
    def value_of(record, field) = record && record[field]

    def assign(record, field, value)
      value.nil? || (value.respond_to?(:empty?) && value.empty?) ? record.delete(field) : record[field] = value
    end

    def format_event(event)
      details = []
      details << "conflicts=#{event[:conflicts].uniq.join(",")}" unless Array(event[:conflicts]).empty?
      unless Array(event[:low_confidence]).empty?
        details << "low-confidence=#{event[:low_confidence].uniq.join(",")}"
      end
      if (delegation = event[:delegation])
        details << "delegation=#{[delegation[:reason], delegation[:cleared]].compact.join("+")}"
        details << "holder=#{delegation[:holder]}" if delegation[:holder]
      end
      "  #{event[:id]} #{event[:decision]}#{details.empty? ? "" : " #{details.join(" ")}"}"
    end
  end
end
