# frozen_string_literal: true

require_relative "test_helper"

class TestStore < Minitest::Test
  def test_parse_finds_all_items
    with_store do |store, _org, _archive|
      assert_equal 7, store.items.size
      assert_equal %w[INBOX NEXT NEXT TODO WAITING DONE NEXT], store.items.map(&:state)
    end
  end

  def test_parse_reads_priority_tags_and_dates
    with_store do |store, _org, _archive|
      flight = find_item(store, "Book flight")
      assert_equal "A", flight.priority
      assert_equal Date.new(2026, 7, 2), flight.deadline
      assert_includes flight.tags, "@computer"
      assert_includes flight.tags, "important"

      eval = find_item(store, "self-eval")
      assert_equal Date.new(2026, 7, 3), eval.scheduled
      assert_nil eval.deadline
    end
  end

  def test_changed_detects_external_writes
    with_store do |store, org, _archive|
      store.reload!
      refute store.changed?
      future = Time.now + 2
      File.write(org, FIXTURE_ORG + "** TODO added later\n")
      File.utime(future, future, org) # avoid same-mtime flakiness
      assert store.changed?
      assert_equal 8, store.items.size
    end
  end

  def test_complete_marks_done_with_closed_stamp
    with_store do |store, org, _archive|
      flight = find_item(store, "Book flight")
      assert store.complete!(flight)
      text = File.read(org)
      assert_match(/^\*\* DONE \[#A\] Book flight in Concur/, text)
      assert_match(/CLOSED: \[#{Date.today}\]/, text)
      assert_equal "DONE", find_item(store, "Book flight").state
    end
  end

  def test_complete_rejects_stale_line_numbers
    with_store do |store, org, _archive|
      flight = find_item(store, "Book flight")
      stale = flight.dup
      stale.line = 1 # points at "* Inbox", not the flight headline
      refute store.complete!(stale)
      refute_match(/DONE.*Book flight/, File.read(org))
    end
  end

  def test_reschedule_updates_existing_deadline
    with_store do |store, org, _archive|
      flight = find_item(store, "Book flight")
      assert store.reschedule!(flight, Date.new(2026, 7, 10))
      assert_match(/DEADLINE: <2026-07-10 Fri>/, File.read(org))
      refute_match(/2026-07-02/, File.read(org))
      assert_equal Date.new(2026, 7, 10), find_item(store, "Book flight").deadline
    end
  end

  def test_reschedule_updates_scheduled_when_no_deadline
    with_store do |store, org, _archive|
      eval = find_item(store, "self-eval")
      assert store.reschedule!(eval, Date.new(2026, 7, 8))
      assert_match(/SCHEDULED: <2026-07-08 Wed>/, File.read(org))
      # the self-eval block itself must not have gained a DEADLINE
      lines = File.readlines(org)
      idx = lines.index { |l| l.include?("self-eval") }
      refute_includes lines[idx + 1], "DEADLINE"
    end
  end

  def test_reschedule_adds_deadline_when_item_has_no_stamp
    with_store do |store, org, _archive|
      plants = find_item(store, "Water the plants")
      assert store.reschedule!(plants, Date.new(2026, 7, 5))
      assert_equal Date.new(2026, 7, 5), find_item(store, "Water the plants").deadline
      lines = File.readlines(org)
      idx = lines.index { |l| l.include?("Water the plants") }
      assert_match(/DEADLINE: <2026-07-05 Sun>/, lines[idx + 1])
    end
  end

  def test_reschedule_promotes_inbox_item_to_todo
    with_store do |store, org, _a|
      garden = find_item(store, "garden")
      assert_equal "INBOX", garden.state
      assert store.reschedule!(garden, Date.new(2026, 7, 10))
      fresh = find_item(store, "garden")
      assert_equal "TODO", fresh.state
      assert_equal Date.new(2026, 7, 10), fresh.deadline
      assert_match(/^\*\* TODO random thought about the garden/, File.read(org))
    end
  end

  def test_reschedule_does_not_promote_non_inbox_states
    with_store do |store, _o, _a|
      waiting = find_item(store, "Travel desk")
      assert store.reschedule!(waiting, Date.new(2026, 7, 9))
      assert_equal "WAITING", find_item(store, "Travel desk").state
    end
  end

  def test_reschedule_promotion_is_undoable
    with_store do |store, org, _a|
      before = File.read(org)
      store.reschedule!(find_item(store, "garden"), Date.new(2026, 7, 10))
      store.undo!
      assert_equal before, File.read(org)
      assert_equal "INBOX", find_item(store, "garden").state
    end
  end

  def test_reschedule_does_not_touch_next_items_stamp
    with_store do |store, org, _archive|
      # WAITING item has no stamp but is followed by other content;
      # rescheduling it must not modify a different item's stamp.
      waiting = find_item(store, "Travel desk")
      assert store.reschedule!(waiting, Date.new(2026, 7, 9))
      assert_equal Date.new(2026, 7, 2), find_item(store, "Book flight").deadline
      assert_equal Date.new(2026, 7, 9), find_item(store, "Travel desk").deadline
    end
  end

  def test_archive_sweeps_done_blocks
    with_store do |store, org, archive|
      n = store.archive_swept!
      assert_equal 1, n
      refute_match(/Old finished thing/, File.read(org))
      arch = File.read(archive)
      assert_match(/DONE \[#C\] Old finished thing/, arch)
      assert_match(/CLOSED: \[2026-06-20\]/, arch) # block body moves too
      assert_equal 6, store.items.size
    end
  end

  def test_set_priority_replaces_existing_cookie
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      assert store.set_priority!(flight, "B")
      assert_match(/^\*\* NEXT \[#B\] Book flight in Concur :@computer:important:urgent:$/, File.read(org))
      assert_equal "B", find_item(store, "Book flight").priority
    end
  end

  def test_set_priority_adds_cookie_to_unprioritized_item
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      assert store.set_priority!(plants, "C")
      assert_match(/^\*\* NEXT \[#C\] Water the plants/, File.read(org))
    end
  end

  def test_set_priority_nil_removes_cookie
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      assert store.set_priority!(flight, nil)
      assert_match(/^\*\* NEXT Book flight in Concur/, File.read(org))
      assert_nil find_item(store, "Book flight").priority
    end
  end

  def test_set_priority_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Book flight").dup
      stale.line = 1
      refute store.set_priority!(stale, "B")
      assert_match(/\[#A\] Book flight/, File.read(org))
    end
  end

  def test_block_returns_headline_and_body
    with_store do |store, _o, _a|
      waiting = find_item(store, "Travel desk")
      block = store.block(waiting)
      assert_equal 2, block.size
      assert_includes block[0], "WAITING Travel desk reply"
      assert_includes block[1], "Some note line."
    end
  end

  def test_block_stops_at_next_headline
    with_store do |store, _o, _a|
      flight = find_item(store, "Book flight")
      block = store.block(flight)
      assert_equal 2, block.size # headline + DEADLINE stamp
      refute block.any? { |l| l.include?("Review PR") }
    end
  end

  def test_block_rejects_stale_line_numbers
    with_store do |store, _o, _a|
      stale = find_item(store, "Book flight").dup
      stale.line = 1
      assert_empty store.block(stale)
    end
  end

  def test_undo_with_empty_history
    with_store do |store, _o, _a|
      assert_equal [:empty], store.undo!
      assert_equal [:empty], store.redo!
    end
  end

  def test_undo_and_redo_roundtrip_a_complete
    with_store do |store, org, _a|
      before = File.read(org)
      store.complete!(find_item(store, "Book flight"))
      after = File.read(org)

      kind, label = store.undo!
      assert_equal :ok, kind
      assert_includes label, "complete: Book flight"
      assert_equal before, File.read(org)
      assert_equal "NEXT", find_item(store, "Book flight").state

      kind, = store.redo!
      assert_equal :ok, kind
      assert_equal after, File.read(org)
      assert_equal "DONE", find_item(store, "Book flight").state
    end
  end

  def test_undo_stacks_multiple_mutations_in_order
    with_store do |store, org, _a|
      original = File.read(org)
      store.set_priority!(find_item(store, "Book flight"), "B")
      store.reschedule!(find_item(store, "Book flight"), Date.new(2026, 7, 20))
      store.undo! # reschedule
      assert_match(/2026-07-02/, File.read(org))
      assert_match(/\[#B\] Book flight/, File.read(org))
      store.undo! # priority
      assert_equal original, File.read(org)
    end
  end

  def test_new_mutation_clears_redo
    with_store do |store, _o, _a|
      store.set_priority!(find_item(store, "Book flight"), "B")
      store.undo!
      store.set_priority!(find_item(store, "Book flight"), "C")
      assert_equal [:empty], store.redo!
    end
  end

  def test_undo_refuses_after_external_edit
    with_store do |store, org, _a|
      store.complete!(find_item(store, "Book flight"))
      File.write(org, File.read(org) + "** TODO claude added this\n")
      kind, label = store.undo!
      assert_equal :conflict, kind
      assert_includes label, "Book flight"
      assert_match(/claude added this/, File.read(org), "conflict must not clobber the file")
    end
  end

  def test_undo_archive_sweep_restores_both_files
    with_store do |store, org, archive|
      org_before = File.read(org)
      refute File.exist?(archive)
      store.archive_swept!
      assert File.exist?(archive)

      kind, = store.undo!
      assert_equal :ok, kind
      assert_equal org_before, File.read(org)
      refute File.exist?(archive), "archive file created by the sweep is removed"
      assert_equal 7, store.items.size
    end
  end

  def test_failed_mutation_records_no_history
    with_store do |store, _o, _a|
      stale = find_item(store, "Book flight").dup
      stale.line = 1
      refute store.complete!(stale)
      assert_equal [:empty], store.undo!
    end
  end

  def test_undo_history_is_capped
    with_store do |store, _o, _a|
      55.times do |i|
        store.set_priority!(find_item(store, "Book flight"), %w[A B C][i % 3])
      end
      undone = 0
      undone += 1 while store.undo!.first == :ok
      assert_equal Tui::Store::UNDO_LIMIT, undone
    end
  end

  def test_archive_with_nothing_to_do
    with_store do |store, _org, archive|
      store.archive_swept!
      assert_equal 0, store.archive_swept!
      assert_equal 1, File.read(archive).scan(/# Archived/).size
    end
  end

  # -- set_deferred! (backs `defer`/`activate`) -------------------------------

  def test_set_deferred_adds_defer_tag_and_keeps_state
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      assert store.set_deferred!(plants, true)
      assert_match(/^\*\* NEXT Water the plants :@home:defer:$/, File.read(org))
      fresh = find_item(store, "Water the plants")
      assert fresh.deferred?
      assert_equal "NEXT", fresh.state # state is untouched
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_deferred_preserves_existing_tags
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      assert store.set_deferred!(flight, true)
      fresh = find_item(store, "Book flight")
      assert fresh.deferred?
      # the pre-existing contexts/tags survive alongside :defer:
      assert_includes fresh.tags, "@computer"
      assert_includes fresh.tags, "important"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_deferred_false_removes_defer_tag
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      store.set_deferred!(plants, true)
      assert store.set_deferred!(find_item(store, "Water the plants"), false)
      assert_match(/^\*\* NEXT Water the plants :@home:$/, File.read(org))
      refute find_item(store, "Water the plants").deferred?
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_deferred_is_undoable
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      store.set_deferred!(plants, true)
      assert find_item(store, "Water the plants").deferred?
      assert_equal :ok, store.undo!.first
      refute find_item(store, "Water the plants").deferred?
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_deferred_rejects_stale_line_numbers
    with_store do |store, _org, _a|
      stale = find_item(store, "Book flight").dup
      stale.line = 1
      refute store.set_deferred!(stale, true)
    end
  end

  def test_completing_a_deferred_task_strips_the_defer_tag
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      store.set_deferred!(plants, true)
      assert store.complete!(find_item(store, "Water the plants"))
      done = find_item(store, "Water the plants")
      assert_equal "DONE", done.state
      refute done.deferred?, "a completed task must not keep the someday/maybe marker"
      assert_match(/^\*\* DONE Water the plants :@home:$/, File.read(org))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cancelling_a_deferred_task_via_set_state_strips_the_defer_tag
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      store.set_deferred!(plants, true)
      assert store.set_state!(find_item(store, "Water the plants"), "CANCELLED")
      cancelled = find_item(store, "Water the plants")
      assert_equal "CANCELLED", cancelled.state
      refute cancelled.deferred?
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- recurrence ------------------------------------------------------------

  RECUR_ORG = <<~ORG
    * Work
    ** NEXT Pay rent :@home:
       DEADLINE: <2026-08-01 Sat +1m>
    ** TODO Weekly review :@computer:
       SCHEDULED: <2026-06-20 Sat .+1w>
    ** NEXT Plain dated task
       DEADLINE: <2026-07-02 Thu>
    ** TODO No date task
  ORG

  def with_recur_store
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, RECUR_ORG)
      yield Tasks::Store.new(org: org, archive: File.join(dir, "archive.org")), org
    end
  end

  def test_parse_reads_repeater_cookie
    with_recur_store do |store, _org|
      assert_equal "+1m", find_item(store, "Pay rent").recur
      assert find_item(store, "Pay rent").recurring?
      assert_equal ".+1w", find_item(store, "Weekly review").recur
      assert_nil find_item(store, "Plain dated task").recur
      refute find_item(store, "Plain dated task").recurring?
    end
  end

  def test_set_recur_attaches_cookie_preserving_date_and_dayname
    with_recur_store do |store, org|
      assert store.set_recur!(find_item(store, "Plain dated task"), ".+2w")
      assert_match(/DEADLINE: <2026-07-02 Thu \.\+2w>/, File.read(org))
      assert_equal ".+2w", find_item(store, "Plain dated task").recur
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_recur_off_removes_cookie
    with_recur_store do |store, org|
      assert store.set_recur!(find_item(store, "Pay rent"), :off)
      assert_match(/DEADLINE: <2026-08-01 Sat>/, File.read(org))
      refute_match(/\+1m/, File.read(org))
      assert_nil find_item(store, "Pay rent").recur
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_recur_rides_scheduled_when_no_deadline
    with_recur_store do |store, org|
      review = find_item(store, "Weekly review")
      assert store.set_recur!(review, "+3d")
      assert_match(/SCHEDULED: <2026-06-20 Sat \+3d>/, File.read(org))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_recur_on_undated_task_returns_false
    with_recur_store do |store, org|
      before = File.read(org)
      refute store.set_recur!(find_item(store, "No date task"), ".+1w")
      assert_equal before, File.read(org)
    end
  end

  def test_set_recur_rejects_stale_line
    with_recur_store do |store, _org|
      stale = find_item(store, "Plain dated task").dup
      stale.line = 1
      refute store.set_recur!(stale, ".+1w")
    end
  end

  def test_complete_recurring_rolls_forward_and_stays_open
    with_recur_store do |store, org|
      rent = find_item(store, "Pay rent")
      assert store.complete!(rent)
      fresh = find_item(store, "Pay rent")
      assert_equal "NEXT", fresh.state, "recurring task stays open"
      # +1m fixed hop from the stored 2026-08-01
      assert_equal Date.new(2026, 9, 1), fresh.deadline
      assert_equal "+1m", fresh.recur, "cookie is retained"
      refute_match(/CLOSED:/, File.read(org), "recurring completion adds no CLOSED stamp")
      refute_match(/DONE/, File.read(org))
      assert_match(/- Did \[#{Date.today}\]/, File.read(org), "logs the occurrence")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_complete_recurring_from_completion_uses_today
    with_recur_store do |store, org|
      review = find_item(store, "Weekly review") # .+1w
      assert store.complete!(review)
      assert_equal Date.today + 7, find_item(store, "Weekly review").scheduled
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_done_via_set_state_also_rolls_recurring
    with_recur_store do |store, org|
      rent = find_item(store, "Pay rent")
      assert store.set_state!(rent, "DONE")
      assert_equal "NEXT", find_item(store, "Pay rent").state
      assert_equal Date.new(2026, 9, 1), find_item(store, "Pay rent").deadline
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_cancel_recurring_truly_closes
    with_recur_store do |store, org|
      rent = find_item(store, "Pay rent")
      assert store.set_state!(rent, "CANCELLED")
      assert_equal "CANCELLED", find_item(store, "Pay rent").state
      assert_match(/CLOSED:/, File.read(org))
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_complete_non_recurring_still_closes
    with_recur_store do |store, org|
      plain = find_item(store, "Plain dated task")
      assert store.complete!(plain)
      assert_equal "DONE", find_item(store, "Plain dated task").state
      assert_match(/CLOSED: \[#{Date.today}\]/, File.read(org))
    end
  end

  def test_complete_recurring_is_undoable
    with_recur_store do |store, org|
      before = File.read(org)
      store.complete!(find_item(store, "Pay rent"))
      refute_equal before, File.read(org)
      store.undo!
      assert_equal before, File.read(org)
      assert_equal "+1m", find_item(store, "Pay rent").recur
    end
  end

  # A recurring parent's roll-forward must stay within the parent's own lines —
  # a deeper child subheading's stamp must not be touched (parse_file binds a
  # stamp to its immediate headline; the mutation must agree).
  def test_complete_recurring_parent_does_not_touch_child_stamp
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, <<~ORG)
        * W
        ** NEXT Parent
           SCHEDULED: <2026-07-01 Wed +1w>
        *** NEXT Child
           SCHEDULED: <2026-07-02 Thu +1d>
      ORG
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.org"))
      assert store.complete!(store.items.find { |i| i.title == "Parent" })
      body = File.read(org)
      assert_match(/Parent\n   SCHEDULED: <2026-07-08 Wed \+1w>/, body, "parent rolled")
      assert_match(/Child\n   SCHEDULED: <2026-07-02 Thu \+1d>/, body, "child untouched")
      # the completion log sits under the parent, before the child
      assert_match(/\+1w>\n   - Did \[#{Date.today}\]\.\n\*\*\* NEXT Child/, body)
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_recur_on_parent_does_not_attach_to_child
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, <<~ORG)
        * W
        ** NEXT Parent
           SCHEDULED: <2026-07-01 Wed>
        *** NEXT Child
           DEADLINE: <2026-09-01 Tue>
      ORG
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.org"))
      assert store.set_recur!(store.items.find { |i| i.title == "Parent" }, "+1w")
      body = File.read(org)
      assert_match(/Parent\n   SCHEDULED: <2026-07-01 Wed \+1w>/, body)
      assert_match(/DEADLINE: <2026-09-01 Tue>\n/, body, "child DEADLINE untouched")
    end
  end

  # A bracket without an ISO date (an org diary/sexp timestamp) can't be rolled;
  # set_recur! must return false, not crash on Date.parse(nil).
  def test_set_recur_on_dateless_bracket_returns_false
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, "* W\n** NEXT Weird\n   DEADLINE: <%%(diary-float t 4 2)>\n")
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.org"))
      before = File.read(org)
      refute store.set_recur!(store.items.find { |i| i.title == "Weird" }, ".+1w")
      assert_equal before, File.read(org)
    end
  end

  # A hand-edited zero-count cookie (++0d) must not be treated as a repeater —
  # a catch-up roll on a zero interval would never terminate. It parses as a
  # plain date instead, so completing it just closes the task.
  def test_zero_count_cookie_is_not_a_repeater
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, "* W\n** NEXT Rent\n   DEADLINE: <2020-01-01 Wed ++0d>\n")
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.org"))
      rent = store.items.find { |i| i.title == "Rent" }
      refute rent.recurring?, "++0d must not register as recurrence"
      assert store.complete!(rent)
      assert_equal "DONE", store.items.find { |i| i.title == "Rent" }.state
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_set_date_preserves_repeater_cookie
    with_recur_store do |store, org|
      rent = find_item(store, "Pay rent") # +1m
      assert store.set_date!(rent, Date.new(2026, 12, 25), kind: :deadline)
      assert_match(/DEADLINE: <2026-12-25 Fri \+1m>/, File.read(org))
      assert_equal "+1m", find_item(store, "Pay rent").recur
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_reschedule_preserves_repeater_cookie
    with_recur_store do |store, org|
      review = find_item(store, "Weekly review") # SCHEDULED .+1w
      assert store.reschedule!(review, Date.new(2026, 7, 10))
      assert_match(/SCHEDULED: <2026-07-10 Fri \.\+1w>/, File.read(org))
      assert Tasks::Check.check(org).ok?
    end
  end
end
