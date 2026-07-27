# frozen_string_literal: true

require_relative "test_helper"
require "tasks/application"

class TestProposals < Minitest::Test
  TODAY = Date.new(2026, 7, 27)

  def with_proposal_app(records: FIXTURE_RECORDS)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, dump_fixture(records))
      app = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(org: org, archive: archive)
      )
      yield app, org, archive
    end
  end

  def test_create_and_explicit_scope_keep_proposal_out_of_accepted_work
    with_proposal_app do |app, org, _archive|
      result = app.create_task({
        title: "Research a better backup plan", state: "PROPOSED",
        deadline: "2026-08-15", priority: "B", body: "Current backups lack an offsite copy."
      }, today: TODAY)
      assert result.ok?, result.errors.inspect
      id = result.touched_ids.fetch(0)
      task = app.get_task(id)

      assert_equal "PROPOSED", task.state
      assert_equal ["Captured [#{TODAY.iso8601}].", "Current backups lack an offsite copy."],
                   task.body
      assert_equal [id], app.list_tasks(Tasks::TaskFilter.new(scope: :proposed)).tasks.map(&:id)
      refute_includes app.list_tasks(Tasks::TaskFilter.new).tasks.map(&:id), id
      %i[agenda next quadrants inbox].each do |view|
        refute_includes app.view_tasks(view, today: TODAY).tasks.map(&:id), id
      end
      refute app.list_projects(today: TODAY).flat_map(&:task_ids).include?(id)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_approve_and_reject_are_checked_atomic_and_undoable
    with_proposal_app do |app, org, archive|
      created = app.create_task({ title: "Review subscriptions", state: "PROPOSED" }, today: TODAY)
      id = created.touched_ids.fetch(0)

      approved = app.approve_task(id, expected_revision: app.get_task(id).revision, today: TODAY)
      assert approved.ok?, approved.errors.inspect
      assert_equal({ action: :approve, from: "PROPOSED", to: "INBOX" }, approved.summary)
      assert_equal "INBOX", app.get_task(id).state

      store = Tasks::Store.new(org: org, archive: archive)
      assert_equal [:ok, "approve proposal: Review subscriptions"], store.undo!
      assert_equal "PROPOSED", store.items.find { |item| item.id == id }.state

      rejected = app.reject_task(id, expected_revision: app.get_task(id).revision, today: TODAY)
      assert rejected.ok?, rejected.errors.inspect
      task = app.get_task(id)
      assert_equal "CANCELLED", task.state
      assert_equal TODAY, task.closed
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_decision_refuses_non_proposals_stale_revisions_and_proposal_trees
    records = FIXTURE_RECORDS + [
      { "type" => "task", "id" => "ee000001", "parent" => FIX[:home],
        "state" => "PROPOSED", "title" => "Parent proposal" },
      { "type" => "task", "id" => "ee000002", "parent" => "ee000001",
        "state" => "PROPOSED", "title" => "Child proposal" },
    ]
    with_proposal_app(records: records) do |app, _org, _archive|
      ordinary = app.approve_task(FIX[:garden])
      assert ordinary.invalid?
      assert_match(/INBOX, not PROPOSED/, ordinary.errors.first)

      parent = app.approve_task("ee000001")
      assert parent.conflict?
      assert_equal ["ee000002"], parent.summary[:proposed_descendant_ids]

      stale_revision = app.get_task("ee000002").revision
      changed = app.update_task(
        "ee000002", { title: "Changed child proposal" },
        expected_revision: stale_revision
      )
      assert changed.ok?
      assert app.reject_task("ee000002", expected_revision: stale_revision).stale?

      child = app.approve_task(
        "ee000002", expected_revision: app.get_task("ee000002").revision
      )
      assert child.ok?, child.errors.inspect
      assert_equal "INBOX", app.get_task("ee000002").state

      parent = app.approve_task(
        "ee000001", expected_revision: app.get_task("ee000001").revision
      )
      assert parent.ok?, parent.errors.inspect
      assert_equal "INBOX", app.get_task("ee000001").state
    end
  end

  def test_accepted_work_cannot_be_moved_or_reopened_beneath_a_proposal
    records = FIXTURE_RECORDS + [
      { "type" => "task", "id" => "ee000001", "parent" => FIX[:home],
        "state" => "PROPOSED", "title" => "Parent proposal" },
      { "type" => "task", "id" => "ee000003", "parent" => "ee000001",
        "state" => "DONE", "title" => "Closed child", "closed" => "2026-07-20" },
    ]
    with_proposal_app(records: records) do |app, _org, _archive|
      accepted = app.get_task(FIX[:garden])
      moved = app.update_task(
        accepted.id,
        { location: Tasks::TaskPlacement.new(parent_id: "ee000001") },
        expected_revision: accepted.revision
      )
      assert moved.invalid?
      assert_match(/accepted work cannot be moved under a proposed task/, moved.errors.first)

      nested = app.get_task("ee000003")
      reopened = app.update_task(
        nested.id, { state: "INBOX" }, expected_revision: nested.revision
      )
      assert reopened.invalid?
      assert_match(/accepted work cannot remain under a proposed task/, reopened.errors.first)
    end
  end

  def test_accepted_parent_cannot_become_proposed_while_accepted_descendants_remain
    records = FIXTURE_RECORDS + [
      { "type" => "task", "id" => "ee000010", "parent" => FIX[:home],
        "state" => "TODO", "title" => "Accepted parent" },
      { "type" => "task", "id" => "ee000011", "parent" => "ee000010",
        "state" => "INBOX", "title" => "Accepted child" },
    ]
    with_proposal_app(records: records) do |app, org, _archive|
      parent = app.get_task("ee000010")
      result = app.update_task(
        parent.id, { state: "PROPOSED" }, expected_revision: parent.revision
      )

      assert result.invalid?
      assert_equal ["cannot set PROPOSED while accepted descendants remain"], result.errors
      assert_equal "TODO", app.get_task("ee000010").state
      assert_equal "INBOX", app.get_task("ee000011").state
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_proposals_cannot_recur_complete_or_archive_as_live_descendants
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "task", "id" => "ff000001", "state" => "DONE",
        "title" => "Closed parent", "closed" => "2026-07-20" },
      { "type" => "task", "id" => "ff000002", "parent" => "ff000001",
        "state" => "PROPOSED", "title" => "Pending child", "deadline" => "2026-08-01" },
    ]
    with_proposal_app(records: records) do |app, org, archive|
      proposal = app.get_task("ff000002")
      recurrence = app.update_task(
        proposal.id, { recurrence: ".+1w" }, expected_revision: proposal.revision
      )
      assert recurrence.invalid?

      completion = app.update_task(
        proposal.id, { state: "DONE" }, expected_revision: proposal.revision
      )
      assert completion.invalid?

      preview = Tasks::Store.new(org: org, archive: archive).archive_preview
      assert preview.blocked?
      assert_equal ["ff000002"], preview.blocks.first.open_ids
    end
  end

  def test_project_archive_never_sweeps_an_undecided_proposal_even_when_forced
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "ff000010", "title" => "Projects" },
      { "type" => "section", "id" => "ff000011", "parent" => "ff000010",
        "title" => "Candidate project" },
      { "type" => "task", "id" => "ff000012", "parent" => "ff000011",
        "state" => "PROPOSED", "title" => "Investigate the candidate" },
    ]
    with_proposal_app(records: records) do |app, org, archive|
      result = app.archive_project("ff000011")
      assert result.conflict?
      assert_equal ["decide proposed tasks before archiving the project"], result.errors
      assert File.exist?(org)
      refute File.exist?(archive)
      assert_equal "PROPOSED", app.get_task("ff000012").state
    end
  end
end
