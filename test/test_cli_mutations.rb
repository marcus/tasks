# frozen_string_literal: true

require_relative "test_helper"

# Store-layer coverage for the CLI's due/state/priority mutations, plus
# end-to-end CLI tests (arg parsing, ref resolution, exit codes) that shell
# out to bin/tasks against a sandbox copy via TASKS_ORG/TASKS_ARCHIVE.
class TestCliMutations < Minitest::Test
  # -- set_date! (backs `due`) ------------------------------------------------

  def test_set_date_replaces_existing_deadline
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      assert store.set_date!(flight, Date.new(2026, 7, 15), kind: :deadline)
      assert_match(/DEADLINE: <2026-07-15 Wed>/, File.read(org))
      refute_match(/2026-07-02/, File.read(org))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_date_adds_deadline_when_item_has_none
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      assert store.set_date!(plants, Date.new(2026, 7, 5), kind: :deadline)
      assert_equal Date.new(2026, 7, 5), find_item(store, "Water the plants").deadline
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_date_deadline_ignores_existing_scheduled
    with_store do |store, org, _a|
      # self-eval has a SCHEDULED but no DEADLINE — `due` must add a DEADLINE,
      # leaving the SCHEDULED stamp intact.
      eval = find_item(store, "self-eval")
      assert store.set_date!(eval, Date.new(2026, 7, 20), kind: :deadline)
      fresh = find_item(store, "self-eval")
      assert_equal Date.new(2026, 7, 20), fresh.deadline
      assert_equal Date.new(2026, 7, 3), fresh.scheduled
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_date_promotes_inbox_to_todo
    with_store do |store, org, _a|
      garden = find_item(store, "garden")
      assert store.set_date!(garden, Date.new(2026, 7, 10), kind: :deadline)
      assert_equal "TODO", find_item(store, "garden").state
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_date_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Book flight").dup
      stale.line = 1
      refute store.set_date!(stale, Date.new(2026, 7, 15), kind: :deadline)
      assert_match(/2026-07-02/, File.read(org))
    end
  end

  def test_set_date_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.set_date!(find_item(store, "Book flight"), Date.new(2026, 7, 15), kind: :deadline)
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  # -- set_date! (backs `schedule`) --------------------------------------------

  def test_set_date_scheduled_kind_sets_scheduled_not_deadline
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      assert store.set_date!(plants, Date.new(2026, 7, 20), kind: :scheduled)
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
      store.set_date!(flight, Date.new(2026, 7, 20), kind: :scheduled)
      assert store.undate!(find_item(store, "Book flight"))
      fresh = find_item(store, "Book flight")
      assert_nil fresh.deadline
      assert_nil fresh.scheduled
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_undate_removes_specific_kind_only
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      store.set_date!(flight, Date.new(2026, 7, 20), kind: :scheduled)
      assert store.undate!(find_item(store, "Book flight"), kind: :deadline)
      fresh = find_item(store, "Book flight")
      assert_nil fresh.deadline
      assert_equal Date.new(2026, 7, 20), fresh.scheduled
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_undate_returns_false_when_no_matching_stamp
    with_store do |store, org, _a|
      pr = find_item(store, "Review PR backlog")
      refute store.undate!(pr)
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  def test_undate_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Book flight").dup
      stale.line = 1
      refute store.undate!(stale)
      assert_match(/DEADLINE: <2026-07-02/, File.read(org))
    end
  end

  def test_undate_never_deletes_prose_mentioning_a_stamp_keyword
    with_store do |store, org, _a|
      # a body note that mentions "DEADLINE:" mid-sentence must survive
      lines = File.readlines(org)
      idx = lines.index { |l| l.include?("DEADLINE: <2026-07-02") }
      lines.insert(idx + 1, "   Waiting on the DEADLINE: confirmation from legal.\n")
      File.write(org, lines.join)
      store.reload!

      assert store.undate!(find_item(store, "Book flight"), kind: :deadline)
      content = File.read(org)
      refute_match(/DEADLINE: <2026-07-02/, content)
      assert_match(/Waiting on the DEADLINE: confirmation/, content)
    end
  end

  def test_undate_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.undate!(find_item(store, "Book flight"))
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  # -- set_state! (backs `state`) ---------------------------------------------

  def test_set_state_open_to_open
    with_store do |store, org, _a|
      assert store.set_state!(find_item(store, "Review PR"), "WAITING")
      assert_equal "WAITING", find_item(store, "Review PR").state
      assert_match(/^\*\* WAITING \[#B\] Review PR backlog/, File.read(org))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_state_entering_done_adds_closed_stamp
    with_store do |store, org, _a|
      assert store.set_state!(find_item(store, "Review PR"), "DONE")
      lines = File.readlines(org)
      idx = lines.index { |l| l.include?("Review PR") }
      assert_match(/CLOSED: \[#{Date.today}\]/, lines[idx + 1])
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_state_cancelled_adds_closed_stamp
    with_store do |store, org, _a|
      assert store.set_state!(find_item(store, "Travel desk"), "CANCELLED")
      assert_match(/^\*\* CANCELLED Travel desk reply/, File.read(org))
      idx = File.readlines(org).index { |l| l.include?("Travel desk") }
      assert_match(/CLOSED: \[#{Date.today}\]/, File.readlines(org)[idx + 1])
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_state_leaving_done_removes_closed_stamp
    with_store do |store, org, _a|
      done = store.items.find { |i| i.title.include?("Old finished") }
      assert store.set_state!(done, "TODO")
      assert_equal "TODO", store.items.find { |i| i.title.include?("Old finished") }.state
      refute_match(/CLOSED: \[2026-06-20\]/, File.read(org))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_state_done_to_cancelled_keeps_single_closed_stamp
    with_store do |store, org, _a|
      done = store.items.find { |i| i.title.include?("Old finished") }
      assert store.set_state!(done, "CANCELLED")
      assert_equal 1, File.read(org).scan(/CLOSED:/).size
      assert_match(/^\*\* CANCELLED \[#C\] Old finished/, File.read(org))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_state_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Review PR").dup
      stale.line = 1
      refute store.set_state!(stale, "DONE")
      assert_match(/^\*\* NEXT \[#B\] Review PR/, File.read(org))
    end
  end

  def test_set_state_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.set_state!(find_item(store, "Review PR"), "DONE")
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  # -- CLI end-to-end (shell out to bin/tasks) --------------------------------

  BIN = File.expand_path("../bin/tasks", __dir__)

  # Run bin/tasks in a sandbox; returns [stdout, stderr, status].
  def run_cli(*args, content: FIXTURE_ORG)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      archive = File.join(dir, "archive.org")
      File.write(org, content)
      env = { "TASKS_ORG" => org, "TASKS_ARCHIVE" => archive }
      require "open3"
      out, err, st = Open3.capture3(env, "ruby", BIN, *args)
      yield org, out, err, st
    end
  end

  def test_cli_due_sets_deadline
    run_cli("due", "Book flight", "2026-07-15") do |org, out, _err, st|
      assert st.success?
      assert_match(/DEADLINE: <2026-07-15/, File.read(org))
      assert_match(/DONE|NEXT/, out) # prints the resulting headline
    end
  end

  def test_cli_priority_clears_with_none
    run_cli("priority", "Book flight", "none") do |org, out, _err, st|
      assert st.success?
      assert_match(/^\*\* NEXT Book flight/, File.read(org))
      assert_match(/Book flight/, out)
    end
  end

  def test_cli_state_reopens_done_item
    run_cli("state", "Old finished", "TODO") do |org, out, _err, st|
      assert st.success?
      assert_match(/^\*\* TODO \[#C\] Old finished/, File.read(org))
      refute_match(/CLOSED:/, File.read(org))
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
    dup_org = "* W\n** TODO pay the bill :@home:\n** NEXT pay the bill :@computer:\n"
    # L3 targets the NEXT copy; the reported headline must be that one
    run_cli("priority", "L3", "A", content: dup_org) do |org, out, _err, st|
      assert st.success?
      assert_match(/NEXT \[#A\] pay the bill/, out)
      assert_match(/^\*\* TODO pay the bill/, File.read(org), "TODO copy untouched")
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
      assert_match(/SCHEDULED: <2026-07-20/, File.read(org))
      assert_match(/DEADLINE: <2026-07-02/, File.read(org), "existing deadline untouched")
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
      assert_match(/^\*\* CANCELLED \[#B\] Review PR backlog/, File.read(org))
      idx = File.readlines(org).index { |l| l.include?("Review PR") }
      assert_match(/CLOSED: \[#{Date.today}\]/, File.readlines(org)[idx + 1])
      assert_match(/CANCELLED/, out)
    end
  end

  def test_cli_cancel_alias_drop
    run_cli("drop", "Review PR") do |org, _out, _err, st|
      assert st.success?
      assert_match(/^\*\* CANCELLED \[#B\] Review PR backlog/, File.read(org))
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
end
