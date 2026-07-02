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

  # -- retitle! ---------------------------------------------------------------

  def test_retitle_replaces_title_only
    with_store do |store, org, _a|
      assert store.retitle!(find_item(store, "Book flight"), "Rebook the flight")
      line = File.readlines(org).find { |l| l.include?("Rebook the flight") }
      assert_match(/^\*\* NEXT \[#A\] Rebook the flight :@computer:important:urgent:$/, line.chomp)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_retitle_preserves_headline_without_tags
    with_store do |store, org, _a|
      assert store.retitle!(find_item(store, "garden"), "prune the roses")
      assert_match(/^\*\* INBOX prune the roses$/, File.readlines(org).find { |l| l.include?("prune") }.chomp)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_retitle_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Book flight").dup
      stale.line = 1
      refute store.retitle!(stale, "nope")
      assert_match(/Book flight in Concur/, File.read(org))
    end
  end

  def test_retitle_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.retitle!(find_item(store, "Book flight"), "Rebook")
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  # -- set_tags! --------------------------------------------------------------

  def test_set_tags_adds_and_removes
    with_store do |store, org, _a|
      pr = find_item(store, "Review PR")
      assert store.set_tags!(pr, add: %w[urgent @home], remove: %w[important])
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
      assert store.set_tags!(pr, add: %w[important])
      assert_equal 1, find_item(store, "Review PR").tags.count("important")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_tags_can_remove_all_leaving_bare_headline
    with_store do |store, org, _a|
      pr = find_item(store, "Review PR")
      assert store.set_tags!(pr, remove: %w[@computer important])
      assert_match(/^\*\* NEXT \[#B\] Review PR backlog$/, File.readlines(org).find { |l| l.include?("Review PR") }.chomp)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_tags_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Review PR").dup
      stale.line = 1
      refute store.set_tags!(stale, add: %w[urgent])
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  # -- add_note! --------------------------------------------------------------

  def test_add_note_appends_body_line
    with_store do |store, org, _a|
      assert store.add_note!(find_item(store, "Review PR"), "ping the reviewers")
      block = store.block(find_item(store, "Review PR"))
      assert_includes block.map(&:strip), "ping the reviewers"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_add_note_lands_within_the_block_not_the_next_task
    with_store do |store, org, _a|
      # garden is the only Inbox item; its note must not bleed into * Work
      assert store.add_note!(find_item(store, "garden"), "north bed")
      lines = File.readlines(org)
      garden = lines.index { |l| l.include?("garden") }
      work = lines.index { |l| l =~ /^\* Work/ }
      note = lines.index { |l| l.include?("north bed") }
      assert garden < note && note < work
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_add_note_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Review PR").dup
      stale.line = 1
      refute store.add_note!(stale, "nope")
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  def test_add_note_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.add_note!(find_item(store, "Review PR"), "a note")
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  # -- move! ------------------------------------------------------------------

  def test_move_relocates_block_under_target_section
    with_store do |store, org, _a|
      line = store.move!(find_item(store, "garden"), "Work")
      assert line.is_a?(Integer) && line.positive?
      lines = File.readlines(org)
      garden = lines.index { |l| l.include?("garden") }
      work = lines.index { |l| l =~ /^\* Work/ }
      home = lines.index { |l| l =~ /^\* Home/ }
      assert work < garden && garden < home, "garden now sits inside Work"
      assert_equal line, garden + 1
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_move_carries_the_whole_block
    with_store do |store, org, _a|
      # self-eval has a SCHEDULED body line that must travel with it
      store.move!(find_item(store, "self-eval"), "Home")
      lines = File.readlines(org)
      idx = lines.index { |l| l.include?("self-eval") }
      assert_match(/SCHEDULED: <2026-07-03/, lines[idx + 1])
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_move_returns_false_for_unknown_section
    with_store do |store, org, _a|
      refute store.move!(find_item(store, "garden"), "Nonexistent")
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  def test_move_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "garden").dup
      stale.line = 99
      refute store.move!(stale, "Work")
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  def test_move_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.move!(find_item(store, "garden"), "Work")
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  # -- capture! ---------------------------------------------------------------

  def test_capture_adds_inbox_item_by_default
    with_store do |store, org, _a|
      line = store.capture!("call the plumber")
      assert line.is_a?(Integer) && line.positive?
      fresh = store.items.find { |i| i.title == "call the plumber" }
      assert_equal "INBOX", fresh.state
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_capture_with_date_lands_as_todo
    with_store do |store, org, _a|
      store.capture!("file taxes", due: Date.new(2026, 7, 20), state: "TODO")
      fresh = store.items.find { |i| i.title == "file taxes" }
      assert_equal "TODO", fresh.state
      assert_equal Date.new(2026, 7, 20), fresh.deadline
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_capture_under_named_project
    with_store do |store, org, _a|
      store.capture!("refactor parser", state: "NEXT", tags: %w[@computer], project: "Work")
      lines = File.readlines(org)
      idx = lines.index { |l| l.include?("refactor parser") }
      work = lines.index { |l| l =~ /^\* Work/ }
      home = lines.index { |l| l =~ /^\* Home/ }
      assert work < idx && idx < home
      assert_match(/^\*\* NEXT refactor parser :@computer:$/, lines[idx].chomp)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_capture_returns_false_for_unknown_project
    with_store do |store, org, _a|
      refute store.capture!("x", project: "Nonexistent")
      assert_equal FIXTURE_ORG, File.read(org)
    end
  end

  def test_capture_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.capture!("something")
      store.undo!
      assert_equal before, File.read(org)
    end
  end

  def test_cli_done_marks_done_with_closed_stamp
    run_cli("done", "Book flight") do |org, out, _err, st|
      assert st.success?
      content = File.read(org)
      assert_match(/^\*\* DONE \[#A\] Book flight/, content)
      assert_match(/CLOSED: \[#{Date.today}\]/, content)
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
        assert_match(/^\*\* DONE \[#A\] Book flight/, File.read(org))
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

  def test_cli_archive_sweeps_to_archive_file
    run_cli("archive") do |org, out, _err, st|
      assert st.success?
      refute_match(/Old finished thing/, File.read(org))
      archive = File.join(File.dirname(org), "archive.org")
      assert_match(/Old finished thing/, File.read(archive))
      assert_match(/Archived 1 item/, out)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_archive_nothing_to_do
    no_done = FIXTURE_ORG.lines.reject.with_index { |l, i|
      l.include?("Old finished") || l.include?("CLOSED: [2026-06-20]")
    }.join
    run_cli("archive", content: no_done) do |_org, out, _err, st|
      assert st.success?
      assert_match(/Nothing to archive/, out)
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

  # -- retitle ----------------------------------------------------------------

  def test_cli_retitle_replaces_title
    run_cli("retitle", "Book flight", "Rebook the flight") do |org, out, _err, st|
      assert st.success?
      assert_match(/^\*\* NEXT \[#A\] Rebook the flight :@computer:important:urgent:/, File.read(org))
      assert_match(/Rebook the flight/, out)
    end
  end

  def test_cli_retitle_alias_rename
    run_cli("rename", "Book flight", "Rebook") do |org, _out, _err, st|
      assert st.success?
      assert_match(/^\*\* NEXT \[#A\] Rebook /, File.read(org))
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
      line = File.readlines(org).find { |l| l.include?("Review PR") }
      assert_match(/urgent/, line)
      assert_match(/@home/, line)
      refute_match(/important/, line)
      assert_match(/Review PR/, out)
    end
  end

  def test_cli_tag_removes_context
    run_cli("tag", "Review PR", "-@computer") do |org, _out, _err, st|
      assert st.success?
      refute_match(/@computer/, File.readlines(org).find { |l| l.include?("Review PR") })
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
      lines = File.readlines(org)
      garden = lines.index { |l| l.include?("garden") }
      work = lines.index { |l| l =~ /^\* Work/ }
      home = lines.index { |l| l =~ /^\* Home/ }
      assert work < garden && garden < home
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
      assert_match(/^\*\* INBOX call the plumber/, File.read(org))
      assert_match(/Captured \[#{Date.today}\]/, File.read(org))
      assert_match(/call the plumber/, out)
    end
  end

  def test_cli_capture_with_flags_lands_processed
    run_cli("capture", "prep board deck", "--due", "2026-07-20",
            "--priority", "A", "--tag", "important", "--context", "@work",
            "--project", "Work") do |org, out, _err, st|
      assert st.success?
      lines = File.readlines(org)
      idx = lines.index { |l| l.include?("prep board deck") }
      work = lines.index { |l| l =~ /^\* Work/ }
      home = lines.index { |l| l =~ /^\* Home/ }
      assert work < idx && idx < home
      assert_match(/^\*\* TODO \[#A\] prep board deck :@work:important:/, lines[idx].chomp)
      assert(lines[idx..(idx + 3)].any? { |l| l =~ /DEADLINE: <2026-07-20/ })
      assert_match(/prep board deck/, out)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cli_capture_explicit_state_overrides_date_default
    run_cli("capture", "waiting on legal", "--due", "2026-07-20", "--state", "WAITING") do |org, _out, _err, st|
      assert st.success?
      assert_match(/^\*\* WAITING waiting on legal/, File.read(org))
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
end
