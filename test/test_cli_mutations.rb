# frozen_string_literal: true

require_relative "test_helper"
require "json"
require "open3"
require "tasks/application"

# Store-layer coverage for the CLI's due/state/priority mutations, plus
# end-to-end CLI tests (arg parsing, ref resolution, exit codes) that shell
# out to bin/tasks against a sandbox copy via TASKS_FILE/TASKS_ARCHIVE.
class TestCliMutations < Minitest::Test
  # -- set_date! (backs `due`) ------------------------------------------------

  def test_set_date_replaces_existing_deadline
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      assert store.test_mutation.set_date(flight, Date.new(2026, 7, 15), kind: :deadline)
      rec = record_for(org, title: "Book flight in Concur")
      assert_equal "2026-07-15", rec["deadline"]
      refute_equal "2026-07-02", rec["deadline"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_date_adds_deadline_when_item_has_none
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      assert store.test_mutation.set_date(plants, Date.new(2026, 7, 5), kind: :deadline)
      assert_equal Date.new(2026, 7, 5), find_item(store, "Water the plants").deadline
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_date_deadline_ignores_existing_scheduled
    with_store do |store, org, _a|
      # self-eval has a SCHEDULED but no DEADLINE — `due` must add a DEADLINE,
      # leaving the SCHEDULED stamp intact.
      eval = find_item(store, "self-eval")
      assert store.test_mutation.set_date(eval, Date.new(2026, 7, 20), kind: :deadline)
      fresh = find_item(store, "self-eval")
      assert_equal Date.new(2026, 7, 20), fresh.deadline
      assert_equal Date.new(2026, 7, 3), fresh.scheduled
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_date_promotes_inbox_to_todo
    with_store do |store, org, _a|
      garden = find_item(store, "garden")
      assert store.test_mutation.set_date(garden, Date.new(2026, 7, 10), kind: :deadline)
      assert_equal "TODO", find_item(store, "garden").state
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_date_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Book flight").dup
      stale.id = nil
      stale.line = 1
      refute store.test_mutation.set_date(stale, Date.new(2026, 7, 15), kind: :deadline)
      assert_equal "2026-07-02", record_for(org, title: "Book flight in Concur")["deadline"]
    end
  end

  def test_set_date_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.test_mutation.set_date(find_item(store, "Book flight"), Date.new(2026, 7, 15), kind: :deadline)
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  # -- set_date! (backs `schedule`) --------------------------------------------

  def test_set_date_scheduled_kind_sets_scheduled_not_deadline
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      assert store.test_mutation.set_date(plants, Date.new(2026, 7, 20), kind: :scheduled)
      fresh = find_item(store, "Water the plants")
      assert_equal Date.new(2026, 7, 20), fresh.scheduled
      assert_nil fresh.deadline
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- undate! (backs `undate`) ------------------------------------------------

  def test_undate_removes_both_when_no_kind
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      store.test_mutation.set_date(flight, Date.new(2026, 7, 20), kind: :scheduled)
      assert store.test_mutation.undate(find_item(store, "Book flight"))
      fresh = find_item(store, "Book flight")
      assert_nil fresh.deadline
      assert_nil fresh.scheduled
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_undate_removes_specific_kind_only
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      store.test_mutation.set_date(flight, Date.new(2026, 7, 20), kind: :scheduled)
      assert store.test_mutation.undate(find_item(store, "Book flight"), kind: :deadline)
      fresh = find_item(store, "Book flight")
      assert_nil fresh.deadline
      assert_equal Date.new(2026, 7, 20), fresh.scheduled
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_undate_returns_false_when_no_matching_stamp
    with_store do |store, org, _a|
      pr = find_item(store, "Review PR backlog")
      refute store.test_mutation.undate(pr)
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  def test_undate_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Book flight").dup
      stale.id = nil
      stale.line = 1
      refute store.test_mutation.undate(stale)
      assert_equal "2026-07-02", record_for(org, title: "Book flight in Concur")["deadline"]
    end
  end

  def test_undate_never_deletes_prose_mentioning_a_stamp_keyword
    with_store do |store, org, _a|
      # undate clears only the date FIELD; a body note that merely mentions
      # "DEADLINE:" mid-sentence must survive as body text.
      flight = find_item(store, "Book flight")
      store.test_mutation.add_note(flight, "Waiting on the DEADLINE: confirmation from legal.")

      assert store.test_mutation.undate(find_item(store, "Book flight"), kind: :deadline)
      rec = record_for(org, title: "Book flight in Concur")
      assert_nil rec["deadline"]
      assert_match(/Waiting on the DEADLINE: confirmation/, rec["body"])
    end
  end

  def test_undate_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.test_mutation.undate(find_item(store, "Book flight"))
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  # -- set_state! (backs `state`) ---------------------------------------------

  def test_set_state_open_to_open
    with_store do |store, org, _a|
      assert store.test_mutation.set_state(find_item(store, "Review PR"), "WAITING")
      assert_equal "WAITING", find_item(store, "Review PR").state
      rec = record_for(org, title: "Review PR backlog")
      assert_equal "WAITING", rec["state"]
      assert_equal "B", rec["priority"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_state_entering_done_adds_closed_stamp
    with_store do |store, org, _a|
      assert store.test_mutation.set_state(find_item(store, "Review PR"), "DONE")
      rec = record_for(org, title: "Review PR backlog")
      assert_equal "DONE", rec["state"]
      assert_equal Date.today.iso8601, rec["closed"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_state_cancelled_adds_closed_stamp
    with_store do |store, org, _a|
      assert store.test_mutation.set_state(find_item(store, "Travel desk"), "CANCELLED")
      rec = record_for(org, title: "Travel desk reply")
      assert_equal "CANCELLED", rec["state"]
      assert_equal Date.today.iso8601, rec["closed"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_state_leaving_done_removes_closed_stamp
    with_store do |store, org, _a|
      done = store.items.find { |i| i.title.include?("Old finished") }
      assert store.test_mutation.set_state(done, "TODO")
      assert_equal "TODO", store.items.find { |i| i.title.include?("Old finished") }.state
      assert_nil record_for(org, title: "Old finished thing")["closed"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_state_done_to_cancelled_keeps_single_closed_stamp
    with_store do |store, org, _a|
      done = store.items.find { |i| i.title.include?("Old finished") }
      assert store.test_mutation.set_state(done, "CANCELLED")
      rec = record_for(org, title: "Old finished thing")
      assert_equal "CANCELLED", rec["state"]
      assert_equal "C", rec["priority"]
      # DONE→CANCELLED keeps the single original closed date, not a fresh one.
      assert_equal "2026-06-20", rec["closed"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_state_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Review PR").dup
      stale.id = nil
      stale.line = 1
      refute store.test_mutation.set_state(stale, "DONE")
      rec = record_for(org, title: "Review PR backlog")
      assert_equal "NEXT", rec["state"]
      assert_equal "B", rec["priority"]
    end
  end

  def test_set_state_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.test_mutation.set_state(find_item(store, "Review PR"), "DONE")
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  # -- retitle! ---------------------------------------------------------------

  def test_retitle_replaces_title_only
    with_store do |store, org, _a|
      assert store.test_mutation.retitle(find_item(store, "Book flight"), "Rebook the flight")
      fresh = find_item(store, "Rebook the flight")
      assert_equal "NEXT [#A] Rebook the flight :@computer:important:urgent:", store.headline(fresh)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_retitle_preserves_headline_without_tags
    with_store do |store, org, _a|
      assert store.test_mutation.retitle(find_item(store, "garden"), "prune the roses")
      assert_equal "INBOX prune the roses", store.headline(find_item(store, "prune the roses"))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_retitle_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Book flight").dup
      stale.id = nil
      stale.line = 1
      refute store.test_mutation.retitle(stale, "nope")
      assert record_for(org, title: "Book flight in Concur")
    end
  end

  def test_retitle_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.test_mutation.retitle(find_item(store, "Book flight"), "Rebook")
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  # -- set_tags! --------------------------------------------------------------

  def test_set_tags_adds_and_removes
    with_store do |store, org, _a|
      pr = find_item(store, "Review PR")
      assert store.test_mutation.set_tags(pr, add: %w[urgent @home], remove: %w[important])
      fresh = find_item(store, "Review PR")
      assert_includes fresh.tags, "urgent"
      assert_includes fresh.tags, "@home"
      refute_includes fresh.tags, "important"
      assert_includes fresh.tags, "@computer" # untouched
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_tags_add_is_idempotent
    with_store do |store, org, _a|
      pr = find_item(store, "Review PR")
      assert store.test_mutation.set_tags(pr, add: %w[important])
      assert_equal 1, find_item(store, "Review PR").tags.count("important")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_tags_can_remove_all_leaving_bare_headline
    with_store do |store, org, _a|
      pr = find_item(store, "Review PR")
      assert store.test_mutation.set_tags(pr, remove: %w[@computer important])
      assert_equal "NEXT [#B] Review PR backlog", store.headline(find_item(store, "Review PR"))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_tags_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Review PR").dup
      stale.id = nil
      stale.line = 1
      refute store.test_mutation.set_tags(stale, add: %w[urgent])
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  # -- add_note! --------------------------------------------------------------

  def test_add_note_appends_body_line
    with_store do |store, org, _a|
      assert store.test_mutation.add_note(find_item(store, "Review PR"), "ping the reviewers")
      body = store.body(find_item(store, "Review PR"))
      assert_includes body.map(&:strip), "ping the reviewers"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_add_note_lands_within_the_block_not_the_next_task
    with_store do |store, org, _a|
      # A note appends to the target's own body field and cannot bleed into a
      # sibling record: garden gains the note, the next task (flight) does not.
      assert store.test_mutation.add_note(find_item(store, "garden"), "north bed")
      assert_match(/north bed/, record_for(org, title: "random thought about the garden")["body"])
      assert_nil record_for(org, title: "Book flight in Concur")["body"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_add_note_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Review PR").dup
      stale.id = nil
      stale.line = 1
      refute store.test_mutation.add_note(stale, "nope")
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  def test_add_note_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.test_mutation.add_note(find_item(store, "Review PR"), "a note")
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  def test_add_note_accepts_binary_tagged_utf8
    # ARGV under a non-UTF-8 locale (LANG unset) arrives tagged ASCII-8BIT even
    # though the bytes are UTF-8. Appending such a note must not raise
    # Encoding::CompatibilityError when joined into the UTF-8 file lines.
    with_store do |store, org, _a|
      note = "follow up — see thread".dup.force_encoding("ASCII-8BIT")
      assert store.test_mutation.add_note(find_item(store, "Review PR"), note)
      assert_includes store.body(find_item(store, "Review PR")).map(&:strip),
                      "follow up — see thread"
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- move! ------------------------------------------------------------------

  def test_move_relocates_block_under_target_section
    with_store do |store, org, _a|
      id = store.test_mutation.move(find_item(store, "garden"), "Work")
      assert_equal FIX[:garden], id
      garden = record_for(org, title: "random thought about the garden")
      work = record_for(org, title: "Work")
      home = record_for(org, title: "Home")
      assert_equal work["id"], garden["parent"], "garden now sits inside Work"
      assert work["line"] < garden["line"] && garden["line"] < home["line"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_move_carries_the_whole_block
    with_store do |store, org, _a|
      # self-eval's SCHEDULED date must travel with the moved record
      store.test_mutation.move(find_item(store, "self-eval"), "Home")
      rec = record_for(org, title: "Midyear self-eval")
      assert_equal "2026-07-03", rec["scheduled"]
      assert_equal record_for(org, title: "Home")["id"], rec["parent"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_move_returns_false_for_unknown_section
    with_store do |store, org, _a|
      refute store.test_mutation.move(find_item(store, "garden"), "Nonexistent")
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  def test_move_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "garden").dup
      stale.id = nil
      stale.line = 99
      refute store.test_mutation.move(stale, "Work")
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  def test_move_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.test_mutation.move(find_item(store, "garden"), "Work")
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  # -- CreateTask -------------------------------------------------------------

  def test_capture_adds_inbox_item_by_default
    with_store do |store, org, _a|
      result = store.create_task!(Tasks::CreateTask.new(title: "call the plumber"))
      assert_equal :ok, result.status
      fresh = store.items.find { |i| i.title == "call the plumber" }
      assert_equal "INBOX", fresh.state
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_capture_accepts_binary_tagged_utf8
    # Same non-UTF-8-locale regression as notes: a capture title with a
    # multibyte char must survive being written into the UTF-8 file.
    with_store do |store, org, _a|
      text = "Draft — Q3 proposal".dup.force_encoding("ASCII-8BIT")
      assert store.create_task!(Tasks::CreateTask.new(title: text)).ok?
      assert_match(/Draft — Q3 proposal/, File.read(org, encoding: "UTF-8"))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_capture_with_date_lands_as_todo
    with_store do |store, org, _a|
      store.create_task!(Tasks::CreateTask.new(
        title: "file taxes", deadline: Date.new(2026, 7, 20), state: "TODO"
      ))
      fresh = store.items.find { |i| i.title == "file taxes" }
      assert_equal "TODO", fresh.state
      assert_equal Date.new(2026, 7, 20), fresh.deadline
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_capture_under_named_project
    with_store do |store, org, _a|
      store.create_task!(Tasks::CreateTask.new(
        title: "refactor parser", state: "NEXT", tags: %w[@computer], project: "Work"
      ))
      rec = record_for(org, title: "refactor parser")
      work = record_for(org, title: "Work")
      home = record_for(org, title: "Home")
      assert work["line"] < rec["line"] && rec["line"] < home["line"]
      assert_equal work["id"], rec["parent"]
      assert_equal "NEXT refactor parser :@computer:", store.headline(find_item(store, "refactor parser"))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_capture_rejects_an_unknown_project
    with_store do |store, org, _a|
      result = store.create_task!(Tasks::CreateTask.new(title: "x", project: "Nonexistent"))
      assert_equal :invalid, result.status
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  def test_capture_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      assert store.create_task!(Tasks::CreateTask.new(title: "something")).ok?
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  def test_cli_done_marks_done_with_closed_stamp
    run_cli("done", "Book flight") do |org, out, _err, st|
      assert st.success?
      rec = record_for(org, title: "Book flight in Concur")
      assert_equal "DONE", rec["state"]
      assert_equal Date.today.iso8601, rec["closed"]
      assert_match(/DONE.*Book flight/, out)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_done_ambiguous_exits_2_not_1
    run_cli("done", "e") do |_org, _out, err, st|
      assert_equal 2, st.exitstatus, "spec: ref failures exit 2"
      assert_match(/ambiguous/, err)
    end
  end

  def test_cli_done_synonyms
    %w[complete close].each do |syn|
      run_cli(syn, "Book flight") do |org, _out, _err, st|
        assert st.success?, "#{syn} should alias done"
        assert_equal "DONE", record_for(org, title: "Book flight in Concur")["state"]
      end
    end
  end

  def test_cli_done_dry_run_writes_nothing
    run_cli("done", "Book flight", "--dry-run") do |org, out, _err, st|
      assert st.success?
      assert_equal FIXTURE_ORG, File.read(org)
      assert_match(/would mark DONE/, out)
    end
  end

  # -- defer / activate ----------------------------------------------------------

  def test_cli_defer_tags_and_hides_from_active_views
    run_cli("defer", "Water the plants") do |org, out, _err, st|
      assert st.success?
      assert_equal %w[@home defer], record_for(org, title: "Water the plants")["tags"]
      assert_match(/:defer:/, out) # prints the resulting headline
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_defer_synonym_snooze
    run_cli("snooze", "Water the plants") do |org, _out, _err, st|
      assert st.success?, "snooze should alias defer"
      assert_includes record_for(org, title: "Water the plants")["tags"], "defer"
    end
  end

  def test_cli_deferred_task_dropped_from_next_and_default_list
    deferred = deferred_fixture
    run_cli("next", content: deferred) do |_org, out, _err, st|
      assert st.success?
      refute_match(/Water the plants/, out, "deferred NEXT is hidden from `next`")
    end
    run_cli("list", content: deferred) do |_org, out, _err, st|
      assert st.success?
      refute_match(/Water the plants/, out, "deferred task is hidden from default list")
    end
  end

  def test_cli_list_deferred_shows_only_deferred
    deferred = deferred_fixture
    run_cli("list", "--deferred", content: deferred) do |_org, out, _err, st|
      assert st.success?
      assert_match(/Water the plants/, out)
      assert_match(/\(deferred\)/, out)
      refute_match(/Book flight/, out, "non-deferred tasks are excluded from --deferred")
    end
  end

  def test_cli_activate_clears_defer_tag
    deferred = deferred_fixture
    run_cli("activate", "Water the plants", content: deferred) do |org, _out, _err, st|
      assert st.success?
      rec = record_for(org, title: "Water the plants")
      assert_equal "NEXT", rec["state"]
      assert_equal %w[@home], rec["tags"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_defer_dry_run_writes_nothing
    run_cli("defer", "Water the plants", "--dry-run") do |org, out, _err, st|
      assert st.success?
      assert_equal FIXTURE_ORG, File.read(org)
      assert_match(/would defer/, out)
    end
  end

  def test_cli_defer_ambiguous_exits_2
    run_cli("defer", "e") do |_org, _out, err, st|
      assert_equal 2, st.exitstatus
      assert_match(/ambiguous/, err)
    end
  end

  # A deferred task that is then completed must not keep an orphaned :defer:
  # tag — otherwise it is invisible to `list --deferred` and unreachable by
  # `activate`, and the dead tag rides into archive.jsonl.
  def test_cli_done_on_deferred_task_clears_defer_tag
    deferred = deferred_fixture
    run_cli("done", "Water the plants", content: deferred) do |org, _out, _err, st|
      assert st.success?
      rec = record_for(org, title: "Water the plants")
      assert_equal "DONE", rec["state"]
      assert_equal %w[@home], rec["tags"], "completing a task drops the someday/maybe marker"
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- list/next date tags -------------------------------------------------------

  def test_cli_list_tags_scheduled_start_with_tilde
    # self-eval has only a SCHEDULED date; it should surface as "~M/D" (start),
    # while a DEADLINE item shows the bare "M/D" (due).
    run_cli("list") do |_org, out, _err, st|
      assert st.success?
      assert_match(/self-eval.*~7\/3/, out, "scheduled-only shows a ~-prefixed start date")
      assert_match(/Book flight.* 7\/2/, out, "deadline shows a bare due date")
      refute_match(/Book flight.*~7\/2/, out, "a deadline is never tilde-prefixed")
    end
  end

  # -- help ----------------------------------------------------------------------

  def test_cli_help_prints_reference
    run_cli("help") do |_org, out, _err, st|
      assert st.success?
      assert_match(/capture/, out)
      assert_match(/archive/, out)
      assert_match(/Full spec: docs\/cli-spec\.md/, out)
    end
  end

  def test_cli_unknown_command_exits_1_with_help
    run_cli("bogus") do |_org, out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/unknown command: "bogus"/, err)
      assert_match(/agenda/, err) # falls back to the full reference
      assert_empty out
    end
  end

  def test_cli_archive_sweeps_to_archive_file
    run_cli("archive") do |org, out, _err, st|
      assert st.success?
      assert_nil record_for(org, title: "Old finished thing")
      archive = File.join(File.dirname(org), "archive.jsonl")
      assert record_for(archive, title: "Old finished thing")
      assert_match(/Archived 1 item/, out)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_archive_nothing_to_do
    no_done = dump_fixture(FIXTURE_RECORDS.reject { |r| r["title"] == "Old finished thing" })
    run_cli("archive", content: no_done) do |_org, out, _err, st|
      assert st.success?
      assert_match(/Nothing to archive/, out)
    end
  end

  def test_cli_archive_refuses_open_descendants_with_actionable_error
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "accc1001", "title" => "Projects" },
      { "type" => "task", "id" => "accc1002", "parent" => "accc1001", "state" => "CANCELLED",
        "title" => "Cancelled project", "closed" => "2026-07-01" },
      { "type" => "task", "id" => "accc1003", "parent" => "accc1002", "state" => "WAITING",
        "title" => "Waiting on vendor" },
    ]

    run_cli("archive", content: dump_fixture(records)) do |org, out, err, st|
      assert_equal 1, st.exitstatus
      assert_empty out
      assert_match(/Archive refused/, err)
      assert_match(/1 open descendant/, err)
      assert_match(/Waiting on vendor/, err)
      assert_match(/Complete, cancel, move, or unnest/, err)
      assert record_for(org, title: "Cancelled project")
      assert record_for(org, title: "Waiting on vendor")
      refute File.exist?(File.join(File.dirname(org), "archive.jsonl"))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_archive_conflict_preserves_live_data_and_explains_recovery
    stale_archive = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "task", "id" => FIX[:old], "state" => "DONE",
        "title" => "Stale archived copy", "closed" => "2026-06-20", "archived" => "2026-07-09" },
    ])

    run_cli("archive", archive_content: stale_archive) do |org, out, err, st|
      assert_equal 1, st.exitstatus
      assert_empty out
      assert_match(/partial or conflicting copies/, err)
      assert_match(/Live tasks were preserved/, err)
      assert_match(/tasks list --archived --json/, err)
      assert record_for(org, title: "Old finished thing")
      archive = File.join(File.dirname(org), "archive.jsonl")
      assert record_for(archive, title: "Stale archived copy")
    end
  end

  def test_cli_archive_child_only_overlap_reports_conflict_instead_of_crashing
    live = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "accc3001", "title" => "Projects" },
      { "type" => "task", "id" => "accc3002", "parent" => "accc3001", "state" => "DONE",
        "title" => "Closed parent", "closed" => "2026-07-08" },
      { "type" => "task", "id" => "accc3003", "parent" => "accc3002", "state" => "DONE",
        "title" => "Closed child", "closed" => "2026-07-08" },
    ])
    child_only_archive = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "task", "id" => "accc3003", "parent" => "accc3002", "state" => "DONE",
        "title" => "Closed child", "closed" => "2026-07-08" },
    ])

    run_cli("archive", content: live, archive_content: child_only_archive) do |org, out, err, st|
      assert_equal 1, st.exitstatus
      assert_empty out
      assert_match(/partial or conflicting copies/, err)
      refute_match(/NoMethodError/, err)
      assert record_for(org, title: "Closed parent")
      assert record_for(org, title: "Closed child")
    end
  end

  def test_cli_archive_duplicate_id_overlap_reports_conflict_instead_of_crashing
    duplicate = { "type" => "task", "id" => FIX[:old], "state" => "DONE",
                  "title" => "Old finished thing", "priority" => "C", "tags" => %w[@computer],
                  "closed" => "2026-06-20", "archived" => "2026-07-09" }
    duplicate_archive = dump_fixture([
      { "type" => "meta", "version" => 1 }, duplicate, duplicate.dup,
    ])

    run_cli("archive", archive_content: duplicate_archive) do |org, out, err, st|
      assert_equal 1, st.exitstatus
      assert_empty out
      assert_match(/partial or conflicting copies/, err)
      refute_match(/NoMethodError/, err)
      assert record_for(org, title: "Old finished thing")
    end
  end

  # -- read commands --json ----------------------------------------------------

  def test_cli_list_json_shape_and_filters
    run_cli("list", "@computer", "--json") do |_org, out, _err, st|
      assert st.success?
      rows = JSON.parse(out)
      refute_empty rows
      rows.each { |r| assert_includes r["contexts"], "@computer" }
      row = rows.first
      %w[state priority title tags contexts scheduled deadline line source headline].each do |k|
        assert row.key?(k), "missing key #{k}"
      end
      assert_equal "live", row["source"]
    end
  end

  def test_cli_list_json_empty_result_is_empty_array
    run_cli("list", "zzznope", "--json") do |_org, out, _err, st|
      assert st.success?
      assert_equal [], JSON.parse(out)
    end
  end

  def test_cli_list_unknown_flag_keeps_legacy_clean_error
    run_cli("list", "--not-a-list-flag") do |_org, out, err, status|
      assert_equal 1, status.exitstatus
      assert_empty out
      assert_equal "unknown flag: --not-a-list-flag\n", err
    end
  end

  def test_cli_list_json_includes_archived_with_source
    run_cli("archive") do |org, _out, _err, _st|
      env = { "TASKS_FILE" => org, "TASKS_ARCHIVE" => File.join(File.dirname(org), "archive.jsonl") }
      out, _err, st = Open3.capture3(env, "ruby", BIN, "list", "-a", "--json")
      assert st.success?
      sources = JSON.parse(out).map { |r| r["source"] }.uniq.sort
      assert_equal %w[archive live], sources
    end
  end

  def test_cli_agenda_json_sorted_by_date_then_priority
    run_cli("agenda", "--json") do |_org, out, _err, st|
      assert st.success?
      rows = JSON.parse(out)
      keys = rows.map { |r| [(r["deadline"] || r["scheduled"]), r["priority"] || "Z"] }
      assert_equal keys.sort, keys, "agenda JSON must be date-then-priority sorted"
    end
  end

  def test_cli_quadrants_json_adds_quadrant_field
    run_cli("quadrants", "--json") do |_org, out, _err, st|
      assert st.success?
      rows = JSON.parse(out)
      flight = rows.find { |r| r["title"].include?("Book flight") }
      assert_equal "Q1", flight["quadrant"] # [#A] + important/urgent tags
      plants = rows.find { |r| r["title"].include?("Water the plants") }
      assert_equal "Q4", plants["quadrant"] # no priority, no date, no tags
    end
  end

  # The CLI reads the urgent_days window from config/env: a far-off deadline is
  # not urgent by default (Q2 for a high-priority task) but becomes urgent (Q1)
  # once the window is widened past it. Uses a deadline relative to real today,
  # since the CLI classifies against Date.today.
  def test_cli_quadrants_honors_urgent_days_window
    far = Date.today + 20
    content = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "dddd0001", "title" => "Work" },
      { "type" => "task", "id" => "dddd0002", "parent" => "dddd0001", "state" => "NEXT",
        "priority" => "A", "title" => "distant milestone", "deadline" => far.iso8601 },
    ])
    q = lambda do |out|
      JSON.parse(out).find { |r| r["title"].include?("distant milestone") }["quadrant"]
    end
    run_cli("quadrants", "--json", content: content, env: { "TASKS_URGENT_DAYS" => nil }) do |_o, out, _e, st|
      assert st.success?
      assert_equal "Q2", q.call(out), "far deadline is not urgent at the default 3-day window"
    end
    run_cli("quadrants", "--json", content: content, env: { "TASKS_URGENT_DAYS" => "30" }) do |_o, out, _e, st|
      assert st.success?
      assert_equal "Q1", q.call(out), "widening the window makes it urgent"
    end
  end

  def test_cli_inbox_and_next_json
    run_cli("inbox", "--json") do |_org, out, _err, st|
      assert st.success?
      rows = JSON.parse(out)
      assert_equal ["INBOX"], rows.map { |r| r["state"] }.uniq
    end
    run_cli("next", "--json") do |_org, out, _err, st|
      assert st.success?
      rows = JSON.parse(out)
      assert_equal ["NEXT"], rows.map { |r| r["state"] }.uniq
      pris = rows.map { |r| r["priority"] || "Z" }
      assert_equal pris.sort, pris, "next JSON sorted by priority"
    end
  end

  # The CLI adapter may keep its line-oriented JSON presentation, but its
  # selected ids and order must come directly from the application facade.
  # These end-to-end cases pin the adapter-to-library parity for every named
  # view and for composed list filters.
  def test_cli_json_query_paths_match_reusable_query_results
    cases = {
      ["list", "@computer", "--json"] => ->(app) {
        app.list_tasks(Tasks::TaskFilter.parse_cli(["@computer", "--json"]).filter)
      },
      ["agenda", "--json"] => ->(app) { app.view_tasks(:agenda) },
      ["next", "--json"] => ->(app) { app.view_tasks(:next) },
      ["quadrants", "--json"] => ->(app) { app.view_tasks(:quadrants) },
      ["inbox", "--json"] => ->(app) { app.view_tasks(:inbox) },
    }

    cases.each do |args, build_result|
      run_cli(*args) do |org, out, err, status|
        assert status.success?, "#{args.join(" ")} failed: #{err}"
        factory = Tasks::StoreFactory.new(org: org, archive: File.join(File.dirname(org), "archive.jsonl"))
        result = build_result.call(Tasks::Application.new(store_factory: factory))
        assert_equal result.tasks.map(&:id), JSON.parse(out).map { |row| row.fetch("id") },
                     "#{args.join(" ")} id selection/order drifted from Tasks::Application"
      end
    end
  end

  def test_cli_capture_missing_flag_value_exits_1
    run_cli("capture", "buy milk", "--due") do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/missing value for --due/, err)
      refute_match(/buy milk/, File.read(org), "nothing captured")
    end
  end

  # -- CLI end-to-end (shell out to bin/tasks) --------------------------------

  BIN = File.expand_path("../bin/tasks", __dir__)

  # Run bin/tasks in a sandbox; returns [stdout, stderr, status].
  def run_cli(*args, content: FIXTURE_ORG, archive_content: nil, env: {})
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, content)
      File.write(archive, archive_content) if archive_content
      env = { "TASKS_FILE" => org, "TASKS_ARCHIVE" => archive }.merge(env)
      require "open3"
      out, err, st = Open3.capture3(env, "ruby", BIN, *args)
      # The CLI emits UTF-8; capture3 tags output with the runner's locale,
      # which is US-ASCII when LANG is unset. Re-tag so assertions matching
      # multibyte output don't raise "invalid byte sequence in US-ASCII".
      yield org, out.force_encoding("UTF-8"), err.force_encoding("UTF-8"), st
    end
  end

  def run_cli_at(org, archive, *args)
    Open3.capture3({ "TASKS_FILE" => org, "TASKS_ARCHIVE" => archive }, "ruby", BIN, *args)
  end

  def test_cli_due_sets_deadline
    run_cli("due", "Book flight", "2026-07-15") do |org, out, _err, st|
      assert st.success?
      assert_equal "2026-07-15", record_for(org, title: "Book flight in Concur")["deadline"]
      assert_match(/DONE|NEXT/, out) # prints the resulting headline
    end
  end

  def test_cli_note_with_em_dash_under_non_utf8_locale
    # End-to-end regression: under a C locale the shell hands Ruby ASCII-8BIT
    # ARGV, so a note with an em-dash used to crash on the UTF-8 file write.
    run_cli("note", "Review PR", "follow up — see thread",
            env: { "LC_ALL" => "C", "LANG" => "", "LC_CTYPE" => "" }) do |org, _out, err, st|
      assert st.success?, "note crashed: #{err}"
      assert_match(/follow up — see thread/, File.read(org, encoding: "UTF-8"))
    end
  end

  def test_cli_priority_clears_with_none
    run_cli("priority", "Book flight", "none") do |org, out, _err, st|
      assert st.success?
      rec = record_for(org, title: "Book flight in Concur")
      assert_equal "NEXT", rec["state"]
      assert_nil rec["priority"]
      assert_match(/Book flight/, out)
    end
  end

  def test_cli_state_reopens_done_item
    run_cli("state", "Old finished", "TODO") do |org, out, _err, st|
      assert st.success?
      rec = record_for(org, title: "Old finished thing")
      assert_equal "TODO", rec["state"]
      assert_nil rec["closed"]
      assert_match(/TODO/, out)
    end
  end

  def test_cli_ref_no_match_exits_2
    run_cli("due", "nonexistent-task", "today") do |_org, _out, err, st|
      assert_equal 2, st.exitstatus
      assert_match(/no match/, err)
    end
  end

  def test_cli_ref_ambiguous_exits_2
    # "e" appears in nearly every fixture title — guaranteed multi-match
    run_cli("priority", "e", "A") do |_org, _out, err, st|
      assert_equal 2, st.exitstatus
      assert_match(/ambiguous/, err)
    end
  end

  def test_cli_unknown_flag_exits_1
    run_cli("due", "Book flight", "today", "--dryrun") do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/unknown flag: --dryrun/, err)
      assert_equal FIXTURE_ORG, File.read(org), "nothing written"
    end
  end

  def test_cli_duplicate_titles_report_the_right_task
    # Records ordered so the NEXT copy lands on physical line 3 (meta=1,
    # section=2, NEXT=3, TODO=4); L3 targets the NEXT copy.
    dup_org = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "eeee0001", "title" => "W" },
      { "type" => "task", "id" => "eeee0002", "parent" => "eeee0001", "state" => "NEXT",
        "title" => "pay the bill", "tags" => %w[@computer] },
      { "type" => "task", "id" => "eeee0003", "parent" => "eeee0001", "state" => "TODO",
        "title" => "pay the bill", "tags" => %w[@home] },
    ])
    run_cli("priority", "L3", "A", content: dup_org) do |org, out, _err, st|
      assert st.success?
      assert_match(/NEXT \[#A\] pay the bill/, out)
      recs = Tasks::Format.parse(File.read(org)).records
      assert_equal "A", recs.find { |r| r["state"] == "NEXT" }["priority"]
      assert_nil recs.find { |r| r["state"] == "TODO" }["priority"], "TODO copy untouched"
    end
  end

  def test_cli_dry_run_writes_nothing
    run_cli("due", "Book flight", "2026-07-15", "--dry-run") do |org, out, _err, st|
      assert st.success?
      assert_equal FIXTURE_ORG, File.read(org)
      assert_match(/would set DEADLINE/, out)
    end
  end

  def test_cli_bad_state_exits_1
    run_cli("state", "Book flight", "BOGUS") do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/unknown state/, err)
    end
  end

  def test_cli_json_output
    run_cli("priority", "Book flight", "C", "--json") do |_org, out, _err, st|
      assert st.success?
      require "json"
      data = JSON.parse(out)
      touched = data.fetch("touched")
      assert_equal 1, touched.size
      assert_equal "C", touched[0]["priority"]
      assert_match(/Book flight/, touched[0]["headline"])
    end
  end

  # -- schedule ----------------------------------------------------------------

  def test_cli_schedule_sets_scheduled
    run_cli("schedule", "Book flight", "2026-07-20") do |org, out, _err, st|
      assert st.success?
      rec = record_for(org, title: "Book flight in Concur")
      assert_equal "2026-07-20", rec["scheduled"]
      assert_equal "2026-07-02", rec["deadline"], "existing deadline untouched"
      assert_match(/Book flight/, out)
    end
  end

  def test_cli_schedule_ambiguous_exits_2
    run_cli("schedule", "e", "today") do |_org, _out, err, st|
      assert_equal 2, st.exitstatus
      assert_match(/ambiguous/, err)
    end
  end

  # -- undate --------------------------------------------------------------------

  def test_cli_undate_removes_deadline
    run_cli("undate", "Book flight") do |org, out, _err, st|
      assert st.success?
      refute_match(/DEADLINE:/, File.read(org))
      assert_match(/Book flight/, out)
    end
  end

  def test_cli_undate_kind_flag_removes_only_that_kind
    run_cli("undate", "self-eval", "--kind", "scheduled") do |org, out, _err, st|
      assert st.success?
      refute_match(/SCHEDULED:/, File.read(org))
      assert_match(/self-eval/, out)
    end
  end

  def test_cli_undate_nothing_to_remove_exits_1
    run_cli("undate", "Review PR") do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/nothing to remove/, err)
    end
  end

  def test_cli_undate_bad_kind_exits_1
    run_cli("undate", "Book flight", "--kind", "bogus") do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/--kind must be deadline or scheduled/, err)
    end
  end

  def test_cli_undate_dry_run_writes_nothing
    run_cli("undate", "Book flight", "--dry-run") do |org, out, _err, st|
      assert st.success?
      assert_equal FIXTURE_ORG, File.read(org)
      assert_match(/would remove/, out)
    end
  end

  # -- cancel --------------------------------------------------------------------

  def test_cli_cancel_marks_cancelled
    run_cli("cancel", "Review PR") do |org, out, _err, st|
      assert st.success?
      rec = record_for(org, title: "Review PR backlog")
      assert_equal "CANCELLED", rec["state"]
      assert_equal "B", rec["priority"]
      assert_equal Date.today.iso8601, rec["closed"]
      assert_match(/CANCELLED/, out)
    end
  end

  def test_cli_cancel_alias_drop
    run_cli("drop", "Review PR") do |org, _out, _err, st|
      assert st.success?
      assert_equal "CANCELLED", record_for(org, title: "Review PR backlog")["state"]
    end
  end

  def test_cli_cancel_no_match_exits_2
    run_cli("cancel", "nonexistent-task") do |_org, _out, err, st|
      assert_equal 2, st.exitstatus
      assert_match(/no match/, err)
    end
  end

  # -- show ------------------------------------------------------------------

  def test_cli_show_human_readable
    run_cli("show", "Book flight") do |_org, out, _err, st|
      assert st.success?
      assert_match(/Book flight/, out)
      assert_match(/deadline:\s+2026-07-02/, out)
    end
  end

  def test_cli_show_prints_notes
    run_cli("show", "Travel desk") do |_org, out, _err, st|
      assert st.success?
      assert_match(/Some note line\./, out)
    end
  end

  def test_cli_show_json
    run_cli("show", "Travel desk", "--json") do |_org, out, _err, st|
      assert st.success?
      require "json"
      data = JSON.parse(out)
      assert_equal "WAITING", data["state"]
      assert_equal ["Some note line."], data["notes"]
      assert_nil data["closed"]
    end
  end

  def test_cli_show_project_skips_closed_ancestors
    # `project` follows the nearest-OPEN-ancestor rule shared with the TUI's
    # Projects view and detail panel: a DONE parent is skipped, an open one isn't.
    nested = dump_fixture(
      [{ "type" => "meta", "version" => 1 },
       { "type" => "section", "id" => "cccc0001", "title" => "Work" },
       { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001", "state" => "DONE",
         "title" => "Done parent", "closed" => "2026-07-01" },
       { "type" => "task", "id" => "cccc0003", "parent" => "cccc0002", "state" => "NEXT",
         "title" => "Hoisted child" },
       { "type" => "task", "id" => "cccc0004", "parent" => "cccc0001", "state" => "TODO",
         "title" => "Open parent" },
       { "type" => "task", "id" => "cccc0005", "parent" => "cccc0004", "state" => "TODO",
         "title" => "Nested child" }]
    )
    require "json"
    run_cli("show", "Hoisted child", "--json", content: nested) do |_org, out, _err, st|
      assert st.success?
      assert_equal "Work", JSON.parse(out)["project"], "DONE parent skipped → section"
    end
    run_cli("show", "Nested child", "--json", content: nested) do |_org, out, _err, st|
      assert st.success?
      assert_equal "Open parent", JSON.parse(out)["project"]
    end
  end

  def test_cli_show_closed_item_includes_closed_date
    run_cli("show", "Old finished", "--include-done", "--json") do |_org, out, _err, st|
      assert st.success?
      require "json"
      data = JSON.parse(out)
      assert_equal "2026-06-20", data["closed"]
    end
  end

  def test_cli_show_ref_no_match_exits_2
    run_cli("show", "nonexistent-task") do |_org, _out, err, st|
      assert_equal 2, st.exitstatus
      assert_match(/no match/, err)
    end
  end

  def test_cli_show_ref_ambiguous_exits_2
    run_cli("show", "e") do |_org, _out, err, st|
      assert_equal 2, st.exitstatus
      assert_match(/ambiguous/, err)
    end
  end

  # -- retitle ----------------------------------------------------------------

  def test_cli_retitle_replaces_title
    run_cli("retitle", "Book flight", "Rebook the flight") do |org, out, _err, st|
      assert st.success?
      rec = record_for(org, title: "Rebook the flight")
      assert_equal "NEXT", rec["state"]
      assert_equal "A", rec["priority"]
      assert_equal %w[@computer important urgent], rec["tags"]
      assert_match(/Rebook the flight/, out)
    end
  end

  def test_cli_retitle_alias_rename
    run_cli("rename", "Book flight", "Rebook") do |org, _out, _err, st|
      assert st.success?
      assert_equal "Rebook", record_for(org, title: "Rebook")["title"]
    end
  end

  def test_cli_retitle_missing_title_exits_1
    run_cli("retitle", "Book flight") do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/usage: tasks retitle/, err)
    end
  end

  # -- tag --------------------------------------------------------------------

  def test_cli_tag_adds_and_removes
    run_cli("tag", "Review PR", "+urgent", "-important", "@home") do |org, out, _err, st|
      assert st.success?
      tags = record_for(org, title: "Review PR backlog")["tags"]
      assert_includes tags, "urgent"
      assert_includes tags, "@home"
      refute_includes tags, "important"
      assert_match(/Review PR/, out)
    end
  end

  def test_cli_tag_removes_context
    run_cli("tag", "Review PR", "-@computer") do |org, _out, _err, st|
      assert st.success?
      refute_includes record_for(org, title: "Review PR backlog")["tags"], "@computer"
    end
  end

  def test_cli_tag_bad_spec_exits_1
    run_cli("tag", "Review PR", "important") do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/must start with/, err)
    end
  end

  def test_cli_tag_dry_run_writes_nothing
    run_cli("tag", "Review PR", "+urgent", "--dry-run") do |org, out, _err, st|
      assert st.success?
      assert_equal FIXTURE_ORG, File.read(org)
      assert_match(/would apply \+urgent/, out)
    end
  end

  # -- note -------------------------------------------------------------------

  def test_cli_note_appends_line
    run_cli("note", "Review PR", "ping the reviewers") do |org, out, _err, st|
      assert st.success?
      assert_match(/ping the reviewers/, File.read(org))
      assert_match(/Review PR/, out)
    end
  end

  def test_cli_note_dry_run_writes_nothing
    run_cli("note", "Review PR", "later", "--dry-run") do |org, out, _err, st|
      assert st.success?
      assert_equal FIXTURE_ORG, File.read(org)
      assert_match(/would add note/, out)
    end
  end

  # -- move -------------------------------------------------------------------

  def test_cli_move_relocates_and_reports_new_headline
    run_cli("move", "garden", "Work") do |org, out, _err, st|
      assert st.success?
      garden = record_for(org, title: "random thought about the garden")
      work = record_for(org, title: "Work")
      home = record_for(org, title: "Home")
      assert_equal work["id"], garden["parent"]
      assert work["line"] < garden["line"] && garden["line"] < home["line"]
      assert_match(/garden/, out)
    end
  end

  def test_cli_move_unknown_section_exits_1
    run_cli("move", "garden", "Nowhere") do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/could not move/, err)
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  # -- capture ----------------------------------------------------------------

  def test_cli_capture_default_inbox
    run_cli("capture", "call the plumber") do |org, out, _err, st|
      assert st.success?
      rec = record_for(org, title: "call the plumber")
      assert_equal "INBOX", rec["state"]
      assert_match(/Captured \[#{Date.today}\]/, rec["body"])
      assert_match(/call the plumber/, out)
    end
  end

  def test_cli_capture_with_flags_lands_processed
    run_cli("capture", "prep board deck", "--due", "2026-07-20",
            "--priority", "A", "--tag", "important", "--context", "@work",
            "--project", "Work") do |org, out, _err, st|
      assert st.success?
      rec = record_for(org, title: "prep board deck")
      work = record_for(org, title: "Work")
      home = record_for(org, title: "Home")
      assert work["line"] < rec["line"] && rec["line"] < home["line"]
      assert_equal "TODO", rec["state"]
      assert_equal "A", rec["priority"]
      assert_equal %w[@work important], rec["tags"]
      assert_equal "2026-07-20", rec["deadline"]
      assert_match(/prep board deck/, out)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_capture_explicit_state_overrides_date_default
    run_cli("capture", "waiting on legal", "--due", "2026-07-20", "--state", "WAITING") do |org, _out, _err, st|
      assert st.success?
      assert_equal "WAITING", record_for(org, title: "waiting on legal")["state"]
    end
  end

  def test_cli_capture_unknown_project_exits_1
    run_cli("capture", "x", "--project", "Nowhere") do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/could not capture/, err)
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  def test_cli_capture_bad_date_exits_1
    run_cli("capture", "x", "--due", "notadate") do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/unrecognized date/, err)
    end
  end

  def test_cli_capture_unknown_flag_exits_1
    run_cli("capture", "x", "--bogus") do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/unknown flag: --bogus/, err)
    end
  end

  def test_cli_capture_dry_run_writes_nothing
    run_cli("capture", "x", "--priority", "B", "--dry-run") do |org, out, _err, st|
      assert st.success?
      assert_equal FIXTURE_ORG, File.read(org)
      assert_match(/would capture under Inbox: INBOX \[#B\] x/, out)
    end
  end

  def test_cli_capture_json
    run_cli("capture", "shiny new task", "--json") do |_org, out, _err, st|
      assert st.success?
      require "json"
      touched = JSON.parse(out).fetch("touched")
      assert_equal 1, touched.size
      assert_equal "shiny new task", touched[0]["title"]
      assert_equal "INBOX", touched[0]["state"]
    end
  end

  # -- recur ------------------------------------------------------------------

  # A fixture with a dated task (Pay rent) to attach recurrence to, and an
  # undated one (Standup notes) for the no-date paths.
  RECUR_RECORDS = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "cccc0001", "title" => "Inbox" },
    { "type" => "section", "id" => "cccc0002", "title" => "Work" },
    { "type" => "task", "id" => "cccc0003", "parent" => "cccc0002", "state" => "NEXT",
      "title" => "Pay rent", "tags" => %w[@home], "deadline" => "2026-08-01" },
    { "type" => "task", "id" => "cccc0004", "parent" => "cccc0002", "state" => "NEXT",
      "title" => "Standup notes", "tags" => %w[@computer] },
  ].freeze
  RECUR_CONTENT = dump_fixture(RECUR_RECORDS)

  # RECUR_RECORDS deep-duped, with `recur` optionally seeded on Pay rent — the
  # jsonl counterpart of the old `RECUR_CONTENT.sub("<…>", "<… .+1w>")` splices.
  def recur_content(pay_rent_recur: nil)
    recs = RECUR_RECORDS.map(&:dup)
    recs.find { |r| r["title"] == "Pay rent" }["recur"] = pay_rent_recur if pay_rent_recur
    dump_fixture(recs)
  end

  def test_cli_recur_sets_cookie_from_friendly_word
    run_cli("recur", "Pay rent", "monthly", content: RECUR_CONTENT) do |org, _out, _err, st|
      assert st.success?
      rec = record_for(org, title: "Pay rent")
      assert_equal ".+1m", rec["recur"]
      assert_equal "2026-08-01", rec["deadline"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_recur_from_schedule_uses_fixed_prefix
    run_cli("recur", "Pay rent", "2w", "--from", "schedule", content: RECUR_CONTENT) do |org, _out, _err, st|
      assert st.success?
      assert_equal "+2w", record_for(org, title: "Pay rent")["recur"]
    end
  end

  def test_cli_recur_off_clears
    run_cli("recur", "Pay rent", "off",
            content: recur_content(pay_rent_recur: ".+1w")) do |org, _out, _err, st|
      assert st.success?
      rec = record_for(org, title: "Pay rent")
      assert_nil rec["recur"]
      assert_equal "2026-08-01", rec["deadline"]
    end
  end

  def test_cli_recur_undated_task_without_on_exits_1
    run_cli("recur", "Standup", "weekly", content: RECUR_CONTENT) do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/no date/i, err)
    end
  end

  def test_cli_recur_undated_task_with_on_seeds_date
    run_cli("recur", "Standup", "every 2 days", "--on", "2026-09-01", content: RECUR_CONTENT) do |org, _out, _err, st|
      assert st.success?
      rec = record_for(org, title: "Standup notes")
      assert_equal "2026-09-01", rec["deadline"]
      assert_equal ".+2d", rec["recur"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_recur_bad_interval_exits_1
    run_cli("recur", "Pay rent", "bananas", content: RECUR_CONTENT) do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/unrecognized interval/, err)
    end
  end

  def test_cli_recur_no_match_exits_2
    run_cli("recur", "nonexistent", "weekly", content: RECUR_CONTENT) do |_org, _out, err, st|
      assert_equal 2, st.exitstatus
      assert_match(/no match/, err)
    end
  end

  def test_cli_recur_dry_run_writes_nothing
    run_cli("recur", "Pay rent", "weekly", "--dry-run", content: RECUR_CONTENT) do |org, out, _err, st|
      assert st.success?
      assert_match(/would set recurrence \.\+1w/, out)
      assert_equal RECUR_CONTENT, File.read(org)
    end
  end

  def test_cli_recur_json_includes_recur_field
    run_cli("recur", "Pay rent", "weekly", "--json", content: RECUR_CONTENT) do |_org, out, _err, st|
      assert st.success?
      require "json"
      touched = JSON.parse(out).fetch("touched")
      assert_equal ".+1w", touched[0]["recur"]
    end
  end

  def test_cli_done_rolls_recurring_task_forward
    content = recur_content(pay_rent_recur: "+1m")
    run_cli("done", "Pay rent", content: content) do |org, out, _err, st|
      assert st.success?
      assert_match(/↻ Pay rent → next 2026-09-01/, out)
      # still open, not archived away — rolled forward instead of closed
      rec = record_for(org, title: "Pay rent")
      assert_equal "NEXT", rec["state"]
      assert_equal "2026-09-01", rec["deadline"]
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- cascading completion (CLI) ----------------------------------------------

  # A nested project fixture: "Ship release" over two open children.
  CASCADE_CONTENT = Tasks::Format.dump([
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "cccd0001", "title" => "Work" },
    { "type" => "task", "id" => "cccd0002", "parent" => "cccd0001", "state" => "TODO",
      "title" => "Ship release" },
    { "type" => "task", "id" => "cccd0003", "parent" => "cccd0002", "state" => "TODO",
      "title" => "Write notes" },
    { "type" => "task", "id" => "cccd0004", "parent" => "cccd0002", "state" => "NEXT",
      "title" => "Tag build" },
  ])

  def test_cli_done_on_parent_prints_every_cascaded_headline
    run_cli("done", "Ship release", content: CASCADE_CONTENT) do |org, out, _err, st|
      assert st.success?
      assert_match(/DONE.*Ship release/, out)
      assert_match(/DONE.*Write notes/, out)
      assert_match(/DONE.*Tag build/, out)
      assert_equal "DONE", record_for(org, title: "Write notes")["state"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_done_json_touched_array_has_all_entries
    run_cli("done", "Ship release", "--json", content: CASCADE_CONTENT) do |_org, out, _err, st|
      assert st.success?
      require "json"
      touched = JSON.parse(out)["touched"]
      assert_equal 3, touched.size
      assert_equal %w[Ship\ release Write\ notes Tag\ build].map { |t| t.tr("\\", " ") }.sort,
                   touched.map { |t| t["title"] }.sort
      assert(touched.all? { |t| t["state"] == "DONE" })
    end
  end

  def test_cli_done_dry_run_reports_descendant_count
    run_cli("done", "Ship release", "--dry-run", content: CASCADE_CONTENT) do |org, out, _err, st|
      assert st.success?
      assert_equal CASCADE_CONTENT, File.read(org), "dry-run writes nothing"
      assert_match(/would mark DONE/, out)
      assert_match(/would also close 2 open descendants/, out)
    end
  end

  def test_cli_done_recurring_parent_prints_roll_and_does_not_cascade
    content = Tasks::Format.dump([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "cccd0011", "title" => "Work" },
      { "type" => "task", "id" => "cccd0012", "parent" => "cccd0011", "state" => "NEXT",
        "title" => "Weekly sync", "deadline" => "2026-08-01", "recur" => "+1m" },
      { "type" => "task", "id" => "cccd0013", "parent" => "cccd0012", "state" => "TODO",
        "title" => "Prep agenda" },
    ])
    run_cli("done", "Weekly sync", content: content) do |org, out, _err, st|
      assert st.success?
      assert_match(/↻ Weekly sync → next 2026-09-01/, out)
      assert_equal "NEXT", record_for(org, title: "Weekly sync")["state"], "rolled, still open"
      assert_equal "TODO", record_for(org, title: "Prep agenda")["state"], "child not cascaded"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_capture_recur_lands_scheduled_and_repeating
    run_cli("capture", "water plants", "--recur", "weekly", content: RECUR_CONTENT) do |org, _out, _err, st|
      assert st.success?
      rec = record_for(org, title: "water plants")
      assert_equal "TODO", rec["state"]
      assert_match(/\A\d{4}-\d{2}-\d{2}\z/, rec["scheduled"])
      assert_equal ".+1w", rec["recur"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_capture_recurrence_is_one_undoable_transaction
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, RECUR_CONTENT)
      before = File.binread(org)

      _out, err, status = run_cli_at(org, archive, "capture", "water plants", "--recur", "weekly")
      assert status.success?, err
      assert_equal ".+1w", record_for(org, title: "water plants")["recur"]

      undo_out, undo_err, undo_status = run_cli_at(org, archive, "undo")
      assert undo_status.success?, undo_err
      assert_equal "undid: capture: water plants\n", undo_out
      assert_equal before, File.binread(org)

      _out, empty_err, empty_status = run_cli_at(org, archive, "undo")
      assert_equal 1, empty_status.exitstatus
      assert_match(/nothing to undo/, empty_err)
    end
  end

  def test_cli_list_recurring_filters
    content = recur_content(pay_rent_recur: "+1m")
    run_cli("list", "--recurring", content: content) do |_org, out, _err, st|
      assert st.success?
      assert_match(/Pay rent/, out)
      refute_match(/Standup/, out)
    end
  end

  # `state <ref> DONE` rolls a recurring task forward (like `done`); its dry-run
  # and output must reflect that, not claim it will just set the state.
  def test_cli_state_done_is_recurrence_aware
    content = recur_content(pay_rent_recur: "+1m")
    run_cli("state", "Pay rent", "DONE", "--dry-run", content: content) do |org, out, _err, st|
      assert st.success?
      assert_match(/would recur → 2026-09-01/, out)
      assert_equal content, File.read(org)
    end
    run_cli("state", "Pay rent", "DONE", content: content) do |org, out, _err, st|
      assert st.success?
      assert_match(/↻ Pay rent → next 2026-09-01/, out)
      assert_equal "NEXT", record_for(org, title: "Pay rent")["state"]
    end
  end

  # Clearing recurrence from a task that has no date is a harmless no-op, not an
  # error (there is nothing to clear).
  def test_cli_recur_off_on_undated_is_noop_success
    run_cli("recur", "Standup", "off", content: RECUR_CONTENT) do |org, _out, _err, st|
      assert st.success?
      assert_equal RECUR_CONTENT, File.read(org)
    end
  end

  def test_cli_recur_on_closed_task_rejected
    recs = RECUR_RECORDS.map(&:dup)
    recs << { "type" => "task", "id" => "cccc0005", "parent" => "cccc0002",
              "state" => "DONE", "title" => "Filed taxes", "closed" => "2026-04-15" }
    closed = dump_fixture(recs)
    run_cli("recur", "Filed taxes", "weekly", "--include-done", content: closed) do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/can't set recurrence on a DONE task/, err)
    end
  end

  def test_cli_capture_recur_with_done_state_rejected
    run_cli("capture", "x", "--recur", "weekly", "--state", "DONE", content: RECUR_CONTENT) do |_org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/can't set recurrence on a DONE task/, err)
    end
  end

  # C1: `list` over a file with a non-string (integer) id must not crash — the
  # reader coerces the id, so the CLI still renders every task.
  def test_cli_list_survives_a_non_string_id
    bad = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "aaaa0001", "title" => "W" },
      { "type" => "task", "id" => 12345678, "parent" => "aaaa0001", "state" => "NEXT",
        "title" => "Int id task" },
    ])
    run_cli("list", content: bad) do |_org, out, err, st|
      assert st.success?, "list crashed: #{err}"
      assert_match(/Int id task/, out)
    end
  end

  # M4: a mutation on a pre-existing-invalid file writes, fails post-Check, and
  # rolls back. The CLI must point at `check` (not the phantom "changed
  # underfoot"), exit 1, and leave the file untouched.
  def test_cli_mutation_on_invalid_file_hints_at_check
    dup = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "aaaa0001", "title" => "W" },
      { "type" => "task", "id" => "aaaa0002", "parent" => "aaaa0001", "state" => "NEXT",
        "title" => "Alpha" },
      { "type" => "task", "id" => "aaaa0002", "parent" => "aaaa0001", "state" => "NEXT",
        "title" => "Beta" },
    ])
    run_cli("priority", "Alpha", "A", content: dup) do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/tasks check/, err)
      assert_equal dup, File.read(org, encoding: "UTF-8"), "file unchanged after rollback"
    end
  end

  # m5: capturing into an empty TASKS_DIR bootstraps a fresh, valid store (meta
  # line + Inbox section + the task), and undo removes it (deletes the file).
  def test_cli_capture_bootstraps_an_empty_store
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      env = { "TASKS_FILE" => org, "TASKS_ARCHIVE" => archive }

      _out, err, st = Open3.capture3(env, "ruby", BIN, "capture", "brand new task")
      assert st.success?, "capture failed: #{err}"
      recs = Tasks::Format.parse(File.read(org, encoding: "UTF-8")).records
      assert_equal "meta", recs.first["type"]
      assert(recs.any? { |r| r["type"] == "section" && r["title"] == "Inbox" }, "Inbox section seeded")
      assert(recs.any? { |r| r["type"] == "task" && r["title"] == "brand new task" }, "task inserted")
      assert Tasks::Check.check(org).ok?, Tasks::Check.check(org).errors.inspect

      _out2, err2, st2 = Open3.capture3(env, "ruby", BIN, "undo")
      assert st2.success?, "undo failed: #{err2}"
      refute File.exist?(org), "undo deletes the bootstrapped file"
    end
  end

  # -- nesting: capture --under, move --under/--top ---------------------------

  # A small nested fixture: Work > Parent > Child, plus a second top-level task
  # and a Home section, so the CLI nesting paths have somewhere to move things.
  NEST_CLI = Tasks::Format.dump([
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "ffff0001", "title" => "Work" },
    { "type" => "task", "id" => "ffff0002", "parent" => "ffff0001", "state" => "TODO",
      "title" => "Parent task" },
    { "type" => "task", "id" => "ffff0003", "parent" => "ffff0002", "state" => "NEXT",
      "title" => "Child task" },
    { "type" => "task", "id" => "ffff0004", "parent" => "ffff0001", "state" => "TODO",
      "title" => "Other task" },
    { "type" => "section", "id" => "ffff0005", "title" => "Home" },
  ])

  def test_cli_capture_under_happy_path
    run_cli("capture", "draft outline", "--under", "Parent task", content: NEST_CLI) do |org, out, _err, st|
      assert_equal 0, st.exitstatus
      rec = record_for(org, title: "draft outline")
      assert_equal "ffff0002", rec["parent"], "nested under Parent task"
      assert_match(/draft outline/, out)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_capture_under_and_project_conflict_exits_1
    run_cli("capture", "x", "--under", "Parent task", "--project", "Work", content: NEST_CLI) do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/can't combine --under and --project/, err)
      refute_match(/"title":"x"/, File.read(org), "nothing captured")
    end
  end

  def test_cli_capture_under_unknown_ref_exits_2
    run_cli("capture", "x", "--under", "no-such-task", content: NEST_CLI) do |_org, _out, err, st|
      assert_equal 2, st.exitstatus
      assert_match(/no match/, err)
    end
  end

  def test_cli_capture_under_over_cap_exits_1_with_depth_message
    # TASKS_MAX_DEPTH=1: Parent task is depth 1, so a child would be depth 2 > 1.
    run_cli("capture", "x", "--under", "Parent task", content: NEST_CLI,
            env: { "TASKS_MAX_DEPTH" => "1" }) do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/would exceed max depth 1/, err)
      assert_equal NEST_CLI, File.read(org, encoding: "UTF-8"), "nothing written"
    end
  end

  def test_cli_move_under_happy_path
    run_cli("move", "Other task", "--under", "Parent task", content: NEST_CLI) do |org, out, _err, st|
      assert_equal 0, st.exitstatus
      assert_equal "ffff0002", record_for(org, title: "Other task")["parent"]
      assert_match(/Other task/, out)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_move_top_happy_path
    run_cli("move", "Child task", "--top", content: NEST_CLI) do |org, out, _err, st|
      assert_equal 0, st.exitstatus
      assert_equal "ffff0001", record_for(org, title: "Child task")["parent"], "unnested to Work"
      assert_match(/Child task/, out)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_move_two_destinations_exits_1_usage
    run_cli("move", "Child task", "Home", "--under", "Parent task", content: NEST_CLI) do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/usage: tasks move/, err)
      assert_equal NEST_CLI, File.read(org, encoding: "UTF-8"), "nothing written"
    end
  end

  def test_cli_move_under_own_child_exits_1_cycle
    run_cli("move", "Parent task", "--under", "Child task", content: NEST_CLI) do |org, _out, err, st|
      assert_equal 1, st.exitstatus
      assert_match(/can't nest a task under its own subtree/, err)
      assert_equal NEST_CLI, File.read(org, encoding: "UTF-8"), "nothing written"
    end
  end

  # Phase 1: CLI mutations must use stable task ids after ref resolution. The
  # Store no longer exposes the line-addressed mutation protocol, so this
  # guards the adapter boundary against reintroduction.
  def test_cli_mutation_adapter_has_no_legacy_store_calls
    source = File.read(BIN, encoding: "UTF-8")

    refute_match(/store\.(?:capture!|complete!|set_state!|set_priority!|reschedule!|set_date!|undate!|retitle!|set_tags!|set_deferred!|set_recur!|add_note!|move!|move_under!|move_top!)/, source)
  end

  def test_cli_tag_patch_preserves_legacy_tag_order_and_undo_label
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, FIXTURE_ORG)

      _out, err, status = run_cli_at(org, archive, "tag", "Review PR", "+urgent", "-important", "@home")
      assert status.success?, err
      assert_equal %w[@computer urgent @home], record_for(org, title: "Review PR backlog")["tags"]

      out, undo_err, undo_status = run_cli_at(org, archive, "undo")
      assert undo_status.success?, undo_err
      assert_equal "undid: tags: Review PR backlog\n", out
      assert_equal %w[@computer important], record_for(org, title: "Review PR backlog")["tags"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_undate_both_dates_remains_one_undoable_change_with_legacy_label
    records = FIXTURE_RECORDS.map(&:dup)
    flight = records.find { |record| record["id"] == FIX[:flight] }
    flight["scheduled"] = "2026-06-30"
    content = dump_fixture(records)

    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, content)

      _out, err, status = run_cli_at(org, archive, "undate", "Book flight")
      assert status.success?, err
      rec = record_for(org, title: "Book flight in Concur")
      assert_nil rec["scheduled"]
      assert_nil rec["deadline"]

      out, undo_err, undo_status = run_cli_at(org, archive, "undo")
      assert undo_status.success?, undo_err
      assert_equal "undid: remove dates: Book flight in Concur\n", out
      assert_equal content, File.read(org, encoding: "UTF-8")
      _out, empty_err, empty_status = run_cli_at(org, archive, "undo")
      assert_equal 1, empty_status.exitstatus
      assert_match(/nothing to undo/, empty_err)
    end
  end

  def test_cli_patch_adapter_maps_vanished_id_to_stale_while_initial_missing_ref_is_exit_two
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, FIXTURE_ORG)
      env = { "TASKS_FILE" => org, "TASKS_ARCHIVE" => archive }

      script = <<~RUBY
        ARGV.replace(["help"])
        load #{BIN.inspect}
        fake = Object.new
        fake.define_singleton_method(:edit_snapshot) { |_id| nil }
        fake.define_singleton_method(:patch_task!) { |_patch| Tasks::MutationResult.new(status: :not_found) }
        fake.define_singleton_method(:last_rollback) { nil }
        @store = fake
        result = patch_task_by_id("deadbeef", field: :title, value: "replacement", label: "retitle")
        puts "\#{result.status}:\#{result.cli_exit_code}"
      RUBY
      out, err, status = Open3.capture3(env, "ruby", "-e", script)
      assert status.success?, err
      assert_equal "stale:1", out.lines.last.strip

      _out, missing_err, missing_status = Open3.capture3(env, "ruby", BIN, "priority", "does not exist", "A")
      assert_equal 2, missing_status.exitstatus
      assert_match(/no match: does not exist/, missing_err)
    end
  end
end
