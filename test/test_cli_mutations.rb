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
  def run_cli(*args)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      archive = File.join(dir, "archive.org")
      File.write(org, FIXTURE_ORG)
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
    # "NEXT" appears in several titles? No — use a shared substring. Both
    # "Review PR backlog" and "Book flight" share no word, but "the" appears
    # in "Water the plants" and "Travel desk"? Use a real shared token.
    run_cli("priority", "e", "A") do |_org, _out, err, st|
      assert_equal 2, st.exitstatus
      assert_match(/ambiguous/, err)
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
end
