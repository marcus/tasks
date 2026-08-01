# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "rbconfig"
require "tasks/jsonl_merge"

class TestJsonlMerge < Minitest::Test
  BIN = File.expand_path("../bin/tasks", __dir__)
  HOME_STAMP = "2026-07-16T10:00:00Z#home"
  WORK_STAMP = "2026-07-16T11:00:00Z#work"

  def base_records
    [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "10000001", "title" => "Work" },
      { "type" => "task", "id" => "10000002", "parent" => "10000001", "state" => "NEXT",
        "title" => "Book Sixt car", "tags" => ["@computer"], "scheduled" => "2026-07-18",
        "body" => "Reservation started." },
      { "type" => "task", "id" => "10000003", "parent" => "10000001", "state" => "TODO",
        "title" => "Call PSE" },
      { "type" => "task", "id" => "10000004", "parent" => "10000001", "state" => "TODO",
        "title" => "Review Stash" },
    ]
  end

  def copy(records)
    Tasks::Format.parse(Tasks::Format.dump(records)).records.map { |record| record.reject { |key, _| key == "line" } }
  end

  def change(records, id, **fields)
    changed = copy(records)
    record = changed.find { |entry| entry["id"] == id }
    fields.each { |key, value| value.nil? ? record.delete(key.to_s) : record[key.to_s] = value }
    changed
  end

  def merge(base, ours, theirs)
    result = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(base),
      ours_text: Tasks::Format.dump(ours),
      theirs_text: Tasks::Format.dump(theirs)
    )
    assert result.ok?, result.error
    [Tasks::Format.parse(result.text).records, result]
  end

  def find(records, id)
    records.find { |record| record["id"] == id }
  end

  def test_non_overlapping_fields_merge_without_conflict
    ours = change(base_records, "10000002", tags: ["@computer", "travel"], updated: HOME_STAMP)
    theirs = change(base_records, "10000002", scheduled: "2026-07-19", updated: WORK_STAMP)

    records, result = merge(base_records, ours, theirs)
    task = find(records, "10000002")

    assert_equal ["@computer", "travel"], task["tags"]
    assert_equal "2026-07-19", task["scheduled"]
    assert_equal WORK_STAMP, task["updated"]
    assert_empty result.events.first[:conflicts]
  end

  def test_same_field_uses_newer_updated_and_is_commutative
    ours = change(base_records, "10000003", title: "Call utility", updated: HOME_STAMP)
    theirs = change(base_records, "10000003", title: "Call PSE billing", updated: WORK_STAMP)

    forward_records, = merge(base_records, ours, theirs)
    reverse_records, = merge(base_records, theirs, ours)

    assert_equal "Call PSE billing", find(forward_records, "10000003")["title"]
    assert_equal Tasks::Format.dump(forward_records), Tasks::Format.dump(reverse_records)
  end

  def test_pre_timestamp_conflict_is_ours_wins_and_logged_low_confidence
    ours = change(base_records, "10000003", title: "Ours title")
    theirs = change(base_records, "10000003", title: "Theirs title")

    records, result = merge(base_records, ours, theirs)

    assert_equal "Ours title", find(records, "10000003")["title"]
    assert_includes result.events.first[:low_confidence], "title"
    assert_includes result.log_lines.join("\n"), "low-confidence=title"
  end

  def test_temporal_pair_conflict_takes_the_whole_pair_from_the_lww_winner
    base = change(base_records, "10000002",
                  scheduled: "2026-07-20", scheduled_time: { "local" => "09:00", "timezone" => "America/Los_Angeles" })
    ours = change(base, "10000002", scheduled: "2026-07-21", updated: HOME_STAMP)
    theirs = change(base, "10000002",
                    scheduled: "2026-07-25", scheduled_time: { "local" => "14:00" }, updated: WORK_STAMP)

    records, result = merge(base, ours, theirs)
    task = find(records, "10000002")

    assert_equal "2026-07-25", task["scheduled"]
    assert_equal({ "local" => "14:00" }, task["scheduled_time"])
    assert_includes result.events.first[:conflicts], "scheduled"

    reverse_records, = merge(base, theirs, ours)
    assert_equal Tasks::Format.dump(records), Tasks::Format.dump(reverse_records)
  end

  def test_temporal_pair_single_side_change_wins_without_conflict
    base = change(base_records, "10000002",
                  scheduled: "2026-07-20", scheduled_time: { "local" => "09:00" })
    ours = change(base, "10000002", tags: ["@computer", "travel"], updated: HOME_STAMP)
    theirs = change(base, "10000002",
                    scheduled: "2026-07-22", scheduled_time: { "local" => "17:00", "timezone" => "Europe/London" },
                    updated: WORK_STAMP)

    records, result = merge(base, ours, theirs)
    task = find(records, "10000002")

    assert_equal "2026-07-22", task["scheduled"]
    assert_equal({ "local" => "17:00", "timezone" => "Europe/London" }, task["scheduled_time"])
    assert_equal ["@computer", "travel"], task["tags"]
    assert_empty result.events.first[:conflicts]
  end

  def test_undate_vs_retime_never_emits_orphan_time_metadata
    base = change(base_records, "10000002",
                  scheduled: "2026-07-20", scheduled_time: { "local" => "09:00", "timezone" => "Europe/London" })
    ours = change(base, "10000002", scheduled: nil, scheduled_time: nil, updated: WORK_STAMP)
    theirs = change(base, "10000002", scheduled_time: { "local" => "10:30", "timezone" => "Europe/London" },
                    updated: HOME_STAMP)

    records, result = merge(base, ours, theirs)
    task = find(records, "10000002")

    refute task.key?("scheduled"), "ours undate should win the whole pair"
    refute task.key?("scheduled_time"), "orphan time metadata must never survive"
    assert_includes result.events.first[:conflicts], "scheduled"
  end

  # -- delegation ------------------------------------------------------------

  def ready(mode: "research", at: "2026-07-27T18:00:00Z", **extra)
    { "kind" => "agent", "mode" => mode, "status" => "ready", "at" => at }
      .merge(extra.transform_keys(&:to_s))
  end

  def claim(assignee, at:, mode: "research", **extra)
    { "kind" => "agent", "mode" => mode, "status" => "claimed", "assignee" => assignee, "at" => at }
      .merge(extra.transform_keys(&:to_s))
  end

  def delegation_of(records, id) = find(records, id)["delegation"]

  def test_one_sided_delegation_change_wins_without_conflict
    ours = change(base_records, "10000002", tags: ["@computer", "travel"], updated: HOME_STAMP)
    theirs = change(base_records, "10000002", delegation: ready, updated: WORK_STAMP)

    records, result = merge(base_records, ours, theirs)

    assert_equal ready, delegation_of(records, "10000002")
    assert_empty result.events.first[:conflicts]
  end

  def test_concurrent_claims_resolve_to_the_earlier_at_symmetrically
    base = change(base_records, "10000002", delegation: ready)
    early = claim("worker/zzz", at: "2026-07-27T18:04:11Z")
    late = claim("worker/aaa", at: "2026-07-27T18:09:00Z")
    ours = change(base, "10000002", delegation: late, updated: HOME_STAMP)
    theirs = change(base, "10000002", delegation: early, updated: WORK_STAMP)

    records, result = merge(base, ours, theirs)
    reverse, = merge(base, theirs, ours)

    assert_equal early, delegation_of(records, "10000002"), "first claim holds the task"
    assert_equal Tasks::Format.dump(records), Tasks::Format.dump(reverse)
    assert_includes result.events.first[:conflicts], "delegation"
    assert_equal :earlier_claim, result.events.first[:delegation][:reason]
    assert_includes result.log_lines.join("\n"), "delegation=earlier_claim holder=worker/zzz"
  end

  def test_simultaneous_claims_tiebreak_on_the_smaller_assignee
    base = change(base_records, "10000002", delegation: ready)
    at = "2026-07-27T18:04:11Z"
    ours = change(base, "10000002", delegation: claim("worker/bbb", at: at), updated: HOME_STAMP)
    theirs = change(base, "10000002", delegation: claim("worker/aaa", at: at), updated: WORK_STAMP)

    records, = merge(base, ours, theirs)
    reverse, = merge(base, theirs, ours)

    assert_equal "worker/aaa", delegation_of(records, "10000002")["assignee"]
    assert_equal Tasks::Format.dump(records), Tasks::Format.dump(reverse)
  end

  def test_delegation_is_taken_whole_never_field_by_field
    base = change(base_records, "10000002", delegation: ready)
    ours = change(base, "10000002",
                  delegation: claim("worker/zzz", at: "2026-07-27T18:04:11Z", mode: "implement",
                                    work_ref: "https://example.com/ours"),
                  updated: HOME_STAMP)
    theirs = change(base, "10000002",
                    delegation: claim("worker/aaa", at: "2026-07-27T18:20:00Z", mode: "refine",
                                      work_ref: "https://example.com/theirs"),
                    updated: WORK_STAMP)

    records, = merge(base, ours, theirs)
    merged = delegation_of(records, "10000002")

    assert_equal ours.find { |record| record["id"] == "10000002" }["delegation"], merged,
                 "the winning side supplies every field of the object"
  end

  def test_owner_undelegate_beats_a_concurrent_claim_in_either_direction
    base = change(base_records, "10000002", delegation: ready)
    revoked = change(base, "10000002", delegation: nil, updated: HOME_STAMP)
    claimed = change(base, "10000002", delegation: claim("worker/aaa", at: "2026-07-27T18:04:11Z"),
                     updated: WORK_STAMP)

    forward, result = merge(base, revoked, claimed)
    reverse, = merge(base, claimed, revoked)

    refute find(forward, "10000002").key?("delegation"), "revocation wins"
    assert_equal Tasks::Format.dump(forward), Tasks::Format.dump(reverse)
    assert_equal :removal_wins, result.events.first[:delegation][:reason]
  end

  def test_owner_undelegate_against_an_unchanged_side_simply_removes_it
    base = change(base_records, "10000002", delegation: ready)
    revoked = change(base, "10000002", delegation: nil, updated: HOME_STAMP)

    records, result = merge(base, revoked, base)

    refute find(records, "10000002").key?("delegation")
    refute_includes Array(result.events.first[:conflicts]), "delegation"
  end

  def test_non_claim_delegation_conflicts_take_the_most_recent_owner_intent
    base = change(base_records, "10000002", delegation: ready(mode: "refine"))
    ours = change(base, "10000002", delegation: ready(mode: "implement", at: "2026-07-27T19:00:00Z"),
                  updated: HOME_STAMP)
    theirs = change(base, "10000002",
                    delegation: { "kind" => "human", "status" => "delegated",
                                  "assignee" => "pat@example.com", "at" => "2026-07-27T20:00:00Z" },
                    updated: WORK_STAMP)

    records, result = merge(base, ours, theirs)
    reverse, = merge(base, theirs, ours)

    assert_equal "human", delegation_of(records, "10000002")["kind"], "the newer intent wins"
    assert_equal Tasks::Format.dump(records), Tasks::Format.dump(reverse)
    assert_equal :later_intent, result.events.first[:delegation][:reason]
  end

  def test_close_against_claim_keeps_provenance_but_drops_a_ready_marker
    base = change(base_records, "10000002", delegation: ready)
    claimed = claim("worker/aaa", at: "2026-07-27T18:04:11Z", work_ref: "https://example.com/pr/42")
    ours = change(base, "10000002", state: "DONE", closed: "2026-07-27", updated: HOME_STAMP)
    theirs = change(base, "10000002", delegation: claimed, updated: WORK_STAMP)

    records, = merge(base, ours, theirs)
    task = find(records, "10000002")
    assert_equal "DONE", task["state"]
    assert_equal claimed, task["delegation"], "who did it and where survives the close"

    # The same close against an untouched ready marker records nothing.
    unclaimed, result = merge(base, ours, base)
    refute find(unclaimed, "10000002").key?("delegation")
    assert_equal :cleared_on_close, result.events.first[:delegation][:reason]
  end

  # Both sides are individually legal; only their combination is not, and the
  # merge must normalize rather than abort over it.
  def test_delegation_against_a_concurrent_proposal_is_dropped_not_fatal
    base = change(base_records, "10000002", delegation: nil)
    ours = change(base, "10000002", delegation: ready, updated: HOME_STAMP)
    theirs = change(base, "10000002", state: "PROPOSED", updated: WORK_STAMP)

    records, result = merge(base, ours, theirs)
    task = find(records, "10000002")

    assert_equal "PROPOSED", task["state"]
    refute task.key?("delegation")
    assert_equal :cleared_on_proposal, result.events.first[:delegation][:reason]
    assert Tasks::Check.check_text(result.text).ok?
  end

  def test_merged_delegation_still_validates_and_lands_in_canonical_order
    base = change(base_records, "10000002", delegation: ready)
    ours = change(base, "10000002",
                  delegation: claim("worker/aaa", at: "2026-07-27T18:04:11Z",
                                    work_ref: "https://example.com/pr/42"),
                  updated: HOME_STAMP)
    theirs = change(base, "10000002", title: "Book the Sixt car", updated: WORK_STAMP)

    _records, result = merge(base, ours, theirs)

    assert Tasks::Check.check_text(result.text).ok?
    assert_includes result.text,
                    %("delegation":{"kind":"agent","mode":"research","status":"claimed",) +
                    %("assignee":"worker/aaa","at":"2026-07-27T18:04:11Z",) +
                    %("work_ref":"https://example.com/pr/42"})
  end

  # The regression that motivated the single total order: with `at` deciding
  # two claims but record-level last-write-wins deciding everything else, the
  # holder depended on the order the devices happened to sync in. Device A
  # claims first, the owner then widens the mode on device C, device B claims
  # last: (A+B)+C used to hold A's claim while (A+C)+B installed B's, so two
  # devices each believed a DIFFERENT worker owned the task.
  def test_pairwise_merge_order_cannot_change_the_claim_holder
    base = change(base_records, "10000002", delegation: ready(at: "2026-07-27T09:00:00Z"))
    first = change(base, "10000002", delegation: claim("worker/aaa", at: "2026-07-27T10:00:00Z"),
                   updated: "2026-07-27T10:00:00Z#a")
    widened = change(base, "10000002", delegation: ready(mode: "implement", at: "2026-07-27T10:10:00Z"),
                     updated: "2026-07-27T10:10:00Z#c")
    second = change(base, "10000002", delegation: claim("worker/bbb", at: "2026-07-27T10:30:00Z"),
                    updated: "2026-07-27T10:30:00Z#b")

    holders = [[first, second, widened], [first, widened, second], [second, widened, first]].map do |x, y, z|
      pair, = merge(base, x, y)
      whole, = merge(base, copy(pair), z)
      delegation_of(whole, "10000002")
    end

    assert_equal 1, holders.uniq.length, "every sync order must converge on one holder: #{holders.inspect}"
    assert_equal "worker/aaa", holders.first["assignee"], "the first claim holds the task"
  end

  # A live claim is never silently downgraded by a concurrent non-removal edit:
  # the owner's release loses to the claim it is racing, and the claim's holder
  # and work_ref — the provenance the feature exists to keep — survive the close
  # that the other device performed.
  def test_a_live_claim_outranks_a_concurrent_release_and_keeps_its_provenance
    held = claim("worker/aaa", at: "2026-07-27T10:00:00Z", work_ref: "https://example.com/pr/42")
    base = change(base_records, "10000002", delegation: held)
    closed = change(base, "10000002", state: "DONE", closed: "2026-07-27", updated: HOME_STAMP)
    released = change(base, "10000002", delegation: ready(at: "2026-07-27T10:02:00Z"),
                      updated: WORK_STAMP)

    records, result = merge(base, closed, released)
    reverse, = merge(base, released, closed)
    task = find(records, "10000002")

    assert_equal "DONE", task["state"]
    assert_equal held, task["delegation"], "the holder and work_ref must survive the close"
    assert_equal Tasks::Format.dump(records), Tasks::Format.dump(reverse)
    assert_equal :claim_holds, result.events.first[:delegation][:reason]
  end

  # Owner revocation stays the escape hatch that beats even a live claim, in
  # both directions and whatever the other side did.
  def test_removal_still_absorbs_a_live_claim_from_either_side
    base = change(base_records, "10000002",
                  delegation: claim("worker/aaa", at: "2026-07-27T10:00:00Z"))
    revoked = change(base, "10000002", delegation: nil, updated: HOME_STAMP)
    reclaimed = change(base, "10000002",
                       delegation: claim("worker/bbb", at: "2026-07-27T09:00:00Z"),
                       updated: WORK_STAMP)

    forward, result = merge(base, revoked, reclaimed)
    reverse, = merge(base, reclaimed, revoked)

    refute find(forward, "10000002").key?("delegation"), "undelegate always wins"
    assert_equal Tasks::Format.dump(forward), Tasks::Format.dump(reverse)
    assert_equal :removal_wins, result.events.first[:delegation][:reason]
  end

  # Defensive: no operation turns a delegated task into a section, but a record
  # whose merged `type` resolves that way must normalize rather than abort the
  # whole merge and block device sync until a hand repair.
  def test_delegation_on_a_record_that_merged_into_a_section_is_dropped_not_fatal
    base = copy(base_records)
    ours = change(base, "10000003",
                  delegation: claim("worker/aaa", at: "2026-07-27T18:04:11Z"), updated: HOME_STAMP)
    theirs = copy(base)
    theirs.find { |record| record["id"] == "10000003" }
          .replace({ "type" => "section", "id" => "10000003", "parent" => "10000001",
                     "title" => "Call PSE", "updated" => WORK_STAMP })

    records, result = merge(base, ours, theirs)
    record = find(records, "10000003")

    assert_equal "section", record["type"]
    refute record.key?("delegation")
    assert_equal :cleared_on_non_task, result.events.first[:delegation][:reason]
    assert Tasks::Check.check_text(result.text).ok?
  end

  # Delegation resolution must be a maximum over ONE total order, which makes it
  # associative and commutative: no sequence of pairwise device syncs can end on
  # two different markers. States stay live here on purpose — clearing a marker
  # on a closed or proposed task is the state machine's rule, and the state
  # merge's own ordering is out of this property's scope.
  def test_delegation_resolution_is_order_independent_across_three_devices
    rng = Random.new(20_260_727)
    stamps = ["2026-07-27T09:00:00Z", "2026-07-27T10:00:00Z", "2026-07-27T11:00:00Z"]
    shapes = lambda do
      [nil,
       ready(mode: %w[refine research implement].sample(random: rng), at: stamps.sample(random: rng)),
       claim("worker/#{%w[aaa bbb ccc].sample(random: rng)}", at: stamps.sample(random: rng),
             mode: %w[refine research implement].sample(random: rng)),
       claim("worker/#{%w[aaa bbb].sample(random: rng)}", at: stamps.sample(random: rng),
             work_ref: "https://example.com/#{rng.rand(2)}"),
       { "kind" => "human", "status" => "delegated", "at" => stamps.sample(random: rng),
         "assignee" => "p#{rng.rand(2)}@example.com" }].sample(random: rng)
    end
    side = lambda do |base, index|
      change(base, "10000002", delegation: shapes.call,
             state: %w[TODO NEXT WAITING].sample(random: rng),
             updated: "2026-07-27T1#{rng.rand(3)}:0#{rng.rand(6)}:00Z#d#{index}")
    end

    300.times do
      base = change(base_records, "10000002", delegation: shapes.call)
      devices = Array.new(3) { |index| side.call(base, index) }

      outcomes = [[0, 1, 2], [0, 2, 1], [1, 2, 0], [2, 1, 0], [1, 0, 2], [2, 0, 1]].map do |x, y, z|
        pair, = merge(base, devices[x], devices[y])
        whole, = merge(base, copy(pair), devices[z])
        delegation_of(whole, "10000002")
      end

      assert_equal 1, outcomes.uniq.length,
                   "sync order changed the marker:\n  base=#{base_delegation(base)}\n" \
                   "  devices=#{devices.map { |records| base_delegation(records) }.join("\n          ")}\n" \
                   "  outcomes=#{outcomes.uniq.inspect}"
    end
  end

  def base_delegation(records) = JSON.generate(find(records, "10000002")["delegation"])

  # v1 is not merged, on either SIDE. Reconciling records field by field across
  # a schema boundary is exactly the silent corruption the version header exists
  # to prevent, and there is no migration to point the operator at any more.
  def test_either_side_at_another_schema_version_refuses_the_merge
    %w[ours theirs].each do |side|
      sides = { base: copy(base_records), ours: copy(base_records), theirs: copy(base_records) }
      sides[side.to_sym].first["version"] = 1

      result = Tasks::JsonlMerge.merge(
        base_text: Tasks::Format.dump(sides[:base]),
        ours_text: Tasks::Format.dump(sides[:ours]),
        theirs_text: Tasks::Format.dump(sides[:theirs])
      )

      refute result.ok?, "#{side} at v1 must refuse"
      assert_equal "#{side} is schema v1; this binary reads schema v2 only", result.error
      refute_match(/migrat/i, result.error)
    end
  end

  # The BASE is not a side. It is consulted to tell "changed" from "unchanged",
  # never merged, so an ancestor older than both sides is safe — and it is the
  # ordinary shape of a merge that reaches back past a schema upgrade. Marcus's
  # task-data repo has nine commits carrying a schema-v1 tasks.jsonl before the
  # 2026-07-16 upgrade, so `merge`, `rebase`, `cherry-pick`, and `revert` can
  # all still produce this base today. Refusing it aborted the merge outright.
  def test_a_base_older_than_both_sides_still_merges
    old_base = copy(base_records)
    old_base.first["version"] = 1
    ours = change(base_records, "10000002", title: "Ours edited")
    theirs = change(base_records, "10000003", title: "Theirs edited")

    result = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(old_base),
      ours_text: Tasks::Format.dump(ours),
      theirs_text: Tasks::Format.dump(theirs)
    )

    assert result.ok?, "a v1 base under v2 sides must merge: #{result.error}"
    records = Tasks::Format.parse(result.text).records
    # Both sides' independent edits survive, and the output is written at the
    # version this binary implements — never downgraded to the ancestor's.
    assert_equal "Ours edited", find(records, "10000002")["title"]
    assert_equal "Theirs edited", find(records, "10000003")["title"]
    assert_equal Tasks::Format::VERSION, records.first["version"]
  end

  # A base NEWER than this binary is still refused: an ancestor ahead of both
  # sides means this build is the stale one and cannot know what its records
  # meant, so "unchanged since base" is a comparison it cannot make.
  def test_a_base_newer_than_this_binary_refuses_the_merge
    future_base = copy(base_records)
    future_base.first["version"] = 3

    result = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(future_base),
      ours_text: Tasks::Format.dump(base_records),
      theirs_text: Tasks::Format.dump(base_records)
    )

    refute result.ok?
    assert_equal "base is schema v3; this binary reads schema v2 only", result.error
  end

  def test_a_future_schema_version_on_a_side_refuses_the_merge
    v3 = copy(base_records)
    v3.first["version"] = 3

    result = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(base_records),
      ours_text: Tasks::Format.dump(base_records),
      theirs_text: Tasks::Format.dump(v3)
    )

    refute result.ok?
    assert_equal "theirs is schema v3; this binary reads schema v2 only", result.error
  end

  def test_tags_union_preserves_base_order_and_sorts_concurrent_additions
    base = change(base_records, "10000002", tags: %w[@computer important])
    ours = change(base, "10000002", tags: %w[@computer important zeta], updated: HOME_STAMP)
    theirs = change(base, "10000002", tags: %w[@computer important alpha], updated: WORK_STAMP)

    records, = merge(base, ours, theirs)

    assert_equal %w[@computer important alpha zeta], find(records, "10000002")["tags"]
  end

  def test_progressed_state_beats_open_state_and_carries_closed_date
    ours = change(base_records, "10000002", state: "DONE", closed: "2026-07-16", updated: HOME_STAMP)
    theirs = change(base_records, "10000002", state: "TODO", updated: WORK_STAMP)

    records, = merge(base_records, ours, theirs)
    task = find(records, "10000002")

    assert_equal "DONE", task["state"]
    assert_equal "2026-07-16", task["closed"]
  end

  def test_body_prefix_chooses_longer_append
    ours = change(base_records, "10000002", body: "Reservation started.\nConfirmation 1", updated: HOME_STAMP)
    theirs = change(ours, "10000002", body: "Reservation started.\nConfirmation 1\nConfirmation 2",
                     updated: WORK_STAMP)

    records, = merge(base_records, ours, theirs)

    assert_equal "Reservation started.\nConfirmation 1\nConfirmation 2", find(records, "10000002")["body"]
  end

  def test_delete_vs_unchanged_deletes_but_delete_vs_edit_keeps_edit
    ours_deleted = copy(base_records).reject { |record| record["id"] == "10000003" }
    unchanged_records, = merge(base_records, ours_deleted, base_records)
    assert_nil find(unchanged_records, "10000003")

    edited = change(base_records, "10000003", title: "Edited concurrently", updated: WORK_STAMP)
    edited_records, result = merge(base_records, ours_deleted, edited)
    assert_equal "Edited concurrently", find(edited_records, "10000003")["title"]
    assert_equal :kept_theirs_edit_over_ours_delete, result.events.first[:decision]
  end

  def test_subtree_delete_vs_descendant_edit_restores_required_ancestor_chain
    nested = copy(base_records)
    find(nested, "10000003")["parent"] = "10000002"
    ours_deleted = nested.reject { |record| %w[10000002 10000003].include?(record["id"]) }
    theirs = change(nested, "10000003", title: "Edited nested task", updated: WORK_STAMP)

    records, result = merge(nested, ours_deleted, theirs)

    assert_equal "10000002", find(records, "10000003")["parent"]
    assert find(records, "10000002"), "the deleted ancestor is restored to keep the edited child valid"
    assert_includes result.events.map { |event| event[:decision] }, :restored_ancestor_for_edited_descendant
    assert Tasks::Check.check_text(result.text).ok?
  end

  def test_adds_from_both_sides_are_kept_in_valid_ours_first_order
    ours = copy(base_records)
    ours << { "type" => "task", "id" => "10000005", "parent" => "10000001", "state" => "TODO",
              "title" => "Ours add", "updated" => HOME_STAMP }
    theirs = copy(base_records)
    theirs << { "type" => "task", "id" => "10000006", "parent" => "10000001", "state" => "TODO",
                "title" => "Theirs add", "updated" => WORK_STAMP }

    records, result = merge(base_records, ours, theirs)

    assert_equal "meta", records.first["type"]
    assert_operator records.index { |record| record["id"] == "10000005" }, :<,
                    records.index { |record| record["id"] == "10000006" }
    assert Tasks::Check.check_text(result.text).ok?
  end

  def test_theirs_only_parent_and_child_are_inserted_as_a_contiguous_subtree
    theirs = copy(base_records)
    theirs << { "type" => "section", "id" => "20000001", "title" => "Home" }
    theirs << { "type" => "task", "id" => "20000002", "parent" => "20000001", "state" => "TODO",
                "title" => "New child", "updated" => WORK_STAMP }

    records, result = merge(base_records, base_records, theirs)
    parent_index = records.index { |record| record["id"] == "20000001" }
    child_index = records.index { |record| record["id"] == "20000002" }

    assert_equal parent_index + 1, child_index
    assert Tasks::Check.check_text(result.text).ok?
  end

  def test_concurrent_reordering_uses_ours_and_is_logged
    base = base_records
    ours = [base[0], base[1], base[3], base[2], base[4]]
    theirs = [base[0], base[1], base[4], base[2], base[3]]

    records, result = merge(base, ours, theirs)

    task_ids = records.filter_map do |record|
      record["id"] if record["type"] == "task"
    end
    assert_equal %w[10000003 10000002 10000004], task_ids
    assert_includes result.events.map { |event| event[:decision] }, :ours_ordering_conflict
  end

  def test_malformed_or_duplicate_side_fails_without_text
    malformed = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(base_records),
      ours_text: "not-json\n",
      theirs_text: Tasks::Format.dump(base_records)
    )
    refute malformed.ok?
    assert_nil malformed.text

    duplicate = copy(base_records) << copy(base_records).last
    invalid = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(base_records),
      ours_text: Tasks::Format.dump(duplicate),
      theirs_text: Tasks::Format.dump(base_records)
    )
    refute invalid.ok?
    assert_includes invalid.error, "duplicate id"
  end

  def test_empty_base_supports_concurrent_first_archive_creation
    ours = [base_records.first, base_records[1], base_records[2]]
    theirs = [base_records.first, base_records[1], base_records[3]]

    result = Tasks::JsonlMerge.merge(
      base_text: "", ours_text: Tasks::Format.dump(ours), theirs_text: Tasks::Format.dump(theirs)
    )

    assert result.ok?, result.error
    records = Tasks::Format.parse(result.text).records
    assert find(records, "10000002")
    assert find(records, "10000003")
    assert Tasks::Check.check_text(result.text).ok?
  end

  def test_archive_vs_concurrent_edit_pair_is_rejected_by_cross_file_check
    live_base = base_records
    live_archiver = copy(live_base).reject { |record| record["id"] == "10000003" }
    live_editor = change(live_base, "10000003", title: "Edited while archiving", updated: WORK_STAMP)
    merged_live = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(live_base), ours_text: Tasks::Format.dump(live_archiver),
      theirs_text: Tasks::Format.dump(live_editor)
    )
    assert merged_live.ok?, merged_live.error

    archive_base = [live_base.first, { "type" => "section", "id" => "90000001", "title" => "Archive" }]
    archive_archiver = copy(archive_base)
    archive_archiver << {
      "type" => "task", "id" => "10000003", "parent" => "90000001", "state" => "DONE",
      "title" => "Call PSE", "closed" => "2026-07-16", "updated" => HOME_STAMP,
    }
    merged_archive = Tasks::JsonlMerge.merge(
      base_text: Tasks::Format.dump(archive_base), ours_text: Tasks::Format.dump(archive_archiver),
      theirs_text: Tasks::Format.dump(archive_base)
    )
    assert merged_archive.ok?, merged_archive.error

    Dir.mktmpdir do |dir|
      live_path = File.join(dir, "tasks.jsonl")
      archive_path = File.join(dir, "archive.jsonl")
      File.write(live_path, merged_live.text)
      File.write(archive_path, merged_archive.text)

      result = Tasks::Check.check_store(live_path, archive_path)

      refute result.ok?
      assert_includes result.errors.map(&:last).join("\n"), 'id "10000003" appears in both'
    end
  end

  def test_cli_driver_leaves_ours_untouched_on_failure_and_logs_it
    Dir.mktmpdir do |dir|
      base = File.join(dir, "base.jsonl")
      ours = File.join(dir, "ours.jsonl")
      theirs = File.join(dir, "theirs.jsonl")
      pathname = File.join(dir, "tasks.jsonl")
      File.write(base, Tasks::Format.dump(base_records))
      File.write(ours, Tasks::Format.dump(base_records))
      File.write(theirs, "<<<<<<< broken\n")
      before = File.binread(ours)

      _stdout, stderr, status = Open3.capture3(RbConfig.ruby, BIN, "merge-driver", base, ours, theirs, pathname)

      refute status.success?
      assert_includes stderr, "merge failed"
      assert_equal before, File.binread(ours)
      assert_includes File.read(File.join(dir, ".tasks-merge.log")), "failed"
    end
  end

  def test_real_world_sixt_pse_stash_divergence_matches_hand_resolution
    ours = copy(base_records)
    find(ours, "10000002")["tags"] = %w[@computer travel]
    find(ours, "10000002")["updated"] = HOME_STAMP
    find(ours, "10000003")["title"] = "Call PSE about final bill"
    find(ours, "10000003")["updated"] = HOME_STAMP

    theirs = copy(base_records)
    find(theirs, "10000002")["scheduled"] = "2026-07-19"
    find(theirs, "10000002")["updated"] = WORK_STAMP
    find(theirs, "10000004")["body"] = "Stash migration notes."
    find(theirs, "10000004")["updated"] = WORK_STAMP

    records, result = merge(base_records, ours, theirs)

    assert_equal %w[@computer travel], find(records, "10000002")["tags"]
    assert_equal "2026-07-19", find(records, "10000002")["scheduled"]
    assert_equal "Call PSE about final bill", find(records, "10000003")["title"]
    assert_equal "Stash migration notes.", find(records, "10000004")["body"]
    assert Tasks::Check.check_text(result.text).ok?
  end
end
