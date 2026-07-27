# frozen_string_literal: true

require_relative "test_helper"
require "tasks/store"

# Phase 1 of the delegation tranche: the schema object, its Store primitives,
# and the guarantees that make single pickup real (atomic claim, worker-matched
# release/work_ref, owner revocation, provenance on close).
class TestDelegation < Minitest::Test
  D = Tasks::Delegation
  WORKER = "claude-code/claude-fable-5/aaaa1111"
  RIVAL  = "claude-code/claude-opus-5/bbbb2222"
  NOW    = Time.utc(2026, 7, 27, 18, 4, 11)
  STAMP  = "2026-07-27T18:04:11Z"
  TODAY  = Date.new(2026, 7, 27)

  def build_store(org, archive, dir, now: NOW)
    Tasks::Store.new(org: org, archive: archive, journal_dir: File.join(dir, "journal"),
                     now: -> { now }, device: "test")
  end

  def with_delegation_store(records: FIXTURE_RECORDS, now: NOW)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, dump_fixture(records))
      yield build_store(org, archive, dir, now: now), org, archive, dir
    end
  end

  def delegation_in(path, id)
    records = Tasks::Format.parse(File.read(path, encoding: "UTF-8")).records
    records.find { |record| record["id"] == id }&.dig("delegation")
  end

  def line_for(path, id)
    File.read(path, encoding: "UTF-8").each_line.find { |line| line.include?(%("id":"#{id}")) }
  end

  # -- happy paths -----------------------------------------------------------

  def test_agent_delegation_claim_work_ref_and_release_round_trip
    with_delegation_store do |store, org, _archive|
      id = FIX[:plants]
      assert store.delegate_task!(id, kind: "agent", mode: "research").ok?
      assert_equal({ "kind" => "agent", "mode" => "research", "status" => "ready", "at" => STAMP },
                   delegation_in(org, id))

      # A read never grants ownership: listing leaves the marker exactly as is.
      store.reload!
      refute store.items.empty?
      assert_equal "ready", delegation_in(org, id)["status"]

      claimed = store.claim_task!(id, worker: WORKER)
      assert claimed.ok?, claimed.errors.inspect
      assert_equal :claim, claimed.summary[:action]
      assert_equal WORKER, claimed.summary[:delegation]["assignee"]

      assert store.set_work_ref!(id, "https://example.com/brief", worker: WORKER).ok?
      assert_includes line_for(org, id),
                      %("delegation":{"kind":"agent","mode":"research","status":"claimed",) +
                      %("assignee":"#{WORKER}","at":"#{STAMP}","work_ref":"https://example.com/brief"})

      released = store.release_task!(id, worker: WORKER)
      assert released.ok?, released.errors.inspect
      marker = delegation_in(org, id)
      assert_equal "ready", marker["status"]
      refute marker.key?("assignee"), "a released task holds no worker"
      assert_equal "https://example.com/brief", marker["work_ref"], "the reference outlives the claim"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_human_delegation_records_an_email_assignee
    with_delegation_store do |store, org, _archive|
      result = store.delegate_task!(FIX[:plants], kind: "human", assignee: "pat@example.com")
      assert result.ok?, result.errors.inspect
      assert_equal({ "kind" => "human", "status" => "delegated",
                     "assignee" => "pat@example.com", "at" => STAMP },
                   delegation_in(org, FIX[:plants]))
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- claim: the single-pickup guarantee ------------------------------------

  def test_claim_is_a_compare_and_set_with_one_winner
    with_delegation_store do |store, org, _archive|
      id = FIX[:plants]
      store.delegate_task!(id, kind: "agent", mode: "implement")
      assert store.claim_task!(id, worker: WORKER).ok?

      lost = store.claim_task!(id, worker: RIVAL)
      assert lost.conflict?
      assert_equal ["already claimed by #{WORKER} at #{STAMP}"], lost.errors
      assert_equal WORKER, lost.summary[:holder]
      assert_equal STAMP, lost.summary[:at]
      assert_equal WORKER, delegation_in(org, id)["assignee"]

      # A claim is granted once; even the holder's retry is a conflict, never a
      # silent re-grant.
      assert store.claim_task!(id, worker: WORKER).conflict?
    end
  end

  def test_concurrent_claims_across_stores_leave_exactly_one_holder
    with_delegation_store do |store, org, archive, dir|
      id = FIX[:plants]
      store.delegate_task!(id, kind: "agent", mode: "research")

      gate = Queue.new
      results = [WORKER, RIVAL].map do |worker|
        Thread.new do
          gate.pop
          build_store(org, archive, dir).claim_task!(id, worker: worker)
        end
      end
      2.times { gate << :go }
      statuses = results.map(&:value)

      assert_equal 1, statuses.count(&:ok?), statuses.map(&:status).inspect
      assert_equal 1, statuses.count(&:conflict?), statuses.map(&:status).inspect
      holder = delegation_in(org, id)
      assert_equal "claimed", holder["status"]
      assert_includes [WORKER, RIVAL], holder["assignee"]
      loser = statuses.find(&:conflict?)
      assert_equal holder["assignee"], loser.summary[:holder]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_claim_refuses_undelegated_and_human_delegated_tasks
    with_delegation_store do |store, _org, _archive|
      undelegated = store.claim_task!(FIX[:plants], worker: WORKER)
      assert undelegated.invalid?
      assert_equal ["task is not delegated to the agent pool"], undelegated.errors

      store.delegate_task!(FIX[:plants], kind: "human", assignee: "pat@example.com")
      human = store.claim_task!(FIX[:plants], worker: WORKER)
      assert human.invalid?
      assert_equal ["task is not delegated to the agent pool"], human.errors
    end
  end

  # -- release, revocation, work_ref -----------------------------------------

  def test_release_requires_the_matching_worker_unless_the_owner_forces_it
    with_delegation_store do |store, org, _archive|
      id = FIX[:plants]
      store.delegate_task!(id, kind: "agent", mode: "refine")
      store.claim_task!(id, worker: WORKER)

      mismatch = store.release_task!(id, worker: RIVAL)
      assert mismatch.conflict?
      assert_equal ["claim is held by #{WORKER}, not #{RIVAL.inspect}"], mismatch.errors
      assert_equal "claimed", delegation_in(org, id)["status"]

      assert store.release_task!(id, worker: nil).conflict?, "an anonymous release is not the owner"

      forced = store.release_task!(id, force: true)
      assert forced.ok?, forced.errors.inspect
      assert_equal WORKER, forced.summary[:released_from]
      assert_equal "ready", delegation_in(org, id)["status"]
      assert store.release_task!(id, force: true).invalid?, "a ready task is not claimed"
    end
  end

  def test_owner_revocation_defeats_a_stale_worker
    with_delegation_store do |store, org, _archive|
      id = FIX[:plants]
      store.delegate_task!(id, kind: "agent", mode: "implement")
      store.claim_task!(id, worker: WORKER)

      revoked = store.undelegate_task!(id)
      assert revoked.ok?, revoked.errors.inspect
      assert_equal WORKER, revoked.summary[:previous]["assignee"]
      assert_nil delegation_in(org, id)

      assert store.release_task!(id, worker: WORKER).invalid?
      stale_ref = store.set_work_ref!(id, "https://example.com/late", worker: WORKER)
      assert stale_ref.invalid?
      assert_equal ["task is not delegated"], stale_ref.errors
      assert store.undelegate_task!(id).no_change?
    end
  end

  def test_work_ref_is_owner_writable_and_worker_writable_only_while_the_claim_matches
    with_delegation_store do |store, org, _archive|
      id = FIX[:plants]
      store.delegate_task!(id, kind: "agent", mode: "research")

      # The owner (no worker id) may set a reference even before anyone claims.
      assert store.set_work_ref!(id, "https://example.com/plan").ok?
      assert_equal "https://example.com/plan", delegation_in(org, id)["work_ref"]
      assert store.set_work_ref!(id, "https://example.com/plan").no_change?

      store.claim_task!(id, worker: WORKER)
      rival = store.set_work_ref!(id, "https://example.com/theirs", worker: RIVAL)
      assert rival.conflict?
      assert_equal ["a work reference from a worker requires a matching claim"], rival.errors

      assert store.set_work_ref!(id, "https://example.com/pr/42", worker: WORKER).ok?
      assert_equal "https://example.com/pr/42", delegation_in(org, id)["work_ref"]
      # Setting a reference is not a status transition, so `at` stays put.
      assert_equal STAMP, delegation_in(org, id)["at"]

      assert store.set_work_ref!(id, nil).ok?
      refute delegation_in(org, id).key?("work_ref")
      assert store.set_work_ref!(FIX[:garden], "https://example.com/x").invalid?
    end
  end

  def test_work_ref_refuses_blank_and_multiline_references
    with_delegation_store do |store, _org, _archive|
      id = FIX[:plants]
      store.delegate_task!(id, kind: "agent", mode: "research")

      blank = store.set_work_ref!(id, "   ")
      assert blank.invalid?
      assert_equal ["delegation.work_ref must be a non-empty string"], blank.errors
      multiline = store.set_work_ref!(id, "https://example.com/a\nhttps://example.com/b")
      assert multiline.invalid?
      assert_equal ["delegation.work_ref must be a single line"], multiline.errors
    end
  end

  # -- eligibility and input validation --------------------------------------

  def test_delegation_refuses_proposed_closed_and_archived_tasks
    records = FIXTURE_RECORDS + [
      { "type" => "task", "id" => "dd000001", "parent" => FIX[:home], "state" => "PROPOSED",
        "title" => "Maybe repaint the shed" },
      { "type" => "task", "id" => "dd000002", "parent" => FIX[:home], "state" => "NEXT",
        "title" => "Swept but still live", "archived" => "2026-07-01" },
    ]
    with_delegation_store(records: records) do |store, _org, _archive|
      proposed = store.delegate_task!("dd000001", kind: "agent", mode: "refine")
      assert proposed.invalid?
      assert_equal ["task is PROPOSED; only accepted live tasks can be delegated"], proposed.errors

      closed = store.delegate_task!(FIX[:old], kind: "human", assignee: "pat@example.com")
      assert closed.invalid?
      assert_equal ["task is DONE; only accepted live tasks can be delegated"], closed.errors

      archived = store.delegate_task!("dd000002", kind: "agent", mode: "refine")
      assert archived.invalid?
      assert_equal ["task is archived; only accepted live tasks can be delegated"], archived.errors

      assert store.delegate_task!("no-such-id", kind: "agent", mode: "refine").not_found?
    end
  end

  def test_invalid_delegation_input_is_a_typed_refusal
    with_delegation_store do |store, org, _archive|
      id = FIX[:plants]
      [
        [{ kind: "robot" }, /kind "robot" must be human or agent/],
        [{ kind: "human", assignee: "pat" }, /must be an email address/],
        [{ kind: "human", assignee: "pat @example.com" }, /must be an email address/],
        [{ kind: "human", assignee: "pat@example.com", mode: "refine" }, /human delegation has no mode/],
        [{ kind: "agent" }, /mode nil must be one of refine\/research\/implement/],
        [{ kind: "agent", mode: "deploy" }, /mode "deploy" must be one of/],
        [{ kind: "agent", mode: "refine", assignee: WORKER }, /claimed by a worker, not assigned/],
      ].each do |attributes, pattern|
        result = store.delegate_task!(id, **{ kind: nil, mode: nil, assignee: nil }.merge(attributes))
        assert result.invalid?, attributes.inspect
        assert_match pattern, result.errors.first, attributes.inspect
      end

      store.delegate_task!(id, kind: "agent", mode: "research")
      [nil, "", "worker with spaces", "w" * 201].each do |worker|
        result = store.claim_task!(id, worker: worker)
        assert result.invalid?, worker.inspect
        assert_match(/worker id/, result.errors.first)
      end
      assert_nil delegation_in(org, id)["assignee"]
    end
  end

  def test_mode_updates_while_ready_and_refuses_while_claimed
    with_delegation_store do |store, org, _archive|
      id = FIX[:plants]
      store.delegate_task!(id, kind: "agent", mode: "refine")
      store.set_work_ref!(id, "https://example.com/brief")

      assert store.delegate_task!(id, kind: "agent", mode: "refine").no_change?
      widened = store.delegate_task!(id, kind: "agent", mode: "implement")
      assert widened.ok?, widened.errors.inspect
      assert_equal "implement", delegation_in(org, id)["mode"]
      assert_equal "https://example.com/brief", delegation_in(org, id)["work_ref"],
                   "a mode update still points at the same work"

      store.claim_task!(id, worker: WORKER)
      blocked = store.delegate_task!(id, kind: "agent", mode: "research")
      assert blocked.conflict?
      assert_match(/already claimed by #{Regexp.escape(WORKER)}/, blocked.errors.first)
      assert_equal "implement", delegation_in(org, id)["mode"]
    end
  end

  def test_replacing_the_kind_replaces_the_whole_marker
    with_delegation_store do |store, org, _archive|
      id = FIX[:plants]
      store.delegate_task!(id, kind: "agent", mode: "research")
      store.set_work_ref!(id, "https://example.com/brief")

      assert store.delegate_task!(id, kind: "human", assignee: "pat@example.com").ok?
      marker = delegation_in(org, id)
      assert_equal({ "kind" => "human", "status" => "delegated",
                     "assignee" => "pat@example.com", "at" => STAMP }, marker)
      refute marker.key?("work_ref"), "a different kind of delegation is a different delegation"
    end
  end

  # -- close, archive, undo --------------------------------------------------

  def test_close_clears_a_ready_marker_and_retains_a_claim_as_provenance
    with_delegation_store do |store, org, _archive|
      ready_id = FIX[:plants]
      claimed_id = FIX[:travel]
      human_id = FIX[:eval]
      store.delegate_task!(ready_id, kind: "agent", mode: "research")
      store.delegate_task!(claimed_id, kind: "agent", mode: "implement")
      store.claim_task!(claimed_id, worker: WORKER)
      store.set_work_ref!(claimed_id, "https://example.com/pr/42", worker: WORKER)
      store.delegate_task!(human_id, kind: "human", assignee: "pat@example.com")

      adapter = store.test_mutation
      store.reload!
      assert adapter.set_state(store.items.find { |item| item.id == ready_id }, "DONE")
      assert adapter.set_state(store.items.find { |item| item.id == claimed_id }, "DONE")
      assert adapter.set_state(store.items.find { |item| item.id == human_id }, "CANCELLED")

      assert_nil delegation_in(org, ready_id), "nothing happened, so nothing is recorded"
      assert_equal({ "kind" => "agent", "mode" => "implement", "status" => "claimed",
                     "assignee" => WORKER, "at" => STAMP,
                     "work_ref" => "https://example.com/pr/42" }, delegation_in(org, claimed_id))
      assert_equal "pat@example.com", delegation_in(org, human_id)["assignee"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_a_delegated_task_cannot_be_turned_back_into_a_proposal
    with_delegation_store do |store, org, _archive|
      id = FIX[:plants]
      store.delegate_task!(id, kind: "human", assignee: "pat@example.com")
      store.reload!
      item = store.items.find { |candidate| candidate.id == id }
      result = store.patch_task!(Tasks::TaskPatch.from(
        store.edit_snapshot(id), field: :state, value: "PROPOSED"
      ))

      assert result.invalid?
      assert_equal ["undelegate before setting PROPOSED"], result.errors
      assert_equal "NEXT", item.state
      assert_equal "pat@example.com", delegation_in(org, id)["assignee"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_completing_a_parent_settles_its_cascaded_descendants
    records = FIXTURE_RECORDS + [
      { "type" => "task", "id" => "dd000010", "parent" => FIX[:home], "state" => "NEXT",
        "title" => "Ship the deck" },
      { "type" => "task", "id" => "dd000011", "parent" => "dd000010", "state" => "TODO",
        "title" => "Queued subtask" },
      { "type" => "task", "id" => "dd000012", "parent" => "dd000010", "state" => "TODO",
        "title" => "Claimed subtask" },
    ]
    with_delegation_store(records: records) do |store, org, _archive|
      store.delegate_task!("dd000011", kind: "agent", mode: "refine")
      store.delegate_task!("dd000012", kind: "agent", mode: "implement")
      store.claim_task!("dd000012", worker: WORKER)

      store.reload!
      assert store.test_mutation.set_state(store.items.find { |item| item.id == "dd000010" }, "DONE")

      assert_nil delegation_in(org, "dd000011")
      assert_equal WORKER, delegation_in(org, "dd000012")["assignee"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_archive_sweep_carries_the_marker_verbatim
    with_delegation_store do |store, org, archive|
      id = FIX[:travel]
      store.delegate_task!(id, kind: "agent", mode: "implement")
      store.claim_task!(id, worker: WORKER)
      store.set_work_ref!(id, "https://example.com/pr/42", worker: WORKER)
      store.reload!
      assert store.test_mutation.set_state(store.items.find { |item| item.id == id }, "DONE")
      before = delegation_in(org, id)

      assert_operator store.archive_swept!, :>, 0
      assert_nil delegation_in(org, id)
      assert_equal before, delegation_in(archive, id)
      assert Tasks::Check.check_store(org, archive).ok?
    end
  end

  def test_delegation_is_revision_aware_and_undoable
    with_delegation_store do |store, org, _archive|
      id = FIX[:plants]
      stale = store.edit_snapshot(id).revision
      assert store.delegate_task!(id, kind: "agent", mode: "research",
                                  expected_revision: stale).ok?

      assert store.claim_task!(id, worker: WORKER, expected_revision: stale).stale?,
             "a delegation change moves the own-revision component"
      fresh = store.edit_snapshot(id).revision
      refute_equal stale, fresh
      assert store.claim_task!(id, worker: WORKER, expected_revision: fresh).ok?
      assert store.claim_task!(id, worker: RIVAL, expected_revision: "not-a-revision").invalid?

      assert_equal [:ok, "claim: Water the plants"], store.undo!
      assert_equal "ready", delegation_in(org, id)["status"]
      assert_equal [:ok, "delegate research: Water the plants"], store.undo!
      assert_nil delegation_in(org, id)
      assert_equal [:ok, "delegate research: Water the plants"], store.redo!
      assert_equal "ready", delegation_in(org, id)["status"]
    end
  end

  def test_idempotent_repeats_do_not_burn_an_undo_slot
    with_delegation_store do |store, org, _archive|
      id = FIX[:plants]
      store.delegate_task!(id, kind: "human", assignee: "pat@example.com")
      repeat = store.delegate_task!(id, kind: "human", assignee: "pat@example.com")
      assert repeat.no_change?
      assert repeat.ok?, "no_change is a success, just not a write"

      assert_equal [:ok, "delegate → pat@example.com: Water the plants"], store.undo!
      assert_nil delegation_in(org, id)
    end
  end

  # -- shape validation ------------------------------------------------------

  def test_store_refuses_to_write_a_marker_the_schema_would_reject
    with_delegation_store do |store, org, _archive|
      id = FIX[:plants]
      # A worker id is the one field a caller supplies verbatim, so drive the
      # shape gate through the value the record would carry.
      assert store.claim_task!(id, worker: "a\tb").invalid?
      assert_nil delegation_in(org, id)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_delegation_module_accepts_valid_objects_and_names_every_violation
    valid = [
      { "kind" => "human", "status" => "delegated", "assignee" => "pat@example.com", "at" => STAMP },
      { "kind" => "agent", "mode" => "research", "status" => "ready", "at" => STAMP },
      { "kind" => "agent", "mode" => "implement", "status" => "claimed", "assignee" => WORKER,
        "at" => STAMP, "work_ref" => "https://example.com/pr/42" },
    ]
    valid.each { |value| assert D.valid?(value), D.errors(value).inspect }

    assert_equal ["delegation must be an object"], D.errors("nope")
    assert_equal ["delegation must not be empty"], D.errors({})

    [
      [{ "kind" => "agent", "mode" => "research", "status" => "ready", "at" => STAMP,
         "assignee" => WORKER }, /assignee is not allowed while ready/],
      [{ "kind" => "agent", "mode" => "research", "status" => "claimed", "at" => STAMP },
       /must be a worker id/],
      [{ "kind" => "agent", "mode" => "research", "status" => "delegated", "at" => STAMP },
       /must be ready or claimed/],
      [{ "kind" => "human", "status" => "delegated", "assignee" => "pat@example.com",
         "at" => "2026-07-27 18:04:11" }, /is not a UTC timestamp/],
      [{ "kind" => "human", "status" => "delegated", "assignee" => "pat@example.com",
         "at" => "2026-02-31T00:00:00Z" }, /is not a UTC timestamp/],
      [{ "kind" => "human", "status" => "delegated", "assignee" => "pat@example.com",
         "at" => STAMP, "work_ref" => "" }, /work_ref must be a non-empty string/],
      [{ "kind" => "human", "status" => "delegated", "assignee" => "pat@example.com",
         "at" => STAMP, "note" => "x" }, /unknown keys: note/],
    ].each do |value, pattern|
      refute D.valid?(value), value.inspect
      assert_match pattern, D.errors(value).join(" | "), value.inspect
    end
  end
end
